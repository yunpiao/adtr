//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const (
	directoryHTTPWriterRole = "directory-http-writer-01"
	directoryHTTPReaderRole = "directory-http-reader-01"
)

type directoryHTTPFixture struct {
	*governanceHTTPFixture
	service *auth.Service
	store   *domains.Store
	engine  *tasks.Engine
	runtime *domainconfig.Runtime
}

// Reuse the actual migrator, installed guards, hashed passwords, encrypted MFA,
// and authenticated /me transport fixture. A missing DSN is a hard setup failure.
// This fixture deliberately has no usable operation credential or LDAP server.
func newDirectoryHTTPFixture(t *testing.T) *directoryHTTPFixture {
	t.Helper()
	base := newGovernanceHTTPFixture(t)
	f := &directoryHTTPFixture{governanceHTTPFixture: base}
	key := bytes.Repeat([]byte{'h'}, 32)
	// Actor names are descriptive; custom role IDs must satisfy the production
	// access API's 24-character grammar before permission and scope checks run.
	roles := []struct {
		actor, id string
		grants    map[string]auth.AccessAuth
	}{
		{directoryHTTPWriterRole, directoryHTTPWriterRole, map[string]auth.AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}},
		{"directory-http-reader", directoryHTTPReaderRole, map[string]auth.AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true}}},
		{"directory-http-no-assets", "directory-http-no-assets", map[string]auth.AccessAuth{"domains": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}},
		{"directory-http-no-domains", "directory-http-no-domain", map[string]auth.AccessAuth{"directory_assets": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}},
		{"directory-http-no-scope", "directory-http-no-scope0", map[string]auth.AccessAuth{"domains": {Readable: true}, "directory_assets": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}},
	}
	for _, role := range roles {
		f.exec(`INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,$2)`, governanceHTTPTenant, role.id)
		for mark, grant := range role.grants {
			f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,$3,$4,$5)`, governanceHTTPTenant, role.id, mark, grant.Readable, grant.Writeable)
		}
		if role.actor != "directory-http-no-scope" {
			f.exec(`INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,$2,'admin-group')`, governanceHTTPTenant, role.id)
		}
		f.seedActor(role.actor, "viewer", role.id, false, false, false, true, key)
	}
	for _, name := range []string{"directory-admin", "directory-admin-other", "directory-bind", "directory-grant", "directory-grant-writer", "directory-cancel", "directory-detach", "directory-revoke", "directory-submit-one", "directory-submit-two", "directory-submit-three"} {
		f.seedActor(name, "platform_admin", "", false, false, false, true, key)
	}
	f.seedActor("directory-no-mfa", "viewer", directoryHTTPWriterRole, false, false, false, false, key)
	// A separate entitled tenant has the same visible domain ID and hostname,
	// but no observations or credential grants. Identity must scope every join.
	foreign := "directory-foreign"
	f.exec(`INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES($1,10,extract(epoch FROM clock_timestamp()+interval '1 year')::bigint,$1,'Other HTTP tenant')`, foreign)
	f.exec(`INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES($1,'domain-one','one.invalid')`, foreign)
	f.exec(`INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port,credential_mode) VALUES($1,'domain-one','one.invalid','dc.one.invalid','starttls','389','unconfigured')`, foreign)
	f.exec(`INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES($1,'foreign-group','Other scope')`, foreign)
	f.exec(`INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,'foreign-group','domain-one')`, foreign)
	f.exec(`INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,'platform_admin','foreign-group')`, foreign)
	c := &governanceHTTPClient{}
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,password_updated_at) SELECT $1,$1,password_hash,'platform_admin','',false,clock_timestamp()-interval '1 minute' FROM adtr.users WHERE username='synthetic-bootstrap' RETURNING id`, foreign).Scan(&c.id); err != nil {
		t.Fatal("seed other tenant's actual authenticated identity", err)
	}
	f.clients[foreign] = c
	reissueSourceHTTPSession(t, base, c)
	var err error
	f.service, err = auth.New(f.conn.Config(), key, governanceHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime = directoryHTTPRuntime(t, "192.0.2.0/24", true)
	f.install(domains.New(f.runtime), nil)
	return f
}

