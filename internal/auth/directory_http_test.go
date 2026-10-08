package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
)

const directorySyncBody = `{"domainId":"domain-a","expectedRevision":"1","expectedCredentialGeneration":"2","idempotencyKey":"directory-request-1","actorPassword":"Synthetic Directory Proof 42","totpCode":"123456"}`
const directoryCancelBody = `{"taskUUID":"directory-task-1","actorPassword":"Synthetic Directory Proof 42","totpCode":"123456"}`

func TestDirectoryRoutePermissionIntersection(t *testing.T) {
	// domains.write and operation-account metadata grants deliberately confer no
	// credential-use authority, and directory writes do not require domains.write.
	for mask := 0; mask < 64; mask++ {
		grants := map[string]AccessAuth{
			"domains":            {Readable: mask&1 != 0, Writeable: mask&2 != 0},
			"directory_assets":   {Readable: mask&4 != 0, Writeable: mask&8 != 0},
			"tasks":              {Readable: mask&16 != 0, Writeable: mask&32 != 0},
			"operation_accounts": {Readable: true, Writeable: true},
		}
		for path, route := range directoryRoutes {
			want := mask&1 != 0 && mask&4 != 0
			if path != "/observation" {
				want = want && mask&16 != 0
			}
			if route.write {
				want = want && mask&8 != 0 && mask&32 != 0
			}
			if got := directoryPathAllowed(route.method, "/api/directory"+path, grants); got != want {
				t.Fatalf("permission mask %06b route %s: got %v, want %v", mask, path, got, want)
			}
		}
	}
	for path, route := range directoryRoutes {
		if directoryPathAllowed(route.method, "/api/directory"+path, nil) {
			t.Fatal("missing grants accepted", path)
		}
	}
}

func TestDirectoryRoutesAreExact(t *testing.T) {
	grants := map[string]AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}
	for _, path := range []string{"/api/directory", "/api/directory/", "/api/directory-extra/task", "/api/directory/task/", "/api/directory/task/extra", "/api/directory/Task", "/api/directory/retry", "/api/directory/recover", "/api/directory/submit", "/api/directory/observation?domainId=domain-a", "/api/domains/directory", "/api/tasks/detail"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if directoryPathAllowed(method, path, grants) {
				t.Fatal("unknown path granted", method, path)
			}
		}
	}
	for path, route := range directoryRoutes {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete, "get", "post"} {
			if got := directoryPathAllowed(method, "/api/directory"+path, grants); got != (method == route.method) {
				t.Fatal("method boundary", method, path, got)
			}
		}
	}
}

func TestDirectoryObservationQuery(t *testing.T) {
	for _, tc := range []struct {
		query, observation, kind string
		page, size               int
	}{
		{"domainId=domain-a", "", "", 1, 50},
		{"domainId=domain-a&pageIdx=1&pageSize=25&kind=user", "", "user", 1, 25},
		{"domainId=domain-a&observationId=observation-1&pageIdx=2&pageSize=50&kind=group", "observation-1", "group", 2, 50},
		{"domainId=domain-a&observationId=observation-1&pageIdx=10000&pageSize=100&kind=computer", "observation-1", "computer", 10000, 100},
	} {
		t.Run(tc.query, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			f, err := directoryQuery("/observation", q)
			if err != nil || f.DomainID != "domain-a" || f.ObservationID != tc.observation || string(f.Kind) != tc.kind || f.PageIdx != tc.page || f.PageSize != tc.size {
				t.Fatal("observation query changed", f, err)
			}
		})
	}
	for _, raw := range []string{
		"", "domainId", "domainId=", "domainId=platform", "domainId=has%20space", "domainId=a%2Fb", "domainId=%00", "domainId=" + strings.Repeat("a", 129),
		"domainId=domain-a&domainId=domain-a", "DomainId=domain-a", "domainId=domain-a&tenantId=other", "domainId=domain-a&actorId=1", "domainId=domain-a&keyword=user",
		"domainId=domain-a&observationId=", "domainId=domain-a&observationId=platform", "domainId=domain-a&observationId=one&observationId=two",
		"domainId=domain-a&kind=", "domainId=domain-a&kind=users", "domainId=domain-a&kind=User", "domainId=domain-a&kind=group&kind=user",
		"domainId=domain-a&pageIdx=0", "domainId=domain-a&pageIdx=2", "domainId=domain-a&pageIdx=10001&observationId=one", "domainId=domain-a&pageIdx=1&pageIdx=1",
		"domainId=domain-a&pageSize=", "domainId=domain-a&pageSize=20", "domainId=domain-a&pageSize=50&pageSize=50",
	} {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = directoryQuery("/observation", q); err == nil {
			t.Fatal("invalid observation query accepted", raw)
		}
	}
	for _, key := range []string{"pageIdx", "pageSize"} {
		for _, number := range []string{"", "-1", "+1", "01", "1.0", "1e0", " 1", "1 ", "9223372036854775808"} {
			q := url.Values{"domainId": {"domain-a"}, "observationId": {"observation-a"}, key: {number}}
			if _, err := directoryQuery("/observation", q); err == nil {
				t.Fatal("noncanonical number accepted", key, number)
			}
		}
	}
}

