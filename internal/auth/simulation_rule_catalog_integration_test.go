//go:build integration

package auth

import (
	"bytes"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationassetassessment"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationdnsip"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationlabels"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	catalog "github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Real isolated PostgreSQL is mandatory. The fixture fails, never skips, when
// ADTR_TEST_DATABASE_URL is missing. No preexisting user grants are modified.
func TestSimulationRuleCatalogHTTPIdentityReviewBridgeAndRevocation(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + simulationdetectionschedules.SchemaSQL + simulationdetectionschedules.ImmutableSchemaSQL)
	f.exec(SimulationLabelsPermissionSchema + simulationlabels.SchemaSQL + simulationlabels.ImmutableSchemaSQL)
	f.exec(SimulationRuleCatalogPermissionSchema + catalog.SchemaSQL + catalog.ImmutableSchemaSQL)
	f.exec(SimulationDetectionExceptionsPermissionSchema + simulationdetectionexceptions.SchemaSQL + simulationdetectionexceptions.ImmutableSchemaSQL)
	f.exec(SimulationDNSIPPermissionSchema + simulationdnsip.SchemaSQL + simulationdnsip.ImmutableSchemaSQL)
	f.exec(SimulationAssetAssessmentPermissionSchema + simulationassetassessment.SchemaSQL + simulationassetassessment.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.seedGroup("one", "catalog-writers", narrowResourceRole, "domain-a")
	f.seedGroup("one", "catalog-reviewers", widerResourceRole, "domain-a")
	f.exec("UPDATE adtr.users SET role_id=$1 WHERE id=$2", widerResourceRole, f.viewer)
	detectionEnabled := true
	call := func(c *resourceTestClient, path string, body any, want int) map[string]any {
		t.Helper()
		method := "GET"
		var b []byte
		if body != nil {
			method = "POST"
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, simulationRuleCatalogPath+path, bytes.NewReader(b))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		f.s.SimulationRuleCatalogHandler(true, detectionEnabled).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", path, w.Code, want, w.Body)
		}
		if want == 200 && w.Header().Get("X-ADTR-User-ID") == "" {
			t.Fatal("missing actor binding")
		}
		if want == 200 && path == "/export?domainId=domain-a&id=rule" && w.Header().Get("Content-Disposition") == "" {
			t.Fatal("not attachment")
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("invalid response")
		}
		return out
	}
	list := "/list?domainId=domain-a&query=&offset=0&limit=20"
	call(nil, list, nil, 401)
	call(f.client(f.admin), list, nil, 403)
	call(f.client(f.operator), list, nil, 403)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_rule_catalog',true,true),('one',$1,'simulation_rule_catalog_publish',true,true),('one',$2,'simulation_rule_catalog',true,false),('one',$2,'simulation_rule_catalog_publish',true,true)", narrowResourceRole, widerResourceRole)
	author, reviewer := f.client(f.operator), f.client(f.viewer)
	profileHint := func(c *resourceTestClient, wantRead, wantPublish bool) {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/auth/me", nil)
		r.AddCookie(c.cookie)
		w := httptest.NewRecorder()
		f.s.ServeHTTP(w, r)
		var u User
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &u) != nil || u.SimulationRuleCatalogReadable == nil || u.SimulationRuleCatalogPublishable == nil || *u.SimulationRuleCatalogReadable != wantRead || *u.SimulationRuleCatalogPublishable != wantPublish {
			t.Fatalf("profile hints %d %s", w.Code, w.Body)
		}
	}
	profileHint(reviewer, true, true)
	profileHint(f.client(f.admin), false, false)
	code, e := totp(resourceTestSecret, f.now.Unix()/30)
	if e != nil {
		t.Fatal(e)
	}
	loginRaw, _ := json.Marshal(map[string]string{"username": "viewer", "password": resourceTestPassword, "totpCode": code})
	login := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginRaw))
	login.Header.Set("Origin", f.s.origin)
	login.Header.Set("Content-Type", "application/json")
	lw := httptest.NewRecorder()
	f.s.ServeHTTP(lw, login)
	var lu User
	if lw.Code != 200 || json.Unmarshal(lw.Body.Bytes(), &lu) != nil || lu.SimulationRuleCatalogReadable == nil || lu.SimulationRuleCatalogPublishable == nil || !*lu.SimulationRuleCatalogReadable || !*lu.SimulationRuleCatalogPublishable {
		t.Fatalf("login reviewer hint %d %s", lw.Code, lw.Body)
	}

	call(author, list, nil, 200)
	call(reviewer, list, nil, 200)
	call(author, "/list?domainId=domain-b&query=&offset=0&limit=20", nil, 404)
	call(f.client(f.other), list, nil, 403)
	create := map[string]any{"domainId": "domain-a", "id": "rule", "expectedRevision": 0, "operationId": "create", "declaration": map[string]any{"combine": "all", "conditions": []any{map[string]any{"field": "event.outcome", "operator": "eq", "values": []string{"failure"}}}}}
	bad := *author
	bad.csrf = "wrong"
	call(&bad, "/create", create, 403)
	call(reviewer, "/create", create, 403)
	made := call(author, "/create", create, 200)
	replay := call(author, "/create", create, 200)
	a, _ := json.Marshal(made)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("receipt changed")
	}
	call(reviewer, "/receipt?domainId=domain-a&operationId=create", nil, 404)
	create["declaration"].(map[string]any)["combine"] = "any"
	call(author, "/create", create, 409)
	create["declaration"].(map[string]any)["combine"] = "all"
	rev := made["rule"].(map[string]any)["revision"].(float64)
	mutate := func(op string, revision float64) map[string]any {
		return map[string]any{"domainId": "domain-a", "id": "rule", "expectedRevision": revision, "operationId": op}
	}
	test := mutate("test", rev)
	test["cases"] = []any{map[string]any{"id": "positive", "fields": map[string]string{"event.outcome": "failure"}, "wantMatch": true}, map[string]any{"id": "negative", "fields": map[string]string{"event.outcome": "success"}, "wantMatch": false}}
	tested := call(author, "/test", test, 200)
	rev = tested["rule"].(map[string]any)["revision"].(float64)
	call(author, "/publish", mutate("self-review", rev), 403)
	// This reviewer cannot edit drafts but can independently publish their passing test.
	published := call(reviewer, "/publish", mutate("publish", rev), 200)
	rev = published["rule"].(map[string]any)["revision"].(float64)
	exported := call(reviewer, "/export?domainId=domain-a&id=rule", nil, 200)
	pub := exported["publications"].([]any)[0].(map[string]any)
	if pub["reviewedBy"] != float64(f.viewer) || pub["lastEditorId"] != float64(f.operator) || pub["testedBy"] != float64(f.operator) {
		t.Fatal("forged identity", pub)
	}
	enabled := call(author, "/enable", mutate("enable", rev), 200)
	rev = enabled["rule"].(map[string]any)["revision"].(float64)
	source := pub["rule"].(map[string]any)
	rule := source["rule"].(map[string]any)
	copyBody := mutate("copy", rev)
	copyBody["publishedVersion"] = rule["version"]
	copyBody["publishedDigest"] = source["digest"]
	copyBody["templateId"] = "copied"
	copyBody["name"] = "Explicit synthetic copy"
	copyBody["acknowledgement"] = catalog.CopyAcknowledgement
	call(author, "/create-template", copyBody, 403)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_detection',true,true)", narrowResourceRole)
	author = f.client(f.operator)
	copied := call(author, "/create-template", copyBody, 200)
	if copied["origin"].(map[string]any)["publishedDigest"] != source["digest"] {
		t.Fatal("origin lost")
	}

	// A catalog read grant alone cannot recover the manual template embedded in a copy receipt.
	call(author, "/receipt?domainId=domain-a&operationId=copy", nil, 200)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection'", narrowResourceRole)
	call(author, "/receipt?domainId=domain-a&operationId=copy", nil, 403)
	call(author, "/receipt?domainId=domain-a&operationId=create", nil, 200)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection'", narrowResourceRole)
	call(author, "/receipt?domainId=domain-a&operationId=copy", nil, 200)
	newCopy := map[string]any{}
	for key, value := range copyBody {
		newCopy[key] = value
	}
	newCopy["operationId"] = "copy-after-readonly-regrant"
	newCopy["templateId"] = "copy-readonly-denied"
	call(author, "/create-template", newCopy, 403)
	detectionEnabled = false
	call(author, "/receipt?domainId=domain-a&operationId=copy", nil, 403)
	call(author, "/receipt?domainId=domain-a&operationId=create", nil, 200)
	call(author, "/create-template", copyBody, 404)
	detectionEnabled = true
	call(author, "/receipt?domainId=domain-a&operationId=copy", nil, 200)
	f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_detection'", narrowResourceRole)
	disabled := call(author, "/disable", mutate("disable", rev), 200)
	copyBody["expectedRevision"] = disabled["rule"].(map[string]any)["revision"]
	copyBody["operationId"] = "copy-disabled"
	copyBody["templateId"] = "copied-disabled"
	call(author, "/create-template", copyBody, 409)
	// Disabling the source does not cascade into independently copied templates.
	var version int64
	var archived bool
	if e := f.conn.QueryRow(f.ctx, "SELECT version,archived FROM adtr.simulation_detection_templates WHERE tenant_id='one' AND domain_id='domain-a' AND template_id='copied'").Scan(&version, &archived); e != nil || version != 1 || archived {
		t.Fatal("copy cascaded", e)
	}
	receipt := call(author, "/receipt?domainId=domain-a&operationId=create", nil, 200)
	if receipt["actorId"] != float64(f.operator) {
		t.Fatal("receipt actor", strconv.FormatInt(f.operator, 10))
	}
	f.exec("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='domain-a'")
	call(author, "/receipt?domainId=domain-a&operationId=create", nil, 404)
	f.exec("UPDATE adtr.resource_domains SET active=true WHERE tenant_id='one' AND id='domain-a'")
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_rule_catalog'", narrowResourceRole)
	call(author, list, nil, 403)
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_rule_catalog_publish'", widerResourceRole)
	call(reviewer, "/publish", mutate("revoked-review", rev), 403)
	profileHint(reviewer, true, false)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current+1)
	call(reviewer, list, nil, 503)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.exec("UPDATE adtr.users SET disabled=true WHERE id=$1", f.viewer)
	call(reviewer, list, nil, 401)
}
