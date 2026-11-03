//go:build integration

package auth

import (
	"bytes"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/schemaversion"
	assessment "github.com/yunpiao/adtr/internal/simulationassetassessment"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationdnsip"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationlabels"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"net/http/httptest"
	"testing"
)

// Requires real isolated PostgreSQL; absent database is a failure, never a skip.
func TestSimulationAssetAssessmentHTTPAuthorityEpochAndRollback(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + simulationdetectionschedules.SchemaSQL + simulationdetectionschedules.ImmutableSchemaSQL)
	f.exec(SimulationLabelsPermissionSchema + simulationlabels.SchemaSQL + simulationlabels.ImmutableSchemaSQL)
	f.exec(SimulationRuleCatalogPermissionSchema + simulationrulecatalog.SchemaSQL + simulationrulecatalog.ImmutableSchemaSQL)
	f.exec(SimulationDetectionExceptionsPermissionSchema + simulationdetectionexceptions.SchemaSQL + simulationdetectionexceptions.ImmutableSchemaSQL)
	f.exec(SimulationDNSIPPermissionSchema + simulationdnsip.SchemaSQL + simulationdnsip.ImmutableSchemaSQL)
	f.exec(SimulationAssetAssessmentPermissionSchema + assessment.SchemaSQL + assessment.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)

	f.seedGroup("one", "asset-writers", narrowResourceRole, "domain-a")
	f.seedGroup("one", "asset-readers", widerResourceRole, "domain-a")
	f.exec("UPDATE adtr.users SET role_id=$1 WHERE id=$2", widerResourceRole, f.viewer)
	call := func(c *resourceTestClient, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		method := "GET"
		var raw []byte
		if body != nil {
			method = "POST"
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, simulationAssetAssessmentPath+path, bytes.NewReader(raw))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		f.s.SimulationAssetAssessmentHandler(true).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s status=%d want=%d", path, w.Code, want)
		}
		return w
	}
	obsPath := "/observation?domainId=domain-a&observationId=synthetic-assets-v1"
	call(nil, obsPath, nil, 401)
	call(f.client(f.admin), obsPath, nil, 403)
	call(f.client(f.operator), obsPath, nil, 403)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_asset_assessment',true,true),('one',$2,'simulation_asset_assessment',true,false)", narrowResourceRole, widerResourceRole)
	author, reader := f.client(f.operator), f.client(f.viewer)
	tx, e := f.conn.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	obs, e := assessment.SeedFixtureTx(f.ctx, tx, "one", "domain-a")
	if e != nil {
		tx.Rollback(f.ctx)
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	call(author, obsPath, nil, 200)
	call(reader, obsPath, nil, 200)
	call(author, "/observation?domainId=domain-b&observationId=synthetic-assets-v1", nil, 404)
	call(f.client(f.other), obsPath, nil, 403)
	input := map[string]any{"domainId": "domain-a", "id": "report-1", "operationId": "op-1", "observationId": obs.ID, "expectedSourceHash": obs.Hash, "scenarioId": "baseline"}
	bad := *author
	bad.csrf = "forged"
	call(&bad, "/create", input, 403)
	call(reader, "/create", input, 403)
	var first, replay assessment.Receipt
	if e = json.Unmarshal(call(author, "/create", input, 200).Body.Bytes(), &first); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(call(author, "/create", input, 200).Body.Bytes(), &replay); e != nil {
		t.Fatal(e)
	}
	if first.Report.Hash == "" || first.Report.Hash != replay.Report.Hash || first.Report.Epoch <= 0 || first.Report.ActorID != f.operator || first.Report.Summary.Total != 11 {
		t.Fatal("report identity/aggregation")
	}
	found := false
	for _, row := range first.Report.Rows {
		if row.ObjectID == "user-disabled-never" {
			found = true
			if row.AccountStatus.Value != "disabled" || row.Activity.Value != "never_observed" {
				t.Fatal("real classifier")
			}
		}
	}
	if !found {
		t.Fatal("fixture result missing")
	}
	call(author, "/detail?domainId=domain-a&id=report-1", nil, 200)
	call(reader, "/detail?domainId=domain-a&id=report-1", nil, 404)
	call(author, "/detail?domainId=domain-b&id=report-1", nil, 404)
	var page assessment.RowsPage
	e = json.Unmarshal(call(author, "/rows?domainId=domain-a&id=report-1&kind=group&accountStatus=&activity=&groupType=&groupScope=&offset=0&limit=1", nil, 200).Body.Bytes(), &page)
	if e != nil || page.Matched != 4 || len(page.Rows) != 1 || page.Summary.Total != 11 {
		t.Fatal("full summary before paging", e)
	}
	call(author, "/receipt?domainId=domain-a&operationId=op-1", nil, 200)
	call(reader, "/receipt?domainId=domain-a&operationId=op-1", nil, 404)
	// Matching domain/report IDs and explicit rights in another tenant reveal nothing.
	// A custom role requires the viewer base role (users_custom_role_base).
	// f.other begins as a platform administrator; convert both fields atomically.
	f.exec("UPDATE adtr.users SET role='viewer',role_id=$1 WHERE tenant_id='two' AND id=$2", widerResourceRole, f.other)
	var foreignBaseRole, foreignCustomRole string
	if e = f.conn.QueryRow(f.ctx, "SELECT role,role_id FROM adtr.users WHERE tenant_id='two' AND id=$1", f.other).Scan(&foreignBaseRole, &foreignCustomRole); e != nil || foreignBaseRole != "viewer" || foreignCustomRole != widerResourceRole {
		t.Fatal("foreign operator fixture role contract", e)
	}
	f.seedGroup("two", "foreign-assets", widerResourceRole, "domain-a")
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('two',$1,'simulation_asset_assessment',true,true)", widerResourceRole)
	foreign := f.client(f.other)
	call(foreign, "/detail?domainId=domain-a&id=report-1", nil, 404)
	call(foreign, "/receipt?domainId=domain-a&operationId=op-1", nil, 404)

	input["scenarioId"] = "stale"
	call(author, "/create", input, 409)
	input["scenarioId"] = "baseline"
	var count int
	if e = f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.auth_audit WHERE action='simulation_asset_assessment_create'").Scan(&count); e != nil || count != 1 {
		t.Fatal("idempotent audit", count, e)
	}
	// Profile is a fail-closed navigation hint, not an authorization substitute.
	profile := func(id int64, want bool) {
		t.Helper()
		tx, err := f.conn.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		got, err := simulationAssetAssessmentProfileReadable(f.ctx, tx, User{ID: id, tenant: "one"})
		if err != nil || got != want {
			t.Fatalf("profile got=%v want=%v err=%v", got, want, err)
		}
	}
	profile(f.operator, true)
	profile(f.admin, false)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current+1)
	profile(f.operator, false)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	// Rolled-back permission mutation must not invalidate an existing report.
	before := first.Report.Epoch
	tx, e = f.conn.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, "UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_asset_assessment'", narrowResourceRole); e != nil {
		tx.Rollback(f.ctx)
		t.Fatal(e)
	}
	var during int64
	if e = tx.QueryRow(f.ctx, "SELECT authorization_version FROM adtr.users WHERE id=$1", f.operator).Scan(&during); e != nil || during <= before {
		tx.Rollback(f.ctx)
		t.Fatal("grant epoch did not advance", e)
	}
	if e = tx.Rollback(f.ctx); e != nil {
		t.Fatal(e)
	}
	var after int64
	if e = f.conn.QueryRow(f.ctx, "SELECT authorization_version FROM adtr.users WHERE id=$1", f.operator).Scan(&after); e != nil || after != before {
		t.Fatal("epoch rollback", e)
	}
	call(author, "/detail?domainId=domain-a&id=report-1", nil, 200)
	// An audit storage failure must roll back both persisted report and receipt.
	f.exec(`CREATE FUNCTION adtr.reject_asset_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='simulation_asset_assessment_create' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_asset_test_audit BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_asset_test_audit();`)
	input["id"] = "rollback-report"
	input["operationId"] = "rollback-op"
	call(author, "/create", input, 503)
	call(author, "/detail?domainId=domain-a&id=rollback-report", nil, 404)
	call(author, "/receipt?domainId=domain-a&operationId=rollback-op", nil, 404)
	f.exec("DROP TRIGGER reject_asset_test_audit ON adtr.auth_audit")
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_asset_assessment'", narrowResourceRole)
	call(author, obsPath, nil, 403)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_asset_assessment'", narrowResourceRole)
	call(author, obsPath, nil, 200)
	call(author, "/detail?domainId=domain-a&id=report-1", nil, 404)
	call(author, "/receipt?domainId=domain-a&operationId=op-1", nil, 404)
	var history assessment.HistoryPage
	if e = json.Unmarshal(call(author, "/history?domainId=domain-a&offset=0&limit=20", nil, 200).Body.Bytes(), &history); e != nil || history.Total != 0 {
		t.Fatal("old epoch history", e)
	}
	// The next current-epoch report remains usable; this is not a permanent denial.
	input["id"] = "report-new"
	input["operationId"] = "op-new"
	call(author, "/create", input, 200)
}
