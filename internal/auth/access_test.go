package auth

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAccessFilterContract(t *testing.T) {
	cases := []struct {
		query string
		roles bool
		ok    bool
	}{
		{"", false, true}, {"pageIdx=1&pageSize=-1", false, true}, {"pageSize=100&sort=2", false, true}, {"search=alice&isSelf=true&filterRole=viewer&filterRole=platform_admin&filterMfaStatus=enable&filterPassStrength=high", false, true},
		{"filterStartCreateTm=2026-01-01T00%3A00%3A00Z&filterEndCreateTm=2026-02-01T00%3A00%3A00Z", false, true},
		{"pageIdx=0", false, false}, {"pageSize=0", false, false}, {"pageSize=101", false, false}, {"pageIdx=2&pageSize=-1", false, false}, {"sort=0", false, false}, {"sort=2", true, false},
		{"filterMfaStatus=disable", false, false}, {"filterMfaStatus=unknown", false, false}, {"filterPassStrength=excellent", false, false}, {"isSelf=1", false, false}, {"tenant=other", false, false}, {"sort=1&sort=-1", false, false},
		{"filterRole=arbitrary", false, false}, {"filterStartPassTm=yesterday", false, false}, {"filterStartCreateTm=2026-02-01T00%3A00%3A00Z&filterEndCreateTm=2026-01-01T00%3A00%3A00Z", false, false}, {"filterRole=viewer", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			q, e := url.ParseQuery(tc.query)
			if e != nil {
				t.Fatal(e)
			}
			_, e = parseAccessFilter(q, tc.roles)
			if (e == nil) != tc.ok {
				t.Fatalf("ok=%v err=%v", tc.ok, e)
			}
		})
	}
	if _, _, _, e := makeAccessPage(accessFilter{page: 1, size: -1}, 1001); e == nil {
		t.Fatal("unbounded list accepted")
	}
	p, limit, offset, e := makeAccessPage(accessFilter{page: 3, size: 20}, 41)
	if e != nil || p.Pages != 3 || limit != 20 || offset != 40 {
		t.Fatal(p, limit, offset, e)
	}
}
func TestAccessProfileAndPermissionValidation(t *testing.T) {
	ptr := func(s string) *string { return &s }
	for _, in := range []accessRequest{{}, {Mobile: ptr("13812345678"), Email: ptr("example@example.test"), Remark: ptr(strings.Repeat("文", 150))}, {Email: ptr(""), Mobile: ptr("")}} {
		if e := validateAccessProfile(in); e != nil {
			t.Fatal(e)
		}
	}
	for _, in := range []accessRequest{{Mobile: ptr("1|812345678")}, {Mobile: ptr("13812345678x")}, {Email: ptr("Name <example@example.test>")}, {Email: ptr("bad")}, {Remark: ptr(strings.Repeat("文", 151))}, {Address: ptr("line\nline")}, {RealName: ptr(strings.Repeat("文", 51))}} {
		if validateAccessProfile(in) == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	for _, g := range [][]AccessGrant{{{Mark: "*", Auth: AccessAuth{true, true}}}, {{Mark: "users", Auth: AccessAuth{false, true}}}, {{Mark: "users"}, {Mark: "users"}}} {
		if _, e := validateGrants(g); e == nil {
			t.Fatal("invalid grant accepted")
		}
	}
	denied, _ := validateGrants(nil)
	if grantAllows(denied, accessRoutes["/users"]) {
		t.Fatal("default must deny")
	}
	limited := map[string]AccessAuth{"users": {true, false}}
	if accessCanDelegate(limited, map[string]AccessAuth{"users": {true, true}}) {
		t.Fatal("privilege escalation accepted")
	}
	if got := permissionNodes(denied, true); len(got) != 0 {
		t.Fatal("denied menu visible")
	}
	for _, p := range permissionNodes(limited, false) {
		if p.Children == nil || p.Paths == nil {
			t.Fatal("null list")
		}
	}
}
func TestAccessRouteRequestValidation(t *testing.T) {
	s := &Service{origin: "https://example.test"}
	cases := []struct {
		method, path, body, origin, content string
		status                              int
	}{
		{"POST", "/api/access/users/create", "{}", "https://evil.test", "application/json", 403},
		{"POST", "/api/access/users/create", "{}", "https://example.test", "text/plain", 400},
		{"POST", "/api/access/users/create", `{"tenant_id":"other"}`, "https://example.test", "application/json", 400},
		{"POST", "/api/access/users/delete", `{"username":"alice","password":"ignored","actorPassword":"secret","totpCode":"123456"}`, "https://example.test", "application/json", 400},
		{"POST", "/api/access/roles/save", `{"roleName":"x","actorPassword":"secret","totpCode":"123456"}`, "https://example.test", "application/json", 400},
		{"POST", "/api/access/permissions/save", `{"roleID":"viewer","permissions":[{"mark":"users","paths":[{"url":"*"}]}],"actorPassword":"secret","totpCode":"123456"}`, "https://example.test", "application/json", 400},
		{"POST", "/api/access/check", `{"token":"fake","paths":[]}`, "https://example.test", "application/json", 400},
		{"POST", "/api/access/users/create", `{"roleID":null}`, "https://example.test", "application/json", 400},
		{"POST", "/api/access/users/create", "{}{}", "https://example.test", "application/json", 400},
		{"POST", "/api/access/users/create", strings.Repeat("x", 40000), "https://example.test", "application/json", 400},
		{"PUT", "/api/access/users/create", "", "", "", 405}, {"GET", "/api/access/missing", "", "", "", 404}, {"GET", "/untrusted/users", "", "", "", 404},
		{"GET", "/api/access/users?filterRole=%ZZ", "", "", "", 400},
		{"GET", "/api/access/menu?tenant%ZZ=other", "", "", "", 400},
		{"GET", "/api/access/users?search=alice;tenant=other", "", "", "", 400},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body[:min(20, len(tc.body))], func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.content)
			w := httptest.NewRecorder()
			s.ServeAccessHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d", w.Code, tc.status)
			}
		})
	}
}

func TestAccessCheckCompositeGrantsAndUTF8(t *testing.T) {
	limited := map[string]AccessAuth{"roles": {true, true}}
	if grantAllows(limited, accessRoutes["/roles/save"]) {
		t.Fatal("roles/save omitted permission-write requirement")
	}
	limited["permissions"] = AccessAuth{true, true}
	if !grantAllows(limited, accessRoutes["/roles/save"]) {
		t.Fatal("composite grant rejected")
	}
	raw := append([]byte(`{"paths":["`), byte(0xff))
	raw = append(raw, []byte(`"]}`)...)
	var in accessRequest
	if validateAccessBody(raw, accessRoutes["/check"], &in) == nil {
		t.Fatal("invalid raw UTF-8 accepted")
	}
}
