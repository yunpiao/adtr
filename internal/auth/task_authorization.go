package auth

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// NewTaskAuthorizer validates durable actors, not session claims. Workers need
// neither a browser cookie nor the MFA encryption key. Logging out alone does
// not revoke an already authorized background task.
func NewTaskAuthorizer() tasks.Authorizer { return taskAuthorizer(time.Now) }

func taskAuthorizer(now func() time.Time) tasks.Authorizer {
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		deny := func() (string, error) { return "", tasks.ErrAuthorization }
		if p.ActorID <= 0 || !validResourceID(p.TenantID) || !validResourceID(scope.DomainID) || scope.TaskName == "" || (action != tasks.Read && action != tasks.Write && action != tasks.Execute) {
			return deny()
		}
		// A platform scope is immutable registry metadata; a domain named platform
		// does not acquire platform authorization. Future platform kinds need an
		// explicit review here rather than inheriting an infrastructure exception.
		if scope.Platform && (scope.DomainID != "platform" || scope.TaskName != "infrastructure.health") || !scope.Platform && scope.DomainID == "platform" {
			return deny()
		}
		if err := lockIdentityTenant(ctx, tx, p.TenantID); err != nil {
			return "", err
		}
		var role string
		var disabled, forced bool
		var passwordUpdated time.Time
		var revision int64
		err := tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(role_id,''),role),disabled,must_change,password_updated_at,authorization_version FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, p.ActorID).Scan(&role, &disabled, &forced, &passwordUpdated, &revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return deny()
		}
		if err != nil {
			return "", err
		}
		instant := now()
		if disabled || forced || !passwordUpdated.Add(90*24*time.Hour).After(instant) || revision <= 0 {
			return deny()
		}
		var grant AccessAuth
		if role == "platform_admin" {
			grant = AccessAuth{Readable: true, Writeable: true}
		} else if role != "viewer" {
			err = tx.QueryRow(ctx, `SELECT readable,writeable FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark='tasks'`, p.TenantID, role).Scan(&grant.Readable, &grant.Writeable)
			if errors.Is(err, pgx.ErrNoRows) {
				return deny()
			}
			if err != nil {
				return "", err
			}
		}
		if !grant.Readable || action != tasks.Read && !grant.Writeable {
			return deny()
		}
		if !scope.Platform {
			// Keep all predicate failures indistinguishable. SQL/connection errors must
			// propagate: an outage must never be persisted as an authorization failure.
			var allowed bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(
    SELECT FROM adtr.resource_tenant_config c
    JOIN adtr.resource_domains d ON d.tenant_id=c.tenant_id
    JOIN adtr.resource_group_members m ON m.tenant_id=d.tenant_id AND m.domain_id=d.id
    JOIN adtr.resource_role_groups r ON r.tenant_id=m.tenant_id AND r.group_id=m.group_id
    WHERE c.tenant_id=$1 AND c.expire_time>$4 AND c.max_ad_count>0
      AND (SELECT count(*) FROM adtr.resource_domains count_domain WHERE count_domain.tenant_id=c.tenant_id AND count_domain.active)<=c.max_ad_count
      AND d.id=$2 AND d.active AND r.role_id=$3
   )`, p.TenantID, scope.DomainID, role, instant.Unix()).Scan(&allowed)
			if err != nil {
				return "", err
			}
			if !allowed {
				return deny()
			}
		}
		return strconv.FormatInt(revision, 10), nil
	}
}
