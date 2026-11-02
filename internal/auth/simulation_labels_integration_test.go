//go:build integration

package auth

import (
	"bytes"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/groupgraph"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationdnsip"
	"github.com/yunpiao/adtr/internal/simulationevents"
	labels "github.com/yunpiao/adtr/internal/simulationlabels"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"net/http/httptest"
	"strconv"
	"testing"
)

// This test requires actual isolated PostgreSQL; the shared fixture fails, never
// skips, when ADTR_TEST_DATABASE_URL is absent.
func TestSimulationLabelsHTTPAuthorityCASAndReceipts(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + simulationdetectionschedules.SchemaSQL + simulationdetectionschedules.ImmutableSchemaSQL)
	f.exec(SimulationLabelsPermissionSchema + labels.SchemaSQL + labels.ImmutableSchemaSQL)
	f.exec(SimulationRuleCatalogPermissionSchema + simulationrulecatalog.SchemaSQL + simulationrulecatalog.ImmutableSchemaSQL)
	f.exec(SimulationDetectionExceptionsPermissionSchema + simulationdetectionexceptions.SchemaSQL + simulationdetectionexceptions.ImmutableSchemaSQL)
	f.exec(SimulationDNSIPPermissionSchema + simulationdnsip.SchemaSQL + simulationdnsip.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.seedGroup("one", "labels-group", narrowResourceRole, "domain-a")
	call := func(c *resourceTestClient, path string, body any, want int) map[string]any {
		t.Helper()
		method := "GET"
		var b []byte
		if body != nil {
			method = "POST"
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, simulationLabelsPath+path, bytes.NewReader(b))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		f.s.SimulationLabelsHandler(true).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got%d want%d %s", path, w.Code, want, w.Body)
		}
		if want == 200 && w.Header().Get("X-ADTR-User-ID") == "" {
			t.Fatal("missing actor binding")
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Body)
		}
		return out
	}
	list := "/datasets?domainId=domain-a&offset=0&limit=20"
	call(nil, list, nil, 401)
	call(f.client(f.admin), list, nil, 403)
	op := f.client(f.operator)
	call(op, list, nil, 403)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_labels',true,true)", narrowResourceRole)
	op = f.client(f.operator)
	call(op, list, nil, 200)
	call(op, "/datasets?domainId=domain-b&offset=0&limit=20", nil, 404)
	tx, e := f.conn.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	scope := labels.Scope{Tenant: "one", Domain: "domain-a", Read: true, Manage: true}
	obs := func(id string, seq uint64, member bool) labels.Observation {
		edges := []groupgraph.Edge{}
		if member {
			edges = append(edges, groupgraph.Edge{GroupID: "g", MemberID: "u"})
		}
		return labels.Observation{ID: id, Sequence: seq, Complete: true, Snapshot: groupgraph.Snapshot{Version: groupgraph.ContractVersion, Simulated: true, Scope: groupgraph.Scope{TenantID: "one", DomainID: "domain-a"}, ObservationID: id, Nodes: []groupgraph.Node{{ID: "g", Kind: groupgraph.Group, MembersKnown: true}, {ID: "u", Kind: groupgraph.User}}, Edges: edges}, Assets: []labels.Asset{{ObjectID: "g", Name: "Synthetic group"}, {ObjectID: "u", Name: "Synthetic user"}}}
	}
	_, e = labels.SeedFixtureTx(f.ctx, tx, scope, labels.Fixture{ID: "fixture", Version: labels.Contract, Producer: "auth-integration", Simulated: true, Observations: []labels.Observation{obs("before", 1, false), obs("after", 2, true)}})
	if e != nil {
		tx.Rollback(f.ctx)
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	body := map[string]any{"domainId": "domain-a", "datasetId": "fixture", "policyId": "policy", "expectedRevision": 0, "operationId": "create", "reason": "Synthetic policy", "spec": map[string]any{"name": "Sensitive group", "sensitiveGroupIds": []string{"g"}, "manualLabels": []any{}}}
	// A valid origin never substitutes for the current session's CSRF proof.
	badCSRF := *op
	badCSRF.csrf = "wrong"
	call(&badCSRF, "/create", body, 403)
	created := call(op, "/create", body, 200)
	replay := call(op, "/create", body, 200)
	a, _ := json.Marshal(created)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("receipt replay changed")
	}
	receipt := call(op, "/operation?domainId=domain-a&datasetId=fixture&operationId=create", nil, 200)
	if receipt["actor"] != strconv.FormatInt(f.operator, 10) {
		t.Fatal(receipt)
	}
	// A same-domain reader may see policies but cannot recover another actor's receipt.
	f.exec("UPDATE adtr.users SET role_id=$1 WHERE id=$2", narrowResourceRole, f.viewer)
	peer := f.client(f.viewer)
	call(peer, "/policy?domainId=domain-a&datasetId=fixture&policyId=policy", nil, 200)
	call(peer, "/operation?domainId=domain-a&datasetId=fixture&operationId=create", nil, 404)
	body["reason"] = "changed"
	call(op, "/create", body, 409)
	evaluate := func(observation, operation string) string {
		r := call(op, "/evaluate", map[string]any{"domainId": "domain-a", "datasetId": "fixture", "policyId": "policy", "expectedRevision": 1, "observationId": observation, "operationId": operation, "reason": "Compare synthetic observations"}, 200)
		return r["evaluation"].(map[string]any)["id"].(string)
	}
	before := evaluate("before", "eval-before")
	after := evaluate("after", "eval-after")
	comparison := call(op, "/compare", map[string]any{"domainId": "domain-a", "datasetId": "fixture", "beforeId": before, "afterId": after, "operationId": "compare", "reason": "Synthetic change"}, 200)
	if len(comparison["comparison"].(map[string]any)["alerts"].([]any)) != 1 {
		t.Fatal(comparison)
	}
	asset := call(op, "/asset?domainId=domain-a&datasetId=fixture&evaluationId="+after+"&objectId=u", nil, 200)
	if asset["isHoneypot"] != nil || asset["honeypotAvailability"] != "unavailable" {
		t.Fatal(asset)
	}
	control := map[string]any{"domainId": "domain-a", "datasetId": "fixture", "policyId": "policy", "expectedRevision": 1, "operationId": "archive-ok", "reason": "Archive test"}
	call(op, "/archive", control, 200)
	control["operationId"] = "stale-restore"
	call(op, "/restore", control, 409)
	control["expectedRevision"] = 2
	control["operationId"] = "restore-ok"
	call(op, "/restore", control, 200)
	call(op, "/history?domainId=domain-a&datasetId=fixture&policyId=policy&offset=0&limit=20", nil, 200)
	// Domain revocation protects an already committed operation receipt too.
	f.exec("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='domain-a'")
	call(op, "/operation?domainId=domain-a&datasetId=fixture&operationId=create", nil, 404)
	f.exec("UPDATE adtr.resource_domains SET active=true WHERE tenant_id='one' AND id='domain-a'")
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_labels'", narrowResourceRole)
	call(op, "/operation?domainId=domain-a&datasetId=fixture&operationId=create", nil, 200)
	call(op, "/archive", map[string]any{"domainId": "domain-a", "datasetId": "fixture", "policyId": "policy", "expectedRevision": 1, "operationId": "archive", "reason": "Archive synthetic policy"}, 403)
	f.exec("UPDATE adtr.access_permissions SET readable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_labels'", narrowResourceRole)
	call(op, "/operation?domainId=domain-a&datasetId=fixture&operationId=create", nil, 403)
}
