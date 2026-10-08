package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/tasks"
)

const credentialUseValidBody = `{"accountId":"account-a","roleId":"platform_admin","purpose":"domain.connection_test","expectedAccountRevision":"1","expectedCredentialRevision":"1","expectedGrantRevision":"0","idempotencyKey":"grant-key","actorPassword":"Synthetic Proof 42","totpCode":"123456"}`

func TestCredentialUseStrictMutationBody(t *testing.T) {
	tests := []struct {
		name, path, body string
		status           int
		code             string
	}{
		{name: "grant", path: "/grant", body: credentialUseValidBody},
		{name: "revoke", path: "/revoke", body: strings.Replace(credentialUseValidBody, `"expectedGrantRevision":"0"`, `"expectedGrantRevision":"8"`, 1)},
		{name: "case-sensitive keys", body: strings.Replace(credentialUseValidBody, `"accountId"`, `"AccountId"`, 1)},
		{name: "duplicate field", body: strings.Replace(credentialUseValidBody, `"accountId":"account-a"`, `"accountId":"account-a","accountId":"account-b"`, 1)},
		{name: "escaped duplicate field", body: strings.Replace(credentialUseValidBody, `"accountId":"account-a"`, `"accountId":"account-a","\u0061ccountId":"account-b"`, 1)},
		{name: "null field", body: strings.Replace(credentialUseValidBody, `"roleId":"platform_admin"`, `"roleId": null `, 1)},
		{name: "caller tenant", body: strings.TrimSuffix(credentialUseValidBody, "}") + `,"tenantId":"foreign"}`},
		{name: "caller actor", body: strings.TrimSuffix(credentialUseValidBody, "}") + `,"actorId":"1"}`},
		{name: "caller admin", body: strings.TrimSuffix(credentialUseValidBody, "}") + `,"admin":true}`},
		{name: "credential input", body: strings.TrimSuffix(credentialUseValidBody, "}") + `,"password":"supplied-secret"}`},
		{name: "target address", body: strings.TrimSuffix(credentialUseValidBody, "}") + `,"target":"localhost"}`},
		{name: "command", body: strings.TrimSuffix(credentialUseValidBody, "}") + `,"command":"run"}`},
		{name: "extra object", body: credentialUseValidBody + `{}`},
		{name: "extra scalar", body: credentialUseValidBody + ` true`},
		{name: "invalid utf8", body: strings.Replace(credentialUseValidBody, "Synthetic", string([]byte{255}), 1)},
		{name: "array", body: "[" + credentialUseValidBody + "]"},
		{name: "null body", body: "null"},
		{name: "empty object", body: "{}"},
		{name: "empty proof", body: strings.Replace(credentialUseValidBody, "Synthetic Proof 42", "", 1)},
		{name: "long proof", body: strings.Replace(credentialUseValidBody, "Synthetic Proof 42", strings.Repeat("a", 257), 1)},
		{name: "letter TOTP", body: strings.Replace(credentialUseValidBody, "123456", "abcdef", 1)},
		{name: "short TOTP", body: strings.Replace(credentialUseValidBody, "123456", "12345", 1)},
		{name: "whitespace TOTP", body: strings.Replace(credentialUseValidBody, "123456", "12345 ", 1)},
		{name: "unicode TOTP", body: strings.Replace(credentialUseValidBody, "123456", "１２３４５６", 1)},
		{name: "account grammar", body: strings.Replace(credentialUseValidBody, "account-a", "account/a", 1)},
		{name: "reserved account", body: strings.Replace(credentialUseValidBody, "account-a", "platform", 1)},
		{name: "role grammar", body: strings.Replace(credentialUseValidBody, "platform_admin", "custom-role", 1)},
		{name: "key grammar", body: strings.Replace(credentialUseValidBody, "grant-key", "bad/key", 1)},
		{name: "reserved key", body: strings.Replace(credentialUseValidBody, "grant-key", "schedule_key", 1)},
		{name: "zero account revision", body: strings.Replace(credentialUseValidBody, `"expectedAccountRevision":"1"`, `"expectedAccountRevision":"0"`, 1)},
		{name: "zero credential revision", body: strings.Replace(credentialUseValidBody, `"expectedCredentialRevision":"1"`, `"expectedCredentialRevision":"0"`, 1)},
		{name: "unsupported purpose", body: strings.Replace(credentialUseValidBody, "domain.connection_test", "domain.install", 1), status: 422, code: "unsupported_credential_purpose"},
		{name: "revoke absent revision", path: "/revoke", body: credentialUseValidBody},
		{name: "unknown operation", path: "/execute", body: credentialUseValidBody},
		{name: "oversized body", body: strings.Repeat(" ", 32769) + credentialUseValidBody},
	}
	for _, field := range strings.Fields(credentialUseMutationFields) {
		var body map[string]any
		if err := json.Unmarshal([]byte(credentialUseValidBody), &body); err != nil {
			t.Fatal(err)
		}
		delete(body, field)
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		tests = append(tests, struct {
			name, path, body string
			status           int
			code             string
		}{name: "missing " + field, body: string(raw)})
		body[field] = 1
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		tests = append(tests, struct {
			name, path, body string
			status           int
			code             string
		}{name: "numeric " + field, body: string(raw)})
	}
	for _, field := range []string{"expectedAccountRevision", "expectedCredentialRevision", "expectedGrantRevision"} {
		for _, value := range []string{"", "-1", "+1", "01", "1.0", "1e0", " 1", "1 ", "9223372036854775808"} {
			var body map[string]any
			if err := json.Unmarshal([]byte(credentialUseValidBody), &body); err != nil {
				t.Fatal(err)
			}
			body[field] = value
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			tests = append(tests, struct {
				name, path, body string
				status           int
				code             string
			}{name: field + " " + value, body: string(raw)})
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = "/grant"
			}
			r := httptest.NewRequest(http.MethodPost, "/api/credential-use"+path, strings.NewReader(tc.body))
			var in credentialUseRequest
			err := decodeCredentialUseRequest(httptest.NewRecorder(), r, path, &in)
			if tc.name == "grant" || tc.name == "revoke" {
				if err != nil {
					t.Fatal(err)
				}
				if in.AccountID != "account-a" || in.RoleID != "platform_admin" || in.ActorPassword != "Synthetic Proof 42" || in.TOTPCode != "123456" {
					t.Fatal("mutation fields not decoded exactly")
				}
				return
			}
			if err == nil {
				t.Fatal("invalid mutation accepted")
			}
			wantStatus, wantCode := tc.status, tc.code
			if wantStatus == 0 {
				wantStatus, wantCode = 400, "invalid_input"
			}
			if status, code := credentialUseHTTPError(err); status != wantStatus || code != wantCode {
				t.Fatalf("got %d %s, want %d %s", status, code, wantStatus, wantCode)
			}
		})
	}
}

