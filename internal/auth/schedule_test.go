package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
)

const scheduleGoodCreate = `{"label":"Synthetic health","taskName":"infrastructure.health","domainId":"platform","payloadVersion":1,"payload":{},"startAt":"2026-10-08T00:00:00Z","intervalSeconds":3600,"idempotencyKey":"synthetic-create","actorPassword":"synthetic-password","totpCode":"123456"}`
const scheduleGoodControl = `{"scheduleUUID":"synthetic-schedule","expectedControlVersion":1,"idempotencyKey":"synthetic-control","actorPassword":"synthetic-password","totpCode":"123456"}`

func scheduleUnitResponse(method, path, body, origin, content string) *httptest.ResponseRecorder {
	s := &Service{origin: "https://example.test"}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", content)
	w := httptest.NewRecorder()
	s.SchedulesHandler(nil).ServeHTTP(w, r)
	return w
}

func TestScheduleHTTPExactRoutesAndTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, origin, content string
		status                                    int
	}{
		{"list", "GET", "/api/tasks/schedules", "", "", "", 503},
		{"detail", "GET", "/api/tasks/schedules/detail?scheduleUUID=synthetic-schedule", "", "", "", 503},
		{"create", "POST", "/api/tasks/schedules/create", scheduleGoodCreate, "https://example.test", "application/json", 503},
		{"enable", "POST", "/api/tasks/schedules/enable", scheduleGoodControl, "https://example.test", "application/json", 503},
		{"pause", "POST", "/api/tasks/schedules/pause", scheduleGoodControl, "https://example.test", "application/json", 503},
		{"media parameters", "POST", "/api/tasks/schedules/create", scheduleGoodCreate, "https://example.test", "application/json; charset=utf-8", 503},
		{"parent route", "GET", "/api/tasks", "", "", "", 404},
		{"prefix collision", "GET", "/api/tasks/schedulesx", "", "", "", 404},
		{"trailing slash", "GET", "/api/tasks/schedules/", "", "", "", 404},
		{"detail trailing slash", "GET", "/api/tasks/schedules/detail/", "", "", "", 404},
		{"list alias", "GET", "/api/tasks/schedules/list", "", "", "", 404},
		{"delete absent", "DELETE", "/api/tasks/schedules/delete", "", "", "", 404},
		{"edit absent", "POST", "/api/tasks/schedules/update", "{}", "https://example.test", "application/json", 404},
		{"wrong read method", "POST", "/api/tasks/schedules", "{}", "https://example.test", "application/json", 405},
		{"wrong write method", "GET", "/api/tasks/schedules/create", "", "", "", 405},
		{"untrusted origin", "POST", "/api/tasks/schedules/create", scheduleGoodCreate, "https://evil.test", "application/json", 403},
		{"missing origin", "POST", "/api/tasks/schedules/create", scheduleGoodCreate, "", "application/json", 403},
		{"origin prefix", "POST", "/api/tasks/schedules/create", scheduleGoodCreate, "https://example.test.evil.test", "application/json", 403},
		{"wrong media", "POST", "/api/tasks/schedules/create", scheduleGoodCreate, "https://example.test", "text/plain", 400},
		{"missing media", "POST", "/api/tasks/schedules/create", scheduleGoodCreate, "https://example.test", "", 400},
		{"write query", "POST", "/api/tasks/schedules/create?actorId=2", scheduleGoodCreate, "https://example.test", "application/json", 400},
		{"malformed encoding", "GET", "/api/tasks/schedules?state=%ZZ", "", "", "", 400},
		{"malformed separator", "GET", "/api/tasks/schedules?pageIdx=1;pageSize=1", "", "", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := scheduleUnitResponse(tc.method, tc.path, tc.body, tc.origin, tc.content)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("schedule response was cacheable")
			}
			if tc.status == 405 && w.Header().Get("Allow") == "" {
				t.Fatal("missing allowed method")
			}
		})
	}
}

