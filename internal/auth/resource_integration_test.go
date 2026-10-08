//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const resourceTestPassword = "Synthetic Resource Password 123"
const resourceTestSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

var narrowResourceRole = strings.Repeat("n", 24)
var widerResourceRole = strings.Repeat("w", 24)

type resourceTestClient struct {
	cookie *http.Cookie
	csrf   string
	id     int64
}
type resourceFixture struct {
	t                              *testing.T
	ctx                            context.Context
	conn                           *pgx.Conn
	s                              *Service
	now                            time.Time
	admin, viewer, operator, other int64
}

func newResourceFixture(t *testing.T) *resourceFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; missing PostgreSQL is a failure, not a skip")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	owner, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	db := "resource_test_" + strings.ToLower(randomToken(9))
	db = strings.NewReplacer("-", "a", "_", "b").Replace(db)
	quoted := pgx.Identifier{db}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		owner.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := owner.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
		owner.Close(ctx)
	})
	cfg = cfg.Copy()
	cfg.Database = db
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	if _, err = conn.Exec(ctx, "CREATE SCHEMA adtr;"+Schema+SchemaV3+ResourceSchema+TaskPermissionSchema+AuditPermissionMarks+SystemPermissionMarks+SchedulePermissionMarks+DomainPermissionMarks+OperationAccountPermissionMarks+OperationalLogPermissionMarks+DirectoryPermissionMarks); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfg, []byte(strings.Repeat("r", 32)), "http://localhost:8080", true)
	if err != nil {
		t.Fatal(err)
	}
	f := &resourceFixture{t: t, ctx: ctx, conn: conn, s: s, now: time.Now().UTC().Truncate(time.Second)}
	s.now = func() time.Time { return f.now }
	hash, err := hashPassword(resourceTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	f.exec("INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('one',$1,'Narrow'),('one',$2,'Wider'),('two',$2,'Foreign')", narrowResourceRole, widerResourceRole)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'roles',true,true),('one',$1,'users',true,true),('one',$2,'roles',true,true),('one',$2,'users',true,true),('one',$1,'permissions',true,true),('one',$2,'permissions',true,true)", narrowResourceRole, widerResourceRole)
	for _, x := range []struct {
		dest                       *int64
		name, tenant, role, custom string
	}{{&f.admin, "admin", "one", "platform_admin", ""}, {&f.viewer, "viewer", "one", "viewer", ""}, {&f.operator, "operator", "one", "viewer", narrowResourceRole}, {&f.other, "other", "two", "platform_admin", ""}} {
		if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,password_updated_at) VALUES($1,$2,$3,$4,$5,false,$6) RETURNING id", x.tenant, x.name, hash, x.role, x.custom, f.now).Scan(x.dest); err != nil {
			t.Fatal(err)
		}
		secret, e := seal(s.key, resourceTestSecret, strconv.FormatInt(*x.dest, 10))
		if e != nil {
			t.Fatal(e)
		}
		f.exec("UPDATE adtr.users SET mfa_secret=$1 WHERE id=$2", secret, *x.dest)
	}
	f.exec("INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('one','domain-a','Synthetic A'),('one','domain-b','Synthetic B'),('two','domain-a','Foreign same ID'),('two','foreign-only','Synthetic foreign')")
	f.exec("INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('one',10,$1,'synthetic-one','Tenant One'),('two',10,$1,'synthetic-two','Tenant Two')", f.now.Add(30*24*time.Hour).Unix())
	return f
}
func (f *resourceFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *resourceFixture) client(id int64) *resourceTestClient {
	f.t.Helper()
	token := randomToken(32)
	f.exec("INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", digest(token), id, f.now.Add(8*time.Hour))
	return &resourceTestClient{&http.Cookie{Name: f.s.cookieName(), Value: token}, csrfToken(f.s.key, token), id}
}
func (f *resourceFixture) call(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method := "GET"
	raw := []byte{}
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, "/api/resources"+path, strings.NewReader(string(raw)))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Origin", f.s.origin)
	req.Header.Set("Content-Type", "application/json")
	if c != nil {
		req.AddCookie(c.cookie)
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.ServeResources(w, req)
	if w.Code != want {
		f.t.Fatalf("%s got %d want %d body=%s", path, w.Code, want, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *resourceFixture) mutate(id int64, path string, body map[string]any, want int) (map[string]any, *resourceTestClient) {
	f.t.Helper()
	f.now = f.now.Add(16 * time.Minute)
	c := f.client(id)
	code, _ := totp(resourceTestSecret, f.now.Unix()/30)
	body["actorPassword"] = resourceTestPassword
	body["totpCode"] = code
	return f.call(c, path, body, want), c
}
func resourceTestMeta(name string, ids ...string) map[string]any {
	datas := []map[string]any{}
	if len(ids) > 0 {
		datas = append(datas, map[string]any{"appName": "ad", "resources": ids})
	}
	return map[string]any{"name": name, "mark": "synthetic", "datas": datas}
}
func resourceTestChecks(groups ...[]string) map[string]any {
	rs := []map[string]any{}
	for _, g := range groups {
		rs = append(rs, map[string]any{"application": "ad", "dataResource": g})
	}
	return map[string]any{"resourceType": 2, "resources": rs}
}
func checkResourceResults(t *testing.T, out map[string]any, wants ...bool) {
	t.Helper()
	got := out["results"].([]any)
	if len(got) != len(wants) {
		t.Fatal(got, wants)
	}
	for i, w := range wants {
		if got[i] != w {
			t.Fatal(got, wants)
		}
	}
	if out["authorizationScopeOnly"] != true {
		t.Fatal("scope not distinguished from AD consumption")
	}
}
func (f *resourceFixture) seedGroup(tenant, id, role string, domains ...string) {
	f.exec("INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES($1,$2,$2)", tenant, id)
	for _, d := range domains {
		f.exec("INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,$2,$3)", tenant, id, d)
	}
	f.exec("INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES($1,$2,$3)", tenant, id, role)
}

func TestResourceDurableCRUDIsolationAndBuiltinGrants(t *testing.T) {
	f := newResourceFixture(t)
	checkResourceResults(t, f.call(f.client(f.admin), "/check", resourceTestChecks([]string{"domain-a"}), 200), false)
	f.seedGroup("two", "foreign-group", "platform_admin", "domain-a", "foreign-only")
	checkResourceResults(t, f.call(f.client(f.other), "/check", resourceTestChecks([]string{"domain-a"}), 200), true)
	f.call(f.client(f.admin), "/groups/detail?id=foreign-group", nil, 404)
	f.mutate(f.admin, "/groups/create", map[string]any{"meta": resourceTestMeta("Foreign", "foreign-only")}, 422)
	f.mutate(f.admin, "/groups/create", map[string]any{"meta": resourceTestMeta("Missing", "unregistered")}, 422)
	made, _ := f.mutate(f.admin, "/groups/create", map[string]any{"meta": resourceTestMeta("Team%_", "domain-a")}, 200)
	id := made["id"].(string)
	var stored string
	if err := f.conn.QueryRow(f.ctx, "SELECT domain_id FROM adtr.resource_group_members WHERE tenant_id='one' AND group_id=$1", id).Scan(&stored); err != nil || stored != "domain-a" {
		t.Fatal(stored, err)
	}
	f.mutate(f.admin, "/groups/create", map[string]any{"meta": resourceTestMeta("team%_", "domain-b")}, 409)
	detail := f.call(f.client(f.admin), "/groups/detail?id="+id, nil, 200)["meta"].(map[string]any)
	if detail["name"] != "Team%_" || detail["applyRoleCount"] != float64(0) || len(detail["datas"].([]any)) != 1 {
		t.Fatal(detail)
	}
	list := f.call(f.client(f.admin), "/groups?name=%25_&pageIdx=1&pageSize=1&sort=true", nil, 200)
	if len(list["metas"].([]any)) != 1 || list["page"].(map[string]any)["total"] != float64(1) || list["exhausted"] != true {
		t.Fatal(list)
	}
	exists := f.call(f.client(f.admin), "/groups/exists?name=TEAM%25_", nil, 200)
	if exists["isExist"] != true {
		t.Fatal(exists)
	}
	viewer := f.client(f.viewer)
	f.mutate(f.admin, "/groups/assign", map[string]any{"id": id, "roleIds": []string{"viewer"}}, 200)
	f.call(viewer, "/grants", nil, 401)
	checkResourceResults(t, f.call(f.client(f.viewer), "/check", resourceTestChecks([]string{"domain-a"}, []string{"domain-b"}, []string{"domain-a", "domain-b"}, []string{"foreign-only"}), 200), true, false, false, false)
	f.call(f.client(f.viewer), "/groups", nil, 403)
	roles := f.call(f.client(f.admin), "/groups/roles?id="+id+"&pageSize=-1", nil, 200)
	if len(roles["details"].([]any)) != 1 || roles["details"].([]any)[0].(map[string]any)["id"] != "viewer" {
		t.Fatal(roles)
	}
	assigned, oldAdmin := f.mutate(f.admin, "/groups/assign", map[string]any{"id": id, "roleIds": []string{"viewer", "platform_admin"}}, 200)
	if assigned["sessionRevoked"] != true {
		t.Fatal(assigned)
	}
	f.call(oldAdmin, "/grants", nil, 401)
	checkResourceResults(t, f.call(f.client(f.admin), "/check", resourceTestChecks([]string{"domain-a"}, []string{"domain-b"}), 200), true, false)
	oldViewer := f.client(f.viewer)
	f.mutate(f.admin, "/groups/update", map[string]any{"id": id, "meta": resourceTestMeta("Renamed", "domain-b")}, 200)
	f.call(oldViewer, "/grants", nil, 401)
	checkResourceResults(t, f.call(f.client(f.viewer), "/check", resourceTestChecks([]string{"domain-a"}, []string{"domain-b"}), 200), false, true)
	f.mutate(f.admin, "/groups/update", map[string]any{"id": id, "meta": resourceTestMeta("Renamed", "domain-b")}, 409)
	f.mutate(f.admin, "/groups/assign", map[string]any{"id": id, "roleIds": []string{}}, 200)
	checkResourceResults(t, f.call(f.client(f.admin), "/check", resourceTestChecks([]string{"domain-b"}), 200), false)
	f.mutate(f.admin, "/groups/delete", map[string]any{"id": id}, 200)
	f.call(f.client(f.admin), "/groups/detail?id="+id, nil, 404)
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.resource_group_members WHERE group_id=$1", id).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	f.mutate(f.admin, "/groups/create", map[string]any{"meta": resourceTestMeta("Empty")}, 200)
	if _, err := f.conn.Exec(f.ctx, "UPDATE adtr.resource_audit SET action='tamper'"); err == nil {
		t.Fatal("audit mutable")
	}
	if _, err := f.conn.Exec(f.ctx, "TRUNCATE adtr.resource_audit"); err == nil {
		t.Fatal("audit truncation allowed")
	}
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.resource_audit WHERE actor_id=$1 AND tenant_id='one' AND target_id=$2", f.admin, id).Scan(&count); err != nil || count < 5 {
		t.Fatal("missing durable object audit", count, err)
	}
}

func TestResourceTenantConfigurationExpiryAndCatalogue(t *testing.T) {
	f := newResourceFixture(t)
	f.seedGroup("one", "scope", "viewer", "domain-a")
	f.exec("DELETE FROM adtr.resource_tenant_config WHERE tenant_id='one'")
	f.call(f.client(f.viewer), "/grants", nil, 409)
	out, c := f.mutate(f.admin, "/tenant/save", map[string]any{"maxAdCount": 2, "expireTime": f.now.Add(48 * time.Hour).Unix(), "uid": "synthetic-customer", "name": "配置"}, 200)
	if out["sessionRevoked"] != true {
		t.Fatal(out)
	}
	f.call(c, "/tenant", nil, 401)
	cfg := f.call(f.client(f.admin), "/tenant", nil, 200)
	if cfg["uid"] != "synthetic-customer" || cfg["name"] != "配置" || cfg["maxAdCount"] != float64(2) {
		t.Fatal(cfg)
	}
	f.mutate(f.admin, "/tenant/save", map[string]any{"maxAdCount": 1, "expireTime": f.now.Add(48 * time.Hour).Unix(), "uid": "synthetic-customer", "name": "配置"}, 409)
	f.call(f.client(f.operator), "/tenant", nil, 403)
	f.mutate(f.operator, "/tenant/save", map[string]any{"maxAdCount": 10, "expireTime": f.now.Add(48 * time.Hour).Unix(), "uid": "forged", "name": "No"}, 403)
	// UID is customer metadata; even another tenant's UID cannot change session scope.
	f.mutate(f.admin, "/tenant/save", map[string]any{"maxAdCount": 2, "expireTime": f.now.Add(48 * time.Hour).Unix(), "uid": "synthetic-two", "name": "Local"}, 200)
	checkResourceResults(t, f.call(f.client(f.viewer), "/check", resourceTestChecks([]string{"domain-a"}, []string{"foreign-only"}), 200), true, false)
	f.exec("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='domain-a'")
	checkResourceResults(t, f.call(f.client(f.viewer), "/check", resourceTestChecks([]string{"domain-a"}), 200), false)
	f.exec("UPDATE adtr.resource_domains SET active=true WHERE tenant_id='one' AND id='domain-a'")
	f.mutate(f.admin, "/tenant/save", map[string]any{"maxAdCount": 2, "expireTime": f.now.Unix(), "uid": "synthetic-two", "name": "Expired"}, 200)
	f.call(f.client(f.viewer), "/grants", nil, 403)
	// Management remains available to repair expiry without granting data access.
	f.call(f.client(f.admin), "/tenant", nil, 200)
	f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1,max_ad_count=0 WHERE tenant_id='one'", f.now.Add(time.Hour).Unix())
	checkResourceResults(t, f.call(f.client(f.viewer), "/check", resourceTestChecks([]string{"domain-a"}), 200), false)
	f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=1 WHERE tenant_id='one'")
	f.call(f.client(f.viewer), "/grants", nil, 403)
}

func TestResourceReferentialBoundariesAtomicityAndDelegation(t *testing.T) {
	f := newResourceFixture(t)
	f.seedGroup("one", "narrow-group", narrowResourceRole, "domain-a")
	f.seedGroup("one", "wide-group", widerResourceRole, "domain-b")
	f.seedGroup("two", "foreign-group", "platform_admin", "foreign-only")
	for _, sql := range []string{
		"INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('one','narrow-group','foreign-only')",
		"INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('one','foreign-group','viewer')",
		"INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('one','narrow-group','missing-role')",
	} {
		if _, err := f.conn.Exec(f.ctx, sql); err == nil {
			t.Fatal("cross-tenant or dangling relation accepted", sql)
		}
	}
	f.mutate(f.operator, "/groups/update", map[string]any{"id": "narrow-group", "meta": resourceTestMeta("Own", "domain-a")}, 403)
	f.mutate(f.operator, "/groups/create", map[string]any{"meta": resourceTestMeta("Beyond", "domain-b")}, 403)
	f.mutate(f.operator, "/groups/assign", map[string]any{"id": "wide-group", "roleIds": []string{widerResourceRole, "viewer"}}, 403)
	f.mutate(f.operator, "/groups/assign", map[string]any{"id": "wide-group", "roleIds": []string{}}, 403)
	f.mutate(f.operator, "/groups/assign", map[string]any{"id": "wide-group", "roleIds": []string{narrowResourceRole}}, 403)
	f.mutate(f.operator, "/groups/assign", map[string]any{"id": "wide-group", "roleIds": []string{"platform_admin"}}, 403)
	f.mutate(f.admin, "/groups/assign", map[string]any{"id": "narrow-group", "roleIds": []string{"viewer", strings.Repeat("q", 24)}}, 404)
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.resource_role_groups WHERE tenant_id='one' AND group_id='narrow-group' AND role_id=$1", narrowResourceRole).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial association replacement", count, err)
	}
	// Forced database audit failure must roll back both membership and fresh TOTP.
	f.exec(`CREATE FUNCTION adtr.fail_resource_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit fault'; END; $$; CREATE TRIGGER resource_audit_fault BEFORE INSERT ON adtr.resource_audit FOR EACH ROW EXECUTE FUNCTION adtr.fail_resource_audit()`)
	f.mutate(f.admin, "/groups/create", map[string]any{"meta": resourceTestMeta("Rollback", "domain-a")}, 500)
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.resource_groups WHERE tenant_id='one' AND name='Rollback'").Scan(&count); err != nil || count != 0 {
		t.Fatal("unaudited mutation persisted", count, err)
	}
	f.exec("DROP TRIGGER resource_audit_fault ON adtr.resource_audit")
	// The role FK removes dormant grants when an unassigned custom role is deleted.
	f.exec("DELETE FROM adtr.access_roles WHERE tenant_id='one' AND id=$1", widerResourceRole)
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.resource_role_groups WHERE tenant_id='one' AND role_id=$1", widerResourceRole).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted role retained grants", count, err)
	}
}