func TestCredentialUseStrictQueries(t *testing.T) {
	for _, path := range []string{"/grants", "/roles", "/effective", "/mutation"} {
		key := "accountId"
		if path == "/mutation" {
			key = "idempotencyKey"
		}
		for _, raw := range []string{key + "=account-a", "", key, key + "=", key + "=one&" + key + "=two", key + "=one&extra=two", "AccountId=one", key + "=has%20space", key + "=has%2Fslash", key + "=%00", key + "=" + strings.Repeat("a", 129), "idempotencyKey=schedule_key", "accountId=platform"} {
			t.Run(path+" "+raw, func(t *testing.T) {
				q, err := url.ParseQuery(raw)
				if err != nil {
					t.Fatal(err)
				}
				err = credentialUseQuery(path, q)
				if (err == nil) != (raw == key+"=account-a") {
					t.Fatal("singleton query acceptance mismatch", err)
				}
			})
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/credential-use/grant?accountId=account-a", strings.NewReader(credentialUseValidBody))
	var in credentialUseRequest
	if err := decodeCredentialUseRequest(httptest.NewRecorder(), r, "/grant", &in); err == nil {
		t.Fatal("mutation accepted a query")
	}
}

func TestCredentialUseCleanupAccountsQuery(t *testing.T) {
	for _, raw := range []string{"", "pageIdx=1&pageSize=10", "pageIdx=2&pageSize=20&keyword=example", "pageIdx=1&pageSize=30", "pageIdx=1&pageSize=40", "pageIdx=1&pageSize=50"} {
		q, err := url.ParseQuery(raw)
		if err != nil || credentialUseQuery("/accounts", q) != nil {
			t.Fatal("valid cleanup-list query rejected", raw, err)
		}
	}
	for _, raw := range []string{"accountId=account-a", "pageIdx=0", "pageIdx=-1", "pageIdx=01", "pageIdx=1.0", "pageIdx=1&pageIdx=2", "pageSize=0", "pageSize=-1", "pageSize=9", "pageSize=15", "pageSize=51", "pageSize=10&pageSize=20", "keyword=a&keyword=b", "keyword=%00", "tenantId=foreign"} {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = credentialUseQuery("/accounts", q); err == nil {
			t.Fatal("invalid cleanup-list query accepted", raw)
		}
	}
	for _, path := range []string{"/accounts", "/grants", "/mutation", "/revoke"} {
		if !credentialUseCleanupPath(path) {
			t.Fatal("cleanup unnecessarily blocked by tenant eligibility", path)
		}
	}
	for _, path := range []string{"/roles", "/effective", "/grant", "/execute", ""} {
		if credentialUseCleanupPath(path) {
			t.Fatal("tenant eligibility bypass outside cleanup", path)
		}
	}
}

func TestCredentialUseRouteAuthorization(t *testing.T) {
	all := map[string]AccessAuth{}
	for _, mark := range accessMarks {
		all[mark] = AccessAuth{Readable: true, Writeable: true}
	}
	for _, role := range []string{"", "viewer", strings.Repeat("c", 24), "platform_admin"} {
		for path, route := range credentialUseRoutes {
			want := path == "/effective" || role == "platform_admin"
			if got := credentialUsePathAllowed(route.method, "/api/credential-use"+path, role, all); got != want {
				t.Fatalf("role %q route %q: got %v want %v", role, path, got, want)
			}
		}
	}
	for _, grants := range []map[string]AccessAuth{nil, {}, {"operation_accounts": {Writeable: true}}, {"domains": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}} {
		if credentialUsePathAllowed(http.MethodGet, "/api/credential-use/effective", "platform_admin", grants) {
			t.Fatal("effective view bypassed explicit operation-account read gate")
		}
	}
	for _, path := range []string{"/api/credential-use", "/api/credential-use/", "/api/credential-use-extra/grants", "/api/credential-use/grants/extra", "/api/credential-use/reveal", "/api/credential-use/execute", "/api/operation-accounts/grants"} {
		if credentialUsePathAllowed(http.MethodGet, path, "platform_admin", all) {
			t.Fatal("unknown route granted", path)
		}
	}
	for path, route := range credentialUseRoutes {
		method := http.MethodPost
		if route.method == method {
			method = http.MethodGet
		}
		if credentialUsePathAllowed(method, "/api/credential-use"+path, "platform_admin", all) {
			t.Fatal("wrong method granted", path)
		}
	}
}

func TestCredentialUseHTTPRejectionAndNoStore(t *testing.T) {
	s := &Service{origin: "http://localhost:8080"}
	for _, tc := range []struct {
		name, method, path, body, origin, contentType string
		status                                        int
		code, allow                                   string
	}{
		{name: "unknown route", method: "GET", path: "/api/credential-use/execute", status: 404, code: "not_found"},
		{name: "method", method: "GET", path: "/api/credential-use/grant", status: 405, code: "method_not_allowed", allow: "POST"},
		{name: "head", method: "HEAD", path: "/api/credential-use/grants", status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "bad query", method: "GET", path: "/api/credential-use/grants?accountId=%zz", status: 400, code: "invalid_input"},
		{name: "long query", method: "GET", path: "/api/credential-use/grants?accountId=" + strings.Repeat("a", 32769), status: 400, code: "invalid_input"},
		{name: "duplicate query", method: "GET", path: "/api/credential-use/roles?accountId=one&accountId=two", status: 400, code: "invalid_input"},
		{name: "missing query", method: "GET", path: "/api/credential-use/effective", status: 400, code: "invalid_input"},
		{name: "cleanup list account query", method: "GET", path: "/api/credential-use/accounts?accountId=account-a", status: 400, code: "invalid_input"},
		{name: "no origin", method: "POST", path: "/api/credential-use/grant", body: credentialUseValidBody, contentType: "application/json", status: 403, code: "forbidden"},
		{name: "foreign origin", method: "POST", path: "/api/credential-use/grant", body: credentialUseValidBody, origin: "https://foreign.test", contentType: "application/json", status: 403, code: "forbidden"},
		{name: "media type", method: "POST", path: "/api/credential-use/grant", body: credentialUseValidBody, origin: s.origin, contentType: "text/plain", status: 400, code: "invalid_input"},
		{name: "invalid body", method: "POST", path: "/api/credential-use/grant", body: "{}", origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "schema unavailable before authentication", method: "GET", path: "/api/credential-use/grants?accountId=account-a", status: 503, code: "schema_unavailable"},
		{name: "cleanup list schema unavailable before authentication", method: "GET", path: "/api/credential-use/accounts?pageIdx=1&pageSize=10", status: 503, code: "schema_unavailable"},
		{name: "mutation schema unavailable before authentication", method: "POST", path: "/api/credential-use/grant", body: credentialUseValidBody, origin: s.origin, contentType: "application/json", status: 503, code: "schema_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			s.CredentialUseHandler(credentialuse.New()).ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Allow") != tc.allow || w.Header().Get("X-ADTR-User-ID") != "" {
				t.Fatalf("unsafe rejection: status %d headers %v", w.Code, w.Header())
			}
			var out map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out) != 1 || out["error"] != tc.code {
				t.Fatalf("unsafe error payload: %s", w.Body)
			}
			if strings.Contains(w.Body.String(), "Synthetic Proof") || strings.Contains(w.Body.String(), "123456") {
				t.Fatal("proof leaked in response")
			}
		})
	}
}

func TestCredentialUseSafeErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{&credentialuse.Error{Status: 409, Code: "revision_conflict"}, 409, "revision_conflict"},
		{&domains.Error{Status: 403, Code: "tenant_expired"}, 403, "tenant_expired"},
		{&operationaccounts.Error{Status: 404, Code: "not_found"}, 404, "not_found"},
		{tasks.ErrSchemaIncompatible, 503, "schema_incompatible"},
		{fail(429, "rate_limited"), 429, "rate_limited"},
		{&pgconn.PgError{Code: "42501", ConstraintName: "credential_use_governance", Message: "private guard details"}, 403, "credential_use_governance_required"},
		{&credentialuse.Error{Status: 403, Code: "credential_use_governance_required"}, 403, "credential_use_governance_required"},
		{&pgconn.PgError{Code: "XX000", Message: "private database details"}, 500, "internal"},
		{errors.New("sensitive internal detail"), 500, "internal"},
	} {
		status, code := credentialUseHTTPError(fmt.Errorf("wrapped: %w", tc.err))
		if status != tc.status || code != tc.code {
			t.Fatalf("error mapping: got %d %s want %d %s", status, code, tc.status, tc.code)
		}
	}
}

func TestCredentialUseProofRedactionAndReceiptShape(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/credential-use/grant", strings.NewReader(credentialUseValidBody))
	var in credentialUseRequest
	if err := decodeCredentialUseRequest(httptest.NewRecorder(), r, "/grant", &in); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{string(raw), fmt.Sprint(in), fmt.Sprintf("%+v", in), fmt.Sprintf("%#v", in)} {
		if strings.Contains(encoded, in.ActorPassword) || strings.Contains(encoded, in.TOTPCode) {
			t.Fatal("request formatting leaked proof")
		}
	}
	raw, err = json.Marshal(credentialUseMutationResult{Receipt: credentialuse.Receipt{Result: "SUCCESS", Operation: "grant"}, ConsumerEnabled: credentialuse.ConsumerEnabled})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["result"] != "SUCCESS" || out["operation"] != "grant" || out["consumerEnabled"] != true {
		t.Fatal("mutation receipt shape changed", out)
	}
	if _, nested := out["receipt"]; nested {
		t.Fatal("POST receipt must be returned directly")
	}
	for _, field := range []string{"actorPassword", "totpCode", "password", "username", "ciphertext", "fingerprint", "secretHash"} {
		if _, exists := out[field]; exists {
			t.Fatal("receipt returned secret material", field)
		}
	}
}
