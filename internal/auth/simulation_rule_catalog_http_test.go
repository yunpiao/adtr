package auth

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSimulationRuleCatalogAuthorityMatrix(t *testing.T) {
	for _, role := range []string{"platform_admin", "viewer"} {
		g, e := loadAccessGrants(context.Background(), nil, "test", role)
		if e != nil {
			t.Fatal(e)
		}
		for path, rt := range simulationRuleCatalogRoutes {
			if simulationRuleCatalogPathAllowed(rt.method, simulationRuleCatalogPath+path, g) {
				t.Fatal("builtin bypass", role, path)
			}
		}
	}
	for mask := 0; mask < 64; mask++ {
		g := map[string]AccessAuth{"simulation_rule_catalog": {mask&1 != 0, mask&2 != 0}, "simulation_rule_catalog_publish": {mask&4 != 0, mask&8 != 0}, "simulation_detection": {mask&16 != 0, mask&32 != 0}}
		for path, rt := range simulationRuleCatalogRoutes {
			want := mask&1 != 0
			if path == "/publish" {
				want = want && mask&4 != 0 && mask&8 != 0
			} else if rt.write {
				want = want && mask&2 != 0
			}
			if path == "/create-template" {
				want = want && mask&16 != 0 && mask&32 != 0
			}
			if got := simulationRuleCatalogPathAllowed(rt.method, simulationRuleCatalogPath+path, g); got != want {
				t.Fatalf("mask%d path%s got%v", mask, path, got)
			}
			for _, bad := range []string{path + "/", path + "/extra"} {
				if simulationRuleCatalogPathAllowed(rt.method, simulationRuleCatalogPath+bad, g) {
					t.Fatal("open route")
				}
			}
			if simulationRuleCatalogPathAllowed("DELETE", simulationRuleCatalogPath+path, g) {
				t.Fatal("wrong method")
			}
		}
	}
}
func TestSimulationRuleCatalogClosedInputs(t *testing.T) {
	valid := `{"domainId":"d","id":"r","expectedRevision":0,"operationId":"op","declaration":{"combine":"all","conditions":[{"field":"event.code","operator":"eq","values":["4625"]}]}}`
	decode := func(path, body string) error {
		r := httptest.NewRequest("POST", simulationRuleCatalogPath+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, e := decodeSimulationRuleCatalogInput(httptest.NewRecorder(), r, path)
		return e
	}
	if e := decode("/create", valid); e != nil {
		t.Fatal(e)
	}
	bad := []string{valid + `{}`, `[]`, strings.Replace(valid, `"domainId":"d"`, `"domainId":"d","domainId":"x"`, 1), strings.Replace(valid, `"conditions":[`, `"conditions":null,"unexpected":[`, 1), strings.Replace(valid, `"values":["4625"]`, `"values":null`, 1), strings.Replace(valid, `"combine":"all"`, `"combine":"all","combine":"any"`, 1), strings.Replace(valid, `"expectedRevision":0`, `"expectedRevision":1`, 1)}
	for _, key := range []string{"actorId", "tenant", "reviewer", "passed", "hash", "snapshot", "history", "reason"} {
		bad = append(bad, strings.TrimSuffix(valid, "}")+`,"`+key+`":"forged"}`)
	}
	for _, b := range bad {
		if decode("/create", b) == nil {
			t.Fatal("accepted", b)
		}
	}
	test := `{"domainId":"d","id":"r","expectedRevision":2,"operationId":"test","cases":[{"id":"positive","fields":{"event.code":"4625"},"wantMatch":true},{"id":"negative","fields":{"event.code":"4624"},"wantMatch":false}]}`
	if e := decode("/test", test); e != nil {
		t.Fatal(e)
	}
	for _, b := range []string{strings.Replace(test, `"wantMatch":true`, `"wantMatch":null`, 1), strings.Replace(test, `"event.code":"4625"`, `"event.code":null`, 1), strings.Replace(test, `"event.code":"4625"`, `"event.code":"4625","event.code":"4624"`, 1), strings.Replace(test, `"fields":{"event.code":"4625"}`, `"fields":null`, 1), strings.Replace(test, `"wantMatch":true`, `"wantMatch":true,"passed":true`, 1)} {
		if decode("/test", b) == nil {
			t.Fatal("accepted fixture", b)
		}
	}
	for _, q := range []string{"domainId=d&query=&offset=0&limit=1&actor=x", "domainId=d&query=&offset=00&limit=1", "domainId=d&query=&offset=0&limit=0", "domainId=d&query=&offset=0&limit=1&domainId=x", "domainId=%ff&query=&offset=0&limit=1"} {
		r := httptest.NewRequest("GET", simulationRuleCatalogPath+"/list?"+q, nil)
		if _, e := decodeSimulationRuleCatalogInput(httptest.NewRecorder(), r, "/list"); e == nil {
			t.Fatal("accepted", q)
		}
	}
}
func TestSimulationRuleCatalogFailClosed(t *testing.T) {
	s := &Service{origin: "https://test.invalid"}
	for _, tc := range []struct {
		enabled, detection         bool
		method, path, body, origin string
		want                       int
	}{{false, true, "GET", "/list?domainId=d&query=&offset=0&limit=1", "", "", 404}, {true, false, "POST", "/create-template", "{}", "https://test.invalid", 404}, {true, true, "DELETE", "/list", "", "", 405}, {true, true, "POST", "/publish", "{}", "", 403}, {true, true, "GET", "/list?domainId=d&query=&offset=0&limit=1", "", "", 503}, {true, true, "GET", "/unknown", "", "", 404}} {
		r := httptest.NewRequest(tc.method, simulationRuleCatalogPath+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.SimulationRuleCatalogHandler(tc.enabled, tc.detection).ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s got%d want%d", tc.path, w.Code, tc.want)
		}
		var body map[string]any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal("nonJSON")
		}
	}
}

