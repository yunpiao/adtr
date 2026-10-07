package auth

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestResourceNameAndMembershipValidation(t *testing.T) {
	for _, s := range []string{"", "has space", "line\nbreak", "has\u00a0space", strings.Repeat("界", 86), "bad\x00name"} {
		if validResourceName(s) {
			t.Errorf("invalid name accepted: %q", s)
		}
	}
	for _, s := range []string{"资源组", "group.one", strings.Repeat("a", 256)} {
		if !validResourceName(s) {
			t.Errorf("valid name rejected: %q", s)
		}
	}
	good := &resourceMetaInput{Name: "资源组", Datas: []ResourceData{{"ad", []string{"domain-a", "domain-b"}}}}
	ids, err := resourceMembers(good)
	if err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	for _, meta := range []*resourceMetaInput{nil, {Name: "ok"}, {Name: "ok", Datas: []ResourceData{{"9", []string{"id"}}}}, {Name: "ok", Datas: []ResourceData{{"ad", []string{"same", "same"}}}}, {Name: "ok", Datas: []ResourceData{{"ad", []string{"../other"}}}}, {Name: "ok", Datas: []ResourceData{{"ad", nil}}}} {
		if _, err = resourceMembers(meta); err == nil {
			t.Fatalf("invalid membership accepted: %+v", meta)
		}
	}
	if _, err = resourceMembers(&resourceMetaInput{Name: "empty", Datas: []ResourceData{}}); err != nil {
		t.Fatal("empty groups are valid", err)
	}
}
func TestResourcePaginationAndLiteralSearch(t *testing.T) {
	for _, raw := range []string{"pageIdx=0", "pageSize=0", "pageSize=101", "pageSize=-2", "pageSize=-1&pageIdx=2", "pageIdx=1000001", "sort=1", "sort=TRUE", "pageIdx=1&pageIdx=2", "tenant=other"} {
		q, _ := url.ParseQuery(raw)
		if _, err := parseResourceFilter(q, false); err == nil {
			t.Fatal("invalid filter", raw)
		}
	}
	q, _ := url.ParseQuery("pageIdx=2&pageSize=10&name=%25_%E8%B5%84%E6%BA%90&sort=true")
	f, err := parseResourceFilter(q, false)
	if err != nil || f.name != "%_资源" || !f.ascending {
		t.Fatal(f, err)
	}
	p, l, o, err := resourcePage(f, 11)
	if err != nil || p.Pages != 2 || l != 10 || o != 10 {
		t.Fatal(p, l, o, err)
	}
	f = resourceFilter{page: 1, size: -1}
	if _, _, _, err = resourcePage(f, 1001); err == nil {
		t.Fatal("unbounded all-results")
	}
	p, _, _, err = resourcePage(f, 0)
	if err != nil || p.Pages != 0 {
		t.Fatal(p, err)
	}
}
func TestResourceCheckUnknownEnumsAndEmptyScope(t *testing.T) {
	typ := 2
	good := resourceRequest{ResourceType: &typ, Resources: []resourceCheck{{"ad", []string{"domain-a"}}}}
	if err := validateResourceChecks(good); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []int{0, 1, -1, 3} {
		in := good
		in.ResourceType = &kind
		if validateResourceChecks(in) == nil {
			t.Fatal("unsupported scope accepted", kind)
		}
	}
	for _, checks := range [][]resourceCheck{nil, {{"ad", nil}}, {{"AD", []string{"id"}}}, {{"1", []string{"id"}}}, {{"ad", []string{"*"}}}, {{"ad", []string{"id", "id"}}}} {
		in := good
		in.Resources = checks
		if validateResourceChecks(in) == nil {
			t.Fatal("invalid check accepted", checks)
		}
	}
}
func TestResourceTenantValidation(t *testing.T) {
	max, expiry, uid, name := 1, int64(1900000000), "customer-uid", "租户"
	good := resourceRequest{MaxADCount: &max, ExpireTime: &expiry, UID: &uid, Name: &name}
	if _, err := resourceTenantInput(good); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{-1, 100001} {
		in := good
		in.MaxADCount = &v
		if _, err := resourceTenantInput(in); err == nil {
			t.Fatal(v)
		}
	}
	for _, v := range []int64{-1, 253402300800} {
		in := good
		in.ExpireTime = &v
		if _, err := resourceTenantInput(in); err == nil {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"", " padded", strings.Repeat("界", 33), "line\nbreak"} {
		in := good
		in.Name = &v
		if _, err := resourceTenantInput(in); err == nil {
			t.Fatal(v)
		}
	}
	in := good
	in.UID = nil
	if _, err := resourceTenantInput(in); err == nil {
		t.Fatal("missing UID")
	}
}
func TestResourceHTTPRejectsUntrustedInputsBeforeDatabase(t *testing.T) {
	s := &Service{origin: "https://example.test"}
	tests := []struct {
		path, body string
		status     int
	}{
		{"/groups/create", `{"tenant":"other"}`, 400},
		{"/groups/create", `{"meta":{"name":"x","datas":[],"id":"forged"}}`, 400},
		{"/groups/create", `{"meta":{"name":"x","name":"y","datas":[]}}`, 400},
		{"/groups/create", `{"meta":{"name":"x","Name":"y","datas":[]}}`, 400},
		{"/groups/create", `{"meta":{"name":"x","datas":[{"appname":"ad","resources":[]}]}}`, 400},
		{"/check", `{"resourceType":2,"resources":[{"application":"ad","Application":"other","dataResource":["d"]}]}`, 400},

		{"/groups/create", `{"meta":null}`, 400},
		{"/groups/create", `{"meta":{"name":"x","datas":[],"mark":null}}`, 400},
		{"/groups/create", "{\"meta\":{\"name\":\"\xff\",\"datas\":[]}}", 400},
		{"/check", `{"token":"forged","resourceType":2,"resources":[]}`, 400},
		{"/check", `{"actorPassword":"not-needed"}`, 400},
		{"/check", `{"resources":[],"resources":[]}`, 400},
		{"/check", `{} {}`, 400},
		{"/check", strings.Repeat("x", 262145), 400},
		{"/tenant/save", `{"uid":5}`, 400},
		{"/tenant/save", `{"actor":"forged"}`, 400},
		{"/missing", `{}`, 404},
	}
	for _, tc := range tests {
		r := httptest.NewRequest("POST", "/api/resources"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeResources(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d: %s", tc.path, w.Code, tc.status, w.Body)
		}
	}
	for _, tc := range []struct {
		method, path, origin, content string
		status                        int
	}{{"POST", "/check", "https://evil.test", "application/json", 403}, {"POST", "/check", s.origin, "text/plain", 400}, {"GET", "/check", "", "", 405}, {"PUT", "/groups/update", s.origin, "application/json", 405}} {
		r := httptest.NewRequest(tc.method, "/api/resources"+tc.path, strings.NewReader("{}"))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		s.ServeResources(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code)
		}
	}
}
func TestResourceJSONNestedDuplicateKeys(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"x":[{"a":1,"a":2}]}`, `{"x":{"a":1,"a":2}}`} {
		if resourceUniqueJSON(json.NewDecoder(strings.NewReader(raw))) == nil {
			t.Fatal(raw)
		}
	}
	if err := resourceUniqueJSON(json.NewDecoder(strings.NewReader(`{"a":[1,2],"x":{"a":1}}`))); err != nil {
		t.Fatal(err)
	}
}

func TestResourceRejectsMalformedQueryBeforeDatabase(t *testing.T) {
	s := &Service{}
	for _, path := range []string{"/api/resources/groups?name=%zz", "/api/resources/groups?name=x;tenant=other", "/api/resources/grants?x=%GG", "/api/resources/groups/detail?id=%z0"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		s.ServeResources(w, r)
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
}

func TestResourcePathIntrospectionMatchesEnforcement(t *testing.T) {
	all := map[string]AccessAuth{"roles": {Readable: true, Writeable: true}, "users": {Readable: true, Writeable: true}, "permissions": {Readable: true, Writeable: true}}
	for _, tc := range []struct {
		method, path, role string
		grants             map[string]AccessAuth
		want               bool
	}{
		{"GET", "/api/resources/groups", "viewer", nil, false},
		{"GET", "/api/resources/grants", "viewer", nil, true},
		{"POST", "/api/resources/check", "viewer", nil, true},
		{"POST", "/api/resources/groups/create", "custom", all, true},
		{"GET", "/api/resources/tenant", "custom", all, false},
		{"POST", "/api/resources/tenant/save", "custom", all, false},
		{"POST", "/api/resources/tenant/save", "platform_admin", all, true},
		{"GET", "/api/resources/check", "platform_admin", all, false},
		{"GET", "/api/resources/unknown", "platform_admin", all, false},
		{"GET", "/api/resources/groups?tenant=foreign", "platform_admin", all, false},
	} {
		if got := resourcePathAllowed(tc.method, tc.path, tc.role, tc.grants); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

type resourceOffsetRow struct{ stamp time.Time }

func (r resourceOffsetRow) Scan(values ...any) error {
	*values[0].(*string) = "id"
	*values[1].(*string) = "name"
	*values[2].(*string) = "mark"
	*values[3].(*time.Time) = r.stamp
	*values[4].(*int) = 0
	*values[5].(*[]string) = []string{}
	return nil
}
func TestResourceCreatedTimeIsUTC(t *testing.T) {
	stamp := time.Date(2026, 10, 7, 12, 34, 56, 0, time.FixedZone("offset", 8*3600))
	meta, err := scanResourceMeta(resourceOffsetRow{stamp})
	if err != nil || meta.CreateTime.Location() != time.UTC || !meta.CreateTime.Equal(stamp) {
		t.Fatal(meta, err)
	}
	raw, err := json.Marshal(meta)
	if err != nil || !strings.Contains(string(raw), "2026-10-07T04:34:56Z") {
		t.Fatal(string(raw), err)
	}
}
