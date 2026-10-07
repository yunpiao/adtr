package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
)

const accessUserColumns = `u.id,u.username,u.pass_strength,u.role,u.mobile,u.email,u.remark,u.created_at,u.mfa_secret<>'',u.password_updated_at,u.address,u.real_name,u.department,u.post,COALESCE(NULLIF(u.role_id,''),u.role),COALESCE(r.name,u.role),u.disabled`
const accessUserJoin = ` FROM adtr.users u LEFT JOIN adtr.access_roles r ON r.tenant_id=u.tenant_id AND r.id=u.role_id `

func scanAccessUser(row pgx.Row) (AccessUser, error) {
	var u AccessUser
	e := row.Scan(&u.ID, &u.Username, &u.PassStrength, &u.Role, &u.Mobile, &u.Email, &u.Remark, &u.Created, &u.HasMFA, &u.PasswordUpdated, &u.Address, &u.RealName, &u.Department, &u.Post, &u.RoleID, &u.RoleName, &u.Disabled)
	u.Priv = 3
	if u.Role == "platform_admin" {
		u.Priv = 1
	} else if !accessBuiltin(u.RoleID) {
		u.Priv = 2
	}
	u.Created = u.Created.UTC()
	u.PasswordUpdated = u.PasswordUpdated.UTC()
	return u, e
}
func (s *Service) listAccessUsers(ctx context.Context, tx pgx.Tx, actor User, q url.Values) (any, error) {
	f, e := parseAccessFilter(q, false)
	if e != nil {
		return nil, e
	}
	args := []any{actor.tenant}
	where := []string{"u.tenant_id=$1"}
	add := func(expr string, v any) { args = append(args, v); where = append(where, fmt.Sprintf(expr, len(args))) }
	if f.self {
		add("u.id=$%d", actor.ID)
	}
	if f.search != "" {
		add("POSITION(lower($%d) IN lower(u.username))>0", f.search)
	}
	if f.roleID != "" {
		add("COALESCE(NULLIF(u.role_id,''),u.role)=$%d", f.roleID)
	}
	if len(f.roles) > 0 {
		add("COALESCE(NULLIF(u.role_id,''),u.role)=ANY($%d::text[])", f.roles)
	}
	if len(f.strength) > 0 {
		add("u.pass_strength=ANY($%d::text[])", f.strength)
	}
	if len(f.mfa) > 0 {
		add("(CASE WHEN u.mfa_secret<>'' THEN 'enable' ELSE 'stop' END)=ANY($%d::text[])", f.mfa)
	}
	if f.startCreated != nil {
		add("u.created_at>=$%d", *f.startCreated)
	}
	if f.endCreated != nil {
		add("u.created_at<$%d", *f.endCreated)
	}
	if f.startPassword != nil {
		add("u.password_updated_at>=$%d", *f.startPassword)
	}
	if f.endPassword != nil {
		add("u.password_updated_at<$%d", *f.endPassword)
	}
	filter := " WHERE " + strings.Join(where, " AND ")
	var total int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM adtr.users u"+filter, args...).Scan(&total); e != nil {
		return nil, e
	}
	page, limit, offset, e := makeAccessPage(f, total)
	if e != nil {
		return nil, e
	}
	order := "u.created_at"
	if f.sort == 2 || f.sort == -2 {
		order = "u.password_updated_at"
	}
	direction := " ASC"
	if f.sort < 0 {
		direction = " DESC"
	}
	args = append(args, limit, offset)
	query := "SELECT " + accessUserColumns + accessUserJoin + filter + " ORDER BY " + order + direction + ",u.id" + direction + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	list := []AccessUser{}
	for rows.Next() {
		u, err := scanAccessUser(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return map[string]any{"page": page, "List": list, "exhausted": offset+len(list) >= total}, nil
}
func accessRequestedRole(in accessRequest, current string) (string, error) {
	id := current
	if in.RoleID != nil {
		id = *in.RoleID
	} else if in.Role != nil {
		id = *in.Role
	}
	if !validRoleID(id) {
		return "", fail(400, "invalid_input")
	}
	base := "viewer"
	if id == "platform_admin" {
		base = id
	}
	if in.Role != nil && *in.Role != base {
		return "", fail(400, "invalid_role")
	}
	return id, nil
}
func (s *Service) accessMayAssign(ctx context.Context, tx pgx.Tx, actor User, grants map[string]AccessAuth, id string) error {
	desired, e := loadAccessGrants(ctx, tx, actor.tenant, id)
	if e != nil {
		return e
	}
	if actor.Role != "platform_admin" && (id == "platform_admin" || !accessCanDelegate(grants, desired)) {
		return fail(403, "forbidden")
	}
	return nil
}
func setAccessRole(ctx context.Context, tx pgx.Tx, tenant string, userID int64, id string) error {
	base, custom := "viewer", id
	if accessBuiltin(id) {
		base = id
		custom = ""
	}
	_, e := tx.Exec(ctx, "UPDATE adtr.users SET role=$1,role_id=$2 WHERE tenant_id=$3 AND id=$4", base, custom, tenant, userID)
	return e
}
func (s *Service) lockAccessTarget(ctx context.Context, tx pgx.Tx, actor User, username string) (AccessUser, error) {
	u, e := scanAccessUser(tx.QueryRow(ctx, "SELECT "+accessUserColumns+accessUserJoin+" WHERE u.tenant_id=$1 AND u.username=$2 FOR UPDATE OF u", actor.tenant, username))
	if errors.Is(e, pgx.ErrNoRows) {
		return u, fail(404, "not_found")
	}
	return u, e
}
func (s *Service) protectAccessAdmin(ctx context.Context, tx pgx.Tx, actor User, target AccessUser, nextRole string, disabled, deleted bool) error {
	if target.ID == actor.ID && (deleted || disabled) {
		return fail(409, "self_protection")
	}
	if actor.Role != "platform_admin" && (target.Role == "platform_admin" || nextRole == "platform_admin") {
		return fail(403, "forbidden")
	}
	if target.Role == "platform_admin" && !target.Disabled && (deleted || disabled || nextRole != "platform_admin") {
		var count int
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM adtr.users WHERE tenant_id=$1 AND role='platform_admin' AND NOT disabled", actor.tenant).Scan(&count); e != nil {
			return e
		}
		if count <= 1 {
			return fail(409, "last_administrator")
		}
	}
	return nil
}
func accessMutation(revoked bool) map[string]any {
	return map[string]any{"result": "SUCCESS", "sessionRevoked": revoked}
}
func (s *Service) mutateAccessUser(ctx context.Context, tx pgx.Tx, actor User, actorRole string, grants map[string]AccessAuth, path string, in accessRequest) (any, bool, error) {
	username, e := normalizeUsername(in.Username)
	if e != nil {
		return nil, false, fail(400, "invalid_input")
	}
	if e = validateAccessProfile(in); e != nil {
		return nil, false, e
	}
	if path == "/users/create" {
		if !validPassword(in.Password) {
			return nil, false, fail(400, "invalid_input")
		}
		id, e := accessRequestedRole(in, "viewer")
		if e != nil {
			return nil, false, e
		}
		if e = s.accessMayAssign(ctx, tx, actor, grants, id); e != nil {
			return nil, false, e
		}
		// Assigning an explicit custom/admin role also requires role-write authority.
		if id != "viewer" && !grants["roles"].Writeable {
			return nil, false, fail(403, "forbidden")
		}
		hash, e := hashPassword(in.Password)
		if e != nil {
			return nil, false, e
		}
		base, custom := "viewer", id
		if accessBuiltin(id) {
			base = id
			custom = ""
		}
		var target int64
		e = tx.QueryRow(ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,pass_strength,role,role_id,mobile,email,remark,address,real_name,department,post,must_change,created_at,password_updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,true,$14,$14) RETURNING id`, actor.tenant, username, hash, passwordStrength(in.Password), base, custom, accessString(in.Mobile), accessString(in.Email), accessString(in.Remark), accessString(in.Address), accessString(in.RealName), accessString(in.Department), accessString(in.Post), s.now()).Scan(&target)
		if e != nil {
			return nil, false, e
		}
		if e = s.audit(ctx, tx, actor, "user_create", target); e != nil {
			return nil, false, e
		}
		out := accessMutation(false)
		out["ID"] = target
		return out, false, nil
	}
	target, e := s.lockAccessTarget(ctx, tx, actor, username)
	if e != nil {
		return nil, false, e
	}
	if a, ok := ctx.Value(auditContextKey{}).(*auditContext); ok {
		a.target = &target.ID
	}
	id, e := accessRequestedRole(in, target.RoleID)
	if e != nil {
		return nil, false, e
	}
	disabled := target.Disabled
	if in.Disabled != nil {
		disabled = *in.Disabled
	}
	if e = s.protectAccessAdmin(ctx, tx, actor, target, id, disabled, path == "/users/delete"); e != nil {
		return nil, false, e
	}
	if path == "/users/delete" {
		if _, e = tx.Exec(ctx, "DELETE FROM adtr.users WHERE tenant_id=$1 AND id=$2", actor.tenant, target.ID); e != nil {
			return nil, false, e
		}
		if e = s.audit(ctx, tx, actor, "user_delete", target.ID); e != nil {
			return nil, false, e
		}
		return accessMutation(false), false, nil
	}
	// Reactivation restores the target role's authority even when roleID is omitted.
	// Route it through the same delegation ceiling as a new role assignment.
	if id != target.RoleID || target.Disabled && !disabled {
		if !grants["roles"].Writeable {
			return nil, false, fail(403, "forbidden")
		}
		if actor.Role != "platform_admin" && target.ID == actor.ID {
			return nil, false, fail(403, "self_role_change_forbidden")
		}
		if e = s.accessMayAssign(ctx, tx, actor, grants, id); e != nil {
			return nil, false, e
		}
	}
	next := target
	for _, p := range []struct {
		src *string
		dst *string
	}{{in.Mobile, &next.Mobile}, {in.Email, &next.Email}, {in.Remark, &next.Remark}, {in.Address, &next.Address}, {in.RealName, &next.RealName}, {in.Department, &next.Department}, {in.Post, &next.Post}} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}
	next.RoleID = id
	next.Disabled = disabled
	if reflect.DeepEqual(next, target) {
		return nil, false, fail(409, "no_change")
	}
	if _, e = tx.Exec(ctx, `UPDATE adtr.users SET mobile=$1,email=$2,remark=$3,address=$4,real_name=$5,department=$6,post=$7,disabled=$8 WHERE tenant_id=$9 AND id=$10`, next.Mobile, next.Email, next.Remark, next.Address, next.RealName, next.Department, next.Post, disabled, actor.tenant, target.ID); e != nil {
		return nil, false, e
	}
	if id != target.RoleID {
		if e = setAccessRole(ctx, tx, actor.tenant, target.ID, id); e != nil {
			return nil, false, e
		}
	}
	revoke := id != target.RoleID || disabled != target.Disabled
	if revoke {
		if _, e = tx.Exec(ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", target.ID); e != nil {
			return nil, false, e
		}
	}
	if e = s.audit(ctx, tx, actor, "user_update", target.ID); e != nil {
		return nil, false, e
	}
	if id != target.RoleID {
		if e = s.audit(ctx, tx, actor, "user_role_assign", target.ID); e != nil {
			return nil, false, e
		}
	}
	revoked := revoke && target.ID == actor.ID
	return accessMutation(revoked), revoked, nil
}
func (s *Service) assignAccessRoles(ctx context.Context, tx pgx.Tx, actor User, actorRole string, grants map[string]AccessAuth, assignments []accessAssignment) (any, bool, error) {
	if len(assignments) < 1 || len(assignments) > 100 {
		return nil, false, fail(400, "invalid_input")
	}
	seen := map[string]bool{}
	revoked := false
	changed := 0
	// One tenant advisory lock covers all targets, including last-admin checks.
	for _, a := range assignments {
		name, e := normalizeUsername(a.Username)
		if e != nil || seen[name] || !validRoleID(a.RoleID) {
			return nil, false, fail(400, "invalid_input")
		}
		seen[name] = true
		target, e := s.lockAccessTarget(ctx, tx, actor, name)
		if e != nil {
			return nil, false, e
		}
		if e = s.protectAccessAdmin(ctx, tx, actor, target, a.RoleID, target.Disabled, false); e != nil {
			return nil, false, e
		}
		if actor.Role != "platform_admin" && target.ID == actor.ID && a.RoleID != actorRole {
			return nil, false, fail(403, "self_role_change_forbidden")
		}
		if e = s.accessMayAssign(ctx, tx, actor, grants, a.RoleID); e != nil {
			return nil, false, e
		}
		if target.RoleID == a.RoleID {
			continue
		}
		if e = setAccessRole(ctx, tx, actor.tenant, target.ID, a.RoleID); e != nil {
			return nil, false, e
		}
		if _, e = tx.Exec(ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", target.ID); e != nil {
			return nil, false, e
		}
		if e = s.audit(ctx, tx, actor, "user_role_assign", target.ID); e != nil {
			return nil, false, e
		}
		changed++
		revoked = revoked || target.ID == actor.ID
	}
	if changed == 0 {
		return nil, false, fail(409, "no_change")
	}
	return accessMutation(revoked), revoked, nil
}