func directoryHTTPRuntime(t *testing.T, cidr string, enabled bool) *domainconfig.Runtime {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ca, policy := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "policy.json")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(policy, []byte(`{"version":1,"targets":[{"tenantId":"governance-http","domain":"one.invalid","serverNames":["dc.one.invalid"],"cidrs":["`+cidr+`"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic-directory", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'d'}, 32)), "ADTR_DIRECTORY_READ_ENABLED": strconv.FormatBool(enabled), "ADTR_LDAP_CA_FILE": ca, "ADTR_LDAP_EGRESS_POLICY_FILE": policy}
	runtime, err := domainconfig.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func (f *directoryHTTPFixture) install(ds *domains.Store, synthetic tasks.Executor) {
	f.t.Helper()
	kind := ds.DirectoryKind()
	if synthetic != nil {
		// Only HTTP/stored-observation tests substitute Execute. Validation,
		// authorizer, engine fencing, SQL guards and OnQuiesced stay production.
		// This is not production LDAP executor coverage.
		kind.Execute = synthetic
	}
	engine, err := tasks.New(f.conn.Config(), tasks.ProductionRegistry(ds.Kind(), ds.AccountKind(), kind), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		f.t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/directory/", f.service.DirectoryHandler(ds, engine))
	mux.Handle("/api/directory-credential-use/", f.service.DirectoryCredentialUseHandler(credentialuse.New()))
	mux.Handle("/api/domains/", f.service.DomainsHandler(ds, engine))
	mux.Handle("/api/tasks", f.service.TasksHandler(engine))
	mux.Handle("/api/tasks/", f.service.TasksHandler(engine))
	mux.Handle("/", f.handler)
	f.handler, f.store, f.engine = mux, ds, engine
}

func (f *directoryHTTPFixture) raw(actor, method, path string, raw []byte, want int, edit func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(f.ctx)
	r.RemoteAddr = "127.0.0.1:8726"
	r.Header.Set("Origin", governanceHTTPOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-ADTR-User-ID", "999999999")
	client := f.clients[actor]
	if client != nil {
		r.AddCookie(client.cookie)
		r.Header.Set("X-CSRF-Token", client.csrf)
	}
	if edit != nil {
		edit(r)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("%s: got HTTP %d, want %d: %s", path, w.Code, want, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("directory HTTP response is cacheable")
	}
	if strings.HasPrefix(path, "/api/directory") {
		if want == 200 && (client == nil || w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(client.id, 10)) {
			f.t.Fatal("response trusted forged actor or lost authenticated actor binding")
		}
		if want >= 400 && w.Header().Get("X-ADTR-User-ID") != "" {
			f.t.Fatal("failed response exposed actor binding")
		}
	}
	if want >= 400 {
		out := operationalDecode[map[string]any](f.t, w.Body.Bytes())
		if len(out) != 1 || out["error"] == nil {
			f.t.Fatal("directory error exposed private metadata")
		}
	}
	return w
}

func (f *directoryHTTPFixture) request(actor, path string, body map[string]any, want int) *httptest.ResponseRecorder {
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
	return f.raw(actor, method, path, raw, want, nil)
}

func directoryHTTPSyncBody(key string) map[string]any {
	return map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedCredentialGeneration": "2", "idempotencyKey": key}
}

// Real current/next time steps, never reset a proof counter or production clock.
// Tests use separate actors for further writes so there is no time-window wait.
func (f *directoryHTTPFixture) proof(actor string, body map[string]any) map[string]any {
	f.t.Helper()
	out := make(map[string]any, len(body)+2)
	for k, v := range body {
		out[k] = v
	}
	last := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients[actor].id)
	now := time.Now().Unix() / 30
	step := max(now, last+1)
	if step > now+1 {
		f.t.Fatal("test actor needs a distinct unused proof; no replay reset is permitted")
	}
	out["actorPassword"], out["totpCode"] = governanceHTTPPassword, operationalCode(step)
	return out
}

func (f *directoryHTTPFixture) error(w *httptest.ResponseRecorder, code string) {
	f.t.Helper()
	if got := operationalDecode[map[string]any](f.t, w.Body.Bytes())["error"]; got != code {
		f.t.Fatalf("got error %v, want %s", got, code)
	}
}

func directoryHTTPGrantBody(role, revision, key string) map[string]any {
	body := governanceHTTPUseBody(role, revision, key)
	body["purpose"] = credentialuse.DirectoryPurpose
	return body
}

func (f *directoryHTTPFixture) grant(actor, role string) string {
	f.t.Helper()
	body := f.proof(actor, directoryHTTPGrantBody(role, "0", "grant-"+actor))
	out := operationalDecode[map[string]any](f.t, f.request(actor, "/api/directory-credential-use/grant", body, 200).Body.Bytes())
	revision, ok := out["grantRevision"].(string)
	if !ok || revision == "0" || out["allowed"] != true || out["purpose"] != credentialuse.DirectoryPurpose || out["replayed"] != false || out["consumerEnabled"] != true {
		f.t.Fatal("directory grant response lost committed purpose/revision")
	}
	return revision
}

func (f *directoryHTTPFixture) bind() {
	f.t.Helper()
	// Binding still requires the separately explicit connection-test purpose.
	// It must never be confused with permission to submit directory tasks.
	revision := f.allow("grant-admin", "platform_admin")
	f.request("directory-bind", "/api/domains/credential-source/reference", f.proof("directory-bind", sourceReferenceBody(revision)), 200)
	if f.count(`SELECT count(*) FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id='domain-one' AND connection_revision=2 AND credential_revision=2 AND credential_mode='operation_account'`, governanceHTTPTenant) != 1 {
		f.t.Fatal("actual HTTP source binding did not establish revision pins")
	}
}

func (f *directoryHTTPFixture) submit(actor, key string) tasks.Task {
	f.t.Helper()
	w := f.request(actor, "/api/directory/sync", f.proof(actor, directoryHTTPSyncBody(key)), 200)
	out := operationalDecode[tasks.Submission](f.t, w.Body.Bytes())
	if out.Task.ID == "" || out.Task.Kind != domains.DirectoryKindName || out.Task.State != tasks.Queued || out.Replayed {
		f.t.Fatal("directory sync did not admit a new queued task")
	}
	directoryHTTPRedacted(f.t, w.Body.Bytes())
	return out.Task
}

func directoryHTTPRedacted(t *testing.T, raw []byte) {
	t.Helper()
	for _, private := range []string{"accountId", "grantRoleId", "grantRevision", "policyRevision", "connectionRevision", "connectionCredentialGeneration", "accountCredentialRevision", "objectGUID", "distinguishedName", "actorPassword", "totpCode", "ciphertext", governanceHTTPPassword, governanceHTTPSecret} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("generic task response disclosed private field %s", private)
		}
	}
}

func TestDirectoryMigratedHTTPReadBoundaries(t *testing.T) {
	f := newDirectoryHTTPFixture(t)
	path := "/api/directory/observation?domainId=domain-one"
	f.request("", path, nil, 401)
	for _, actor := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, path, nil, 403)
	}
	f.request("directory-http-no-scope", path, nil, 404)
	f.request("directory-admin", "/api/directory/observation?domainId=domain-two", nil, 404)
	out := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if out.Available || out.List == nil || len(out.List) != 0 || out.ObservationID != "" || out.Source != nil || out.Page.Index != 1 || out.Page.Size != 50 {
		t.Fatal("no successful observation must be unavailable with an explicit empty list")
	}
	for _, query := range []string{"domainId=domain-one&pageIdx=2", "domainId=domain-one&pageSize=26", "domainId=domain-one&kind=all", "domainId=domain-one&domainId=domain-two", "domainId=domain-one&actorId=1", "domainId=domain-one&pageIdx=01"} {
		f.request("directory-admin", "/api/directory/observation?"+query, nil, 400)
	}
	f.request("directory-http-reader", "/api/directory/receipt?domainId=domain-one&idempotencyKey=unknown", nil, 403)
	f.request("directory-admin", "/api/directory/receipt?domainId=domain-one&idempotencyKey=unknown", nil, 404)
}

func TestDirectoryMigratedHTTPTrustedPurposeAndReceipts(t *testing.T) {
	f := newDirectoryHTTPFixture(t)
	prefix := "/api/directory-credential-use"
	effective := prefix + "/effective?accountId=" + governanceHTTPAccount
	for _, actor := range []string{"directory-admin", directoryHTTPWriterRole, "directory-http-reader"} {
		out := operationalDecode[credentialuse.Effective](t, f.request(actor, effective, nil, 200).Body.Bytes())
		if out.ExplicitlyGranted || out.Eligible || out.GrantRevision != "0" || out.Purpose != credentialuse.DirectoryPurpose {
			t.Fatal("ordinary rights or builtin administrator invented directory credential authority")
		}
	}
	for _, path := range []string{"/accounts", "/grants?accountId=" + governanceHTTPAccount, "/roles?accountId=" + governanceHTTPAccount, "/mutation?idempotencyKey=unknown"} {
		f.request(directoryHTTPWriterRole, prefix+path, nil, 403)
	}
	f.request("directory-http-no-scope", effective, nil, 404)
	f.request("directory-http-no-assets", effective, nil, 403)
	body := f.proof("directory-grant", directoryHTTPGrantBody(directoryHTTPWriterRole, "0", "trusted-purpose"))
	f.error(f.request("directory-grant", "/api/credential-use/grant", body, 422), "unsupported_credential_purpose")
	legacy := f.proof("directory-grant", governanceHTTPUseBody(directoryHTTPWriterRole, "0", "wrong-purpose"))
	f.error(f.request("directory-grant", prefix+"/grant", legacy, 422), "unsupported_credential_purpose")
	f.request(directoryHTTPWriterRole, prefix+"/grant", f.proof(directoryHTTPWriterRole, directoryHTTPGrantBody(directoryHTTPWriterRole, "0", "cannot-self-grant")), 403)
	f.request(directoryHTTPWriterRole, prefix+"/revoke", f.proof(directoryHTTPWriterRole, directoryHTTPGrantBody(directoryHTTPWriterRole, "1", "cannot-revoke")), 403)
	if f.count(`SELECT count(*) FROM adtr.operation_account_use_grants`) != 0 || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != -1 {
		t.Fatal("wrong prefix or purpose changed grant/proof state")
	}
	out := operationalDecode[map[string]any](t, f.request("directory-grant", prefix+"/grant", body, 200).Body.Bytes())
	revision, ok := out["grantRevision"].(string)
	if !ok || revision == "0" || out["purpose"] != credentialuse.DirectoryPurpose || out["allowed"] != true {
		t.Fatal("real directory-purpose grant failed")
	}
	step := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id)
	replay := operationalDecode[map[string]any](t, f.request("directory-grant", prefix+"/grant", body, 200).Body.Bytes())
	if replay["replayed"] != true || replay["grantRevision"] != revision || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != step {
		t.Fatal("committed grant replay changed revision or consumed proof twice")
	}
	receipt := operationalDecode[struct {
		Receipt         credentialuse.Receipt `json:"receipt"`
		ConsumerEnabled bool                  `json:"consumerEnabled"`
	}](t, f.request("directory-grant", prefix+"/mutation?idempotencyKey=trusted-purpose", nil, 200).Body.Bytes())
	if receipt.Receipt.Purpose != credentialuse.DirectoryPurpose || receipt.Receipt.GrantRevision != revision || !receipt.ConsumerEnabled {
		t.Fatal("purpose-bound grant recovery lost its receipt")
	}
	f.request("directory-grant", "/api/credential-use/mutation?idempotencyKey=trusted-purpose", nil, 404)
	f.request("directory-admin-other", prefix+"/mutation?idempotencyKey=trusted-purpose", nil, 404)
	outEffective := operationalDecode[credentialuse.Effective](t, f.request(directoryHTTPWriterRole, effective, nil, 200).Body.Bytes())
	if !outEffective.ExplicitlyGranted || !outEffective.Eligible || outEffective.GrantRevision != revision {
		t.Fatal("writer without domains.write or operation-account metadata rights cannot use explicit directory grant")
	}
	if f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE purpose='domain.connection_test'`) != 0 {
		t.Fatal("directory grant silently authorized connection testing")
	}
	grants := operationalDecode[credentialuse.GrantList](t, f.request("directory-admin", prefix+"/grants?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
	if len(grants.Grants) != 1 || grants.Grants[0].RoleID != directoryHTTPWriterRole || grants.Grants[0].Purpose != credentialuse.DirectoryPurpose {
		t.Fatal("trusted directory grant listing mixed purposes")
	}
	roles := operationalDecode[credentialuse.RoleList](t, f.request("directory-admin", prefix+"/roles?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
	found := false
	for _, role := range roles.Roles {
		if role.RoleID == directoryHTTPWriterRole {
			found = true
		}
	}
	if !found {
		t.Fatal("minimal directory writer was omitted from eligible grant roles")
	}
	accounts := operationalDecode[credentialuse.AccountList](t, f.request("directory-admin", prefix+"/accounts", nil, 200).Body.Bytes())
	if len(accounts.List) != 1 || accounts.List[0].AccountID != governanceHTTPAccount {
		t.Fatal("explicit directory allow absent from account catalogue")
	}
}

func TestDirectoryMigratedHTTPSyncProofPinsAndGenericControls(t *testing.T) {
	f := newDirectoryHTTPFixture(t)
	body := directoryHTTPSyncBody("transport-boundaries")
	body["expectedRevision"], body["expectedCredentialGeneration"] = "1", "1"
	body = f.proof("directory-admin", body)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	f.raw("", http.MethodPost, "/api/directory/sync", raw, 401, nil)
	for _, malformed := range [][]byte{
		bytes.Replace(raw, []byte(`"expectedRevision":"1"`), []byte(`"expectedRevision":1`), 1),
		bytes.Replace(raw, []byte(`"expectedRevision":"1"`), []byte(`"expectedRevision":null`), 1),
		bytes.Replace(raw, []byte(`"domainId":"domain-one"`), []byte(`"domainId":"domain-one","domainId":"domain-two"`), 1),
		append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"actorId":1}`)...),
	} {
		f.raw("directory-admin", http.MethodPost, "/api/directory/sync", malformed, 400, nil)
	}
	f.raw("directory-admin", http.MethodPost, "/api/directory/sync?domainId=domain-one", raw, 400, nil)
	f.raw("directory-admin", http.MethodPost, "/api/directory/sync", raw, 400, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") })
	for _, edit := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("Origin") },
		func(r *http.Request) { r.Header.Set("Origin", "https://other.invalid") },
		func(r *http.Request) { r.Header.Add("Origin", governanceHTTPOrigin) },
		func(r *http.Request) { r.Header.Del("X-CSRF-Token") },
		func(r *http.Request) { r.Header.Set("X-CSRF-Token", f.clients["directory-admin-other"].csrf) },
	} {
		f.raw("directory-admin", http.MethodPost, "/api/directory/sync", raw, 403, edit)
	}
	f.request("directory-no-mfa", "/api/directory/sync", body, 403)
	body["actorPassword"] = "incorrect synthetic password"
	f.error(f.request("directory-admin", "/api/directory/sync", body, 401), "invalid_credentials")
	body["actorPassword"] = governanceHTTPPassword
	f.error(f.request("directory-admin", "/api/directory/sync", body, 409), "directory_account_required")
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id) != -1 || f.count(`SELECT count(*) FROM adtr.tasks`) != 0 {
		t.Fatal("rejected request consumed proof or admitted a task")
	}
	f.bind()
	f.error(f.request("directory-admin", "/api/directory/sync", f.proof("directory-admin", directoryHTTPSyncBody("ungiven-directory-purpose")), 403), "forbidden")
	f.grant("directory-grant-writer", directoryHTTPWriterRole)
	for _, actor := range []string{"directory-http-reader", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, "/api/directory/sync", f.proof(actor, directoryHTTPSyncBody("denied-"+actor)), 403)
	}
	stale := directoryHTTPSyncBody("stale-pins")
	stale["expectedCredentialGeneration"] = "1"
	f.error(f.request(directoryHTTPWriterRole, "/api/directory/sync", f.proof(directoryHTTPWriterRole, stale), 409), "revision_conflict")
	firstProof := f.proof(directoryHTTPWriterRole, directoryHTTPSyncBody("sync-intent"))
	first := operationalDecode[tasks.Submission](t, f.request(directoryHTTPWriterRole, "/api/directory/sync", firstProof, 200).Body.Bytes())
	if first.Task.ID == "" || first.Replayed {
		t.Fatal("first sync did not create a task")
	}
	f.error(f.request(directoryHTTPWriterRole, "/api/directory/sync", firstProof, 401), "invalid_credentials")
	replay := operationalDecode[tasks.Submission](t, f.request(directoryHTTPWriterRole, "/api/directory/sync", f.proof(directoryHTTPWriterRole, directoryHTTPSyncBody("sync-intent")), 200).Body.Bytes())
	if !replay.Replayed || replay.Task.ID != first.Task.ID || f.count(`SELECT count(*) FROM adtr.tasks`) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE state='reserved'`) != 1 {
		t.Fatal("sync replay duplicated the task or its reservation")
	}
	if f.count(`SELECT count(*) FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id='domain-one' AND diagnostic_generation=2 AND latest_test_task_id IS NULL`, governanceHTTPTenant) != 1 {
		t.Fatal("directory sync changed unrelated diagnostic identity")
	}
	for _, path := range []string{
		"/api/directory/receipt?domainId=domain-one&idempotencyKey=sync-intent",
		"/api/directory/task?taskUUID=" + first.Task.ID,
		"/api/tasks/detail?taskUUID=" + first.Task.ID,
		"/api/tasks?domainId=domain-one",
	} {
		directoryHTTPRedacted(t, f.request(directoryHTTPWriterRole, path, nil, 200).Body.Bytes())
	}
	f.request("directory-http-reader", "/api/directory/task?taskUUID="+first.Task.ID, nil, 403)
	f.request("directory-http-reader", "/api/directory/cancel", f.proof("directory-http-reader", map[string]any{"taskUUID": first.Task.ID}), 403)
	f.request("directory-admin-other", "/api/directory/receipt?domainId=domain-one&idempotencyKey=sync-intent", nil, 404)
	f.request("directory-http-no-scope", "/api/directory/task?taskUUID="+first.Task.ID, nil, 404)
	f.request("directory-foreign", "/api/directory/task?taskUUID="+first.Task.ID, nil, 404)
	f.request("directory-foreign", "/api/directory/receipt?domainId=domain-one&idempotencyKey=sync-intent", nil, 404)
	f.grant("directory-grant", "platform_admin")
	f.error(f.request("directory-admin-other", "/api/directory/sync", f.proof("directory-admin-other", directoryHTTPSyncBody("sync-intent")), 409), "idempotency_conflict")
	for _, path := range []string{"/api/tasks/cancel", "/api/tasks/recover"} {
		control := map[string]any{"taskUUID": first.Task.ID}
		if strings.HasSuffix(path, "recover") {
			control["idempotencyKey"] = "no-generic-recovery"
		}
		f.error(f.request("directory-cancel", path, f.proof("directory-cancel", control), 400), "directory_route_required")
	}
	f.error(f.request("directory-cancel", "/api/tasks/submit", f.proof("directory-cancel", map[string]any{"taskName": domains.DirectoryKindName, "domainId": "domain-one", "payloadVersion": 1, "payload": map[string]any{}, "idempotencyKey": "cannot-submit-generically"}), 400), "directory_route_required")
	control := map[string]any{"taskUUID": first.Task.ID}
	cancelled := operationalDecode[struct {
		Task tasks.Task `json:"task"`
	}](t, f.request("directory-cancel", "/api/directory/cancel", f.proof("directory-cancel", control), 200).Body.Bytes())
	if cancelled.Task.State != tasks.Cancelled || f.count(`SELECT count(*) FROM adtr.domain_audit WHERE action='domain_directory_cancel' AND task_id=$1`, first.Task.ID) != 1 {
		t.Fatal("dedicated cancellation lost persisted outcome or control audit")
	}
}

func directoryHTTPObservation(users int) ldapconnection.DirectoryObservation {
	start := time.Now().UTC().Truncate(time.Millisecond)
	out := ldapconnection.DirectoryObservation{
		Objects: []directoryassets.Object{},
		Source:  ldapconnection.DirectorySource{ServerName: "dc.one.invalid", DCHostName: "dc.one.invalid", Domain: "one.invalid", NamingContext: "DC=one,DC=invalid", StartedAt: start, CompletedAt: start.Add(10 * time.Millisecond), ElapsedMilliseconds: 10, Pages: 1},
	}
	// Descending input deliberately exercises the read API's stable GUID sort.
	for i := users; i > 0; i-- {
		name, bits := fmt.Sprintf("synthetic-%d", i), uint32(0)
		o := directoryassets.Object{GUID: fmt.Sprintf("00000000-0000-0000-0000-%012x", i), DN: "CN=" + name + ",DC=one,DC=invalid", Kind: directoryassets.User, Classes: []string{"top", "user"}}
		if i == 1 {
			o.SAMAccountName, o.UserAccountControl = &name, &bits
		}
		out.Objects = append(out.Objects, o)
	}
	return out
}

type directoryHTTPSyntheticRun struct {
	release func()
	done    <-chan error
	joined  bool
}

// This is an explicitly synthetic observation executor, not the production LDAP
// producer. It never reads/decrypts credentials and never uses the network. Real
// HTTP admission precedes real tasks.Engine claim/start/fencing, and real SQL
// guards accept the opened-use/observation transitions. Production OnQuiesced
// receives only the witness issued after this executor actually returns.
// Corrupt bodies are inserted at the storage boundary, with all guards enabled,
// to test reader defense in depth; no immutable observation is later rewritten.
func (f *directoryHTTPFixture) stage(observation ldapconnection.DirectoryObservation, corruption string) *directoryHTTPSyntheticRun {
	f.t.Helper()
	raw, err := json.Marshal(observation)
	if err != nil {
		f.t.Fatal(err)
	}
	if corruption == "noncanonical" {
		raw = append([]byte(" "), raw...)
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	if corruption == "digest" {
		hash = strings.Repeat("0", 64)
	}
	staged, release, done := make(chan error, 1), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	run := &directoryHTTPSyntheticRun{release: func() { once.Do(func() { close(release) }) }, done: done}
	f.install(f.store, func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		_, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			tag, err := tx.Exec(ctx, `UPDATE adtr.domain_directory_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$4,opener_fencing_token=$5,opener_attempt=$6 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state='reserved'`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt)
			if err == nil && tag.RowsAffected() != 1 {
				err = fmt.Errorf("synthetic opened-use transition affected %d rows", tag.RowsAffected())
			}
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.ActorID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt, len(observation.Objects), raw, hash)
			}
			// Deliberately private synthetic metadata proves public handlers scrub
			// bytes, rather than passing merely because production emits {}.
			return 95, json.RawMessage(`{"policyRevision":"synthetic-private"}`), json.RawMessage(`{"objectGUID":"synthetic-private"}`), err
		})
		staged <- err
		if err != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_stage_failed"}
		}
		select {
		case <-release:
			return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{"objectGUID":"synthetic-private"}`)}
		case <-ctx.Done():
			// Hold this synthetic cleanup until the test releases it, so a
			// cancel acknowledgement cannot race the live-open assertion.
			// Cleanup always closes release, even when the test fails.
			<-release
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_cancelled"}
		}
	})
	ctx, cancel := context.WithCancel(f.ctx)
	engine := f.engine
	go func() {
		worked, err := engine.RunOne(ctx, "synthetic-directory-http")
		if err == nil && !worked {
			err = fmt.Errorf("synthetic executor found no admitted task")
		}
		done <- err
	}()
	f.t.Cleanup(func() {
		run.release()
		cancel()
		if !run.joined {
			select {
			case <-run.done:
			case <-time.After(10 * time.Second):
				f.t.Error("synthetic executor did not return during cleanup")
			}
		}
	})
	select {
	case err := <-staged:
		if err != nil {
			f.t.Fatal("real SQL observation staging", err)
		}
	case err := <-done:
		run.joined = true
		f.t.Fatal("real engine returned before staging", err)
	case <-f.ctx.Done():
		f.t.Fatal("real observation staging timed out", f.ctx.Err())
	}
	return run
}

func (f *directoryHTTPFixture) finish(run *directoryHTTPSyntheticRun, task tasks.Task, state tasks.State) {
	f.t.Helper()
	run.release()
	select {
	case err := <-run.done:
		run.joined = true
		if err != nil {
			f.t.Fatal("real engine finish/return acknowledgement", err)
		}
	case <-f.ctx.Done():
		f.t.Fatal("actual executor return was not observed", f.ctx.Err())
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state=$2`, task.ID, string(state)) != 1 ||
		f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND state='quiesced' AND quiescence_reason='executor_returned'`, task.ID) != 1 ||
		f.count(`SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read' AND object_id=$1`, task.ID) != 0 {
		f.t.Fatal("actual engine return failed to persist the outcome and matching lifecycle acknowledgement")
	}
}