func TestDirectoryReceiptAndTaskQueries(t *testing.T) {
	for _, tc := range []struct {
		path, query string
		valid       bool
	}{
		{"/receipt", "domainId=domain-a&idempotencyKey=directory-key", true},
		{"/receipt", "domainId=domain-a", false},
		{"/receipt", "idempotencyKey=directory-key", false},
		{"/receipt", "domainId=platform&idempotencyKey=directory-key", false},
		{"/receipt", "domainId=domain-a&idempotencyKey=schedule_key", false},
		{"/receipt", "domainId=domain-a&idempotencyKey=", false},
		{"/receipt", "domainId=domain-a&idempotencyKey=directory-key&actorId=1", false},
		{"/receipt", "domainId=domain-a&idempotencyKey=directory-key&idempotencyKey=directory-key", false},
		{"/receipt", "domainId=domain-a&domainId=domain-a&idempotencyKey=directory-key", false},
		{"/receipt", "DomainId=domain-a&idempotencyKey=directory-key", false},
		{"/task", "taskUUID=directory-task-1", true},
		{"/task", "", false},
		{"/task", "taskUUID=", false},
		{"/task", "taskUUID=platform", false},
		{"/task", "taskUuid=directory-task-1", false},
		{"/task", "taskUUID=directory-task-1&domainId=domain-a", false},
		{"/task", "taskUUID=directory-task-1&taskUUID=directory-task-1", false},
		{"/task", "taskUUID=has%20space", false},
		{"/task", "taskUUID=" + strings.Repeat("a", 129), false},
		{"/sync", "domainId=domain-a", false},
		{"/unknown", "", false},
	} {
		t.Run(tc.path+" "+tc.query, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			f, err := directoryQuery(tc.path, q)
			if (err == nil) != tc.valid || tc.valid && tc.path == "/receipt" && f.DomainID != "domain-a" {
				t.Fatal("query boundary", f, err)
			}
		})
	}
}

