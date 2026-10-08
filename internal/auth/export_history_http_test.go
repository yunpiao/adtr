package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExportHistoryUsesDedicatedReadPermissions(t *testing.T) {
	grants := map[string]AccessAuth{"audit": {Readable: true}, "audit_exports": {Readable: true}}
	for _, path := range []string{"/api/audit/exports/history", "/api/audit/exports/detail", "/api/audit/exports/download"} {
		if !auditPathAllowed("GET", path, grants) {
			t.Fatal("dedicated read unexpectedly needs task/write permission", path)
		}
	}
	if auditPathAllowed("POST", "/api/audit/exports", grants) {
		t.Fatal("history read authorized export submission")
	}
	delete(grants, "audit_exports")
	if auditPathAllowed("GET", "/api/audit/exports/history", grants) {
		t.Fatal("history leaked without export read")
	}
	grants["audit_exports"] = AccessAuth{Readable: true}
	delete(grants, "audit")
	if auditPathAllowed("GET", "/api/audit/exports/history", grants) {
		t.Fatal("history leaked without audit read")
	}
}
func TestExportHistoryHTTPContractBeforeDatabase(t *testing.T) {
	s := &Service{}
	for _, tc := range []struct {
		method, path, code string
		status             int
	}{
		{"POST", "/api/audit/exports/history", "method_not_allowed", 405},
		{"GET", "/api/audit/exports/history/", "not_found", 404},
		{"GET", "/api/audit/exports/history?status=padding", "invalid_input", 400},
		{"GET", "/api/audit/exports/history?status=%FF", "invalid_input", 400},
		{"GET", "/api/audit/exports/history?status=%ZZ", "invalid_input", 400},
		{"GET", "/api/audit/exports/history?pageIdx=1&pageIdx=1", "invalid_input", 400},
		{"GET", "/api/audit/exports/history?modelType=Leak", "unsupported_model_type", 400},
		{"GET", "/api/audit/exports/history?appType=1", "unsupported_app_type", 400},
		{"GET", "/api/audit/exports/history?file_name=..%2Fprivate", "invalid_input", 400},
		{"GET", "/api/audit/exports/history?" + strings.Repeat("x", 32769), "invalid_input", 400},
		{"GET", "/api/audit/exports/history?pageSize=10&status=cancel_requested", "tasks_unavailable", 503},
	} {
		t.Run(tc.code+tc.method+tc.path[:min(len(tc.path), 75)], func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			s.AuditHandler(nil).ServeHTTP(w, r)
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != tc.status || len(body) != 1 || body["error"] != tc.code {
				t.Fatal(w.Code, w.Body.String(), err)
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-ADTR-User-ID") != "" {
				t.Fatal("unsafe failed-read headers", w.Header())
			}
			if tc.status == http.StatusMethodNotAllowed && w.Header().Get("Allow") != "GET" {
				t.Fatal("wrong method contract")
			}
		})
	}
}
