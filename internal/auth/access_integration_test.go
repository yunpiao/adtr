//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type accessTestClient struct {
	cookie *http.Cookie
	csrf   string
}
type accessFixture struct {
	t        *testing.T
	s        *Service
	conn     *pgx.Conn
	ctx      context.Context
	now      time.Time
	admin    accessTestClient
	secret   string
	password string
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; missing PostgreSQL is not a pass")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	admin, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	name := "access_test_" + strings.ToLower(strings.NewReplacer("-", "a", "_", "b").Replace(randomToken(9)))
	ident := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+ident); e != nil {
		admin.Close(ctx)
		t.Fatal(e)
	}
	cfg = cfg.Copy()
	cfg.Database = name
	conn, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		conn.Close(ctx)
		if _, err := admin.Exec(ctx, "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close(ctx)
	})
	if _, e = conn.Exec(ctx, "CREATE SCHEMA adtr;"+Schema+SchemaV3+ResourceSchema+TaskPermissionSchema+AuditPermissionMarks+SystemPermissionMarks+SchedulePermissionMarks+DomainPermissionMarks+OperationAccountPermissionMarks+OperationalLogPermissionMarks+DirectoryPermissionMarks); e != nil {
		t.Fatal(e)
	}
	s, e := New(cfg, []byte(strings.Repeat("x", 32)), "http://localhost:8080", true)
	if e != nil {
		t.Fatal(e)
	}
	f := &accessFixture{t: t, s: s, conn: conn, ctx: ctx, now: time.Now().UTC().Truncate(time.Second), password: "Changed Password 123"}
	s.now = func() time.Time { return f.now }
	if e = s.Bootstrap(ctx, "admin", "Initial Password 123"); e != nil {
		t.Fatal(e)
	}
	f.call(&f.admin, "/api/auth/login", map[string]any{"username": "admin", "password": "Initial Password 123"}, 200)
	f.call(&f.admin, "/api/access/users", nil, 403)
	f.call(&f.admin, "/api/auth/password", map[string]any{"oldPassword": "Initial Password 123", "newPassword": f.password}, 200)
	enrolled := f.call(&f.admin, "/api/auth/mfa/enroll", map[string]any{"password": f.password}, 200)
	f.secret = enrolled["secret"].(string)
	code, _ := totp(f.secret, f.now.Unix()/30)
	f.call(&f.admin, "/api/auth/mfa/confirm", map[string]any{"password": f.password, "secret": f.secret, "mfaCode": code}, 200)
	return f
}
func (f *accessFixture) call(c *accessTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method := "GET"
	raw := ""
	if body != nil {
		method = "POST"
		b, e := json.Marshal(body)
		if e != nil {
			f.t.Fatal(e)
		}
		raw = string(b)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(raw))
	req.RemoteAddr = "127.0.0.1:3456"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", f.s.origin)
	if c != nil {
		if c.cookie != nil {
			req.AddCookie(c.cookie)
		}
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	if strings.HasPrefix(path, "/api/auth/") {
		f.s.ServeHTTP(w, req)
	} else {
		f.s.ServeAccessHTTP(w, req)
	}
	if w.Code != want {
		f.t.Fatalf("%s status=%d want=%d body=%s", path, w.Code, want, w.Body.String())
	}
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		f.t.Fatal(e)
	}
	if c != nil {
		for _, cookie := range w.Result().Cookies() {
			c.cookie = cookie
		}
		if v, ok := out["csrfToken"].(string); ok {
			c.csrf = v
		}
	}
	return out
}
func (f *accessFixture) proof(body map[string]any) map[string]any {
	f.t.Helper()
	f.now = f.now.Add(30 * time.Second)
	// Separate each case's limiter budget. Existing auth integration tests exercise
	// durable lockout; access cases below still call the actual rate limiter.
	if _, e := f.conn.Exec(f.ctx, "DELETE FROM adtr.auth_attempts"); e != nil {
		f.t.Fatal(e)
	}
	code, _ := totp(f.secret, f.now.Unix()/30)
	body["actorPassword"] = f.password
	body["totpCode"] = code
	return body
}
func (f *accessFixture) sql(query string, args ...any) {
	f.t.Helper()
	if _, e := f.conn.Exec(f.ctx, query, args...); e != nil {
		f.t.Fatal(e)
	}
}
func (f *accessFixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if e := f.conn.QueryRow(f.ctx, query, args...).Scan(&n); e != nil {
		f.t.Fatal(e)
	}
	return n
}
func (f *accessFixture) createUser(name string, role string) {
	f.t.Helper()
	f.call(&f.admin, "/api/access/users/create", f.proof(map[string]any{"username": name, "password": "Created Password 123", "roleID": role, "mobile": "13812345678", "email": name + "@example.test", "remark": "created remark", "address": "Shanghai", "realName": "Synthetic User", "department": "Testing", "post": "Analyst"}), 200)
}
func (f *accessFixture) loginUser(name string) *accessTestClient {
	f.t.Helper()
	c := &accessTestClient{}
	f.call(c, "/api/auth/login", map[string]any{"username": name, "password": "Created Password 123"}, 200)
	f.call(c, "/api/access/menu", nil, 403)
	f.call(c, "/api/auth/password", map[string]any{"oldPassword": "Created Password 123", "newPassword": "User Changed Password 123"}, 200)
	return c
}
func accessGrantInput(mark string, read, write bool) map[string]any {
	return map[string]any{"mark": mark, "auth": map[string]any{"readable": read, "writeable": write}}
}
func (f *accessFixture) role(name string, grants ...map[string]any) string {
	f.t.Helper()
	if grants == nil {
		grants = []map[string]any{}
	}
	out := f.call(&f.admin, "/api/access/roles/save", f.proof(map[string]any{"roleName": name, "remark": "test role", "permissions": grants}), 200)
	return out["roleID"].(string)
}
func TestAccessDurableLifecycleAndFilters(t *testing.T) {
	f := newAccessFixture(t)
	f.call(nil, "/api/access/users", nil, 401)
	readRole := f.role("User readers", accessGrantInput("users", true, false))
	roleInfo := f.call(&f.admin, "/api/access/roles/detail?roleID="+readRole, nil, 200)
	if roleInfo["role"].(map[string]any)["name"] != "User readers" {
		t.Fatal("role detail persistence", roleInfo)
	}
	wantMarks := map[string]bool{
		"users": true, "roles": true, "permissions": true, "tasks": true,
		"audit": true, "audit_exports": true, "system": true, "schedules": true,
		"task_archive": true, "domains": true, "operation_accounts": true,
		"system_logs": true, "directory_assets": true,
	}
	permissions := roleInfo["permissions"].([]any)
	if len(permissions) != len(wantMarks) {
		t.Fatalf("role detail permission count=%d want=%d", len(permissions), len(wantMarks))
	}
	for _, value := range permissions {
		permission := value.(map[string]any)
		mark := permission["mark"].(string)
		if !wantMarks[mark] {
			t.Fatalf("unexpected or duplicate permission mark %q", mark)
		}
		delete(wantMarks, mark)
		grant := permission["auth"].(map[string]any)
		if grant["readable"] != (mark == "users") || grant["writeable"] != false || permission["checked"] != (mark == "users") {
			t.Fatalf("role detail changed persisted grant for %s: %v", mark, permission)
		}
	}
	f.createUser("alice", "viewer")
	alice := f.loginUser("alice")
	f.call(alice, "/api/access/users", nil, 403)
	menu := f.call(alice, "/api/access/menu", nil, 200)
	if len(menu["menu"].([]any)) != 0 {
		t.Fatal("viewer saw management menu")
	}
	check := f.call(alice, "/api/access/check", map[string]any{"paths": []string{"GET /api/access/users", "GET /api/access/menu", "DELETE /api/access/users", "GET /api/access/not-real"}}, 200)
	got := check["results"].([]any)
	if got[0] != false || got[1] != true || got[2] != false || got[3] != false {
		t.Fatal("default deny", got)
	}
	list := f.call(&f.admin, "/api/access/users?search=ali&filterRole=viewer&filterMfaStatus=stop&filterPassStrength=high&pageSize=1&sort=1", nil, 200)
	rows := list["List"].([]any)
	if len(rows) != 1 {
		t.Fatal("combined filters", list)
	}
	u := rows[0].(map[string]any)
	for k, want := range map[string]any{"username": "alice", "roleID": "viewer", "mobile": "13812345678", "email": "alice@example.test", "remark": "created remark", "address": "Shanghai", "realName": "Synthetic User", "department": "Testing", "post": "Analyst", "avatar": "", "priv": float64(3), "hasMfa": false, "disabled": false} {
		if u[k] != want {
			t.Fatalf("%s=%v want %v", k, u[k], want)
		}
	}
	created, e := time.Parse(time.RFC3339Nano, u["createTm"].(string))
	if e != nil {
		t.Fatal(e)
	}
	q := url.Values{"filterStartCreateTm": {created.Format(time.RFC3339Nano)}, "filterEndCreateTm": {created.Add(time.Second).Format(time.RFC3339Nano)}, "search": {"alice"}}
	if len(f.call(&f.admin, "/api/access/users?"+q.Encode(), nil, 200)["List"].([]any)) != 1 {
		t.Fatal("inclusive start filter")
	}
	q.Set("filterEndCreateTm", created.Format(time.RFC3339Nano))
	q.Set("filterStartCreateTm", created.Add(-time.Second).Format(time.RFC3339Nano))
	if len(f.call(&f.admin, "/api/access/users?"+q.Encode(), nil, 200)["List"].([]any)) != 0 {
		t.Fatal("exclusive end filter")
	}
	f.call(&f.admin, "/api/access/users?filterMfaStatus=disable", nil, 400)
	self := f.call(&f.admin, "/api/access/users?isSelf=true", nil, 200)["List"].([]any)
	if len(self) != 1 || self[0].(map[string]any)["username"] != "admin" {
		t.Fatal("self filter", self)
	}
	f.call(&f.admin, "/api/access/users/update", f.proof(map[string]any{"username": "alice", "remark": "changed", "mobile": "", "email": ""}), 200)
	persisted := f.call(&f.admin, "/api/access/users?search=alice", nil, 200)["List"].([]any)[0].(map[string]any)
	if persisted["remark"] != "changed" || persisted["mobile"] != "" || persisted["email"] != "" || persisted["department"] != "Testing" {
		t.Fatal("partial update did not persist", persisted)
	}
	f.call(&f.admin, "/api/access/users/update", f.proof(map[string]any{"username": "alice", "remark": "changed"}), 409)
	f.call(&f.admin, "/api/access/assignments", f.proof(map[string]any{"userRoles": []map[string]string{{"username": "alice", "roleID": readRole}}}), 200)
	f.call(alice, "/api/auth/me", nil, 401)
	f.call(alice, "/api/auth/login", map[string]any{"username": "alice", "password": "User Changed Password 123"}, 200)
	f.call(alice, "/api/access/users", nil, 200)
	f.call(alice, "/api/access/roles", nil, 403)
	f.call(alice, "/api/access/users/delete", map[string]any{"username": "admin", "actorPassword": "User Changed Password 123", "totpCode": "123456"}, 403)
	f.call(&f.admin, "/api/access/roles/delete", f.proof(map[string]any{"roleID": readRole}), 409)
	f.call(&f.admin, "/api/access/permissions/save", f.proof(map[string]any{"roleID": readRole, "permissions": []any{}}), 200)
	f.call(alice, "/api/auth/me", nil, 401)
	f.call(alice, "/api/auth/login", map[string]any{"username": "alice", "password": "User Changed Password 123"}, 200)
	f.call(alice, "/api/access/users", nil, 403)
	f.call(&f.admin, "/api/access/users/update", f.proof(map[string]any{"username": "alice", "disabled": true}), 200)
	f.call(alice, "/api/auth/me", nil, 401)
	f.call(alice, "/api/auth/login", map[string]any{"username": "alice", "password": "User Changed Password 123"}, 401)
	f.call(&f.admin, "/api/access/users/delete", f.proof(map[string]any{"username": "alice"}), 200)
	if f.count("SELECT count(*) FROM adtr.users WHERE username='alice'") != 0 {
		t.Fatal("delete did not persist")
	}
	f.call(&f.admin, "/api/access/roles/delete", f.proof(map[string]any{"roleID": readRole}), 200)
	f.call(&f.admin, "/api/access/roles/detail?roleID="+readRole, nil, 404)
	if f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='user_create'") != 1 || f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='user_role_assign'") != 1 || f.count("SELECT count(*) FROM adtr.auth_audit WHERE action=$1", "permissions_update:"+readRole) != 1 {
		t.Fatal("missing atomic mutation audits")
	}
	// A fresh service instance reads the same committed roles and profiles.
	again, e := New(f.s.database, f.s.key, f.s.origin, true)
	if e != nil {
		t.Fatal(e)
	}
	again.now = f.s.now
	f.s = again
	if f.call(&f.admin, "/api/access/users/exists?username=alice", nil, 200)["result"] != false {
		t.Fatal("restart observed deleted user")
	}
	f.sql("INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) SELECT 'default','bulk_'||g,password_hash,'viewer',false FROM generate_series(1,1001) g CROSS JOIN adtr.users u WHERE u.username='admin'")
	f.call(&f.admin, "/api/access/users?pageSize=-1", nil, 422)
	result := f.call(&f.admin, "/api/access/users?pageSize=10&pageIdx=101&search=bulk_", nil, 200)
	if len(result["List"].([]any)) != 1 || result["exhausted"] != true {
		t.Fatal("final page", result)
	}
	result = f.call(&f.admin, "/api/access/users?search=bulk%25", nil, 200)
	if len(result["List"].([]any)) != 0 {
		t.Fatal("search unexpectedly treated wildcard")
	}
}
func TestAccessTenantSafetyAndAtomicFailures(t *testing.T) {
	f := newAccessFixture(t)
	f.createUser("alice", "viewer")
	f.createUser("bob", "viewer")
	role := f.role("Read only", accessGrantInput("users", true, false))
	f.sql("INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) SELECT 'other','outsider',password_hash,'viewer',false FROM adtr.users WHERE username='admin'")
	otherRole := randomToken(18)
	f.sql("INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('other',$1,'Hidden role')", otherRole)
	if f.call(&f.admin, "/api/access/users/exists?username=outsider", nil, 200)["result"] != false {
		t.Fatal("cross tenant exists leak")
	}
	f.call(&f.admin, "/api/access/roles/detail?roleID="+otherRole, nil, 404)
	f.call(&f.admin, "/api/access/permissions?roleID="+otherRole, nil, 404)
	f.call(&f.admin, "/api/access/users/delete", f.proof(map[string]any{"username": "outsider"}), 404)
	f.call(&f.admin, "/api/access/users/update", f.proof(map[string]any{"username": "alice", "roleID": otherRole}), 404)
	f.call(&f.admin, "/api/access/assignments", f.proof(map[string]any{"userRoles": []map[string]string{{"username": "alice", "roleID": role}, {"username": "outsider", "roleID": role}}}), 404)
	if f.count("SELECT count(*) FROM adtr.users WHERE username='alice' AND role_id='' ") != 1 || f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='user_role_assign'") != 0 {
		t.Fatal("failed bulk was not atomic")
	}
	f.call(&f.admin, "/api/access/assignments", f.proof(map[string]any{"userRoles": []map[string]string{{"username": "alice", "roleID": role}, {"username": "ALICE", "roleID": role}}}), 400)
	f.call(&f.admin, "/api/access/users/delete", f.proof(map[string]any{"username": "admin"}), 409)
	f.call(&f.admin, "/api/access/users/update", f.proof(map[string]any{"username": "admin", "disabled": true}), 409)
	f.call(&f.admin, "/api/access/assignments", f.proof(map[string]any{"userRoles": []map[string]string{{"username": "admin", "roleID": "viewer"}}}), 409)
	f.call(&f.admin, "/api/access/roles/delete", f.proof(map[string]any{"roleID": "viewer"}), 409)
	f.call(&f.admin, "/api/access/roles/save", f.proof(map[string]any{"roleName": "Scoped", "dataSrc": map[string]any{"ad": []string{"arbitrary"}}, "permissions": []any{}}), 400)
	f.call(&f.admin, "/api/access/roles/save", f.proof(map[string]any{"roleID": role, "roleName": "Renamed", "permissions": []any{}}), 409)
	f.call(&f.admin, "/api/access/roles/save", f.proof(map[string]any{"roleName": "READ ONLY", "permissions": []any{}}), 409)
	// Direct database constraints also prohibit cross-tenant custom assignments.
	if _, e := f.conn.Exec(f.ctx, "UPDATE adtr.users SET role_id=$1 WHERE username='alice'", otherRole); e == nil {
		t.Fatal("cross tenant foreign key accepted")
	}
	bad := f.admin
	bad.csrf = "bad"
	f.call(&bad, "/api/access/users/delete", f.proof(map[string]any{"username": "alice"}), 403)
	body := f.proof(map[string]any{"username": "alice", "remark": "proof succeeded"})
	f.call(&f.admin, "/api/access/users/update", body, 200)
	body["remark"] = "replay"
	f.call(&f.admin, "/api/access/users/update", body, 401)
	f.sql(`CREATE FUNCTION adtr.test_reject_user_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='user_create' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER test_audit_failure BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.test_reject_user_audit()`)
	body = f.proof(map[string]any{"username": "rolledback", "password": "Created Password 123"})
	f.call(&f.admin, "/api/access/users/create", body, 500)
	if f.count("SELECT count(*) FROM adtr.users WHERE username='rolledback'") != 0 {
		t.Fatal("audit failure leaked mutation")
	}
	f.sql("DROP TRIGGER test_audit_failure ON adtr.auth_audit; DROP FUNCTION adtr.test_reject_user_audit()")
	f.call(&f.admin, "/api/access/users/create", body, 200) // failed mutation did not consume the proof
	f.call(&f.admin, "/api/access/users/delete", f.proof(map[string]any{"username": "missing"}), 404)
	f.call(&f.admin, "/api/access/assignments", f.proof(map[string]any{"userRoles": []map[string]string{{"username": "alice", "roleID": role}, {"username": "bob", "roleID": role}}}), 200)
	if f.count("SELECT count(*) FROM adtr.users WHERE role_id=$1", role) != 2 {
		t.Fatal("bulk persistence missing")
	}
}

