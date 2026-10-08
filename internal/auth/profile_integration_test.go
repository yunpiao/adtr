//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

func profileIntegrationImage(t *testing.T, c color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, c)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func profileCall(t *testing.T, f *accessFixture, client *accessTestClient, method, path string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:3456"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	if client != nil {
		if client.cookie != nil {
			r.AddCookie(client.cookie)
		}
		r.Header.Set("X-CSRF-Token", client.csrf)
	}
	w := httptest.NewRecorder()
	f.s.ServeProfileHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	if (want == 401 || want == 400 || want == 403) && w.Header().Get("X-Profile-User-ID") != "" {
		t.Fatal("unauthenticated or rejected response leaked avatar owner")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing privacy headers")
	}
	return w
}
func profileFixture(t *testing.T) *accessFixture {
	t.Helper()
	f := newAccessFixture(t)
	f.sql(ProfileSchema)
	f.sql("CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL)")
	f.sql("INSERT INTO adtr.schema_version(singleton,version) VALUES(true,$1)", schemaversion.Current)
	return f
}
func profileID(t *testing.T, f *accessFixture, name string) int64 {
	t.Helper()
	var id int64
	if e := f.conn.QueryRow(f.ctx, "SELECT id FROM adtr.users WHERE username=$1", name).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}
func profileViewer(t *testing.T, f *accessFixture, name, tenant string) accessTestClient {
	t.Helper()
	hash, e := hashPassword("Synthetic viewer pass 123")
	if e != nil {
		t.Fatal(e)
	}
	f.sql("INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) VALUES($1,$2,$3,'viewer',false)", tenant, name, hash)
	var client accessTestClient
	f.call(&client, "/api/auth/login", map[string]any{"username": name, "password": "Synthetic viewer pass 123"}, 200)
	return client
}