func TestDirectoryMutationBodyStrictness(t *testing.T) {
	for _, tc := range []struct{ path, body string }{{"/sync", directorySyncBody}, {"/cancel", directoryCancelBody}} {
		t.Run(tc.path, func(t *testing.T) {
			var in directoryRequest
			r := httptest.NewRequest(http.MethodPost, "/api/directory"+tc.path, strings.NewReader(tc.body))
			if err := decodeDirectoryRequest(httptest.NewRecorder(), r, tc.path, &in); err != nil {
				t.Fatal(err)
			}
			if in.ActorPassword != "Synthetic Directory Proof 42" || in.TOTPCode != "123456" || tc.path == "/sync" && (in.DomainID != "domain-a" || in.ExpectedRevision != "1" || in.ExpectedCredentialGeneration != "2" || in.IdempotencyKey != "directory-request-1") || tc.path == "/cancel" && in.TaskUUID != "directory-task-1" {
				t.Fatal("exact request fields not decoded")
			}
			var original map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &original); err != nil {
				t.Fatal(err)
			}
			bodies := []string{"", "null", "[]", "{}", tc.body + `{}`, tc.body + `null`, strings.TrimSuffix(tc.body, "}") + `,"tenantId":"other"}`, strings.TrimSuffix(tc.body, "}") + `,"actorPassword":"replacement"}`, strings.Replace(tc.body, "Synthetic", string([]byte{0xff}), 1), strings.Repeat(" ", 32768) + tc.body}
			for field, value := range original {
				for _, replacement := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`42`), json.RawMessage(`true`), json.RawMessage(`[]`), json.RawMessage(`{}`), json.RawMessage(`""`)} {
					original[field] = replacement
					raw, err := json.Marshal(original)
					if err != nil {
						t.Fatal(err)
					}
					bodies = append(bodies, string(raw))
				}
				delete(original, field)
				raw, err := json.Marshal(original)
				if err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, string(raw))
				alias := strings.ToUpper(field[:1]) + field[1:]
				original[alias] = value
				raw, err = json.Marshal(original)
				if err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, string(raw))
				delete(original, alias)
				original[field] = value
			}
			for _, proof := range []string{"12345x", "１２", "12345", "1234567", " 12345"} {
				bodies = append(bodies, strings.Replace(tc.body, "123456", proof, 1))
			}
			bodies = append(bodies, strings.Replace(tc.body, "Synthetic Directory Proof 42", strings.Repeat("p", 257), 1))
			for i, body := range bodies {
				r = httptest.NewRequest(http.MethodPost, "/api/directory"+tc.path, strings.NewReader(body))
				if err := decodeDirectoryRequest(httptest.NewRecorder(), r, tc.path, &directoryRequest{}); err == nil {
					t.Fatal("invalid body accepted", i)
				}
			}
			for _, query := range []string{"?domainId=domain-a", "?ignored=", "?ignored"} {
				r = httptest.NewRequest(http.MethodPost, "/api/directory"+tc.path+query, strings.NewReader(tc.body))
				if err := decodeDirectoryRequest(httptest.NewRecorder(), r, tc.path, &directoryRequest{}); err == nil {
					t.Fatal("mutation accepted query", query)
				}
			}
		})
	}
	for _, revision := range []string{"0", "-1", "+1", "01", "1.0", "1e0", " 1", "1 ", "9223372036854775808"} {
		for _, field := range []string{"expectedRevision", "expectedCredentialGeneration"} {
			var body map[string]string
			if err := json.Unmarshal([]byte(directorySyncBody), &body); err != nil {
				t.Fatal(err)
			}
			body[field] = revision
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/directory/sync", strings.NewReader(string(raw)))
			if err = decodeDirectoryRequest(httptest.NewRecorder(), r, "/sync", &directoryRequest{}); err == nil {
				t.Fatal("noncanonical revision accepted", field, revision)
			}
		}
	}
	for _, tc := range []struct{ path, body string }{{"/cancel", directorySyncBody}, {"/sync", directoryCancelBody}, {"/task", directoryCancelBody}, {"/unknown", directorySyncBody}} {
		r := httptest.NewRequest(http.MethodPost, "/api/directory"+tc.path, strings.NewReader(tc.body))
		if err := decodeDirectoryRequest(httptest.NewRecorder(), r, tc.path, &directoryRequest{}); err == nil {
			t.Fatal("wrong route body accepted", tc.path)
		}
	}
}

