package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	schedules "github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestSimulationDetectionSchedulesClosedRoutesRequireBothGrants(t *testing.T) {
	for path, rt := range simulationDetectionSchedulesRoutes {
		for mask := 0; mask < 16; mask++ {
			grants := map[string]AccessAuth{"simulation_detection": {Readable: mask&1 != 0, Writeable: mask&2 != 0}, "simulation_detection_schedules": {Readable: mask&4 != 0, Writeable: mask&8 != 0}}
			want := mask&5 == 5 && (!rt.write || mask == 15)
			if got := simulationDetectionSchedulesPathAllowed(rt.method, simulationDetectionSchedulesPath+path, grants); got != want {
				t.Fatalf("%s mask%d got%v", path, mask, got)
			}
			if simulationDetectionSchedulesPathAllowed(rt.method, simulationDetectionSchedulesPath+path+"/", grants) || simulationDetectionSchedulesPathAllowed("DELETE", simulationDetectionSchedulesPath+path, grants) {
				t.Fatal("route widened")
			}
		}
	}
	for _, role := range []string{"platform_admin", "viewer"} {
		g, err := loadAccessGrants(context.Background(), nil, "one", role)
		if err != nil || g["simulation_detection_schedules"].Readable || g["simulation_detection_schedules"].Writeable {
			t.Fatal("default grant", role, err)
		}
	}
}
func TestSimulationDetectionSchedulesClosedInputs(t *testing.T) {
	valid := `{"domainId":"d","label":"Synthetic","cycle":0,"templateId":"t","templateVersion":1,"datasetId":"f","operationId":"op"}`
	decode := func(body string) error {
		r := httptest.NewRequest("POST", simulationDetectionSchedulesPath+"/create", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeSimulationDetectionSchedulesInput(httptest.NewRecorder(), r, "/create")
		return err
	}
	if err := decode(valid); err != nil {
		t.Fatal(err)
	}
	cases := []string{valid + `{}`, `[]`, strings.Replace(valid, `"cycle":0,`, "", 1), strings.Replace(valid, `"cycle":0`, `"cycle":null`, 1), strings.Replace(valid, `"cycle":0`, `"cycle":0,"cycle":1`, 1), strings.Replace(valid, `"cycle":0`, `"cycle":3`, 1), strings.Replace(valid, `"label":"Synthetic"`, `"label":" leading"`, 1), strings.Replace(valid, `"domainId":"d"`, `"domainId":"platform"`, 1), strings.Replace(valid, `"templateVersion":1`, `"templateVersion":0`, 1)}
	for _, key := range []string{"tenant", "actorId", "id", "at", "anchor", "rate", "Rate", "scope", "payload", "authorizationVersion", "expectedControlVersion"} {
		cases = append(cases, strings.TrimSuffix(valid, "}")+`,"`+key+`":"forged"}`)
	}
	for _, body := range cases {
		if decode(body) == nil {
			t.Fatal("accepted", body)
		}
	}
	for _, q := range []string{"domainId=d&pageIdx=1&pageSize=20&actorId=2", "domainId=d&pageIdx=1&pageSize=51", "domainId=d&pageIdx=01&pageSize=20", "domainId=d&pageIdx=0&pageSize=20", "domainId=d&pageIdx=10001&pageSize=20", "domainId=d&pageIdx=1&pageSize=20&pageSize=20", "domainId=%ff&pageIdx=1&pageSize=20", "domainId=d;actor=x&pageIdx=1&pageSize=20"} {
		r := httptest.NewRequest("GET", simulationDetectionSchedulesPath+"/list?"+q, nil)
		if _, err := decodeSimulationDetectionSchedulesInput(httptest.NewRecorder(), r, "/list"); err == nil {
			t.Fatal("accepted", q)
		}
	}
	for _, path := range []string{"/enable", "/pause", "/archive"} {
		r := httptest.NewRequest("POST", simulationDetectionSchedulesPath+path, strings.NewReader(`{"domainId":"d","id":"s","operationId":"o","expectedControlVersion":0}`))
		r.Header.Set("Content-Type", "application/json")
		if _, err := decodeSimulationDetectionSchedulesInput(httptest.NewRecorder(), r, path); err == nil {
			t.Fatal("CAS absent", path)
		}
	}
}
func TestSimulationDetectionSchedulesFailClosed(t *testing.T) {
	s := &Service{origin: "https://test.invalid"}
	for _, tc := range []struct {
		manual, scheduled          bool
		method, path, body, origin string
		want                       int
	}{
		{false, false, "GET", "/list?domainId=d&pageIdx=1&pageSize=20", "", "", 404},
		{true, false, "GET", "/list?domainId=d&pageIdx=1&pageSize=20", "", "", 404},
		{false, true, "GET", "/list?domainId=d&pageIdx=1&pageSize=20", "", "", 404},
		{true, true, "GET", "/unknown", "", "", 404},
		{true, true, "DELETE", "/list", "", "", 405},
		{true, true, "POST", "/archive", "{}", "", 403},
		{true, true, "POST", "/archive", "{}", s.origin, 400},
		{true, true, "GET", "/list?domainId=d&pageIdx=1&pageSize=20", "", "", 503},
		{true, true, "GET", "/list?domainId=d&pageIdx=1&pageSize=20", "body", "", 400},
	} {
		r := httptest.NewRequest(tc.method, simulationDetectionSchedulesPath+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.SimulationDetectionSchedulesHandler(tc.manual, tc.scheduled, nil).ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc, w.Code, w.Body)
		}
	}
}
func TestSimulationDetectionSchedulesAuthorizerRejectsMalformedAndDisabled(t *testing.T) {
	p := tasks.Principal{TenantID: "one", ActorID: 1}
	scope := tasks.Scope{DomainID: "d", TaskName: simulationDetectionScheduledKind}
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		a := NewSimulationDetectionSchedulesTaskAuthorizer(flags[0], flags[1])
		if _, err := a(context.Background(), nil, p, scope, tasks.Execute); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal(flags, err)
		}
	}
	a := simulationDetectionSchedulesTaskAuthorizer(time.Now)
	for _, kind := range []string{"simulation.detection", "infrastructure.health", "simulation.detection.scheduled.extra", ""} {
		sc := scope
		sc.TaskName = kind
		if _, err := a(context.Background(), nil, p, sc, tasks.Read); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal(kind, err)
		}
	}
}
func TestSimulationDetectionSchedulesErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{schedules.ErrConfiguration, 409, "configuration_changed"}, {schedules.ErrConflict, 409, "revision_conflict"}, {schedules.ErrDenied, 403, "forbidden"}, {schedules.ErrInvalid, 400, "invalid_input"}, {schedules.ErrLimit, 422, "simulation_limit_exceeded"}, {schedules.ErrNotFound, 404, "not_found"}, {tasks.ErrAuthorization, 403, "forbidden"}} {
		status, code := simulationDetectionSchedulesHTTPError(tc.err)
		if status != tc.status || code != tc.code {
			t.Fatal(tc, status, code)
		}
	}
}

