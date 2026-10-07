//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type taskFixture struct {
	*resourceFixture
	engine *tasks.Engine
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()
	f := newResourceFixture(t) // Own isolated PostgreSQL; fails, never skips if absent.
	f.exec(TaskAuthorizationSchema + tasks.Schema)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'tasks',true,true)", narrowResourceRole)
	f.exec(`CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version(singleton,version) VALUES(true,5);`)
	e, err := tasks.New(f.s.database, tasks.ProductionRegistry(), taskAuthorizer(f.s.now), 5)
	if err != nil {
		t.Fatal(err)
	}
	return &taskFixture{f, e}
}
func (f *taskFixture) callTask(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method := "GET"
	var raw []byte
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "/api/tasks"+path, strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:4321"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.TasksHandler(f.engine).ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("tasks %s got %d want %d: %s", path, w.Code, want, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *taskFixture) proof(body map[string]any) map[string]any {
	f.now = f.now.Add(31 * time.Second)
	code, _ := totp(resourceTestSecret, f.now.Unix()/30)
	body["actorPassword"] = resourceTestPassword
	body["totpCode"] = code
	return body
}
func taskSubmitBody(key string) map[string]any {
	return map[string]any{"taskName": "infrastructure.health", "domainId": "platform", "payloadVersion": 1, "payload": map[string]any{}, "idempotencyKey": key}
}
func (f *taskFixture) version(id int64) int64 {
	f.t.Helper()
	var v int64
	if err := f.conn.QueryRow(f.ctx, "SELECT authorization_version FROM adtr.users WHERE id=$1", id).Scan(&v); err != nil {
		f.t.Fatal(err)
	}
	return v
}
func taskOutputID(t *testing.T, out map[string]any) string {
	t.Helper()
	task, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatal(out)
	}
	id, ok := task["taskUUID"].(string)
	if !ok {
		t.Fatal(out)
	}
	return id
}
func (f *taskFixture) authorized(actor int64, tenant string, scope tasks.Scope, action tasks.Action) (string, error) {
	f.t.Helper()
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if err = f.engine.CheckSchemaTx(f.ctx, tx); err != nil {
		return "", err
	}
	v, err := taskAuthorizer(f.s.now)(f.ctx, tx, tasks.Principal{TenantID: tenant, ActorID: actor}, scope, action)
	if err != nil {
		return v, err
	}
	return v, tx.Commit(f.ctx)
}
func (f *taskFixture) taskState(id string) (string, string, int) {
	f.t.Helper()
	var state, code string
	var attempt int
	if err := f.conn.QueryRow(f.ctx, "SELECT state,error_code,attempt FROM adtr.tasks WHERE task_id=$1", id).Scan(&state, &code, &attempt); err != nil {
		f.t.Fatal(err)
	}
	return state, code, attempt
}
func (f *taskFixture) clearRate() { f.exec("DELETE FROM adtr.auth_attempts") }

