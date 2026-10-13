package auth

import (
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGroupMembersV4FixedReaderRouteAndFivePins(t *testing.T) {
	grants := map[string]AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true}}
	if !groupMembersV4PathAllowed("GET", groupMembersV4Path, grants) {
		t.Fatal("read-only route denied")
	}
	for _, key := range []string{"domains", "directory_assets"} {
		old := grants[key]
		delete(grants, key)
		if groupMembersV4PathAllowed("GET", groupMembersV4Path, grants) {
			t.Fatal("missing authority accepted")
		}
		grants[key] = old
	}
	for _, path := range []string{"/api/group-assets/v3", groupMembersV4Path + "/", "/api/group-assets/v4", "/api/group-assets/v4/memberOf"} {
		if groupMembersV4PathAllowed("GET", path, grants) {
			t.Fatal("route widened")
		}
	}
	if groupMembersV4PathAllowed("POST", groupMembersV4Path, grants) {
		t.Fatal("read route mutated")
	}
	q := url.Values{"domainId": {"domain-a"}, "expectedRevision": {"1"}, "expectedCredentialRevision": {"1"}, "observationId": {"observation-a"}, "objectGUID": {"00000000-0000-0000-0000-000000000000"}}
	if _, err := groupMembersV4Query(q); err != nil {
		t.Fatal("valid pins", err)
	}
	for key := range q {
		clone := url.Values{}
		for k, v := range q {
			clone[k] = append([]string(nil), v...)
		}
		delete(clone, key)
		if _, err := groupMembersV4Query(clone); err == nil {
			t.Fatal("missing required pin", key)
		}
	}
	for _, extra := range []string{"dictionaryVersion=4", "memberCount=0", "memberOf=x", "search=x", "pageSize=10", "purpose=domain.directory_read.v4", "tenant=other", "domainId=other", "expectedRevision=01", "objectGUID=ABCDEF00-0000-0000-0000-000000000000"} {
		bad, _ := url.ParseQuery(q.Encode() + "&" + extra)
		if _, err := groupMembersV4Query(bad); err == nil {
			t.Fatal("ambiguous/unsupported input", extra)
		}
	}
	s := &Service{}
	for _, input := range []struct {
		method, path, body string
		want               int
	}{{"GET", groupMembersV4Path + "?" + q.Encode(), "", 503}, {"GET", groupMembersV4Path + "?" + q.Encode(), "x", 400}, {"POST", groupMembersV4Path, "", 405}, {"GET", groupMembersV4Path + "/", "", 404}, {"GET", groupMembersV4Path + "?domainId=a", "", 400}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(input.method, input.path, strings.NewReader(input.body))
		s.GroupMembersV4Handler(nil).ServeHTTP(w, r)
		if w.Code != input.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("boundary %s: %d", input.path, w.Code)
		}
	}
}
func TestDirectoryV4ExactPurposeAndRoutes(t *testing.T) {
	grants := map[string]AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true}}
	if !directoryV4PathAllowed(http.MethodGet, "/api/directory/v4/observation", grants) || directoryV4PathAllowed(http.MethodPost, "/api/directory/v4/sync", grants) {
		t.Fatal("reader permissions widened")
	}
	for _, path := range []string{"/api/directory/v3/observation", "/api/directory/v4/observation/", "/api/directory/v4/member"} {
		if directoryV4PathAllowed(http.MethodGet, path, grants) {
			t.Fatal("route widened")
		}
	}
	prefix, enabled := credentialUseEndpointForPurpose(credentialuse.DirectoryV4Purpose)
	if prefix != "/api/directory-credential-use/v4" || !enabled {
		t.Fatal("separate compiled capability missing")
	}
	if !isDirectoryTaskKind(domains.DirectoryV4KindName) || dedicatedTaskRouteCode(domains.DirectoryV4KindName) != "directory_route_required" {
		t.Fatal("generic route bypass")
	}
	s := &Service{origin: "http://example.test"}
	for _, query := range []string{"dictionaryVersion=4", "purpose=domain.directory_read.v4", "pageSize=25&pageSize=50", "unknown=x"} {
		w := httptest.NewRecorder()
		s.DirectoryV4Handler(nil, nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/directory/v4/observation?domainId=domain-a&"+query, nil))
		if w.Code != 400 {
			t.Fatal("caller profile reached database", w.Code)
		}
	}
}
