package auth

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

const archiveGoodRequest = `{"targets":[{"taskUUID":"synthetic-task","visibilityVersion":0}],"before":"2021-01-01T00:00:00Z","reason":"Synthetic retention review","idempotencyKey":"synthetic-archive","actorPassword":"synthetic-password","totpCode":"123456"}`
const archiveGoodRestore = `{"targets":[{"taskUUID":"synthetic-task","visibilityVersion":1}],"reason":"Synthetic retention review","idempotencyKey":"synthetic-restore","actorPassword":"synthetic-password","totpCode":"123456"}`
const archiveCandidateURL = "/api/tasks/archive-candidates?before=2021-01-01T00:00:00Z"

func archiveUnitResponse(method, path, body, origin, content string) *httptest.ResponseRecorder {
	s := &Service{origin: "https://example.test"}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", content)
	w := httptest.NewRecorder()
	s.ArchiveHandler(nil).ServeHTTP(w, r)
	return w
}

func TestArchiveHTTPExactRoutesAndTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, origin, content string
		status                                    int
	}{
		{"candidates", "GET", archiveCandidateURL, "", "", "", 503},
		{"archive", "POST", "/api/tasks/archive", archiveGoodRequest, "https://example.test", "application/json", 503},
		{"restore", "POST", "/api/tasks/restore", archiveGoodRestore, "https://example.test", "application/json", 503},
		{"media parameters", "POST", "/api/tasks/archive", archiveGoodRequest, "https://example.test", "application/json; charset=utf-8", 503},
		{"parent route", "GET", "/api/tasks", "", "", "", 404},
		{"unknown route", "GET", "/api/tasks/archive/list", "", "", "", 404},
		{"trailing slash", "POST", "/api/tasks/archive/", archiveGoodRequest, "https://example.test", "application/json", 404},
		{"prefix collision", "GET", "/api/tasks/archive-candidatesx", "", "", "", 404},
		{"wrong read method", "POST", archiveCandidateURL, "{}", "https://example.test", "application/json", 405},
		{"wrong archive method", "GET", "/api/tasks/archive", "", "", "", 405},
		{"wrong restore method", "DELETE", "/api/tasks/restore", "", "", "", 405},
		{"untrusted origin", "POST", "/api/tasks/archive", archiveGoodRequest, "https://evil.test", "application/json", 403},
		{"missing origin", "POST", "/api/tasks/archive", archiveGoodRequest, "", "application/json", 403},
		{"origin prefix", "POST", "/api/tasks/restore", archiveGoodRestore, "https://example.test.evil.test", "application/json", 403},
		{"wrong media", "POST", "/api/tasks/archive", archiveGoodRequest, "https://example.test", "text/plain", 400},
		{"missing media", "POST", "/api/tasks/archive", archiveGoodRequest, "https://example.test", "", 400},
		{"archive query", "POST", "/api/tasks/archive?actorId=2", archiveGoodRequest, "https://example.test", "application/json", 400},
		{"restore query", "POST", "/api/tasks/restore?tenantId=other", archiveGoodRestore, "https://example.test", "application/json", 400},
		{"malformed encoding", "GET", archiveCandidateURL + "&pageIdx=%ZZ", "", "", "", 400},
		{"malformed separator", "GET", archiveCandidateURL + "&pageIdx=1;pageSize=2", "", "", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := archiveUnitResponse(tc.method, tc.path, tc.body, tc.origin, tc.content)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("archive response was cacheable")
			}
			if tc.status == 405 && w.Header().Get("Allow") == "" {
				t.Fatal("missing allowed method")
			}
		})
	}
}

func TestArchiveHTTPRequiresExactNonNullFields(t *testing.T) {
	for _, tc := range []struct{ route, body string }{{"archive", archiveGoodRequest}, {"restore", archiveGoodRestore}} {
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
					w := archiveUnitResponse("POST", "/api/tasks/"+tc.route, string(raw), "https://example.test", "application/json")
					if w.Code != 400 {
						t.Fatalf("accepted %s %s: %d %s", mutation, field, w.Code, w.Body)
					}
				})
			}
		}
	}
}