func TestDirectoryHTTPRejectionsBeforeDatabase(t *testing.T) {
	s := &Service{origin: "http://localhost:8080"}
	for _, tc := range []struct {
		name, method, target, body, origin, contentType string
		status                                          int
		code, allow                                     string
	}{
		{name: "unknown route", method: "GET", target: "/api/directory/execute", status: 404, code: "not_found"},
		{name: "trailing slash", method: "GET", target: "/api/directory/task/", status: 404, code: "not_found"},
		{name: "wrong method", method: "GET", target: "/api/directory/sync", status: 405, code: "method_not_allowed", allow: "POST"},
		{name: "HEAD denied", method: "HEAD", target: "/api/directory/observation", status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "malformed query", method: "GET", target: "/api/directory/observation?domainId=%zz", status: 400, code: "invalid_input"},
		{name: "semicolon query", method: "GET", target: "/api/directory/observation?domainId=domain-a;kind=user", status: 400, code: "invalid_input"},
		{name: "overlong query", method: "GET", target: "/api/directory/observation?domainId=" + strings.Repeat("a", 32769), status: 400, code: "invalid_input"},
		{name: "missing domain", method: "GET", target: "/api/directory/observation", status: 400, code: "invalid_input"},
		{name: "duplicate domain", method: "GET", target: "/api/directory/observation?domainId=domain-a&domainId=domain-a", status: 400, code: "invalid_input"},
		{name: "unpinned page", method: "GET", target: "/api/directory/observation?domainId=domain-a&pageIdx=2", status: 400, code: "invalid_input"},
		{name: "missing origin", method: "POST", target: "/api/directory/sync", body: directorySyncBody, contentType: "application/json", status: 403, code: "forbidden"},
		{name: "foreign origin", method: "POST", target: "/api/directory/sync", body: directorySyncBody, origin: "https://foreign.test", contentType: "application/json", status: 403, code: "forbidden"},
		{name: "origin suffix", method: "POST", target: "/api/directory/cancel", body: directoryCancelBody, origin: s.origin + ".foreign.test", contentType: "application/json", status: 403, code: "forbidden"},
		{name: "plain text body", method: "POST", target: "/api/directory/cancel", body: directoryCancelBody, origin: s.origin, contentType: "text/plain", status: 400, code: "invalid_input"},
		{name: "bad content type", method: "POST", target: "/api/directory/sync", body: directorySyncBody, origin: s.origin, contentType: "application/json; charset", status: 400, code: "invalid_input"},
		{name: "proof malformed", method: "POST", target: "/api/directory/cancel", body: strings.Replace(directoryCancelBody, "123456", "12345x", 1), origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "body query", method: "POST", target: "/api/directory/sync?domainId=domain-a", body: directorySyncBody, origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "observation unavailable", method: "GET", target: "/api/directory/observation?domainId=domain-a", status: 503, code: "schema_unavailable"},
		{name: "receipt unavailable", method: "GET", target: "/api/directory/receipt?domainId=domain-a&idempotencyKey=directory-key", status: 503, code: "schema_unavailable"},
		{name: "task unavailable", method: "GET", target: "/api/directory/task?taskUUID=directory-task-1", status: 503, code: "schema_unavailable"},
		{name: "sync unavailable", method: "POST", target: "/api/directory/sync", body: directorySyncBody, origin: s.origin, contentType: "application/json", status: 503, code: "schema_unavailable"},
		{name: "cancel unavailable", method: "POST", target: "/api/directory/cancel", body: directoryCancelBody, origin: s.origin, contentType: "application/json; charset=utf-8", status: 503, code: "schema_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			s.DirectoryHandler(nil, nil).ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Allow") != tc.allow || w.Header().Get("X-ADTR-User-ID") != "" {
				t.Fatalf("unsafe HTTP rejection: status %d headers %v", w.Code, w.Header())
			}
			assertDirectoryErrorEnvelope(t, w, tc.code)
		})
	}
	for _, header := range []string{"Origin", "Content-Type"} {
		r := httptest.NewRequest(http.MethodPost, "/api/directory/sync", strings.NewReader(directorySyncBody))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Add(header, r.Header.Get(header))
		w := httptest.NewRecorder()
		s.DirectoryHandler(nil, nil).ServeHTTP(w, r)
		want := 400
		if header == "Origin" {
			want = 403
		}
		if w.Code != want {
			t.Fatal("ambiguous security header accepted", header, w.Code)
		}
	}
}