func TestTaskHTTPFreshProofSessionAndTenantBoundary(t *testing.T) {
	f := newTaskFixture(t)
	f.callTask(nil, "", nil, 401)
	f.callTask(f.client(f.viewer), "", nil, 403)
	admin := f.client(f.admin)
	operator := f.client(f.operator)
	foreign := f.client(f.other)
	f.callTask(admin, "/kinds", nil, 200)
	f.callTask(operator, "", nil, 200)
	initial := f.version(f.operator)
	badCSRF := *operator
	badCSRF.csrf = "wrong"
	f.callTask(&badCSRF, "/submit", f.proof(taskSubmitBody("csrf")), 403)
	bad := f.proof(taskSubmitBody("bad-password"))
	bad["actorPassword"] = "wrong password"
	f.callTask(operator, "/submit", bad, 401)
	good := f.proof(taskSubmitBody("first"))
	out := f.callTask(operator, "/submit", good, 200)
	id := taskOutputID(t, out)
	if out["replayed"] != false || f.version(f.operator) != initial {
		t.Fatal("proof changed authorization epoch or submission replayed")
	}
	var actor int64
	var tenant, revision string
	if err := f.conn.QueryRow(f.ctx, "SELECT actor_id,tenant_id,authorization_version FROM adtr.tasks WHERE task_id=$1", id).Scan(&actor, &tenant, &revision); err != nil {
		t.Fatal(err)
	}
	if actor != f.operator || tenant != "one" || revision != fmt.Sprint(initial) {
		t.Fatal("untrusted principal persisted")
	}
	f.callTask(operator, "/submit", good, 401) // Reused code never replays a write.
	replay := f.callTask(operator, "/submit", f.proof(taskSubmitBody("first")), 200)
	if replay["replayed"] != true || taskOutputID(t, replay) != id {
		t.Fatal("idempotent HTTP retry lost original task")
	}
	f.callTask(foreign, "/detail?taskUUID="+id, nil, 404)
	other := f.callTask(foreign, "", nil, 200)
	if other["page"].(map[string]any)["total"] != float64(0) {
		t.Fatal("foreign list leakage")
	}
	f.callTask(operator, "/detail?taskUUID="+id, nil, 200)
	invalid := f.proof(taskSubmitBody("unknown"))
	invalid["taskName"] = "engineRestart"
	f.callTask(operator, "/submit", invalid, 400)
	missing := f.proof(map[string]any{"taskUUID": "missing"})
	f.callTask(operator, "/cancel", missing, 404)
	// A failed task mutation rolls back TOTP consumption and task/event changes.
	f.callTask(operator, "/submit", map[string]any{"taskName": "infrastructure.health", "domainId": "platform", "payloadVersion": 1, "payload": map[string]any{}, "idempotencyKey": "second", "actorPassword": resourceTestPassword, "totpCode": missing["totpCode"]}, 200)
	f.clearRate()
	// Actual logout removes only the session; a persisted actor still authorizes work.
	req := httptest.NewRequest("POST", "/api/auth/logout", strings.NewReader(`{}`))
	req.AddCookie(operator.cookie)
	req.Header.Set("Origin", f.s.origin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", operator.csrf)
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	f.callTask(operator, "", nil, 401)
	if f.version(f.operator) != initial {
		t.Fatal("logout revoked task epoch")
	}
	lease, err := f.engine.Claim(f.ctx, "synthetic-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.Start(f.ctx, lease); err != nil {
		t.Fatal("worker required session/key", err)
	}
	// A forced-change account is blocked even if the session cookie still exists.
	f.exec("UPDATE adtr.users SET must_change=true WHERE id=$1", f.admin)
	f.callTask(admin, "", nil, 403)
}

func TestTaskAuthorizationEpochIsMonotonicAndServerOwned(t *testing.T) {
	f := newTaskFixture(t)
	old, other := f.version(f.operator), f.version(f.other)
	unchanged := func(sql string, args ...any) {
		t.Helper()
		f.exec(sql, args...)
		if f.version(f.operator) != old {
			t.Fatalf("nonsecurity change bumped epoch: %s", sql)
		}
	}
	changed := func(sql string, args ...any) {
		t.Helper()
		f.exec(sql, args...)
		next := f.version(f.operator)
		if next <= old {
			t.Fatalf("security mutation failed to advance epoch: %s", sql)
		}
		old = next
		if f.version(f.other) != other {
			t.Fatal("foreign tenant epoch changed")
		}
	}
	unchanged("UPDATE adtr.users SET remark='new profile',email='synthetic@example.test',mfa_last_step=mfa_last_step+1,mfa_pending='pending',mfa_pending_until=now() WHERE id=$1", f.operator)
	c := f.client(f.operator)
	unchanged("DELETE FROM adtr.sessions WHERE token_hash=$1", digest(c.cookie.Value))
	changed("UPDATE adtr.users SET disabled=true WHERE id=$1", f.operator)
	changed("UPDATE adtr.users SET disabled=false WHERE id=$1", f.operator)
	changed("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	changed("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	unchanged("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	unchanged("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='users'", narrowResourceRole)
	unchanged("UPDATE adtr.resource_tenant_config SET uid='changed-customer',name='Changed Name' WHERE tenant_id='one'")
	changed("UPDATE adtr.resource_tenant_config SET expire_time=expire_time+1 WHERE tenant_id='one'")
	changed("UPDATE adtr.resource_tenant_config SET max_ad_count=max_ad_count+1 WHERE tenant_id='one'")
	unchanged("UPDATE adtr.resource_domains SET name='Changed domain' WHERE tenant_id='one' AND id='domain-a'")
	changed("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='domain-a'")
	changed("UPDATE adtr.resource_domains SET active=true WHERE tenant_id='one' AND id='domain-a'")
	f.exec("INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('one','task-group','TaskGroup')")
	changed("INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('one',$1,'task-group')", narrowResourceRole)
	changed("INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('one','task-group','domain-a')")
	changed("DELETE FROM adtr.resource_group_members WHERE tenant_id='one' AND group_id='task-group'")
	changed("INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('one','task-group','domain-a')")
	changed("DELETE FROM adtr.resource_groups WHERE tenant_id='one' AND id='task-group'")
	for _, sql := range []string{"UPDATE adtr.users SET authorization_version=1 WHERE id=$1", "UPDATE adtr.users SET authorization_version=authorization_version+1000 WHERE id=$1", "UPDATE adtr.users SET tenant_id='other' WHERE id=$1"} {
		if _, err := f.conn.Exec(f.ctx, sql, f.operator); err == nil {
			t.Fatalf("accepted client-owned epoch/identity: %s", sql)
		}
		if f.version(f.operator) != old {
			t.Fatal("failed direct mutation changed epoch")
		}
	}
	if _, err := f.conn.Exec(f.ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,authorization_version) VALUES('one','forged-version','unused','viewer',999)"); err == nil {
		t.Fatal("accepted supplied insertion epoch")
	}
	for _, table := range []string{"users", "access_roles", "access_permissions", "resource_groups", "resource_group_members", "resource_role_groups", "resource_domains", "resource_tenant_config"} {
		if _, err := f.conn.Exec(f.ctx, "TRUNCATE adtr."+table+" CASCADE"); err == nil {
			t.Fatal("TRUNCATE bypass", table)
		}
	}
}

func TestTaskWorkerRejectsRevokeRegrantAndLiveIdentityLoss(t *testing.T) {
	for _, change := range []string{"revoke-regrant", "disable-enable", "force-change", "password-expiry", "deleted", "role-reassigned"} {
		t.Run(change, func(t *testing.T) {
			f := newTaskFixture(t)
			c := f.client(f.operator)
			out := f.callTask(c, "/submit", f.proof(taskSubmitBody("old-authorization")), 200)
			id := taskOutputID(t, out)
			switch change {
			case "revoke-regrant":
				f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
				f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
			case "disable-enable":
				f.exec("UPDATE adtr.users SET disabled=true WHERE id=$1", f.operator)
				f.exec("UPDATE adtr.users SET disabled=false WHERE id=$1", f.operator)
			case "force-change":
				f.exec("UPDATE adtr.users SET must_change=true WHERE id=$1", f.operator)
			case "password-expiry":
				f.now = f.now.Add(91 * 24 * time.Hour)
			case "deleted":
				f.exec("DELETE FROM adtr.users WHERE id=$1", f.operator)
			case "role-reassigned":
				f.exec("UPDATE adtr.users SET role_id='' WHERE id=$1", f.operator)
			}
			lease, err := f.engine.Claim(f.ctx, "worker")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.engine.Start(f.ctx, lease); !errors.Is(err, tasks.ErrAuthorization) {
				t.Fatal("old task executed after live authorization changed", err)
			}
			state, code, attempt := f.taskState(id)
			if state != "failed" || code != "authorization_revoked" || attempt != 0 {
				t.Fatal(state, code, attempt)
			}
		})
	}
}

func TestTaskLiveDomainChecksHaveNoAdministratorBypass(t *testing.T) {
	f := newTaskFixture(t)
	platform := tasks.Scope{TaskName: "infrastructure.health", DomainID: "platform", Platform: true}
	domain := tasks.Scope{TaskName: "synthetic.read", DomainID: "domain-a"}
	for _, id := range []int64{f.admin, f.operator} {
		if _, err := f.authorized(id, "one", platform, tasks.Execute); err != nil {
			t.Fatal(err)
		}
		if _, err := f.authorized(id, "one", domain, tasks.Execute); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("implicit data authorization", id, err)
		}
	}
	f.exec("INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('one','scope','Scope')")
	f.exec("INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('one','scope','domain-a')")
	f.exec("INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('one','platform_admin','scope'),('one',$1,'scope')", narrowResourceRole)
	for _, id := range []int64{f.admin, f.operator} {
		if _, err := f.authorized(id, "one", domain, tasks.Execute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.authorized(f.other, "one", domain, tasks.Execute); !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("foreign actor accepted", err)
	}
	if _, err := f.authorized(f.admin, "one", tasks.Scope{TaskName: "synthetic.read", DomainID: "domain-b"}, tasks.Read); !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("ungranted domain accepted", err)
	}
	for _, sql := range []string{"UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='domain-a'", "UPDATE adtr.resource_tenant_config SET expire_time=0 WHERE tenant_id='one'", "UPDATE adtr.resource_tenant_config SET max_ad_count=1 WHERE tenant_id='one'"} {
		f.exec(sql)
		if _, err := f.authorized(f.admin, "one", domain, tasks.Execute); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("invalid domain eligibility accepted", sql, err)
		}
		// Repair eligibility before the next independent denial.
		f.exec("UPDATE adtr.resource_domains SET active=true WHERE tenant_id='one' AND id='domain-a'")
		f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1,max_ad_count=10 WHERE tenant_id='one'", f.now.Add(time.Hour).Unix())
	}
	f.exec("DELETE FROM adtr.resource_tenant_config WHERE tenant_id='one'")
	if _, err := f.authorized(f.admin, "one", platform, tasks.Execute); err != nil {
		t.Fatal("infrastructure requires nonexistent AD license", err)
	}
	if _, err := f.authorized(f.admin, "one", domain, tasks.Execute); !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("missing domain config allowed", err)
	}
	if _, err := f.authorized(f.admin, "one", tasks.Scope{TaskName: "synthetic.read", DomainID: "platform", Platform: true}, tasks.Execute); !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("caller invented platform kind", err)
	}
}

