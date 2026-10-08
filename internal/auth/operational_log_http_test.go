package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperationalLogReadAndWritePermissionBoundaries(t *testing.T) {
	grants := map[string]AccessAuth{"system": {Readable: true}, "system_logs": {Readable: true}}
	for path, route := range operationalLogRoutes {
		full := "/api/system/logs" + path
		if got := operationalLogPathAllowed(route.method, full, "default", grants); got == route.write {
			t.Fatal("read/write boundary", path, got)
		}
		if operationalLogPathAllowed(route.method, full, "foreign", map[string]AccessAuth{"system": {Readable: true, Writeable: true}, "system_logs": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}) {
			t.Fatal("other tenant obtained installation logs")
		}
	}
	grants["system_logs"] = AccessAuth{Readable: true, Writeable: true}
	grants["tasks"] = AccessAuth{Readable: true, Writeable: true}
	if !operationalLogPathAllowed("POST", "/api/system/logs/bundles", "default", grants) {
		t.Fatal("authorized submit denied")
	}
	delete(grants, "system")
	if operationalLogPathAllowed("GET", "/api/system/logs", "default", grants) {
		t.Fatal("system permission omitted")
	}
}
func TestOperationalBundleBodyIsStrictAndProofRedacted(t *testing.T) {
	valid := `{"startTm":"2026-10-07T00:00:00Z","endTm":"2026-10-08T00:00:00Z","systemType":["api"],"idempotencyKey":"synthetic-bundle-key","actorPassword":"synthetic-private-proof","totpCode":"123456"}`
	for _, body := range []string{valid, strings.TrimSuffix(valid, "}") + `,"src":"/private"}`, strings.Replace(valid, `["api"]`, `[]`, 1), strings.Replace(valid, `"123456"`, `"12345x"`, 1), strings.TrimSuffix(valid, "}") + `,"systemType":["worker"]}`} {
		r := httptest.NewRequest("POST", "/api/system/logs/bundles", strings.NewReader(body))
		var in operationalLogRequest
		e := decodeOperationalLogRequest(httptest.NewRecorder(), r, "/bundles", operationalLogRoutes["/bundles"], &in)
		if (e == nil) != (body == valid) {
			t.Fatal("body acceptance", e)
		}
		if body == valid {
			raw, e := json.Marshal(in)
			if e != nil {
				t.Fatal(e)
			}
			for _, v := range []string{string(raw), fmt.Sprint(in), fmt.Sprintf("%#v", in)} {
				if strings.Contains(v, in.ActorPassword) || strings.Contains(v, in.TOTPCode) {
					t.Fatal("proof leaked")
				}
			}
		}
	}
}
func TestDomainEnrollmentIntrospectionRequiresBuiltinAdmin(t *testing.T) {
	g := map[string]AccessAuth{"domains": {Readable: true, Writeable: true}}
	for _, path := range []string{"/api/domains/create", "/api/domains/create-unconfigured", "/api/domains/creation"} {
		method := "POST"
		if strings.HasSuffix(path, "/creation") {
			method = "GET"
		}
		if domainAccessPathAllowed(method, path, "custom", g) || !domainAccessPathAllowed(method, path, "platform_admin", g) {
			t.Fatal("enrollment permission disagrees with server", path)
		}
	}
}

func TestOperationalSchemaFailureCannotTriggerUngatedBookkeeping(t *testing.T) {
	for _, e := range []error{tasks.ErrSchemaIncompatible, errors.New("synthetic missing schema relation"), tasks.ErrSchemaGateRequired} {
		status, code := taskHTTPError(operationalLogSchemaError(e))
		if status != 503 {
			t.Fatal("failed gate would record failure before schema verification", status, code)
		}
		if e == tasks.ErrSchemaIncompatible && code != "schema_incompatible" {
			t.Fatal(code)
		}
	}
}
