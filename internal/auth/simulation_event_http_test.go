package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

func TestSimulationEventExplicitGrantAndClosedRoutes(t *testing.T) {
	for _, role := range []string{"platform_admin", "viewer"} {
		grants, err := loadAccessGrants(context.Background(), nil, "test", role)
		if err != nil || grants["simulation_events"].Readable || grants["simulation_events"].Writeable {
			t.Fatalf("builtin inherited simulation: %s %v", role, err)
		}
		for path, method := range simulationEventRoutes {
			if simulationEventPathAllowed(method, simulationEventPath+path, grants) {
				t.Fatal("builtin allowed", role, path)
			}
		}
	}
	if _, err := validateGrants([]AccessGrant{{Mark: "simulation_events", Auth: AccessAuth{true, true}}}); err != nil {
		t.Fatal(err)
	}
	for _, only := range []bool{false, true} {
		for _, n := range permissionNodes(nil, only) {
			if n.Mark == "simulation_events" {
				t.Fatal("ungranted capability visible")
			}
		}
	}
	read := map[string]AccessAuth{"simulation_events": {Readable: true}}
	write := map[string]AccessAuth{"simulation_events": {Readable: true, Writeable: true}}
	for path, method := range simulationEventRoutes {
		full := simulationEventPath + path
		if !simulationEventPathAllowed(method, full, write) || simulationEventPathAllowed(method, full+"/", write) || simulationEventPathAllowed("DELETE", full, write) {
			t.Fatal("route boundary", path)
		}
		if simulationEventPathAllowed(method, full, read) != (path != "/transition") {
			t.Fatal("read/write separation", path)
		}
		if simulationEventPathAllowed(method, full, map[string]AccessAuth{"simulation_events": {Writeable: true}}) {
			t.Fatal("write without read")
		}
	}
	found := false
	for _, n := range permissionNodes(read, true) {
		if n.Mark == "simulation_events" {
			found = true
			if len(n.Paths) != 5 {
				t.Fatal("incomplete routes")
			}
		}
	}
	if !found {
		t.Fatal("granted capability hidden")
	}
}

func TestSimulationEventClosedInputs(t *testing.T) {
	valid := `{"domainId":"domain-a","datasetId":"fixture-a","caseId":"case-a","operationId":"operation-a","action":"close","reason":"synthetic review","expectedRevision":1}`
	bodies := []struct {
		body string
		ok   bool
	}{{valid, true}, {strings.Replace(valid, `"expectedRevision":1`, `"expectedRevision":0`, 1), false}, {strings.Replace(valid, `"action":"close"`, `"action":"remote_block"`, 1), false}, {strings.Replace(valid, `"reason":"synthetic review"`, `"reason":""`, 1), false}, {strings.Replace(valid, `"domainId":"domain-a"`, `"domainId":null`, 1), false}, {valid + `{}`, false}, {`[]`, false}, {strings.Replace(valid, `"reason":"synthetic review"`, `"reason":"synthetic review","reason":"x"`, 1), false}, {strings.Replace(valid, `"expectedRevision":1`, `"expectedRevision":1.5`, 1), false}}
	for _, key := range []string{"actor", "tenant", "scope", "manage", "At", "fixture", "upload", "DomainID", "expectedrevision"} {
		bodies = append(bodies, struct {
			body string
			ok   bool
		}{strings.TrimSuffix(valid, "}") + `,"` + key + `":"forged"}`, false})
	}
	for _, tc := range bodies {
		r := httptest.NewRequest("POST", simulationEventPath+"/transition", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeSimulationEventInput(httptest.NewRecorder(), r, "/transition")
		if (err == nil) != tc.ok {
			t.Fatalf("input %s: %v", tc.body, err)
		}
	}
	for _, tc := range []struct {
		path, query string
		ok          bool
	}{
		{"/datasets", "domainId=d&offset=0&limit=100", true},
		{"/detail", "domainId=d&datasetId=f&caseId=c&historyOffset=0&historyLimit=100", true},
		{"/operation", "domainId=d&datasetId=f&operationId=o", true},
		{"/datasets", "domainId=d&offset=00&limit=100", false},
		{"/datasets", "domainId=d&offset=-1&limit=100", false},
		{"/datasets", "domainId=d&offset=0&limit=101", false},
		{"/datasets", "domainId=d&offset=0&limit=1&domainId=e", false},
		{"/operation", "domainId=d&datasetId=f&operationId=o&actor=other", false},
		{"/detail", "domainId=d&datasetId=f&caseId=c&historyOffset=1025&historyLimit=1", false},
	} {
		r := httptest.NewRequest("GET", simulationEventPath+tc.path+"?"+tc.query, nil)
		_, err := decodeSimulationEventInput(httptest.NewRecorder(), r, tc.path)
		if (err == nil) != tc.ok {
			t.Fatalf("query %s: %v", tc.query, err)
		}
	}
}

func TestSimulationEventHandlerFailClosed(t *testing.T) {
	s := &Service{origin: "https://test.invalid"}
	for _, tc := range []struct {
		enabled                    bool
		method, path, body, origin string
		want                       int
	}{
		{false, "GET", simulationEventPath + "/datasets?domainId=d&offset=0&limit=1", "", "", 404},
		{true, "GET", simulationEventPath + "/unknown", "", "", 404},
		{true, "DELETE", simulationEventPath + "/datasets", "", "", 405},
		{true, "POST", simulationEventPath + "/query", `{}`, "", 403},
		{true, "POST", simulationEventPath + "/query", `{}`, "https://test.invalid", 400},
		{true, "POST", simulationEventPath + "/query", `{"domainId":"d","datasetId":"f","offset":0,"limit":1}`, "https://test.invalid", 503},
		{true, "GET", simulationEventPath + "/datasets?domainId=d&offset=0&limit=1", "", "", 503},
		{true, "GET", simulationEventPath + "/datasets?domainId=d&offset=0&limit=1", "payload", "", 400},
		{true, "POST", simulationEventPath + "/transition", strings.Repeat("x", 8193), "https://test.invalid", 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.SimulationEventsHandler(tc.enabled).ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s got %d want %d: %s", tc.method, tc.path, w.Code, tc.want, w.Body)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{simulationevents.ErrNotFound, 404, "not_found"}, {securityevents.ErrDenied, 403, "forbidden"}, {securityevents.ErrConflict, 409, "revision_conflict"}, {securityevents.ErrInvalid, 400, "invalid_input"}, {simulationevents.ErrLimit, 422, "simulation_limit_exceeded"}, {errors.New("secret database details"), 503, "simulation_unavailable"}, {fail(401, "unauthenticated"), 401, "unauthenticated"}} {
		status, code := simulationEventHTTPError(tc.err)
		if status != tc.status || code != tc.code {
			t.Fatal(status, code)
		}
	}
}

func TestSimulationEventProfileHintIsAuthenticatedOnlyAndDefaultDenied(t *testing.T) {
	raw, err := json.Marshal(User{})
	if err != nil || strings.Contains(string(raw), "simulationEventsReadable") {
		t.Fatal("unpopulated user exposes hint", string(raw), err)
	}
	for _, granted := range []bool{false, true} {
		u := User{SimulationEventsReadable: &granted}
		raw, err = json.Marshal(u)
		var got map[string]any
		if err != nil || json.Unmarshal(raw, &got) != nil || got["simulationEventsReadable"] != granted {
			t.Fatal("hint wire shape", string(raw), err)
		}
	}
	if allowed, err := simulationEventProfileReadable(context.Background(), nil, User{NeedChange: true}); err != nil || allowed {
		t.Fatal("password-change state advertises capability")
	}
}
