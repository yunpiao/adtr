package auth

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRawDNReferencesV4FixedReaderRouteAndSevenKeys(t *testing.T) {
	grants := map[string]AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true}}
	if !rawDNReferencesV4PathAllowed("GET", rawDNReferencesV4Path, grants) {
		t.Fatal("read-only route denied")
	}
	for _, key := range []string{"domains", "directory_assets"} {
		old := grants[key]
		delete(grants, key)
		if rawDNReferencesV4PathAllowed("GET", rawDNReferencesV4Path, grants) {
			t.Fatal("missing authority accepted")
		}
		grants[key] = old
	}
	for _, path := range []string{"/api/group-assets/v3", rawDNReferencesV4Path + "/", "/api/group-assets/v4", "/api/group-assets/v4/memberOf"} {
		if rawDNReferencesV4PathAllowed("GET", path, grants) {
			t.Fatal("route widened")
		}
	}
	if rawDNReferencesV4PathAllowed("POST", rawDNReferencesV4Path, grants) {
		t.Fatal("read route mutated")
	}
	q := url.Values{"pageIdx": {"1"}, "pageSize": {"25"}, "domainId": {"domain-a"}, "expectedRevision": {"1"}, "expectedCredentialRevision": {"1"}, "observationId": {"observation-a"}, "objectGUID": {"00000000-0000-0000-0000-000000000000"}}
	if _, err := rawDNReferencesV4Query(q); err != nil {
		t.Fatal("valid pins", err)
	}
	for key := range q {
		clone := url.Values{}
		for k, v := range q {
			clone[k] = append([]string(nil), v...)
		}
		delete(clone, key)
		if _, err := rawDNReferencesV4Query(clone); err == nil {
			t.Fatal("missing required pin", key)
		}
	}
	for _, extra := range []string{"dictionaryVersion=4", "memberCount=0", "memberOf=x", "search=x", "pageSize=10", "purpose=domain.directory_read.v4", "tenant=other", "domainId=other", "expectedRevision=01", "objectGUID=ABCDEF00-0000-0000-0000-000000000000"} {
		bad, _ := url.ParseQuery(q.Encode() + "&" + extra)
		if _, err := rawDNReferencesV4Query(bad); err == nil {
			t.Fatal("ambiguous/unsupported input", extra)
		}
	}
	s := &Service{}
	for _, input := range []struct {
		method, path, body string
		want               int
	}{{"GET", rawDNReferencesV4Path + "?" + q.Encode(), "", 503}, {"GET", rawDNReferencesV4Path + "?" + q.Encode(), "x", 400}, {"POST", rawDNReferencesV4Path, "", 405}, {"GET", rawDNReferencesV4Path + "/", "", 404}, {"GET", rawDNReferencesV4Path + "?domainId=a", "", 400}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(input.method, input.path, strings.NewReader(input.body))
		s.RawDNReferencesV4Handler(nil).ServeHTTP(w, r)
		if w.Code != input.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("boundary %s: %d", input.path, w.Code)
		}
	}
}

func TestRawDNReferencesV4StrictPagingAndHTTPEncoding(t *testing.T) {
	q := url.Values{"domainId": {"domain-a"}, "expectedRevision": {"1"}, "expectedCredentialRevision": {"1"}, "observationId": {"observation-a"}, "objectGUID": {"00000000-0000-0000-0000-000000000001"}, "pageIdx": {"1"}, "pageSize": {"25"}}
	for key, values := range map[string][]string{"pageIdx": {"", "0", "-1", "10001", "01", "+1", "1.0", " 1", "99999999999999999999"}, "pageSize": {"", "10", "24", "26", "0", "-1", "101", "025"}, "expectedRevision": {"0", "01"}, "objectGUID": {"ABCDEF00-0000-0000-0000-000000000000"}} {
		original := q.Get(key)
		for _, value := range values {
			q.Set(key, value)
			if _, err := rawDNReferencesV4Query(q); err == nil {
				t.Fatal("invalid input accepted", key, value)
			}
		}
		q.Set(key, original)
	}
	for _, size := range []string{"25", "50", "100"} {
		q.Set("pageSize", size)
		q.Set("pageIdx", "10000")
		if _, err := rawDNReferencesV4Query(q); err != nil {
			t.Fatal("allowed bound", err)
		}
	}
	s := &Service{}
	for _, query := range []string{q.Encode() + "&bad=%zz", q.Encode() + "&x=1;y=2", strings.Repeat("x", 32769)} {
		w := httptest.NewRecorder()
		s.RawDNReferencesV4Handler(nil).ServeHTTP(w, httptest.NewRequest("GET", rawDNReferencesV4Path+"?"+query, nil))
		if w.Code != 400 || w.Header().Get("X-ADTR-User-ID") != "" {
			t.Fatal("malformed query reached schema", w.Code)
		}
	}
	r := httptest.NewRequest("GET", rawDNReferencesV4Path+"?"+q.Encode(), nil)
	r.TransferEncoding = []string{"chunked"}
	w := httptest.NewRecorder()
	s.RawDNReferencesV4Handler(nil).ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("transfer body accepted")
	}
}
