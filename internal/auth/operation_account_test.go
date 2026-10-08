package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperationAccountStrictHTTP(t *testing.T) {
	valid := `{"domainId":"domain-a","expectedDomainRevision":"1","username":"reader@example.test","password":"synthetic","idempotencyKey":"key","actorPassword":"proof","totpCode":"123456"}`
	for _, body := range []string{valid, strings.Replace(valid, `"domainId":`, `"DomainId":`, 1), strings.Replace(valid, `"password":"synthetic"`, `"password":null`, 1), strings.Replace(valid, `"password":"synthetic"`, `"password":"synthetic","password":"second"`, 1), strings.TrimSuffix(valid, "}") + `,"tenantId":"other"}`, strings.TrimSuffix(valid, "}") + `,"verify":true}`, valid + `{}`, strings.Replace(valid, `"expectedDomainRevision":"1"`, `"expectedDomainRevision":1`, 1), strings.Replace(valid, `"totpCode":"123456"`, `"totpCode":"abcdef"`, 1), strings.Replace(valid, "synthetic", string([]byte{255}), 1)} {
		r := httptest.NewRequest("POST", "/api/operation-accounts/create", strings.NewReader(body))
		var in operationAccountRequest
		e := decodeOperationAccountRequest(httptest.NewRecorder(), r, "/create", operationAccountRoutes["/create"], &in)
		if (e == nil) != (body == valid) {
			t.Fatal("strict body mismatch", e)
		}
	}
	for _, body := range []string{`{"accountId":"a","expectedRevision":"1","idempotencyKey":"k","actorPassword":"proof","totpCode":"123456","username":"r@example.test"}`, `{"accountId":"a","expectedRevision":"1","idempotencyKey":"k","actorPassword":"proof","totpCode":"123456","label":null}`} {
		r := httptest.NewRequest("POST", "/api/operation-accounts/update", strings.NewReader(body))
		var in operationAccountRequest
		if decodeOperationAccountRequest(httptest.NewRecorder(), r, "/update", operationAccountRoutes["/update"], &in) == nil {
			t.Fatal("half pair/null accepted")
		}
	}
	for _, query := range []string{"?x=1", ""} {
		body := valid
		if query == "" {
			body = strings.Repeat(" ", 32769) + valid
		}
		r := httptest.NewRequest("POST", "/api/operation-accounts/create"+query, strings.NewReader(body))
		var in operationAccountRequest
		if decodeOperationAccountRequest(httptest.NewRecorder(), r, "/create", operationAccountRoutes["/create"], &in) == nil {
			t.Fatal("query/oversize accepted")
		}
	}
}
func TestOperationAccountRouteFunctionIntersection(t *testing.T) {
	grants := map[string]AccessAuth{"operation_accounts": {Readable: true}}
	for _, suffix := range []string{"", "/domains", "/detail", "/mutation"} {
		if !operationAccountPathAllowed("GET", "/api/operation-accounts"+suffix, grants) {
			t.Fatal(suffix)
		}
	}
	if operationAccountPathAllowed("POST", "/api/operation-accounts/create", grants) {
		t.Fatal("reader modified")
	}
	grants["operation_accounts"] = AccessAuth{Readable: true, Writeable: true}
	for _, suffix := range []string{"/create", "/update", "/delete"} {
		if !operationAccountPathAllowed("POST", "/api/operation-accounts"+suffix, grants) {
			t.Fatal(suffix)
		}
	}
	for _, path := range []string{"/api/operation-accounts/unknown", "/api/operation-accounts/reveal", "/api/operation-accounts-extra", "/api/domains"} {
		if operationAccountPathAllowed("GET", path, grants) {
			t.Fatal("prefix authority", path)
		}
	}
	if operationAccountPathAllowed("POST", "/api/operation-accounts", grants) {
		t.Fatal("method authority")
	}
}
