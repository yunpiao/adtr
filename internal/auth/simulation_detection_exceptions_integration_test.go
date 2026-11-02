//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	exceptions "github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationlabels"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http/httptest"
	"testing"
	"time"
)

// Mandatory isolated PostgreSQL: missing DSN is a failure, never a skip.
func TestSimulationDetectionExceptionsHTTPAuthorityAndRevocation(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + simulationdetectionschedules.SchemaSQL + simulationdetectionschedules.ImmutableSchemaSQL)
	f.exec(SimulationLabelsPermissionSchema + simulationlabels.SchemaSQL + simulationlabels.ImmutableSchemaSQL)
	f.exec(SimulationRuleCatalogPermissionSchema + simulationrulecatalog.SchemaSQL + simulationrulecatalog.ImmutableSchemaSQL)
	f.exec(SimulationDetectionExceptionsPermissionSchema + exceptions.SchemaSQL + exceptions.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.seedGroup("one", "exception-writers", narrowResourceRole, "domain-a")
	f.seedGroup("one", "exception-readers", widerResourceRole, "domain-a")
	f.exec("UPDATE adtr.users SET role_id=$1 WHERE id=$2", widerResourceRole, f.viewer)
	authorize := simulationDetectionTaskAuthorizer(f.s.now)
	engine, e := tasks.New(f.s.database, tasks.ProductionRegistry(simulationdetection.NewKind(authorize)), authorize, schemaversion.Current)
	if e != nil {
		t.Fatal(e)
	}
	detectionEnabled, eventsEnabled := true, true
	call := func(c *resourceTestClient, path string, body any, want int) map[string]any {
		t.Helper()
		method := "GET"
		var raw []byte
		if body != nil {
			method = "POST"
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, simulationDetectionExceptionsPath+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", f.s.origin)
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		f.s.SimulationDetectionExceptionsHandler(true, detectionEnabled, eventsEnabled, engine).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got%d want%d: %s", path, w.Code, want, w.Body)
		}
		if want == 200 && w.Header().Get("X-ADTR-User-ID") == "" {
			t.Fatal("no actor binding")
		}
		if path == "/export?domainId=domain-a" && want == 200 && w.Header().Get("Content-Disposition") == "" {
			t.Fatal("missing attachment")
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("nonobject response", w.Body)
		}
		return out
	}
	snapshot := "/snapshot?domainId=domain-a"
	call(nil, snapshot, nil, 401)
	call(f.client(f.admin), snapshot, nil, 403)
	call(f.client(f.operator), snapshot, nil, 403)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_detection_exceptions',true,true),('one',$2,'simulation_detection_exceptions',true,false),('one',$1,'simulation_detection',true,true),('one',$2,'simulation_detection',true,false),('one',$1,'simulation_events',true,true),('one',$2,'simulation_events',true,false)", narrowResourceRole, widerResourceRole)
	author, reader := f.client(f.operator), f.client(f.viewer)
	call(author, snapshot, nil, 200)
	call(reader, snapshot, nil, 200)
	call(author, "/snapshot?domainId=domain-b", nil, 404)
	call(f.client(f.other), snapshot, nil, 403)
	hint := func(c *resourceTestClient, want bool) {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/auth/me", nil)
		r.AddCookie(c.cookie)
		w := httptest.NewRecorder()
		f.s.ServeHTTP(w, r)
		var u User
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &u) != nil || u.SimulationDetectionExceptionsReadable == nil || *u.SimulationDetectionExceptionsReadable != want {
			t.Fatal("profile hint", w.Code, w.Body)
		}
	}
	hint(author, true)
	hint(reader, true)
	hint(f.client(f.admin), false)
	declaration := map[string]any{"id": "global", "global": true, "sourceTaskId": "", "ruleId": "", "reason": "explicit domain-shared synthetic configuration", "notBefore": "2026-01-01T00:00:00Z", "expiresAt": "2099-01-01T00:00:00Z", "conditions": []any{map[string]any{"field": "event.outcome", "operator": "eq", "values": []string{"failure"}}}}
	create := map[string]any{"domainId": "domain-a", "operationId": "create", "expectedRevision": 1, "declaration": declaration}
	bad := *author
	bad.csrf = "forged"
	call(&bad, "/create", create, 403)
	call(reader, "/create", create, 403)
	made := call(author, "/create", create, 200)
	replay := call(author, "/create", create, 200)
	a, _ := json.Marshal(made)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("nonidentical replay")
	}
	if made["action"] != "create" || made["revision"] != float64(2) || made["intent"].(map[string]any)["declaration"].(map[string]any)["reason"] != declaration["reason"] {
		t.Fatal("intent lost", made)
	}
	call(reader, "/receipt?domainId=domain-a&operationId=create", nil, 404)
	declaration["reason"] = "different intent"
	call(author, "/create", create, 409)
	declaration["reason"] = "explicit domain-shared synthetic configuration"
	disabled := call(author, "/export?domainId=domain-a", nil, 200)
	entry := disabled["snapshot"].(map[string]any)["entries"].([]any)[0].(map[string]any)
	if entry["enabled"] != false {
		t.Fatal("draft enabled")
	}
	enable := map[string]any{"domainId": "domain-a", "operationId": "enable", "expectedRevision": 2, "id": "global"}
	call(author, "/enable", enable, 200)
	// A real existing Worker creates the trusted source; no fabricated success row.
	run := exceptionHTTPWorkerRun(t, f, engine)
	evaluation := map[string]any{"domainId": "domain-a", "operationId": "eval", "expectedRevision": 3, "id": "evaluation", "sourceTaskId": run}
	evaluated := call(author, "/evaluate", evaluation, 200)
	ev := evaluated["evaluation"].(map[string]any)
	matches := ev["matches"].([]any)
	if len(matches) != 1 || len(matches[0].(map[string]any)["exceptions"].([]any)) != 1 {
		t.Fatal("nonempty annotation proof", ev)
	}
	source := ev["source"].(map[string]any)
	original := source["matches"].([]any)
	if len(original) != 1 || original[0].(map[string]any)["event_id"] != "positive" {
		t.Fatal("original matches changed", source)
	}
	call(reader, "/evaluation?domainId=domain-a&id=evaluation", nil, 404)
	call(reader, "/evaluate", evaluation, 403)
	call(author, "/evaluation?domainId=domain-a&id=evaluation", nil, 200)
	call(author, "/receipt?domainId=domain-a&operationId=eval", nil, 200)
	// Source flags are checked on recovery too, without blanket-denying config receipts.
	detectionEnabled = false
	call(author, "/evaluation?domainId=domain-a&id=evaluation", nil, 404)
	call(author, "/receipt?domainId=domain-a&operationId=eval", nil, 403)
	call(author, "/receipt?domainId=domain-a&operationId=create", nil, 200)
	detectionEnabled = true
	eventsEnabled = false
	call(author, "/receipt?domainId=domain-a&operationId=eval", nil, 403)
	eventsEnabled = true
	recovered := call(author, "/receipt?domainId=domain-a&operationId=eval", nil, 200)
	a, _ = json.Marshal(evaluated)
	b, _ = json.Marshal(recovered)
	if string(a) != string(b) {
		t.Fatal("recovered evidence changed")
	}
	// Event read revocation is immediate. Regrant cannot resurrect an old task epoch.
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_events'", narrowResourceRole)
	call(author, "/receipt?domainId=domain-a&operationId=eval", nil, 403)
	call(author, "/receipt?domainId=domain-a&operationId=create", nil, 200)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_events'", narrowResourceRole)
	call(author, "/evaluation?domainId=domain-a&id=evaluation", nil, 404)
	call(author, "/receipt?domainId=domain-a&operationId=eval", nil, 404)
	// Later controls do not mutate immutable historical evaluation bytes.
	control := map[string]any{"domainId": "domain-a", "operationId": "disable", "expectedRevision": 3, "id": "global"}
	call(author, "/disable", control, 200)
	old := call(author, "/history?domainId=domain-a&revision=3", nil, 200)
	if old["snapshot"].(map[string]any)["entries"].([]any)[0].(map[string]any)["enabled"] != true {
		t.Fatal("history rewritten")
	}
	control["operationId"] = "archive"
	control["expectedRevision"] = 4
	call(author, "/archive", control, 200)
	// Failed imports and audit failures must be atomic and leave no receipt.
	invalid := map[string]any{"domainId": "domain-a", "operationId": "import-invalid", "expectedRevision": 5, "entries": []any{declaration, declaration}}
	call(author, "/import", invalid, 400)
	call(author, "/receipt?domainId=domain-a&operationId=import-invalid", nil, 404)
	f.exec(`CREATE FUNCTION adtr.reject_exception_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='simulation_detection_exceptions_create' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER reject_exception_audit BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_exception_audit();`)
	declaration["id"] = "rollback"
	create["operationId"] = "rollback"
	create["expectedRevision"] = 5
	call(author, "/create", create, 503)
	call(author, "/receipt?domainId=domain-a&operationId=rollback", nil, 404)
	current := call(author, snapshot, nil, 200)
	if current["snapshot"].(map[string]any)["revision"] != float64(5) {
		t.Fatal("audit failure persisted mutation")
	}
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection_exceptions'", narrowResourceRole)
	call(author, snapshot, nil, 403)
	call(author, "/receipt?domainId=domain-a&operationId=create", nil, 403)
	hint(author, false)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current+1)
	call(reader, snapshot, nil, 503)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.exec("UPDATE adtr.users SET disabled=true WHERE id=$1", f.viewer)
	call(reader, snapshot, nil, 401)
}