func (f *directoryHTTPFixture) unavailable(id string) {
	f.t.Helper()
	path := "/api/directory/observation?domainId=domain-one"
	out := operationalDecode[domains.DirectoryList](f.t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if out.Available || out.ObservationID != "" || out.Source != nil || out.List == nil || len(out.List) != 0 {
		f.t.Fatal("unavailable observation exposed data or claimed an observed empty result")
	}
	if id != "" {
		f.error(f.request("directory-http-reader", path+"&observationId="+id, nil, 409), "directory_observation_unavailable")
	}
}

func TestDirectoryMigratedHTTPStoredObservationSuccessAndPinnedPagination(t *testing.T) {
	f := newDirectoryHTTPFixture(t)
	f.bind()
	grant := f.grant("directory-grant", "platform_admin")
	first := f.submit("directory-submit-one", "snapshot-one")
	f.unavailable(first.ID)
	observation := directoryHTTPObservation(30)
	observation.Objects = append(observation.Objects,
		directoryassets.Object{GUID: "ffffffff-0000-0000-0000-000000000001", DN: "CN=Synthetic Group,DC=one,DC=invalid", Kind: directoryassets.Group, Classes: []string{"group", "top"}},
		directoryassets.Object{GUID: "ffffffff-0000-0000-0000-000000000002", DN: "CN=Synthetic Computer,DC=one,DC=invalid", Kind: directoryassets.Computer, Classes: []string{"computer", "top", "user"}},
	)
	run := f.stage(observation, "")
	if f.count(`SELECT count(*) FROM adtr.domain_directory_observations WHERE task_id=$1`, first.ID) != 1 || f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state='running'`, first.ID) != 1 {
		t.Fatal("test did not reach a real committed staged observation on a running task")
	}
	f.unavailable(first.ID)
	for _, path := range []string{"/api/directory/task?taskUUID=" + first.ID, "/api/tasks/detail?taskUUID=" + first.ID, "/api/tasks?domainId=domain-one"} {
		directoryHTTPRedacted(t, f.request("directory-admin", path, nil, 200).Body.Bytes())
	}
	f.finish(run, first, tasks.Succeeded)
	path := "/api/directory/observation?domainId=domain-one&kind=user&pageSize=25"
	page := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if !page.Available || page.ObservationID != first.ID || page.Source == nil || page.Source.Domain != "one.invalid" || page.Page.Total != 30 || page.Page.Pages != 2 || len(page.List) != 25 {
		t.Fatal("successful stored observation is not readable by an independent reader without a credential grant or task rights")
	}
	for i, object := range page.List {
		if object.GUID != fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1) || object.Kind != directoryassets.User {
			t.Fatal("page was not stably ordered after kind filtering")
		}
	}
	if page.List[0].SAMAccountName == nil || page.List[0].UserAccountControl == nil || *page.List[0].UserAccountControl != 0 || page.List[1].SAMAccountName != nil || page.List[1].UserAccountControl != nil {
		t.Fatal("stored dictionary lost absent versus observed-zero values")
	}
	foreign := operationalDecode[domains.DirectoryList](t, f.request("directory-foreign", "/api/directory/observation?domainId=domain-one", nil, 200).Body.Bytes())
	if foreign.Available || len(foreign.List) != 0 || foreign.Source != nil {
		t.Fatal("same domain ID crossed tenant boundary")
	}
	f.request("directory-foreign", "/api/directory/observation?domainId=domain-one&observationId="+first.ID, nil, 409)
	second := f.submit("directory-submit-two", "snapshot-two")
	run = f.stage(directoryHTTPObservation(1), "")
	f.finish(run, second, tasks.Succeeded)
	pinned := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", path+"&pageIdx=2&observationId="+first.ID, nil, 200).Body.Bytes())
	if pinned.ObservationID != first.ID || pinned.Page.Total != 30 || len(pinned.List) != 5 || pinned.List[0].GUID != "00000000-0000-0000-0000-00000000001a" {
		t.Fatal("new observation changed pinned page contents")
	}
	latest := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if latest.ObservationID != second.ID || latest.Page.Total != 1 {
		t.Fatal("un-pinned first page did not select newest successful observation")
	}
	group := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", "/api/directory/observation?domainId=domain-one&kind=group&observationId="+first.ID, nil, 200).Body.Bytes())
	if group.Page.Total != 1 || len(group.List) != 1 || group.List[0].Kind != directoryassets.Group {
		t.Fatal("group filter mixed object types")
	}
	for _, path := range []string{"/api/directory/task?taskUUID=" + first.ID, "/api/tasks/detail?taskUUID=" + first.ID, "/api/tasks?domainId=domain-one"} {
		directoryHTTPRedacted(t, f.request("directory-admin", path, nil, 200).Body.Bytes())
	}
	f.request("directory-revoke", "/api/directory-credential-use/revoke", f.proof("directory-revoke", directoryHTTPGrantBody("platform_admin", grant, "revoke-future-directory-use")), 200)
	f.request("directory-admin", "/api/access/users/update", f.proof("directory-admin", map[string]any{"username": "directory-submit-one", "disabled": true}), 200)
	afterRevocation := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", path+"&observationId="+first.ID, nil, 200).Body.Bytes())
	if !afterRevocation.Available || afterRevocation.ObservationID != first.ID {
		t.Fatal("reading a historical successful observation depended on current producer/credential authority")
	}
	f.exec(`UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, governanceHTTPTenant, directoryHTTPReaderRole)
	f.request("directory-http-reader", path+"&observationId="+first.ID, nil, 403)
}