func TestArchiveHTTPRejectsAmbiguousTargetsAndUntrustedFields(t *testing.T) {
	for _, target := range []string{
		`null`, `[]`, `{}`, `"synthetic-task"`,
		`{"taskUUID":"synthetic-task"}`, `{"visibilityVersion":0}`,
		`{"taskUUID":null,"visibilityVersion":0}`, `{"taskUUID":"synthetic-task","visibilityVersion":null}`,
		`{"TaskUUID":"synthetic-task","visibilityVersion":0}`,
		`{"taskUUID":"one","taskUUID":"two","visibilityVersion":0}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":0,"visibilityVersion":1}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":0,"archived":true}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":0,"tenantId":"other"}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":"0"}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":0.0}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":0e0}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":-1}`,
		`{"taskUUID":"synthetic-task","visibilityVersion":9223372036854775808}`,
		`{"taskUUID":"","visibilityVersion":0}`,
		`{"taskUUID":"one/two","visibilityVersion":0}`,
	} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range []struct{ route, body, old string }{
				{"archive", archiveGoodRequest, `{"taskUUID":"synthetic-task","visibilityVersion":0}`},
				{"restore", archiveGoodRestore, `{"taskUUID":"synthetic-task","visibilityVersion":1}`},
			} {
				raw := strings.Replace(tc.body, tc.old, target, 1)
				w := archiveUnitResponse("POST", "/api/tasks/"+tc.route, raw, "https://example.test", "application/json")
				if w.Code != 400 {
					t.Fatalf("accepted %s target %s: %d %s", tc.route, target, w.Code, w.Body)
				}
			}
		})
	}
	for _, extra := range []string{
		`"actorId":2`, `"tenantId":"other"`, `"authorizationVersion":"99"`,
		`"platform":true`, `"archived":true`, `"terminalAt":"2020-01-01T00:00:00Z"`,
		`"operationUUID":"caller"`, `"purge":true`, `"state":"succeeded"`,
	} {
		for _, tc := range []struct{ route, body string }{{"archive", archiveGoodRequest}, {"restore", archiveGoodRestore}} {
			raw := strings.TrimSuffix(tc.body, "}") + "," + extra + "}"
			w := archiveUnitResponse("POST", "/api/tasks/"+tc.route, raw, "https://example.test", "application/json")
			if w.Code != 400 {
				t.Fatalf("accepted %s %s: %d %s", tc.route, extra, w.Code, w.Body)
			}
		}
	}
}

