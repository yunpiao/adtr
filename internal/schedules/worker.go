package schedules

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

const PollInterval = time.Second
const CandidateLimit = 32
const operationTimeout = 5 * time.Second

func closeConnection(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

type candidate struct {
	id        string
	principal tasks.Principal
}

// Tick discovers at most 32 candidates without row locks. Each candidate then
// uses its own bounded, lock-ordered transaction. No scheduler reservation lease
// survives a commit: task/cursor/occurrence/event are one atomic decision.
func (e *Engine) Tick(ctx context.Context) (int, error) {
	candidates, err := e.candidates(ctx)
	if err != nil {
		return 0, err
	}
	admitted := 0
	var failures []error
	for _, c := range candidates {
		if ctx.Err() != nil {
			return admitted, ctx.Err()
		}
		ok, err := e.admit(ctx, c)
		if err != nil {
			failures = append(failures, err)
		} else if ok {
			admitted++
		}
	}
	return admitted, errors.Join(failures...)
}
func (e *Engine) candidates(parent context.Context) ([]candidate, error) {
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, e.database.Copy())
	if err != nil {
		return nil, err
	}
	defer closeConnection(conn)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if err = e.CheckSchemaTx(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT schedule_id,tenant_id,actor_id FROM adtr.task_schedules WHERE state='enabled' AND next_at<=clock_timestamp() ORDER BY next_at,schedule_id LIMIT $1`, CandidateLimit)
	if err != nil {
		return nil, err
	}
	list := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.principal.TenantID, &c.principal.ActorID); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return list, nil
}

func (e *Engine) admit(parent context.Context, c candidate) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, e.database.Copy())
	if err != nil {
		return false, err
	}
	defer closeConnection(conn)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	if err = e.CheckSchemaTx(ctx, tx); err != nil {
		return false, err
	}
	// This authorizer must lock tenant then actor before any schedule/task row.
	epoch, authorizationErr := e.auth(ctx, tx, c.principal, tasks.Execute)
	if authorizationErr != nil && !denied(authorizationErr) {
		return false, authorizationErr
	}
	s, err := byID(ctx, tx, c.principal, c.id, true)
	if err != nil {
		return false, err
	}
	if s.State != Enabled {
		return false, nil
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return false, err
	}
	if authorizationErr != nil || epoch != s.AuthorizationVersion {
		s, err = scan(tx.QueryRow(ctx, `UPDATE adtr.task_schedules SET state='authorization_blocked',control_version=control_version+1,error_code='authorization_revoked',updated_at=clock_timestamp() WHERE schedule_id=$1 RETURNING `+columns, s.ID))
		if err != nil {
			return false, err
		}
		if err = appendEvent(ctx, tx, s, "authorization_blocked", nil, nil, ""); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	due := latestDue(s.StartAt, now, s.IntervalSeconds)
	if due < s.NextIndex {
		return false, nil
	}
	when, err := gridAt(s.StartAt, s.IntervalSeconds, due)
	if err != nil {
		return false, err
	}
	next, err := gridAt(s.StartAt, s.IntervalSeconds, due+1)
	if err != nil {
		s, err = scan(tx.QueryRow(ctx, `UPDATE adtr.task_schedules SET state='paused',control_version=control_version+1,error_code='schedule_time_overflow',updated_at=clock_timestamp() WHERE schedule_id=$1 RETURNING `+columns, s.ID))
		if err != nil {
			return false, err
		}
		if err = appendEvent(ctx, tx, s, "schedule_time_overflow", nil, nil, ""); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if due > s.NextIndex {
		first, last := s.NextIndex, due-1
		if err = appendEvent(ctx, tx, s, "skipped_misfire", &first, &last, ""); err != nil {
			return false, err
		}
	}
	var overlapping bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.task_schedule_occurrences o JOIN adtr.tasks t ON t.task_id=o.task_id AND t.tenant_id=o.tenant_id WHERE o.tenant_id=$1 AND o.schedule_id=$2 AND t.state IN ('queued','running','retry_wait','cancel_requested'))`, s.TenantID, s.ID).Scan(&overlapping)
	if err != nil {
		return false, err
	}
	if overlapping {
		if err = appendEvent(ctx, tx, s, "skipped_overlap", &due, &due, ""); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE adtr.task_schedules SET next_index=$1,next_at=$2,updated_at=clock_timestamp() WHERE schedule_id=$3`, due+1, next, s.ID); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	result, err := e.queue.SubmitScheduledTx(ctx, tx, c.principal, tasks.ScheduledInput{SubmitInput: tasks.SubmitInput{TaskName: s.TaskName, DomainID: s.DomainID, PayloadVersion: s.PayloadVersion, Payload: s.Payload}, ScheduleID: s.ID, ScheduledAt: when, ExpectedAuthorizationVersion: s.AuthorizationVersion})
	if err != nil {
		return false, err
	}
	if result.Task.TenantID != s.TenantID || result.Task.ActorID != s.ActorID || result.Task.AuthorizationVersion != s.AuthorizationVersion || result.Task.Kind != s.TaskName || result.Task.DomainID != s.DomainID || result.Task.PayloadVersion != s.PayloadVersion {
		return false, problem(409, "schedule_occurrence_conflict")
	}
	if err = appendEvent(ctx, tx, s, "admitted", &due, &due, result.Task.ID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE adtr.task_schedules SET next_index=$1,next_at=$2,last_task_id=$3,last_scheduled_at=$4,error_code='',updated_at=clock_timestamp() WHERE schedule_id=$5`, due+1, next, result.Task.ID, when, s.ID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
