//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Every run owns a new database with synthetic accounts; no existing schema is modified.
func TestDurableAuthenticationLifecycle(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; missing database is not a pass")
	}
	ctx := context.Background()
	adminCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer admin.Close(ctx)
	dbName := "auth_test_" + strings.ToLower(randomToken(9))
	dbName = strings.ReplaceAll(strings.ReplaceAll(dbName, "-", "a"), "_", "b")
	identifier := pgx.Identifier{dbName}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+identifier+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg := adminCfg.Copy()
	cfg.Database = dbName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, "CREATE SCHEMA adtr;"+Schema); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfg, []byte(strings.Repeat("x", 32)), "http://localhost:8080", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	if err = s.Bootstrap(ctx, "Admin", "Initial Password 123"); err != nil {
		t.Fatal(err)
	}
	if s.Bootstrap(ctx, "another", "Other Password 123") == nil {
		t.Fatal("bootstrap repeated")
	}
	type client struct {
		cookie *http.Cookie
		csrf   string
	}
	call := func(c *client, path string, body map[string]string, want int) map[string]any {
		t.Helper()
		method := "POST"
		var raw []byte
		if body == nil {
			method = "GET"
		} else {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, "/api/auth"+path, strings.NewReader(string(raw)))
		r.RemoteAddr = "127.0.0.1:4444"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", s.origin)
		if c != nil {
			if c.cookie != nil {
				r.AddCookie(c.cookie)
			}
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: status=%d want=%d response=%s", path, w.Code, want, w.Body.String())
		}
		var out map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if c != nil {
			for _, cookie := range w.Result().Cookies() {
				c.cookie = cookie
			}
			if v, ok := out["csrfToken"].(string); ok {
				c.csrf = v
			}
		}
		return out
	}
	c := &client{}
	first := call(c, "/login", map[string]string{"username": "ADMIN", "password": "Initial Password 123"}, 200)
	if first["needChangePwd"] != true || first["username"] != "admin" {
		t.Fatal("first login contract", first)
	}
	call(c, "/mfa", nil, 403)
	old := *c
	bad := *c
	bad.csrf = "wrong"
	call(&bad, "/password", map[string]string{"oldPassword": "Initial Password 123", "newPassword": "Changed Password 123"}, 403)
	changed := call(c, "/password", map[string]string{"oldPassword": "Initial Password 123", "newPassword": "Changed Password 123"}, 200)
	if changed["needChangePwd"] != false || c.cookie.Value == old.cookie.Value || c.csrf == old.csrf {
		t.Fatal("password change must rotate and clear gate")
	}
	call(&old, "/me", nil, 401)
	call(c, "/password", map[string]string{"username": "other", "oldPassword": "Changed Password 123", "newPassword": "Another Password 123"}, 403)
	begin := call(c, "/mfa/enroll", map[string]string{"password": "Changed Password 123"}, 200)
	secret := begin["secret"].(string)
	var persisted string
	if err = conn.QueryRow(ctx, "SELECT mfa_pending FROM adtr.users WHERE username='admin'").Scan(&persisted); err != nil || persisted == secret || persisted == "" {
		t.Fatal("MFA secret was not encrypted", err)
	}
	code, _ := totp(secret, now.Unix()/30)
	beforeMFA := *c
	enabled := call(c, "/mfa/confirm", map[string]string{"password": "Changed Password 123", "secret": secret, "mfaCode": code}, 200)
	if enabled["hasMfa"] != true {
		t.Fatal("MFA not enabled")
	}
	call(&beforeMFA, "/me", nil, 401)
	// Enrollment consumes its TOTP step; immediately reusing that code must fail.
	call(&client{}, "/login", map[string]string{"username": "admin", "password": "Changed Password 123", "totpCode": code}, 401)
	call(&client{}, "/login", map[string]string{"username": "admin", "password": "Changed Password 123"}, 401)
	now = now.Add(30 * time.Second)
	code, _ = totp(secret, now.Unix()/30)
	c2 := &client{}
	call(c2, "/login", map[string]string{"username": "admin", "password": "Changed Password 123", "totpCode": code}, 200)
	call(&client{}, "/login", map[string]string{"username": "admin", "password": "Changed Password 123", "totpCode": code}, 401)
	// Create isolated tenant fixtures for administrator authorization boundaries.
	hash, e := hashPassword("Viewer Password 123")
	if e != nil {
		t.Fatal(e)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) VALUES('default','viewer',$1,'viewer',false),('other','outsider',$1,'viewer',false)", hash); err != nil {
		t.Fatal(err)
	}
	viewer := &client{}
	call(viewer, "/login", map[string]string{"username": "viewer", "password": "Viewer Password 123"}, 200)
	call(viewer, "/reset-password", map[string]string{"username": "admin", "newPassword": "Reset Password 123", "password": "Viewer Password 123"}, 403)
	now = now.Add(30 * time.Second)
	code, _ = totp(secret, now.Unix()/30)
	call(c2, "/reset-password", map[string]string{"username": "outsider", "newPassword": "Reset Password 123", "password": "Changed Password 123", "totpCode": code}, 403)
	call(c2, "/reset-password", map[string]string{"username": "viewer", "newPassword": "Reset Password 123", "password": "Changed Password 123", "totpCode": code}, 200)
	call(viewer, "/me", nil, 401)
	reset := call(viewer, "/login", map[string]string{"username": "viewer", "password": "Reset Password 123"}, 200)
	if reset["needChangePwd"] != true {
		t.Fatal("reset did not require change")
	}
	now = now.Add(30 * time.Second)
	code, _ = totp(secret, now.Unix()/30)
	disabled := call(c2, "/mfa/disable", map[string]string{"password": "Changed Password 123", "mfaCode": code}, 200)
	if disabled["hasMfa"] != false {
		t.Fatal("MFA not disabled")
	}
	call(c, "/me", nil, 401)
	saved := *c2
	call(c2, "/logout", map[string]string{}, 200)
	call(&saved, "/me", nil, 401)
	if _, err = conn.Exec(ctx, "UPDATE adtr.users SET password_updated_at=$1 WHERE username='admin'", now.Add(-91*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	expired := call(c, "/login", map[string]string{"username": "admin", "password": "Changed Password 123"}, 200)
	if expired["isExpired"] != true || expired["needChangePwd"] != true {
		t.Fatal("expiry gate missing")
	}
	now = now.Add(9 * time.Hour)
	call(c, "/me", nil, 401)
	var audits int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM adtr.auth_audit").Scan(&audits); err != nil || audits < 9 {
		t.Fatal("audit records missing", audits, err)
	}
	if _, err = conn.Exec(ctx, "UPDATE adtr.auth_audit SET action='tampered'"); err == nil {
		t.Fatal("audit updates allowed")
	}
	if _, err = conn.Exec(ctx, "DELETE FROM adtr.auth_audit"); err == nil {
		t.Fatal("audit deletion allowed")
	}
	if _, err = conn.Exec(ctx, "TRUNCATE adtr.auth_audit"); err == nil {
		t.Fatal("audit truncation allowed")
	}
	var deniedActor bool
	if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.auth_audit a JOIN adtr.users u ON a.actor_id=u.id WHERE u.username='viewer' AND a.tenant_id='default' AND a.action='reset-password_denied')").Scan(&deniedActor); err != nil || !deniedActor {
		t.Fatal("authenticated denial lost actor or scope", err)
	}
	// Two concurrent attempts using one TOTP step must produce one session.
	var raceID int64
	if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) VALUES('default','race-user',$1,'viewer',false) RETURNING id", hash).Scan(&raceID); err != nil {
		t.Fatal(err)
	}
	raceSecret := "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	encrypted, e := seal(s.key, raceSecret, strconv.FormatInt(raceID, 10))
	if e != nil {
		t.Fatal(e)
	}
	if _, err = conn.Exec(ctx, "UPDATE adtr.users SET mfa_secret=$1 WHERE id=$2", encrypted, raceID); err != nil {
		t.Fatal(err)
	}
	raceCode, _ := totp(raceSecret, now.Unix()/30)
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{"username": "race-user", "password": "Viewer Password 123", "totpCode": raceCode})
			r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(string(body)))
			r.Header.Set("Origin", s.origin)
			r.Header.Set("Content-Type", "application/json")
			r.RemoteAddr = "127.0.0.2:4444"
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			statuses <- w.Code
		}()
	}
	wg.Wait()
	close(statuses)
	accepted, rejected := 0, 0
	for status := range statuses {
		if status == 200 {
			accepted++
		} else if status == 401 {
			rejected++
		} else {
			t.Fatalf("unexpected concurrent login status %d", status)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatal("concurrent TOTP replay accepted", accepted, rejected)
	}
	// Hold the user row while a request has already read its old session.
	// Revoke under that lock, then release it: the waiting request must recheck.
	fresh := &client{}
	call(fresh, "/login", map[string]string{"username": "viewer", "password": "Reset Password 123"}, 200)
	lock, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var viewerID int64
	if err = lock.QueryRow(ctx, "SELECT id FROM adtr.users WHERE username='viewer' FOR UPDATE").Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("GET", "/api/auth/me", nil)
		r.AddCookie(fresh.cookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		result <- w.Code
	}()
	observed := false
	for range 100 {
		var waiting int
		e = admin.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND wait_event_type='Lock'", dbName).Scan(&waiting)
		if e != nil {
			t.Fatal(e)
		}
		if waiting > 0 {
			observed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		_ = lock.Rollback(ctx)
		t.Fatal("did not observe concurrent authentication waiting on user lock")
	}
	if _, err = lock.Exec(ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", viewerID); err != nil {
		t.Fatal(err)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if status := <-result; status != 401 {
		t.Fatal("revoked waiting session accepted", status)
	}
	// Database-persisted rate limits survive constructing another service instance.
	s2, err := New(cfg, s.key, s.origin, true)
	if err != nil {
		t.Fatal(err)
	}
	s2.now = s.now
	for i := 0; i < 10; i++ {
		if e := s2.rate(ctx, "isolated-limit", 10); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.rate(ctx, "isolated-limit", 10); e == nil {
		t.Fatal("persistent rate limit missing")
	}
	t.Log(fmt.Sprintf("Verified durable authentication lifecycle with %s audit records", strconv.Itoa(audits)))
}
