package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/credentialuse"
)

var directoryCredentialUseValidBody = strings.Replace(credentialUseValidBody, credentialuse.Purpose, credentialuse.DirectoryPurpose, 1)

func TestDirectoryCredentialUseRouteAuthorization(t *testing.T) {
	reader := map[string]AccessAuth{
		"domains":          {Readable: true},
		"directory_assets": {Readable: true},
	}
	for _, role := range []string{"", "viewer", strings.Repeat("c", 24), "platform_admin", "Platform_Admin"} {
		for path, route := range credentialUseRoutes {
			want := path == "/effective" || role == "platform_admin"
			if got := directoryCredentialUsePathAllowed(route.method, "/api/directory-credential-use"+path, role, reader); got != want {
				t.Fatalf("role %q route %q: got %v want %v", role, path, got, want)
			}
		}
		if credentialUsePathAllowed(http.MethodGet, "/api/credential-use/effective", role, reader) {
			t.Fatalf("directory-only reader %q gained operation-account metadata authority", role)
		}
	}
	for _, grants := range []map[string]AccessAuth{
		nil,
		{},
		{"domains": {Readable: true}},
		{"directory_assets": {Readable: true}},
		{"domains": {Writeable: true}, "directory_assets": {Readable: true}},
		{"domains": {Readable: true}, "directory_assets": {Writeable: true}},
		{"domains": {Readable: true}, "operation_accounts": {Readable: true, Writeable: true}},
		{"directory_assets": {Readable: true}, "operation_accounts": {Readable: true, Writeable: true}},
		{"operation_accounts": {Readable: true, Writeable: true}},
	} {
		for _, role := range []string{"viewer", "platform_admin"} {
			if directoryCredentialUsePathAllowed(http.MethodGet, "/api/directory-credential-use/effective", role, grants) {
				t.Fatalf("directory effective read bypassed both explicit read gates: role %q grants %+v", role, grants)
			}
		}
	}
	for path, route := range credentialUseRoutes {
		if path != "/effective" && !directoryCredentialUsePathAllowed(route.method, "/api/directory-credential-use"+path, "platform_admin", nil) {
			t.Fatalf("builtin grant administration acquired an unrelated metadata permission gate: %s", path)
		}
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, "get", "post"} {
			if method != route.method && directoryCredentialUsePathAllowed(method, "/api/directory-credential-use"+path, "platform_admin", reader) {
				t.Fatalf("wrong method granted: %s %s", method, path)
			}
		}
		if credentialUsePathAllowed(route.method, "/api/directory-credential-use"+path, "platform_admin", reader) || directoryCredentialUsePathAllowed(route.method, "/api/credential-use"+path, "platform_admin", reader) {
			t.Fatalf("purpose crossed trusted prefix: %s", path)
		}
	}
	for _, path := range []string{
		"/api/directory-credential-use", "/api/directory-credential-use/", "/api/directory-credential-use-extra/effective",
		"/api/directory-credential-use/effective/extra", "/api/directory-credential-use//effective",
		"/api/directory-credential-use/effective?purpose=domain.connection_test", "/api/directory-credential-use/reveal",
		"/api/directory-credential-use/execute", "/api/operation-accounts/grants", "/api/directory-credential-use/EFFECTIVE",
	} {
		if directoryCredentialUsePathAllowed(http.MethodGet, path, "platform_admin", reader) {
			t.Fatalf("unknown route granted: %s", path)
		}
	}
}

func TestDirectoryCredentialUseStrictTrustedPurpose(t *testing.T) {
	for _, trusted := range []string{credentialuse.Purpose, credentialuse.DirectoryPurpose, credentialuse.DirectoryV2Purpose} {
		for _, supplied := range []string{credentialuse.Purpose, credentialuse.DirectoryPurpose, credentialuse.DirectoryV2Purpose, "", "domain.install", "Domain.directory_read", " domain.directory_read", "domain.directory_read "} {
			for _, path := range []string{"/grant", "/revoke"} {
				t.Run(trusted+"/"+supplied+path, func(t *testing.T) {
					body := strings.Replace(credentialUseValidBody, credentialuse.Purpose, supplied, 1)
					if path == "/revoke" {
						body = strings.Replace(body, `"expectedGrantRevision":"0"`, `"expectedGrantRevision":"8"`, 1)
					}
					r := httptest.NewRequest(http.MethodPost, "/ignored-trusted-handler-path", strings.NewReader(body))
					var in credentialUseRequest
					err := decodeCredentialUseRequestForPurpose(httptest.NewRecorder(), r, path, &in, trusted)
					if supplied == trusted {
						if err != nil || in.Purpose != trusted || in.AccountID != "account-a" || in.ActorPassword != "Synthetic Proof 42" || in.TOTPCode != "123456" {
							t.Fatalf("trusted mutation was not decoded exactly: %v", err)
						}
						return
					}
					if status, code := credentialUseHTTPError(err); err == nil || status != http.StatusUnprocessableEntity || code != "unsupported_credential_purpose" {
						t.Fatalf("wrong purpose accepted or misclassified: %v (%d %s)", err, status, code)
					}
				})
			}
		}
	}
	// Keep the legacy parser pinned even if an internal caller passes the new URL.
	r := httptest.NewRequest(http.MethodPost, "/api/directory-credential-use/grant", strings.NewReader(directoryCredentialUseValidBody))
	var in credentialUseRequest
	if err := decodeCredentialUseRequest(httptest.NewRecorder(), r, "/grant", &in); err == nil {
		t.Fatal("legacy parser selected purpose from the request URL")
	}
}

