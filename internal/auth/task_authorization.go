package auth

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationallogs"
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
		if scope.Platform && (scope.DomainID != "platform" || (scope.TaskName != "infrastructure.health" && scope.TaskName != "audit.export" && scope.TaskName != operationallogs.BundleKindName)) || !scope.Platform && scope.DomainID == "platform" {
			return deny()
		}
		if scope.TaskName == operationallogs.BundleKindName && p.TenantID != operationallogs.OwnerTenantID {
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
		if scope.TaskName == operationallogs.BundleKindName && role != "platform_admin" {
			var allowed bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.access_permissions s JOIN adtr.access_permissions l ON l.tenant_id=s.tenant_id AND l.role_id=s.role_id WHERE s.tenant_id=$1 AND s.role_id=$2 AND s.mark='system' AND s.readable AND l.mark='system_logs' AND l.readable AND ($3 OR l.writeable))`, p.TenantID, role, action == tasks.Read).Scan(&allowed)
			if err != nil {
				return "", err
			}
			if !allowed {
				return deny()
			}
		}
		if scope.TaskName == "audit.export" && role != "platform_admin" {
			var allowed bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.access_permissions a JOIN adtr.access_permissions x ON x.tenant_id=a.tenant_id AND x.role_id=a.role_id WHERE a.tenant_id=$1 AND a.role_id=$2 AND a.mark='audit' AND a.readable AND x.mark='audit_exports' AND x.readable AND ($3 OR x.writeable))`, p.TenantID, role, action == tasks.Read).Scan(&allowed)
			if err != nil {
				return "", err
			}
			if !allowed {
				return deny()
			}
		}
		if domains.IsConnectionTestKind(scope.TaskName) && role != "platform_admin" {
			var allowed bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark='domains' AND readable AND ($3 OR writeable))`, p.TenantID, role, action == tasks.Read).Scan(&allowed)
			if err != nil {
				return "", err
			}
			if !allowed {
				return deny()
			}
		}
		if scope.TaskName == domains.DirectoryKindName && role != "platform_admin" {
			var allowed bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.access_permissions d JOIN adtr.access_permissions a ON a.tenant_id=d.tenant_id AND a.role_id=d.role_id WHERE d.tenant_id=$1 AND d.role_id=$2 AND d.mark='domains' AND d.readable AND a.mark='directory_assets' AND a.readable AND ($3 OR a.writeable))`, p.TenantID, role, action == tasks.Read).Scan(&allowed)
			if err != nil {
				return "", err
			}
			if !allowed {
				return deny()
			}
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
		// Reading/cancelling a task is safe after use revocation. Only execution
		// resolves a live account source and always requires an explicit allow,
		// including platform administrators.
		if (scope.TaskName == domains.AccountKindName || scope.TaskName == domains.DirectoryKindName) && action == tasks.Execute {
			purpose := "domain.connection_test"
			eligibility := "adtr.credential_use_role_eligible(c.tenant_id,c.domain_id,$3)"
			if scope.TaskName == domains.DirectoryKindName {
				purpose = "domain.directory_read"
				eligibility = "adtr.credential_use_role_eligible_for_purpose(c.tenant_id,c.domain_id,$3,$4)"
			}
			var allowed bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(
 SELECT FROM adtr.domain_connections c
 JOIN adtr.operation_accounts a ON a.tenant_id=c.tenant_id AND a.domain_id=c.domain_id AND a.account_id=c.operation_account_id AND a.credential_revision=c.operation_account_credential_revision AND a.deleted_at IS NULL
 JOIN adtr.operation_account_credentials pair ON pair.tenant_id=a.tenant_id AND pair.domain_id=a.domain_id AND pair.account_id=a.account_id AND pair.credential_revision=a.credential_revision AND pair.envelope_version=2
 JOIN adtr.operation_account_use_grants g ON g.tenant_id=a.tenant_id AND g.domain_id=a.domain_id AND g.account_id=a.account_id AND g.role_id=$3 AND g.purpose=$4 AND g.allowed AND g.account_credential_revision=a.credential_revision
 JOIN adtr.operation_account_dependencies d ON d.tenant_id=a.tenant_id AND d.domain_id=a.domain_id AND d.account_id=a.account_id AND d.consumer_kind='domain.connection_binding' AND d.object_id=c.domain_id AND d.account_credential_revision=a.credential_revision AND d.connection_credential_generation=c.credential_revision
 WHERE c.tenant_id=$1 AND c.domain_id=$2 AND c.deleted_at IS NULL AND c.credential_mode='operation_account'
 AND `+eligibility+`)`, p.TenantID, scope.DomainID, role, purpose).Scan(&allowed)
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
