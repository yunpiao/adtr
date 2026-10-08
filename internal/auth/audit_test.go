package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditClosedRoutesAndPermissionSeparation(t *testing.T) {
	grants := map[string]AccessAuth{"audit": {Readable: true}, "audit_exports": {Readable: true}, "tasks": {Readable: true, Writeable: true}}
	for _, path := range []string{"/api/audit", "/api/audit/types", "/api/audit/columns", "/api/audit/exports/detail", "/api/audit/exports/download"} {
		if !auditPathAllowed("GET", path, grants) {
			t.Fatal(path)
		}
	}
	for _, path := range []string{"/api/audit/delete", "/api/audit/restore", "/api/audit/exports"} {
		if auditPathAllowed("POST", path, grants) {
			t.Fatal(path)
		}
	}
	grants["audit"] = AccessAuth{Readable: true, Writeable: true}
	grants["audit_exports"] = AccessAuth{Readable: true, Writeable: true}
	if !auditPathAllowed("POST", "/api/audit/exports", grants) {
		t.Fatal("valid export denied")
	}
	delete(grants, "tasks")
	if auditPathAllowed("POST", "/api/audit/exports", grants) {
		t.Fatal("export requires tasks.writeable")
	}
	if !auditPathAllowed("GET", "/api/audit/exports/download", grants) {
		t.Fatal("download unnecessarily needs tasks permission")
	}
	for _, path := range []string{"/api/audit/", "/api/audits", "/api/audit/delete/", "/api/audit/exports/download/"} {
		if auditPathAllowed("GET", path, grants) {
			t.Fatal("unknown path", path)
		}
	}
	delete(grants, "audit")
	if auditPathAllowed("GET", "/api/audit/exports/detail", grants) {
		t.Fatal("exports cannot bypass audit.readable")
	}
}
func TestAuditBodyRejectsAmbiguousAndSecretBearingPayload(t *testing.T) {
	good := `{"filterEvent":[],"logTypeList":[],"selectColumn":["event"],"idempotencyKey":"export-1","actorPassword":"synthetic-password","totpCode":"123456"}`
	for _, raw := range []string{strings.Replace(good, `"selectColumn":["event"]`, `"selectColumn":[]`, 1), strings.Replace(good, `"selectColumn":["event"]`, `"selectColumn":["event","event"]`, 1), strings.Replace(good, `"filterEvent":[]`, `"filterEvent":null`, 1), strings.Replace(good, `"filterEvent":[]`, `"filterEvent":[],"filterEvent":[]`, 1), strings.Replace(good, `"filterEvent":[]`, `"filterEvent":[],"token":"secret"`, 1), strings.Replace(good, `"filterEvent":[]`, `"filterEvent":[],"createSort":0`, 1), good + ` {}`} {
		r := httptest.NewRequest("POST", "/api/audit/exports", strings.NewReader(raw))
		if _, e := decodeAuditRequest(httptest.NewRecorder(), r, "/exports", auditRoutes["/exports"]); e == nil {
			t.Fatal("accepted", raw)
		}
	}
	r := httptest.NewRequest("POST", "/api/audit/exports", strings.NewReader(good))
	if _, e := decodeAuditRequest(httptest.NewRecorder(), r, "/exports", auditRoutes["/exports"]); e != nil {
		t.Fatal(e)
	}
}

func TestAuditOmittedOptionalExportFieldsNormalize(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/audit/exports", strings.NewReader(`{"selectColumn":["event"],"idempotencyKey":"optional-defaults","actorPassword":"synthetic-password","totpCode":"123456"}`))
	in, err := decodeAuditRequest(httptest.NewRecorder(), r, "/exports", auditRoutes["/exports"])
	if err != nil {
		t.Fatal(err)
	}
	p := in.payload()
	if p.CreateSort != -1 || p.FilterEvent == nil || p.LogTypeList == nil {
		t.Fatal(p)
	}
}
