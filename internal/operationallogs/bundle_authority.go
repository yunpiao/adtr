package operationallogs

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// RequireReadTx is shared by journal and dedicated bundle reads. It uses the
// same tenant-before-user lock order as task authorization, without requiring
// task visibility grants for reading this installation journal.
func RequireReadTx(ctx context.Context, tx pgx.Tx, p Principal) (string, error) {
	if err := RequireSchema(ctx, tx); err != nil {
		return "", err
	}
	if !validPrincipal(p) {
		return "", tasks.ErrAuthorization
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, p.TenantID); err != nil {
		return "", err
	}
	var epoch, role string
	var active bool
	err := tx.QueryRow(ctx, `SELECT authorization_version::text,COALESCE(NULLIF(role_id,''),role),NOT disabled AND NOT must_change AND password_updated_at+interval '90 days'>clock_timestamp() AND authorization_version>0 FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, p.ActorID).Scan(&epoch, &role, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", tasks.ErrAuthorization
	}
	if err != nil {
		return "", err
	}
	if !active {
		return "", tasks.ErrAuthorization
	}
	if role == "platform_admin" {
		return epoch, nil
	}
	if role == "viewer" {
		return "", tasks.ErrAuthorization
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.access_permissions s JOIN adtr.access_permissions l ON l.tenant_id=s.tenant_id AND l.role_id=s.role_id WHERE s.tenant_id=$1 AND s.role_id=$2 AND s.mark='system' AND s.readable AND l.mark='system_logs' AND l.readable)`, p.TenantID, role).Scan(&allowed)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", tasks.ErrAuthorization
	}
	return epoch, nil
}

func bundleAuthority(ctx context.Context, tx pgx.Tx, p Principal, write bool) (string, error) {
	epoch, err := RequireReadTx(ctx, tx, p)
	if err != nil || !write {
		return epoch, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(u.role_id,''),u.role)='platform_admin' OR (COALESCE(NULLIF(u.role_id,''),u.role)<>'viewer' AND EXISTS(SELECT FROM adtr.access_permissions l JOIN adtr.access_permissions t ON t.tenant_id=l.tenant_id AND t.role_id=l.role_id WHERE l.tenant_id=u.tenant_id AND l.role_id=COALESCE(NULLIF(u.role_id,''),u.role) AND l.mark='system_logs' AND l.readable AND l.writeable AND t.mark='tasks' AND t.readable AND t.writeable)) FROM adtr.users u WHERE u.tenant_id=$1 AND u.id=$2`, p.TenantID, p.ActorID).Scan(&allowed)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", tasks.ErrAuthorization
	}
	return epoch, nil
}

// RecordBundleTx is used in the caller's actual mutation transaction. The
// unique task/action control prevents a repeated submit or cancel audit.
func RecordBundleTx(ctx context.Context, tx pgx.Tx, p Principal, id, action string) error {
	epoch, err := bundleAuthority(ctx, tx, p, true)
	if err != nil {
		return err
	}
	if !validUUID(id) || (action != "bundle_submit" && action != "bundle_cancel") {
		return problem(400, "invalid_input")
	}
	var state tasks.State
	err = tx.QueryRow(ctx, `SELECT state FROM adtr.tasks t WHERE task_id=$1 AND tenant_id=$2 AND actor_id=$3 AND authorization_version=$4 AND kind=$5 AND domain_id='platform' AND NOT EXISTS(SELECT FROM adtr.task_visibility v WHERE v.task_id=t.task_id AND v.tenant_id=t.tenant_id AND v.archived) FOR UPDATE`, id, p.TenantID, p.ActorID, epoch, BundleKindName).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return problem(404, "not_found")
	}
	if err != nil {
		return err
	}
	if action == "bundle_submit" && state != tasks.Queued || action == "bundle_cancel" && state != tasks.CancelRequested && state != tasks.Cancelled {
		return problem(409, "invalid_task_state")
	}
	_, err = tx.Exec(ctx, `INSERT INTO adtr.operational_log_audit(tenant_id,actor_id,task_id,action) VALUES($1,$2,$3,$4) ON CONFLICT(task_id,action) DO NOTHING`, p.TenantID, p.ActorID, id, action)
	return err
}
