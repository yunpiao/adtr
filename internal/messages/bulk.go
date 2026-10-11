package messages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	BulkBatch     = 50
	MaxUserBulk   = 4
	MaxTenantBulk = 32
	MaxGlobalBulk = 256
)

type BulkRequest struct {
	ResultID    string   `json:"resultId"`
	OperationID string   `json:"operationId"`
	Selection   string   `json:"selection"`
	MessageIDs  []string `json:"messageIds,omitempty"`
}
type BulkReceipt struct {
	OperationID string    `json:"operationId"`
	ResultID    string    `json:"resultId"`
	Selection   string    `json:"selection"`
	AcceptedAt  time.Time `json:"acceptedAt"`
	Target      int       `json:"target"`
}
type BulkOperation struct {
	Receipt   BulkReceipt `json:"receipt"`
	State     string      `json:"state"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Progress  Progress    `json:"progress"`
}

func bulkHash(in BulkRequest) (string, error) {
	if !identifier.MatchString(in.ResultID) || !identifier.MatchString(in.OperationID) || (in.Selection != "selected" && in.Selection != "all") {
		return "", problem(400, "invalid_bulk_input")
	}
	if in.Selection == "all" && in.MessageIDs != nil || in.Selection == "selected" && (len(in.MessageIDs) == 0 || len(in.MessageIDs) > MaxCandidates) {
		return "", problem(400, "invalid_bulk_input")
	}
	ids := append([]string(nil), in.MessageIDs...)
	sort.Strings(ids)
	for i, id := range ids {
		if !identifier.MatchString(id) || i > 0 && ids[i-1] == id {
			return "", problem(400, "invalid_bulk_input")
		}
	}
	raw, _ := json.Marshal(struct {
		ResultID, Selection string
		IDs                 []string
	}{in.ResultID, in.Selection, ids})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func bulkLoad(ctx context.Context, tx pgx.Tx, id Identity, operationID string, lock bool) (BulkOperation, string, int64, error) {
	var out BulkOperation
	var hash string
	var epoch int64
	query := `SELECT operation_id,result_id,selection,accepted_at,target,state,updated_at,processed,newly_read,already_read,request_hash,authorization_version FROM adtr.message_bulk_operations WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, query, id.TenantID, id.UserID, operationID).Scan(&out.Receipt.OperationID, &out.Receipt.ResultID, &out.Receipt.Selection, &out.Receipt.AcceptedAt, &out.Receipt.Target, &out.State, &out.UpdatedAt, &out.Progress.Processed, &out.Progress.NewlyRead, &out.Progress.AlreadyRead, &hash, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, "", 0, problem(404, "not_found")
	}
	out.Receipt.AcceptedAt = out.Receipt.AcceptedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	out.Progress.Target = out.Receipt.Target
	out.Progress.Remaining = out.Progress.Target - out.Progress.Processed
	return out, hash, epoch, err
}
func (s *Store) BulkOperationTx(ctx context.Context, tx pgx.Tx, id Identity, operationID string) (BulkOperation, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return BulkOperation{}, err
	}
	if !identifier.MatchString(operationID) {
		return BulkOperation{}, problem(400, "invalid_operation")
	}
	out, _, _, err := bulkLoad(ctx, tx, id, operationID, false)
	return out, err
}
func (s *Store) SubmitBulkTx(ctx context.Context, tx pgx.Tx, id Identity, in BulkRequest, now time.Time) (BulkOperation, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return BulkOperation{}, err
	}
	hash, err := bulkHash(in)
	if err != nil {
		return BulkOperation{}, err
	}
	if now.IsZero() {
		return BulkOperation{}, problem(400, "invalid_input")
	}
	// HTTP holds live actor locks before this operation key; workers never take it.
	key := "adtr-message-bulk:" + id.TenantID + ":" + strconv.FormatInt(id.UserID, 10) + ":" + in.OperationID
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return BulkOperation{}, err
	}
	old, oldHash, _, err := bulkLoad(ctx, tx, id, in.OperationID, false)
	if err == nil {
		if oldHash != hash {
			return BulkOperation{}, problem(409, "operation_conflict")
		}
		return old, nil
	}
	var missing *Error
	if !errors.As(err, &missing) || missing.Status != 404 {
		return BulkOperation{}, err
	}
	v, err := s.loadTx(ctx, tx, id, in.ResultID, now)
	if err != nil {
		return BulkOperation{}, err
	}
	members := v.core.IDs()
	wanted := map[string]bool{}
	for _, m := range in.MessageIDs {
		wanted[m] = true
	}
	targets := make([]string, 0, len(members))
	for _, m := range members {
		if in.Selection == "all" || wanted[m] {
			targets = append(targets, m)
			delete(wanted, m)
		}
	}
	if len(wanted) > 0 {
		return BulkOperation{}, problem(400, "target_not_in_result")
	}
	if len(targets) > MaxCandidates {
		return BulkOperation{}, problem(422, "budget_exceeded")
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734192851::bigint)`); err != nil {
		return BulkOperation{}, err
	}
	var user, tenant, global int
	if err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM (SELECT 1 FROM adtr.message_bulk_operations WHERE tenant_id=$1 AND user_id=$2 AND state IN ('queued','running') LIMIT 4) a),
 (SELECT count(*) FROM (SELECT 1 FROM adtr.message_bulk_operations WHERE tenant_id=$1 AND state IN ('queued','running') LIMIT 32) b),
 (SELECT count(*) FROM (SELECT 1 FROM adtr.message_bulk_operations WHERE state IN ('queued','running') LIMIT 256) c)`, id.TenantID, id.UserID).Scan(&user, &tenant, &global); err != nil {
		return BulkOperation{}, err
	}
	if len(targets) > 0 && (user >= MaxUserBulk || tenant >= MaxTenantBulk || global >= MaxGlobalBulk) {
		return BulkOperation{}, problem(429, "bulk_quota_exceeded")
	}
	state := "queued"
	if len(targets) == 0 {
		state = "completed"
	}
	now = now.UTC().Truncate(time.Microsecond)
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.message_bulk_operations(tenant_id,user_id,operation_id,result_id,selection,request_hash,authorization_version,accepted_at,updated_at,state,target) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,$10)`, id.TenantID, id.UserID, in.OperationID, in.ResultID, in.Selection, hash, id.AuthorizationVersion, now, state, len(targets)); err != nil {
		return BulkOperation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.message_bulk_targets(tenant_id,user_id,operation_id,ordinal,message_id) SELECT $1,$2,$3,(ordinality-1)::integer,message_id FROM unnest($4::text[]) WITH ORDINALITY AS target(message_id,ordinality)`, id.TenantID, id.UserID, in.OperationID, targets); err != nil {
		return BulkOperation{}, err
	}
	out, _, _, err := bulkLoad(ctx, tx, id, in.OperationID, false)
	return out, err
}
func (s *Store) CancelBulkTx(ctx context.Context, tx pgx.Tx, id Identity, operationID string, now time.Time) (BulkOperation, error) {
	if err := s.requireIdentity(ctx, tx, id); err != nil {
		return BulkOperation{}, err
	}
	if !identifier.MatchString(operationID) || now.IsZero() {
		return BulkOperation{}, problem(400, "invalid_input")
	}
	out, _, _, err := bulkLoad(ctx, tx, id, operationID, true)
	if err != nil {
		return out, err
	}
	if out.State == "queued" || out.State == "running" {
		if _, err = tx.Exec(ctx, `UPDATE adtr.message_bulk_operations SET state='cancelled',updated_at=$4 WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, id.TenantID, id.UserID, operationID, now.UTC()); err != nil {
			return BulkOperation{}, err
		}
		out, _, _, err = bulkLoad(ctx, tx, id, operationID, false)
	}
	return out, err
}
