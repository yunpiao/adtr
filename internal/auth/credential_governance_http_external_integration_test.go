//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/store"
)

const (
	governanceHTTPTenant   = "governance-http"
	governanceHTTPRole     = "rrrrrrrrrrrrrrrrrrrrrrrr"
	governanceHTTPDelegate = "dddddddddddddddddddddddd"
	governanceHTTPEmpty    = "eeeeeeeeeeeeeeeeeeeeeeee"
	governanceHTTPAccount  = "synthetic-http-account"
	governanceHTTPPassword = "Synthetic HTTP Password 123"
	governanceHTTPSecret   = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	governanceHTTPOrigin   = "http://localhost:8080"
)

type governanceHTTPClient struct {
	id     int64
	cookie *http.Cookie
	csrf   string
}

type governanceHTTPFixture struct {
	t       *testing.T
	ctx     context.Context
	conn    *pgx.Conn
	handler http.Handler
	clients map[string]*governanceHTTPClient
}

// This external-package fixture deliberately imports the real migrator. No
// schema fragments, version markers, guard substitutions, or context setters
// stand in for the production proof -> transaction context -> SQL guard path.
func newGovernanceHTTPFixture(t *testing.T) *governanceHTTPFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: real PostgreSQL HTTP governance integration is blocked")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated PostgreSQL test configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	owner, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("isolated PostgreSQL unavailable", err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	name := fmt.Sprintf("adtr_governance_http_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := owner.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error("remove isolated HTTP governance database", err)
		}
	})
	cfg = cfg.Copy()
	cfg.Database = name
	if err = store.Migrate(ctx, cfg); err != nil {
		t.Fatal("install actual migrations", err)
	}
	if err = store.Ready(ctx, cfg); err != nil {
		t.Fatal("actual migrated schema is not ready", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	key := bytes.Repeat([]byte{'h'}, 32)
	s, err := auth.New(cfg, key, governanceHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Bootstrap(ctx, "synthetic-bootstrap", governanceHTTPPassword); err != nil {
		t.Fatal("create real synthetic password hash", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/auth/", s)
	mux.HandleFunc("/api/access/", s.ServeAccessHTTP)
	mux.HandleFunc("/api/resources/", s.ServeResources)
	mux.Handle("/api/credential-use/", s.CredentialUseHandler(credentialuse.New()))
	f := &governanceHTTPFixture{t: t, ctx: ctx, conn: conn, handler: mux, clients: map[string]*governanceHTTPClient{}}
	var guards int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname=ANY($1::text[])`, []string{
		"credential_use_user_guard", "credential_use_permissions", "credential_use_role_groups", "credential_use_group_members",
		"credential_use_tenant_guard", "credential_use_grant_guard", "credential_use_capture_members", "credential_use_capture_role_groups",
	}).Scan(&guards)
	if err != nil || guards != 8 {
		t.Fatal("actual cross-module governance guards are required", guards, err)
	}
	f.exec(`INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)
 VALUES($1,10,extract(epoch FROM clock_timestamp()+interval '1 year')::bigint,'synthetic-http','HTTP governance')`, governanceHTTPTenant)
	f.exec(`INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,'Credential role'),($1,$3,'Delegator'),($1,$4,'Empty credential role')`, governanceHTTPTenant, governanceHTTPRole, governanceHTTPDelegate, governanceHTTPEmpty)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT $1,r,mark,true,true FROM unnest(ARRAY[$2,$3,$4]::text[]) r CROSS JOIN unnest(ARRAY['domains','tasks','operation_accounts']) mark`, governanceHTTPTenant, governanceHTTPRole, governanceHTTPDelegate, governanceHTTPEmpty)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT $1,$2,mark,true,true FROM unnest(ARRAY['users','roles','permissions','audit','system']) mark`, governanceHTTPTenant, governanceHTTPDelegate)
	f.exec(`INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES($1,'domain-one','one.invalid'),($1,'domain-two','two.invalid')`, governanceHTTPTenant)
	f.exec(`INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port,credential_mode)
 VALUES($1,'domain-one','one.invalid','dc.one.invalid','starttls','389','unconfigured'),($1,'domain-two','two.invalid','dc.two.invalid','starttls','389','unconfigured')`, governanceHTTPTenant)
	f.exec(`INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES($1,'admin-group','AdminScope'),($1,'role-group','RoleScope'),($1,'delegate-group','DelegateScope'),($1,'empty-group','EmptyScope')`, governanceHTTPTenant)
	f.exec(`INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES
 ($1,'admin-group','domain-one'),($1,'role-group','domain-one'),($1,'delegate-group','domain-one'),($1,'delegate-group','domain-two'),($1,'empty-group','domain-one')`, governanceHTTPTenant)
	f.exec(`INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,'platform_admin','admin-group'),($1,$2,'role-group'),($1,$3,'delegate-group'),($1,$4,'empty-group')`, governanceHTTPTenant, governanceHTTPRole, governanceHTTPDelegate, governanceHTTPEmpty)

	// These are synthetic authenticated-session fixtures, not login coverage.
	// Every proof actor starts with an unused TOTP step, and all sensitive user
	// setup precedes the first allow. No protected replay counter is reset.
	for _, name := range []string{"grant", "grant-admin", "grant-empty", "create", "assign", "reactivate", "permissions", "roles", "resource", "reset", "tenant", "revoke", "revoke-admin", "delete-role"} {
		f.seedActor(name, "platform_admin", "", false, false, false, true, key)
	}
	f.seedActor("delegate", "viewer", governanceHTTPDelegate, false, false, false, true, key)
	f.seedActor("member", "viewer", governanceHTTPRole, false, false, false, true, key)
	f.seedActor("assignment-target", "viewer", "", false, false, false, true, key)
	f.seedActor("disabled-member", "viewer", governanceHTTPRole, true, false, false, true, key)
	f.seedActor("reset-target", "viewer", governanceHTTPRole, false, false, false, true, key)
	f.seedActor("forced-member", "viewer", governanceHTTPRole, false, true, false, true, key)
	f.seedActor("expired-member", "viewer", governanceHTTPRole, false, false, true, true, key)
	f.seedActor("enroll-member", "viewer", governanceHTTPRole, false, false, false, false, key)
	f.seedActor("disable-member", "viewer", governanceHTTPRole, false, false, false, true, key)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, q := range []string{
		`INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES($1,'domain-one',$2,'Synthetic HTTP account')`,
		`INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) VALUES($1,'domain-one',$2,1,'unavailable-synthetic-key',decode(repeat('ab',32),'hex'))`,
		`INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES($1,'domain-one','operation_accounts',$2)`,
	} {
		if _, err = tx.Exec(ctx, q, governanceHTTPTenant, governanceHTTPAccount); err != nil {
			t.Fatal("seed complete synthetic account", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("commit synthetic account with actual deferred guards", err)
	}
	return f
}

func (f *governanceHTTPFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.Exec(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *governanceHTTPFixture) count(q string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.conn.QueryRow(f.ctx, q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *governanceHTTPFixture) seedActor(name, role, custom string, disabled, forced, expired, mfa bool, key []byte) {
	f.t.Helper()
	c := &governanceHTTPClient{}
	err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,disabled,must_change,password_updated_at)
 SELECT $1,$2,password_hash,$3,$4,$5,$6,clock_timestamp()-CASE WHEN $7 THEN interval '100 days' ELSE interval '1 minute' END
 FROM adtr.users WHERE username='synthetic-bootstrap' RETURNING id`, governanceHTTPTenant, name, role, custom, disabled, forced, expired).Scan(&c.id)
	if err != nil {
		f.t.Fatal("seed synthetic proof actor", err)
	}
	if mfa {
		// Exact production seal format: nonce-prefixed AES-GCM, raw standard
		// base64, and decimal user ID as associated data.
		block, err := aes.NewCipher(key)
		if err != nil {
			f.t.Fatal(err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			f.t.Fatal(err)
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			f.t.Fatal(err)
		}
		sealed := base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(governanceHTTPSecret), []byte(strconv.FormatInt(c.id, 10))))
		f.exec(`UPDATE adtr.users SET mfa_secret=$1 WHERE id=$2`, sealed, c.id)
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		f.t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	hash := sha256.Sum256([]byte(token))
	f.exec(`INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, hex.EncodeToString(hash[:]), c.id)
	c.cookie = &http.Cookie{Name: "adtr_session", Value: token}
	f.clients[name] = c
	if !disabled {
		f.call(c, "/api/auth/me", nil, http.StatusOK)
		if c.csrf == "" {
			f.t.Fatal("real /me did not provide CSRF")
		}
	}
}

func governanceHTTPCode(t *testing.T, secret string) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var step [8]byte
	binary.BigEndian.PutUint64(step[:], uint64(time.Now().Unix()/30))
	h := hmac.New(sha1.New, key)
	_, _ = h.Write(step[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1000000)
}

func (f *governanceHTTPFixture) call(c *governanceHTTPClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method := http.MethodGet
	var raw []byte
	if body != nil {
		method = http.MethodPost
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(f.ctx)
	r.RemoteAddr = "127.0.0.1:8754"
	r.Header.Set("Origin", governanceHTTPOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(c.cookie)
	r.Header.Set("X-CSRF-Token", c.csrf)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal("HTTP response was not a JSON object", err)
	}
	if w.Code != want {
		f.t.Fatalf("%s: got HTTP %d, want %d (error=%v)", path, w.Code, want, out["error"])
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("sensitive response is cacheable")
	}
	if strings.HasPrefix(path, "/api/credential-use/") && want == http.StatusOK {
		if w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(c.id, 10) || out["consumerEnabled"] != true {
			f.t.Fatal("credential-use result lost actor binding or omitted the compiled consumer capability")
		}
	}
	if want >= 400 && (len(out) != 1 || out["error"] == nil) {
		f.t.Fatal("error response is not a safe error code")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "adtr_session" {
			c.cookie = cookie
		}
	}
	if csrf, ok := out["csrfToken"].(string); ok {
		c.csrf = csrf
	}
	return out
}

func (f *governanceHTTPFixture) mutate(actor, path string, body map[string]any, status int) map[string]any {
	f.t.Helper()
	body["actorPassword"] = governanceHTTPPassword
	body["totpCode"] = governanceHTTPCode(f.t, governanceHTTPSecret)
	return f.call(f.clients[actor], path, body, status)
}

func governanceHTTPUseBody(role, revision, key string) map[string]any {
	return map[string]any{"accountId": governanceHTTPAccount, "roleId": role, "purpose": credentialuse.Purpose,
		"expectedAccountRevision": "1", "expectedCredentialRevision": "1", "expectedGrantRevision": revision, "idempotencyKey": key}
}

func (f *governanceHTTPFixture) allow(actor, role string) string {
	f.t.Helper()
	before := f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, f.clients["member"].id)
	out := f.mutate(actor, "/api/credential-use/grant", governanceHTTPUseBody(role, "0", "allow-"+actor), http.StatusOK)
	revision, ok := out["grantRevision"].(string)
	if !ok || revision == "0" || out["allowed"] != true || out["replayed"] != false {
		f.t.Fatal("real grant route did not create an explicit allow")
	}
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients[actor].id) < 0 || f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, f.clients["member"].id) <= before {
		f.t.Fatal("grant did not consume proof and invalidate durable authority")
	}
	return revision
}

func (f *governanceHTTPFixture) denied(out map[string]any) {
	f.t.Helper()
	if out["error"] != "credential_use_governance_required" {
		f.t.Fatal("expected installed SQL governance guard rejection", out["error"])
	}
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["delegate"].id) != -1 {
		f.t.Fatal("denied mutation consumed the custom actor's proof")
	}
}

func governanceHTTPPermissions(extra string) []map[string]any {
	permissions := []map[string]any{}
	for _, mark := range []string{"domains", "tasks", "operation_accounts", extra} {
		permissions = append(permissions, map[string]any{"mark": mark, "auth": map[string]bool{"readable": true, "writeable": true}})
	}
	return permissions
}

func governanceHTTPMeta(name string, domains ...string) map[string]any {
	return map[string]any{"name": name, "mark": "synthetic", "datas": []map[string]any{{"appName": "ad", "resources": domains}}}
}

func TestCredentialGovernanceMigratedHTTPGrantReadAndCleanup(t *testing.T) {
	f := newGovernanceHTTPFixture(t)
	for _, actor := range []string{"grant", "member"} {
		out := f.call(f.clients[actor], "/api/credential-use/effective?accountId="+governanceHTTPAccount, nil, 200)
		if out["explicitlyGranted"] != false || out["eligible"] != false || out["grantRevision"] != "0" {
			t.Fatal("normal rights invented credential authority")
		}
	}
	revision := f.allow("grant", governanceHTTPRole)
	adminRevision := f.allow("grant-admin", "platform_admin")
	grants := f.call(f.clients["grant"], "/api/credential-use/grants?accountId="+governanceHTTPAccount, nil, 200)["grants"].([]any)
	if len(grants) != 2 {
		t.Fatal("actual grant catalogue lost explicit roles")
	}
	for _, actor := range []string{"grant", "member"} {
		out := f.call(f.clients[actor], "/api/credential-use/effective?accountId="+governanceHTTPAccount, nil, 200)
		if out["explicitlyGranted"] != true || out["eligible"] != true || out["accountCredentialRevision"] != "1" {
			t.Fatal("committed explicit allow is not effective")
		}
	}
	step := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["grant"].id)
	replay := f.mutate("grant", "/api/credential-use/grant", governanceHTTPUseBody(governanceHTTPRole, "0", "allow-grant"), 200)
	if replay["replayed"] != true || replay["grantRevision"] != revision || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["grant"].id) != step {
		t.Fatal("durable replay consumed proof or changed the result")
	}
	for _, tc := range []struct{ actor, role, revision string }{{"revoke", governanceHTTPRole, revision}, {"revoke-admin", "platform_admin", adminRevision}} {
		out := f.mutate(tc.actor, "/api/credential-use/revoke", governanceHTTPUseBody(tc.role, tc.revision, "revoke-"+tc.actor), 200)
		if out["allowed"] != false || out["grantRevision"] == tc.revision {
			t.Fatal("HTTP cleanup did not revoke existing authority")
		}
	}
	if f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE allowed`) != 0 || f.count(`SELECT count(*) FROM adtr.tasks`) != 0 || f.count(`SELECT count(*) FROM adtr.operation_account_dependencies`) != 0 {
		t.Fatal("B1 cleanup left allows or admitted a consumer")
	}
}

