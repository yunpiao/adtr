package messages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type bulkCandidate struct {
	tenant    string
	user      int64
	operation string
}

// Selection commits independently of execution: a poisoned or blocked actor
// cannot monopolize the head. A fixed upper bound forces a finite round even
// when higher queue IDs keep arriving. No identity locks are taken here.
func (s *Store) nextBulk(ctx context.Context) (bulkCandidate, error) {
	var candidate bulkCandidate
	err := s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var after, upper int64
		if err := tx.QueryRow(ctx, `SELECT after_id,upper_id FROM adtr.message_bulk_cursor WHERE singleton=true FOR UPDATE`).Scan(&after, &upper); err != nil {
			return err
		}
		if after >= upper {
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(queue_id),0) FROM adtr.message_bulk_operations`).Scan(&upper); err != nil {
				return err
			}
			after = 0
		}
		var queue int64
		err := tx.QueryRow(ctx, `SELECT queue_id,tenant_id,user_id,operation_id FROM adtr.message_bulk_operations WHERE state IN ('queued','running') AND queue_id>$1 AND queue_id<=$2 ORDER BY queue_id LIMIT 1`, after, upper).Scan(&queue, &candidate.tenant, &candidate.user, &candidate.operation)
		if errors.Is(err, pgx.ErrNoRows) {
			queue = upper
			candidate = bulkCandidate{}
		} else if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE adtr.message_bulk_cursor SET after_id=$1,upper_id=$2 WHERE singleton=true`, queue, upper)
		return err
	})
	return candidate, err
}

// BulkOnce advances at most one bounded transaction. The authorizer must be
// the live database authority, retaining its tenant and actor locks to commit.
// A nil authorizer fails closed; no stored epoch or expired snapshot grants work.
func (s *Store) BulkOnce(ctx context.Context, authorize tasks.Authorizer) error {
	if authorize == nil {
		return errors.New("message bulk authorizer required")
	}
	candidate, err := s.nextBulk(ctx)
	if err != nil || candidate.operation == "" {
		return err
	}
	return s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		revision, authErr := authorize(ctx, tx, tasks.Principal{TenantID: candidate.tenant, ActorID: candidate.user}, tasks.Scope{DomainID: "platform", TaskName: "infrastructure.health", Platform: true}, tasks.Read)
		if authErr != nil && !errors.Is(authErr, tasks.ErrAuthorization) {
			return authErr
		}
		// Re-read after live identity locks; candidate selection is never authority.
		id := Identity{TenantID: candidate.tenant, UserID: candidate.user}
		op, _, epoch, err := bulkLoad(ctx, tx, id, candidate.operation, true)
		if err != nil {
			return err
		}
		if op.State != "queued" && op.State != "running" {
			return nil
		}
		if authErr != nil || revision != strconv.FormatInt(epoch, 10) {
			_, err = tx.Exec(ctx, `UPDATE adtr.message_bulk_operations SET state='authorization_blocked',updated_at=clock_timestamp() WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, id.TenantID, id.UserID, candidate.operation)
			return err
		}
		rows, err := tx.Query(ctx, `SELECT ordinal,message_id FROM adtr.message_bulk_targets WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3 AND ordinal>=$4 ORDER BY ordinal LIMIT 50`, id.TenantID, id.UserID, candidate.operation, op.Progress.Processed)
		if err != nil {
			return err
		}
		type target struct {
			ordinal int
			message string
		}
		batch := make([]target, 0, BulkBatch)
		for rows.Next() {
			var t target
			if err = rows.Scan(&t.ordinal, &t.message); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		expected := min(BulkBatch, op.Progress.Remaining)
		if len(batch) != expected {
			return problem(500, "invalid_stored_bulk_operation")
		}
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		newly := 0
		for index, target := range batch {
			if target.ordinal != op.Progress.Processed+index {
				return problem(500, "invalid_stored_bulk_operation")
			}
			var first time.Time
			err = tx.QueryRow(ctx, `INSERT INTO adtr.message_reads(tenant_id,user_id,message_id,first_read_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING first_read_at`, id.TenantID, id.UserID, target.message, now).Scan(&first)
			fresh := err == nil
			if errors.Is(err, pgx.ErrNoRows) {
				err = tx.QueryRow(ctx, `SELECT first_read_at FROM adtr.message_reads WHERE tenant_id=$1 AND user_id=$2 AND message_id=$3`, id.TenantID, id.UserID, target.message).Scan(&first)
			}
			if err != nil {
				return err
			}
			if fresh {
				newly++
			}
			if _, err = tx.Exec(ctx, `INSERT INTO adtr.message_bulk_receipts(tenant_id,user_id,operation_id,ordinal,first_read_at,committed_at,newly_read) VALUES($1,$2,$3,$4,$5,$6,$7)`, id.TenantID, id.UserID, candidate.operation, target.ordinal, first, now, fresh); err != nil {
				return err
			}
		}
		state := "running"
		if len(batch) == op.Progress.Remaining {
			state = "completed"
		}
		_, err = tx.Exec(ctx, `UPDATE adtr.message_bulk_operations SET processed=processed+$4,newly_read=newly_read+$5,already_read=already_read+$6,state=$7,updated_at=$8 WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, id.TenantID, id.UserID, candidate.operation, len(batch), newly, len(batch)-newly, state, now)
		return err
	})
}
func (s *Store) RunBulk(ctx context.Context, authorize tasks.Authorizer) error {
	if authorize == nil {
		return errors.New("message bulk authorizer required")
	}
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.BulkOnce(ctx, authorize); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "message bulk cycle failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