func TestArchiveHTTPBoundsAndMalformedBodies(t *testing.T) {
	for name, raw := range map[string]string{
		"empty": `{}`, "array": `[]`, "null": `null`,
		"trailing object":   archiveGoodRequest + `{}`,
		"oversized":         strings.Repeat(" ", 128*1024) + archiveGoodRequest,
		"invalid UTF-8":     strings.Replace(archiveGoodRequest, "Synthetic retention review", "synthetic\xffreason", 1),
		"empty targets":     strings.Replace(archiveGoodRequest, `[{"taskUUID":"synthetic-task","visibilityVersion":0}]`, `[]`, 1),
		"duplicate targets": strings.Replace(archiveGoodRequest, `[{"taskUUID":"synthetic-task","visibilityVersion":0}]`, `[{"taskUUID":"synthetic-task","visibilityVersion":0},{"taskUUID":"synthetic-task","visibilityVersion":1}]`, 1),
		"blank reason":      strings.Replace(archiveGoodRequest, "Synthetic retention review", "   ", 1),
		"untrimmed reason":  strings.Replace(archiveGoodRequest, "Synthetic retention review", " Synthetic retention review ", 1),
		"long reason":       strings.Replace(archiveGoodRequest, "Synthetic retention review", strings.Repeat("界", 501), 1),
		"control reason":    strings.Replace(archiveGoodRequest, "Synthetic retention review", `synthetic\nreason`, 1),
		"nul reason":        strings.Replace(archiveGoodRequest, "Synthetic retention review", `synthetic\u0000reason`, 1),
		"empty key":         strings.Replace(archiveGoodRequest, `"synthetic-archive"`, `""`, 1),
		"invalid key":       strings.Replace(archiveGoodRequest, `"synthetic-archive"`, `"one/two"`, 1),
		"empty password":    strings.Replace(archiveGoodRequest, `"synthetic-password"`, `""`, 1),
		"long password":     strings.Replace(archiveGoodRequest, "synthetic-password", strings.Repeat("x", 257), 1),
		"short TOTP":        strings.Replace(archiveGoodRequest, `"123456"`, `"12345"`, 1),
		"long TOTP":         strings.Replace(archiveGoodRequest, `"123456"`, `"1234567"`, 1),
		"restore cutoff":    strings.TrimSuffix(archiveGoodRestore, "}") + `,"before":"2021-01-01T00:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := "/api/tasks/archive"
			if name == "restore cutoff" {
				path = "/api/tasks/restore"
			}
			w := archiveUnitResponse("POST", path, raw, "https://example.test", "application/json")
			if w.Code != 400 {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
	for _, count := range []int{1, 100, 101} {
		targets := make([]string, count)
		for i := range targets {
			targets[i] = fmt.Sprintf(`{"taskUUID":"synthetic-%d","visibilityVersion":0}`, i)
		}
		raw := strings.Replace(archiveGoodRequest, `[{"taskUUID":"synthetic-task","visibilityVersion":0}]`, "["+strings.Join(targets, ",")+"]", 1)
		w := archiveUnitResponse("POST", "/api/tasks/archive", raw, "https://example.test", "application/json")
		want := 503
		if count > 100 {
			want = 400
		}
		if w.Code != want {
			t.Fatalf("%d targets got %d want %d: %s", count, w.Code, want, w.Body)
		}
	}
	for _, reason := range []string{"x", strings.Repeat("界", 500)} {
		raw := strings.Replace(archiveGoodRequest, "Synthetic retention review", reason, 1)
		if w := archiveUnitResponse("POST", "/api/tasks/archive", raw, "https://example.test", "application/json"); w.Code != 503 {
			t.Fatalf("valid reason rejected: %d %s", w.Code, w.Body)
		}
	}
}

func TestArchiveHTTPRequiresBothExactPermissionMarks(t *testing.T) {
	for path, route := range archiveRoutes {
		for _, tasksGrant := range []AccessAuth{{}, {Readable: true}, {Readable: true, Writeable: true}} {
			for _, archiveGrant := range []AccessAuth{{}, {Readable: true}, {Readable: true, Writeable: true}} {
				grants := map[string]AccessAuth{"tasks": tasksGrant, "task_archive": archiveGrant, "audit": {Readable: true, Writeable: true}}
				want := tasksGrant.Readable && archiveGrant.Readable && (!route.write || tasksGrant.Writeable && archiveGrant.Writeable)
				if got := archivePathAllowed(route.method, "/api/tasks"+path, grants); got != want {
					t.Fatalf("route %s tasks=%+v archive=%+v: got %v want %v", path, tasksGrant, archiveGrant, got, want)
				}
			}
		}
	}
	grants := map[string]AccessAuth{"tasks": {Readable: true, Writeable: true}, "task_archive": {Readable: true, Writeable: true}}
	for _, path := range []string{"/api/tasks", "/api/tasks/archive/", "/api/tasks/archive-candidates/", "/api/tasks/restore?tenantId=other", "/api/tasks/archive/delete"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			if archivePathAllowed(method, path, grants) {
				t.Fatal("permission registry accepted unknown route", method, path)
			}
		}
	}
}

func TestArchiveHTTPStrictCutoffAndPagination(t *testing.T) {
	for _, before := range []string{
		"", "2021-01-01", "2021-01-01T00:00:00", "2021-01-01T00:00:00+00:00",
		"2021-01-01T00:00:00-00:00", "2021-01-01T01:00:00+01:00", "2021-01-01t00:00:00z",
		"2021-01-01T00:00:00.1234567Z", "2021-01-01T00:00:00.0000000Z", "2021-01-01T00:00:60Z",
	} {
		raw := strings.Replace(archiveGoodRequest, "2021-01-01T00:00:00Z", before, 1)
		if w := archiveUnitResponse("POST", "/api/tasks/archive", raw, "https://example.test", "application/json"); w.Code != 400 {
			t.Fatalf("accepted cutoff %q: %d %s", before, w.Code, w.Body)
		}
	}
	for _, query := range []string{
		"pageIdx=0", "pageIdx=01", "pageIdx=1000001", "pageIdx=-1", "pageIdx=1.0", "pageIdx=",
		"pageSize=0", "pageSize=101", "pageSize=%2B1", "pageSize=1&pageSize=2",
		"tenantId=one", "actorId=1", "state=succeeded", "domainId=platform", "taskName=infrastructure.health", "sort=asc",
		"before=2020-01-01T00:00:00Z",
	} {
		if w := archiveUnitResponse("GET", archiveCandidateURL+"&"+query, "", "", ""); w.Code != 400 {
			t.Fatalf("accepted candidate query %q: %d %s", query, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/api/tasks/archive-candidates", "/api/tasks/archive-candidates?before=", "/api/tasks/archive-candidates?before=2021-01-01T00:00:00.1234567Z"} {
		if w := archiveUnitResponse("GET", path, "", "", ""); w.Code != 400 {
			t.Fatalf("accepted missing/invalid cutoff: %d %s", w.Code, w.Body)
		}
	}
	for _, before := range []string{"2021-01-01T00:00:00Z", "2021-01-01T00:00:00.1Z", "2021-01-01T00:00:00.123456Z"} {
		path := "/api/tasks/archive-candidates?before=" + before + "&pageIdx=1&pageSize=100"
		if w := archiveUnitResponse("GET", path, "", "", ""); w.Code != 503 {
			t.Fatalf("valid cutoff rejected: %d %s", w.Code, w.Body)
		}
	}
}
