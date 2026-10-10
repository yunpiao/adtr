package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const userAssetsV2ValidQuery = "domainId=domain-a&expectedRevision=1&expectedCredentialRevision=2"
const userAssetV2GUID = "00000000-0000-0000-0000-000000000001"

func TestUserAssetsV2ExactReadPermissionIntersection(t *testing.T) {
	for mask := 0; mask < 64; mask++ {
		grants := map[string]AccessAuth{"domains": {Readable: mask&1 != 0, Writeable: mask&2 != 0}, "directory_assets": {Readable: mask&4 != 0, Writeable: mask&8 != 0}, "tasks": {Readable: mask&16 != 0, Writeable: mask&32 != 0}, "operation_accounts": {Readable: true, Writeable: true}}
		for _, path := range []string{userAssetsV2Path, userAssetsV2Path + "/detail"} {
			if got, want := userAssetsV2PathAllowed("GET", path, grants), mask&1 != 0 && mask&4 != 0; got != want {
				t.Fatalf("mask=%d path=%s got=%v want=%v", mask, path, got, want)
			}
			for _, method := range []string{"POST", "PUT", "DELETE", "HEAD", "OPTIONS", "get"} {
				if userAssetsV2PathAllowed(method, path, grants) {
					t.Fatal("non-GET permission", method)
				}
			}
		}
		for _, path := range []string{"/api/user-assets", userAssetsV2Path + "/", userAssetsV2Path + "/detail/", userAssetsV2Path + "/sync", userAssetsV2Path + "/v2/detail", "/api/directory/v2/observation", userAssetsV2Path + "?domainId=x"} {
			if userAssetsV2PathAllowed("GET", path, grants) {
				t.Fatal("nonexact permission", path)
			}
		}
	}
	nodes := permissionNodes(map[string]AccessAuth{"directory_assets": {Readable: true}}, true)
	seen := map[string]string{}
	for _, node := range nodes {
		for _, path := range node.Paths {
			seen[path.URL] = path.Auth
		}
	}
	if seen["GET "+userAssetsV2Path] != "readable" || seen["GET "+userAssetsV2Path+"/detail"] != "readable" {
		t.Fatal("read route catalog missing", seen)
	}
}

func TestUserAssetsV2QueryStrictBoundedLiteralContract(t *testing.T) {
	parse := func(raw string, detail bool) (int, string) {
		q, e := url.ParseQuery(raw)
		if e != nil {
			return 400, "invalid_input"
		}
		_, _, e = userAssetsV2Query(detail, q)
		if e == nil {
			return 200, ""
		}
		return domainHTTPError(e)
	}
	for _, extra := range []string{"", "&search=", "&search=%00%2B%26%25%5F%2A%3F%27%22%5C%28%29%5B%5D", "&pageSize=10", "&pageSize=20", "&pageSize=30", "&pageSize=40", "&pageSize=50", "&pageIdx=10000&observationId=obs-a", "&search=" + url.QueryEscape(strings.Repeat("😀", 25))} {
		if status, _ := parse(userAssetsV2ValidQuery+extra, false); status != 200 {
			t.Fatal("valid literal query rejected", extra, status)
		}
	}
	for _, extra := range []string{"&domainId=other", "&pageIdx=2", "&pageIdx=0", "&pageIdx=-1", "&pageIdx=01", "&pageIdx=%2B1", "&pageIdx=1.0", "&pageIdx=10001", "&pageIdx=999999999999999999999999", "&pageSize=0", "&pageSize=-1", "&pageSize=25", "&pageSize=100", "&pageSize=010", "&pageSize=10&pageSize=20", "&pageSize=", "&observationId=", "&search=%FF", "&search=%ED%A0%80", "&search=%ZZ", "&search=a&search=b", "&search=" + strings.Repeat("a", 51), "&search=" + url.QueryEscape(strings.Repeat("😀", 26)), "&kind=user", "&objectGUID=" + userAssetV2GUID, "&actorId=1", "&dictionaryVersion=2", "&expectedCredentialGeneration=2"} {
		if status, code := parse(userAssetsV2ValidQuery+extra, false); status != 400 || code != "invalid_input" {
			t.Fatal("invalid query accepted", extra, status, code)
		}
	}
	for _, key := range []string{"domainId", "expectedRevision", "expectedCredentialRevision"} {
		q, _ := url.ParseQuery(userAssetsV2ValidQuery)
		q.Del(key)
		if status, _ := parse(q.Encode(), false); status != 400 {
			t.Fatal("missing required", key)
		}
		for _, v := range []string{"", "0", "-1", "01", "+1", "9223372036854775808"} {
			if key == "domainId" && v != "" {
				continue
			}
			q.Set(key, v)
			if status, _ := parse(q.Encode(), false); status != 400 {
				t.Fatal("invalid source pin", key, v)
			}
		}
	}
	for _, key := range []string{"userStatus", "domainName", "startTm", "endTm", "updateTm", "creatTm", "userType", "sAMAccountName", "scoreSort", "overviewType"} {
		if status, code := parse(userAssetsV2ValidQuery+"&"+key+"=x", false); status != 422 || code != "unsupported_user_asset_filter" {
			t.Fatal("deferred filter not explicit", key, status, code)
		}
		for _, bad := range []string{"&" + key + "=x&" + key + "=y", "&" + key + "=%FF", "&" + key + "=x&unexpected=x"} {
			if status, _ := parse(userAssetsV2ValidQuery+bad, false); status != 400 {
				t.Fatal("malformed structure hidden by deferred filter", bad)
			}
		}
	}
	q, _ := url.ParseQuery(userAssetsV2ValidQuery + "&search=" + url.QueryEscape(" Raw\x00😀+%_\\ "))
	f, _, err := userAssetsV2Query(false, q)
	if err != nil || f.Search != " Raw\x00😀+%_\\ " || f.PageIdx != 1 || f.PageSize != 10 || f.ExpectedCredentialRevision != "2" {
		t.Fatal("query bytes/defaults changed", f, err)
	}
}

