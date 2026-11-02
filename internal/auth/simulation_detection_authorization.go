package auth

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
	"strconv"
	"time"
)

// NewSimulationDetectionTaskAuthorizer rechecks the durable actor and independent
// synthetic grant at every engine authorization boundary. No tasks/admin bypass.
func NewSimulationDetectionTaskAuthorizer(enabled bool) tasks.Authorizer {
	detection := simulationDetectionTaskAuthorizer(time.Now)
	legacy := NewTaskAuthorizer()
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		if scope.TaskName != "simulation.detection" {
			return legacy(ctx, tx, p, scope, action)
		}
		if !enabled {
			return "", tasks.ErrAuthorization
		}
		return detection(ctx, tx, p, scope, action)
	}
}
func simulationDetectionTaskAuthorizer(now func() time.Time) tasks.Authorizer {
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		deny := func() (string, error) { return "", tasks.ErrAuthorization }
		if tx == nil || p.ActorID <= 0 || !validResourceID(p.TenantID) || !validResourceID(scope.DomainID) || scope.Platform || scope.DomainID == "platform" || scope.TaskName != "simulation.detection" || (action != tasks.Read && action != tasks.Write && action != tasks.Execute) {
			return deny()
		}
		if err := lockIdentityTenant(ctx, tx, p.TenantID); err != nil {
			return "", err
		}
		var role string
		var disabled, forced bool
		var updated time.Time
		var revision int64
		err := tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(role_id,''),role),disabled,must_change,password_updated_at,authorization_version FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, p.ActorID).Scan(&role, &disabled, &forced, &updated, &revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return deny()
		}
		if err != nil {
			return "", err
		}
		if disabled || forced || !updated.Add(90*24*time.Hour).After(now()) || revision <= 0 {
			return deny()
		}
		grants, err := loadAccessGrants(ctx, tx, p.TenantID, role)
		if err != nil {
			return "", err
		}
		g := grants["simulation_detection"]
		if !g.Readable || action != tasks.Read && !g.Writeable {
			return deny()
		}
		service := Service{now: now}
		allowed, err := service.effectiveResourceIDs(ctx, tx, User{tenant: p.TenantID, ID: p.ActorID}, role)
		if err != nil {
			var f failure
			if errors.As(err, &f) {
				return deny()
			}
			return "", err
		}
		for _, id := range allowed {
			if id == scope.DomainID {
				return strconv.FormatInt(revision, 10), nil
			}
		}
		return deny()
	}
}
