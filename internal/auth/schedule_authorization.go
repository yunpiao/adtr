package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/tasks"
)

func NewScheduleAuthorizer() tasks.Authorizer { return scheduleAuthorizer(time.Now) }

// Schedule ownership is enforced by the engine. This checks both live grants
// under the existing tenant -> actor locking protocol; it stores no proof.
func scheduleAuthorizer(now func() time.Time) tasks.Authorizer {
	base := taskAuthorizer(now)
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		if scope.TaskName != schedules.HealthKind || !scope.Platform || scope.DomainID != "platform" {
			return "", tasks.ErrAuthorization
		}
		epoch, err := base(ctx, tx, p, scope, action)
		if err != nil {
			return "", err
		}
		var role string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(role_id,''),role) FROM adtr.users WHERE tenant_id=$1 AND id=$2`, p.TenantID, p.ActorID).Scan(&role); err != nil {
			return "", err
		}
		if role == "platform_admin" {
			return epoch, nil
		}
		var readable, writeable bool
		err = tx.QueryRow(ctx, `SELECT readable,writeable FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark='schedules'`, p.TenantID, role).Scan(&readable, &writeable)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", tasks.ErrAuthorization
		}
		if err != nil {
			return "", err
		}
		if !readable || action != tasks.Read && !writeable {
			return "", tasks.ErrAuthorization
		}
		return epoch, nil
	}
}
