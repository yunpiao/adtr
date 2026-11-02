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

func TestSimulationRiskExplicitGrantAndClosedRoutes(t *testing.T) {
	for _, role := range []string{"platform_admin", "viewer"} {
		grants, err := loadAccessGrants(context.Background(), nil, "test", role)
		if err != nil || grants["simulation_risk"].Readable || grants["simulation_risk"].Writeable {
			t.Fatalf("builtin inherited simulation: %s %v", role, err)
		}
		for path, method := range simulationRiskRoutes {
			if simulationRiskPathAllowed(method, simulationRiskPath+path, grants) {
				t.Fatal("builtin allowed", role, path)
			}
		}
	}
	if _, err := validateGrants([]AccessGrant{{Mark: "simulation_risk", Auth: AccessAuth{true, true}}}); err != nil {
		t.Fatal(err)
	}
	for _, only := range []bool{false, true} {
		for _, n := range permissionNodes(nil, only) {
			if n.Mark == "simulation_risk" {
				t.Fatal("ungranted capability visible")
			}
		}
	}
	read := map[string]AccessAuth{"simulation_risk": {Readable: true}}
	write := map[string]AccessAuth{"simulation_risk": {Readable: true, Writeable: true}}
	for path, method := range simulationRiskRoutes {
		full := simulationRiskPath + path
		if !simulationRiskPathAllowed(method, full, write) || simulationRiskPathAllowed(method, full+"/", write) || simulationRiskPathAllowed("DELETE", full, write) {
			t.Fatal("route boundary", path)
		}
		if simulationRiskPathAllowed(method, full, read) != (path != "/reset") {
			t.Fatal("read/write separation", path)
		}
		if simulationRiskPathAllowed(method, full, map[string]AccessAuth{"simulation_risk": {Writeable: true}}) {
			t.Fatal("write without read")
		}
	}
	found := false
	for _, n := range permissionNodes(read, true) {
		if n.Mark == "simulation_risk" {
			found = true
			if len(n.Paths) != 4 {
				t.Fatal("incomplete routes")
			}
		}
	}
	if !found {
		t.Fatal("granted capability hidden")
	}
}