func TestDirectoryCredentialUseHTTPTrustedPrefixIsolation(t *testing.T) {
	s := &Service{origin: "http://localhost:8080"}
	endpoints := []struct {
		prefix, purpose string
		handler         http.Handler
	}{
		{"/api/credential-use", credentialuse.Purpose, s.CredentialUseHandler(credentialuse.New())},
		{"/api/directory-credential-use", credentialuse.DirectoryPurpose, s.DirectoryCredentialUseHandler(credentialuse.New())},
		{"/api/directory-credential-use/v2", credentialuse.DirectoryV2Purpose, s.DirectoryV2CredentialUseHandler(credentialuse.New())},
	}
	for _, serving := range endpoints {
		for _, requested := range endpoints {
			for path, route := range credentialUseRoutes {
				t.Run(serving.prefix+" serves "+requested.prefix+path, func(t *testing.T) {
					query, body := "", ""
					switch path {
					case "/grants", "/roles", "/effective":
						query = "?accountId=account-a"
					case "/mutation":
						query = "?idempotencyKey=grant-key"
					case "/grant", "/revoke":
						body = strings.Replace(credentialUseValidBody, credentialuse.Purpose, requested.purpose, 1)
						if path == "/revoke" {
							body = strings.Replace(body, `"expectedGrantRevision":"0"`, `"expectedGrantRevision":"8"`, 1)
						}
					}
					r := httptest.NewRequest(route.method, requested.prefix+path+query, strings.NewReader(body))
					r.Header.Set("Origin", s.origin)
					r.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					serving.handler.ServeHTTP(w, r)
					want := http.StatusNotFound
					if serving.prefix == requested.prefix {
						want = http.StatusServiceUnavailable
					}
					if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-ADTR-User-ID") != "" {
						t.Fatalf("trusted prefix isolation failed: status %d headers %v", w.Code, w.Header())
					}
				})
			}
		}
		for _, supplied := range []string{credentialuse.Purpose, credentialuse.DirectoryPurpose, credentialuse.DirectoryV2Purpose} {
			body := strings.Replace(credentialUseValidBody, credentialuse.Purpose, supplied, 1)
			r := httptest.NewRequest(http.MethodPost, serving.prefix+"/grant", strings.NewReader(body))
			r.Header.Set("Origin", s.origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			serving.handler.ServeHTTP(w, r)
			want := http.StatusUnprocessableEntity
			if supplied == serving.purpose {
				want = http.StatusServiceUnavailable
			}
			if w.Code != want {
				t.Fatalf("body purpose %q selected authority at %s: status %d", supplied, serving.prefix, w.Code)
			}
		}
	}
}

