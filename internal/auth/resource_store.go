package auth

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"
)

const resourceMetaColumns = `g.id,g.name,g.mark,g.created_at,
 (SELECT count(*) FROM adtr.resource_role_groups r WHERE r.tenant_id=g.tenant_id AND r.group_id=g.id),
 ARRAY(SELECT m.domain_id FROM adtr.resource_group_members m WHERE m.tenant_id=g.tenant_id AND m.group_id=g.id ORDER BY m.domain_id)`

func scanResourceMeta(row pgx.Row) (ResourceMeta, error) {
	m := ResourceMeta{Datas: []ResourceData{}}
	var ids []string
	err := row.Scan(&m.ID, &m.Name, &m.Mark, &m.CreateTime, &m.ApplyRoleCount, &ids)
	m.CreateTime = m.CreateTime.UTC()
	if len(ids) > 0 {
		m.Datas = append(m.Datas, ResourceData{LocalResourceApplication, ids})
	}
	return m, err
}
func (s *Service) getResourceGroup(ctx context.Context, tx pgx.Tx, tenant, id string) (ResourceMeta, error) {
	if !validResourceID(id) {
		return ResourceMeta{}, fail(400, "invalid_input")
	}
	m, err := scanResourceMeta(tx.QueryRow(ctx, "SELECT "+resourceMetaColumns+" FROM adtr.resource_groups g WHERE g.tenant_id=$1 AND g.id=$2", tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, fail(404, "not_found")
	}
	return m, err
}
func (s *Service) listResourceGroups(ctx context.Context, tx pgx.Tx, u User, q url.Values) (any, error) {
	f, err := parseResourceFilter(q, false)
	if err != nil {
		return nil, err
	}
	var total int
	err = tx.QueryRow(ctx, "SELECT count(*) FROM adtr.resource_groups WHERE tenant_id=$1 AND position(lower($2) in lower(name))>0", u.tenant, f.name).Scan(&total)
	if err != nil {
		return nil, err
	}
	page, limit, offset, err := resourcePage(f, total)
	if err != nil {
		return nil, err
	}
	direction := "DESC"
	if f.ascending {
		direction = "ASC"
	}
	rows, err := tx.Query(ctx, "SELECT "+resourceMetaColumns+" FROM adtr.resource_groups g WHERE g.tenant_id=$1 AND position(lower($2) in lower(g.name))>0 ORDER BY g.created_at "+direction+",g.id "+direction+" LIMIT $3 OFFSET $4", u.tenant, f.name, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ResourceMeta{}
	for rows.Next() {
		m, e := scanResourceMeta(rows)
		if e != nil {
			return nil, e
		}
		list = append(list, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"page": page, "metas": list, "exhausted": offset+len(list) >= total}, nil
}
func (s *Service) resourceGroupRoles(ctx context.Context, tx pgx.Tx, tenant, id string) ([]string, error) {
	rows, err := tx.Query(ctx, "SELECT role_id FROM adtr.resource_role_groups WHERE tenant_id=$1 AND group_id=$2 ORDER BY role_id", tenant, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		ids = append(ids, v)
	}
	return ids, rows.Err()
}
func (s *Service) listResourceRoles(ctx context.Context, tx pgx.Tx, u User, q url.Values) (any, error) {
	f, err := parseResourceFilter(q, true)
	if err != nil {
		return nil, err
	}
	id := q.Get("id")
	if _, err = s.getResourceGroup(ctx, tx, u.tenant, id); err != nil {
		return nil, err
	}
	var total int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM adtr.resource_role_groups WHERE tenant_id=$1 AND group_id=$2", u.tenant, id).Scan(&total); err != nil {
		return nil, err
	}
	page, limit, offset, err := resourcePage(f, total)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT r.role_id,COALESCE(a.name,r.role_id),COALESCE(a.remark,'') FROM adtr.resource_role_groups r
 LEFT JOIN adtr.access_roles a ON a.tenant_id=r.tenant_id AND a.id=r.custom_role_id
 WHERE r.tenant_id=$1 AND r.group_id=$2 ORDER BY r.role_id LIMIT $3 OFFSET $4`, u.tenant, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	details := []map[string]string{}
	for rows.Next() {
		var rid, name, mark string
		if err = rows.Scan(&rid, &name, &mark); err != nil {
			return nil, err
		}
		details = append(details, map[string]string{"id": rid, "name": name, "mark": mark})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"page": page, "details": details, "exhausted": offset+len(details) >= total}, nil
}
func (s *Service) getResourceTenant(ctx context.Context, tx pgx.Tx, tenant string) (ResourceTenant, error) {
	var v ResourceTenant
	err := tx.QueryRow(ctx, "SELECT max_ad_count,expire_time,uid,name FROM adtr.resource_tenant_config WHERE tenant_id=$1", tenant).Scan(&v.MaxADCount, &v.ExpireTime, &v.UID, &v.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, fail(409, "tenant_not_configured")
	}
	return v, err
}
func (s *Service) effectiveResourceIDs(ctx context.Context, tx pgx.Tx, u User, role string) ([]string, error) {
	config, err := s.getResourceTenant(ctx, tx, u.tenant)
	if err != nil {
		return nil, err
	}
	if config.ExpireTime <= s.now().Unix() {
		return nil, fail(403, "tenant_expired")
	}
	if config.MaxADCount == 0 {
		return []string{}, nil
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM adtr.resource_domains WHERE tenant_id=$1 AND active", u.tenant).Scan(&count); err != nil {
		return nil, err
	}
	if count > config.MaxADCount {
		return nil, fail(403, "tenant_domain_limit_exceeded")
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT d.id FROM adtr.resource_domains d
 JOIN adtr.resource_group_members m ON m.tenant_id=d.tenant_id AND m.domain_id=d.id
 JOIN adtr.resource_role_groups r ON r.tenant_id=m.tenant_id AND r.group_id=m.group_id
 WHERE d.tenant_id=$1 AND d.active AND r.role_id=$2 ORDER BY d.id`, u.tenant, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Service) resourceValidateDomains(ctx context.Context, tx pgx.Tx, tenant string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM adtr.resource_domains WHERE tenant_id=$1 AND active AND id=ANY($2::text[])", tenant, ids).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return fail(422, "unavailable_resource")
	}
	return nil
}
func (s *Service) resourceDelegation(ctx context.Context, tx pgx.Tx, u User, role string, ids []string) error {
	if role == "platform_admin" || len(ids) == 0 {
		return nil
	}
	owned, err := s.persistedResourceIDs(ctx, tx, u.tenant, role)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !slices.Contains(owned, id) {
			return fail(403, "resource_delegation_forbidden")
		}
	}
	return nil
}
func (s *Service) revokeResourceRoles(ctx context.Context, tx pgx.Tx, u User, roles []string) (bool, error) {
	if len(roles) == 0 {
		return false, nil
	}
	var mine bool
	err := tx.QueryRow(ctx, "SELECT COALESCE(NULLIF(role_id,''),role)=ANY($2::text[]) FROM adtr.users WHERE id=$1", u.ID, roles).Scan(&mine)
	if err != nil {
		return false, err
	}

	if err = s.lockResourceUsers(ctx, tx, u.tenant, roles); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM adtr.sessions s USING adtr.users u WHERE s.user_id=u.id AND u.tenant_id=$1 AND COALESCE(NULLIF(u.role_id,''),u.role)=ANY($2::text[])`, u.tenant, roles)
	return mine, err
}
func (s *Service) resourceAudit(ctx context.Context, tx pgx.Tx, u User, action, id string) error {
	if err := setAuditMetadata(ctx, tx, u); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO adtr.resource_audit(actor_id,tenant_id,action,target_id) VALUES($1,$2,$3,$4)", u.ID, u.tenant, action, id); err != nil {
		return err
	}
	return s.audit(ctx, tx, u, action, u.ID)
}
func (s *Service) mutateResourceGroup(ctx context.Context, tx pgx.Tx, u User, role, path string, in resourceRequest) (any, error) {
	var old ResourceMeta
	var roles []string
	var err error
	creating := path == "/groups/create"
	deleting := path == "/groups/delete"
	if !creating {
		old, err = s.getResourceGroup(ctx, tx, u.tenant, in.ID)
		if err != nil {
			return nil, err
		}
		roles, err = s.resourceGroupRoles(ctx, tx, u.tenant, in.ID)
		if err != nil {
			return nil, err
		}
		if role != "platform_admin" && slices.Contains(roles, role) {
			return nil, fail(403, "own_resource_scope_forbidden")
		}
		if role != "platform_admin" && slices.Contains(roles, "platform_admin") {
			return nil, fail(403, "resource_delegation_forbidden")
		}

		if err = s.resourceRoleFunctionCeiling(ctx, tx, u.tenant, role, roles); err != nil {
			return nil, err
		}
		if len(old.Datas) > 0 {
			if err = s.resourceDelegation(ctx, tx, u, role, old.Datas[0].Resources); err != nil {
				return nil, err
			}
		}
	}
	ids := []string{}
	if !deleting {
		ids, err = resourceMembers(in.Meta)
		if err != nil {
			return nil, err
		}
		sort.Strings(ids)
		if err = s.resourceValidateDomains(ctx, tx, u.tenant, ids); err != nil {
			return nil, err
		}
		if err = s.resourceDelegation(ctx, tx, u, role, ids); err != nil {
			return nil, err
		}
		if !creating {
			previous := []string{}
			if len(old.Datas) > 0 {
				previous = old.Datas[0].Resources
			}
			if old.Name == in.Meta.Name && old.Mark == in.Meta.Mark && slices.Equal(previous, ids) {
				return nil, fail(409, "no_change")
			}
		}
	}
	id := in.ID
	action := "resource_group_update"
	if creating {
		id = randomToken(18)
		action = "resource_group_create"
		_, err = tx.Exec(ctx, "INSERT INTO adtr.resource_groups(tenant_id,id,name,mark,created_at) VALUES($1,$2,$3,$4,$5)", u.tenant, id, in.Meta.Name, in.Meta.Mark, s.now())
	} else if deleting {
		action = "resource_group_delete"
		_, err = tx.Exec(ctx, "DELETE FROM adtr.resource_groups WHERE tenant_id=$1 AND id=$2", u.tenant, id)
	} else {
		_, err = tx.Exec(ctx, "UPDATE adtr.resource_groups SET name=$3,mark=$4 WHERE tenant_id=$1 AND id=$2", u.tenant, id, in.Meta.Name, in.Meta.Mark)
	}
	if err != nil {
		return nil, err
	}
	previousMembers := []string{}
	if len(old.Datas) > 0 {
		previousMembers = old.Datas[0].Resources
	}
	// Renaming a group or editing its remark is not a scope revocation.
	if !deleting && (creating || !slices.Equal(previousMembers, ids)) {
		if _, err = tx.Exec(ctx, "DELETE FROM adtr.resource_group_members WHERE tenant_id=$1 AND group_id=$2", u.tenant, id); err != nil {
			return nil, err
		}
		for _, domain := range ids {
			if _, err = tx.Exec(ctx, "INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,$2,$3)", u.tenant, id, domain); err != nil {
				return nil, err
			}
		}
	}
	revoked, err := s.revokeResourceRoles(ctx, tx, u, roles)
	if err != nil {
		return nil, err
	}
	if err = s.resourceAudit(ctx, tx, u, action, id); err != nil {
		return nil, err
	}
	return map[string]any{"result": "SUCCESS", "id": id, "sessionRevoked": revoked}, nil
}
func (s *Service) assignResourceRoles(ctx context.Context, tx pgx.Tx, u User, role string, in resourceRequest) (any, error) {
	group, err := s.getResourceGroup(ctx, tx, u.tenant, in.ID)
	if err != nil {
		return nil, err
	}
	if in.RoleIDs == nil || len(in.RoleIDs) > 100 {
		return nil, fail(400, "invalid_input")
	}
	seen := map[string]bool{}
	for _, rid := range in.RoleIDs {
		if !validRoleID(rid) || seen[rid] {
			return nil, fail(400, "invalid_input")
		}
		seen[rid] = true
		if !accessBuiltin(rid) {
			var exists bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=$1 AND id=$2)", u.tenant, rid).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, fail(404, "not_found")
			}
		}
	}
	old, err := s.resourceGroupRoles(ctx, tx, u.tenant, in.ID)
	if err != nil {
		return nil, err
	}

	if err = s.resourceRoleFunctionCeiling(ctx, tx, u.tenant, role, append(append([]string{}, old...), in.RoleIDs...)); err != nil {
		return nil, err
	}
	if role != "platform_admin" && (seen[role] || slices.Contains(old, role) || seen["platform_admin"] || slices.Contains(old, "platform_admin")) {
		return nil, fail(403, "resource_delegation_forbidden")
	}
	next := append([]string{}, in.RoleIDs...)
	sort.Strings(next)
	if slices.Equal(old, next) {
		return nil, fail(409, "no_change")
	}
	if len(group.Datas) > 0 {
		if err = s.resourceDelegation(ctx, tx, u, role, group.Datas[0].Resources); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, "DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND group_id=$2", u.tenant, in.ID); err != nil {
		return nil, err
	}
	for _, rid := range next {
		if _, err = tx.Exec(ctx, "INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,$2,$3)", u.tenant, rid, in.ID); err != nil {
			return nil, err
		}
	}
	affected := append(append([]string{}, old...), next...)
	revoked, err := s.revokeResourceRoles(ctx, tx, u, affected)
	if err != nil {
		return nil, err
	}
	if err = s.resourceAudit(ctx, tx, u, "resource_roles_replace", in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"result": "SUCCESS", "sessionRevoked": revoked}, nil
}
func (s *Service) saveResourceTenant(ctx context.Context, tx pgx.Tx, u User, in resourceRequest) (any, error) {
	v, err := resourceTenantInput(in)
	if err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM adtr.resource_domains WHERE tenant_id=$1 AND active", u.tenant).Scan(&count); err != nil {
		return nil, err
	}
	if v.MaxADCount < count {
		return nil, fail(409, "domain_limit_below_existing")
	}
	var old ResourceTenant
	err = tx.QueryRow(ctx, "SELECT max_ad_count,expire_time,uid,name FROM adtr.resource_tenant_config WHERE tenant_id=$1", u.tenant).Scan(&old.MaxADCount, &old.ExpireTime, &old.UID, &old.Name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil && old == v {
		return nil, fail(409, "no_change")
	}
	_, err = tx.Exec(ctx, `INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name,updated_at) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(tenant_id) DO UPDATE SET max_ad_count=EXCLUDED.max_ad_count,expire_time=EXCLUDED.expire_time,uid=EXCLUDED.uid,name=EXCLUDED.name,updated_at=EXCLUDED.updated_at`, u.tenant, v.MaxADCount, v.ExpireTime, v.UID, v.Name, s.now())
	if err != nil {
		return nil, err
	}
	if err = s.lockResourceUsers(ctx, tx, u.tenant, nil); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM adtr.sessions s USING adtr.users u WHERE s.user_id=u.id AND u.tenant_id=$1", u.tenant); err != nil {
		return nil, err
	}
	if err = s.resourceAudit(ctx, tx, u, "resource_tenant_update", u.tenant); err != nil {
		return nil, err
	}
	return map[string]any{"result": "SUCCESS", "sessionRevoked": true}, nil
}

// persistedResourceIDs includes inactive catalogue entries and ignores tenant
// expiry. Delegation must not plant a latent broader grant that becomes active
// when a domain reconnects or the tenant is renewed.
func (s *Service) persistedResourceIDs(ctx context.Context, tx pgx.Tx, tenant, role string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT m.domain_id FROM adtr.resource_group_members m
 JOIN adtr.resource_role_groups r ON r.tenant_id=m.tenant_id AND r.group_id=m.group_id
 WHERE m.tenant_id=$1 AND r.role_id=$2 ORDER BY m.domain_id`, tenant, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// canDelegateResourceRole must be called by ALL function-role assignment and