func TestSimulationDetectionSchedulesPermissionMigrationDefaultDeny(t *testing.T) {
	sql := SimulationDetectionSchedulesPermissionSchema
	for _, mark := range accessMarks {
		if mark == "simulation_labels" || mark == "simulation_rule_catalog" || mark == "simulation_rule_catalog_publish" || mark == "simulation_detection_exceptions" || mark == "simulation_dns_ip" || mark == "simulation_asset_assessment" {
			if strings.Contains(sql, "'"+mark+"'") {
				t.Fatal("historical migration27 gained future mark")
			}
			if !strings.Contains(SimulationAssetAssessmentPermissionSchema, "'"+mark+"'") {
				t.Fatal("migration32 missing current mark")
			}
			continue
		}
		if !strings.Contains(sql, "'"+mark+"'") {
			t.Fatal("missing mark", mark)
		}
	}
	for _, required := range []string{"CREATE TRIGGER simulation_detection_schedules_grant_epoch", "adtr.task_auth_lock", "adtr.task_auth_bump", "OLD.mark='simulation_detection_schedules'", "NEW.mark='simulation_detection_schedules'"} {
		if !strings.Contains(sql, required) {
			t.Fatal("missing epoch guard", required)
		}
	}
	for _, forbidden := range []string{"INSERT INTO adtr.access_permissions", "DROP TRIGGER", "DISABLE TRIGGER", "CREATE OR REPLACE FUNCTION adtr.task_auth"} {
		if strings.Contains(sql, forbidden) {
			t.Fatal("changed existing authority", forbidden)
		}
	}
	if _, err := validateGrants([]AccessGrant{{Mark: "simulation_detection_schedules", Auth: AccessAuth{true, true}}}); err != nil {
		t.Fatal(err)
	}
}
