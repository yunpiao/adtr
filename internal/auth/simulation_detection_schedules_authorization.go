package auth

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

const simulationDetectionScheduledKind = "simulation.detection.scheduled"

// NewSimulationDetectionSchedulesTaskAuthorizer changes only the exact scheduled
// synthetic kind. Existing manual detection and health authorization are delegated
// unchanged. Both feature switches are required; neither grants any permission.
func NewSimulationDetectionSchedulesTaskAuthorizer(manualEnabled, schedulesEnabled bool) tasks.Authorizer {
	legacy := NewSimulationDetectionTaskAuthorizer(manualEnabled)
	scheduled := simulationDetectionSchedulesTaskAuthorizer(time.Now)
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		if scope.TaskName != simulationDetectionScheduledKind {
			return legacy(ctx, tx, p, scope, action)
		}
		if !manualEnabled || !schedulesEnabled {
			return "", tasks.ErrAuthorization
		}
		return scheduled(ctx, tx, p, scope, action)
	}
}

func simulationDetectionSchedulesTaskAuthorizer(now func() time.Time) tasks.Authorizer {
	manual := simulationDetectionTaskAuthorizer(now)
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		if scope.TaskName != simulationDetectionScheduledKind {
			return "", tasks.ErrAuthorization
		}
		// The existing authorizer locks tenant then actor and validates live identity,
		// domain, existing detection grant and epoch. Reuse without widening its kind.
		manualScope := scope
		manualScope.TaskName = "simulation.detection"
		epoch, err := manual(ctx, tx, p, manualScope, action)
		if err != nil {
			return "", err
		}
		var role string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(role_id,''),role) FROM adtr.users WHERE tenant_id=$1 AND id=$2`, p.TenantID, p.ActorID).Scan(&role); err != nil {
			return "", err
		}
		grants, err := loadAccessGrants(ctx, tx, p.TenantID, role)
		if err != nil {
			return "", err
		}
		g := grants["simulation_detection_schedules"]
		if !g.Readable || action != tasks.Read && !g.Writeable {
			return "", tasks.ErrAuthorization
		}
		return epoch, nil
	}
}