func TestCredentialGovernanceMigratedHTTPAccessMutations(t *testing.T) {
	f := newGovernanceHTTPFixture(t)
	f.allow("grant", governanceHTTPRole)
	for _, tc := range []struct {
		actor, path string
		body        map[string]any
	}{
		{"create", "/api/access/users/create", map[string]any{"username": "new-protected-user", "password": "Synthetic New User Password 123", "roleID": governanceHTTPRole}},
		{"assign", "/api/access/assignments", map[string]any{"userRoles": []map[string]string{{"username": "assignment-target", "roleID": governanceHTTPRole}}}},
		{"reactivate", "/api/access/users/update", map[string]any{"username": "disabled-member", "disabled": false}},
		{"permissions", "/api/access/permissions/save", map[string]any{"roleID": governanceHTTPRole, "permissions": governanceHTTPPermissions("audit")}},
		{"roles", "/api/access/roles/save", map[string]any{"roleID": governanceHTTPRole, "roleName": "Credential role", "remark": "protected replacement", "permissions": governanceHTTPPermissions("system")}},
	} {
		t.Run(tc.actor, func(t *testing.T) {
			parent := f.t
			f.t = t
			defer func() { f.t = parent }()
			f.denied(f.mutate("delegate", tc.path, tc.body, 403))
			f.mutate(tc.actor, tc.path, tc.body, 200)
			if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients[tc.actor].id) < 0 {
				t.Fatal("successful HTTP management did not consume proof")
			}
		})
	}
	if f.count(`SELECT count(*) FROM adtr.users WHERE tenant_id=$1 AND username IN ('new-protected-user','assignment-target','disabled-member') AND role_id=$2 AND NOT disabled`, governanceHTTPTenant, governanceHTTPRole) != 3 {
		t.Fatal("HTTP user creation, assignment, or reactivation did not persist")
	}
	if f.count(`SELECT count(*) FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark IN ('domains','tasks','operation_accounts','system') AND readable AND writeable`, governanceHTTPTenant, governanceHTTPRole) != 4 {
		t.Fatal("guarded permission delete/reinsert did not persist")
	}
	// A separate pre-migration-style connection never gets HTTP provenance.
	// Test after successful handlers as well: authority must not become global.
	for _, q := range []string{
		`UPDATE adtr.users SET disabled=false WHERE tenant_id=$1 AND username='disabled-member'`,
		`UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id=$1 AND role_id='rrrrrrrrrrrrrrrrrrrrrrrr' AND mark='tasks'`,
		`DELETE FROM adtr.resource_group_members WHERE tenant_id=$1 AND group_id='role-group'`,
	} {
		// Reactivation must really change state to exercise the first guard.
		if strings.Contains(q, "disabled=false") {
			f.exec(`UPDATE adtr.users SET disabled=true WHERE tenant_id=$1 AND username='disabled-member'`, governanceHTTPTenant)
		}
		_, err := f.conn.Exec(f.ctx, q, governanceHTTPTenant)
		if !credentialuse.IsGovernanceError(err) {
			t.Fatal("context-free old-style write did not fail closed", err)
		}
	}
	f.allow("grant-empty", governanceHTTPEmpty)
	f.denied(f.mutate("delegate", "/api/access/roles/delete", map[string]any{"roleID": governanceHTTPEmpty}, 403))
	f.mutate("delete-role", "/api/access/roles/delete", map[string]any{"roleID": governanceHTTPEmpty}, 200)
	if f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE role_id=$1`, governanceHTTPEmpty) != 0 || f.count(`SELECT count(*) FROM adtr.operation_account_use_audit WHERE role_id=$1 AND action='credential_use_role_deleted'`, governanceHTTPEmpty) != 1 {
		t.Fatal("HTTP role deletion lost protected cascade history")
	}
}

func TestCredentialGovernanceMigratedHTTPResourceReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, path, group string
		adminGrant        bool
		body              map[string]any
	}{
		{"role-bindings", "/api/resources/groups/assign", "role-group", false, map[string]any{"id": "role-group", "roleIds": []string{governanceHTTPRole, "viewer"}}},
		{"group-members", "/api/resources/groups/update", "role-group", false, map[string]any{"id": "role-group", "meta": governanceHTTPMeta("RoleScope", "domain-one", "domain-two")}},
		{"admin-bindings", "/api/resources/groups/assign", "admin-group", true, map[string]any{"id": "admin-group", "roleIds": []string{"platform_admin", "viewer"}}},
		{"admin-members", "/api/resources/groups/update", "admin-group", true, map[string]any{"id": "admin-group", "meta": governanceHTTPMeta("AdminScope", "domain-one", "domain-two")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGovernanceHTTPFixture(t)
			f.allow("grant", governanceHTTPRole)
			if tc.adminGrant {
				f.allow("grant-admin", "platform_admin")
			} else {
				f.denied(f.mutate("delegate", tc.path, tc.body, 403))
				f.denied(f.mutate("delegate", "/api/resources/groups/delete", map[string]any{"id": "role-group"}, 403))
			}
			out := f.mutate("resource", tc.path, tc.body, 200)
			if out["sessionRevoked"] != tc.adminGrant {
				t.Fatal("resource replacement lost expected actor session revocation")
			}
			var count int64
			if strings.HasSuffix(tc.path, "/assign") {
				count = f.count(`SELECT count(*) FROM adtr.resource_role_groups WHERE tenant_id=$1 AND group_id=$2`, governanceHTTPTenant, tc.group)
			} else {
				count = f.count(`SELECT count(*) FROM adtr.resource_group_members WHERE tenant_id=$1 AND group_id=$2`, governanceHTTPTenant, tc.group)
			}
			if count != 2 || f.count(`SELECT count(*) FROM adtr.resource_group_members m JOIN adtr.resource_role_groups r USING(tenant_id,group_id) WHERE m.tenant_id=$1 AND m.domain_id='domain-one' AND r.role_id='platform_admin'`, governanceHTTPTenant) != 1 {
				t.Fatal("authorized replacement lost final administrator scope")
			}
			_, err := f.conn.Exec(f.ctx, `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, governanceHTTPTenant, governanceHTTPRole)
			if !credentialuse.IsGovernanceError(err) {
				t.Fatal("HTTP replacement leaked authority to context-free SQL", err)
			}
		})
	}
}

