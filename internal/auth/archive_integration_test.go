//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

type archiveFixture struct {
	*taskFixture
	archive *taskarchive.Engine
	before  string
}

func newArchiveFixture(t *testing.T) *archiveFixture {
	t.Helper()
	f := newTaskFixture(t) // Isolated real PostgreSQL; absence is a failure, never a skip.
	f.exec(ArchivePermissionSchema + taskarchive.Schema + DomainPermissionMarks + OperationAccountPermissionMarks)
	f.exec("UPDATE adtr.schema_version SET version=$1 WHERE singleton=true", taskarchive.SchemaVersion)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'task_archive',true,true)", narrowResourceRole)
	engine, err := tasks.New(f.s.database, tasks.ProductionRegistry(), taskAuthorizer(f.s.now), taskarchive.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = engine
	archive, err := taskarchive.New(archiveAuthorizer(f.s.now))
	if err != nil {
		t.Fatal(err)
	}
	return &archiveFixture{taskFixture: f, archive: archive, before: "2021-01-01T00:00:00Z"}
}

func (f *archiveFixture) callArchive(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method := "GET"
	var raw []byte
	if body != nil {
		method = "POST"
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "/api/tasks"+path, strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:5432"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.ArchiveHandler(f.archive).ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("archive %s got %d want %d: %s", path, w.Code, want, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *archiveFixture) completeHealth(c *resourceTestClient, key string) string {
	f.t.Helper()
	id := taskOutputID(f.t, f.callTask(c, "/submit", f.proof(taskSubmitBody(key)), 200))
	worked, err := f.engine.RunOne(f.ctx, "archive-synthetic-worker")
	if err != nil || !worked {
		f.t.Fatal("health completion", worked, err)
	}
	var state string
	var terminal, cutoff time.Time
	if err := f.conn.QueryRow(f.ctx, "SELECT state,terminal_at,clock_timestamp() FROM adtr.tasks WHERE task_id=$1", id).Scan(&state, &terminal, &cutoff); err != nil {
		f.t.Fatal(err)
	}
	if state != "succeeded" || !terminal.Before(cutoff) {
		f.t.Fatal("missing successful trustworthy terminal time", state, terminal, cutoff)
	}
	f.before = cutoff.UTC().Format(time.RFC3339Nano)
	f.clearRate()
	return id
}

func (f *archiveFixture) archiveBody(key string, version int64, ids ...string) map[string]any {
	body := archiveRestoreBody(key, version, ids...)
	body["before"] = f.before
	return body
}

func archiveRestoreBody(key string, version int64, ids ...string) map[string]any {
	targets := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, map[string]any{"taskUUID": id, "visibilityVersion": version})
	}
	return map[string]any{"targets": targets, "reason": "Synthetic retention review", "idempotencyKey": key}
}

func (f *archiveFixture) proofStep(actor int64) int64 {
	f.t.Helper()
	var step int64
	if err := f.conn.QueryRow(f.ctx, "SELECT mfa_last_step FROM adtr.users WHERE id=$1", actor).Scan(&step); err != nil {
		f.t.Fatal(err)
	}
	return step
}

func (f *archiveFixture) candidates(c *resourceTestClient) map[string]any {
	f.t.Helper()
	return f.callArchive(c, "/archive-candidates?before="+f.before, nil, 200)
}

func archiveReceipt(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	receipt, ok := out["receipt"].(map[string]any)
	if !ok {
		t.Fatal("missing archive receipt", out)
	}
	if id, ok := receipt["operationUUID"].(string); !ok || id == "" {
		t.Fatal("missing operation identity", out)
	}
	if occurred, ok := receipt["occurredAt"].(string); !ok || occurred == "" {
		t.Fatal("missing operation timestamp", out)
	}
	for _, private := range []string{"actorId", "tenantId", "authorizationVersion", "idempotencyKey", "requestHash", "actorPassword", "totpCode"} {
		if _, exists := receipt[private]; exists {
			t.Fatal("private receipt field disclosed", private)
		}
	}
	return receipt
}

func (f *archiveFixture) visibility(id string) (bool, int64) {
	f.t.Helper()
	var archived bool
	var version int64
	if err := f.conn.QueryRow(f.ctx, `SELECT COALESCE(v.archived,false),COALESCE(v.visibility_version,0) FROM adtr.tasks t LEFT JOIN adtr.task_visibility v ON v.task_id=t.task_id AND v.tenant_id=t.tenant_id WHERE t.task_id=$1`, id).Scan(&archived, &version); err != nil {
		f.t.Fatal(err)
	}
	return archived, version
}