func TestResourceAuthenticationAndConcurrentFreshProof(t *testing.T) {
	f := newResourceFixture(t)
	f.call(nil, "/groups", nil, 401)

	f.call(f.client(f.viewer), "/check", map[string]any{"resourceType": 1, "resources": []map[string]any{{"application": "ad", "dataResource": []string{"domain-a"}}}}, 422)
	f.call(f.client(f.viewer), "/check", map[string]any{"resourceType": 2, "resources": []map[string]any{{"application": "unknown", "dataResource": []string{"domain-a"}}}}, 422)
	f.call(f.client(f.viewer), "/check", map[string]any{"resourceType": 2, "token": "forged", "resources": []map[string]any{}}, 400)
	c := f.client(f.admin)
	c.csrf = "wrong"
	f.call(c, "/check", resourceTestChecks([]string{"domain-a"}), 403)
	c = f.client(f.admin)
	f.call(c, "/groups/create", map[string]any{"meta": resourceTestMeta("NoProof")}, 401)
	f.exec("UPDATE adtr.users SET must_change=true WHERE id=$1", f.admin)
	f.call(c, "/groups", nil, 403)
	f.exec("UPDATE adtr.users SET must_change=false WHERE id=$1", f.admin)
	f.exec("UPDATE adtr.users SET disabled=true WHERE id=$1", f.admin)
	f.call(c, "/groups", nil, 401)
	f.exec("UPDATE adtr.users SET disabled=false WHERE id=$1", f.admin)
	f.exec("UPDATE adtr.sessions SET expires_at=$1 WHERE token_hash=$2", f.now, digest(c.cookie.Value))
	f.call(c, "/groups", nil, 401)
	f.now = f.now.Add(16 * time.Minute)
	one, two := f.client(f.admin), f.client(f.admin)
	code, _ := totp(resourceTestSecret, f.now.Unix()/30)
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for i, c := range []*resourceTestClient{one, two} {
		wg.Add(1)
		go func(i int, c *resourceTestClient) {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{"meta": resourceTestMeta(fmt.Sprintf("race-%d", i)), "actorPassword": resourceTestPassword, "totpCode": code})
			r := httptest.NewRequest("POST", "/api/resources/groups/create", strings.NewReader(string(raw)))
			r.Header.Set("Origin", f.s.origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", c.csrf)
			r.AddCookie(c.cookie)
			w := httptest.NewRecorder()
			f.s.ServeResources(w, r)
			results <- w.Code
		}(i, c)
	}
	wg.Wait()
	close(results)
	statuses := map[int]int{}
	for status := range results {
		statuses[status]++
	}
	if statuses[200] != 1 || statuses[401] != 1 {
		t.Fatal("TOTP replay race", statuses)
	}
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.resource_groups WHERE name LIKE 'race-%'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

// These assertions exercise the actual F45 endpoints after the F47 integration
// hooks described in docs/resource-contract.md are installed. They intentionally
// fail if a caller can switch to a function-equal but domain-broader role.
func TestResourceCrossModuleRoleDelegation(t *testing.T) {
	f := newResourceFixture(t)
	f.seedGroup("one", "narrow-group", narrowResourceRole, "domain-a")
	f.seedGroup("one", "wide-group", widerResourceRole, "domain-b")
	f.exec("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='domain-b'")
	f.exec("UPDATE adtr.resource_tenant_config SET expire_time=0 WHERE tenant_id='one'")

	f.exec("INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,disabled,password_updated_at) SELECT tenant_id,'disabled-wide',password_hash,'viewer',$1,false,true,password_updated_at FROM adtr.users WHERE id=$2", widerResourceRole, f.viewer)
	for _, tc := range []struct {
		path string
		body map[string]any
	}{
		{"/assignments", map[string]any{"userRoles": []map[string]string{{"username": "viewer", "roleID": widerResourceRole}}}},
		{"/users/create", map[string]any{"username": "escalated", "password": "Synthetic Newly Created 123", "roleID": widerResourceRole}},
		{"/users/update", map[string]any{"username": "viewer", "roleID": widerResourceRole}},
		{"/users/update", map[string]any{"username": "disabled-wide", "disabled": false}},
		{"/permissions/save", map[string]any{"roleID": widerResourceRole, "permissions": []map[string]any{{"mark": "roles", "auth": map[string]bool{"readable": true, "writeable": true}}}}},
		{"/roles/save", map[string]any{"roleID": widerResourceRole, "roleName": "Wider", "remark": "changed", "permissions": []map[string]any{{"mark": "roles", "auth": map[string]bool{"readable": true, "writeable": true}}}}},
	} {
		f.now = f.now.Add(16 * time.Minute)
		c := f.client(f.operator)
		code, _ := totp(resourceTestSecret, f.now.Unix()/30)
		tc.body["actorPassword"] = resourceTestPassword
		tc.body["totpCode"] = code
		raw, _ := json.Marshal(tc.body)
		r := httptest.NewRequest("POST", "/api/access"+tc.path, strings.NewReader(string(raw)))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", c.csrf)
		r.AddCookie(c.cookie)
		w := httptest.NewRecorder()
		f.s.ServeAccessHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s latent scope escalation: %d %s", tc.path, w.Code, w.Body)
		}

		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] != "resource_delegation_forbidden" {
			t.Fatalf("%s did not enforce resource-role ceiling: %s", tc.path, w.Body)
		}
	}
	var role string
	if err := f.conn.QueryRow(f.ctx, "SELECT role_id FROM adtr.users WHERE id=$1", f.operator).Scan(&role); err != nil || role != narrowResourceRole {
		t.Fatal("role changed across rejected request", role, err)
	}

	var disabled bool
	if err := f.conn.QueryRow(f.ctx, "SELECT disabled FROM adtr.users WHERE username='disabled-wide'").Scan(&disabled); err != nil || !disabled {
		t.Fatal("broader user reactivated across rejected request", disabled, err)
	}
}

