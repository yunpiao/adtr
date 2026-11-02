//go:build integration

package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

type simulationLostResponse struct {
	header http.Header
	status int
}

func (w *simulationLostResponse) Header() http.Header       { return w.header }
func (w *simulationLostResponse) WriteHeader(n int)         { w.status = n }
func (w *simulationLostResponse) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// Uses newResourceFixture's freshly created database, never existing grant data.
func TestSimulationEventHTTPAuthorizationRecoveryAndAuditAtomicity(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_events',true,true)", narrowResourceRole)
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
		r := httptest.NewRequest(method, simulationEventPath+path, bytes.NewReader(raw))
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
		f.s.SimulationEventsHandler(true).ServeHTTP(w, request(c, path, body))
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", path, w.Code, want, w.Body)
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
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &profile) != nil || profile.SimulationEventsReadable == nil || *profile.SimulationEventsReadable != want {
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
	if loginResponse.Code != 200 || json.Unmarshal(loginResponse.Body.Bytes(), &loginProfile) != nil || loginProfile.SimulationEventsReadable == nil || !*loginProfile.SimulationEventsReadable {
		t.Fatalf("fresh login hint: %d %s", loginResponse.Code, loginResponse.Body)
	}
	profileHint(operator, true)
	profileHint(f.client(f.admin), false)
	profileHint(f.client(f.viewer), false)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_events'", narrowResourceRole)
	profileHint(operator, false)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_events'", narrowResourceRole)
	profileHint(operator, true)
	list := "/datasets?domainId=domain-a&offset=0&limit=10"
	call(nil, list, nil, 401)
	call(f.client(f.admin), list, nil, 403)
	call(f.client(f.viewer), list, nil, 403)
	call(operator, list, nil, 200)
	call(operator, "/datasets?domainId=domain-b&offset=0&limit=10", nil, 404)
	body := map[string]any{"domainId": "domain-a", "datasetId": "fixture-a", "caseId": "case-a", "operationId": "operation-a", "action": "mark_suspicious", "reason": "synthetic review", "expectedRevision": 1}
	r := request(operator, "/transition", body)
	r.Header.Del("X-CSRF-Token")
	w := httptest.NewRecorder()
	f.s.SimulationEventsHandler(true).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	// Success is committed even when response bytes are lost. Recovery is GET,
	// and retrying the same operation returns the same receipt without new audit.
	lost := &simulationLostResponse{header: http.Header{}}
	f.s.SimulationEventsHandler(true).ServeHTTP(lost, request(operator, "/transition", body))
	if lost.status != 200 {
		t.Fatal("lost response status", lost.status)
	}
	operation := "/operation?domainId=domain-a&datasetId=fixture-a&operationId=operation-a"
	first := call(operator, operation, nil, 200)
	retry := call(operator, "/transition", body, 200)
	if !bytes.Equal(first, retry) {
		t.Fatal("recovery receipt changed")
	}
	var count int
	if err = f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.auth_audit WHERE action='simulation_event_transition'").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate/missing audit", count, err)
	}
	body["reason"] = "changed payload"
	call(operator, "/transition", body, 409)
	// Current read/write permission and domain membership govern recovery too.
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_events'", narrowResourceRole)
	call(operator, "/transition", body, 403)
	call(operator, operation, nil, 200)
	f.exec("DELETE FROM adtr.resource_role_groups WHERE tenant_id='one' AND group_id='simulation-group'")
	call(operator, operation, nil, 404)
	f.exec("INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('one','simulation-group',$1)", narrowResourceRole)
	f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_events'", narrowResourceRole)
	// Application-audit failure must roll back case, local history AND receipt.
	// Add a failing INSERT trigger in this owned database; all production
	// immutability/revocation triggers remain enabled and unchanged.
	f.exec(`CREATE FUNCTION adtr.fail_simulation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='simulation_event_transition' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER simulation_audit_failure BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.fail_simulation_audit()`)
	body["operationId"] = "operation-b"
	body["action"] = "close"
	body["expectedRevision"] = 2
	call(operator, "/transition", body, 503)
	call(operator, "/operation?domainId=domain-a&datasetId=fixture-a&operationId=operation-b", nil, 404)
	var detail simulationevents.Detail
	if err = json.Unmarshal(call(operator, "/detail?domainId=domain-a&datasetId=fixture-a&caseId=case-a&historyOffset=0&historyLimit=10", nil, 200), &detail); err != nil || detail.Case.Revision != 2 || detail.Case.State != "open" || detail.HistoryTotal != 1 {
		t.Fatal("partial write", detail, err)
	}
	f.exec("UPDATE adtr.schema_version SET version=23")
	call(operator, list, nil, 503)
}
