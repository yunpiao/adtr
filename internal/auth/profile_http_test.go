package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfileHTTPBoundary(t *testing.T) {
	s := &Service{origin: "https://example.test", secure: true}
	for _, tt := range []struct {
		name, method, path, origin, content, body string
		cookie                                    bool
		want                                      int
	}{
		{name: "unknown route", method: "GET", path: "/api/profile/other", want: 404},
		{name: "wrong prefix", method: "GET", path: "/other/api/profile/me", want: 404},
		{name: "post me forbidden", method: "POST", path: "/api/profile/me", want: 405},
		{name: "delete forbidden", method: "DELETE", path: "/api/profile/avatar", want: 405},
		{name: "query scope injection", method: "GET", path: "/api/profile/me?userId=2", want: 400},
		{name: "malformed query", method: "GET", path: "/api/profile/avatar?x=%zz", want: 400},
		{name: "no identity", method: "GET", path: "/api/profile/me", want: 401},
		{name: "no avatar identity", method: "GET", path: "/api/profile/avatar", want: 401},
		{name: "wrong origin", method: "POST", path: "/api/profile/avatar", origin: "https://evil.test", want: 403},
		{name: "missing origin", method: "POST", path: "/api/profile/avatar", want: 403},
		{name: "missing cookie", method: "POST", path: "/api/profile/avatar", origin: s.origin, want: 401},
		{name: "wrong content", method: "POST", path: "/api/profile/avatar", origin: s.origin, content: "image/png", cookie: true, want: 400},
		{name: "invalid body", method: "POST", path: "/api/profile/avatar", origin: s.origin, content: "application/json", body: `{"userId":1}`, cookie: true, want: 400},
		{name: "oversize body", method: "POST", path: "/api/profile/avatar", origin: s.origin, content: "application/json", body: strings.Repeat(" ", profileMaxBodyBytes+1), cookie: true, want: 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Content-Type", tt.content)
			if tt.cookie {
				r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: strings.Repeat("a", 43)})
			}
			w := httptest.NewRecorder()
			s.ServeProfileHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tt.want, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
				t.Fatal("missing response privacy headers")
			}
			if tt.want == 405 && w.Header().Get("Allow") == "" {
				t.Fatal("missing Allow")
			}
		})
	}
}
