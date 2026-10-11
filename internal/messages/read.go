package messages

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
	"strconv"
	"time"
)

func readReceipt(ctx context.Context, tx pgx.Tx, id Identity, operationID string) (ReadReceipt, error) {
	var out ReadReceipt
	var newly bool
	err := tx.QueryRow(ctx, `SELECT operation_id,result_id,message_id,first_read_at,committed_at,newly_read FROM adtr.message_read_operations WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, id.TenantID, id.UserID, operationID).Scan(&out.OperationID, &out.ResultID, &out.MessageID, &out.FirstReadAt, &out.CommittedAt, &newly)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReadReceipt{}, problem(404, "not_found")
	}
	if err != nil {
		return ReadReceipt{}, err
	}
	out.FirstReadAt = out.FirstReadAt.UTC()
	out.CommittedAt = out.CommittedAt.UTC()
	out.Progress = Progress{Target: 1, Processed: 1}
	if newly {
		out.Progress.NewlyRead = 1
	} else {
		out.Progress.AlreadyRead = 1
	}
	return out, nil
}
func (s *Store) ReadOperationTx(ctx context.Context, tx pgx.Tx, id Identity, operationID string) (ReadReceipt, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return ReadReceipt{}, err
	}
	if !identifier.MatchString(operationID) {
		return ReadReceipt{}, problem(400, "invalid_operation")
	}
	// The adapter rechecks the CURRENT session and permission. A historical
	// receipt is deliberately independent of its expired/deleted old snapshot.
	return readReceipt(ctx, tx, id, operationID)
}
func (s *Store) MarkReadTx(ctx context.Context, tx pgx.Tx, id Identity, resultID, messageID, operationID string, now time.Time) (ReadReceipt, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return ReadReceipt{}, err
	}
	if !identifier.MatchString(operationID) || !identifier.MatchString(resultID) || !identifier.MatchString(messageID) || now.IsZero() {
		return ReadReceipt{}, problem(400, "invalid_input")
	}
	// Serialize this identity/operation before checking a receipt, including
	// callers in separate processes. Never acquire an identity lock afterward.
	key := "adtr-message-read:" + id.TenantID + ":" + strconv.FormatInt(id.UserID, 10) + ":" + operationID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return ReadReceipt{}, err
	}
	previous, err := readReceipt(ctx, tx, id, operationID)
	if err == nil {
		if previous.ResultID != resultID || previous.MessageID != messageID {
			return ReadReceipt{}, problem(409, "operation_conflict")
		}
		return previous, nil
	}
	var missing *Error
	if !errors.As(err, &missing) || missing.Status != 404 {
		return ReadReceipt{}, err
	}
	v, err := s.loadTx(ctx, tx, id, resultID, now)
	if err != nil {
		return ReadReceipt{}, err
	}
	current := messagecore.ReadState{TenantID: id.TenantID, UserID: strconv.FormatInt(id.UserID, 10), FirstReadAt: map[string]time.Time{}}
	var first time.Time
	err = tx.QueryRow(ctx, `SELECT first_read_at FROM adtr.message_reads WHERE tenant_id=$1 AND user_id=$2 AND message_id=$3`, id.TenantID, id.UserID, messageID).Scan(&first)
	if err == nil {
		current.FirstReadAt[messageID] = first
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ReadReceipt{}, err
	}
	// Pure validation rejects out-of-result IDs before any persistent write.
	if _, err = v.core.MarkRead(current, []string{messageID}, now); err != nil {
		return ReadReceipt{}, coreError(err)
	}
	now = now.UTC().Truncate(time.Microsecond)
	err = tx.QueryRow(ctx, `INSERT INTO adtr.message_reads(tenant_id,user_id,message_id,first_read_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING first_read_at`, id.TenantID, id.UserID, messageID, now).Scan(&first)
	newly := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT first_read_at FROM adtr.message_reads WHERE tenant_id=$1 AND user_id=$2 AND message_id=$3`, id.TenantID, id.UserID, messageID).Scan(&first)
	}
	if err != nil {
		return ReadReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.message_read_operations(tenant_id,user_id,operation_id,result_id,message_id,first_read_at,committed_at,newly_read) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id.TenantID, id.UserID, operationID, resultID, messageID, first, now, newly); err != nil {
		return ReadReceipt{}, err
	}
	return readReceipt(ctx, tx, id, operationID)
}
