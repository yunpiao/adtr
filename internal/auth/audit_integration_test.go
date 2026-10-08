//go:build integration

package auth

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

type auditFixture struct{ *taskFixture }

func newAuditFixture(t *testing.T) *auditFixture {
	f := newTaskFixture(t)
	// One pre-migration row proves historical metadata is never fabricated.
	f.exec(`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at) VALUES('one',$1,'historic_unknown',$1,'2020-01-01T00:00:00Z')`, f.admin)
	f.exec(audit.Schema + AuditPermissionSchema + SystemPermissionMarks + systemhealth.Schema + SchedulePermissionSchema + ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema + DomainPermissionSchema + domains.Schema + audit.DomainViewSchema + OperationAccountPermissionSchema + operationaccounts.Schema + audit.OperationAccountViewSchema + OperationalLogPermissionMarks + DirectoryPermissionMarks)
	f.exec("UPDATE adtr.schema_version SET version=$1 WHERE singleton=true", audit.SchemaVersion)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'audit',true,true),('one',$1,'audit_exports',true,true)`, narrowResourceRole)
	e, err := tasks.New(f.s.database, tasks.ProductionRegistry(audit.Kind()), taskAuthorizer(f.s.now), audit.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = e
	return &auditFixture{f}
}
func (f *auditFixture) callAudit(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	w := f.auditResponse(c, path, body)
	if w.Code != want {
		f.t.Fatalf("audit %s got %d want %d: %s", path, w.Code, want, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *auditFixture) auditResponse(c *resourceTestClient, path string, body map[string]any) *httptest.ResponseRecorder {
	f.t.Helper()
	method := "GET"
	var raw []byte
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "/api/audit"+path, bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.AuditHandler(f.engine).ServeHTTP(w, r)
	return w
}
func auditListRows(t *testing.T, m map[string]any) []any {
	t.Helper()
	rows, ok := m["List"].([]any)
	if !ok {
		t.Fatal(m)
	}
	return rows
}
func (f *auditFixture) seedAuth(tenant, action string, actor int64) string {
	f.t.Helper()
	var id int64
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES($1,$2,$3,$2) RETURNING id`, tenant, actor, action).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return "auth." + strconv.FormatInt(id, 10)
}
func exportBody(key string, columns ...string) map[string]any {
	return map[string]any{"filterEvent": []string{"user_update"}, "logTypeList": []int{9}, "startTm": "2020-02-01T00:00:00Z", "endTm": "2020-02-02T00:00:00Z", "createSort": 1, "selectColumn": columns, "idempotencyKey": key}
}
func (f *auditFixture) runExport() {
	f.t.Helper()
	worked, err := f.engine.RunOne(context.Background(), "audit-synthetic-worker")
	if err != nil || !worked {
		f.t.Fatal(worked, err)
	}
}
func (f *auditFixture) taskID(out map[string]any) string {
	f.t.Helper()
	id, ok := out["taskUUID"].(string)
	if !ok || id == "" {
		f.t.Fatal(out)
	}
	return id
}