func TestCredentialGovernanceMigratedHTTPResetAndSelfService(t *testing.T) {
	f := newGovernanceHTTPFixture(t)
	revision := f.allow("grant", governanceHTTPRole)
	_, err := f.conn.Exec(f.ctx, `UPDATE adtr.users SET password_hash='old-style-synthetic-hash',must_change=true WHERE tenant_id=$1 AND username='reset-target'`, governanceHTTPTenant)
	if !credentialuse.IsGovernanceError(err) {
		t.Fatal("old-style password reset did not fail closed", err)
	}
	reset := map[string]any{"username": "reset-target", "newPassword": "Synthetic Reset Password 123", "password": governanceHTTPPassword, "totpCode": governanceHTTPCode(t, governanceHTTPSecret)}
	if out := f.call(f.clients["delegate"], "/api/auth/reset-password", reset, 403); out["error"] != "forbidden" {
		t.Fatal("custom actor reset was not rejected")
	}
	reset["totpCode"] = governanceHTTPCode(t, governanceHTTPSecret)
	f.call(f.clients["reset"], "/api/auth/reset-password", reset, 200)
	if f.count(`SELECT count(*) FROM adtr.users WHERE username='reset-target' AND must_change`) != 1 || f.count(`SELECT count(*) FROM adtr.sessions WHERE user_id=$1`, f.clients["reset-target"].id) != 0 {
		t.Fatal("guarded administrator reset did not force change and revoke sessions")
	}
	for _, name := range []string{"forced-member", "expired-member"} {
		c := f.clients[name]
		old := *c
		before := f.call(c, "/api/auth/me", nil, 200)
		if before["needChangePwd"] != true || name == "expired-member" && before["isExpired"] != true {
			t.Fatal("password recovery fixture is not forced or expired")
		}
		f.call(c, "/api/auth/password", map[string]any{"oldPassword": "wrong-password", "newPassword": "Synthetic Changed Password 123"}, 401)
		out := f.call(c, "/api/auth/password", map[string]any{"oldPassword": governanceHTTPPassword, "newPassword": "Synthetic Changed Password 123"}, 200)
		if out["needChangePwd"] != false || out["isExpired"] != false || c.cookie.Value == old.cookie.Value || c.csrf == old.csrf {
			t.Fatal("protected self password recovery did not clear the gate and rotate the session")
		}
		f.call(&old, "/api/auth/me", nil, 401)
		f.call(c, "/api/auth/me", nil, 200)
	}
	enroll := f.clients["enroll-member"]
	f.call(enroll, "/api/auth/mfa/enroll", map[string]any{"password": "wrong-password"}, 401)
	begin := f.call(enroll, "/api/auth/mfa/enroll", map[string]any{"password": governanceHTTPPassword}, 200)
	secret, ok := begin["secret"].(string)
	if !ok || secret == "" || f.count(`SELECT count(*) FROM adtr.users WHERE id=$1 AND mfa_secret='' AND mfa_pending<>'' AND mfa_pending<>$2 AND mfa_pending_until>clock_timestamp()`, enroll.id, secret) != 1 {
		t.Fatal("protected MFA begin did not persist an encrypted pending secret")
	}
	oldEnroll := *enroll
	f.call(enroll, "/api/auth/mfa/confirm", map[string]any{"password": "wrong-password", "secret": secret, "mfaCode": governanceHTTPCode(t, secret)}, 401)
	confirmed := f.call(enroll, "/api/auth/mfa/confirm", map[string]any{"password": governanceHTTPPassword, "secret": secret, "mfaCode": governanceHTTPCode(t, secret)}, 200)
	if confirmed["hasMfa"] != true || f.count(`SELECT count(*) FROM adtr.users WHERE id=$1 AND mfa_secret<>'' AND mfa_pending='' AND mfa_pending_until IS NULL AND mfa_last_step>=0`, enroll.id) != 1 {
		t.Fatal("protected MFA confirm did not consume and install pending proof")
	}
	f.call(&oldEnroll, "/api/auth/me", nil, 401)
	disable := f.clients["disable-member"]
	oldDisable := *disable
	f.call(disable, "/api/auth/mfa/disable", map[string]any{"password": "wrong-password", "mfaCode": governanceHTTPCode(t, governanceHTTPSecret)}, 401)
	disabled := f.call(disable, "/api/auth/mfa/disable", map[string]any{"password": governanceHTTPPassword, "mfaCode": governanceHTTPCode(t, governanceHTTPSecret)}, 200)
	if disabled["hasMfa"] != false || f.count(`SELECT count(*) FROM adtr.users WHERE id=$1 AND mfa_secret='' AND mfa_pending='' AND mfa_pending_until IS NULL AND mfa_last_step=-1`, disable.id) != 1 {
		t.Fatal("protected MFA disable did not clear MFA state")
	}
	f.call(&oldDisable, "/api/auth/me", nil, 401)
	if f.count(`SELECT count(*) FROM adtr.users WHERE username IN ('forced-member','expired-member','enroll-member','disable-member','reset-target') AND tenant_id=$1 AND role_id=$2 AND NOT disabled`, governanceHTTPTenant, governanceHTTPRole) != 5 || f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE role_id=$1 AND allowed AND grant_revision::text=$2`, governanceHTTPRole, revision) != 1 {
		t.Fatal("self-service changed role membership or renewed credential authority")
	}
	if f.count(`SELECT count(*) FROM adtr.auth_audit WHERE tenant_id=$1 AND action IN ('password_reset','password_change','mfa_enroll_begin','mfa_enable','mfa_disable')`, governanceHTTPTenant) != 6 {
		t.Fatal("successful protected core-auth operations lost audit events")
	}
}

func TestCredentialGovernanceMigratedHTTPTenantEntitlement(t *testing.T) {
	f := newGovernanceHTTPFixture(t)
	f.allow("grant", governanceHTTPRole)
	_, err := f.conn.Exec(f.ctx, `UPDATE adtr.resource_tenant_config SET max_ad_count=11 WHERE tenant_id=$1`, governanceHTTPTenant)
	if !credentialuse.IsGovernanceError(err) {
		t.Fatal("old-style entitlement change did not fail closed", err)
	}
	f.mutate("tenant", "/api/resources/tenant/save", map[string]any{"maxAdCount": 11, "expireTime": time.Now().Add(366 * 24 * time.Hour).Unix(), "uid": "synthetic-http", "name": "HTTP governance"}, 200)
	if f.count(`SELECT max_ad_count FROM adtr.resource_tenant_config WHERE tenant_id=$1`, governanceHTTPTenant) != 11 {
		t.Fatal("proof-bound tenant context did not permit entitlement management")
	}
}
