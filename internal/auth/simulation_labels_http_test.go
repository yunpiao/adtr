package auth

import (
	"encoding/json"
	labels "github.com/yunpiao/adtr/internal/simulationlabels"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSimulationLabelsIndependentClosedPermissions(t *testing.T) {
	for _, p := range simulationLabelsRouteOrder {
		rt := simulationLabelsRoutes[p]
		for _, g := range []map[string]AccessAuth{nil, {"simulation_events": {Readable: true, Writeable: true}, "simulation_risk": {Readable: true, Writeable: true}, "simulation_detection": {Readable: true, Writeable: true}, "simulation_detection_schedules": {Readable: true, Writeable: true}}, {"simulation_labels": {Writeable: true}}} {
			if simulationLabelsPathAllowed(rt.method, simulationLabelsPath+p, g) {
				t.Fatal(p, "inherited permission")
			}
		}
		g := map[string]AccessAuth{"simulation_labels": {Readable: true}}
		if simulationLabelsPathAllowed(rt.method, simulationLabelsPath+p, g) == rt.write {
			t.Fatal(p, "read grant")
		}
		g["simulation_labels"] = AccessAuth{Readable: true, Writeable: true}
		if !simulationLabelsPathAllowed(rt.method, simulationLabelsPath+p, g) {
			t.Fatal(p)
		}
		for _, suffix := range []string{p + "/", p + "evil"} {
			if simulationLabelsPathAllowed(rt.method, simulationLabelsPath+suffix, g) {
				t.Fatal(suffix)
			}
		}
	}
}
func TestSimulationLabelsStrictDTO(t *testing.T) {
	create := `{"domainId":"domain-a","datasetId":"fixture","policyId":"p","expectedRevision":0,"spec":{"name":"Policy","sensitiveGroupIds":["g"],"manualLabels":[{"objectId":"u","label":"sensitive"}]},"operationId":"op","reason":"test"}`
	for _, tc := range []struct {
		name, path, method, body string
		valid                    bool
	}{
		{"create", "/create", "POST", create, true},
		{"duplicate", "/create", "POST", strings.Replace(create, `"domainId":"domain-a"`, `"domainId":"domain-a","domainId":"domain-a"`, 1), false},
		{"nestedduplicate", "/create", "POST", strings.Replace(create, `"label":"sensitive"`, `"label":"sensitive","label":"x"`, 1), false},
		{"tenant", "/create", "POST", strings.Replace(create, `"reason":"test"`, `"reason":"test","tenant":"one"`, 1), false},
		{"budget", "/create", "POST", strings.Replace(create, `"reason":"test"`, `"reason":"test","maxDepth":16`, 1), false},
		{"nullarray", "/create", "POST", strings.Replace(create, `["g"]`, `null`, 1), false},
		{"nestedmissing", "/create", "POST", strings.Replace(create, `"objectId":"u",`, ``, 1), false},
		{"nestedunknown", "/create", "POST", strings.Replace(create, `"objectId":"u"`, `"objectId":"u","actor":"2"`, 1), false},
		{"trailing", "/create", "POST", create + `{}`, false},
		{"revision", "/create", "POST", strings.Replace(create, `"expectedRevision":0`, `"expectedRevision":1`, 1), false},
		{"updatezero", "/update", "POST", create, false},
		{"reason", "/create", "POST", strings.Replace(create, `"reason":"test"`, `"reason":""`, 1), false},
		{"list", "/datasets?domainId=domain-a&offset=0&limit=100", "GET", "", true},
		{"overflow", "/datasets?domainId=domain-a&offset=0&limit=101", "GET", "", false},
		{"duplicatequery", "/datasets?domainId=domain-a&offset=0&limit=10&limit=10", "GET", "", false},
		{"noncanonical", "/datasets?domainId=domain-a&offset=00&limit=10", "GET", "", false},
		{"badutf8", "/datasets?domainId=%ff&offset=0&limit=10", "GET", "", false},
		{"filters", "/assets?domainId=domain-a&datasetId=f&evaluationId=e&kind=&name=&offset=0&limit=10", "GET", "", true},
		{"kind", "/assets?domainId=domain-a&datasetId=f&evaluationId=e&kind=other&name=&offset=0&limit=10", "GET", "", false},
		{"getbody", "/datasets?domainId=domain-a&offset=0&limit=10", "GET", "{}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, simulationLabelsPath+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			_, e := decodeSimulationLabelsInput(httptest.NewRecorder(), r, strings.Split(tc.path, "?")[0])
			if (e == nil) != tc.valid {
				t.Fatalf("valid%v err%v", tc.valid, e)
			}
		})
	}
	var obj map[string]any
	json.Unmarshal([]byte(create), &obj)
	spec := obj["spec"].(map[string]any)
	groups := make([]string, labels.MaxGroups+1)
	for i := range groups {
		groups[i] = "g"
	}
	spec["sensitiveGroupIds"] = groups
	b, _ := json.Marshal(obj)
	r := httptest.NewRequest("POST", simulationLabelsPath+"/create", strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	if _, e := decodeSimulationLabelsInput(httptest.NewRecorder(), r, "/create"); e == nil {
		t.Fatal("unbounded groups")
	}
}
func TestSimulationLabelsHandlerClosedBeforeDatabase(t *testing.T) {
	s := &Service{origin: "https://adtr.test"}
	for _, tc := range []struct {
		method, path, origin string
		enabled              bool
		want                 int
	}{{"GET", "/datasets?domainId=d&offset=0&limit=1", "", false, 404}, {"POST", "/datasets", "", true, 405}, {"POST", "/create", "https://evil.test", true, 403}, {"GET", "/seed", "", true, 404}, {"GET", "/datasets?domainId=d&offset=0&limit=1", "", true, 503}} {
		r := httptest.NewRequest(tc.method, simulationLabelsPath+tc.path, nil)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		s.SimulationLabelsHandler(tc.enabled).ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%+v got %d %s", tc, w.Code, w.Body)
		}
	}
}
func TestSimulationLabelsErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		e      error
		status int
		code   string
	}{{labels.ErrDenied, 403, "forbidden"}, {labels.ErrInvalid, 400, "invalid_input"}, {labels.ErrNotFound, 404, "not_found"}, {labels.ErrConflict, 409, "revision_conflict"}, {labels.ErrPageTooLarge, 422, "simulation_page_too_large"}, {labels.ErrLimit, 422, "simulation_limit_exceeded"}, {labels.ErrIncomplete, 409, "observation_incomplete"}, {labels.ErrIncompatible, 409, "observations_incompatible"}} {
		s, c := simulationLabelsHTTPError(tc.e)
		if s != tc.status || c != tc.code {
			t.Fatal(tc, s, c)
		}
	}
}
