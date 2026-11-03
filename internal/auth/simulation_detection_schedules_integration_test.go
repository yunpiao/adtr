//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/simulationassetassessment"
	"github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/simulationdnsip"
	"github.com/yunpiao/adtr/internal/simulationlabels"
	"github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	schedules "github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Requires real isolated PostgreSQL. No DSN fails rather than counts as a pass.
func TestSimulationDetectionSchedulesHTTPAuthorityReceiptsAndCAS(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + schedules.SchemaSQL + schedules.ImmutableSchemaSQL)
	f.exec(SimulationLabelsPermissionSchema + simulationlabels.SchemaSQL + simulationlabels.ImmutableSchemaSQL)
	f.exec(SimulationRuleCatalogPermissionSchema + simulationrulecatalog.SchemaSQL + simulationrulecatalog.ImmutableSchemaSQL)
	f.exec(SimulationDetectionExceptionsPermissionSchema + simulationdetectionexceptions.SchemaSQL + simulationdetectionexceptions.ImmutableSchemaSQL)
	f.exec(SimulationDNSIPPermissionSchema + simulationdnsip.SchemaSQL + simulationdnsip.ImmutableSchemaSQL)
	f.exec(SimulationAssetAssessmentPermissionSchema + simulationassetassessment.SchemaSQL + simulationassetassessment.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.seedGroup("one", "schedule-group", narrowResourceRole, "domain-a")
	auth := simulationDetectionSchedulesTaskAuthorizer(f.s.now)
	queue, err := tasks.New(f.s.database, tasks.ProductionRegistry(schedules.NewKind(auth)), auth, schemaversion.Current)
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
		r := httptest.NewRequest(method, simulationDetectionSchedulesPath+path, bytes.NewReader(raw))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		f.s.SimulationDetectionSchedulesHandler(true, true, queue).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got%d want%d %s", path, w.Code, want, w.Body)
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Body)
		}
		return out
	}
	list := "/list?domainId=domain-a&pageIdx=1&pageSize=20"
	call(nil, list, nil, 401)
	call(f.client(f.admin), list, nil, 403)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_detection',true,true)", narrowResourceRole)
	call(f.client(f.operator), list, nil, 403) // manual-only never acquires scheduling
	f.exec("UPDATE adtr.access_permissions SET mark='simulation_detection_schedules' WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection'", narrowResourceRole)
	call(f.client(f.operator), list, nil, 403) // schedule-only lacks actual detection authority
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_detection',true,true)", narrowResourceRole)
	op := f.client(f.operator)
	call(op, list, nil, 200)
	call(op, "/list?domainId=domain-b&pageIdx=1&pageSize=20", nil, 404)
	// Seed explicitly synthetic, immutable input using existing validated fixture API.
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	provenance := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "schedule-auth-fixture", Synthetic: true}
	_, err = simulationevents.SeedFixtureTx(f.ctx, tx, securityevents.Scope{Tenant: "one", Domain: "domain-a", Read: true, Manage: true}, simulationevents.Fixture{ID: "dataset", Provenance: provenance, Events: []securityevents.RawEvent{}})
	if err != nil {
		tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	_, err = simulationdetection.MutateTx(f.ctx, tx, simulationdetection.Scope{Tenant: "one", Domain: "domain-a", ActorID: f.operator, Read: true, Manage: true}, simulationdetection.Mutation{ID: "template", Name: "Synthetic schedule template", OperationID: "seed-template", Rules: []detectionrules.Rule{{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: detectionrules.Scope{Tenant: "one", Domain: "domain-a"}, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}}}, func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil })
	if err != nil {
		tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"domainId": "domain-a", "label": "Synthetic schedule", "cycle": 0, "templateId": "template", "templateVersion": 1, "datasetId": "dataset", "operationId": "create"}
	created := call(op, "/create", body, 200)
	schedule := created["schedule"].(map[string]any)
	id := schedule["id"].(string)
	if schedule["state"] != "paused" || schedule["controlVersion"] != float64(1) {
		t.Fatal(created)
	}
	// A second explicitly authorized actor still cannot read this owner's row.
	f.exec("UPDATE adtr.users SET role_id=$1 WHERE id=$2", narrowResourceRole, f.viewer)
	peer := f.client(f.viewer)
	peerList := call(peer, list, nil, 200)
	if len(peerList["schedules"].([]any)) != 0 {
		t.Fatal("owner rows leaked", peerList)
	}
	call(peer, "/detail?domainId=domain-a&id="+id+"&pageIdx=1&pageSize=20", nil, 404)
	call(peer, "/receipt?domainId=domain-a&operationId=create", nil, 404)
	if !call(op, "/create", body, 200)["replayed"].(bool) {
		t.Fatal("missing replay")
	}
	body["label"] = "Changed request"
	call(op, "/create", body, 409)
	control := map[string]any{"domainId": "domain-a", "id": id, "operationId": "enable", "expectedControlVersion": 1}
	call(op, "/enable", control, 200)
	receipt := call(op, "/receipt?domainId=domain-a&operationId=create", nil, 200)
	if receipt["receipt"].(map[string]any)["state"] != "paused" || receipt["schedule"].(map[string]any)["state"] != "enabled" {
		t.Fatal("historical receipt mislabeled current", receipt)
	}
	control["operationId"] = "stale-pause"
	call(op, "/pause", control, 409)
	// CSRF remains enforced independently of route and grant checks.
	r := httptest.NewRequest("POST", simulationDetectionSchedulesPath+"/pause", bytes.NewBufferString(`{"domainId":"domain-a","id":"`+id+`","operationId":"csrf","expectedControlVersion":2}`))
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(op.cookie)
	w := httptest.NewRecorder()
	f.s.SimulationDetectionSchedulesHandler(true, true, queue).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF accepted", w.Code)
	}
	// Audit failure must roll back control, event and receipt atomically.
	f.exec(`CREATE FUNCTION adtr.reject_schedule_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='simulation_detection_schedules_paused' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER reject_schedule_audit BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_schedule_audit();`)
	control["operationId"] = "rollback-pause"
	control["expectedControlVersion"] = 2
	call(op, "/pause", control, 503)
	call(op, "/receipt?domainId=domain-a&operationId=rollback-pause", nil, 404)
	detail := call(op, "/detail?domainId=domain-a&id="+id+"&pageIdx=1&pageSize=20", nil, 200)
	if detail["schedule"].(map[string]any)["controlVersion"] != float64(2) {
		t.Fatal("audit rollback failed", detail)
	}
	before := f.version(f.operator)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection_schedules'", narrowResourceRole)
	if f.version(f.operator) <= before {
		t.Fatal("new grant failed to bump epoch")
	}
	call(f.client(f.operator), list, nil, 403)
	tx, err = f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth(f.ctx, tx, tasks.Principal{TenantID: "one", ActorID: f.operator}, tasks.Scope{TaskName: schedules.KindName, DomainID: "domain-a"}, tasks.Execute)
	tx.Rollback(f.ctx)
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("revoked worker accepted", err)
	}
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection_schedules'", narrowResourceRole)
	control["operationId"] = "restored-cannot-revive"
	call(f.client(f.operator), "/enable", control, 403)
}