func TestProfilePersistenceAndSelfScope(t *testing.T) {
	f := profileFixture(t)
	id := profileID(t, f, "admin")
	f.sql("UPDATE adtr.users SET real_name='Profile Person',department='Research',post='Analyst',address='Lab',mobile='13812345678',email='profile@example.test',remark='synthetic' WHERE id=$1", id)
	first := profileCall(t, f, &f.admin, "GET", "/api/profile/me", nil, 200)
	var p AccessUser
	if e := json.Unmarshal(first.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p.ID != id || p.Username != "admin" || p.RealName != "Profile Person" || p.Department != "Research" || p.Post != "Analyst" || p.Address != "Lab" || p.Mobile != "13812345678" || p.Email != "profile@example.test" || p.Remark != "synthetic" || p.RoleID != "platform_admin" || p.RoleName != "platform_admin" || p.Priv != 1 || !p.HasMFA || p.Avatar != "" || p.Created.IsZero() || p.PasswordUpdated.IsZero() {
		t.Fatalf("unexpected persisted profile: %+v", p)
	}
	for _, secret := range []string{"password_hash", "mfa_secret", "csrfToken", "passwordHash", "mfaPending"} {
		if strings.Contains(first.Body.String(), secret) {
			t.Fatalf("private field %s exposed", secret)
		}
	}
	missingAvatar := profileCall(t, f, &f.admin, "GET", "/api/profile/avatar", nil, 404)
	if missingAvatar.Header().Get("X-Profile-User-ID") != strconv.FormatInt(id, 10) {
		t.Fatal("empty avatar is not bound to authenticated identity")
	}
	one := profileIntegrationImage(t, color.NRGBA{R: 255, A: 255})
	two := profileIntegrationImage(t, color.NRGBA{G: 255, A: 255})
	for i, file := range []string{one, two} {
		uploaded := profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", map[string]any{"userId": id, "file": file}, 200)
		if strings.TrimSpace(uploaded.Body.String()) != `{"result":"success"}` {
			t.Fatal(uploaded.Body.String())
		}
		got := profileCall(t, f, &f.admin, "GET", "/api/profile/avatar", nil, 200)
		want, e := normalizeProfileAvatar(file)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(got.Body.Bytes(), want) || got.Header().Get("Content-Type") != "image/png" || got.Header().Get("X-Profile-User-ID") != strconv.FormatInt(id, 10) {
			t.Fatal("read does not match committed sanitized bytes")
		}
		var stored []byte
		if e = f.conn.QueryRow(f.ctx, "SELECT png FROM adtr.profile_avatars WHERE user_id=$1 AND tenant_id='default'", id).Scan(&stored); e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(stored, want) {
			t.Fatal("storage mismatch")
		}
		if n := f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='profile_avatar_update' AND actor_id=$1 AND target_id=$1 AND tenant_id='default'", id); n != i+1 {
			t.Fatalf("audit count=%d", n)
		}
	}
	updated := profileCall(t, f, &f.admin, "GET", "/api/profile/me", nil, 200)
	if e := json.Unmarshal(updated.Body.Bytes(), &p); e != nil || p.Avatar != "/api/profile/avatar" {
		t.Fatalf("avatar reference not persisted: %s", updated.Body.String())
	}
	// Ordinary viewers can only access themselves; no management grant is needed.
	viewer := profileViewer(t, f, "profile-viewer", "default")
	viewerID := profileID(t, f, "profile-viewer")
	foreign := profileViewer(t, f, "foreign-viewer", "other-tenant")
	foreignID := profileID(t, f, "foreign-viewer")
	for _, c := range []*accessTestClient{&viewer, &foreign} {
		profileCall(t, f, c, "POST", "/api/profile/avatar", map[string]any{"userId": id, "file": one}, 403)
		profileCall(t, f, c, "GET", "/api/profile/avatar", nil, 404)
		profileCall(t, f, c, "GET", "/api/profile/me?userId=1", nil, 400)
	}
	profileCall(t, f, &viewer, "POST", "/api/profile/avatar", map[string]any{"userId": viewerID, "file": one}, 200)
	profileCall(t, f, &foreign, "POST", "/api/profile/avatar", map[string]any{"userId": foreignID, "file": two}, 200)
	for _, tt := range []struct {
		c    *accessTestClient
		id   int64
		file string
	}{{&viewer, viewerID, one}, {&foreign, foreignID, two}} {
		got := profileCall(t, f, tt.c, "GET", "/api/profile/avatar", nil, 200)
		want, _ := normalizeProfileAvatar(tt.file)
		if !bytes.Equal(got.Body.Bytes(), want) {
			t.Fatal("cross-tenant avatar returned")
		}
		got = profileCall(t, f, tt.c, "GET", "/api/profile/me", nil, 200)
		if e := json.Unmarshal(got.Body.Bytes(), &p); e != nil || p.ID != tt.id || p.Priv != 3 {
			t.Fatal("wrong self profile")
		}
	}
}