func TestSimulationRiskClosedInputs(t *testing.T) {
	valid := `{"domainId":"domain-a","datasetId":"fixture-a","subject":"subject-a","operationId":"operation-a","reason":"synthetic review","expectedRevision":1}`
	bodies := []struct {
		body string
		ok   bool
	}{{valid, true}, {strings.Replace(valid, `"expectedRevision":1`, `"expectedRevision":0`, 1), false}, {strings.Replace(valid, `"reason":"synthetic review"`, `"reason":""`, 1), false}, {strings.Replace(valid, `"domainId":"domain-a"`, `"domainId":null`, 1), false}, {valid + `{}`, false}, {`[]`, false}, {strings.Replace(valid, `"reason":"synthetic review"`, `"reason":"synthetic review","reason":"x"`, 1), false}, {strings.Replace(valid, `"expectedRevision":1`, `"expectedRevision":1.5`, 1), false}}
	for _, key := range []string{"actor", "tenant", "scope", "manage", "At", "fixture", "upload", "DomainID", "expectedrevision"} {
		bodies = append(bodies, struct {
			body string
			ok   bool
		}{strings.TrimSuffix(valid, "}") + `,"` + key + `":"forged"}`, false})
	}
	for _, tc := range bodies {
		r := httptest.NewRequest("POST", simulationRiskPath+"/reset", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeSimulationRiskInput(httptest.NewRecorder(), r, "/reset")
		if (err == nil) != tc.ok {
			t.Fatalf("input %s: %v", tc.body, err)
		}
	}
	for _, tc := range []struct {
		path, query string
		ok          bool
	}{
		{"/datasets", "domainId=d&offset=0&limit=100", true},
		{"/operation", "domainId=d&datasetId=f&operationId=o", true},
		{"/operation", "domainId=d&datasetId=f&operationId=o", true},
		{"/datasets", "domainId=d&offset=00&limit=100", false},
		{"/datasets", "domainId=d&offset=-1&limit=100", false},
		{"/datasets", "domainId=d&offset=0&limit=101", false},
		{"/datasets", "domainId=d&offset=0&limit=1&domainId=e", false},
		{"/operation", "domainId=d&datasetId=f&operationId=o&actor=other", false},
		{"/operation", "domainId=d&datasetId=f&operationId=o&historyLimit=1", false},
	} {
		r := httptest.NewRequest("GET", simulationRiskPath+tc.path+"?"+tc.query, nil)
		_, err := decodeSimulationRiskInput(httptest.NewRecorder(), r, tc.path)
		if (err == nil) != tc.ok {
			t.Fatalf("query %s: %v", tc.query, err)
		}
	}
}

func TestSimulationRiskHandlerFailClosed(t *testing.T) {
	s := &Service{origin: "https://test.invalid"}
	for _, tc := range []struct {
		enabled                    bool
		method, path, body, origin string
		want                       int
	}{
		{false, "GET", simulationRiskPath + "/datasets?domainId=d&offset=0&limit=1", "", "", 404},
		{true, "GET", simulationRiskPath + "/unknown", "", "", 404},
		{true, "DELETE", simulationRiskPath + "/datasets", "", "", 405},
		{true, "POST", simulationRiskPath + "/query", `{}`, "", 403},
		{true, "POST", simulationRiskPath + "/query", `{}`, "https://test.invalid", 400},
		{true, "POST", simulationRiskPath + "/query", `{"domainId":"d","datasetId":"f","subject":"u"}`, "https://test.invalid", 503},
		{true, "GET", simulationRiskPath + "/datasets?domainId=d&offset=0&limit=1", "", "", 503},
		{true, "GET", simulationRiskPath + "/datasets?domainId=d&offset=0&limit=1", "payload", "", 400},
		{true, "POST", simulationRiskPath + "/reset", strings.Repeat("x", 8193), "https://test.invalid", 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.SimulationRiskHandler(tc.enabled).ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s got %d want %d: %s", tc.method, tc.path, w.Code, tc.want, w.Body)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{simulationevents.ErrNotFound, 404, "not_found"}, {securityevents.ErrDenied, 403, "forbidden"}, {securityevents.ErrConflict, 409, "revision_conflict"}, {securityevents.ErrInvalid, 400, "invalid_input"}, {simulationevents.ErrLimit, 422, "simulation_limit_exceeded"}, {errors.New("secret database details"), 503, "simulation_unavailable"}, {fail(401, "unauthenticated"), 401, "unauthenticated"}} {
		status, code := simulationRiskHTTPError(tc.err)
		if status != tc.status || code != tc.code {
			t.Fatal(status, code)
		}
	}
}

func TestSimulationRiskProfileHintIsAuthenticatedOnlyAndDefaultDenied(t *testing.T) {
	raw, err := json.Marshal(User{})
	if err != nil || strings.Contains(string(raw), "simulationRiskReadable") {
		t.Fatal("unpopulated user exposes hint", string(raw), err)
	}
	for _, granted := range []bool{false, true} {
		u := User{SimulationRiskReadable: &granted}
		raw, err = json.Marshal(u)
		var got map[string]any
		if err != nil || json.Unmarshal(raw, &got) != nil || got["simulationRiskReadable"] != granted {
			t.Fatal("hint wire shape", string(raw), err)
		}
	}
	if allowed, err := simulationRiskProfileReadable(context.Background(), nil, User{NeedChange: true}); err != nil || allowed {
		t.Fatal("password-change state advertises capability")
	}
}

func TestSimulationRiskPermissionMigrationPreservesBoundaries(t *testing.T) {
	sql := SimulationRiskPermissionSchema
	for _, required := range []string{"'simulation_events','simulation_risk'", "CREATE TRIGGER simulation_risk_grant_epoch", "adtr.task_auth_lock", "adtr.task_auth_bump"} {
		if !strings.Contains(sql, required) {
			t.Fatal("missing migration boundary", required)
		}
	}
	for _, forbidden := range []string{"INSERT INTO", "DELETE FROM", "DROP TRIGGER", "DROP FUNCTION", "ALTER FUNCTION"} {
		if strings.Contains(sql, forbidden) {
			t.Fatal("migration mutates grants or old guards", forbidden)
		}
	}
}
func TestSimulationRiskQueryStrictSubjectAndNoClientScope(t *testing.T) {
	valid := `{"domainId":"d","datasetId":"f","subject":"synthetic-user"}`
	cases := []string{
		strings.Replace(valid, `"synthetic-user"`, `""`, 1),
		strings.Replace(valid, `"synthetic-user"`, `" spaced "`, 1),
		strings.Replace(valid, `"synthetic-user"`, `null`, 1),
		strings.Replace(valid, `"synthetic-user"`, `"x\ny"`, 1),
		strings.Replace(valid, `"synthetic-user"`, `"`+strings.Repeat("u", 129)+`"`, 1),
	}
	for _, key := range []string{"scope", "tenant", "actor", "at", "asOf", "as_of", "events", "resets", "read", "manage", "offset"} {
		cases = append(cases, strings.TrimSuffix(valid, "}")+`,"`+key+`":"forged"}`)
	}
	for _, body := range cases {
		r := httptest.NewRequest("POST", simulationRiskPath+"/query", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if _, err := decodeSimulationRiskInput(httptest.NewRecorder(), r, "/query"); err == nil {
			t.Fatal("accepted", body)
		}
	}
}

func TestSimulationEventHintSchemaAdditiveCompatibility(t *testing.T) {
	for _, v := range []int{0, 23, 24, 25, 26, 27, 28, 29, 30, 1000} {
		if simulationEventProfileSchemaAvailable(v) != (v == 24 || v == 25 || v == 26 || v == 27 || v == 28 || v == 29) {
			t.Fatal("event hint schema", v)
		}
	}
}
