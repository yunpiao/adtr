package auth

import (
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDomainSourceRequestRejectsMixedAndAmbiguousInputs(t *testing.T) {
	base := `{"domainId":"abcdefghijklmnopqr","expectedRevision":"1","expectedConnectionCredentialGeneration":"1","idempotencyKey":"source-intent-123456","actorPassword":"synthetic-proof","totpCode":"123456"}`
	valid := strings.TrimSuffix(base, "}") + `,"username":"SYNTHETIC\\reader","password":"synthetic account pair"}`
	cases := []struct {
		name, path, body string
		ok               bool
	}{
		{"detach", "/detach", base, true},
		{"custom", "/custom", valid, true},
		{"duplicate", "/detach", strings.TrimSuffix(base, "}") + `,"domainId":"abcdefghijklmnopqr"}`, false},
		{"null generation", "/detach", strings.Replace(base, `"expectedConnectionCredentialGeneration":"1"`, `"expectedConnectionCredentialGeneration":null`, 1), false},
		{"numeric generation", "/detach", strings.Replace(base, `"expectedConnectionCredentialGeneration":"1"`, `"expectedConnectionCredentialGeneration":1`, 1), false},
		{"foreign source field", "/detach", strings.TrimSuffix(base, "}") + `,"accountId":"abcdefghijklmnopqr"}`, false},
		{"missing proof", "/detach", strings.Replace(base, `,"actorPassword":"synthetic-proof"`, "", 1), false},
		{"nondecimal TOTP", "/detach", strings.Replace(base, `"totpCode":"123456"`, `"totpCode":"12345x"`, 1), false},
		{"trailing JSON", "/detach", base + `{}`, false},
		{"null pair", "/custom", strings.Replace(valid, `"password":"synthetic account pair"`, `"password":null`, 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/domains/credential-source"+tc.path, strings.NewReader(tc.body))
			var in domainSourceRequest
			err := decodeDomainSourceRequest(httptest.NewRecorder(), r, tc.path, domainSourceRoutes[tc.path], &in)
			if (err == nil) != tc.ok {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.ok, err)
			}
		})
	}
}
func TestDomainSourceRequestFormattingDoesNotExposeProofOrPair(t *testing.T) {
	in := domainSourceRequest{ActorPassword: "synthetic-private-proof", TOTPCode: "829371"}
	u, p := "synthetic-private-name", "synthetic-private-pair"
	in.Username = &u
	in.Password = &p
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{fmt.Sprint(in), fmt.Sprintf("%#v", in), string(raw)} {
		for _, secret := range []string{in.ActorPassword, in.TOTPCode, u, p} {
			if strings.Contains(out, secret) {
				t.Fatal("mutation formatter disclosed sensitive field")
			}
		}
	}
}
func TestDomainSourcePermissionBoundary(t *testing.T) {
	grants := map[string]AccessAuth{"domains": {Readable: true, Writeable: true}}
	if !domainPathAllowed("POST", "/api/domains/credential-source/detach", grants) {
		t.Fatal("detach must not require lost account metadata permission")
	}
	if domainPathAllowed("POST", "/api/domains/credential-source/reference", grants) {
		t.Fatal("reference disclosed account selection without metadata read")
	}
	grants["operation_accounts"] = AccessAuth{Readable: true}
	if !domainPathAllowed("POST", "/api/domains/credential-source/reference", grants) {
		t.Fatal("normal reference route permission failed")
	}
	grants["domains"] = AccessAuth{Readable: true}
	if domainPathAllowed("GET", "/api/domains/credential-source/mutation", grants) {
		t.Fatal("mutation recovery lost its write-permission boundary")
	}
	if !domainPathAllowed("GET", "/api/domains/credential-source", grants) {
		t.Fatal("read-only source detail denied")
	}
	for _, path := range []string{"/api/domains/credential-source-extra", "/api/domains/credential-source/reference/", "/api/domains/credential-source/force-release"} {
		if domainPathAllowed("GET", path, grants) {
			t.Fatal("unexpected route accepted", path)
		}
	}
}

func TestDomainSourceGuardHasSafeForbiddenMapping(t *testing.T) {
	status, code := domainSourceHTTPError(&pgconn.PgError{Code: "42501", ConstraintName: "domain_source_authorization", Message: "synthetic internal SQL context"})
	if status != 403 || code != "credential_source_authorization_required" {
		t.Fatal(status, code)
	}
}

func TestDomainBootstrapProofFormattingIsRedacted(t *testing.T) {
	in := domainRequest{ActorPassword: "private-synthetic-proof", TOTPCode: "839201"}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{fmt.Sprint(in), fmt.Sprintf("%#v", in), string(raw)} {
		if strings.Contains(out, in.ActorPassword) || strings.Contains(out, in.TOTPCode) {
			t.Fatal("domain wrapper disclosed proof")
		}
	}
}
