//go:build integration

package auth

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdashboard"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationrisk"
)

func newSimulationDashboardFixture(t *testing.T) *taskFixture {
	t.Helper()
	f := newTaskFixture(t) // Isolated real PostgreSQL; missing DSN fails, never skips.
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	for _, mark := range []string{"simulation_events", "simulation_risk", "simulation_detection"} {
		f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,$2,true,false),('two',$3,$2,true,false)", narrowResourceRole, mark, widerResourceRole)
	}
	f.seedGroup("one", "dashboard-group", narrowResourceRole, "domain-a")
	f.seedGroup("two", "dashboard-group", widerResourceRole, "domain-a")
	f.exec("UPDATE adtr.users SET role='viewer',role_id=$1 WHERE id=$2", widerResourceRole, f.other)
	provenance := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "dashboard-http-synthetic", Synthetic: true}
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	_, err = simulationevents.SeedFixtureTx(f.ctx, tx, securityevents.Scope{Tenant: "one", Domain: "domain-a", Read: true, Manage: true}, simulationevents.Fixture{ID: "dashboard-fixture", Provenance: provenance,
		Events: []securityevents.RawEvent{{ID: "event-a", Tenant: "one", Domain: "domain-a", Kind: "login_failure", Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: f.now.Add(-time.Minute).Format(time.RFC3339), Provenance: provenance}},
		Cases:  []simulationevents.FixtureCase{{ID: "case-a", EvidenceIDs: []string{"event-a"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSimulationDashboardHTTPGrantScopeSessionAndNoMutation(t *testing.T) {
	f := newSimulationDashboardFixture(t)
	handler := f.s.SimulationDashboardHandler(true, true, true, true)
	query := simulationDashboardPath + "/summary?domainId=domain-a&datasetId=dashboard-fixture&window=24h"
	call := func(c *resourceTestClient, path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c.cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body)
		}
		if want != 200 && w.Header().Get("X-ADTR-User-ID") != "" {
			t.Fatal("error leaked actor binding")
		}
		return w
	}
	op := f.client(f.operator)
	call(nil, query, 401)
	call(f.client(f.admin), query, 403)
	call(f.client(f.viewer), query, 403)
	call(op, simulationDashboardPath+"/summary?domainId=domain-b&datasetId=dashboard-fixture&window=24h", 404)
	call(f.client(f.other), query, 404) // Same domain ID and all grants in another tenant.
	call(op, simulationDashboardPath+"/summary?domainId=domain-a&datasetId=missing&window=24h", 404)
	counts := func() [4]int64 {
		t.Helper()
		var v [4]int64
		if err := f.conn.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.tasks),(SELECT count(*) FROM adtr.auth_audit),(SELECT count(*) FROM adtr.simulation_event_transitions),(SELECT count(*) FROM adtr.simulation_risk_resets)`).Scan(&v[0], &v[1], &v[2], &v[3]); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := counts()
	w := call(op, query, 200)
	if w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(f.operator, 10) {
		t.Fatal("wrong actor binding")
	}
	var summary simulationdashboard.Summary
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.Simulation || summary.ProductAccepted || summary.Dataset.ID != "dashboard-fixture" || summary.Dataset.EventCount != 1 || summary.Cases.Total != 1 || summary.Cases.Open != 1 || summary.Cases.Closed != 0 || summary.Risk.Status != "not_selected" || summary.LatestManualDetection.Status != "no_result" || len(summary.Unavailable) == 0 {
		t.Fatalf("wrong actual summary: %s", w.Body)
	}
	call(op, query+"&subject=synthetic-user", 200)
	if after := counts(); after != before {
		t.Fatalf("GET mutated source %v -> %v", before, after)
	}
	for _, mark := range []string{"simulation_events", "simulation_risk", "simulation_detection"} {
		f.exec("UPDATE adtr.access_permissions SET readable=false WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
		call(op, query, 403)
		f.exec("UPDATE adtr.access_permissions SET readable=true WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
		call(op, query, 200)
	}
	f.exec("DELETE FROM adtr.sessions WHERE token_hash=$1", digest(op.cookie.Value))
	call(op, query, 401)
	op = f.client(f.operator)
	f.exec("UPDATE adtr.users SET must_change=true WHERE id=$1", f.operator)
	call(op, query, 403)
	f.exec("UPDATE adtr.users SET must_change=false WHERE id=$1", f.operator)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current-1)
	w = call(op, query, 503)
	if w.Body.String() != "{\"error\":\"schema_incompatible\"}\n" {
		t.Fatal(w.Body)
	}
}

func TestSimulationDashboardRechecksSessionAfterIdentityLock(t *testing.T) {
	f := newSimulationDashboardFixture(t)
	op := f.client(f.operator)
	gateConn, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer gateConn.Close(f.ctx)
	gate, err := gateConn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(f.ctx)
	if err = lockIdentityTenant(f.ctx, gate, "one"); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("GET", simulationDashboardPath+"/summary?domainId=domain-a&datasetId=dashboard-fixture&window=24h", nil)
		r.AddCookie(op.cookie)
		w := httptest.NewRecorder()
		f.s.SimulationDashboardHandler(true, true, true, true).ServeHTTP(w, r)
		done <- w
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var count int
		if err = f.conn.QueryRow(f.ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			blocked = true
			break
		}
		select {
		case w := <-done:
			t.Fatalf("bypassed lock %d", w.Code)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !blocked {
		t.Fatal("dashboard did not reach identity lock")
	}
	if _, err = gate.Exec(f.ctx, "DELETE FROM adtr.sessions WHERE token_hash=$1", digest(op.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 401 || w.Header().Get("X-ADTR-User-ID") != "" {
			t.Fatalf("stale session %d %s", w.Code, w.Body)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("dashboard request did not finish")
	}
}