func TestResourceRevocationWaitsForConcurrentSessionCreation(t *testing.T) {
	f := newResourceFixture(t)
	f.seedGroup("one", "scope", "viewer", "domain-a")
	f.now = f.now.Add(16 * time.Minute)
	c := f.client(f.admin)
	code, _ := totp(resourceTestSecret, f.now.Unix()/30)
	blocker, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close(f.ctx)
	tx, err := blocker.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, "SELECT id FROM adtr.users WHERE id=$1 FOR UPDATE", f.viewer); err != nil {
		t.Fatal(err)
	}
	pending := randomToken(32)
	if _, err = tx.Exec(f.ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", digest(pending), f.viewer, f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		raw, _ := json.Marshal(map[string]any{"id": "scope", "meta": resourceTestMeta("Updated", "domain-b"), "actorPassword": resourceTestPassword, "totpCode": code})
		r := httptest.NewRequest("POST", "/api/resources/groups/update", strings.NewReader(string(raw)))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", c.csrf)
		r.AddCookie(c.cookie)
		w := httptest.NewRecorder()
		f.s.ServeResources(w, r)
		done <- w
	}()
	waiting := false
	deadline := time.Now().Add(5 * time.Second)
	for !waiting && time.Now().Before(deadline) {
		if err = f.conn.QueryRow(f.ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND position('FOR UPDATE' in query)>0)").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			select {
			case w := <-done:
				t.Fatalf("revocation returned before target row lock was released: %d %s", w.Code, w.Body)
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	if !waiting {
		t.Fatal("revocation did not wait on target row lock")
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatalf("revocation failed: %d %s", w.Code, w.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("revocation did not resume")
	}
	var count int
	if err = f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.sessions WHERE token_hash=$1", digest(pending)).Scan(&count); err != nil || count != 0 {
		t.Fatal("concurrently inserted old-scope session survived", count, err)
	}
}

func TestResourceAssociationCannotBypassFunctionDelegationCeiling(t *testing.T) {
	f := newResourceFixture(t)
	f.seedGroup("one", "owned", narrowResourceRole, "domain-a")
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark IN ('users','permissions')", narrowResourceRole)
	group, _ := f.mutate(f.operator, "/groups/create", map[string]any{"meta": resourceTestMeta("WithinOwnScope", "domain-a")}, 200)
	f.mutate(f.operator, "/groups/assign", map[string]any{"id": group["id"], "roleIds": []string{widerResourceRole}}, 403)
}
