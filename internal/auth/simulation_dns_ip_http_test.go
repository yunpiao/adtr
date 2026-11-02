package auth

import (
	"context"
	"errors"
	"github.com/yunpiao/adtr/internal/simulationdnsip"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSimulationDNSIPRouteAuthority(t *testing.T) {
	for _, read := range []bool{false, true} {
		for _, writeable := range []bool{false, true} {
			grants := map[string]AccessAuth{"simulation_dns_ip": {Readable: read, Writeable: writeable}, "tasks": {Readable: true, Writeable: true}, "simulation_detection": {Readable: true, Writeable: true}}
			for path, rt := range simulationDNSIPRoutes {
				want := read && (!rt.write || writeable)
				if simulationDNSIPPathAllowed(rt.method, simulationDNSIPPath+path, grants) != want {
					t.Fatal(path, read, writeable)
				}
				if simulationDNSIPPathAllowed("PATCH", simulationDNSIPPath+path, grants) {
					t.Fatal("method")
				}
			}
		}
	}
	if simulationDNSIPPathAllowed("GET", simulationDNSIPPath+"/seed", map[string]AccessAuth{"simulation_dns_ip": {Readable: true, Writeable: true}}) {
		t.Fatal("untrusted seed exposed")
	}
	if simulationDNSIPPathAllowed("GET", simulationDNSIPPath+"/observations", map[string]AccessAuth{"tasks": {Readable: true, Writeable: true}}) {
		t.Fatal("tasks bypass")
	}
}
func TestSimulationDNSIPStrictInputs(t *testing.T) {
	validExport := `{"domainId":"domain-a","observationId":"fixed","observationHash":"` + strings.Repeat("a", 64) + `","keyword":"","family":"","operationId":"export-op"}`
	tests := []struct {
		path, body string
		good       bool
	}{
		{"/observations?domainId=domain-a", "", true},
		{"/records?domainId=domain-a&observationId=fixed&keyword=&family=&offset=0&limit=20", "", true},
		{"/records?domainId=domain-a&observationId=fixed&keyword=192.0.2&family=ipv4&offset=10&limit=100", "", true},
		{"/records?domainId=domain-a&observationId=fixed&keyword=&family=&offset=00&limit=20", "", false},
		{"/records?domainId=domain-a&observationId=fixed&keyword=&family=&offset=0&limit=101", "", false},
		{"/records?domainId=domain-a&observationId=fixed&keyword=%0a&family=&offset=0&limit=20", "", false},
		{"/records?domainId=domain-a&observationId=fixed&keyword=&family=all&offset=0&limit=20", "", false},
		{"/observations?domainId=platform", "", false},
		{"/observations?domainId=domain-a&domainId=domain-b", "", false},
		{"/observations?domainId=domain-a&dnsDomain=evil.test", "", false},
		{"/export", validExport, true},
		{"/export", strings.Replace(validExport, `"keyword":""`, `"keyword":null`, 1), false},
		{"/export", strings.Replace(validExport, `"family":""`, `"family":"ipv4","family":"ipv6"`, 1), false},
		{"/export", strings.Replace(validExport, `"observationHash":"`+strings.Repeat("a", 64)+`"`, `"observationHash":"`+strings.Repeat("A", 64)+`"`, 1), false},
		{"/export", strings.TrimSuffix(validExport, "}") + `,"records":[]}`, false},
		{"/export", validExport + `{}`, false},
		{"/export?extra=x", validExport, false},
		{"/cancel", `{"domainId":"domain-a","taskId":"task-id","operationId":"cancel-op"}`, true},
		{"/download?domainId=domain-a&taskId=task-id", "", true},
	}
	for _, tt := range tests {
		method := "GET"
		if tt.body != "" {
			method = "POST"
		}
		r := httptest.NewRequest(method, simulationDNSIPPath+tt.path, strings.NewReader(tt.body))
		r.Header.Set("Content-Type", "application/json")
		path := strings.Split(tt.path, "?")[0]
		_, e := decodeSimulationDNSIPInput(httptest.NewRecorder(), r, path)
		if (e == nil) != tt.good {
			t.Fatalf("%s body%s good%v err%v", tt.path, tt.body, tt.good, e)
		}
	}
}
func TestSimulationDNSIPHandlerFailsClosed(t *testing.T) {
	s := Service{origin: "https://example.test"}
	for _, tt := range []struct {
		method, path, body string
		enabled            bool
		want               int
	}{
		{"GET", "/observations?domainId=domain-a", "", false, 404},
		{"GET", "/observations?domainId=domain-a", "", true, 503},
		{"PATCH", "/observations?domainId=domain-a", "", true, 405},
		{"POST", "/seed", `{}`, true, 404},
		{"POST", "/cancel", `{}`, true, 403},
	} {
		r := httptest.NewRequest(tt.method, simulationDNSIPPath+tt.path, strings.NewReader(tt.body))
		w := httptest.NewRecorder()
		s.SimulationDNSIPHandler(tt.enabled, nil).ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatal(tt, w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("unsafe headers")
		}
	}
}
func TestSimulationDNSIPAuthorizerBoundaries(t *testing.T) {
	p := tasks.Principal{TenantID: "one", ActorID: 1}
	s := tasks.Scope{TaskName: simulationdnsip.KindName, DomainID: "domain-a"}
	for _, flags := range [][3]bool{{false, false, false}, {true, true, false}} {
		a := NewSimulationDNSIPTaskAuthorizer(flags[0], flags[1], flags[2])
		if _, e := a(context.Background(), nil, p, s, tasks.Execute); !errors.Is(e, tasks.ErrAuthorization) {
			t.Fatal(e)
		}
	}
	for _, action := range []tasks.Action{tasks.Read, tasks.Write, tasks.Execute} {
		if _, e := simulationDNSIPTaskAuthorizer(nil)(context.Background(), nil, p, s, action); !errors.Is(e, tasks.ErrAuthorization) {
			t.Fatal("nil tx authorized", e)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{simulationdnsip.ErrDenied, 403}, {simulationdnsip.ErrNotFound, 404}, {simulationdnsip.ErrConflict, 409}, {simulationdnsip.ErrInvalid, 400}, {simulationdnsip.ErrLimit, 422}, {context.DeadlineExceeded, 503}} {
		status, _ := simulationDNSIPHTTPError(tc.err)
		if status != tc.status {
			t.Fatal(status, tc.status)
		}
	}
}