// existing-role function-grant mutation paths, in their authenticated locked
// transaction. actor and actorRole must come from authenticateAccess, not JSON.
func (s *Service) canDelegateResourceRole(ctx context.Context, tx pgx.Tx, actor User, actorRole, targetRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	if targetRole == "platform_admin" {
		return fail(403, "resource_delegation_forbidden")
	}
	target, err := s.persistedResourceIDs(ctx, tx, actor.tenant, targetRole)
	if err != nil {
		return err
	}
	return s.resourceDelegation(ctx, tx, actor, actorRole, target)
}

// Lock before DELETE so a concurrent login/password/MFA transaction cannot insert
// a replacement session after the revocation statement's READ COMMITTED snapshot.
func (s *Service) lockResourceUsers(ctx context.Context, tx pgx.Tx, tenant string, roles []string) error {
	query := "SELECT id FROM adtr.users WHERE tenant_id=$1"
	args := []any{tenant}
	if roles != nil {
		query += " AND COALESCE(NULLIF(role_id,''),role)=ANY($2::text[])"
		args = append(args, roles)
	}
	query += " ORDER BY id FOR UPDATE"
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return err
		}
	}
	return rows.Err()
}

// A resource writer may not expand a role with function powers beyond their own.
func (s *Service) resourceRoleFunctionCeiling(ctx context.Context, tx pgx.Tx, tenant, actorRole string, roles []string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	own, err := loadAccessGrants(ctx, tx, tenant, actorRole)
	if err != nil {
		return err
	}
	for _, role := range roles {
		desired, e := loadAccessGrants(ctx, tx, tenant, role)
		if e != nil {
			return e
		}
		if !accessCanDelegate(own, desired) {
			return fail(403, "resource_delegation_forbidden")
		}
	}
	return nil
}