func exceptionHTTPWorkerRun(t *testing.T, f *taskFixture, engine *tasks.Engine) string {
	t.Helper()
	provenance := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "exception-http-worker", Synthetic: true}
	raw := []securityevents.RawEvent{}
	for _, v := range []struct{ id, kind string }{{"positive", "login_failure"}, {"negative", "login_success"}} {
		raw = append(raw, securityevents.RawEvent{ID: v.id, Tenant: "one", Domain: "domain-a", Kind: v.kind, Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: provenance})
	}
	scope := simulationdetection.Scope{Tenant: "one", Domain: "domain-a", ActorID: f.operator, Read: true, Manage: true}
	audit := func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil }
	tx, e := f.conn.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	if e = tasks.CheckSchemaTx(f.ctx, tx, schemaversion.Current); e != nil {
		t.Fatal(e)
	}
	if _, e = simulationevents.SeedFixtureTx(f.ctx, tx, securityevents.Scope{Tenant: "one", Domain: "domain-a", Read: true, Manage: true}, simulationevents.Fixture{ID: "dataset", Provenance: provenance, Events: raw}); e != nil {
		t.Fatal(e)
	}
	rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Enabled: true, Scope: detectionrules.Scope{Tenant: "one", Domain: "domain-a"}, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
	if _, e = simulationdetection.MutateTx(f.ctx, tx, scope, simulationdetection.Mutation{ID: "template", Name: "SIMULATED HTTP exception proof", OperationID: "template", Rules: []detectionrules.Rule{rule}}, audit); e != nil {
		t.Fatal(e)
	}
	sub, e := simulationdetection.SubmitRunTx(f.ctx, tx, scope, engine, simulationdetection.RunInput{TemplateID: "template", TemplateVersion: 1, DatasetID: "dataset", OperationID: "run"}, audit)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	worker, e := tasks.NewWorker(engine, tasks.WorkerConfig{Owner: "exception-http-worker", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	ctx, stop := context.WithTimeout(f.ctx, 20*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { stop(); <-done }()
	for {
		tx, e := f.conn.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if e = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); e != nil {
			tx.Rollback(ctx)
			t.Fatal(e)
		}
		d, e := simulationdetection.ReadRunTx(ctx, tx, scope, engine, sub.Task.ID)
		tx.Rollback(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if d.Task.State == tasks.Succeeded {
			var r simulationdetection.Result
			if json.Unmarshal(d.Task.Result, &r) != nil || r.EventCount != 2 || len(r.Matches) != 1 || r.Matches[0].EventID != "positive" {
				t.Fatal("Worker exact result missing")
			}
			return sub.Task.ID
		}
		if d.Task.State.Terminal() {
			t.Fatal("Worker failed", d.Task.State)
		}
		select {
		case <-ctx.Done():
			t.Fatal("Worker deadline")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
