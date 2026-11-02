package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/simulationdashboard"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestSimulationDashboardRequiresAllThreeReadGrants(t *testing.T) {
	marks := []string{"simulation_events", "simulation_risk", "simulation_detection"}
	for mask := 0; mask < 8; mask++ {
		grants := map[string]AccessAuth{"tasks": {Readable: true, Writeable: true}, "system": {Readable: true, Writeable: true}}
		for i, m := range marks {
			grants[m] = AccessAuth{Readable: mask&(1<<i) != 0, Writeable: true}
		}
		if got := simulationDashboardPathAllowed("GET", simulationDashboardPath+"/summary", grants); got != (mask == 7) {
			t.Fatalf("mask %d got %v", mask, got)
		}
		for _, path := range []string{simulationDashboardPath, simulationDashboardPath + "/summary/", simulationDashboardPath + "/unknown"} {
			if simulationDashboardPathAllowed("GET", path, grants) {
				t.Fatal("unknown route", path)
			}
		}
		for _, method := range []string{"HEAD", "POST", "DELETE", "PUT"} {
			if simulationDashboardPathAllowed(method, simulationDashboardPath+"/summary", grants) {
				t.Fatal("unknown method", method)
			}
		}
	}
	for _, role := range []string{"platform_admin", "viewer"} {
		grants, err := loadAccessGrants(context.Background(), nil, "test", role)
		if err != nil || simulationDashboardPathAllowed("GET", simulationDashboardPath+"/summary", grants) {
			t.Fatalf("builtin bypass %s %v", role, err)
		}
	}
}

func TestSimulationDashboardClosedQuery(t *testing.T) {
	valid := "domainId=domain-a&datasetId=dataset-a&window=24h"
	cases := []struct {
		query string
		ok    bool
	}{
		{valid, true}, {valid + "&subject=synthetic-user", true}, {strings.Replace(valid, "24h", "1h", 1), true}, {strings.Replace(valid, "24h", "7d", 1), true},
		{"domainId=d&datasetId=f", false}, {"datasetId=f&window=24h", false}, {"domainId=d&window=24h", false},
		{valid + "&window=1h", false}, {valid + "&domainId=other", false}, {valid + "&datasetId=other", false}, {valid + "&subject=a&subject=b", false},
		{valid + "&subject=", false}, {valid + "&subject=%20a", false}, {valid + "&subject=a%20", false}, {valid + "&subject=%00", false}, {valid + "&subject=%FF", false}, {valid + "&subject=" + strings.Repeat("a", 129), false},
		{strings.Replace(valid, "24h", "0", 1), false}, {strings.Replace(valid, "24h", "24H", 1), false}, {strings.Replace(valid, "domain-a", "null", 1), true},
		{strings.Replace(valid, "domain-a", "domain%2Fa", 1), false}, {valid + ";x=1", false}, {valid + "&bad=%zz", false}, {strings.Repeat("a", 2049), false},
	}
	for _, k := range []string{"tenant", "actorId", "permission", "authorizationVersion", "asOf", "severity", "score", "DomainId", "Window", "offset", "limit", "upload"} {
		cases = append(cases, struct {
			query string
			ok    bool
		}{valid + "&" + k + "=forged", false})
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", simulationDashboardPath+"/summary?"+tc.query, nil)
		_, err := decodeSimulationDashboardInput(r)
		if (err == nil) != tc.ok {
			t.Fatalf("query %q: %v", tc.query, err)
		}
	}
	for _, setup := range []func(*http.Request){func(r *http.Request) { r.ContentLength = 1 }, func(r *http.Request) { r.ContentLength = -1 }, func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }} {
		r := httptest.NewRequest("GET", simulationDashboardPath+"/summary?"+valid, nil)
		setup(r)
		if _, err := decodeSimulationDashboardInput(r); err == nil {
			t.Fatal("GET body admitted")
		}
	}
}

func TestSimulationDashboardFeatureGatesAndFailClosed(t *testing.T) {
	s := &Service{}
	for mask := 0; mask < 16; mask++ {
		h := s.SimulationDashboardHandler(mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0)
		r := httptest.NewRequest("GET", simulationDashboardPath+"/summary?domainId=d&datasetId=f&window=24h", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 404
		if mask == 15 {
			want = 503
		}
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-ADTR-User-ID") != "" {
			t.Fatalf("flag mask %d: %d %s", mask, w.Code, w.Body)
		}
	}
	h := s.SimulationDashboardHandler(true, true, true, true)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", simulationDashboardPath + "/unknown", "", 404},
		{"POST", simulationDashboardPath + "/summary", "", 405},
		{"HEAD", simulationDashboardPath + "/summary", "", 405},
		{"GET", simulationDashboardPath + "/summary?domainId=d&datasetId=f&window=24h", "payload", 400},
		{"GET", simulationDashboardPath + "/summary?domainId=d&datasetId=f&window=24h&actorId=1", "", 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET" {
			t.Fatal("missing Allow")
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{simulationdashboard.ErrNotFound, 404, "not_found"}, {simulationdashboard.ErrDenied, 403, "forbidden"}, {tasks.ErrAuthorization, 403, "forbidden"}, {simulationdashboard.ErrLimit, 422, "simulation_limit_exceeded"},
		{simulationdashboard.ErrInvalid, 503, "simulation_unavailable"}, {simulationdashboard.ErrConflict, 503, "simulation_unavailable"}, {context.Canceled, 503, "simulation_unavailable"}, {errors.New("secret database detail"), 503, "simulation_unavailable"}, {fail(401, "unauthenticated"), 401, "unauthenticated"},
	} {
		status, code := simulationDashboardHTTPError(tc.err)
		if status != tc.status || code != tc.code {
			t.Fatal(status, code)
		}
	}
}
