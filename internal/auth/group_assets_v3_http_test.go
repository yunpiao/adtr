package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGroupAssetsV3ExactPermissionIntersection(t *testing.T) {
	for mask := 0; mask < 64; mask++ {
		g := map[string]AccessAuth{"domains": {Readable: mask&1 != 0, Writeable: mask&2 != 0}, "directory_assets": {Readable: mask&4 != 0, Writeable: mask&8 != 0}, "tasks": {Readable: mask&16 != 0, Writeable: mask&32 != 0}}
		if groupAssetsV3PathAllowed("GET", groupAssetsV3Path, g) != (mask&1 != 0 && mask&4 != 0) {
			t.Fatal(mask)
		}
		for _, m := range []string{"POST", "HEAD", "PUT", "DELETE"} {
			if groupAssetsV3PathAllowed(m, groupAssetsV3Path, g) {
				t.Fatal(m)
			}
		}
		for _, p := range []string{groupAssetsV3Path + "/detail", groupAssetsV3Path + "/", userAssetsV2Path, "/api/directory/v3/observation"} {
			if groupAssetsV3PathAllowed("GET", p, g) {
				t.Fatal(p)
			}
		}
	}
	nodes := permissionNodes(map[string]AccessAuth{"directory_assets": {Readable: true}}, true)
	found := false
	for _, n := range nodes {
		for _, p := range n.Paths {
			if p.URL == "GET "+groupAssetsV3Path && p.Auth == "readable" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing permission catalog")
	}
}
func TestGroupAssetsV3QueryClosedContract(t *testing.T) {
	for _, extra := range []string{"", "&search=", "&search=%00", "&pageSize=50", "&observationId=obs&pageIdx=2", "&creatTm=desc"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery + extra)
		if _, e := groupAssetsV3Query(q); e != nil {
			t.Fatal(extra, e)
		}
	}
	for _, extra := range []string{"&sAMAccountName=x", "&kind=group", "&objectGUID=x", "&pageIdx=2", "&pageIdx=01", "&pageSize=-1", "&pageSize=100", "&search=a&search=b", "&search=%FF", "&search=" + strings.Repeat("a", 21), "&creatTm=1", "&creatTm=", "&tenantId=x", "&groupType=x&unknown=x", "&groupType=x&pageIdx=01", "&groupType=x&creatTm=1"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery + extra)
		_, e := groupAssetsV3Query(q)
		status, code := domainHTTPError(e)
		if status != 400 || code != "invalid_input" {
			t.Fatal(extra, status, code)
		}
	}
	for _, key := range []string{"status", "domainName", "groupMold", "groupScope", "groupType", "startTm", "endTm", "updateTm", "overviewType"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery + "&" + key + "=x")
		_, e := groupAssetsV3Query(q)
		status, code := domainHTTPError(e)
		if status != 422 || code != "unsupported_group_asset_filter" {
			t.Fatal(key, status, code)
		}
	}
	for _, key := range []string{"domainId", "expectedRevision", "expectedCredentialRevision"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery)
		q.Del(key)
		_, e := groupAssetsV3Query(q)
		if status, _ := domainHTTPError(e); status != 400 {
			t.Fatal(key, e)
		}
	}
}
func TestGroupAssetsV3HTTPBoundary(t *testing.T) {
	s := &Service{}
	mux := http.NewServeMux()
	handler := s.GroupAssetsV3Handler(nil)
	mux.Handle(groupAssetsV3Path, handler)
	mux.Handle(groupAssetsV3Path+"/", handler)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"GET", groupAssetsV3Path + "?" + userAssetsV2ValidQuery, "", 503}, {"POST", groupAssetsV3Path, "", 405}, {"GET", groupAssetsV3Path + "/detail", "", 404}, {"GET", groupAssetsV3Path + "?" + userAssetsV2ValidQuery, "{}", 400}, {"GET", groupAssetsV3Path + "?" + userAssetsV2ValidQuery + "&groupMold=0", "", 422}} {
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