func (f *archiveFixture) executionSnapshot(id string) string {
	f.t.Helper()
	var snapshot string
	if err := f.conn.QueryRow(f.ctx, `SELECT jsonb_build_object('task',to_jsonb(t),'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM adtr.task_events e WHERE e.task_id=t.task_id),'outbox',(SELECT jsonb_agg(to_jsonb(o) ORDER BY o.id) FROM adtr.task_outbox o WHERE o.task_id=t.task_id))::text FROM adtr.tasks t WHERE t.task_id=$1`, id).Scan(&snapshot); err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

func TestArchiveHTTPFreshProofTenantWideVisibilityAndHistoricalReplay(t *testing.T) {
	f := newArchiveFixture(t)
	admin, operator, foreign := f.client(f.admin), f.client(f.operator), f.client(f.other)
	id := f.completeHealth(admin, "archive-owned-by-admin")
	f.callArchive(nil, "/archive-candidates?before="+f.before, nil, 401)
	f.callArchive(f.client(f.viewer), "/archive-candidates?before="+f.before, nil, 403)
	candidates := f.candidates(operator)
	rows := candidates["tasks"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["taskUUID"] != id || rows[0].(map[string]any)["visibilityVersion"] != float64(0) || candidates["before"] != f.before {
		t.Fatal("same-tenant noncreator lost eligible health task", candidates)
	}
	if other := f.candidates(foreign); len(other["tasks"].([]any)) != 0 || other["page"].(map[string]any)["total"] != float64(0) {
		t.Fatal("foreign candidates leaked", other)
	}
	initialEpoch, initialStep := f.version(f.operator), f.proofStep(f.operator)
	badCSRF := *operator
	badCSRF.csrf = "wrong"
	f.callArchive(&badCSRF, "/archive", f.proof(f.archiveBody("csrf", 0, id)), 403)
	badPassword := f.proof(f.archiveBody("password", 0, id))
	badPassword["actorPassword"] = "wrong password"
	f.callArchive(operator, "/archive", badPassword, 401)
	if f.proofStep(f.operator) != initialStep {
		t.Fatal("bad CSRF or password consumed proof")
	}
	f.clearRate()
	snapshot := f.executionSnapshot(id)
	body := f.proof(f.archiveBody("first-archive", 0, id))
	out := f.callArchive(operator, "/archive", body, 200)
	receipt := archiveReceipt(t, out)
	target := receipt["targets"].([]any)[0].(map[string]any)
	if out["replayed"] != false || receipt["action"] != "archive" || receipt["before"] != f.before || target["archived"] != true || target["visibilityVersion"] != float64(1) || target["taskUUID"] != id {
		t.Fatal("wrong archive receipt", out)
	}
	if f.version(f.operator) != initialEpoch || f.executionSnapshot(id) != snapshot {
		t.Fatal("archival changed authorization or execution evidence")
	}
	f.callArchive(operator, "/archive", body, 401)
	replay := f.callArchive(operator, "/archive", f.proof(f.archiveBody("first-archive", 0, id)), 200)
	if replay["replayed"] != true || !reflect.DeepEqual(replay["receipt"], receipt) {
		t.Fatal("retry lost immutable receipt", replay)
	}
	f.clearRate()
	if current := f.candidates(operator); len(current["tasks"].([]any)) != 0 {
		t.Fatal("archived task remained a candidate", current)
	}
	// A same-tenant noncreator can restore; actor identity scopes operation keys,
	// not eligibility to manage infrastructure.health visibility.
	restored := f.callArchive(operator, "/restore", f.proof(archiveRestoreBody("first-restore", 1, id)), 200)
	if _, exists := archiveReceipt(t, restored)["before"]; exists {
		t.Fatal("restore fabricated a cutoff", restored)
	}
	if archived, version := f.visibility(id); archived || version != 2 {
		t.Fatal("restore did not advance visibility", archived, version)
	}
	replay = f.callArchive(operator, "/archive", f.proof(f.archiveBody("first-archive", 0, id)), 200)
	if replay["replayed"] != true || !reflect.DeepEqual(replay["receipt"], receipt) {
		t.Fatal("replay after restore lost historical outcome", replay)
	}
	if archived, version := f.visibility(id); archived || version != 2 {
		t.Fatal("archive replay reapplied the old mutation", archived, version)
	}
	if current := f.candidates(operator); len(current["tasks"].([]any)) != 1 || current["tasks"].([]any)[0].(map[string]any)["visibilityVersion"] != float64(2) {
		t.Fatal("candidate omitted restored version", current)
	}
	f.clearRate()
	f.callArchive(foreign, "/archive", f.proof(f.archiveBody("foreign-archive", 0, id)), 404)
	f.callArchive(foreign, "/restore", f.proof(archiveRestoreBody("foreign-restore", 2, id)), 404)
	conflict := f.proof(f.archiveBody("first-archive", 0, id))
	conflict["reason"] = "A different operation"
	step := f.proofStep(f.operator)
	f.callArchive(operator, "/archive", conflict, 409)
	if f.proofStep(f.operator) != step || f.executionSnapshot(id) != snapshot {
		t.Fatal("idempotency conflict changed proof or execution history")
	}
}

func TestArchiveHTTPRequiresBothCurrentPermissionGrants(t *testing.T) {
	for _, mark := range []string{"tasks", "task_archive"} {
		for _, grant := range []string{"readable", "writeable"} {
			t.Run(mark+"/"+grant, func(t *testing.T) {
				f := newArchiveFixture(t)
				c := f.client(f.operator)
				id := f.completeHealth(f.client(f.admin), "permission-health")
				if grant == "readable" {
					f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
					f.callArchive(c, "/archive-candidates?before="+f.before, nil, 403)
				} else {
					f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
					f.candidates(c)
				}
				step := f.proofStep(f.operator)
				f.callArchive(c, "/archive", f.proof(f.archiveBody("missing-archive-grant", 0, id)), 403)
				f.callArchive(c, "/restore", f.proof(archiveRestoreBody("missing-restore-grant", 0, id)), 403)
				if f.proofStep(f.operator) != step {
					t.Fatal("denied permissions consumed proof")
				}
				if archived, version := f.visibility(id); archived || version != 0 {
					t.Fatal("permission denial changed visibility", archived, version)
				}
			})
		}
	}
}

func TestArchiveHTTPBatchConflictAndFutureCutoffRollback(t *testing.T) {
	f := newArchiveFixture(t)
	c := f.client(f.operator)
	id := f.completeHealth(f.client(f.admin), "batch-health")
	foreignID := f.completeHealth(f.client(f.other), "foreign-health")
	for _, tc := range []struct {
		name string
		body map[string]any
		code int
	}{
		{"foreign batch", f.archiveBody("foreign-batch", 0, id, foreignID), 404},
		{"missing batch", f.archiveBody("missing-batch", 0, id, "missing-task"), 404},
		{"stale version", f.archiveBody("stale-version", 1, id), 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := f.proofStep(f.operator)
			f.callArchive(c, "/archive", f.proof(tc.body), tc.code)
			if f.proofStep(f.operator) != step {
				t.Fatal("failed batch consumed proof")
			}
			if archived, version := f.visibility(id); archived || version != 0 {
				t.Fatal("partial batch committed", archived, version)
			}
			f.clearRate()
		})
	}
	future := f.proof(f.archiveBody("future-before", 0, id))
	future["before"] = f.now.Add(24 * time.Hour).UTC().Format(time.RFC3339)
	step := f.proofStep(f.operator)
	f.callArchive(c, "/archive", future, 400)
	f.callArchive(c, "/archive-candidates?before="+future["before"].(string), nil, 400)
	if f.proofStep(f.operator) != step {
		t.Fatal("future cutoff consumed proof")
	}
	// The same fresh proof succeeds after correcting the rejected request.
	future["before"] = f.before
	f.callArchive(c, "/archive", future, 200)
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.task_archive_operations").Scan(&count); err != nil || count != 1 {
		t.Fatal("failed batches persisted receipts", count, err)
	}
}

