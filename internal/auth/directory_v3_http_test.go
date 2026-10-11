package auth

import (
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectoryV3RoutesRequireExactPurposeAndPermissions(t *testing.T) {
	grants := map[string]AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true}}
	if !directoryV3PathAllowed("GET", "/api/directory/v3/observation", grants) || directoryV3PathAllowed("POST", "/api/directory/v3/sync", grants) {
		t.Fatal("reader permissions widened")
	}
	if !userAssetV3PathAllowed("GET", "/api/user-assets/v3/detail", grants) || userAssetV3PathAllowed("GET", "/api/user-assets/v2/detail", grants) || userAssetV3PathAllowed("GET", "/api/user-assets/v3", grants) {
		t.Fatal("detail route widened")
	}
	for _, mark := range []string{"domains", "directory_assets"} {
		old := grants[mark]
		delete(grants, mark)
		if userAssetV3PathAllowed("GET", userAssetV3Path, grants) {
			t.Fatal("missing reader permission allowed")
		}
		grants[mark] = old
	}
	prefix, enabled := credentialUseEndpointForPurpose(credentialuse.DirectoryV3Purpose)
	if prefix != "/api/directory-credential-use/v3" || !enabled {
		t.Fatal("compiled capability missing")
	}
	if !isDirectoryTaskKind(domains.DirectoryV3KindName) || dedicatedTaskRouteCode(domains.DirectoryV3KindName) != "directory_route_required" {
		t.Fatal("generic task controls widened")
	}
	for _, path := range []string{"/api/directory/v2/observation", "/api/directory/v3/observation/", "/api/directory/v3/unknown"} {
		if directoryV3PathAllowed("GET", path, grants) {
			t.Fatal("unknown path accepted")
		}
	}
}
func TestDirectoryV3RejectsAmbiguousMetadataBeforeDatabase(t *testing.T) {
	s := &Service{origin: "http://example.test"}
	for _, query := range []string{"dictionaryVersion=3", "purpose=domain.directory_read.v3", "pageSize=25&pageSize=50", "unknown=x"} {
		w := httptest.NewRecorder()
		s.DirectoryV3Handler(nil, nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/directory/v3/observation?domainId=domain-a&"+query, nil))
		if w.Code != 400 {
			t.Fatal("caller profile reached database", w.Code)
		}
	}
	for _, body := range []string{`{"domainId":"a","dictionaryVersion":3}`, `{"purpose":"domain.directory_read.v2"}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/directory/v3/sync", strings.NewReader(body))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		s.DirectoryV3Handler(nil, nil).ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("unknown body reached database", w.Code)
		}
	}
	for _, query := range []string{"", "domainId=a", "domainId=a&objectGUID=bad", "domainId=a&observationId=old&expectedRevision=1&expectedCredentialRevision=1&objectGUID=00000000-0000-0000-0000-000000000000&search=x"} {
		w := httptest.NewRecorder()
		s.UserAssetV3Handler(nil).ServeHTTP(w, httptest.NewRequest("GET", userAssetV3Path+"?"+query, nil))
		if w.Code != 400 {
			t.Fatal("invalid detail reached database", w.Code)
		}
	}
}