func TestAuditHTTPDefaultsPrivateReplayAndGenericRecovery(t *testing.T) {
	f := newAuditFixture(t)
	admin := f.client(f.admin)
	operator := f.client(f.operator)
	optional := map[string]any{"selectColumn": []string{"event"}, "idempotencyKey": "omitted-fields"}
	out := f.callAudit(admin, "/exports", f.proof(optional), 200)
	id := f.taskID(out)
	var payload []byte
	if err := f.conn.QueryRow(f.ctx, `SELECT payload FROM adtr.tasks WHERE task_id=$1`, id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if json.Unmarshal(payload, &parsed) != nil || parsed["createSort"] != float64(-1) || len(parsed["filterEvent"].([]any)) != 0 || len(parsed["logTypeList"].([]any)) != 0 {
		t.Fatal(string(payload))
	}
	foreign := map[string]any{"selectColumn": []string{"event"}, "idempotencyKey": "omitted-fields"}
	f.callAudit(operator, "/exports", f.proof(foreign), 409)
	// Missing and foreign private tasks remain indistinguishable on recovery;
	// malformed proof never becomes an existence oracle.
	for _, target := range []string{id, "missing-task"} {
		bad := f.proof(map[string]any{"taskUUID": target, "idempotencyKey": "recovery-" + target})
		bad["actorPassword"] = "wrong-password"
		f.callTask(operator, "/recover", bad, 401)
		good := f.proof(map[string]any{"taskUUID": target, "idempotencyKey": "recovery-" + target})
		f.callTask(operator, "/recover", good, 404)
	}
	owner := f.proof(map[string]any{"taskUUID": id, "idempotencyKey": "owner-recovery"})
	f.callTask(admin, "/recover", owner, 400)
}

func TestAuditHistoricalMetadataScopesFiltersAndSchema(t *testing.T) {
	f := newAuditFixture(t)
	admin := f.client(f.admin)
	operator := f.client(f.operator)
	f.callAudit(nil, "", nil, 401)
	f.callAudit(f.client(f.viewer), "", nil, 403)
	out := f.callAudit(admin, "?filterEvent=historic_unknown", nil, 200)
	rows := auditListRows(t, out)
	if len(rows) != 1 {
		t.Fatal(out)
	}
	row := rows[0].(map[string]any)
	availability := row["availability"].(map[string]any)
	if row["loginUser"] != nil || row["sourceIp"] != nil || row["eventResult"] != "NONE" || availability["eventResult"] != false {
		t.Fatal("invented historical metadata", row)
	}
	local := f.seedAuth("one", "user_update", f.admin)
	f.seedAuth("two", "user_update", f.other)
	out = f.callAudit(admin, "?filterEvent=user_update", nil, 200)
	if out["page"].(map[string]any)["total"] != float64(1) || auditListRows(t, out)[0].(map[string]any)["ID"] != local {
		t.Fatal("tenant leakage", out)
	}
	f.callAudit(admin, "?pageIdx=0", nil, 400)
	f.callAudit(admin, "?pageSize=-1&pageIdx=2", nil, 400)
	f.callAudit(admin, "?createSort=0", nil, 400)
	out = f.callAudit(admin, "?keyword=%25", nil, 200)
	if len(auditListRows(t, out)) != 0 {
		t.Fatal("LIKE wildcard interpreted", out)
	}
	// Domain task events are hidden until the exact current F47 scope allows them.
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) VALUES('domain-audit-source','one','domain-a','synthetic.read',1,'{}','hash',$1,$2,'domain-audit-source','succeeded',1)`, f.admin, fmt.Sprint(f.version(f.admin)))
	f.exec(`INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version) VALUES('domain-audit-source','one','domain-a',$1,'finished','succeeded',1,1,1,$2)`, f.admin, fmt.Sprint(f.version(f.admin)))
	out = f.callAudit(admin, "?filterEvent=finished", nil, 200)
	if len(auditListRows(t, out)) != 0 {
		t.Fatal("administrator bypassed F47", out)
	}
	f.exec(`INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('one','audit-domain','Audit domain');INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('one','audit-domain','domain-a');INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('one','platform_admin','audit-domain')`)
	out = f.callAudit(admin, "?filterEvent=finished", nil, 200)
	if len(auditListRows(t, out)) != 1 {
		t.Fatal(out)
	}
	out = f.callAudit(operator, "?filterEvent=finished", nil, 200)
	if len(auditListRows(t, out)) != 0 {
		t.Fatal("ungranted operator saw task", out)
	}
	f.exec("UPDATE adtr.schema_version SET version=$1 WHERE singleton=true", audit.SchemaVersion-1)
	f.callAudit(admin, "", nil, 503)
}
func TestAuditReversibleVisibilityAtomicityAndProtectedLedger(t *testing.T) {
	f := newAuditFixture(t)
	c := f.client(f.admin)
	id := f.seedAuth("one", "user_update", f.admin)
	foreign := f.seedAuth("two", "user_update", f.other)
	f.callAudit(c, "/delete", f.proof(map[string]any{"id": []string{id, foreign}, "reason": "synthetic"}), 404)
	out := f.callAudit(c, "?filterEvent=user_update", nil, 200)
	if len(auditListRows(t, out)) != 1 {
		t.Fatal("partial cross-tenant mutation", out)
	}
	out = f.callAudit(c, "/delete", f.proof(map[string]any{"id": []string{id}, "reason": "synthetic visibility test"}), 200)
	if out["changed"] != float64(1) || out["visibilityRevision"] != float64(1) {
		t.Fatal(out)
	}
	out = f.callAudit(c, "?filterEvent=user_update", nil, 200)
	if len(auditListRows(t, out)) != 0 {
		t.Fatal(out)
	}
	out = f.callAudit(c, "?filterEvent=user_update&visibility=hidden", nil, 200)
	if len(auditListRows(t, out)) != 1 {
		t.Fatal(out)
	}
	controls := f.callAudit(c, "?filterEvent=audit_hide", nil, 200)
	control := auditListRows(t, controls)[0].(map[string]any)
	if control["loginUser"] != "admin" || control["sourceIp"] != "127.0.0.1" || control["deletable"] != false {
		t.Fatal("untrusted or missing metadata", control)
	}
	if strings.Contains(control["eventArgs"].(string), resourceTestPassword) || strings.Contains(control["eventArgs"].(string), "203.0.113.99") {
		t.Fatal("secret or forwarded header captured", control)
	}
	f.callAudit(c, "/delete", f.proof(map[string]any{"id": []string{id}, "reason": "already hidden"}), 409)
	f.callAudit(c, "/delete", f.proof(map[string]any{"id": []string{control["ID"].(string)}, "reason": "conceal ledger"}), 409)
	f.callAudit(c, "/restore", f.proof(map[string]any{"id": []string{id}, "reason": "restore"}), 200)
	for _, sql := range []string{`UPDATE adtr.auth_audit SET action='tampered' WHERE tenant_id='one'`, `DELETE FROM adtr.audit_events`, `TRUNCATE adtr.audit_events`} {
		if _, err := f.conn.Exec(f.ctx, sql); err == nil {
			t.Fatal("mutable evidence", sql)
		}
	}
	f.exec(`CREATE FUNCTION adtr.reject_synthetic_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic control audit unavailable';END;$$;CREATE TRIGGER reject_synthetic_audit BEFORE INSERT ON adtr.audit_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_synthetic_audit();`)
	body := f.proof(map[string]any{"id": []string{id}, "reason": "must roll back"})
	f.callAudit(c, "/delete", body, 500)
	out = f.callAudit(c, "?filterEvent=user_update", nil, 200)
	if len(auditListRows(t, out)) != 1 {
		t.Fatal("visibility survived ledger failure", out)
	}
	f.exec(`DROP TRIGGER reject_synthetic_audit ON adtr.audit_events`)
	f.callAudit(c, "/delete", body, 200)
}
func TestAuditExportRealXLSXSnapshotBatchesAndDownloadRevocation(t *testing.T) {
	f := newAuditFixture(t)
	c := f.client(f.operator)
	f.exec(`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at) SELECT 'one',$1,'user_update',g,'2020-02-01T12:00:00Z'::timestamptz FROM generate_series(1,10001) g`, f.operator)
	body := exportBody("batch-export", "eventArgs", "CreateTm", "loginUser")
	out := f.callAudit(c, "/exports", f.proof(body), 200)
	id := f.taskID(out)
	// Same actor and same payload replays one durable task using a fresh proof.
	replay := f.callAudit(c, "/exports", f.proof(exportBody("batch-export", "eventArgs", "CreateTm", "loginUser")), 200)
	if replay["replayed"] != true || f.taskID(replay) != id {
		t.Fatal(replay)
	}
	f.runExport()
	detail := f.callAudit(c, "/exports/detail?taskUUID="+id, nil, 200)
	if detail["task"].(map[string]any)["state"] != "succeeded" || detail["rowCount"] != float64(10001) || detail["downloadReady"] != true {
		t.Fatal(detail)
	}
	response := f.auditResponse(c, "/exports/download?taskUUID="+id, nil)
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Body)
	}
	zipped, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal("not XLSX", err)
	}
	var sheet []byte
	for _, file := range zipped.File {
		if file.Name == "xl/worksheets/sheet1.xml" {
			r, e := file.Open()
			if e != nil {
				t.Fatal(e)
			}
			sheet, e = io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	decoder := xml.NewDecoder(bytes.NewReader(sheet))
	rowCount := 0
	var texts []string
	for {
		token, e := decoder.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Local == "row" {
				rowCount++
			}
			if start.Name.Local == "t" {
				var text string
				if e = decoder.DecodeElement(&text, &start); e != nil {
					t.Fatal(e)
				}
				texts = append(texts, text)
			}
		}
	}
	if rowCount != 10002 || !strings.Contains(strings.Join(texts, "\n"), `"targetId": 10001`) || !strings.Contains(strings.Join(texts, "\n"), `"targetId": 1}`) {
		t.Fatal("wrong actual first/last or batch count", rowCount)
	}
	var snapshots, records int
	if err = f.conn.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.audit_export_snapshots WHERE task_id=$1),(SELECT count(*) FROM adtr.audit_export_rows WHERE task_id=$1)`, id).Scan(&snapshots, &records); err != nil || snapshots != 1 || records != 10001 {
		t.Fatal(snapshots, records, err)
	}
	f.callAudit(f.client(f.admin), "/exports/detail?taskUUID="+id, nil, 404)
	f.callAudit(f.client(f.other), "/exports/detail?taskUUID="+id, nil, 404)
	// Revoking tasks.readable alone does not block the dedicated read endpoints,
	// but changes the actor epoch and irrevocably invalidates this older export.
	f.exec(`UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='audit_exports'`, narrowResourceRole)
	f.exec(`UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='audit_exports'`, narrowResourceRole)
	f.callAudit(c, "/exports/download?taskUUID="+id, nil, 403)
}
func TestAuditExportEmptyOverflowAndStaleVisibility(t *testing.T) {
	f := newAuditFixture(t)
	c := f.client(f.admin)
	empty := f.taskID(f.callAudit(c, "/exports", f.proof(exportBody("empty", "event")), 200))
	f.runExport()
	out := f.callAudit(c, "/exports/detail?taskUUID="+empty, nil, 200)
	if out["rowCount"] != float64(0) || out["downloadReady"] != true {
		t.Fatal("empty results must be truthful header-only workbook", out)
	}
	id := f.seedAuth("one", "user_update", f.admin)
	f.callAudit(c, "/delete", f.proof(map[string]any{"id": []string{id}, "reason": "visibility changed"}), 200)
	f.callAudit(c, "/exports/detail?taskUUID="+empty, nil, 409)
	f.callAudit(c, "/exports/download?taskUUID="+empty, nil, 409)
	f.exec(`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at) SELECT 'one',$1,'user_update',g,'2020-02-01T12:00:00Z'::timestamptz FROM generate_series(1,100001) g`, f.admin)
	oversized := f.taskID(f.callAudit(c, "/exports", f.proof(exportBody("oversized", "event")), 200))
	f.runExport()
	out = f.callAudit(c, "/exports/detail?taskUUID="+oversized, nil, 200)
	task := out["task"].(map[string]any)
	if task["state"] != "failed" || task["error"] != "result_too_large" || out["downloadReady"] != false {
		t.Fatal(out)
	}
	var count int
	if err := f.conn.QueryRow(f.ctx, `SELECT count(*) FROM adtr.audit_export_snapshots WHERE task_id=$1`, oversized).Scan(&count); err != nil || count != 0 {
		t.Fatal("oversize partial snapshot", count, err)
	}
	f.callAudit(c, "/exports/download?taskUUID="+oversized, nil, 409)
	f.callAudit(c, "?pageSize=-1&filterEvent="+url.QueryEscape("user_update"), nil, 422)
}

func TestAuditExportDomainExpiryWithoutEpochMutation(t *testing.T) {
	f := newAuditFixture(t)
	c := f.client(f.admin)
	f.exec(`INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('one','expiry-scope','Expiry scope');INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('one','expiry-scope','domain-a');INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('one','platform_admin','expiry-scope')`)
	expires := time.Now().Unix() + 5
	f.exec(`UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'`, expires)
	epoch := f.version(f.admin)
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) VALUES('expiring-source','one','domain-a','synthetic.read',1,'{}','hash',$1,$2,'expiring-source','succeeded',1)`, f.admin, fmt.Sprint(epoch))
	f.exec(`INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version,occurred_at) VALUES('expiring-source','one','domain-a',$1,'finished','succeeded',1,1,1,$2,'2020-02-01T12:00:00Z')`, f.admin, fmt.Sprint(epoch))
	body := exportBody("expiring-domain", "event")
	body["filterEvent"] = []string{"finished"}
	id := f.taskID(f.callAudit(c, "/exports", f.proof(body), 200))
	f.runExport()
	out := f.callAudit(c, "/exports/detail?taskUUID="+id, nil, 200)
	if out["rowCount"] != float64(1) || out["downloadReady"] != true {
		t.Fatal(out)
	}
	time.Sleep(max(0, time.Until(time.Unix(expires, 0).Add(50*time.Millisecond))))
	if f.version(f.admin) != epoch {
		t.Fatal("test changed epoch rather than time")
	}
	f.callAudit(c, "/exports/detail?taskUUID="+id, nil, 403)
	f.callAudit(c, "/exports/download?taskUUID="+id, nil, 403)
	replayBody := exportBody("expiring-domain", "event")
	replayBody["filterEvent"] = []string{"finished"}
	replay := f.callAudit(c, "/exports", f.proof(replayBody), 200)
	replayedTask := replay["task"].(map[string]any)
	if replay["replayed"] != true || len(replayedTask["result"].(map[string]any)) != 0 || len(replayedTask["cursor"].(map[string]any)) != 0 {
		t.Fatal("replay leaked expired snapshot metadata", replay)
	}
}

func TestAuditExportRetrySnapshotAndCancelledManifestNeverPublish(t *testing.T) {
	f := newAuditFixture(t)
	c := f.client(f.admin)
	f.exec(`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at) SELECT 'one',$1,'user_update',g,'2020-02-01T12:00:00Z'::timestamptz FROM generate_series(1,2) g`, f.admin)
	id := f.taskID(f.callAudit(c, "/exports", f.proof(exportBody("retry-snapshot", "eventArgs")), 200))
	first, err := f.engine.Claim(f.ctx, "first-audit-worker")
	if err != nil {
		t.Fatal(err)
	}
	first, err = f.engine.Start(f.ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var firstVersion int64
	interrupted := audit.Kind().Execute(f.ctx, tasks.Execution{Task: first.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		calls++
		if calls > 1 {
			return 0, context.Canceled
		}
		next, e := f.engine.WithTx(ctx, first, v, work)
		firstVersion = next
		return next, e
	}})
	if interrupted.State != tasks.Failed || !interrupted.Retryable || interrupted.Code != "worker_interrupted" {
		t.Fatal(interrupted)
	}
	if _, err = f.engine.Finish(f.ctx, first, interrupted); err != nil {
		t.Fatal(err)
	}
	// New events cannot enter an already durable snapshot on retry.
	f.exec(`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at) VALUES('one',$1,'user_update',999,'2020-02-01T12:00:00Z')`, f.admin)
	f.exec(`UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id)
	second, err := f.engine.Claim(f.ctx, "second-audit-worker")
	if err != nil {
		t.Fatal(err)
	}
	second, err = f.engine.Start(f.ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = f.engine.WithTx(f.ctx, first, firstVersion, func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		called = true
		return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
	})
	if !errors.Is(err, tasks.ErrLeaseLost) || called {
		t.Fatal("stale worker entered artifact transaction", called, err)
	}
	outcome := audit.Kind().Execute(f.ctx, tasks.Execution{Task: second.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, second, v, work)
	}})
	if outcome.State != tasks.Succeeded {
		t.Fatal(outcome)
	}
	var result struct {
		RowCount      int   `json:"rowCount"`
		ArtifactToken int64 `json:"artifactToken"`
	}
	if json.Unmarshal(outcome.Result, &result) != nil || result.RowCount != 2 || result.ArtifactToken != second.Token {
		t.Fatal(string(outcome.Result))
	}
	var lateRows int
	if err = f.conn.QueryRow(f.ctx, `SELECT count(*) FROM adtr.audit_export_rows WHERE task_id=$1 AND row_data->>'eventArgs' LIKE '%999%'`, id).Scan(&lateRows); err != nil || lateRows != 0 {
		t.Fatal("retry refreshed snapshot", lateRows, err)
	}
	// Cancellation after staging a complete workbook but before Finish must still
	// prevent publication, even when the executor truthfully returned success.
	f.callTask(c, "/cancel", f.proof(map[string]any{"taskUUID": id}), 200)
	final, err := f.engine.Finish(f.ctx, second, outcome)
	if err != nil || final.State != tasks.Cancelled || string(final.Result) != "{}" {
		t.Fatal(final, err)
	}
	f.callAudit(c, "/exports/download?taskUUID="+id, nil, 409)
}