func TestArchiveHTTPAuditFailureRollsBackProofVisibilityAndReceipt(t *testing.T) {
	f := newArchiveFixture(t)
	c := f.client(f.operator)
	id := f.completeHealth(f.client(f.admin), "atomic-health")
	snapshot := f.executionSnapshot(id)
	f.exec(`CREATE FUNCTION adtr.reject_synthetic_archive_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic archive audit unavailable'; END; $$;
CREATE TRIGGER reject_synthetic_archive_event BEFORE INSERT ON adtr.task_archive_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_synthetic_archive_event();`)
	body := f.proof(f.archiveBody("atomic-archive", 0, id))
	step := f.proofStep(f.operator)
	f.callArchive(c, "/archive", body, 500)
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.task_archive_operations)+(SELECT count(*) FROM adtr.task_archive_events)+(SELECT count(*) FROM adtr.task_visibility)").Scan(&count); err != nil || count != 0 {
		t.Fatal("archive survived its audit failure", count, err)
	}
	if f.proofStep(f.operator) != step || f.executionSnapshot(id) != snapshot {
		t.Fatal("failed archive changed proof or execution evidence")
	}
	f.exec("DROP TRIGGER reject_synthetic_archive_event ON adtr.task_archive_events")
	f.callArchive(c, "/archive", body, 200)
	f.clearRate()
	f.exec("CREATE TRIGGER reject_synthetic_archive_event BEFORE INSERT ON adtr.task_archive_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_synthetic_archive_event()")
	body = f.proof(archiveRestoreBody("atomic-restore", 1, id))
	step = f.proofStep(f.operator)
	f.callArchive(c, "/restore", body, 500)
	if archived, version := f.visibility(id); !archived || version != 1 {
		t.Fatal("failed restore changed visibility", archived, version)
	}
	if f.proofStep(f.operator) != step || f.executionSnapshot(id) != snapshot {
		t.Fatal("failed restore changed proof or execution evidence")
	}
	if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.task_archive_operations)+(SELECT count(*) FROM adtr.task_archive_events)").Scan(&count); err != nil || count != 2 {
		t.Fatal("failed restore persisted a receipt or event", count, err)
	}
	f.exec("DROP TRIGGER reject_synthetic_archive_event ON adtr.task_archive_events")
	f.callArchive(c, "/restore", body, 200)
}

