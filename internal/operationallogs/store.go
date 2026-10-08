package operationallogs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

const appendTimeout = 2 * time.Second
const readTimeout = 3 * time.Second

type Store struct {
	config         *pgx.ConnConfig
	expectedSchema int
}

func NewStore(config *pgx.ConnConfig, expectedSchema int) (*Store, error) {
	if config == nil || expectedSchema != SchemaVersion {
		return nil, problem(500, "invalid_configuration")
	}
	return &Store{config: config.Copy(), expectedSchema: expectedSchema}, nil
}

// RequireSchema verifies the caller already acquired the migration gate before
// taking authentication locks. It does not introduce a late blocking lock.
func RequireSchema(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return tasks.ErrSchemaGateRequired
	}
	var version int
	var held bool
	err := tx.QueryRow(ctx, `SELECT version,EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode IN ('ShareLock','ExclusiveLock')) FROM adtr.schema_version WHERE singleton=true`).Scan(&version, &held)
	if err != nil {
		return err
	}
	if version != SchemaVersion {
		return tasks.ErrSchemaIncompatible
	}
	if !held {
		return tasks.ErrSchemaGateRequired
	}
	return nil
}
func safeError(err error) error {
	if err == nil {
		return nil
	}
	var public *Error
	if errors.As(err, &public) {
		return public
	}
	if errors.Is(err, tasks.ErrSchemaIncompatible) {
		return problem(503, "schema_incompatible")
	}
	if errors.Is(err, tasks.ErrAuthorization) {
		return problem(403, "forbidden")
	}
	if errors.Is(err, tasks.ErrSchemaGateRequired) {
		return problem(503, "schema_gate_required")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return problem(503, "journal_timeout")
	}
	return problem(503, "journal_storage_unavailable")
}
func validateObservation(o RecorderObservation, e Event) error {
	if o.ProcessID != e.ProcessID || o.Module != e.Module || o.StartedAt.IsZero() || o.StartedAt.Unix() < 0 || o.StartedAt.Nanosecond()%1000 != 0 || o.ObservedAt.IsZero() || o.ObservedAt.Unix() < 0 || o.ObservedAt.Nanosecond()%1000 != 0 || o.Accepted < 1 || o.Acknowledged < 0 || o.Rejected < 0 || o.QueueFull < 0 || o.WriteFailures < 0 || o.Unacknowledged < 0 || o.Abandoned < 0 || o.Acknowledged > o.Accepted || o.Unacknowledged > o.Accepted-o.Acknowledged || o.Abandoned > o.Accepted-o.Acknowledged-o.Unacknowledged {
		return problem(400, "invalid_observation")
	}
	return nil
}

func (s *Store) Append(ctx context.Context, e Event, o RecorderObservation) (Event, error) {
	definite := func(err error) (Event, error) { return Event{}, &AppendError{Err: safeError(err)} }
	if err := validateEvent(e, false); err != nil {
		return definite(err)
	}
	if err := validateObservation(o, e); err != nil {
		return definite(err)
	}
	ctx, cancel := context.WithTimeout(ctx, appendTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return definite(err)
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer stop()
		_ = conn.Close(closeCtx)
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return definite(err)
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, s.expectedSchema); err != nil {
		return definite(err)
	}
	var process string
	err = tx.QueryRow(ctx, `INSERT INTO adtr.operational_log_reports(process_id,module,started_at,observed_at,accepted,acknowledged,rejected,queue_full,write_failures,unacknowledged,abandoned,acceptance_stopped)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
 ON CONFLICT(process_id) DO UPDATE SET observed_at=GREATEST(adtr.operational_log_reports.observed_at,EXCLUDED.observed_at),reported_at=GREATEST(adtr.operational_log_reports.reported_at,clock_timestamp()),
 accepted=GREATEST(adtr.operational_log_reports.accepted,EXCLUDED.accepted),acknowledged=GREATEST(adtr.operational_log_reports.acknowledged,EXCLUDED.acknowledged),
 rejected=GREATEST(adtr.operational_log_reports.rejected,EXCLUDED.rejected),queue_full=GREATEST(adtr.operational_log_reports.queue_full,EXCLUDED.queue_full),
 write_failures=GREATEST(adtr.operational_log_reports.write_failures,EXCLUDED.write_failures),unacknowledged=GREATEST(adtr.operational_log_reports.unacknowledged,EXCLUDED.unacknowledged),
 abandoned=GREATEST(adtr.operational_log_reports.abandoned,EXCLUDED.abandoned),acceptance_stopped=adtr.operational_log_reports.acceptance_stopped OR EXCLUDED.acceptance_stopped
 WHERE adtr.operational_log_reports.module=EXCLUDED.module AND adtr.operational_log_reports.started_at=EXCLUDED.started_at RETURNING process_id`, o.ProcessID, string(o.Module), o.StartedAt, o.ObservedAt, o.Accepted, o.Acknowledged, o.Rejected, o.QueueFull, o.WriteFailures, o.Unacknowledged, o.Abandoned, o.AcceptanceStopped).Scan(&process)
	if errors.Is(err, pgx.ErrNoRows) {
		return definite(problem(409, "process_conflict"))
	}
	if err != nil {
		return definite(err)
	}
	stored, err := scanEvent(tx.QueryRow(ctx, `INSERT INTO adtr.operational_log_events(event_id,process_id,schema_version,module,code,outcome,severity,reason,observed_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(event_id) DO NOTHING RETURNING `+eventColumns, e.EventID, e.ProcessID, e.SchemaVersion, string(e.Module), string(e.Code), string(e.Outcome), string(e.Severity), string(e.Reason), e.ObservedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		stored, err = scanEvent(tx.QueryRow(ctx, `SELECT `+eventColumns+` FROM adtr.operational_log_events WHERE event_id=$1`, e.EventID))
		if err == nil && !sameEvent(e, stored) {
			return definite(problem(409, "event_conflict"))
		}
	}
	if err != nil {
		return definite(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Event{}, &AppendError{Err: safeError(err), Uncertain: !errors.Is(err, pgx.ErrTxCommitRollback)}
	}
	return stored, nil
}

const eventColumns = `schema_version,event_id,process_id,module,code,outcome,severity,reason,observed_at,recorded_at`

func scanEvent(row pgx.Row) (Event, error) {
	var e Event
	err := row.Scan(&e.SchemaVersion, &e.EventID, &e.ProcessID, &e.Module, &e.Code, &e.Outcome, &e.Severity, &e.Reason, &e.ObservedAt, &e.RecordedAt)
	if err != nil {
		return e, err
	}
	e.ObservedAt = e.ObservedAt.UTC()
	e.RecordedAt = e.RecordedAt.UTC()
	if validateEvent(e, true) != nil {
		return Event{}, problem(503, "invalid_stored_event")
	}
	return e, nil
}
func sameEvent(a, b Event) bool {
	return a.SchemaVersion == b.SchemaVersion && a.EventID == b.EventID && a.ProcessID == b.ProcessID && a.Module == b.Module && a.Code == b.Code && a.Outcome == b.Outcome && a.Severity == b.Severity && a.Reason == b.Reason && a.ObservedAt.Equal(b.ObservedAt)
}
