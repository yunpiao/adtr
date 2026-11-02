package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

func TestSimulationDetectionExplicitGrantAndClosedRoutes(t *testing.T) {
	for _, role := range []string{"platform_admin", "viewer"} {
		grants, err := loadAccessGrants(context.Background(), nil, "test", role)
		if err != nil || grants["simulation_detection"].Readable || grants["simulation_detection"].Writeable {
			t.Fatalf("builtin inherited simulation: %s %v", role, err)
		}
		for path, route := range simulationDetectionRoutes {
			method := route.method
			if simulationDetectionPathAllowed(method, simulationDetectionPath+path, grants) {
				t.Fatal("builtin allowed", role, path)
			}
		}
	}
	if _, err := validateGrants([]AccessGrant{{Mark: "simulation_detection", Auth: AccessAuth{true, true}}}); err != nil {
		t.Fatal(err)
	}
	for _, only := range []bool{false, true} {
		for _, n := range permissionNodes(nil, only) {
			if n.Mark == "simulation_detection" {
				t.Fatal("ungranted capability visible")
			}
		}
	}
	read := map[string]AccessAuth{"simulation_detection": {Readable: true}}
	write := map[string]AccessAuth{"simulation_detection": {Readable: true, Writeable: true}}
	for path, route := range simulationDetectionRoutes {
		method := route.method
		full := simulationDetectionPath + path
		if !simulationDetectionPathAllowed(method, full, write) || simulationDetectionPathAllowed(method, full+"/", write) || simulationDetectionPathAllowed("DELETE", full, write) {
			t.Fatal("route boundary", path)
		}
		if simulationDetectionPathAllowed(method, full, read) != !simulationDetectionRoutes[path].write {
			t.Fatal("read/write separation", path)
		}
		if simulationDetectionPathAllowed(method, full, map[string]AccessAuth{"simulation_detection": {Writeable: true}}) {
			t.Fatal("write without read")
		}
	}
	found := false
	for _, n := range permissionNodes(read, true) {
		if n.Mark == "simulation_detection" {
			found = true
			if len(n.Paths) != len(simulationDetectionRoutes) {
				t.Fatal("incomplete routes")
			}
		}
	}
	if !found {
		t.Fatal("granted capability hidden")
	}
}

func TestSimulationDetectionClosedInputs(t *testing.T) {
	valid := `{"domainId":"d","templateId":"t","name":"Synthetic rule","operationId":"op","rules":[{"id":"r","enabled":true,"combine":"all","conditions":[{"field":"event.code","operator":"eq","values":["4625"]}]}]}`
	cases := []string{valid + `{}`, `[]`, strings.Replace(valid, `"enabled":true`, `"enabled":true,"scope":{"tenant":"other"}`, 1), strings.Replace(valid, `"enabled":true`, `"enabled":null`, 1), strings.Replace(valid, `"domainId":"d"`, `"domainId":"d","domainId":"x"`, 1)}
	for _, key := range []string{"actor", "tenant", "at", "version", "hash", "scope", "manage", "ExpectedVersion"} {
		cases = append(cases, strings.TrimSuffix(valid, "}")+`,"`+key+`":"forged"}`)
	}
	decode := func(body string) error {
		r := httptest.NewRequest("POST", simulationDetectionPath+"/create", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeSimulationDetectionInput(httptest.NewRecorder(), r, "/create")
		return err
	}
	if err := decode(valid); err != nil {
		t.Fatal(err)
	}
	for _, body := range cases {
		if decode(body) == nil {
			t.Fatal("accepted", body)
		}
	}
	for _, q := range []string{"domainId=d&domainId=x", "domainId=d&actor=x", "domainId=%ff", "domainId=d&offset=0", "domainId="} {
		r := httptest.NewRequest("GET", simulationDetectionPath+"/templates?"+q, nil)
		if _, err := decodeSimulationDetectionInput(httptest.NewRecorder(), r, "/templates"); err == nil {
			t.Fatal("accepted query", q)
		}
	}
}
func TestSimulationDetectionFailClosed(t *testing.T) {
	s := &Service{origin: "https://test.invalid"}
	for _, tc := range []struct {
		enabled                    bool
		method, path, body, origin string
		want                       int
	}{
		{false, "GET", "/templates?domainId=d", "", "", 404},
		{true, "GET", "/unknown", "", "", 404},
		{true, "DELETE", "/templates", "", "", 405},
		{true, "POST", "/archive", `{}`, "", 403},
		{true, "POST", "/archive", `{}`, s.origin, 400},
		{true, "GET", "/templates?domainId=d", "", "", 503},
		{true, "GET", "/templates?domainId=d", "body", "", 400},
	} {
		r := httptest.NewRequest(tc.method, simulationDetectionPath+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.SimulationDetectionHandler(tc.enabled, nil).ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc, w.Code, w.Body)
		}
	}
	raw, _ := json.Marshal(User{})
	if strings.Contains(string(raw), "simulationDetectionReadable") {
		t.Fatal("unpopulated hint")
	}
	if ok, err := simulationDetectionProfileReadable(context.Background(), nil, User{NeedChange: true}); err != nil || ok {
		t.Fatal("forced password hint")
	}
	auth := simulationDetectionTaskAuthorizer(time.Now)
	for _, scope := range []tasks.Scope{{TaskName: "simulation.detection", DomainID: "d"}, {TaskName: "other", DomainID: "d"}, {TaskName: "simulation.detection", DomainID: "platform", Platform: true}} {
		if _, err := auth(context.Background(), nil, tasks.Principal{TenantID: "t", ActorID: 1}, scope, tasks.Execute); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal(err)
		}
	}
}
func TestSimulationDetectionPermissionMigrationPreservesBoundaries(t *testing.T) {
	sql := SimulationDetectionPermissionSchema
	// Migration 26 is immutable history: migration 27 introduces scheduling.
	for _, mark := range accessMarks {
		if mark == "simulation_detection_schedules" || mark == "simulation_labels" || mark == "simulation_rule_catalog" || mark == "simulation_rule_catalog_publish" {
			if strings.Contains(sql, "'"+mark+"'") {
				t.Fatal("historical migration gained future capability")
			}
			if !strings.Contains(SimulationRuleCatalogPermissionSchema, "'"+mark+"'") {
				t.Fatal("migration29 missing current mark")
			}
			continue
		}
		if !strings.Contains(sql, "'"+mark+"'") {
			t.Fatal("missing mark", mark)
		}
	}
	for _, required := range []string{"CREATE TRIGGER simulation_detection_grant_epoch", "adtr.task_auth_lock", "adtr.task_auth_bump"} {
		if !strings.Contains(sql, required) {
			t.Fatal(required)
		}
	}
	for _, bad := range []string{"INSERT INTO", "DELETE FROM", "DROP TRIGGER", "DROP FUNCTION", "ALTER FUNCTION"} {
		if strings.Contains(sql, bad) {
			t.Fatal(bad)
		}
	}
}