func TestArchiveHTTPRejectsLiveAuditAndUnregisteredTargets(t *testing.T) {
	f := newArchiveFixture(t)
	c := f.client(f.operator)
	eligibleID := f.completeHealth(f.client(f.admin), "eligible-health")
	for _, target := range []struct {
		id, kind, domain string
		terminal         bool
	}{
		{"live-health", "infrastructure.health", "platform", false},
		{"private-audit-export", "audit.export", "platform", true},
		{"unregistered-task", "synthetic.other", "platform", true},
		{"wrong-domain-health", "infrastructure.health", "domain-a", true},
	} {
		// Synthetic unsupported tasks never execute. A real database transition
		// supplies terminal_at instead of forging an old completion timestamp.
		f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) VALUES($1,'one',$2,$3,1,'{}','synthetic',$4,$5,$1,'queued',1)`, target.id, target.domain, target.kind, f.operator, fmt.Sprint(f.version(f.operator)))
		if target.terminal {
			f.exec("UPDATE adtr.tasks SET state='succeeded' WHERE task_id=$1", target.id)
		}
	}
	var cutoff time.Time
	if err := f.conn.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&cutoff); err != nil {
		t.Fatal(err)
	}
	f.before = cutoff.UTC().Format(time.RFC3339Nano)
	rows := f.candidates(c)["tasks"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["taskUUID"] != eligibleID {
		t.Fatal("candidate selection included an ineligible kind, scope or state", rows)
	}
	for _, id := range []string{"live-health", "private-audit-export", "unregistered-task", "wrong-domain-health"} {
		step := f.proofStep(f.operator)
		f.callArchive(c, "/archive", f.proof(f.archiveBody("ineligible-"+id, 0, eligibleID, id)), 409)
		if f.proofStep(f.operator) != step {
			t.Fatal("ineligible target consumed proof", id)
		}
		if archived, version := f.visibility(eligibleID); archived || version != 0 {
			t.Fatal("ineligible batch partially archived health task", archived, version)
		}
		f.clearRate()
	}
}

func TestArchiveHTTPSchemaPregateBeforeIdentityProofAndWrites(t *testing.T) {
	f := newArchiveFixture(t)
	c := f.client(f.admin)
	initialEpoch, initialStep := f.version(f.admin), f.proofStep(f.admin)
	locker, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close(f.ctx)
	tx, err := locker.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if err := lockIdentityTenant(f.ctx, tx, "one"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{taskarchive.SchemaVersion - 1, taskarchive.SchemaVersion + 1, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			if version == 0 {
				f.exec("DELETE FROM adtr.schema_version")
			} else {
				f.exec("UPDATE adtr.schema_version SET version=$1", version)
			}
			started := time.Now()
			for _, client := range []*resourceTestClient{c, nil} {
				f.callArchive(client, "/archive-candidates?before="+f.before, nil, 503)
			}
			f.callArchive(c, "/archive", f.proof(f.archiveBody("schema-archive", 0, "missing-task")), 503)
			f.callArchive(c, "/restore", f.proof(archiveRestoreBody("schema-restore", 1, "missing-task")), 503)
			if time.Since(started) > 2*time.Second {
				t.Fatal("schema pregate waited on identity lock")
			}
			var count int
			if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.auth_attempts)+(SELECT count(*) FROM adtr.auth_audit)+(SELECT count(*) FROM adtr.task_archive_operations)+(SELECT count(*) FROM adtr.task_archive_events)+(SELECT count(*) FROM adtr.task_visibility)").Scan(&count); err != nil || count != 0 {
				t.Fatal("schema mismatch wrote durable data", count, err)
			}
			if f.version(f.admin) != initialEpoch || f.proofStep(f.admin) != initialStep {
				t.Fatal("schema mismatch changed identity or proof")
			}
		})
	}
	if err := tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.exec("INSERT INTO adtr.schema_version(singleton,version) VALUES(true,$1)", taskarchive.SchemaVersion)
	f.candidates(c)
}

func TestArchivePermissionEpochChangesAreMonotonicAndTenantScoped(t *testing.T) {
	f := newArchiveFixture(t)
	old, foreign := f.version(f.operator), f.version(f.other)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='task_archive'", narrowResourceRole)
	if f.version(f.operator) != old {
		t.Fatal("no-op archive permission update changed epoch")
	}
	for _, sql := range []string{
		"UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='task_archive'",
		"UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='task_archive'",
		"DELETE FROM adtr.access_permissions WHERE tenant_id='one' AND role_id=$1 AND mark='task_archive'",
		"INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'task_archive',true,true)",
	} {
		f.exec(sql, narrowResourceRole)
		next := f.version(f.operator)
		if next <= old || f.version(f.other) != foreign {
			t.Fatal("archive permission change missed or crossed tenant epoch", old, next)
		}
		old = next
	}
	if _, err := f.conn.Exec(f.ctx, "INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'arbitrary_archive_bypass',true,true)", narrowResourceRole); err == nil {
		t.Fatal("permission whitelist accepted arbitrary mark")
	}
}

func TestArchiveAuthorizerDatabaseFailureIsNotPermissionDenial(t *testing.T) {
	f := newArchiveFixture(t)
	tx, err := f.conn.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, _ = tx.Exec(f.ctx, "SELECT 1/0")
	_, err = NewArchiveAuthorizer()(f.ctx, tx, tasks.Principal{TenantID: "one", ActorID: f.operator}, tasks.Scope{TaskName: "infrastructure.health", DomainID: "platform", Platform: true}, tasks.Read)
	if err == nil || errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("database failure became permission denial", err)
	}
}

func TestArchiveAuthorizerRejectsUnsupportedScopesAndActions(t *testing.T) {
	f := newArchiveFixture(t)
	for _, tc := range []struct {
		scope  tasks.Scope
		action tasks.Action
	}{
		{tasks.Scope{TaskName: "audit.export", DomainID: "platform", Platform: true}, tasks.Read},
		{tasks.Scope{TaskName: "synthetic.other", DomainID: "platform", Platform: true}, tasks.Write},
		{tasks.Scope{TaskName: "infrastructure.health", DomainID: "domain-a"}, tasks.Write},
		{tasks.Scope{TaskName: "infrastructure.health", DomainID: "platform"}, tasks.Read},
		{tasks.Scope{TaskName: "infrastructure.health", DomainID: "platform", Platform: true}, tasks.Execute},
	} {
		tx, err := f.conn.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = archiveAuthorizer(f.s.now)(f.ctx, tx, tasks.Principal{TenantID: "one", ActorID: f.admin}, tc.scope, tc.action)
		_ = tx.Rollback(f.ctx)
		if !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("administrator bypassed archive's closed scope", tc.scope, tc.action, err)
		}
	}
}

var _ tasks.Authorizer = NewArchiveAuthorizer()