func TestUserAssetV2DetailQueryRequiresEveryExactIdentity(t *testing.T) {
	base, _ := url.ParseQuery(userAssetsV2ValidQuery + "&observationId=obs-a&objectGUID=" + userAssetV2GUID)
	_, in, err := userAssetsV2Query(true, base)
	if err != nil || in.ObjectGUID != userAssetV2GUID || in.ObservationID != "obs-a" {
		t.Fatal("valid detail", in, err)
	}
	for _, key := range []string{"domainId", "expectedRevision", "expectedCredentialRevision", "observationId", "objectGUID"} {
		q, _ := url.ParseQuery(base.Encode())
		q.Del(key)
		if _, _, err := userAssetsV2Query(true, q); err == nil {
			t.Fatal("missing detail identity", key)
		}
	}
	for key, value := range map[string]string{"objectGUID": "AAAAAAAA-0000-0000-0000-000000000001", "search": "", "pageIdx": "1", "pageSize": "10", "kind": "user", "userStatus": "1"} {
		q, _ := url.ParseQuery(base.Encode())
		q.Set(key, value)
		if _, _, err := userAssetsV2Query(true, q); err == nil {
			t.Fatal("invalid detail query", key)
		}
	}
}

func TestUserAssetsV2RoutesAndInputsPrecedeAnyDatabaseRead(t *testing.T) {
	handler := (&Service{}).UserAssetsV2Handler(nil)
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", userAssetsV2Path + "?" + userAssetsV2ValidQuery, 503},
		{"GET", userAssetsV2Path + "/detail?" + userAssetsV2ValidQuery + "&observationId=obs-a&objectGUID=" + userAssetV2GUID, 503},
		{"GET", userAssetsV2Path, 400}, {"GET", userAssetsV2Path + "?" + userAssetsV2ValidQuery + "&search=%zz", 400},
		{"GET", userAssetsV2Path + "?" + userAssetsV2ValidQuery + "&search=" + strings.Repeat("a", 32768), 400},
		{"GET", userAssetsV2Path + "?" + userAssetsV2ValidQuery + "&userStatus=0", 422},
		{"GET", userAssetsV2Path + "/", 404}, {"GET", userAssetsV2Path + "/detail/", 404}, {"POST", userAssetsV2Path, 405}, {"HEAD", userAssetsV2Path + "/detail", 405}, {"POST", userAssetsV2Path + "/sync", 404},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != c.want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-ADTR-User-ID") != "" {
			t.Fatal(c, w.Code, w.Header(), w.Body.String())
		}
		if c.want == 405 && !reflect.DeepEqual(w.Header().Values("Allow"), []string{http.MethodGet}) {
			t.Fatal("wrong Allow", w.Header())
		}
	}
}

func TestUserAssetsV2RejectsGetBodies(t *testing.T) {
	for _, path := range []string{userAssetsV2Path + "?" + userAssetsV2ValidQuery, userAssetsV2Path + "/detail?" + userAssetsV2ValidQuery + "&observationId=obs-a&objectGUID=" + userAssetV2GUID} {
		for _, length := range []int64{2, -1} {
			r := httptest.NewRequest("GET", path, strings.NewReader("{}"))
			r.ContentLength = length
			w := httptest.NewRecorder()
			(&Service{}).UserAssetsV2Handler(nil).ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatal("GET body accepted", path, length, w.Code)
			}
		}
	}
}