func (f *accessFixture) enroll(c *accessTestClient, password string) string {
	f.t.Helper()
	f.sql("DELETE FROM adtr.auth_attempts")
	begin := f.call(c, "/api/auth/mfa/enroll", map[string]any{"password": password}, 200)
	secret := begin["secret"].(string)
	code, _ := totp(secret, f.now.Unix()/30)
	f.call(c, "/api/auth/mfa/confirm", map[string]any{"password": password, "secret": secret, "mfaCode": code}, 200)
	return secret
}
func TestAccessCustomRoleCannotElevate(t *testing.T) {
	f := newAccessFixture(t)
	operatorRole := f.role("User operators", accessGrantInput("users", true, true), accessGrantInput("roles", true, true))
	strongerRole := f.role("Permission managers", accessGrantInput("permissions", true, true))
	f.createUser("operator", operatorRole)
	operator := f.loginUser("operator")
	secret := f.enroll(operator, "User Changed Password 123")
	proof := func(body map[string]any) map[string]any {
		f.now = f.now.Add(30 * time.Second)
		f.sql("DELETE FROM adtr.auth_attempts")
		code, _ := totp(secret, f.now.Unix()/30)
		body["actorPassword"] = "User Changed Password 123"
		body["totpCode"] = code
		return body
	}
	f.call(operator, "/api/access/users/create", proof(map[string]any{"username": "victim", "password": "Created Password 123"}), 200)
	f.call(operator, "/api/access/users/create", proof(map[string]any{"username": "elevated", "password": "Created Password 123", "roleID": strongerRole}), 403)
	f.call(operator, "/api/access/users/create", proof(map[string]any{"username": "admin2", "password": "Created Password 123", "roleID": "platform_admin"}), 403)
	f.call(operator, "/api/access/users/update", proof(map[string]any{"username": "admin", "remark": "unsafe"}), 403)
	f.call(operator, "/api/access/assignments", proof(map[string]any{"userRoles": []map[string]string{{"username": "victim", "roleID": strongerRole}}}), 403)
	f.call(operator, "/api/access/assignments", proof(map[string]any{"userRoles": []map[string]string{{"username": "operator", "roleID": "viewer"}}}), 403)
	f.call(operator, "/api/access/roles/save", proof(map[string]any{"roleName": "unauthorized", "permissions": []any{}}), 403)
	f.call(operator, "/api/access/permissions/save", proof(map[string]any{"roleID": operatorRole, "permissions": []any{}}), 403)
	// Equivalent rights can be assigned to someone else, but the target's sessions
	// are invalidated and freshly authenticated permissions come from storage.
	f.call(operator, "/api/access/assignments", proof(map[string]any{"userRoles": []map[string]string{{"username": "victim", "roleID": operatorRole}}}), 200)
	if f.count("SELECT count(*) FROM adtr.users WHERE username='victim' AND role_id=$1", operatorRole) != 1 {
		t.Fatal("allowed delegation did not persist")
	}

	// Re-enabling a disabled account restores its stored function permissions.
	// Omitting roleID must not bypass the same ceiling as explicit assignment.
	f.createUser("broader", strongerRole)
	f.call(&f.admin, "/api/access/users/update", f.proof(map[string]any{"username": "broader", "disabled": true}), 200)
	f.call(operator, "/api/access/users/update", proof(map[string]any{"username": "broader", "disabled": false}), 403)
	f.call(operator, "/api/access/users/update", proof(map[string]any{"username": "broader", "disabled": false, "roleID": strongerRole}), 403)
	if f.count("SELECT count(*) FROM adtr.users WHERE username='broader' AND disabled") != 1 {
		t.Fatal("broader disabled account was reactivated")
	}
	f.call(operator, "/api/access/users/update", proof(map[string]any{"username": "victim", "disabled": true}), 200)
	f.call(operator, "/api/access/users/update", proof(map[string]any{"username": "victim", "disabled": false}), 200)
	if f.count("SELECT count(*) FROM adtr.users WHERE username='victim' AND NOT disabled AND role_id=$1", operatorRole) != 1 {
		t.Fatal("bounded reactivation did not persist")
	}
	// A users-only writer may disable accounts, but cannot restore even a
	// viewer account without role-assignment authority.
	f.createUser("plain", "viewer")
	f.call(&f.admin, "/api/access/users/update", f.proof(map[string]any{"username": "plain", "disabled": true}), 200)
	f.call(&f.admin, "/api/access/permissions/save", f.proof(map[string]any{"roleID": operatorRole, "permissions": []map[string]any{accessGrantInput("users", true, true)}}), 200)
	f.now = f.now.Add(30 * time.Second)
	code, _ := totp(secret, f.now.Unix()/30)
	f.call(operator, "/api/auth/login", map[string]any{"username": "operator", "password": "User Changed Password 123", "totpCode": code}, 200)
	f.call(operator, "/api/access/users/update", proof(map[string]any{"username": "plain", "disabled": false}), 403)
	if f.count("SELECT count(*) FROM adtr.users WHERE username='plain' AND disabled") != 1 {
		t.Fatal("reactivation bypassed role-write requirement")
	}
}

