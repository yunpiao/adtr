package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestMetadataUsesPeerAndStaticPathOnly(t *testing.T) {
	r := httptest.NewRequest("POST", "https://adtr.test/api/auth/login?password=must-not-capture", strings.NewReader(`{"password":"must-not-capture"}`))
	r.RemoteAddr = "[2001:db8::1]:33445"
	r.Header.Set("X-Forwarded-For", "198.51.100.10")
	a := newAuditContext(r)
	if a.peerIP != "2001:db8::1" || a.requestPath != "/api/auth/login" || a.username != "" || a.actor != nil || a.tenant != "system" || a.requestID == "" {
		t.Fatalf("untrusted metadata captured: %#v", a)
	}
	if newAuditContext(r).requestID == a.requestID {
		t.Fatal("requests share correlation ID")
	}
	r.RemoteAddr = "not-an-address"
	r.URL.Path = "/api/audit/user-secret-123"
	a = newAuditContext(r)
	if a.peerIP != "" || a.requestPath != "" {
		t.Fatal("malformed peer or dynamic path accepted")
	}
}
