//go:build integration

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationlabels"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http/httptest"
	"strings"
	"testing"
)

// This test owns an isolated PostgreSQL fixture and fails rather than skipping
// when PostgreSQL is unavailable. It never modifies an existing deployment.
func TestSimulationDetectionHTTPAuthorizationRecoveryAndAuditAtomicity(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + simulationdetectionschedules.SchemaSQL + simulationdetectionschedules.ImmutableSchemaSQL)
	f.exec(SimulationLabelsPermissionSchema + simulationlabels.SchemaSQL + simulationlabels.ImmutableSchemaSQL)
	f.exec(SimulationRuleCatalogPermissionSchema + simulationrulecatalog.SchemaSQL + simulationrulecatalog.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_detection',true,true)", narrowResourceRole)
	f.seedGroup("one", "detection-group", narrowResourceRole, "domain-a")
	authorizer := simulationDetectionTaskAuthorizer(f.s.now)
	engine, err := tasks.New(f.s.database, tasks.ProductionRegistry(simulationdetection.NewKind(authorizer)), authorizer, schemaversion.Current)
	if err != nil {
		t.Fatal(err)
	}
	call := func(c *resourceTestClient, path string, body any, want int) map[string]any {
		t.Helper()
		method := "GET"
		var raw []byte
		if body != nil {
			method = "POST"
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, simulationDetectionPath+path, bytes.NewReader(raw))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		f.s.SimulationDetectionHandler(true, engine).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", path, w.Code, want, w.Body)
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Body)
		}
		return out
	}
	checkWorker := func(actor int64, domain string, action tasks.Action, want bool) {
		t.Helper()
		tx, err := f.conn.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		version, err := authorizer(f.ctx, tx, tasks.Principal{TenantID: "one", ActorID: actor}, tasks.Scope{TaskName: simulationdetection.KindName, DomainID: domain}, action)
		if want && (err != nil || version == "") {
			t.Fatal("worker authorization denied", err)
		}
		if !want && !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("worker authorization accepted", err)
		}
	}
	checkWorker(f.operator, "domain-a", tasks.Execute, true)
	checkWorker(f.operator, "domain-b", tasks.Execute, false)
	checkWorker(f.admin, "domain-a", tasks.Execute, false)
	checkWorker(f.viewer, "domain-a", tasks.Read, false)
	op := f.client(f.operator)
	// Current browser write requires both the session CSRF token and exact origin.
	for _, csrf := range []string{"", "forged"} {
		r := httptest.NewRequest("POST", simulationDetectionPath+"/archive", strings.NewReader(`{"domainId":"domain-a","templateId":"t","operationId":"csrf-op","expectedVersion":1}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(op.cookie)
		w := httptest.NewRecorder()
		f.s.SimulationDetectionHandler(true, engine).ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("CSRF accepted", w.Code, w.Body)
		}
	}
	// Permission changes must retain every previous epoch guard.
	var guards int
	if err := f.conn.QueryRow(f.ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid='adtr.access_permissions'::regclass AND tgname IN ('task_auth_permissions','simulation_event_grant_epoch','simulation_risk_grant_epoch','simulation_detection_grant_epoch') AND tgenabled='O'`).Scan(&guards); err != nil || guards != 4 {
		t.Fatal("permission guards replaced", guards, err)
	}
	call(nil, "/templates?domainId=domain-a", nil, 401)
	call(f.client(f.admin), "/templates?domainId=domain-a", nil, 403)
	call(f.client(f.viewer), "/templates?domainId=domain-a", nil, 403)
	call(op, "/templates?domainId=domain-b", nil, 404)
	input := map[string]any{"domainId": "domain-a", "templateId": "template-a", "name": "Synthetic only", "operationId": "create-a", "rules": []any{map[string]any{"id": "rule-a", "enabled": true, "combine": "all", "conditions": []any{map[string]any{"field": "event.code", "operator": "eq", "values": []string{"4625"}}}}}}
	created := call(op, "/create", input, 200)
	replay := call(op, "/create", input, 200)
	a, _ := json.Marshal(created)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("nonexact replay", string(a), string(b))
	}
	receipt := call(op, "/receipt?domainId=domain-a&operationId=create-a", nil, 200)
	b, _ = json.Marshal(receipt)
	if string(a) != string(b) {
		t.Fatal("receipt mismatch")
	}
	input["name"] = "Changed input"
	call(op, "/create", input, 409)
	input["operationId"] = "update-a"
	input["expectedVersion"] = 1
	call(op, "/update", input, 200)
	call(op, "/archive", map[string]any{"domainId": "domain-a", "templateId": "template-a", "operationId": "archive-a", "expectedVersion": 2}, 200)
	var count int
	if err = f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.auth_audit WHERE action='simulation_detection_create'").Scan(&count); err != nil || count != 1 {
		t.Fatal("audit replay", count, err)
	}
	// New mutation must roll back when audit append fails.
	f.exec(`CREATE FUNCTION adtr.reject_detection_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='simulation_detection_create' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER reject_detection_audit BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_detection_audit();`)
	delete(input, "expectedVersion")
	input["templateId"] = "rollback-a"
	input["operationId"] = "rollback-a"
	call(op, "/create", input, 503)
	call(op, "/template?domainId=domain-a&templateId=rollback-a", nil, 404)
	call(op, "/receipt?domainId=domain-a&operationId=rollback-a", nil, 404)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection'", narrowResourceRole)
	call(op, "/receipt?domainId=domain-a&operationId=create-a", nil, 403)
	checkWorker(f.operator, "domain-a", tasks.Execute, false)
	f.exec("UPDATE adtr.schema_version SET version=25")
	out := call(op, "/templates?domainId=domain-a", nil, 503)
	if !strings.Contains(out["error"].(string), "schema_incompatible") {
		t.Fatal(out)
	}
}
