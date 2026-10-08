package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"

	"github.com/jackc/pgx/v5"
)

const accessRolesSource = `(SELECT id,name,remark,created_at FROM adtr.access_roles WHERE tenant_id=$1 UNION ALL SELECT 'platform_admin','platform_admin','',timestamptz '1970-01-01 00:00:00+00' UNION ALL SELECT 'viewer','viewer','',timestamptz '1970-01-01 00:00:00+00') r`
const accessRoleColumns = `r.id,r.name,r.remark,r.created_at,(SELECT count(*) FROM adtr.users u WHERE u.tenant_id=$1 AND COALESCE(NULLIF(u.role_id,''),u.role)=r.id)`

func scanAccessRole(row pgx.Row, actorRole string, grants map[string]AccessAuth) (AccessRole, error) {
	r := AccessRole{DataSrc: map[string][]string{}}
	e := row.Scan(&r.ID, &r.Name, &r.Remark, &r.Created, &r.UserNum)
	r.AllowEdit = !accessBuiltin(r.ID) && r.ID != actorRole && grants["roles"].Writeable
	r.AllowDelete = r.AllowEdit && r.UserNum == 0
	r.Created = r.Created.UTC()
	return r, e
}
func (s *Service) listAccessRoles(ctx context.Context, tx pgx.Tx, actor User, actorRole string, grants map[string]AccessAuth, q url.Values) (any, error) {
	f, e := parseAccessFilter(q, true)
	if e != nil {
		return nil, e
	}
	args := []any{actor.tenant}
	where := ""
	if f.search != "" {
		args = append(args, f.search)
		where = " WHERE POSITION(lower($2) IN lower(r.name))>0"
	}
	var total int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM "+accessRolesSource+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	page, limit, offset, e := makeAccessPage(f, total)
	if e != nil {
		return nil, e
	}
	direction := " ASC"
	if f.sort < 0 {
		direction = " DESC"
	}
	args = append(args, limit, offset)
	query := "SELECT " + accessRoleColumns + " FROM " + accessRolesSource + where + " ORDER BY r.created_at" + direction + ",r.id" + direction + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	list := []AccessRole{}
	for rows.Next() {
		r, err := scanAccessRole(rows, actorRole, grants)
		if err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return map[string]any{"page": page, "list": list, "exhausted": offset+len(list) >= total}, nil
}
func (s *Service) readAccessRole(ctx context.Context, tx pgx.Tx, actor User, actorRole string, grants map[string]AccessAuth, id string) (AccessRole, error) {
	if !validRoleID(id) {
		return AccessRole{}, fail(400, "invalid_input")
	}
	r, e := scanAccessRole(tx.QueryRow(ctx, "SELECT "+accessRoleColumns+" FROM "+accessRolesSource+" WHERE r.id=$2", actor.tenant, id), actorRole, grants)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, fail(404, "not_found")
	}
	return r, e
}
func normalizedAccessGrants(grants map[string]AccessAuth) map[string]AccessAuth {
	out := map[string]AccessAuth{}
	for _, m := range accessMarks {
		out[m] = grants[m]
	}
	return out
}
func storeAccessGrants(ctx context.Context, tx pgx.Tx, tenant, id string, grants map[string]AccessAuth) error {
	if _, e := tx.Exec(ctx, "DELETE FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2", tenant, id); e != nil {
		return e
	}
	for _, m := range accessMarks {
		g := grants[m]
		if _, e := tx.Exec(ctx, "INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,$3,$4,$5)", tenant, id, m, g.Readable, g.Writeable); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) auditAccessRole(ctx context.Context, tx pgx.Tx, actor User, action, id string) error {
	// Roles use opaque IDs, so retain them in the action; input role IDs are validated.
	return s.audit(ctx, tx, actor, action+":"+id, actor.ID)
}
func (s *Service) mutateAccessRole(ctx context.Context, tx pgx.Tx, actor User, actorRole string, actorGrants map[string]AccessAuth, path string, in accessRequest) (any, bool, error) {
	if len(in.DataSrc) != 0 {
		return nil, false, fail(400, "unsupported_data_source_scope")
	}
	if in.Remark != nil && !validAccessText(*in.Remark, 150) {
		return nil, false, fail(400, "invalid_input")
	}
	id := accessString(in.RoleID)
	creating := path == "/roles/save" && in.RoleID == nil
	if !creating && !validRoleID(id) {
		return nil, false, fail(400, "invalid_input")
	}
	if accessBuiltin(id) {
		return nil, false, fail(409, "builtin_role")
	}
	if id == actorRole {
		return nil, false, fail(403, "self_role_change_forbidden")
	}
	if path == "/roles/save" && !validRoleName(in.RoleName) {
		return nil, false, fail(400, "invalid_input")
	}
	var current AccessRole
	var currentGrants map[string]AccessAuth
	var e error
	if !creating {
		current, e = s.readAccessRole(ctx, tx, actor, actorRole, actorGrants, id)
		if e != nil {
			return nil, false, e
		}
		currentGrants, e = loadAccessGrants(ctx, tx, actor.tenant, id)
		if e != nil {
			return nil, false, e
		}
	}
	if !creating && path != "/roles/delete" {
		if e = s.canDelegateResourceRole(ctx, tx, actor, actorRole, id); e != nil {
			return nil, false, e
		}
	}
	if path == "/roles/delete" {
		if current.UserNum != 0 {
			return nil, false, fail(409, "role_in_use")
		}
		if _, e = tx.Exec(ctx, "DELETE FROM adtr.access_roles WHERE tenant_id=$1 AND id=$2", actor.tenant, id); e != nil {
			return nil, false, e
		}
		if e = s.auditAccessRole(ctx, tx, actor, "role_delete", id); e != nil {
			return nil, false, e
		}
		return accessMutation(false), false, nil
	}
	desired, e := validateGrants(in.Permissions)
	if e != nil {
		return nil, false, e
	}
	if !accessCanDelegate(actorGrants, desired) {
		return nil, false, fail(403, "forbidden")
	}
	if path == "/roles/save" && !actorGrants["permissions"].Writeable {
		return nil, false, fail(403, "forbidden")
	}
	changedPerms := !reflect.DeepEqual(normalizedAccessGrants(currentGrants), normalizedAccessGrants(desired))
	if creating {
		id = randomToken(18)
		if _, e = tx.Exec(ctx, "INSERT INTO adtr.access_roles(tenant_id,id,name,remark,created_at) VALUES($1,$2,$3,$4,$5)", actor.tenant, id, in.RoleName, accessString(in.Remark), s.now()); e != nil {
			return nil, false, e
		}
	} else if path == "/roles/save" {
		if in.RoleName != current.Name {
			return nil, false, fail(409, "immutable_role_name")
		}
		remark := current.Remark
		if in.Remark != nil {
			remark = *in.Remark
		}
		if !changedPerms && remark == current.Remark {
			return nil, false, fail(409, "no_change")
		}
		if _, e = tx.Exec(ctx, "UPDATE adtr.access_roles SET remark=$1 WHERE tenant_id=$2 AND id=$3", remark, actor.tenant, id); e != nil {
			return nil, false, e
		}
	} else if !changedPerms {
		return nil, false, fail(409, "no_change")
	}
	// Metadata-only edits must not revoke background authorization epochs.
	if creating || changedPerms {
		if e = storeAccessGrants(ctx, tx, actor.tenant, id, desired); e != nil {
			return nil, false, e
		}
	}
	if changedPerms && !creating {
		// Authentication also locks users before creating/rotating sessions.
		// Acquire the same locks before taking the DELETE statement snapshot.
		rows, err := tx.Query(ctx, "SELECT id FROM adtr.users WHERE tenant_id=$1 AND role_id=$2 ORDER BY id FOR UPDATE", actor.tenant, id)
		if err != nil {
			return nil, false, err
		}
		for rows.Next() {
			var lockedID int64
			if err = rows.Scan(&lockedID); err != nil {
				rows.Close()
				return nil, false, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, false, err
		}

		if _, e = tx.Exec(ctx, "DELETE FROM adtr.sessions s USING adtr.users u WHERE s.user_id=u.id AND u.tenant_id=$1 AND u.role_id=$2", actor.tenant, id); e != nil {
			return nil, false, e
		}
	}
	action := "role_update"
	if creating {
		action = "role_create"
	}
	if path == "/permissions/save" {
		action = "permissions_update"
	}
	if e = s.auditAccessRole(ctx, tx, actor, action, id); e != nil {
		return nil, false, e
	}
	out := accessMutation(false)
	out["roleID"] = id
	return out, false, nil
}
