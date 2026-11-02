package auth

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// Navigation hint only; handlers always reauthorize. Unknown schemas fail closed.
func simulationDetectionSchedulesProfileReadable(ctx context.Context, tx pgx.Tx, actor User) (bool, error) {
	if actor.NeedChange {
		return false, nil
	}
	var available bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('adtr.schema_version') IS NOT NULL AND to_regclass('adtr.access_permissions') IS NOT NULL`).Scan(&available); err != nil {
		return false, err
	}
	if !available {
		return false, nil
	}
	var version int
	if err := tx.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton").Scan(&version); err != nil {
		return false, err
	}
	if version != 27 {
		return false, nil
	}
	var role string
	if err := tx.QueryRow(ctx, "SELECT COALESCE(NULLIF(role_id,''),role) FROM adtr.users WHERE tenant_id=$1 AND id=$2", actor.tenant, actor.ID).Scan(&role); err != nil {
		return false, err
	}
	grants, err := loadAccessGrants(ctx, tx, actor.tenant, role)
	if err != nil {
		return false, err
	}
	return grants["simulation_detection"].Readable && grants["simulation_detection_schedules"].Readable, nil
}