func TestDirectoryMigratedHTTPStoredEmptyCancellationAndCurrentSource(t *testing.T) {
	for _, scenario := range []string{"empty", "cancelled", "changed-policy", "detached-source"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDirectoryHTTPFixture(t)
			f.bind()
			f.grant("directory-grant", "platform_admin")
			task := f.submit("directory-submit-one", "observation-"+scenario)
			observation := directoryHTTPObservation(1)
			if scenario == "empty" {
				observation = directoryHTTPObservation(0)
			}
			run := f.stage(observation, "")
			if scenario == "cancelled" {
				out := operationalDecode[struct {
					Task tasks.Task `json:"task"`
				}](t, f.request("directory-cancel", "/api/directory/cancel", f.proof("directory-cancel", map[string]any{"taskUUID": task.ID}), 200).Body.Bytes())
				if out.Task.State != tasks.CancelRequested || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND state='opened'`, task.ID) != 1 || f.count(`SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read' AND object_id=$1`, task.ID) != 1 {
					t.Fatal("cancel acceptance falsely acknowledged an executor that has not returned")
				}
				f.unavailable(task.ID)
				f.finish(run, task, tasks.Cancelled)
				f.unavailable(task.ID)
				return
			}
			f.finish(run, task, tasks.Succeeded)
			path := "/api/directory/observation?domainId=domain-one"
			out := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
			if !out.Available || out.ObservationID != task.ID || out.Source == nil {
				t.Fatal("real succeeded task did not expose its observation before invalidation")
			}
			switch scenario {
			case "empty":
				if out.List == nil || len(out.List) != 0 || out.Page.Total != 0 || out.Page.Pages != 0 {
					t.Fatal("observed empty directory was confused with unavailable")
				}
			case "changed-policy":
				f.install(domains.New(directoryHTTPRuntime(t, "198.51.100.0/24", true)), nil)
				f.unavailable(task.ID)
			case "detached-source":
				f.request("directory-detach", "/api/domains/credential-source/detach", f.proof("directory-detach", map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedConnectionCredentialGeneration": "2", "idempotencyKey": "detach-observed-source"}), 200)
				f.unavailable(task.ID)
			}
		})
	}
}