func TestAccessRoleRevocationWaitsForConcurrentAuthentication(t *testing.T) {
	f := newAccessFixture(t)
	role := f.role("Readers", accessGrantInput("users", true, false))
	f.createUser("reader", role)
	reader := f.loginUser("reader")
	// Pause a real authentication-style transaction after its user lock. The same
	// readUser/rotate helpers and sessions table are used by login and password/MFA.
	authConn, e := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if e != nil {
		t.Fatal(e)
	}
	defer authConn.Close(f.ctx)
	tx, e := authConn.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	u, e := f.s.readUser(tx.QueryRow(f.ctx, "SELECT "+columns+" FROM adtr.users u WHERE username='reader' FOR UPDATE"))
	if e != nil {
		t.Fatal(e)
	}
	body := f.proof(map[string]any{"roleID": role, "permissions": []any{}})
	raw, _ := json.Marshal(body)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/access/permissions/save", strings.NewReader(string(raw)))
		r.RemoteAddr = "127.0.0.1:3456"
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", f.admin.csrf)
		r.AddCookie(f.admin.cookie)
		w := httptest.NewRecorder()
		f.s.ServeAccessHTTP(w, r)
		done <- w
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if f.count("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM adtr.users WHERE tenant_id=%'") > 0 {
			blocked = true
			break
		}
		select {
		case w := <-done:
			t.Fatalf("revocation bypassed paused authentication: %d %s", w.Code, w.Body.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !blocked {
		t.Fatal("revocation did not acquire member user locks")
	}
	token, e := f.s.rotate(f.ctx, tx, &u)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatalf("revocation failed: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation remained blocked")
	}
	if f.count("SELECT count(*) FROM adtr.sessions WHERE user_id=$1", u.ID) != 0 {
		t.Fatal("concurrent rotated session survived revocation")
	}
	f.call(reader, "/api/auth/me", nil, 401)
	concurrent := &accessTestClient{cookie: &http.Cookie{Name: f.s.cookieName(), Value: token}, csrf: csrfToken(f.s.key, token)}
	f.call(concurrent, "/api/access/users", nil, 401)
}

func TestAccessConcurrentLastAdministratorProtection(t *testing.T) {
	f := newAccessFixture(t)
	f.createUser("second", "platform_admin")
	second := f.loginUser("second")
	secondSecret := f.enroll(second, "User Changed Password 123")
	f.now = f.now.Add(30 * time.Second)
	f.sql("DELETE FROM adtr.auth_attempts")
	code1, _ := totp(f.secret, f.now.Unix()/30)
	code2, _ := totp(secondSecret, f.now.Unix()/30)
	type attempt struct {
		c                      *accessTestClient
		password, code, target string
	}
	attempts := []attempt{{&f.admin, f.password, code1, "second"}, {second, "User Changed Password 123", code2, "admin"}}
	results := make(chan int, 2)
	var start sync.WaitGroup
	start.Add(1)
	for _, a := range attempts {
		go func(a attempt) {
			start.Wait()
			body, _ := json.Marshal(map[string]any{"userRoles": []map[string]string{{"username": a.target, "roleID": "viewer"}}, "actorPassword": a.password, "totpCode": a.code})
			r := httptest.NewRequest("POST", "/api/access/assignments", strings.NewReader(string(body)))
			r.RemoteAddr = "127.0.0.1:3456"
			r.Header.Set("Origin", f.s.origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", a.c.csrf)
			r.AddCookie(a.c.cookie)
			w := httptest.NewRecorder()
			f.s.ServeAccessHTTP(w, r)
			results <- w.Code
		}(a)
	}
	start.Done()
	one, two := <-results, <-results
	if !((one == 200 && two == 401) || (one == 401 && two == 200)) {
		t.Fatalf("unexpected concurrent demotion results %d/%d", one, two)
	}
	if f.count("SELECT count(*) FROM adtr.users WHERE tenant_id='default' AND role='platform_admin' AND NOT disabled") != 1 {
		t.Fatal("last administrator disappeared")
	}
}

func TestAccessAndAuthenticationShareTenantLockOrder(t *testing.T) {
	f := newAccessFixture(t)
	f.createUser("second", "platform_admin")
	second := f.loginUser("second")
	secondSecret := f.enroll(second, "User Changed Password 123")
	f.now = f.now.Add(30 * time.Second)
	f.sql("DELETE FROM adtr.auth_attempts")
	code1, _ := totp(f.secret, f.now.Unix()/30)
	code2, _ := totp(secondSecret, f.now.Unix()/30)
	type operation struct {
		client *accessTestClient
		path   string
		body   map[string]any
	}
	ops := []operation{
		{&f.admin, "/api/auth/reset-password", map[string]any{"username": "second", "newPassword": "Reset Password 123", "password": f.password, "totpCode": code1}},
		{second, "/api/access/users/update", map[string]any{"username": "admin", "remark": "concurrent safe profile change", "actorPassword": "User Changed Password 123", "totpCode": code2}},
	}
	gateConn, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer gateConn.Close(f.ctx)
	gate, err := gateConn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(f.ctx)
	if err = lockIdentityTenant(f.ctx, gate, "default"); err != nil {
		t.Fatal(err)
	}

	results := make(chan struct {
		path   string
		status int
	}, 2)
	start := make(chan struct{})
	for _, op := range ops {
		go func(op operation) {
			<-start
			raw, _ := json.Marshal(op.body)
			r := httptest.NewRequest("POST", op.path, strings.NewReader(string(raw)))
			r.RemoteAddr = "127.0.0.1:3456"
			r.Header.Set("Origin", f.s.origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", op.client.csrf)
			r.AddCookie(op.client.cookie)
			w := httptest.NewRecorder()
			if strings.HasPrefix(op.path, "/api/auth/") {
				f.s.ServeHTTP(w, r)
			} else {
				f.s.ServeAccessHTTP(w, r)
			}
			results <- struct {
				path   string
				status int
			}{op.path, w.Code}
		}(op)
	}
	close(start)
	// Both actual HTTP paths must reach the SAME first lock before either can
	// acquire an actor row. The pre-fix auth path completes through this gate.
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if f.count("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory'") >= 2 {
			blocked = true
			break
		}
		select {
		case result := <-results:
			t.Fatalf("%s bypassed tenant lock with status %d", result.path, result.status)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !blocked {
		t.Fatal("both HTTP paths did not wait for the shared tenant lock")
	}
	if err = gate.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}

	for range ops {
		select {
		case result := <-results:
			if result.path == "/api/auth/reset-password" && result.status != 200 || result.path == "/api/access/users/update" && result.status != 200 && result.status != 401 {
				t.Fatalf("cross-path lock-order failure %s=%d", result.path, result.status)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("cross-path operation timed out")
		}
	}
	if f.count("SELECT count(*) FROM adtr.users WHERE username='second' AND must_change") != 1 {
		t.Fatal("reset did not persist")
	}
}