func TestSimulationRuleCatalogPublicationPermissionRepresentation(t *testing.T) {
	grants := map[string]AccessAuth{"simulation_rule_catalog": {Readable: true}, "simulation_rule_catalog_publish": {Readable: true, Writeable: true}}
	found := 0
	for _, node := range permissionNodes(grants, true) {
		if node.Mark == "simulation_rule_catalog_publish" {
			found++
			if len(node.Paths) != 1 || node.Paths[0].URL != "POST "+simulationRuleCatalogPath+"/publish" || node.Paths[0].Auth != "writeable" {
				t.Fatal("publication capability lost", node)
			}
		}
		if node.Mark == "simulation_rule_catalog" {
			found++
			for _, path := range node.Paths {
				if path.URL == "POST "+simulationRuleCatalogPath+"/publish" {
					t.Fatal("publication incorrectly coupled to draft management")
				}
			}
		}
	}
	if found != 2 {
		t.Fatal("missing independent permission nodes", found)
	}
}

func TestSimulationRuleCatalogReceiptScopeRequiresLiveDetectionFlagAndRead(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, read := range []bool{false, true} {
			for _, write := range []bool{false, true} {
				g := map[string]AccessAuth{"simulation_rule_catalog": {Readable: true}, "simulation_detection": {Readable: read, Writeable: write}}
				scope := simulationRuleCatalogScope(User{ID: 17, tenant: "one"}, "domain-a", g, enabled)
				if scope.DetectionRead != (read && enabled) || scope.DetectionManage != (write && enabled) || !scope.Read || scope.Tenant != "one" || scope.Domain != "domain-a" || scope.ActorID != 17 {
					t.Fatal("incorrect live scope", scope)
				}
				if !simulationRuleCatalogPathAllowed("GET", simulationRuleCatalogPath+"/receipt", g) {
					t.Fatal("ordinary catalog receipt blanket denied")
				}
			}
		}
	}
	s := &Service{origin: "https://test.invalid"}
	r := httptest.NewRequest("GET", simulationRuleCatalogPath+"/receipt?domainId=d&operationId=op", nil)
	w := httptest.NewRecorder()
	s.SimulationRuleCatalogHandler(true, false).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("receipt route must proceed to authenticated receipt-specific gate, got %d", w.Code)
	}
}
