package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	for _, tc := range []struct {
		name, mode, url, listen string
		valid                   bool
	}{
		{"api", "api", "postgres://test:secret@localhost/test?sslmode=verify-full", "", true},
		{"worker", "worker", "postgres://localhost/test?sslmode=verify-full", "127.0.0.1:8081", true},
		{"insecure default", "api", "postgres://localhost/test", "", false},
		{"insecure explicit", "api", "postgres://localhost/test?sslmode=disable", "", false},
		{"ambiguous TLS", "api", "postgres://localhost/test?sslmode=verify-full&sslmode=disable", "", false},
		{"missing", "api", "", "", false},
		{"scheme", "api", "http://localhost/test", "", false},
		{"no database", "api", "postgres://localhost/", "", false},
		{"port", "api", "postgres://localhost/test?sslmode=verify-full", "127.0.0.1:0", false},
		{"unknown mode", "other", "postgres://localhost/test", "", false},
		{"parse secret", "api", "postgres://test:secret%ZZ@localhost/test", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(tc.mode, func(key string) string {
				if key == "ADTR_DATABASE_URL" {
					return tc.url
				}
				if key == "ADTR_LISTEN_ADDR" {
					return tc.listen
				}
				return ""
			})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestDevelopmentTLSException(t *testing.T) {
	_, err := LoadConfig("api", func(key string) string {
		switch key {
		case "ADTR_DATABASE_URL":
			return "postgres://localhost/test?sslmode=disable"
		case "ADTR_DEVELOPMENT":
			return "true"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHealthContract(t *testing.T) {
	for _, service := range []string{"api", "worker"} {
		for _, available := range []bool{true, false} {
			check := func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("probe has no deadline")
				}
				if !available {
					return errors.New("secret database password")
				}
				return nil
			}
			h := Handler(service, check)
			for _, tc := range []struct {
				method, path string
				want         int
			}{
				{"GET", "/livez", 200}, {"GET", "/readyz", map[bool]int{true: 200, false: 503}[available]},
				{"POST", "/readyz", 405}, {"GET", "/api/domains", 404},
			} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
				if w.Code != tc.want {
					t.Errorf("%s %s: got %d want %d", tc.method, tc.path, w.Code, tc.want)
				}
				if strings.Contains(w.Body.String(), "secret") {
					t.Fatal("error leaked")
				}
			}
		}
	}
}

func TestMissingCheckFailsClosed(t *testing.T) {
	w := httptest.NewRecorder()
	Handler("api", nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Code)
	}
}