func TestDirectoryCredentialUseHTTPRejectionAndNoStore(t *testing.T) {
	s := &Service{origin: "http://localhost:8080"}
	for _, tc := range []struct {
		name, method, path, body, origin, contentType string
		status                                        int
		code, allow                                   string
	}{
		{name: "unknown route", method: "GET", path: "/execute", status: 404, code: "not_found"},
		{name: "method", method: "GET", path: "/grant", status: 405, code: "method_not_allowed", allow: "POST"},
		{name: "head", method: "HEAD", path: "/grants", status: 405, code: "method_not_allowed", allow: "GET"},
		{name: "bad query", method: "GET", path: "/grants?accountId=%zz", status: 400, code: "invalid_input"},
		{name: "long query", method: "GET", path: "/grants?accountId=" + strings.Repeat("a", 32769), status: 400, code: "invalid_input"},
		{name: "duplicate query", method: "GET", path: "/roles?accountId=one&accountId=two", status: 400, code: "invalid_input"},
		{name: "missing query", method: "GET", path: "/effective", status: 400, code: "invalid_input"},
		{name: "query purpose", method: "GET", path: "/effective?accountId=account-a&purpose=domain.directory_read", status: 400, code: "invalid_input"},
		{name: "list purpose", method: "GET", path: "/accounts?purpose=domain.directory_read", status: 400, code: "invalid_input"},
		{name: "cleanup list account query", method: "GET", path: "/accounts?accountId=account-a", status: 400, code: "invalid_input"},
		{name: "no origin", method: "POST", path: "/grant", body: directoryCredentialUseValidBody, contentType: "application/json", status: 403, code: "forbidden"},
		{name: "foreign origin", method: "POST", path: "/grant", body: directoryCredentialUseValidBody, origin: "https://foreign.test", contentType: "application/json", status: 403, code: "forbidden"},
		{name: "media type", method: "POST", path: "/grant", body: directoryCredentialUseValidBody, origin: s.origin, contentType: "text/plain", status: 400, code: "invalid_input"},
		{name: "invalid body", method: "POST", path: "/grant", body: "{}", origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "mutation query", method: "POST", path: "/grant?purpose=domain.directory_read", body: directoryCredentialUseValidBody, origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "wrong purpose", method: "POST", path: "/grant", body: credentialUseValidBody, origin: s.origin, contentType: "application/json", status: 422, code: "unsupported_credential_purpose"},
		{name: "duplicate purpose", method: "POST", path: "/grant", body: strings.TrimSuffix(directoryCredentialUseValidBody, "}") + `,"purpose":"domain.connection_test"}`, origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "escaped duplicate purpose", method: "POST", path: "/grant", body: strings.TrimSuffix(directoryCredentialUseValidBody, "}") + `,"\u0070urpose":"domain.connection_test"}`, origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "caller tenant", method: "POST", path: "/grant", body: strings.TrimSuffix(directoryCredentialUseValidBody, "}") + `,"tenantId":"foreign"}`, origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "oversized body", method: "POST", path: "/grant", body: strings.Repeat(" ", 32769) + directoryCredentialUseValidBody, origin: s.origin, contentType: "application/json", status: 400, code: "invalid_input"},
		{name: "schema unavailable before authentication", method: "GET", path: "/grants?accountId=account-a", status: 503, code: "schema_unavailable"},
		{name: "cleanup list schema unavailable before authentication", method: "GET", path: "/accounts?pageIdx=1&pageSize=10", status: 503, code: "schema_unavailable"},
		{name: "mutation schema unavailable before authentication", method: "POST", path: "/grant", body: directoryCredentialUseValidBody, origin: s.origin, contentType: "application/json", status: 503, code: "schema_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/directory-credential-use"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			s.DirectoryCredentialUseHandler(credentialuse.New()).ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Allow") != tc.allow || w.Header().Get("X-ADTR-User-ID") != "" {
				t.Fatalf("unsafe rejection: status %d headers %v", w.Code, w.Header())
			}
			var out map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out) != 1 || out["error"] != tc.code {
				t.Fatalf("unsafe error payload: %s", w.Body)
			}
			for _, secret := range []string{"Synthetic Proof", "123456", "actorPassword", "totpCode", "fingerprint"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("error response disclosed proof or receipt metadata: %s", secret)
				}
			}
		})
	}
}

func TestDirectoryCredentialUseConsumerStatusIsolation(t *testing.T) {
	for _, tc := range []struct {
		purpose, prefix string
		enabled         bool
	}{
		{credentialuse.Purpose, "/api/credential-use", credentialuse.ConsumerEnabled},
		{credentialuse.DirectoryPurpose, "/api/directory-credential-use", credentialuse.DirectoryConsumerEnabled},
		{"", "", false},
		{"domain.install", "", false},
	} {
		prefix, enabled := credentialUseEndpointForPurpose(tc.purpose)
		if prefix != tc.prefix || enabled != tc.enabled {
			t.Fatalf("wrong endpoint or compiled consumer status for %q: %q %v", tc.purpose, prefix, enabled)
		}
		if prefix == "" && credentialUsePathAllowedForPurpose(http.MethodGet, "/effective", "platform_admin", map[string]AccessAuth{"operation_accounts": {Readable: true}}, tc.purpose) {
			t.Fatal("unsupported trusted purpose acquired authority")
		}
	}
}
