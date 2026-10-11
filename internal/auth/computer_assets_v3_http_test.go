package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestComputerAssetsV3ExactPermissionIntersection(t *testing.T) {
	for mask := 0; mask < 64; mask++ {
		g := map[string]AccessAuth{"domains": {Readable: mask&1 != 0, Writeable: mask&2 != 0}, "directory_assets": {Readable: mask&4 != 0, Writeable: mask&8 != 0}, "tasks": {Readable: mask&16 != 0, Writeable: mask&32 != 0}}
		if computerAssetsV3PathAllowed("GET", computerAssetsV3Path, g) != (mask&1 != 0 && mask&4 != 0) {
			t.Fatal(mask)
		}
		for _, m := range []string{"POST", "HEAD", "PUT", "DELETE"} {
			if computerAssetsV3PathAllowed(m, computerAssetsV3Path, g) {
				t.Fatal(m)
			}
		}
		for _, p := range []string{computerAssetsV3Path + "/detail", computerAssetsV3Path + "/", userAssetsV2Path, "/api/directory/v3/observation"} {
			if computerAssetsV3PathAllowed("GET", p, g) {
				t.Fatal(p)
			}
		}
	}
	nodes := permissionNodes(map[string]AccessAuth{"directory_assets": {Readable: true}}, true)
	found := false
	for _, n := range nodes {
		for _, p := range n.Paths {
			if p.URL == "GET "+computerAssetsV3Path && p.Auth == "readable" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing permission catalog")
	}
}
func TestComputerAssetsV3QueryClosedContract(t *testing.T) {
	for _, extra := range []string{"", "&search=", "&search=%00", "&pageSize=50", "&observationId=obs&pageIdx=2", "&startTm=0001-01-01T00:00:00Z&endTm=2026-01-01T00:00:00Z&creatTm=desc"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery + extra)
		if _, e := computerAssetsV3Query(q); e != nil {
			t.Fatal(extra, e)
		}
	}
	for _, extra := range []string{"&sAMAccountName=x", "&kind=computer", "&objectGUID=x", "&pageIdx=2", "&pageIdx=01", "&pageSize=-1", "&pageSize=100", "&search=a&search=b", "&search=%FF", "&search=" + strings.Repeat("a", 51), "&startTm=2026-01-01T00:00:00Z", "&creatTm=1", "&creatTm=", "&tenantId=x", "&computerType=x&unknown=x", "&computerType=x&pageIdx=01", "&computerType=x&creatTm=1"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery + extra)
		_, e := computerAssetsV3Query(q)
		status, code := domainHTTPError(e)
		if status != 400 || code != "invalid_input" {
			t.Fatal(extra, status, code)
		}
	}
	for _, key := range []string{"computerStatus", "domainName", "updateTm", "computerType", "overviewType"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery + "&" + key + "=x")
		_, e := computerAssetsV3Query(q)
		status, code := domainHTTPError(e)
		if status != 422 || code != "unsupported_computer_asset_filter" {
			t.Fatal(key, status, code)
		}
	}
	for _, key := range []string{"domainId", "expectedRevision", "expectedCredentialRevision"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery)
		q.Del(key)
		_, e := computerAssetsV3Query(q)
		if status, _ := domainHTTPError(e); status != 400 {
			t.Fatal(key, e)
		}
	}
}
func TestComputerAssetsV3HTTPBoundary(t *testing.T) {
	s := &Service{}
	mux := http.NewServeMux()
	handler := s.ComputerAssetsV3Handler(nil)
	mux.Handle(computerAssetsV3Path, handler)
	mux.Handle(computerAssetsV3Path+"/", handler)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"GET", computerAssetsV3Path + "?" + userAssetsV2ValidQuery, "", 503}, {"POST", computerAssetsV3Path, "", 405}, {"GET", computerAssetsV3Path + "/detail", "", 404}, {"GET", computerAssetsV3Path + "?" + userAssetsV2ValidQuery, "{}", 400}, {"GET", computerAssetsV3Path + "?" + userAssetsV2ValidQuery + "&computerStatus=0", "", 422}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		mux.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET" {
			t.Fatal("missing Allow")
		}
	}
}
