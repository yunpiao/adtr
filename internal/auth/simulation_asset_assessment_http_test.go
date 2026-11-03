package auth

import (
	"context"
	assessment "github.com/yunpiao/adtr/internal/simulationassetassessment"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSimulationAssetAssessmentRouteAuthority(t *testing.T) {
	for _, read := range []bool{false, true} {
		for _, manage := range []bool{false, true} {
			grants := map[string]AccessAuth{"simulation_asset_assessment": {Readable: read, Writeable: manage}, "tasks": {Readable: true, Writeable: true}}
			for path, rt := range simulationAssetAssessmentRoutes {
				if got := simulationAssetAssessmentPathAllowed(rt.method, simulationAssetAssessmentPath+path, grants); got != (read && (!rt.write || manage)) {
					t.Fatal(path, read, manage)
				}
				if simulationAssetAssessmentPathAllowed("PATCH", simulationAssetAssessmentPath+path, grants) {
					t.Fatal("method")
				}
			}
		}
	}
	if simulationAssetAssessmentPathAllowed("GET", simulationAssetAssessmentPath+"/history", map[string]AccessAuth{"tasks": {Readable: true, Writeable: true}}) {
		t.Fatal("unrelated permission bypass")
	}
	for _, path := range []string{"/seed", "/worker", "/evaluate", "/"} {
		if simulationAssetAssessmentPathAllowed("POST", simulationAssetAssessmentPath+path, map[string]AccessAuth{"simulation_asset_assessment": {Readable: true, Writeable: true}}) {
			t.Fatal(path)
		}
	}
}
func TestSimulationAssetAssessmentStrictInputs(t *testing.T) {
	create := `{"domainId":"domain-a","id":"report-1","operationId":"op-1","observationId":"synthetic-assets-v1","expectedSourceHash":"` + strings.Repeat("a", 64) + `","scenarioId":"baseline"}`
	rows := "/rows?domainId=domain-a&id=report-1&kind=&accountStatus=&activity=&groupType=&groupScope=&offset=0&limit=20"
	for _, tt := range []struct {
		path, body string
		good       bool
	}{
		{"/create", create, true},
		{"/observation?domainId=domain-a&observationId=synthetic-assets-v1", "", true},
		{"/detail?domainId=domain-a&id=report-1", "", true},
		{"/receipt?domainId=domain-a&operationId=op-1", "", true},
		{"/history?domainId=domain-a&offset=0&limit=20", "", true},
		{rows, "", true},
		{strings.Replace(rows, "kind=", "kind=group", 1), "", true},
		{strings.Replace(rows, "accountStatus=", "accountStatus=active0", 1), "", false},
		{strings.Replace(rows, "limit=20", "limit=21", 1), "", false},
		{strings.Replace(rows, "offset=0", "offset=00", 1), "", false},
		{strings.Replace(rows, "offset=0", "offset=65", 1), "", false},
		{rows + "&id=other", "", false},
		{strings.Replace(rows, "domain-a", "example.test", 1), "", false},
		{"/history?domainId=platform&offset=0&limit=20", "", false},
		{"/create?extra=x", create, false},
		{"/create", create + `{}`, false},
		{"/create", strings.Replace(create, `"id":"report-1"`, `"id":null`, 1), false},
		{"/create", strings.Replace(create, `"id":"report-1"`, `"id":"report-1","id":"other"`, 1), false},
		{"/create", strings.Replace(create, strings.Repeat("a", 64), strings.Repeat("A", 64), 1), false},
		{"/create", strings.TrimSuffix(create, "}") + `,"asOf":"2026-01-01T00:00:00Z"}`, false},
		{"/create", strings.TrimSuffix(create, "}") + `,"facts":[]}`, false},
		{"/create", strings.TrimSuffix(create, "}") + `,"results":[]}`, false},
	} {
		method := "GET"
		if tt.body != "" {
			method = "POST"
		}
		r := httptest.NewRequest(method, simulationAssetAssessmentPath+tt.path, strings.NewReader(tt.body))
		r.Header.Set("Content-Type", "application/json")
		_, e := decodeSimulationAssetAssessmentInput(httptest.NewRecorder(), r, strings.Split(tt.path, "?")[0])
		if (e == nil) != tt.good {
			t.Fatalf("%s good=%v err=%v", tt.path, tt.good, e)
		}
	}
}
func TestSimulationAssetAssessmentHandlerFailsClosed(t *testing.T) {
	s := Service{origin: "https://example.test"}
	for _, tt := range []struct {
		method, path string
		enabled      bool
		want         int
	}{
		{"GET", "/observation?domainId=domain-a&observationId=synthetic-assets-v1", false, 404},
		{"GET", "/observation?domainId=domain-a&observationId=synthetic-assets-v1", true, 503},
		{"PATCH", "/observation", true, 405}, {"POST", "/seed", true, 404}, {"POST", "/create", true, 403},
	} {
		r := httptest.NewRequest(tt.method, simulationAssetAssessmentPath+tt.path, nil)
		w := httptest.NewRecorder()
		s.SimulationAssetAssessmentHandler(tt.enabled).ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatal(tt, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("headers")
		}
	}
	for _, tt := range []struct {
		e    error
		want int
	}{{assessment.ErrDenied, 403}, {assessment.ErrNotFound, 404}, {assessment.ErrConflict, 409}, {assessment.ErrInvalid, 400}, {assessment.ErrLimit, 422}, {context.DeadlineExceeded, 503}} {
		got, _ := simulationAssetAssessmentHTTPError(tt.e)
		if got != tt.want {
			t.Fatal(tt.e, got)
		}
	}
	for _, s := range []string{"CREATE TRIGGER simulation_asset_assessment_grant_epoch", "adtr.task_auth_lock", "adtr.task_auth_bump", "OLD.mark ='simulation_asset_assessment'", "NEW.mark ='simulation_asset_assessment'"} {
		if !strings.Contains(SimulationAssetAssessmentPermissionSchema, s) {
			t.Fatal(s)
		}
	}
	if strings.Contains(SimulationAssetAssessmentPermissionSchema, "INSERT INTO") {
		t.Fatal("migration grants rights")
	}
}
