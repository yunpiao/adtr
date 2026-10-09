package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/credentialuse"
)

// ServeAccessHTTP reuses durable identity, session and CSRF protections.
func (s *Service) ServeAccessHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path, ok := strings.CutPrefix(r.URL.Path, "/api/access")
	route, exists := accessRoutes[path]
	if !ok || !exists {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in accessRequest
	if route.method == "POST" {
		if r.Header.Get("Origin") != s.origin {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
		if e != nil || validateAccessBody(raw, route, &in) != nil || r.URL.RawQuery != "" {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	value, revoked, err := s.handleAccess(ctx, r, path, route, in)
	if err != nil {
		s.recordFailure(ctx, "access"+strings.ReplaceAll(path, "/", "_"))
		if credentialuse.IsGovernanceError(err) {
			err = fail(403, "credential_use_governance_required")
		}
		var f failure
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			err = fail(409, "already_exists")
		}
		if errors.As(err, &f) {
			if f.status == 429 {
				w.Header().Set("Retry-After", "900")
			}
			write(w, f.status, map[string]string{"error": f.code})
		} else {
			write(w, 500, map[string]string{"error": "internal"})
		}
		return
	}
	if revoked {
		s.cookie(w, "")
	}
	write(w, 200, value)
}
func validateAccessBody(raw []byte, route accessRoute, out *accessRequest) error {
	if !utf8.Valid(raw) {
		return fail(400, "invalid_input")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return fail(400, "invalid_input")
	}
	if strings.Contains(route.fields, "permissions") {
		if _, ok := fields["permissions"]; !ok {
			return fail(400, "invalid_input")
		}
	}
	allowed := " " + route.fields + " "
	if route.write {
		allowed += "actorPassword totpCode "
	}
	for k, v := range fields {
		if !strings.Contains(allowed, " "+k+" ") || string(v) == "null" {
			return fail(400, "invalid_input")
		}
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return fail(400, "invalid_input")
	}
	if route.write && (len(out.ActorPassword) > 256 || len(out.TOTPCode) != 6) {
		return fail(400, "invalid_input")
	}
	return nil
}

// authenticateAccess serializes against access revocation before acquiring the
// actor lock. The session is rechecked after locking to defeat stale snapshots.
func (s *Service) authenticateAccess(ctx context.Context, tx pgx.Tx, r *http.Request) (User, string, map[string]AccessAuth, error) {
	var empty User
	cookie, e := r.Cookie(s.cookieName())
	if e != nil || len(cookie.Value) != 43 {
		return empty, "", nil, fail(401, "unauthenticated")
	}
	var tenant string
	e = tx.QueryRow(ctx, "SELECT u.tenant_id FROM adtr.sessions s JOIN adtr.users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>$2 AND NOT u.disabled", digest(cookie.Value), s.now()).Scan(&tenant)
	if errors.Is(e, pgx.ErrNoRows) {
		return empty, "", nil, fail(401, "unauthenticated")
	}
	if e != nil {
		return empty, "", nil, e
	}
	if e = lockIdentityTenant(ctx, tx, tenant); e != nil {
		return empty, "", nil, e
	}
	u, e := s.readUser(tx.QueryRow(ctx, "SELECT "+columns+" FROM adtr.sessions s JOIN adtr.users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>$2 AND NOT u.disabled AND u.tenant_id=$3 FOR UPDATE OF u", digest(cookie.Value), s.now(), tenant))
	if errors.Is(e, pgx.ErrNoRows) {
		return empty, "", nil, fail(401, "unauthenticated")
	}
	if e != nil {
		return empty, "", nil, e
	}
	var active bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.sessions WHERE token_hash=$1 AND user_id=$2 AND expires_at>$3)", digest(cookie.Value), u.ID, s.now()).Scan(&active); e != nil {
		return empty, "", nil, e
	}
	if !active {
		return empty, "", nil, fail(401, "unauthenticated")
	}
	if a, ok := ctx.Value(auditContextKey{}).(*auditContext); ok {
		a.actor = &u.ID
		a.tenant = u.tenant
		a.username = u.Username
	}
	u.CSRF = csrfToken(s.key, cookie.Value)
	if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(u.CSRF)) != 1 {
		return empty, "", nil, fail(403, "forbidden")
	}
	if u.NeedChange {
		return empty, "", nil, fail(403, "password_change_required")
	}
	var roleID string
	if e = tx.QueryRow(ctx, "SELECT COALESCE(NULLIF(role_id,''),role) FROM adtr.users WHERE id=$1 AND tenant_id=$2", u.ID, u.tenant).Scan(&roleID); e != nil {
		return empty, "", nil, e
	}
	grants, e := loadAccessGrants(ctx, tx, u.tenant, roleID)
	return u, roleID, grants, e
}
func (s *Service) requireAccessProof(ctx context.Context, tx pgx.Tx, u *User, password, code string) error {
	if e := s.rate(ctx, "sensitive:"+strconv.FormatInt(u.ID, 10), 10); e != nil {
		return e
	}
	if !u.HasMFA {
		return fail(403, "mfa_required")
	}
	if len(password) > 256 || !checkPassword(u.passwordHash, password) || !s.mfa(u, code) {
		return fail(401, "invalid_credentials")
	}
	_, e := tx.Exec(ctx, "UPDATE adtr.users SET mfa_last_step=$1 WHERE id=$2 AND tenant_id=$3", u.lastStep, u.ID, u.tenant)
	return e
}
func (s *Service) handleAccess(ctx context.Context, r *http.Request, path string, route accessRoute, in accessRequest) (any, bool, error) {
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if e := s.rate(ctx, "ip:"+ip, 60); e != nil {
			return nil, false, e
		}
	}
	conn, e := pgx.ConnectConfig(ctx, s.database.Copy())
	if e != nil {
		return nil, false, e
	}
	defer conn.Close(ctx)
	tx, e := conn.Begin(ctx)
	if e != nil {
		return nil, false, e
	}
	defer tx.Rollback(ctx)
	u, roleID, grants, e := s.authenticateAccess(ctx, tx, r)
	if e != nil {
		return nil, false, e
	}
	if !grantAllows(grants, route) {
		return nil, false, fail(403, "forbidden")
	}
	if route.write {
		if e = s.requireAccessProof(ctx, tx, &u, in.ActorPassword, in.TOTPCode); e != nil {
			return nil, false, e
		}
		if e = setCredentialGovernance(ctx, tx, u, credentialuse.ManageRoles); e != nil {
			return nil, false, e
		}
	}
	var value any
	var revoked bool
	switch path {
	case "/users":
		value, e = s.listAccessUsers(ctx, tx, u, r.URL.Query())
	case "/users/exists":
		name, err := accessSingleQuery(r, "username")
		if err != nil {
			return nil, false, err
		}
		name, err = normalizeUsername(name)
		if err != nil {
			return nil, false, fail(400, "invalid_input")
		}
		var exists bool
		e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.users WHERE tenant_id=$1 AND username=$2)", u.tenant, name).Scan(&exists)
		value = map[string]bool{"result": exists}
	case "/users/create", "/users/update", "/users/delete":
		value, revoked, e = s.mutateAccessUser(ctx, tx, u, roleID, grants, path, in)
	case "/roles":
		value, e = s.listAccessRoles(ctx, tx, u, roleID, grants, r.URL.Query())
	case "/roles/detail":
		id, err := accessSingleQuery(r, "roleID")
		if err != nil {
			return nil, false, err
		}
		var role AccessRole
		role, e = s.readAccessRole(ctx, tx, u, roleID, grants, id)
		if e == nil {
			var p map[string]AccessAuth
			p, e = loadAccessGrants(ctx, tx, u.tenant, id)
			value = map[string]any{"role": role, "permissions": permissionNodes(p, false)}
		}
	case "/roles/exists":
		name, err := accessSingleQuery(r, "name")
		if err != nil {
			return nil, false, err
		}
		if !validAccessText(name, 50) || name == "" {
			return nil, false, fail(400, "invalid_input")
		}
		exists := strings.EqualFold(name, "platform_admin") || strings.EqualFold(name, "viewer")
		if !exists {
			e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=$1 AND lower(name)=lower($2))", u.tenant, name).Scan(&exists)
		}
		value = map[string]bool{"exists": exists}
	case "/roles/save", "/roles/delete", "/permissions/save":
		value, revoked, e = s.mutateAccessRole(ctx, tx, u, roleID, grants, path, in)
	case "/assignments":
		value, revoked, e = s.assignAccessRoles(ctx, tx, u, roleID, grants, in.UserRoles)
	case "/permissions":
		id, err := accessSingleQuery(r, "roleID")
		if err != nil {
			return nil, false, err
		}
		var p map[string]AccessAuth
		p, e = loadAccessGrants(ctx, tx, u.tenant, id)
		value = map[string]any{"permissions": permissionNodes(p, false)}
	case "/menu":
		if len(r.URL.Query()) != 0 {
			return nil, false, fail(400, "invalid_input")
		}
		value = map[string]any{"menu": permissionNodes(grants, true)}
	case "/check":
		if len(in.Paths) < 1 || len(in.Paths) > 100 {
			return nil, false, fail(400, "invalid_input")
		}
		results := make([]bool, len(in.Paths))
		for i, p := range in.Paths {
			if len(p) > 256 {
				return nil, false, fail(400, "invalid_input")
			}
			method, url, ok := strings.Cut(p, " ")
			suffix, prefix := strings.CutPrefix(url, "/api/access")
			rt, known := accessRoutes[suffix]
			results[i] = ok && prefix && known && method == rt.method && grantAllows(grants, rt)
			if ok && !prefix {
				results[i] = resourcePathAllowed(method, url, roleID, grants) || taskPathAllowed(method, url, grants) || auditPathAllowed(method, url, grants) || systemPathAllowed(method, url, u.tenant, grants) || schedulePathAllowed(method, url, grants) || archivePathAllowed(method, url, grants) || credentialUsePathAllowed(method, url, roleID, grants) || operationAccountPathAllowed(method, url, grants) || domainSelectionPathAllowed(method, url, grants) || domainAccessPathAllowed(method, url, roleID, grants) || operationalLogPathAllowed(method, url, u.tenant, grants) || directoryPathAllowed(method, url, grants) || directoryCredentialUsePathAllowed(method, url, roleID, grants) || directoryV2PathAllowed(method, url, grants) || directoryV2CredentialUsePathAllowed(method, url, roleID, grants)
			}
		}
		value = map[string]any{"results": results}
	}
	if e != nil {
		return nil, false, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, false, e
	}
	return value, revoked, nil
}
func accessSingleQuery(r *http.Request, key string) (string, error) {
	q := r.URL.Query()
	v, ok := q[key]
	if len(q) != 1 || !ok || len(v) != 1 || v[0] == "" {
		return "", fail(400, "invalid_input")
	}
	return v[0], nil
}
func loadAccessGrants(ctx context.Context, tx pgx.Tx, tenant, id string) (map[string]AccessAuth, error) {
	out := map[string]AccessAuth{}
	if id == "platform_admin" {
		for _, m := range accessMarks {
			out[m] = AccessAuth{true, true}
		}
		return out, nil
	}
	if id == "viewer" {
		return out, nil
	}
	if !validRoleID(id) {
		return nil, fail(400, "invalid_input")
	}
	var exists bool
	if e := tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=$1 AND id=$2)", tenant, id).Scan(&exists); e != nil {
		return nil, e
	}
	if !exists {
		return nil, fail(404, "not_found")
	}
	rows, e := tx.Query(ctx, "SELECT mark,readable,writeable FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2", tenant, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var a AccessAuth
		if e = rows.Scan(&m, &a.Readable, &a.Writeable); e != nil {
			return nil, e
		}
		out[m] = a
	}
	return out, rows.Err()
}
func permissionNodes(grants map[string]AccessAuth, onlyReadable bool) []AccessPermission {
	nodes := make([]AccessPermission, 0, len(accessMarks))
	names := map[string]string{"users": "Users", "roles": "Roles", "permissions": "Permissions", "tasks": "Tasks", "audit": "Audit", "audit_exports": "Audit exports", "system": "System", "schedules": "Schedules", "task_archive": "Task archive", "domains": "Domain connections", "operation_accounts": "Operation accounts", "system_logs": "Operational logs", "directory_assets": "Directory assets"}
	keys := make([]string, 0, len(accessRoutes))
	for p := range accessRoutes {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, mark := range accessMarks {
		a := grants[mark]
		if onlyReadable && !a.Readable {
			continue
		}
		paths := []AccessPath{}
		for _, p := range keys {
			r := accessRoutes[p]
			if r.mark != mark {
				continue
			}
			mode := "readable"
			if r.write {
				mode = "writeable"
			}
			paths = append(paths, AccessPath{r.method + " " + p, r.method + " /api/access" + p, mode})
		}
		if mark == "roles" {
			resourceKeys := []string{}
			for path, rt := range resourceRoutes {
				if rt.management && !rt.tenant {
					resourceKeys = append(resourceKeys, path)
				}
			}
			sort.Strings(resourceKeys)
			for _, path := range resourceKeys {
				rt := resourceRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/resources" + path, mode})
			}
		}
		if mark == "audit" || mark == "audit_exports" {
			auditKeys := []string{}
			for path := range auditRoutes {
				if strings.HasPrefix(path, "/exports") == (mark == "audit_exports") {
					auditKeys = append(auditKeys, path)
				}
			}
			sort.Strings(auditKeys)
			for _, path := range auditKeys {
				rt := auditRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/audit" + path, mode})
			}
		}
		if mark == "directory_assets" {
			useKeys := []string{}
			for path := range credentialUseRoutes {
				useKeys = append(useKeys, path)
			}
			sort.Strings(useKeys)
			for _, path := range useKeys {
				rt := credentialUseRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				name := "Directory credential governance (builtin admin): " + path
				if path == "/effective" {
					name = "Own directory-use eligibility"
				}
				paths = append(paths, AccessPath{name, rt.method + " /api/directory-credential-use" + path, mode})
				paths = append(paths, AccessPath{"Dictionary 2: " + name, rt.method + " /api/directory-credential-use/v2" + path, mode})
			}
			keys := []string{}
			for path := range directoryRoutes {
				keys = append(keys, path)
			}
			sort.Strings(keys)
			for _, path := range keys {
				rt := directoryRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/directory" + path, mode})
				paths = append(paths, AccessPath{"Dictionary 2: " + rt.method + " " + path, rt.method + " /api/directory/v2" + path, mode})
			}
		}
		if mark == "system_logs" {
			keys := []string{}
			for path := range operationalLogRoutes {
				keys = append(keys, path)
			}
			sort.Strings(keys)
			for _, path := range keys {
				rt := operationalLogRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/system/logs" + path, mode})
			}
		}
		if mark == "system" {
			systemKeys := []string{}
			for path := range systemRoutes {
				systemKeys = append(systemKeys, path)
			}
			sort.Strings(systemKeys)
			for _, path := range systemKeys {
				rt := systemRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/system" + path, mode})
			}
		}
		if mark == "schedules" {
			keys := []string{}
			for path := range scheduleRoutes {
				keys = append(keys, path)
			}
			sort.Strings(keys)
			for _, path := range keys {
				rt := scheduleRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/tasks/schedules" + path, mode})
			}
		}
		if mark == "task_archive" {
			keys := []string{}
			for path := range archiveRoutes {
				keys = append(keys, path)
			}
			sort.Strings(keys)
			for _, path := range keys {
				rt := archiveRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/tasks" + path, mode})
			}
		}
		if mark == "operation_accounts" {
			useKeys := []string{}
			for path := range credentialUseRoutes {
				useKeys = append(useKeys, path)
			}
			sort.Strings(useKeys)
			for _, path := range useKeys {
				rt := credentialUseRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				name := "Credential use governance (builtin admin): " + path
				if path == "/effective" {
					name = "Own credential-use eligibility"
				}
				paths = append(paths, AccessPath{name, rt.method + " /api/credential-use" + path, mode})
			}
			keys := []string{}
			for path := range operationAccountRoutes {
				keys = append(keys, path)
			}
			sort.Strings(keys)
			for _, path := range keys {
				rt := operationAccountRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/operation-accounts" + path, mode})
			}
		}
		if mark == "domains" {
			paths = append(paths, AccessPath{"Select configured domains", "GET /api/domain-selection", "readable"}, AccessPath{"Resolve configured selection", "GET /api/domain-selection/resolve", "readable"})
			keys := []string{}
			for path := range domainRoutes {
				keys = append(keys, path)
			}
			sort.Strings(keys)
			for _, path := range keys {
				rt := domainRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " " + path, rt.method + " /api/domains" + path, mode})
			}
		}
		if mark == "tasks" {
			taskKeys := []string{}
			for path := range taskRoutes {
				taskKeys = append(taskKeys, path)
			}
			sort.Strings(taskKeys)
			for _, path := range taskKeys {
				rt := taskRoutes[path]
				mode := "readable"
				if rt.write {
					mode = "writeable"
				}
				paths = append(paths, AccessPath{rt.method + " /tasks" + path, rt.method + " /api/tasks" + path, mode})
			}
		}
		nodes = append(nodes, AccessPermission{names[mark], mark, []AccessPermission{}, paths, a.Readable, a, AccessAuth{true, true}, mark})
	}
	return nodes
}
