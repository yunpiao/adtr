package auth

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
)

func TestTaskHTTPTrustBoundary(t *testing.T) {
	s := &Service{origin: "https://example.test"}
	valid := `{"taskName":"infrastructure.health","domainId":"platform","payloadVersion":1,"payload":{},"idempotencyKey":"synthetic","actorPassword":"synthetic-password","totpCode":"123456"}`
	for _, tc := range []struct {
		method, path, body, origin, content string
		status                              int
	}{
		{"GET", "/api/tasks/list", "", "", "", 404},
		{"GET", "/api/tasks/", "", "", "", 404},
		{"GET", "/api/tasks/unknown", "", "", "", 404},
		{"GET", "/api/tasks/submit", "", "", "", 405},
		{"POST", "/api/tasks/submit", valid, "https://evil.test", "application/json", 403},
		{"POST", "/api/tasks/submit", valid, s.origin, "text/plain", 400},
		{"POST", "/api/tasks/submit", `{}`, s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.Replace(valid, `"payload":{}`, `"payload":null`, 1), s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.Replace(valid, `"payload":{}`, `"payload":{"x":1,"x":2}`, 1), s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.Replace(valid, `"domainId":"platform"`, `"domainId":"platform","domainId":"other"`, 1), s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.Replace(valid, `"taskName"`, `"TaskName"`, 1), s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.TrimSuffix(valid, "}") + `,"actorId":2}`, s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.TrimSuffix(valid, "}") + `,"tenantId":"other"}`, s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.TrimSuffix(valid, "}") + `,"authorizationVersion":"7"}`, s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.TrimSuffix(valid, "}") + `,"platform":true}`, s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", valid + `{}`, s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit", strings.Repeat(" ", 128*1024) + valid, s.origin, "application/json", 400},
		{"POST", "/api/tasks/submit?actorId=2", valid, s.origin, "application/json", 400},
		{"GET", "/api/tasks?k=%ZZ", "", "", "", 400},
		{"GET", "/api/tasks?state=running&state=failed", "", "", "", 400},
		{"GET", "/api/tasks/detail?taskUUID=x&tenantId=other", "", "", "", 400},
		{"GET", "/api/tasks/kinds?tenantId=other", "", "", "", 400},
		{"GET", "/api/tasks", "", "", "", 503},
	} {
		t.Run(tc.path+"/"+tc.body[:min(len(tc.body), 20)], func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.content)
			w := httptest.NewRecorder()
			s.TasksHandler(nil).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body)
			}
		})
	}
}
func TestTaskPaginationContract(t *testing.T) {
	for _, query := range []string{"pageIdx=0", "pageIdx=01", "pageIdx=1000001", "pageSize=-1", "pageSize=101", "pageSize=+1", "pageSize=1&pageSize=2", "state=STARTED", "tenant=one", "domainId=", "domainId=a/b", "sort=asc"} {
		q, _ := url.ParseQuery(query)
		if _, err := parseTaskFilter(q); err == nil {
			t.Fatalf("accepted %q", query)
		}
	}
	for _, query := range []string{"", "pageIdx=1&pageSize=100", "domainId=platform&taskName=infrastructure.health&state=retry_wait"} {
		q, _ := url.ParseQuery(query)
		if _, err := parseTaskFilter(q); err != nil {
			t.Fatalf("rejected %q: %v", query, err)
		}
	}
}
func TestTaskPermissionAndErrorContract(t *testing.T) {
	read := map[string]AccessAuth{"tasks": {Readable: true}}
	write := map[string]AccessAuth{"tasks": {Readable: true, Writeable: true}}
	for path, route := range taskRoutes {
		if taskPathAllowed(route.method, "/api/tasks"+path, nil) {
			t.Fatal("default allowed")
		}
		if taskPathAllowed(route.method, "/api/tasks"+path, read) == route.write {
			t.Fatalf("wrong read grant %s", path)
		}
		if !taskPathAllowed(route.method, "/api/tasks"+path, write) {
			t.Fatalf("writer denied %s", path)
		}
	}
	for _, path := range []string{"/api/tasks/list", "/api/tasks/", "/api/tasks/submit?x=y", "/api/tasks/unknown"} {
		if taskPathAllowed("GET", path, write) {
			t.Fatal(path)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{tasks.ErrAuthorization, 403, "forbidden"}, {fail(401, "unauthenticated"), 401, "unauthenticated"},
		{tasks.ErrSchemaIncompatible, 503, "schema_incompatible"}, {tasks.ErrSchemaGateRequired, 503, "schema_gate_required"},
		{&tasks.Error{Status: 409, Code: "idempotency_conflict"}, 409, "idempotency_conflict"},
		{errors.New("database connection with secret"), 500, "internal"},
	} {
		status, code := taskHTTPError(tc.err)
		if status != tc.status || code != tc.code {
			t.Fatal(status, code)
		}
	}
}
