package auth

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestOriginConfiguration(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://test:synthetic@localhost/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"", "http://example.com", "https://example.com/path", "https://user@example.com", "https://example.com#fragment"} {
		if _, err = New(cfg, make([]byte, 32), origin, false); err == nil {
			t.Fatal("invalid origin accepted", origin)
		}
	}
	s, err := New(cfg, make([]byte, 32), "http://localhost:8080", true)
	if err != nil {
		t.Fatal(err)
	}
	if s.cookieName() != "adtr_session" {
		t.Fatal("wrong development cookie")
	}
	if _, err = New(cfg, make([]byte, 1), "https://example.com", false); err == nil {
		t.Fatal("short key accepted")
	}
}
func TestHTTPRejectsBeforeDatabase(t *testing.T) {
	s := &Service{origin: "https://example.com"}
	for _, tc := range []struct {
		method, path, body, origin, content string
		status                              int
	}{
		{"POST", "/api/auth/login", "{}", "https://evil.example", "application/json", 403},
		{"POST", "/api/auth/login", "{}", "https://example.com", "text/plain", 400},
		{"POST", "/api/auth/login", "{\"unknown\":1}", "https://example.com", "application/json", 400},
		{"POST", "/api/auth/login", "{}{}", "https://example.com", "application/json", 400},
		{"POST", "/api/auth/login", "{\"username\":\"a" + string([]byte{0xff}) + "\"}", "https://example.com", "application/json", 400},
		{"POST", "/api/auth/login", strings.Repeat("x", 5000), "https://example.com", "application/json", 400},
		{"GET", "/api/auth/login", "", "", "", 405},
		{"GET", "/api/auth/missing", "", "", "", 404},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d != %d", tc.path, w.Code, tc.status)
		}
	}
}
func TestCookieSecurity(t *testing.T) {
	s := &Service{secure: true}
	w := httptest.NewRecorder()
	s.cookie(w, "synthetic")
	c := w.Result().Cookies()[0]
	if c.Name != "__Host-adtr_session" || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != 3 {
		t.Fatal("insecure cookie")
	}
}
