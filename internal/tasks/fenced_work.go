package tasks

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// WithTx gives trusted registered executors a transaction whose writes are
// committed only with their checkpoint. Schema, actor authorization, epoch and
// lease checks precede work; the final conditional update rechecks the live
// database-clock lease and result version. Any error rolls back module writes.
func (e *Engine) WithTx(ctx context.Context, l Lease, expectedVersion int64, work FencedWork) (int64, error) {
	if work == nil || expectedVersion < 0 {
		return 0, problem(400, "invalid_progress")
	}
	var version int64
	err := e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		t, _, _, authErr, err := e.workerLock(ctx, tx, l)
		if err != nil {
			return err
		}
		if authErr != nil {
			return ErrAuthorization
		}
		if t.State == CancelRequested {
			return ErrCancelRequested
		}
		if t.State != Running {
			return ErrLeaseLost
		}
		if t.ResultVersion != expectedVersion {
			return problem(409, "result_version_conflict")
		}
		progress, cursor, result, err := work(ctx, executorTx{tx})
		if err != nil {
			return err
		}
		// Locks serialize stored policy, but clock-based password/license validity
		// can expire while module work runs. Recheck it before any commit.
		_, _, _, authErr, err = e.workerLock(ctx, tx, l)
		if err != nil {
			return err
		}
		if authErr != nil {
			return ErrAuthorization
		}
		if progress < 0 || progress > 100 {
			return problem(400, "invalid_progress")
		}
		if progress < t.Progress {
			return problem(400, "progress_regression")
		}
		cursor, err = canonicalObject(cursor)
		if err != nil {
			return err
		}
		result, err = canonicalObject(result)
		if err != nil {
			return err
		}
		t, err = scanFencedTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET progress=$2,cursor=$3,result=$4,result_version=result_version+1,updated_at=clock_timestamp() WHERE task_id=$1 AND lease_owner=$5 AND fencing_token=$6 AND lease_until>clock_timestamp() AND result_version=$7 AND state='running' RETURNING `+taskColumns, t.ID, progress, cursor, result, l.Owner, l.Token, expectedVersion))
		if err != nil {
			return err
		}
		version = t.ResultVersion
		return appendEvent(ctx, tx, t, t.ActorID, "progress")
	})
	return version, err
}

// Registered executors are trusted application code, but do not receive API
// methods that can accidentally commit/rollback the engine's transaction.
type executorTx struct{ pgx.Tx }

var errExecutorTransaction = errors.New("executor cannot manage engine transaction")

func (executorTx) Commit(context.Context) error          { return errExecutorTransaction }
func (executorTx) Rollback(context.Context) error        { return errExecutorTransaction }
func (executorTx) Begin(context.Context) (pgx.Tx, error) { return nil, errExecutorTransaction }
func (executorTx) Conn() *pgx.Conn                       { return nil }