func TestTaskProofAndEpochRollbackOnAuditFailure(t *testing.T) {
	f := newTaskFixture(t)
	c := f.client(f.operator)
	before := f.version(f.operator)
	var step int64
	if err := f.conn.QueryRow(f.ctx, "SELECT mfa_last_step FROM adtr.users WHERE id=$1", f.operator).Scan(&step); err != nil {
		t.Fatal(err)
	}
	f.exec(`CREATE FUNCTION adtr.reject_synthetic_task_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit unavailable'; END; $$;
 CREATE TRIGGER reject_synthetic_task_audit BEFORE INSERT ON adtr.task_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_synthetic_task_audit();`)
	body := f.proof(taskSubmitBody("atomic"))
	f.callTask(c, "/submit", body, 500)
	var count int
	var afterStep int64
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.tasks").Scan(&count); err != nil || count != 0 {
		t.Fatal("task survived audit failure", count, err)
	}
	if err := f.conn.QueryRow(f.ctx, "SELECT mfa_last_step FROM adtr.users WHERE id=$1", f.operator).Scan(&afterStep); err != nil || afterStep != step || f.version(f.operator) != before {
		t.Fatal("proof/epoch survived audit failure", afterStep, step, err)
	}
	f.exec("DROP TRIGGER reject_synthetic_task_audit ON adtr.task_events")
	f.callTask(c, "/submit", body, 200) // The same proof is still unused after rollback.
	// Security change + failing audit in one transaction cannot revoke queued work.
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE adtr.users SET disabled=true WHERE id=$1", f.operator); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE adtr.auth_audit SET action='illegal'"); err == nil { // No rows need not fire; force an explicit failure instead.
		_, err = tx.Exec(f.ctx, "SELECT 1/0")
	}
	if err == nil {
		t.Fatal("missing synthetic failure")
	}
	_ = tx.Rollback(f.ctx)
	if f.version(f.operator) != before {
		t.Fatal("rolled-back security change advanced stored epoch")
	}
}

