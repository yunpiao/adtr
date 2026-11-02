//go:build integration

package auth

import (
	"bytes"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

// Installs the complete synthetic schema 24–27 chain in an isolated database,
// never existing grant data. Legacy endpoints must continue working after upgrade.
func TestSimulationRiskHTTPAuthorizationRecoveryAndAuditAtomicity(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + simulationdetectionschedules.SchemaSQL + simulationdetectionschedules.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	var guards int
	if err := f.conn.QueryRow(f.ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid='adtr.access_permissions'::regclass AND tgname IN ('task_auth_permissions','simulation_event_grant_epoch','simulation_risk_grant_epoch','simulation_detection_grant_epoch') AND tgenabled='O'`).Scan(&guards); err != nil || guards != 4 {
		t.Fatal("permission guards replaced", guards, err)
	}
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_risk',true,true)", narrowResourceRole)
	f.seedGroup("one", "simulation-group", narrowResourceRole, "domain-a")
	scope := securityevents.Scope{Tenant: "one", Domain: "domain-a", Read: true, Manage: true}
	provenance := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "http-synthetic-only", Synthetic: true}
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = simulationevents.SeedFixtureTx(f.ctx, tx, scope, simulationevents.Fixture{ID: "fixture-a", Provenance: provenance, Events: []securityevents.RawEvent{{ID: "event-a", Tenant: "one", Domain: "domain-a", Kind: "login_failure", Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: provenance}}, Cases: []simulationevents.FixtureCase{{ID: "case-a", EvidenceIDs: []string{"event-a"}}}})
	if err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	// The same immutable ledger triggers as migration 24 remain enabled.
	// A rejected statement ends only its implicit transaction; no trigger is disabled.
	if _, err = f.conn.Exec(f.ctx, "UPDATE adtr.simulation_event_datasets SET fixture=fixture WHERE dataset_id='fixture-a'"); err == nil {
		t.Fatal("fixture mutation accepted despite immutable schema")
	}
	operator := f.client(f.operator)
	request := func(c *resourceTestClient, path string, body any) *http.Request {
		method := "GET"
		var raw []byte
		if body != nil {
			method = "POST"
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, simulationRiskPath+path, bytes.NewReader(raw))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		return r
	}
	call := func(c *resourceTestClient, path string, body any, want int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		f.s.SimulationRiskHandler(true).ServeHTTP(w, request(c, path, body))
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", path, w.Code, want, w.Body)
		}
		if want == 200 && w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(f.operator, 10) {
			t.Fatal("actor header", w.Header())
		}
		return w.Body.Bytes()
	}
	profileHint := func(c *resourceTestClient, want bool) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/auth/me", nil)
		if c != nil {
			req.AddCookie(c.cookie)
		}
		w := httptest.NewRecorder()
		f.s.ServeHTTP(w, req)
		var profile User
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &profile) != nil || profile.SimulationRiskReadable == nil || *profile.SimulationRiskReadable != want {
			t.Fatalf("profile hint: got %d %s want %v", w.Code, w.Body, want)
		}
	}
	// Fresh login returns the hint without requiring a second navigation fetch.
	code, err := totp(resourceTestSecret, f.now.Unix()/30)
	if err != nil {
		t.Fatal(err)
	}
	loginRaw, _ := json.Marshal(map[string]string{"username": "operator", "password": resourceTestPassword, "totpCode": code})
	loginRequest := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginRaw))
	loginRequest.Header.Set("Origin", f.s.origin)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	f.s.ServeHTTP(loginResponse, loginRequest)
	var loginProfile User
	if loginResponse.Code != 200 || json.Unmarshal(loginResponse.Body.Bytes(), &loginProfile) != nil || loginProfile.SimulationRiskReadable == nil || !*loginProfile.SimulationRiskReadable {
		t.Fatalf("fresh login hint: %d %s", loginResponse.Code, loginResponse.Body)
	}
	profileHint(operator, true)
	profileHint(f.client(f.admin), false)
	profileHint(f.client(f.viewer), false)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_risk'", narrowResourceRole)
	profileHint(operator, false)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_risk'", narrowResourceRole)
	profileHint(operator, true)
	list := "/datasets?domainId=domain-a&offset=0&limit=10"
	call(nil, list, nil, 401)
	call(f.client(f.admin), list, nil, 403)
	call(f.client(f.viewer), list, nil, 403)
	call(operator, list, nil, 200)
	call(operator, "/datasets?domainId=domain-b&offset=0&limit=10", nil, 404)
	body := map[string]any{"domainId": "domain-a", "datasetId": "fixture-a", "subject": "synthetic-user", "operationId": "operation-a", "reason": "synthetic review", "expectedRevision": 1}
	r := request(operator, "/reset", body)
	r.Header.Del("X-CSRF-Token")
	w := httptest.NewRecorder()
	f.s.SimulationRiskHandler(true).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	// Success is committed even when response bytes are lost. Recovery is GET,
	// and retrying the same operation returns the same receipt without new audit.
	lost := &simulationLostResponse{header: http.Header{}}
	f.s.SimulationRiskHandler(true).ServeHTTP(lost, request(operator, "/reset", body))
	if lost.status != 200 {
		t.Fatal("lost response status", lost.status)
	}
	operation := "/operation?domainId=domain-a&datasetId=fixture-a&operationId=operation-a"
	first := call(operator, operation, nil, 200)
	retry := call(operator, "/reset", body, 200)
	if !bytes.Equal(first, retry) {
		t.Fatal("recovery receipt changed")
	}
	for _, sql := range []string{"UPDATE adtr.simulation_risk_resets SET reason=reason", "DELETE FROM adtr.simulation_risk_receipts", "TRUNCATE adtr.simulation_risk_receipts"} {
		if _, err := f.conn.Exec(f.ctx, sql); err == nil {
			t.Fatal("immutable risk ledger accepted", sql)
		}
	}
	var count int
	if err = f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.auth_audit WHERE action='simulation_risk_reset'").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate/missing audit", count, err)
	}
	body["reason"] = "changed payload"
	call(operator, "/reset", body, 409)
	// Current read/write permission and domain membership govern recovery too.
	var beforeEpoch, afterEpoch int64
	if err := f.conn.QueryRow(f.ctx, "SELECT authorization_version FROM adtr.users WHERE id=$1", f.operator).Scan(&beforeEpoch); err != nil {
		t.Fatal(err)
	}
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_risk'", narrowResourceRole)
	call(operator, "/reset", body, 403)
	if err := f.conn.QueryRow(f.ctx, "SELECT authorization_version FROM adtr.users WHERE id=$1", f.operator).Scan(&afterEpoch); err != nil || afterEpoch <= beforeEpoch {
		t.Fatal("risk revocation did not bump epoch", beforeEpoch, afterEpoch, err)
	}
	call(operator, operation, nil, 200)
	f.exec("UPDATE adtr.access_permissions SET readable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_risk'", narrowResourceRole)
	call(operator, operation, nil, 403)
	f.exec("UPDATE adtr.access_permissions SET readable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_risk'", narrowResourceRole)
	f.exec("DELETE FROM adtr.resource_role_groups WHERE tenant_id='one' AND group_id='simulation-group'")
	call(operator, operation, nil, 404)
	f.exec("INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('one','simulation-group',$1)", narrowResourceRole)
	f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_risk'", narrowResourceRole)
	// Application-audit failure must roll back reset history AND receipt.
	// Add a failing INSERT trigger in this owned database; all production
	// immutability/revocation triggers remain enabled and unchanged.
	f.exec(`CREATE FUNCTION adtr.fail_simulation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='simulation_risk_reset' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER simulation_audit_failure BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.fail_simulation_audit()`)
	body["operationId"] = "operation-b"

	body["expectedRevision"] = 2
	call(operator, "/reset", body, 503)
	call(operator, "/operation?domainId=domain-a&datasetId=fixture-a&operationId=operation-b", nil, 404)
	var detail simulationrisk.Result
	if err = json.Unmarshal(call(operator, "/query", map[string]any{"domainId": "domain-a", "datasetId": "fixture-a", "subject": "synthetic-user"}, 200), &detail); err != nil || detail.Revision != 2 {
		t.Fatal("partial write", detail, err)
	}
	f.exec("UPDATE adtr.schema_version SET version=24")
	call(operator, list, nil, 503)
}
