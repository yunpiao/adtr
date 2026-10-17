package messages

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"os"
	"time"
)

// ProjectOnce performs one bounded cyclic candidate scan. Its cursor is a
// sweep position, never an acknowledgement that lower event IDs are complete.
// A low ID committed late is found after wraparound; latency grows with history.
func (s *Store) ProjectOnce(ctx context.Context) error {
	return s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var after, upper int64
		if err := tx.QueryRow(ctx, `SELECT after_id,upper_id FROM adtr.message_projection_cursor WHERE singleton=true FOR UPDATE`).Scan(&after, &upper); err != nil {
			return err
		}
		// Freeze the currently visible tail for one sweep. New arrivals cannot
		// indefinitely postpone wraparound and strand a late low-ID commit.
		if after >= upper {
			after = 0
			if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT id FROM adtr.task_outbox ORDER BY id DESC LIMIT 1),0)`).Scan(&upper); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT id FROM adtr.task_outbox WHERE id>$1 AND id<=$2 ORDER BY id LIMIT 200`, after, upper)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			_, err = tx.Exec(ctx, `UPDATE adtr.message_projection_cursor SET after_id=$1,upper_id=$1 WHERE singleton=true`, upper)
			return err
		}
		// Only these at-most-200 indexed candidates are joined and deduplicated.
		// There is no unbounded anti-join across already projected history.
		_, err = tx.Exec(ctx, `INSERT INTO adtr.messages(message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version,message_type,sub_type,title,description,occurred_at)
  SELECT 'health-'||e.id::text,t.tenant_id,t.actor_id,t.task_id,e.id,e.result_version,'task_result',e.state,
    'Infrastructure health task '||e.state,
    CASE e.state WHEN 'succeeded' THEN 'The infrastructure health task completed successfully.'
    WHEN 'cancelled' THEN 'The infrastructure health task was cancelled.'
    WHEN 'partial_failed' THEN 'The infrastructure health task completed with partial failure.'
    WHEN 'dead_letter' THEN 'The infrastructure health task exhausted its retry policy.'
    ELSE 'The infrastructure health task failed.' END,e.occurred_at
  FROM adtr.task_outbox o JOIN adtr.task_events e ON e.id=o.event_id AND e.task_id=o.task_id
  JOIN adtr.tasks t ON t.task_id=e.task_id AND t.tenant_id=e.tenant_id AND t.domain_id=e.domain_id
  WHERE o.id=ANY($1::bigint[]) AND t.kind='infrastructure.health' AND t.domain_id='platform'
    AND e.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled')
    AND e.action IN ('finished','cancel_requested','authorization_rejected','attempts_exhausted','lease_recovered','kind_rejected')
    AND NOT EXISTS(SELECT FROM adtr.messages m WHERE m.source_event_id=e.id)
  ON CONFLICT DO NOTHING`, ids)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE adtr.message_projection_cursor SET after_id=$1,upper_id=$2 WHERE singleton=true`, ids[len(ids)-1], upper)
		return err
	})
}

// CleanupOnce removes only a bounded batch of expired, disposable snapshots.
// Durable messages, read times and operation receipts have no implicit deletion.
func (s *Store) CleanupOnce(ctx context.Context) error {
	return s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `WITH expired AS (SELECT result_id FROM adtr.message_results WHERE expires_at<=clock_timestamp() ORDER BY expires_at,result_id LIMIT 200 FOR UPDATE SKIP LOCKED)
  DELETE FROM adtr.message_results r USING expired e WHERE r.result_id=e.result_id`)
		return err
	})
}
func (s *Store) transaction(parent context.Context, work func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(parent, CycleTimeout)
	defer cancel()
	conn, err := dbadmission.ConnectConfig(ctx, s.database)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), CycleTimeout)
		defer stop()
		_ = conn.Close(closeCtx)
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, stop := context.WithTimeout(context.Background(), CycleTimeout)
		defer stop()
		_ = tx.Rollback(rollbackCtx)
	}()
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
		return err
	}
	if err = s.CheckSchemaTx(ctx, tx); err != nil {
		return err
	}
	if err = work(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Run retries ordinary cycle failures while the caller is alive. Diagnostics
// are static: connection strings, SQL parameters and private data never log.
// Cancellation stops new work; transactions retain their bounded context.
func (s *Store) Run(ctx context.Context) error {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.ProjectOnce(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "message projection cycle failed")
		}
		if ctx.Err() != nil {
			return nil
		}
		if err := s.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "message snapshot cleanup cycle failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
