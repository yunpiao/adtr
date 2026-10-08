package tasks

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const migrationLockID int64 = 734192801

var ErrSchemaIncompatible = &Error{503, "schema_incompatible"}
var ErrSchemaGateRequired = &Error{503, "schema_gate_required"}

// CheckSchemaTx must run immediately after Begin, before any tenant/user/task
// lock. It holds a shared migration lock and a schema row share lock to commit.
// The explicit migrator uses the exclusive advisory lock with the same key.
func (e *Engine) CheckSchemaTx(ctx context.Context, tx pgx.Tx) error {
	return CheckSchemaTx(ctx, tx, e.expectedSchemaVersion)
}

// CheckSchemaTx is also available to transaction owners using a fixture or
// composing modules. Runtime callers pass the same exact version as Engine.New.
func CheckSchemaTx(ctx context.Context, tx pgx.Tx, expected int) error {
	if tx == nil || expected <= 0 {
		return ErrSchemaIncompatible
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared($1::bigint)", migrationLockID); err != nil {
		return err
	}
	return checkSchemaVersion(ctx, tx, expected)
}
func checkSchemaVersion(ctx context.Context, tx pgx.Tx, expected int) error {
	var actual int
	err := tx.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton=true FOR SHARE").Scan(&actual)
	if err != nil {
		var p *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &p) && (p.Code == "42P01" || p.Code == "42703") {
			return ErrSchemaIncompatible
		}
		return err
	}
	if actual != expected {
		return ErrSchemaIncompatible
	}
	return nil
}

// Public Tx methods must not acquire the migration lock after authentication
// has already acquired tenant/user locks. Verify the required earlier gate;
// missing-gate callers fail closed rather than creating a lock-order inversion.
func (e *Engine) requireSchemaTx(ctx context.Context, tx pgx.Tx) error {
	var held bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=$1 AND objsubid=1 AND mode IN ('ShareLock','ExclusiveLock'))`, migrationLockID).Scan(&held)
	if err != nil {
		return err
	}
	if !held {
		return ErrSchemaGateRequired
	}
	return checkSchemaVersion(ctx, tx, e.expectedSchemaVersion)
}