func TestProfileAuthenticationAndAtomicFailures(t *testing.T) {
	f := profileFixture(t)
	id := profileID(t, f, "admin")
	file := profileIntegrationImage(t, color.NRGBA{B: 255, A: 255})
	upload := map[string]any{"userId": id, "file": file}
	profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", upload, 200)
	bad := f.admin
	bad.csrf = "wrong"
	profileCall(t, f, &bad, "POST", "/api/profile/avatar", upload, 403)
	profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", map[string]any{"userId": id, "file": "not-an-image"}, 400)
	// Audit insertion failure must roll back the replacement, not merely return 500.
	f.sql(`CREATE FUNCTION adtr.profile_test_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='profile_avatar_update' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER profile_test_fail BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.profile_test_reject_audit();`)
	changed := profileIntegrationImage(t, color.NRGBA{R: 255, A: 255})
	profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", map[string]any{"userId": id, "file": changed}, 500)
	got := profileCall(t, f, &f.admin, "GET", "/api/profile/avatar", nil, 200)
	want, _ := normalizeProfileAvatar(file)
	if !bytes.Equal(got.Body.Bytes(), want) {
		t.Fatal("failed transaction replaced avatar")
	}
	if f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='profile_avatar_update'") != 1 {
		t.Fatal("failed write left success audit")
	}
	f.sql("DROP TRIGGER profile_test_fail ON adtr.auth_audit")
	profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", map[string]any{"userId": id, "file": changed}, 200)
	// Existing identity helper rejects forced password changes and disabled users.
	f.sql("UPDATE adtr.users SET must_change=true WHERE id=$1", id)
	profileCall(t, f, &f.admin, "GET", "/api/profile/me", nil, 403)
	profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", upload, 403)
	f.sql("UPDATE adtr.users SET must_change=false,disabled=true WHERE id=$1", id)
	profileCall(t, f, &f.admin, "GET", "/api/profile/avatar", nil, 401)
	f.sql("UPDATE adtr.users SET disabled=false WHERE id=$1", id)
	f.sql("UPDATE adtr.sessions SET expires_at=$1 WHERE user_id=$2", f.now, id)
	profileCall(t, f, &f.admin, "GET", "/api/profile/me", nil, 401)
	// Re-login, then explicit revocation invalidates both read and write.
	f.now = f.now.Add(30 * time.Second)
	code, _ := totp(f.secret, f.now.Unix()/30)
	f.call(&f.admin, "/api/auth/login", map[string]any{"username": "admin", "password": f.password, "totpCode": code}, 200)
	f.sql("DELETE FROM adtr.sessions WHERE user_id=$1", id)
	profileCall(t, f, &f.admin, "GET", "/api/profile/avatar", nil, 401)
	profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", upload, 401)
}

func TestProfileMissingMigrationAndStorageIsolation(t *testing.T) {
	f := newAccessFixture(t)
	id := profileID(t, f, "admin")
	profileCall(t, f, &f.admin, "GET", "/api/profile/me", nil, 503)
	f.sql(ProfileSchema)
	f.sql("CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL)")
	f.sql("INSERT INTO adtr.schema_version(singleton,version) VALUES(true,$1)", schemaversion.Current)
	file := profileIntegrationImage(t, color.NRGBA{R: 255, A: 255})
	raw, _ := normalizeProfileAvatar(file)
	// Corrupt tenant binding cannot be read or overwritten by the self endpoint.
	f.sql("INSERT INTO adtr.profile_avatars(user_id,tenant_id,png,updated_at) VALUES($1,'wrong-tenant',$2,$3)", id, raw, f.now)
	profileCall(t, f, &f.admin, "GET", "/api/profile/avatar", nil, 404)
	profileCall(t, f, &f.admin, "POST", "/api/profile/avatar", map[string]any{"userId": id, "file": file}, 409)
	if f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='profile_avatar_update'") != 0 {
		t.Fatal("conflict recorded success")
	}
}

func TestProfileConcurrentRevocationAfterSessionLookup(t *testing.T) {
	f := profileFixture(t)
	id := profileID(t, f, "admin")
	gateConn, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer gateConn.Close(f.ctx)
	gate, err := gateConn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(f.ctx)
	if err = lockIdentityTenant(f.ctx, gate, "default"); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"userId": id, "file": profileIntegrationImage(t, color.NRGBA{B: 255, A: 255})})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan int, 2)
	for _, method := range []string{"GET", "POST"} {
		go func(method string) {
			r := httptest.NewRequest(method, "/api/profile/avatar", bytes.NewReader(raw))
			r.RemoteAddr = "127.0.0.1:3456"
			r.Header.Set("Origin", f.s.origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", f.admin.csrf)
			r.AddCookie(f.admin.cookie)
			w := httptest.NewRecorder()
			f.s.ServeProfileHTTP(w, r)
			results <- w.Code
		}(method)
	}
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if f.count("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory'") >= 2 {
			blocked = true
			break
		}
		select {
		case status := <-results:
			t.Fatalf("profile bypassed shared tenant lock, status=%d", status)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !blocked {
		t.Fatal("profile requests did not reach the shared lock")
	}
	if _, err = gate.Exec(f.ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case status := <-results:
			if status != 401 {
				t.Fatalf("stale session returned status %d", status)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("profile request did not finish")
		}
	}
	if f.count("SELECT count(*) FROM adtr.profile_avatars") != 0 || f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='profile_avatar_update'") != 0 {
		t.Fatal("revoked request mutated avatar or success audit")
	}
}

