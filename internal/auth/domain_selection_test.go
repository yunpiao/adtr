package auth

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/domains"
)

func TestDomainSelectionReadOnlyExactFunctionGrant(t *testing.T) {
	for _, grants := range []map[string]AccessAuth{
		nil,
		{"tasks": {Readable: true, Writeable: true}},
		{"operation_accounts": {Readable: true, Writeable: true}},
		{"domains": {Writeable: true}},
		{"domains": {Readable: true}},
	} {
		for _, path := range []string{"/api/domain-selection", "/api/domain-selection/resolve"} {
			if domainSelectionPathAllowed("GET", path, grants) != grants["domains"].Readable {
				t.Fatal("unexpected module grant", path, grants)
			}
			for _, method := range []string{"POST", "PUT", "DELETE", "HEAD", "OPTIONS"} {
				if domainSelectionPathAllowed(method, path, grants) {
					t.Fatal("selection method granted", method, path)
				}
			}
		}
	}
	for _, path := range []string{"/api/domain-selection/", "/api/domain-selection/unknown", "/api/domain-selection-other", "/api/domain-selection/resolve/", "/api/domain-selection/test"} {
		if domainSelectionPathAllowed("GET", path, map[string]AccessAuth{"domains": {Readable: true, Writeable: true}}) {
			t.Fatal("prefix authority", path)
		}
	}
}

func TestDomainSelectionHTTPStrictBeforeDatabase(t *testing.T) {
	s := &Service{}
	for _, test := range []struct {
		method, path string
		status       int
		code         string
	}{
		{"GET", "/api/domain-selection", 503, "domains_unavailable"},
		{"GET", "/api/domain-selection?pageSize=10&keyword=%25_", 503, "domains_unavailable"},
		{"GET", "/api/domain-selection/resolve?domainId=opaque-id&expectedRevision=1&expectedCredentialRevision=1", 503, "domains_unavailable"},
		{"GET", "/api/domain-selection?keyword=%zz", 400, "invalid_input"},
		{"GET", "/api/domain-selection?keyword=a;b", 400, "invalid_input"},
		{"GET", "/api/domain-selection?keyword=" + strings.Repeat("a", 32769), 400, "invalid_input"},
		{"GET", "/api/domain-selection?keyword=%FF", 400, "invalid_input"},
		{"GET", "/api/domain-selection?pageSize=20&pageSize=20", 400, "invalid_input"},
		{"GET", "/api/domain-selection?unknown=1", 400, "invalid_input"},
		{"GET", "/api/domain-selection?ModuleType=0", 422, "unsupported_source_filter"},
		{"GET", "/api/domain-selection/resolve", 400, "invalid_input"},
		{"GET", "/api/domain-selection/resolve?domainId=d&expectedRevision=01&expectedCredentialRevision=1", 400, "invalid_input"},
		{"POST", "/api/domain-selection", 405, "method_not_allowed"},
		{"POST", "/api/domain-selection/resolve", 405, "method_not_allowed"},
		{"HEAD", "/api/domain-selection", 405, "method_not_allowed"},
		{"GET", "/api/domain-selection-other", 404, "not_found"},
		{"GET", "/api/domain-selection/test", 404, "not_found"},
	} {
		r := httptest.NewRequest(test.method, test.path, nil)
		w := httptest.NewRecorder()
		s.DomainSelectionHandler(domains.New(nil)).ServeHTTP(w, r)
		var out map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Code != test.status || len(out) != 1 || out["error"] != test.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %s", test.method, test.path, w.Code, w.Body)
		}
		if w.Header().Get("X-ADTR-User-ID") != "" {
			t.Fatal("failure response exposed an actor binding")
		}
		if test.status == 405 && w.Header().Get("Allow") != "GET" {
			t.Fatal("incorrect allowed methods")
		}
	}
}
