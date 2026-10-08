package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrOperationalLogKindCollision = errors.New("migration blocked: reserved operational log task kind already exists")

var ErrDirectoryTaskKindCollision = errors.New("migration blocked: reserved directory task kind already exists")
var ErrDirectoryDependencyKindCollision = errors.New("migration blocked: reserved directory dependency kind already exists")

// ErrCredentialUseMigrationBlocked is deliberately free of tenant, account,
// worker and task identifiers. Migration cannot manufacture execution evidence.
var ErrCredentialUseMigrationBlocked = errors.New("migration blocked: account credential use is not confirmed quiescent")

// A stopped executor still needs its original exact-schema acknowledgement
// transaction. Refuse the upgrade promptly while any such use remains opened;
// do not wait under the exclusive gate that the acknowledgement itself needs.
func requireCredentialUseQuiescence(ctx context.Context, tx pgx.Tx, installed, target int) error {
	if installed < 13 || installed >= target {
		return nil
	}
	var exclusive, opened bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode='ExclusiveLock'),EXISTS(SELECT FROM adtr.domain_account_task_uses WHERE state='opened')`).Scan(&exclusive, &opened)
	if err != nil {
		return errors.New("credential-use migration check failed")
	}
	if !exclusive {
		return errors.New("exclusive migration gate required")
	}
	// The directory ledger first exists in schema15. Earlier upgrades must not
	// query a missing relation; later upgrades must drain both exact ledgers.
	if installed >= 15 && !opened {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.domain_directory_task_uses WHERE state='opened')`).Scan(&opened); err != nil {
			return errors.New("directory credential-use migration check failed")
		}
	}
	if opened {
		return ErrCredentialUseMigrationBlocked
	}
	return nil
}