func TestDirectorySafeRateAndSchemaFailureResponses(t *testing.T) {
	// These are response-contract tests. Real session/CSRF/proof consumption,
	// durable rate counters and revocation races require PostgreSQL integration.
	s := &Service{}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fail(429, "rate_limited"), 429, "rate_limited"},
		{fail(401, "invalid_credentials"), 401, "invalid_credentials"},
		{fail(403, "mfa_required"), 403, "mfa_required"},
		{fail(403, "forbidden"), 403, "forbidden"},
		{&domains.Error{Status: 409, Code: "directory_observation_unavailable"}, 409, "directory_observation_unavailable"},
		{&domains.Error{Status: 503, Code: "directory_read_disabled"}, 503, "directory_read_disabled"},
		{&tasks.Error{Status: 409, Code: "idempotency_conflict"}, 409, "idempotency_conflict"},
		{tasks.ErrAuthorization, 403, "forbidden"},
		{directorySchemaError(tasks.ErrSchemaIncompatible), 503, "schema_incompatible"},
		{directorySchemaError(errors.New("private schema information")), 503, "schema_unavailable"},
		{directorySchemaError(tasks.ErrSchemaGateRequired), 503, "schema_unavailable"},
		{&pgconn.PgError{Code: "XX000", Message: "private database information"}, 500, "internal"},
		{errors.New("private password information"), 500, "internal"},
	} {
		w := httptest.NewRecorder()
		s.writeDirectoryError(w, context.Background(), "/sync", fmt.Errorf("wrapped: %w", tc.err))
		if w.Code != tc.status || (w.Header().Get("Retry-After") == "900") != (tc.status == 429) || w.Header().Get("X-ADTR-User-ID") != "" {
			t.Fatalf("unsafe failure mapping: status %d headers %v", w.Code, w.Header())
		}
		assertDirectoryErrorEnvelope(t, w, tc.code)
	}
}

func TestDirectoryRequestProofRedaction(t *testing.T) {
	for _, tc := range []struct{ path, body string }{{"/sync", directorySyncBody}, {"/cancel", directoryCancelBody}} {
		r := httptest.NewRequest(http.MethodPost, "/api/directory"+tc.path, strings.NewReader(tc.body))
		var in directoryRequest
		if err := decodeDirectoryRequest(httptest.NewRecorder(), r, tc.path, &in); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, formatted := range []string{string(raw), fmt.Sprint(in), fmt.Sprintf("%+v", in), fmt.Sprintf("%#v", in)} {
			if strings.Contains(formatted, in.ActorPassword) || strings.Contains(formatted, in.TOTPCode) {
				t.Fatal("request formatting disclosed proof")
			}
		}
		payload, err := json.Marshal(in.DirectoryInput)
		if err != nil || strings.Contains(string(payload), "actorPassword") || strings.Contains(string(payload), "totpCode") || strings.Contains(string(payload), in.ActorPassword) {
			t.Fatal("proof entered store submission metadata", err)
		}
	}
}

func TestDirectorySchemaFailureDoesNotStartFailureAudit(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://synthetic:synthetic@127.0.0.1:1/synthetic?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	connections := 0
	cfg.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		connections++
		return nil, errors.New("synthetic database unavailable")
	}
	s := &Service{database: cfg}
	for _, err := range []error{tasks.ErrSchemaIncompatible, tasks.ErrSchemaGateRequired, errors.New("synthetic missing relation")} {
		w := httptest.NewRecorder()
		s.writeDirectoryError(w, context.Background(), "/sync", directorySchemaError(err))
		if w.Code != 503 || connections != 0 {
			t.Fatal("schema rejection attempted ungated failure audit", w.Code, connections)
		}
	}
	// A normal denial still reaches the existing failure-attribution path. The
	// synthetic dial prevents network/SQL and distinguishes this from a no-op test.
	w := httptest.NewRecorder()
	s.writeDirectoryError(w, context.Background(), "/cancel", fail(403, "forbidden"))
	if w.Code != 403 || connections == 0 {
		t.Fatal("ordinary denial lost failure attribution")
	}
}

func assertDirectoryErrorEnvelope(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body) != 1 || body["error"] != code {
		t.Fatalf("unexpected error envelope %s", w.Body)
	}
	for _, secret := range []string{"Synthetic Directory Proof", "123456", "private database", "private password", "private schema"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private data disclosed in failure response")
		}
	}
}