func TestScheduleHTTPRequiresEveryExactNonNullField(t *testing.T) {
	for _, tc := range []struct{ route, body string }{{"create", scheduleGoodCreate}, {"enable", scheduleGoodControl}, {"pause", scheduleGoodControl}} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.body), &fields); err != nil {
			t.Fatal(err)
		}
		for field := range fields {
			for _, mutation := range []string{"missing", "null", "alias", "duplicate"} {
				t.Run(tc.route+"/"+field+"/"+mutation, func(t *testing.T) {
					var changed map[string]json.RawMessage
					if err := json.Unmarshal([]byte(tc.body), &changed); err != nil {
						t.Fatal(err)
					}
					switch mutation {
					case "missing":
						delete(changed, field)
					case "null":
						changed[field] = json.RawMessage(`null`)
					case "alias":
						changed[strings.ToUpper(field[:1])+field[1:]] = changed[field]
						delete(changed, field)
					}
					raw, err := json.Marshal(changed)
					if err != nil {
						t.Fatal(err)
					}
					if mutation == "duplicate" {
						raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"` + field + `":` + string(fields[field]) + `}`)
					}
					w := scheduleUnitResponse("POST", "/api/tasks/schedules/"+tc.route, string(raw), "https://example.test", "application/json")
					if w.Code != 400 {
						t.Fatalf("accepted %s %s: %d %s", mutation, field, w.Code, w.Body)
					}
				})
			}
		}
	}
}

func TestScheduleHTTPRejectsSpoofedAndMalformedBodies(t *testing.T) {
	for _, extra := range []string{
		`"actorId":2`, `"tenantId":"other"`, `"authorizationVersion":"99"`, `"platform":true`,
		`"state":"enabled"`, `"controlVersion":2`, `"maxAttempts":99`, `"leaseOwner":"caller"`,
		`"fencingToken":3`, `"nextDueAt":"2026-10-08T00:00:00Z"`, `"result":{}`, `"catchUp":true`,
	} {
		t.Run(extra, func(t *testing.T) {
			raw := strings.TrimSuffix(scheduleGoodCreate, "}") + "," + extra + "}"
			w := scheduleUnitResponse("POST", "/api/tasks/schedules/create", raw, "https://example.test", "application/json")
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
	for name, raw := range map[string]string{
		"empty object":    `{}`,
		"array":           `[]`,
		"null":            `null`,
		"trailing object": scheduleGoodCreate + `{}`,
		"oversized":       strings.Repeat(" ", 128*1024) + scheduleGoodCreate,
		"invalid UTF-8":   strings.Replace(scheduleGoodCreate, "Synthetic health", "Synthetic\xffhealth", 1),
		"nested duplicate": strings.Replace(scheduleGoodCreate, `"payload":{}`,
			`"payload":{"nested":{"command":"one","command":"two"}}`, 1),
		"numeric string":    strings.Replace(scheduleGoodCreate, `"intervalSeconds":3600`, `"intervalSeconds":"3600"`, 1),
		"fraction interval": strings.Replace(scheduleGoodCreate, `"intervalSeconds":3600`, `"intervalSeconds":3600.5`, 1),
		"fraction version":  strings.Replace(scheduleGoodCreate, `"payloadVersion":1`, `"payloadVersion":1.0`, 1),
		"empty password":    strings.Replace(scheduleGoodCreate, `"synthetic-password"`, `""`, 1),
		"long password":     strings.Replace(scheduleGoodCreate, "synthetic-password", strings.Repeat("x", 257), 1),
		"short TOTP":        strings.Replace(scheduleGoodCreate, `"123456"`, `"12345"`, 1),
		"long TOTP":         strings.Replace(scheduleGoodCreate, `"123456"`, `"1234567"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			w := scheduleUnitResponse("POST", "/api/tasks/schedules/create", raw, "https://example.test", "application/json")
			if w.Code != 400 {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
	for _, tc := range []struct{ route, body, extra string }{
		{"create", scheduleGoodCreate, `"scheduleUUID":"caller-selected"`},
		{"create", scheduleGoodCreate, `"expectedControlVersion":1`},
		{"enable", scheduleGoodControl, `"taskName":"infrastructure.health"`},
		{"pause", scheduleGoodControl, `"label":"Changed definition"`},
	} {
		raw := strings.TrimSuffix(tc.body, "}") + "," + tc.extra + "}"
		w := scheduleUnitResponse("POST", "/api/tasks/schedules/"+tc.route, raw, "https://example.test", "application/json")
		if w.Code != 400 {
			t.Fatal("field from another route accepted", tc.route, tc.extra, w.Code)
		}
	}
}

func TestScheduleHTTPStrictPaginationAndDetailQueries(t *testing.T) {
	for _, query := range []string{
		"pageIdx=0", "pageIdx=01", "pageIdx=1000001", "pageIdx=-1", "pageIdx=1.0",
		"pageSize=0", "pageSize=101", "pageSize=%2B1", "pageSize=1&pageSize=2", "pageIdx=",
		"state=", "state=ENABLED", "state=running", "state=paused&state=enabled",
		"tenantId=one", "actorId=1", "domainId=platform", "taskName=infrastructure.health", "sort=asc",
	} {
		t.Run("list/"+query, func(t *testing.T) {
			w := scheduleUnitResponse("GET", "/api/tasks/schedules?"+query, "", "", "")
			if w.Code != 400 {
				t.Fatalf("accepted %q: %d %s", query, w.Code, w.Body)
			}
		})
	}
	for _, query := range []string{
		"", "pageIdx=1&pageSize=100", "pageIdx=1000000&pageSize=1", "state=paused", "state=enabled", "state=authorization_blocked",
	} {
		t.Run("valid list/"+query, func(t *testing.T) {
			w := scheduleUnitResponse("GET", "/api/tasks/schedules?"+query, "", "", "")
			if w.Code != 503 {
				t.Fatalf("rejected %q: %d %s", query, w.Code, w.Body)
			}
		})
	}
	for _, query := range []string{
		"", "scheduleUUID=", "scheduleUUID=one&scheduleUUID=two", "taskUUID=one",
		"scheduleUUID=one&tenantId=other", "scheduleUUID=one&state=paused", "scheduleUUID=one&pageIdx=0",
		"scheduleUUID=one&pageSize=101", "scheduleUUID=one&pageSize=1&pageSize=2",
	} {
		t.Run("detail/"+query, func(t *testing.T) {
			w := scheduleUnitResponse("GET", "/api/tasks/schedules/detail?"+query, "", "", "")
			if w.Code != 400 {
				t.Fatalf("accepted %q: %d %s", query, w.Code, w.Body)
			}
		})
	}
	for _, query := range []string{"scheduleUUID=one", "scheduleUUID=one&pageIdx=2&pageSize=1"} {
		w := scheduleUnitResponse("GET", "/api/tasks/schedules/detail?"+query, "", "", "")
		if w.Code != 503 {
			t.Fatalf("rejected %q: %d %s", query, w.Code, w.Body)
		}
	}
}

func TestSchedulePermissionPathsRequireBothExactGrants(t *testing.T) {
	read := map[string]AccessAuth{"tasks": {Readable: true}, "schedules": {Readable: true}}
	write := map[string]AccessAuth{"tasks": {Readable: true, Writeable: true}, "schedules": {Readable: true, Writeable: true}}
	for suffix, route := range scheduleRoutes {
		path := "/api/tasks/schedules" + suffix
		if schedulePathAllowed(route.method, path, nil) {
			t.Fatal("schedule access defaulted to allowed", path)
		}
		if got := schedulePathAllowed(route.method, path, read); got != !route.write {
			t.Fatal("wrong read permission", path, got)
		}
		if !schedulePathAllowed(route.method, path, write) {
			t.Fatal("writer denied", path)
		}
		for _, grants := range []map[string]AccessAuth{
			{"tasks": {Readable: true, Writeable: true}},
			{"schedules": {Readable: true, Writeable: true}},
			{"tasks": {Writeable: true}, "schedules": {Readable: true, Writeable: true}},
			{"tasks": {Readable: true, Writeable: true}, "schedules": {Writeable: true}},
			{"tasks": {Readable: true, Writeable: true}, "Schedules": {Readable: true, Writeable: true}},
		} {
			if schedulePathAllowed(route.method, path, grants) {
				t.Fatal("missing exact readable grant allowed", path, grants)
			}
		}
		if route.write {
			for _, grants := range []map[string]AccessAuth{
				{"tasks": {Readable: true}, "schedules": {Readable: true, Writeable: true}},
				{"tasks": {Readable: true, Writeable: true}, "schedules": {Readable: true}},
			} {
				if schedulePathAllowed(route.method, path, grants) {
					t.Fatal("missing write grant allowed", path, grants)
				}
			}
		}
		if schedulePathAllowed("DELETE", path, write) {
			t.Fatal("unregistered method allowed", path)
		}
	}
	for _, path := range []string{
		"/api/tasks", "/api/tasks/schedules/", "/api/tasks/schedules/list", "/api/tasks/schedulesx",
		"/api/tasks/schedules/detail?actorId=2", "/api/tasks/schedules/update", "/api/tasks/schedules/create/",
	} {
		if schedulePathAllowed("GET", path, write) || schedulePathAllowed("POST", path, write) {
			t.Fatal("unregistered path allowed", path)
		}
	}
}

func TestScheduleAuthorizerRejectsUnregisteredScopesBeforeDatabase(t *testing.T) {
	authorize := NewScheduleAuthorizer()
	for _, scope := range []tasks.Scope{
		{TaskName: "audit.export", DomainID: "platform", Platform: true},
		{TaskName: "engineRestart", DomainID: "platform", Platform: true},
		{TaskName: "infrastructure.health", DomainID: "platform"},
		{TaskName: "infrastructure.health", DomainID: "domain-a", Platform: true},
		{TaskName: "infrastructure.health", DomainID: "domain-a"},
	} {
		version, err := authorize(context.Background(), nil, tasks.Principal{TenantID: "one", ActorID: 1}, scope, tasks.Execute)
		if !errors.Is(err, tasks.ErrAuthorization) || version != "" {
			t.Fatal("unregistered scheduler scope accepted", scope, version, err)
		}
	}
}

func TestScheduleMenuExposesExactRegisteredPaths(t *testing.T) {
	grants := map[string]AccessAuth{"tasks": {Readable: true, Writeable: true}, "schedules": {Readable: true, Writeable: true}}
	for _, node := range permissionNodes(grants, true) {
		if node.Mark != "schedules" {
			continue
		}
		if node.Name == "" || len(node.Paths) != len(scheduleRoutes) {
			t.Fatal("schedule menu lost its name or registered paths", node)
		}
		seen := map[string]bool{}
		for _, path := range node.Paths {
			method, url, ok := strings.Cut(path.URL, " ")
			if !ok || !schedulePathAllowed(method, url, grants) || seen[path.URL] {
				t.Fatal("schedule menu and route permissions disagree", path)
			}
			seen[path.URL] = true
		}
		return
	}
	t.Fatal("schedule menu missing despite both grants")
}
