package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDomainHTTPStrictBodyBeforeDatabase(t *testing.T) {
	valid := `{"domain":"example.test","dcHostName":"dc1.example.test","port":"389","username":"reader@example.test","password":"synthetic","idempotencyKey":"request-1","actorPassword":"proof","totpCode":"123456"}`
	for _, body := range []string{valid, strings.Replace(valid, `"port":"389"`, `"port":"389","port":"636"`, 1), strings.Replace(valid, `"domain":`, `"Domain":`, 1), strings.Replace(valid, `"password":"synthetic"`, `"password":null`, 1), strings.Replace(valid, `"port":"389"`, `"port":389`, 1), strings.TrimSuffix(valid, "}") + `,"tenant":"other"}`, valid + `{}`, strings.Replace(valid, "example.test", "K.example", 1)} {
		r := httptest.NewRequest("POST", "/api/domains/create", strings.NewReader(body))
		w := httptest.NewRecorder()
		var in domainRequest
		err := decodeDomainRequest(w, r, "/create", domainRoutes["/create"], &in)
		if (err == nil) != (body == valid) {
			t.Fatalf("body validation mismatch: %v", err)
		}
	}
	for _, body := range []string{`{"domainId":"id","expectedRevision":"1","dcHostName":"dc.example.test","port":"389","actorPassword":"p","totpCode":"123456","username":"reader@example.test"}`, `{"domainId":"id","expectedRevision":"1","idempotencyKey":"k","actorPassword":"p","totpCode":"123456","ldapAddr":"10.0.0.1"}`} {
		path := "/update"
		if strings.Contains(body, "idempotencyKey") {
			path = "/test"
		}
		r := httptest.NewRequest("POST", "/api/domains"+path, strings.NewReader(body))
		var in domainRequest
		if decodeDomainRequest(httptest.NewRecorder(), r, path, domainRoutes[path], &in) == nil {
			t.Fatal("half replacement or test override accepted")
		}
	}
}
func TestDomainRouteFunctionIntersection(t *testing.T) {
	grants := map[string]AccessAuth{"domains": {Readable: true, Writeable: true}}
	if !domainPathAllowed("GET", "/api/domains", grants) || domainPathAllowed("POST", "/api/domains/test", grants) || domainPathAllowed("GET", "/api/domains/test-result", grants) || domainPathAllowed("GET", "/api/domains/unknown", grants) {
		t.Fatal("domain route boundary")
	}
	grants["tasks"] = AccessAuth{Readable: true}
	if !domainPathAllowed("GET", "/api/domains/test-result", grants) || domainPathAllowed("POST", "/api/domains/test", grants) {
		t.Fatal("task write boundary")
	}
}
