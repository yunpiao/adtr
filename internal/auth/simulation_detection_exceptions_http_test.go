package auth

import (
	"context"
	"encoding/json"
	"errors"
	exceptions "github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSimulationDetectionExceptionsAuthorityMatrix(t *testing.T) {
	for _, role := range []string{"platform_admin", "viewer"} {
		g, e := loadAccessGrants(context.Background(), nil, "test", role)
		if e != nil {
			t.Fatal(e)
		}
		for p, rt := range simulationDetectionExceptionsRoutes {
			if simulationDetectionExceptionsPathAllowed(rt.method, simulationDetectionExceptionsPath+p, g) {
				t.Fatal("builtin bypass", role, p)
			}
		}
	}
	for mask := 0; mask < 64; mask++ {
		g := map[string]AccessAuth{"simulation_detection_exceptions": {mask&1 != 0, mask&2 != 0}, "simulation_detection": {mask&4 != 0, mask&8 != 0}, "simulation_events": {mask&16 != 0, mask&32 != 0}}
		for p, rt := range simulationDetectionExceptionsRoutes {
			want := mask&1 != 0 && (!rt.write || mask&2 != 0)
			if simulationDetectionExceptionsSourceRoute(p) {
				want = want && mask&4 != 0 && mask&16 != 0
			}
			if got := simulationDetectionExceptionsPathAllowed(rt.method, simulationDetectionExceptionsPath+p, g); got != want {
				t.Fatalf("mask%d %s got%v", mask, p, got)
			}
			for _, bad := range []string{p + "/", p + "/extra"} {
				if simulationDetectionExceptionsPathAllowed(rt.method, simulationDetectionExceptionsPath+bad, g) {
					t.Fatal("open route")
				}
			}
			if simulationDetectionExceptionsPathAllowed("DELETE", simulationDetectionExceptionsPath+p, g) {
				t.Fatal("method bypass")
			}
		}
	}
}
func TestSimulationDetectionExceptionsClosedInputs(t *testing.T) {
	declaration := `{"id":"e","global":true,"sourceTaskId":"","ruleId":"","reason":"synthetic","notBefore":"2026-01-01T00:00:00Z","expiresAt":"2027-01-01T00:00:00Z","conditions":[{"field":"event.outcome","operator":"eq","values":["failure"]}]}`
	valid := `{"domainId":"d","operationId":"op","expectedRevision":1,"declaration":` + declaration + `}`
	decode := func(path, body string) error {
		r := httptest.NewRequest("POST", simulationDetectionExceptionsPath+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, e := decodeSimulationDetectionExceptionsInput(httptest.NewRecorder(), r, path)
		return e
	}
	if e := decode("/create", valid); e != nil {
		t.Fatal(e)
	}
	bad := []string{valid + `{}`, `[]`, strings.Replace(valid, `"domainId":"d"`, `"domainId":"d","domainId":"x"`, 1), strings.Replace(valid, `"expectedRevision":1`, `"expectedRevision":0`, 1), strings.Replace(valid, `"expectedRevision":1`, `"expectedRevision":257`, 1), strings.Replace(valid, `"global":true`, `"global":null`, 1), strings.Replace(valid, `"global":true`, `"global":false`, 1), strings.Replace(valid, `"sourceTaskId":""`, `"sourceTaskId":"forged"`, 1), strings.Replace(valid, `"conditions":[`, `"conditions":null,"unknown":[`, 1), strings.Replace(valid, `"values":["failure"]`, `"values":[null]`, 1), strings.Replace(valid, `"values":["failure"]`, `"values":["a","b"]`, 1), strings.Replace(valid, `"operator":"eq"`, `"operator":"regex"`, 1), strings.Replace(valid, `"field":"event.outcome"`, `"field":"event.code"`, 1), strings.Replace(valid, `"field":"event.outcome"`, `"field":"object.kind"`, 1), strings.Replace(valid, `"reason":"synthetic"`, `"reason":"synthetic","reason":"duplicate"`, 1), strings.Replace(valid, `"expiresAt":"2027-01-01T00:00:00Z"`, `"expiresAt":"2025-01-01T00:00:00Z"`, 1)}
	for _, key := range []string{"actorId", "tenant", "matches", "passed", "digest", "enabled", "archived", "snapshot", "id"} {
		bad = append(bad, strings.TrimSuffix(valid, "}")+`,"`+key+`":"forged"}`)
	}
	for _, b := range bad {
		if decode("/create", b) == nil {
			t.Fatal("accepted invalid", b)
		}
	}
	bound := strings.Replace(strings.Replace(strings.Replace(valid, `"global":true`, `"global":false`, 1), `"sourceTaskId":""`, `"sourceTaskId":"source"`, 1), `"ruleId":""`, `"ruleId":"rule"`, 1)
	if e := decode("/edit", bound); e != nil {
		t.Fatal(e)
	}
	imp := `{"domainId":"d","operationId":"import","expectedRevision":1,"entries":[` + declaration + `]}`
	if e := decode("/import", imp); e != nil {
		t.Fatal(e)
	}
	for _, b := range []string{strings.Replace(imp, `[`+declaration+`]`, `[`+declaration+`,`+declaration+`]`, 1), strings.Replace(imp, `[`+declaration+`]`, `null`, 1), strings.Replace(imp, `[`+declaration+`]`, `[]`, 1)} {
		if decode("/import", b) == nil {
			t.Fatal("bad import accepted")
		}
	}
	for _, q := range []string{"domainId=d&revision=00", "domainId=d&revision=0", "domainId=d&revision=257", "domainId=d&revision=1&revision=2", "domainId=d&revision=1&actor=x", "domainId=%ff&revision=1"} {
		r := httptest.NewRequest("GET", simulationDetectionExceptionsPath+"/history?"+q, nil)
		if _, e := decodeSimulationDetectionExceptionsInput(httptest.NewRecorder(), r, "/history"); e == nil {
			t.Fatal("bad query", q)
		}
	}
	preview := `{"domainId":"d","sourceTaskId":"task","eventId":"event","ruleId":"rule","id":"preview","reason":"synthetic","notBefore":"2026-01-01T00:00:00Z","expiresAt":"2027-01-01T00:00:00Z"}`
	if e := decode("/preview", preview); e != nil {
		t.Fatal(e)
	}
	eval := `{"domainId":"d","operationId":"op","expectedRevision":1,"id":"evaluation","sourceTaskId":"task"}`
	if e := decode("/evaluate", eval); e != nil {
		t.Fatal(e)
	}
	for _, b := range []string{strings.TrimSuffix(eval, "}") + `,"matches":[]}`, strings.Replace(eval, `"sourceTaskId":"task"`, `"sourceTaskId":null`, 1)} {
		if decode("/evaluate", b) == nil {
			t.Fatal("forged eval accepted")
		}
	}
}
func TestSimulationDetectionExceptionsFailClosedAndLiveFlags(t *testing.T) {
	s := &Service{origin: "https://test.invalid"}
	for _, tc := range []struct {
		enabled, detection, events bool
		method, path, body, origin string
		want                       int
	}{
		{false, true, true, "GET", "/snapshot?domainId=d", "", "", 404},
		{true, false, true, "POST", "/preview", "{}", "https://test.invalid", 404},
		{true, true, false, "GET", "/evaluations?domainId=d", "", "", 404},
		{true, true, true, "DELETE", "/snapshot", "", "", 405},
		{true, true, true, "POST", "/create", "{}", "", 403},
		{true, true, true, "GET", "/snapshot?domainId=d", "", "", 503},
		{true, false, false, "GET", "/receipt?domainId=d&operationId=op", "", "", 503},
		{true, true, true, "GET", "/unknown", "", "", 404},
	} {
		r := httptest.NewRequest(tc.method, simulationDetectionExceptionsPath+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.SimulationDetectionExceptionsHandler(tc.enabled, tc.detection, tc.events, nil).ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s got%d want%d", tc.path, w.Code, tc.want)
		}
		var body map[string]any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal("not JSON")
		}
	}
	for _, de := range []bool{false, true} {
		for _, ee := range []bool{false, true} {
			for mask := 0; mask < 16; mask++ {
				g := map[string]AccessAuth{"simulation_detection_exceptions": {true, true}, "simulation_detection": {mask&1 != 0, mask&2 != 0}, "simulation_events": {mask&4 != 0, mask&8 != 0}}
				s := simulationDetectionExceptionsScope(User{ID: 7, tenant: "tenant"}, "domain", g, de, ee)
				if !s.Read || !s.Manage || s.DetectionRead != (mask&1 != 0 && de) || s.EventsRead != (mask&4 != 0 && ee) || s.ActorID != 7 || s.Tenant != "tenant" || s.Domain != "domain" {
					t.Fatal("scope mismatch", s)
				}
			}
		}
	}
	for _, tc := range []struct {
		e      error
		status int
		code   string
	}{{exceptions.ErrDenied, 403, "forbidden"}, {exceptions.ErrInvalid, 400, "invalid_input"}, {exceptions.ErrConflict, 409, "revision_conflict"}, {exceptions.ErrLimit, 422, "simulation_limit_exceeded"}, {exceptions.ErrNotFound, 404, "not_found"}, {tasks.ErrSchemaIncompatible, 503, "schema_incompatible"}, {errors.New("private diagnostic"), 503, "simulation_unavailable"}} {
		s, c := simulationDetectionExceptionsHTTPError(tc.e)
		if s != tc.status || c != tc.code {
			t.Fatalf("%v got%d/%s", tc.e, s, c)
		}
	}
}