func TestDirectoryMigratedHTTPStoredCorruptionFailsClosed(t *testing.T) {
	for _, corruption := range []string{"digest", "noncanonical", "cross-domain-object"} {
		t.Run(corruption, func(t *testing.T) {
			f := newDirectoryHTTPFixture(t)
			f.bind()
			f.grant("directory-grant", "platform_admin")
			task := f.submit("directory-submit-one", "corrupt-"+corruption)
			observation := directoryHTTPObservation(1)
			if corruption == "cross-domain-object" {
				observation.Objects[0].DN = "CN=Synthetic,DC=other,DC=invalid"
			}
			run := f.stage(observation, corruption)
			f.finish(run, task, tasks.Succeeded)
			for _, pin := range []string{"", "&observationId=" + task.ID} {
				f.error(f.request("directory-http-reader", "/api/directory/observation?domainId=domain-one"+pin, nil, 409), "directory_observation_unavailable")
			}
			if f.count(`SELECT count(*) FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID) != 1 {
				t.Fatal("corruption test failed to persist bytes behind real SQL guards")
			}
		})
	}
}

func TestDirectoryMigratedHTTPDeploymentGateAndLiveReceiptAuthority(t *testing.T) {
	f := newDirectoryHTTPFixture(t)
	f.bind()
	f.grant("directory-grant-writer", directoryHTTPWriterRole)
	task := f.submit(directoryHTTPWriterRole, "deployment-receipt")
	f.install(domains.New(directoryHTTPRuntime(t, "192.0.2.0/24", false)), nil)
	f.error(f.request(directoryHTTPWriterRole, "/api/directory/sync", f.proof(directoryHTTPWriterRole, directoryHTTPSyncBody("disabled-read")), 503), "directory_read_disabled")
	receiptPath := "/api/directory/receipt?domainId=domain-one&idempotencyKey=deployment-receipt"
	receipt := operationalDecode[struct {
		Task tasks.Task `json:"task"`
	}](t, f.request(directoryHTTPWriterRole, receiptPath, nil, 200).Body.Bytes())
	if receipt.Task.ID != task.ID {
		t.Fatal("deployment switch hid safe committed receipt recovery")
	}
	f.request("directory-admin", "/api/access/permissions/save", f.proof("directory-admin", map[string]any{"roleID": directoryHTTPWriterRole, "permissions": []any{}}), 200)
	// Permission replacement revokes the old session. A newly authenticated
	// transport must still honor live permissions when recovering old intent.
	f.request(directoryHTTPWriterRole, receiptPath, nil, 401)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients[directoryHTTPWriterRole])
	f.request(directoryHTTPWriterRole, receiptPath, nil, 403)
	f.request(directoryHTTPWriterRole, "/api/directory/task?taskUUID="+task.ID, nil, 403)
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1`, task.ID) != 1 {
		t.Fatal("permission revocation erased the original intent")
	}
}

func TestDirectoryHTTPUninitializedSchemaGatePrecedesIdentity(t *testing.T) {
	// This is a genuinely uninitialized isolated database, not a schema stamp
	// or a replacement security function. No mutation may fall through the gate.
	f := newOperationalHTTPFixture(t, false)
	cfg := f.cfg.Copy()
	cfg.Tracer = f.trace
	s, err := auth.New(cfg, f.key, operationalHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	ds := domains.New(nil)
	engine, err := tasks.New(cfg, tasks.ProductionRegistry(ds.DirectoryKind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/directory/", s.DirectoryHandler(ds, engine))
	mux.Handle("/api/directory-credential-use/", s.DirectoryCredentialUseHandler(credentialuse.New()))
	f.handler = mux
	f.trace.take()
	for _, path := range []string{"/api/directory/observation?domainId=domain-one", "/api/directory/receipt?domainId=domain-one&idempotencyKey=unknown", "/api/directory/task?taskUUID=" + operationalHTTPMissingTask, "/api/directory-credential-use/effective?accountId=" + governanceHTTPAccount} {
		f.error(f.call(nil, path, nil, 503), "schema_incompatible")
	}
	body := directoryHTTPSyncBody("schema-gated-intent")
	body["actorPassword"], body["totpCode"] = governanceHTTPPassword, "123456"
	f.error(f.call(nil, "/api/directory/sync", body, 503), "schema_incompatible")
	for _, query := range f.trace.take() {
		lower := strings.ToLower(query)
		if strings.Contains(lower, "adtr.users") || strings.Contains(lower, "adtr.sessions") || strings.Contains(lower, "insert ") || strings.Contains(lower, "update ") || strings.Contains(lower, "delete ") {
			t.Fatalf("schema gate reached authentication, proof, or failure-audit writes: %s", query)
		}
	}
}