func TestTaskAuthorizationDatabaseErrorsAreNotDenial(t *testing.T) {
	f := newTaskFixture(t)
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	_, _ = tx.Exec(f.ctx, "SELECT 1/0")
	_, err = NewTaskAuthorizer()(f.ctx, tx, tasks.Principal{TenantID: "one", ActorID: f.admin}, tasks.Scope{TaskName: "infrastructure.health", DomainID: "platform", Platform: true}, tasks.Execute)
	if err == nil || errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("database outage became permission denial", err)
	}
}

func TestTaskAuthorizationLockOrderFailsFastForUncoordinatedSQL(t *testing.T) {
	f := newTaskFixture(t)
	holder, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close(f.ctx)
	tx, err := holder.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if err = lockIdentityTenant(f.ctx, tx, "one"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	_, err = f.conn.Exec(ctx, "UPDATE adtr.users SET disabled=true WHERE id=$1", f.operator)
	var pgerr interface{ SQLState() string }
	if !errors.As(err, &pgerr) || pgerr.SQLState() != "40001" {
		t.Fatal("uncoordinated SQL did not fail fast", err)
	}
}

// Compile-time check: runtime workers accept the authorizer without an auth Service.
var _ tasks.Authorizer = NewTaskAuthorizer()

func TestTaskRunningBoundariesRejectRestoredGrantEpoch(t *testing.T) {
	for _, boundary := range []string{"heartbeat", "progress", "finish"} {
		t.Run(boundary, func(t *testing.T) {
			f := newTaskFixture(t)
			c := f.client(f.operator)
			out := f.callTask(c, "/submit", f.proof(taskSubmitBody("running")), 200)
			id := taskOutputID(t, out)
			lease, err := f.engine.Claim(f.ctx, "running-worker")
			if err != nil {
				t.Fatal(err)
			}
			lease, err = f.engine.Start(f.ctx, lease)
			if err != nil {
				t.Fatal(err)
			}
			f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
			f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
			switch boundary {
			case "heartbeat":
				_, err = f.engine.Heartbeat(f.ctx, lease)
			case "progress":
				_, err = f.engine.Progress(f.ctx, lease, 50, json.RawMessage(`{}`), json.RawMessage(`{}`), lease.Task.ResultVersion)
			case "finish":
				_, err = f.engine.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{"safe":"synthetic"}`)})
			}
			if boundary == "finish" {
				if err != nil {
					t.Fatal("failed to persist rejected execution", err)
				}
			} else if !errors.Is(err, tasks.ErrAuthorization) {
				t.Fatal("boundary accepted restored old authorization", boundary, err)
			}
			state, code, attempt := f.taskState(id)
			wantState, wantCode := "failed", "authorization_revoked"
			if boundary == "heartbeat" {
				wantState = "cancel_requested"
			}
			if boundary == "progress" {
				wantState, wantCode = "running", ""
			}
			if state != wantState || code != wantCode || attempt != 1 {
				t.Fatal(state, code, attempt)
			}
			if boundary == "progress" {
				var progress int
				var revision int64
				if err := f.conn.QueryRow(f.ctx, "SELECT progress,result_version FROM adtr.tasks WHERE task_id=$1", id).Scan(&progress, &revision); err != nil || progress != 0 || revision != lease.Task.ResultVersion {
					t.Fatal("rejected progress changed durable checkpoint", progress, revision, err)
				}
			}
		})
	}
}

func TestTaskLoginAndProofDoNotRevokeAuthorizationEpoch(t *testing.T) {
	f := newTaskFixture(t)
	before := f.version(f.operator)
	f.now = f.now.Add(31 * time.Second)
	code, _ := totp(resourceTestSecret, f.now.Unix()/30)
	raw, _ := json.Marshal(map[string]string{"username": "operator", "password": resourceTestPassword, "totpCode": code})
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:4444"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if f.version(f.operator) != before {
		t.Fatal("ordinary MFA login revoked task epoch")
	}
	var profile map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	c := &resourceTestClient{cookie: cookies[0], csrf: profile["csrfToken"].(string), id: f.operator}
	body := taskSubmitBody("consumed-login-proof")
	body["actorPassword"] = resourceTestPassword
	body["totpCode"] = code
	f.callTask(c, "/submit", body, 401)
	f.callTask(c, "/submit", f.proof(taskSubmitBody("fresh-proof")), 200)
	if f.version(f.operator) != before {
		t.Fatal("fresh operation proof revoked task epoch")
	}
}

func TestTaskMetadataOnlyHTTPChangesPreserveSubmissionEpoch(t *testing.T) {
	f := newTaskFixture(t)
	f.exec("INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('one','metadata','Metadata')")
	f.exec("INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('one','metadata','domain-a')")
	f.exec("INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('one',$1,'metadata')", narrowResourceRole)
	operator := f.client(f.operator)
	out := f.callTask(operator, "/submit", f.proof(taskSubmitBody("metadata-safe")), 200)
	id := taskOutputID(t, out)
	epoch := f.version(f.operator)
	grants := []map[string]any{}
	for _, mark := range []string{"users", "roles", "permissions", "tasks"} {
		grants = append(grants, map[string]any{"mark": mark, "auth": map[string]bool{"readable": true, "writeable": true}})
	}
	body := f.proof(map[string]any{"roleID": narrowResourceRole, "roleName": "Narrow", "remark": "new description only", "permissions": grants})
	raw, _ := json.Marshal(body)
	admin := f.client(f.admin)
	r := httptest.NewRequest("POST", "/api/access/roles/save", strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:3333"
	r.AddCookie(admin.cookie)
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", admin.csrf)
	w := httptest.NewRecorder()
	f.s.ServeAccessHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if f.version(f.operator) != epoch {
		t.Fatal("role description edit invalidated queued authorization")
	}
	body = f.proof(map[string]any{"id": "metadata", "meta": resourceTestMeta("RenamedGroup", "domain-a")})
	f.call(admin, "/groups/update", body, 200)
	if f.version(f.operator) != epoch {
		t.Fatal("group name/mark edit invalidated queued authorization")
	}
	lease, err := f.engine.Claim(f.ctx, "metadata-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.Start(f.ctx, lease); err != nil {
		t.Fatal("metadata edit stopped authorized work", err)
	}
	state, _, _ := f.taskState(id)
	if state != "running" {
		t.Fatal(state)
	}
}

func TestTaskHTTPReadOnlyCancellationRecoveryAndErrors(t *testing.T) {
	f := newTaskFixture(t)
	admin := f.client(f.admin)
	operator := f.client(f.operator)
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	first := f.callTask(admin, "/submit", f.proof(taskSubmitBody("admin-task")), 200)
	id := taskOutputID(t, first)
	f.callTask(operator, "", nil, 200)
	f.callTask(operator, "/detail?taskUUID="+id, nil, 200)
	f.callTask(operator, "/submit", f.proof(taskSubmitBody("read-only")), 403)
	f.callTask(operator, "/cancel", f.proof(map[string]any{"taskUUID": id}), 403)
	f.callTask(admin, "/recover", f.proof(map[string]any{"taskUUID": id, "idempotencyKey": "cannot-recover-queued"}), 409)
	f.callTask(admin, "/cancel", f.proof(map[string]any{"taskUUID": id}), 200)
	state, code, attempt := f.taskState(id)
	if state != "cancelled" || code != "cancelled" || attempt != 0 {
		t.Fatal(state, code, attempt)
	}
	f.callTask(admin, "/recover", f.proof(map[string]any{"taskUUID": id, "idempotencyKey": "cannot-recover-cancelled"}), 409)
	second := f.callTask(admin, "/submit", f.proof(taskSubmitBody("will-fail")), 200)
	failedID := taskOutputID(t, second)
	lease, err := f.engine.Claim(f.ctx, "error-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Failed, Code: "synthetic_failure"}); err != nil {
		t.Fatal(err)
	}
	f.callTask(admin, "/cancel", f.proof(map[string]any{"taskUUID": failedID}), 409)
	recovered := f.callTask(admin, "/recover", f.proof(map[string]any{"taskUUID": failedID, "idempotencyKey": "manual-recovery"}), 200)
	recoveredID := taskOutputID(t, recovered)
	if recoveredID == failedID || recovered["task"].(map[string]any)["parentTaskUUID"] != failedID {
		t.Fatal("recovery rewrote parent history", recovered)
	}
	oldState, _, _ := f.taskState(failedID)
	if oldState != "failed" {
		t.Fatal("parent state mutated", oldState)
	}
	// A caller cannot select another domain or a different payload version.
	f.clearRate()
	invalid := f.proof(taskSubmitBody("bad-domain"))
	invalid["domainId"] = "domain-a"
	f.callTask(admin, "/submit", invalid, 400)
	invalid = f.proof(taskSubmitBody("bad-version"))
	invalid["payloadVersion"] = 2
	f.callTask(admin, "/submit", invalid, 400)
	invalid = f.proof(taskSubmitBody("bad-payload"))
	invalid["payload"] = map[string]any{"command": "restart"}
	f.callTask(admin, "/submit", invalid, 400)
}

func TestTaskHTTPRefusesIncompatibleSchemaBeforeProofOrTaskMutation(t *testing.T) {
	f := newTaskFixture(t)
	c := f.client(f.admin)
	before := f.version(f.admin)
	var proofStep int64
	if err := f.conn.QueryRow(f.ctx, "SELECT mfa_last_step FROM adtr.users WHERE id=$1", f.admin).Scan(&proofStep); err != nil {
		t.Fatal(err)
	}
	for _, schema := range []int{4, 6, 0} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			if schema == 0 {
				f.exec("DELETE FROM adtr.schema_version")
			} else {
				f.exec("UPDATE adtr.schema_version SET version=$1", schema)
			}
			for _, path := range []string{"", "/kinds", "/detail?taskUUID=missing"} {
				f.callTask(c, path, nil, 503)
			}
			body := f.proof(taskSubmitBody(fmt.Sprintf("schema-%d", schema)))
			f.callTask(c, "/submit", body, 503)
			f.callTask(c, "/cancel", f.proof(map[string]any{"taskUUID": "missing"}), 503)
			f.callTask(c, "/recover", f.proof(map[string]any{"taskUUID": "missing", "idempotencyKey": "recovery"}), 503)
			var count int
			if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.tasks)+(SELECT count(*) FROM adtr.task_events)+(SELECT count(*) FROM adtr.task_outbox)+(SELECT count(*) FROM adtr.auth_attempts)+(SELECT count(*) FROM adtr.auth_audit)").Scan(&count); err != nil || count != 0 {
				t.Fatal("incompatible schema changed task data", count, err)
			}
			var step int64
			if err := f.conn.QueryRow(f.ctx, "SELECT mfa_last_step FROM adtr.users WHERE id=$1", f.admin).Scan(&step); err != nil || step != proofStep || f.version(f.admin) != before {
				t.Fatal("incompatible schema consumed proof/epoch", step, err)
			}
		})
	}
	f.exec("INSERT INTO adtr.schema_version(singleton,version) VALUES(true,5)")
	f.callTask(c, "/submit", f.proof(taskSubmitBody("schema-restored")), 200)
}