func TestProfileRevocationDuringUnlockedImageProcessing(t *testing.T) {
	f := profileFixture(t)
	id := profileID(t, f, "admin")
	raw, err := json.Marshal(map[string]any{"userId": id, "file": profileIntegrationImage(t, color.NRGBA{G: 255, A: 255})})
	if err != nil {
		t.Fatal(err)
	}
	request := func(csrf string) *http.Request {
		r := httptest.NewRequest("POST", "/api/profile/avatar", bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:3456"
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(f.admin.cookie)
		return r
	}
	// Authentication and CSRF must finish before any expensive decoder call.
	decoded := false
	bad := httptest.NewRecorder()
	f.s.serveProfileHTTP(bad, request("wrong"), func(file string) ([]byte, error) { decoded = true; return normalizeProfileAvatar(file) })
	if bad.Code != 403 || decoded {
		t.Fatal("unauthorized request reached image processing")
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	released := false
	unblock := func() {
		if !released {
			close(resume)
			released = true
		}
	}
	defer unblock()
	result := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		f.s.serveProfileHTTP(w, request(f.admin.csrf), func(file string) ([]byte, error) { close(entered); <-resume; return normalizeProfileAvatar(file) })
		result <- w.Code
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("authorized request did not reach decoder")
	}
	// The mutation below needs both locks. A decoder running inside its first
	// transaction would block here and fail the deadline instead of passing.
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(f.ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if err = lockIdentityTenant(ctx, tx, "default"); err != nil {
		t.Fatalf("decoder retained tenant lock: %v", err)
	}
	var lockedID int64
	if err = tx.QueryRow(ctx, "SELECT id FROM adtr.users WHERE id=$1 FOR UPDATE", id).Scan(&lockedID); err != nil {
		t.Fatalf("decoder retained user lock: %v", err)
	}
	if _, err = tx.Exec(ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case status := <-result:
		if status != 401 {
			t.Fatalf("revoked-during-decode upload status=%d", status)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("upload did not finish")
	}
	if f.count("SELECT count(*) FROM adtr.profile_avatars") != 0 || f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='profile_avatar_update'") != 0 {
		t.Fatal("revoked-during-decode upload committed")
	}
}

func TestProfileSchemaGatePrecedesRateAndImageProcessing(t *testing.T) {
	f := profileFixture(t)
	id := profileID(t, f, "admin")
	file := profileIntegrationImage(t, color.NRGBA{R: 255, A: 255})
	raw, _ := json.Marshal(map[string]any{"userId": id, "file": file})
	for _, version := range []int{schemaversion.Current - 1, schemaversion.Current + 1, 0, -1} {
		f.sql("UPDATE adtr.schema_version SET version=$1", version)
		if version == 0 {
			f.sql("DELETE FROM adtr.schema_version")
		}
		if version == -1 {
			f.sql("DROP TABLE adtr.schema_version")
		}
		beforeRate := f.count("SELECT COALESCE(sum(count),0) FROM adtr.auth_attempts")
		beforeAudit := f.count("SELECT count(*) FROM adtr.auth_audit")
		r := httptest.NewRequest("POST", "/api/profile/avatar", bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:3456"
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", f.admin.csrf)
		r.AddCookie(f.admin.cookie)
		w := httptest.NewRecorder()
		decoded := false
		f.s.serveProfileHTTP(w, r, func(string) ([]byte, error) { decoded = true; return nil, nil })
		if w.Code != 503 || decoded || w.Header().Get("X-Profile-User-ID") != "" {
			t.Fatalf("incompatible schema reached profile operation: %d", w.Code)
		}
		if f.count("SELECT COALESCE(sum(count),0) FROM adtr.auth_attempts") != beforeRate || f.count("SELECT count(*) FROM adtr.auth_audit") != beforeAudit || f.count("SELECT count(*) FROM adtr.profile_avatars") != 0 {
			t.Fatal("schema rejection mutated state")
		}
	}
}

func TestProfileSchemaChangeDuringUnlockedDecode(t *testing.T) {
	f := profileFixture(t)
	id := profileID(t, f, "admin")
	raw, _ := json.Marshal(map[string]any{"userId": id, "file": profileIntegrationImage(t, color.NRGBA{G: 255, A: 255})})
	r := httptest.NewRequest("POST", "/api/profile/avatar", bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:3456"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", f.admin.csrf)
	r.AddCookie(f.admin.cookie)
	w := httptest.NewRecorder()
	decoded := false
	f.s.serveProfileHTTP(w, r, func(file string) ([]byte, error) {
		decoded = true
		tx, err := f.conn.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		var locked bool
		if err = tx.QueryRow(f.ctx, "SELECT pg_try_advisory_xact_lock(734192801)").Scan(&locked); err != nil || !locked {
			t.Fatal("decoder retains migration lock", err)
		}
		if _, err = tx.Exec(f.ctx, "UPDATE adtr.schema_version SET version=$1", schemaversion.Current+1); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		return normalizeProfileAvatar(file)
	})
	if !decoded || w.Code != 503 {
		t.Fatalf("second schema gate not enforced: %d", w.Code)
	}
	if f.count("SELECT count(*) FROM adtr.profile_avatars") != 0 || f.count("SELECT count(*) FROM adtr.auth_audit WHERE action='profile_avatar_update'") != 0 {
		t.Fatal("schema race committed image or success audit")
	}
}

func TestProfileAuditCapturesTrustedMetadata(t *testing.T) {
	f := profileFixture(t)
	f.sql(TaskAuthorizationSchema + tasks.Schema + audit.Schema)
	id := profileID(t, f, "admin")
	file := profileIntegrationImage(t, color.NRGBA{R: 255, A: 255})
	raw, _ := json.Marshal(map[string]any{"userId": id, "file": file})
	r := httptest.NewRequest("POST", "/api/profile/avatar", bytes.NewReader(raw))
	r.RemoteAddr = "192.0.2.42:1234"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", f.admin.csrf)
	r.Header.Set("X-Forwarded-For", "198.51.100.99")
	r.Header.Set("X-Request-ID", "untrusted-client-id")
	r.AddCookie(f.admin.cookie)
	w := httptest.NewRecorder()
	f.s.ServeProfileHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var username, ip, path, requestID, result, args string
	var actor, target int64
	if err := f.conn.QueryRow(f.ctx, `SELECT a.audit_username,a.audit_ip,a.audit_path,a.audit_request_id,a.actor_id,a.target_id,s.event_result,s.event_args::text FROM adtr.auth_audit a JOIN adtr.audit_source s ON s.source='auth' AND s.source_id=a.id WHERE a.action='profile_avatar_update'`).Scan(&username, &ip, &path, &requestID, &actor, &target, &result, &args); err != nil {
		t.Fatal(err)
	}
	if username != "admin" || ip != "192.0.2.42" || path != "/api/profile/avatar" || requestID == "" || requestID == "untrusted-client-id" || actor != id || target != id || result != "SUCCESS" {
		t.Fatalf("incorrect trusted audit: %s %s %s %s %d %d %s", username, ip, path, requestID, actor, target, result)
	}
	if strings.Contains(args, file) || strings.Contains(args, "untrusted-client-id") || strings.Contains(args, "198.51.100.99") {
		t.Fatal("audit contains untrusted input")
	}
}
