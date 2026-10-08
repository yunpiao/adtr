//go:build integration

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

type credentialUseFixture struct {
	*operationAccountFixture
	uses *credentialuse.Store
}

func newCredentialUseFixture(t *testing.T) *credentialUseFixture {
	t.Helper()
	f := newOperationAccountFixture(t)
	f.exec(audit.AccountReferenceViewSchema)
	// Install the actual migration-12 grant tables and SQL guards on the F03
	// component fixture. A version marker alone cannot make this test valid.
	var installed bool
	err := f.conn.QueryRow(f.ctx, `SELECT to_regclass('adtr.operation_account_use_grants') IS NOT NULL
	 AND to_regclass('adtr.operation_account_use_mutations') IS NOT NULL
	 AND to_regclass('adtr.operation_account_use_audit') IS NOT NULL
	 AND EXISTS(SELECT FROM pg_trigger WHERE tgname='credential_use_grant_guard' AND NOT tgisinternal)`).Scan(&installed)
	if err != nil || !installed {
		t.Fatal("credential-use integration requires actual migration-12 tables and guards", err)
	}
	return &credentialUseFixture{operationAccountFixture: f, uses: credentialuse.New()}
}

func (f *credentialUseFixture) useResponse(c *resourceTestClient, path string, body map[string]any, change func(*http.Request)) *httptest.ResponseRecorder {
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
	r := httptest.NewRequest(method, "/api/credential-use"+path, bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:8765"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	f.s.CredentialUseHandler(f.uses).ServeHTTP(w, r)
	return w
}

func (f *credentialUseFixture) callUse(c *resourceTestClient, path string, body map[string]any, status int) map[string]any {
	f.t.Helper()
	w := f.useResponse(c, path, body, nil)
	if w.Code != status {
		f.t.Fatalf("credential use %s: got %d want %d: %s", path, w.Code, status, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("credential-use response was cacheable")
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	if status == http.StatusOK {
		if c == nil || w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(c.id, 10) || out["consumerEnabled"] != true {
			f.t.Fatal("successful credential-use response lacks bound identity or enables consumer")
		}
	} else if w.Header().Get("X-ADTR-User-ID") != "" || len(out) != 1 || out["error"] == nil {
		f.t.Fatal("credential-use failure leaks identity or non-error data")
	}
	for _, secret := range []string{resourceTestPassword, resourceTestSecret, "Synthetic Secret", "registry-reader@alternate.test", "ciphertext", "actorPassword", "totpCode", "fingerprint"} {
		if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
			f.t.Fatal("credential-use response disclosed secret material")
		}
	}
	return out
}

func useMutationBody(account, role, grantRevision, key string) map[string]any {
	return map[string]any{"accountId": account, "roleId": role, "purpose": credentialuse.Purpose,
		"expectedAccountRevision": "1", "expectedCredentialRevision": "1", "expectedGrantRevision": grantRevision, "idempotencyKey": key}
}

func (f *credentialUseFixture) proofStep(id int64) int64 {
	f.t.Helper()
	var step int64
	if err := f.conn.QueryRow(f.ctx, "SELECT mfa_last_step FROM adtr.users WHERE id=$1", id).Scan(&step); err != nil {
		f.t.Fatal(err)
	}
	return step
}

func useRevision(t *testing.T, out map[string]any) int64 {
	t.Helper()
	value, ok := out["grantRevision"].(string)
	if !ok {
		t.Fatal("grant revision is not a string")
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision <= 0 {
		t.Fatal("grant revision is not positive and canonical")
	}
	return revision
}

func TestCredentialUseHTTPDefaultDenySelfGrantReplayAndEpoch(t *testing.T) {
	f := newCredentialUseFixture(t)
	id := f.createAccount("use-self-account")
	admin := f.client(f.admin)
	for _, c := range []*resourceTestClient{admin, f.client(f.operator)} {
		out := f.callUse(c, "/effective?accountId="+id, nil, 200)
		if out["explicitlyGranted"] != false || out["eligible"] != false || out["grantRevision"] != "0" || out["purpose"] != credentialuse.Purpose {
			t.Fatal("normal function rights invented credential authority", out)
		}
	}
	before := f.version(f.admin)
	body := f.proof(useMutationBody(id, "platform_admin", "0", "self-grant"))
	granted := f.callUse(admin, "/grant", body, 200)
	firstRevision := useRevision(t, granted)
	if granted["allowed"] != true || granted["replayed"] != false || f.version(f.admin) <= before {
		t.Fatal("explicit self-grant did not advance durable authority")
	}
	step, epoch := f.proofStep(f.admin), f.version(f.admin)
	replayed := f.callUse(admin, "/grant", body, 200)
	if replayed["replayed"] != true || useRevision(t, replayed) != firstRevision || f.proofStep(f.admin) != step || f.version(f.admin) != epoch {
		t.Fatal("committed replay consumed proof or renewed authority")
	}
	changed := maps.Clone(body)
	changed["expectedAccountRevision"] = "2"
	if out := f.callUse(admin, "/grant", changed, 409); out["error"] != "idempotency_conflict" {
		t.Fatal("same-key changed metadata did not conflict", out)
	}
	changed = maps.Clone(body)
	changed["idempotencyKey"] = "unused-key-reused-proof"
	changed["expectedGrantRevision"] = granted["grantRevision"]
	if out := f.callUse(admin, "/grant", changed, 401); out["error"] != "invalid_credentials" {
		t.Fatal("new intent reused consumed proof", out)
	}
	noop := f.callUse(admin, "/grant", f.proof(useMutationBody(id, "platform_admin", granted["grantRevision"].(string), "self-noop")), 200)
	if useRevision(t, noop) != firstRevision || f.version(f.admin) != epoch {
		t.Fatal("no-op artificially advanced grant revision or epoch")
	}
	if out := f.callUse(f.client(f.operator), "/effective?accountId="+id, nil, 200); out["explicitlyGranted"] != false || out["grantRevision"] != "0" {
		t.Fatal("another role inherited administrator grant")
	}
	revoked := f.callUse(admin, "/revoke", f.proof(useMutationBody(id, "platform_admin", granted["grantRevision"].(string), "self-revoke")), 200)
	secondRevision := useRevision(t, revoked)
	if secondRevision <= firstRevision || revoked["allowed"] != false || f.version(f.admin) <= epoch {
		t.Fatal("revocation failed to invalidate prior authority")
	}
	replayed = f.callUse(admin, "/grant", body, 200)
	if replayed["allowed"] != true || replayed["currentAllowed"] != false || replayed["currentGrantRevision"] != revoked["grantRevision"] || useRevision(t, replayed) != firstRevision {
		t.Fatal("replay renewed a revoked grant or lost original outcome")
	}
	again := f.callUse(admin, "/grant", f.proof(useMutationBody(id, "platform_admin", revoked["grantRevision"].(string), "self-regrant")), 200)
	if useRevision(t, again) <= secondRevision {
		t.Fatal("regrant restored an old incarnation")
	}
	if out := f.callUse(admin, "/effective?accountId="+id, nil, 200); out["explicitlyGranted"] != true || out["eligible"] != true || out["accountCredentialRevision"] != "1" {
		t.Fatal("explicit self-grant is not reflected in current eligibility")
	}
	if f.scalar("SELECT count(*) FROM adtr.tasks") != 0 || f.scalar("SELECT count(*) FROM adtr.operation_account_dependencies") != 0 {
		t.Fatal("B1 admitted a consumer or execution dependency")
	}
}

func TestCredentialUseHTTPRoleTargetsAndActorBoundReceipts(t *testing.T) {
	f := newCredentialUseFixture(t)
	id := f.createAccount("use-role-account")
	var secondMember, secondAdmin int64
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,password_updated_at)
	 SELECT tenant_id,'second-member',password_hash,'viewer',$1,false,password_updated_at FROM adtr.users WHERE id=$2 RETURNING id`, narrowResourceRole, f.operator).Scan(&secondMember); err != nil {
		t.Fatal(err)
	}
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change,password_updated_at)
	 SELECT tenant_id,'second-admin',password_hash,'platform_admin',false,password_updated_at FROM adtr.users WHERE id=$1 RETURNING id`, f.admin).Scan(&secondAdmin); err != nil {
		t.Fatal(err)
	}
	admin := f.client(f.admin)
	roles := f.callUse(admin, "/roles?accountId="+id, nil, 200)["roles"].([]any)
	found := false
	for _, value := range roles {
		role := value.(map[string]any)
		if role["roleId"] == "viewer" {
			t.Fatal("viewer offered as a credential target")
		}
		if role["roleId"] == narrowResourceRole {
			found = role["memberCount"] == float64(2)
		}
	}
	if !found {
		t.Fatal("role-wide member count or eligible role missing")
	}
	for _, c := range []*resourceTestClient{f.client(f.operator), f.client(f.viewer)} {
		f.callUse(c, "/grants?accountId="+id, nil, 403)
		f.callUse(c, "/accounts", nil, 403)
		f.callUse(c, "/grant", f.proof(useMutationBody(id, narrowResourceRole, "0", "nonadmin-"+strconv.FormatInt(c.id, 10))), 403)
	}
	if out := f.callUse(admin, "/grant", f.proof(useMutationBody(id, "viewer", "0", "viewer-target")), 422); out["error"] != "credential_use_target_unavailable" {
		t.Fatal("viewer target was not rejected")
	}
	if out := f.callUse(admin, "/grant", f.proof(useMutationBody(id, strings.Repeat("z", 24), "0", "missing-role")), 404); out["error"] != "not_found" {
		t.Fatal("missing target leaked revisions")
	}
	f.callUse(admin, "/grant", f.proof(useMutationBody(id, narrowResourceRole, "0", "role-grant")), 200)
	for _, member := range []int64{f.operator, secondMember} {
		if out := f.callUse(f.client(member), "/effective?accountId="+id, nil, 200); out["explicitlyGranted"] != true || out["eligible"] != true {
			t.Fatal("explicit role grant did not cover current members")
		}
	}
	f.callUse(f.client(f.other), "/grants?accountId="+id, nil, 404)
	f.callUse(f.client(f.other), "/mutation?idempotencyKey=role-grant", nil, 404)
	f.callUse(f.client(secondAdmin), "/mutation?idempotencyKey=role-grant", nil, 404)
	receipt := f.callUse(admin, "/mutation?idempotencyKey=role-grant", nil, 200)["receipt"].(map[string]any)
	if receipt["accountId"] != id || receipt["roleId"] != narrowResourceRole || receipt["replayed"] != true {
		t.Fatal("original actor did not recover safe receipt")
	}
	if _, extra := receipt["consumerEnabled"]; extra {
		t.Fatal("receipt nested a response-envelope field")
	}
}

func TestCredentialUseHTTPProofCSRFAuditRollback(t *testing.T) {
	f := newCredentialUseFixture(t)
	id := f.createAccount("use-rollback-account")
	admin := f.client(f.admin)
	body := f.proof(useMutationBody(id, narrowResourceRole, "0", "atomic-use-grant"))
	beforeStep, beforeEpoch := f.proofStep(f.admin), f.version(f.admin)
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("X-CSRF-Token") }, func(r *http.Request) { r.Header.Set("Origin", "https://foreign.test") }} {
		w := f.useResponse(admin, "/grant", body, change)
		if w.Code != 403 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("mutation bypassed CSRF or origin checks")
		}
	}
	f.callUse(nil, "/grant", body, 401)
	bad := maps.Clone(body)
	bad["actorPassword"] = "Incorrect Synthetic Proof"
	f.callUse(admin, "/grant", bad, 401)
	if f.proofStep(f.admin) != beforeStep || f.version(f.admin) != beforeEpoch || f.scalar("SELECT count(*) FROM adtr.operation_account_use_grants") != 0 {
		t.Fatal("rejected proof changed authority")
	}
	f.exec(`CREATE FUNCTION adtr.reject_synthetic_use_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END; $$;
	 CREATE TRIGGER reject_synthetic_use_audit BEFORE INSERT ON adtr.operation_account_use_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_synthetic_use_audit()`)
	f.callUse(admin, "/grant", body, 500)
	if f.proofStep(f.admin) != beforeStep || f.version(f.admin) != beforeEpoch || f.scalar("SELECT count(*) FROM adtr.operation_account_use_grants") != 0 || f.scalar("SELECT count(*) FROM adtr.operation_account_use_mutations") != 0 {
		t.Fatal("audit failure did not roll back proof, epoch, grant and receipt")
	}
	f.exec("DROP TRIGGER reject_synthetic_use_audit ON adtr.operation_account_use_audit; DROP FUNCTION adtr.reject_synthetic_use_audit()")
	f.callUse(admin, "/grant", body, 200)
}

func TestCredentialUseHTTPAccountRotationAndDeletedReceipt(t *testing.T) {
	f := newCredentialUseFixture(t)
	id := f.createAccount("use-rotation-account")
	admin := f.client(f.admin)
	body := f.proof(useMutationBody(id, narrowResourceRole, "0", "before-rotation"))
	granted := f.callUse(admin, "/grant", body, 200)
	beforeEpoch := f.version(f.operator)
	label := f.accountBody(id, "1", "use-label-only")
	label["label"] = "Updated registry label"
	f.callAccount(admin, "/update", f.proof(label), 200)
	if f.version(f.operator) != beforeEpoch {
		t.Fatal("label edit invalidated credential authority")
	}
	if out := f.callUse(f.client(f.operator), "/effective?accountId="+id, nil, 200); out["explicitlyGranted"] != true || out["eligible"] != true || out["grantRevision"] != granted["grantRevision"] {
		t.Fatal("label edit changed the explicit grant")
	}
	// A committed request recovers before its now-obsolete account CAS.
	f.callUse(admin, "/grant", body, 200)
	rotate := f.accountBody(id, "2", "use-pair-rotation")
	rotate["username"] = "rotated-reader@alternate.test"
	rotate["password"] = "Another Synthetic Credential 42"
	f.callAccount(admin, "/update", f.proof(rotate), 200)
	if f.version(f.operator) <= beforeEpoch {
		t.Fatal("pair rotation did not invalidate authority")
	}
	after := f.callUse(f.client(f.operator), "/effective?accountId="+id, nil, 200)
	if after["explicitlyGranted"] != false || after["eligible"] != false || after["accountCredentialRevision"] != "2" || useRevision(t, after) <= useRevision(t, granted) {
		t.Fatal("replacement credentials inherited prior allow")
	}
	oldReceipt := f.callUse(admin, "/mutation?idempotencyKey=before-rotation", nil, 200)["receipt"].(map[string]any)
	if oldReceipt["accountCredentialRevision"] != "1" || oldReceipt["allowed"] != true || oldReceipt["currentAllowed"] != false {
		t.Fatal("rotation rewrote original receipt or retained current allow")
	}
	newGrant := useMutationBody(id, narrowResourceRole, after["grantRevision"].(string), "after-rotation")
	newGrant["expectedAccountRevision"] = "3"
	newGrant["expectedCredentialRevision"] = "2"
	f.callUse(admin, "/grant", f.proof(newGrant), 200)
	f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "3", "use-account-delete")), 200)
	f.callUse(admin, "/grants?accountId="+id, nil, 404)
	recovered := f.callUse(admin, "/mutation?idempotencyKey=after-rotation", nil, 200)["receipt"].(map[string]any)
	if recovered["accountDeleted"] != true || recovered["allowed"] != true || recovered["currentAllowed"] != false || recovered["accountCredentialRevision"] != "2" {
		t.Fatal("account tombstone erased or renewed the committed receipt")
	}
	if f.scalar("SELECT count(*) FROM adtr.operation_account_use_audit WHERE action='credential_use_pair_replaced'") != 1 || f.scalar("SELECT count(*) FROM adtr.operation_account_use_audit WHERE action='credential_use_account_deleted'") != 1 {
		t.Fatal("automatic revocation history missing")
	}
}

func TestCredentialUseHTTPCleanupAfterTenantExpiryAndOverquota(t *testing.T) {
	for _, condition := range []string{"application_expired", "overquota"} {
		t.Run(condition, func(t *testing.T) {
			f := newCredentialUseFixture(t)
			id := f.createAccount("use-cleanup-account")
			// Configure the synthetic transition before any allow exists. The
			// protected record is never altered through an old-style bypass.
			if condition == "application_expired" {
				f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'", f.now.Add(2*time.Minute).Unix())
			} else {
				f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=(SELECT count(*) FROM adtr.resource_domains WHERE tenant_id='one' AND active) WHERE tenant_id='one'")
			}
			admin := f.client(f.admin)
			granted := f.callUse(admin, "/grant", f.proof(useMutationBody(id, narrowResourceRole, "0", "cleanup-grant")), 200)
			if condition == "application_expired" {
				// This exercises the HTTP application's expiry gate. Actual SQL
				// clock expiry is covered by the store's governance integration.
				f.now = f.now.Add(2 * time.Minute)
			} else {
				f.exec("INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('one','cleanup-added-domain','Synthetic quota transition')")
			}
			for _, path := range []string{"/roles?accountId=" + id, "/effective?accountId=" + id} {
				f.callUse(admin, path, nil, 403)
			}
			f.callUse(admin, "/grant", f.proof(useMutationBody(id, "platform_admin", "0", "ineligible-grant")), 403)
			f.callUse(admin, "/grants?accountId="+id, nil, 200)
			f.callUse(admin, "/mutation?idempotencyKey=cleanup-grant", nil, 200)
			list := f.callUse(admin, "/accounts?pageIdx=1&pageSize=10", nil, 200)
			if list["page"].(map[string]any)["total"] != float64(1) || len(list["List"].([]any)) != 1 {
				t.Fatal("ineligible tenant lost grant cleanup discovery")
			}
			// Governance and revocation must not depend on the domain vault.
			f.runtime = nil
			f.accounts = operationaccounts.New(nil)
			f.uses = credentialuse.New()
			revoked := f.callUse(admin, "/revoke", f.proof(useMutationBody(id, narrowResourceRole, granted["grantRevision"].(string), "cleanup-revoke")), 200)
			if revoked["allowed"] != false {
				t.Fatal("tenant cleanup failed to revoke")
			}
			if out := f.callUse(admin, "/accounts", nil, 200); len(out["List"].([]any)) != 0 || out["page"].(map[string]any)["total"] != float64(0) {
				t.Fatal("cleanup list returned account with no remaining allows")
			}
		})
	}
}

func TestCredentialUseHTTPAccountsScopeAndCurrentSchema(t *testing.T) {
	f := newCredentialUseFixture(t)
	id := f.createAccount("use-list-granted")
	f.createAccount("use-list-ungranted")
	f.exec("INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('one','inactive-cleanup-domain','Synthetic inactive cleanup domain')")
	f.seedGroup("one", "inactive-cleanup-group", "platform_admin", "inactive-cleanup-domain")
	// An account-free catalogue domain can genuinely become inactive while
	// its old role membership remains persisted.
	f.exec("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='inactive-cleanup-domain'")
	admin := f.client(f.admin)
	granted := f.callUse(admin, "/grant", f.proof(useMutationBody(id, narrowResourceRole, "0", "list-grant")), 200)
	list := f.callUse(admin, "/accounts?pageIdx=1&pageSize=10&keyword=Registry", nil, 200)
	if list["page"].(map[string]any)["total"] != float64(1) || len(list["List"].([]any)) != 1 || list["List"].([]any)[0].(map[string]any)["accountId"] != id {
		t.Fatal("cleanup list included ungranted accounts or lost metadata")
	}
	foreign := f.callUse(f.client(f.other), "/accounts", nil, 200)
	if foreign["page"].(map[string]any)["total"] != float64(0) || len(foreign["List"].([]any)) != 0 {
		t.Fatal("cleanup list counted foreign records before scope filtering")
	}
	_, err := f.conn.Exec(f.ctx, "UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id=$1", f.domain)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23514" {
		t.Fatal("live operation-account parent guard did not reject catalogue deactivation", err)
	}
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if err = tasks.CheckSchemaTx(f.ctx, tx, schemaversion.Current); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/credential-use/accounts", nil)
	r.AddCookie(admin.cookie)
	actor, role, _, err := f.s.authenticateAccess(f.ctx, tx, r)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := f.s.credentialUseCleanupScope(f.ctx, tx, actor, role)
	if err != nil || !slices.Contains(allowed, f.domain) || slices.Contains(allowed, "inactive-cleanup-domain") {
		t.Fatal("cleanup scope failed to intersect persisted membership with active catalogue", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	// Removing the sole administrator binding is valid only after explicit
	// revocation. Recovery then checks today's scope and hides the old receipt.
	f.callUse(admin, "/revoke", f.proof(useMutationBody(id, narrowResourceRole, granted["grantRevision"].(string), "list-revoke")), 200)
	f.exec("DELETE FROM adtr.resource_role_groups WHERE tenant_id='one' AND role_id='platform_admin' AND group_id=$1", "grant-"+f.domain)
	list = f.callUse(admin, "/accounts", nil, 200)
	if list["page"].(map[string]any)["total"] != float64(0) || len(list["List"].([]any)) != 0 {
		t.Fatal("cleanup list retained records after revocation and scope removal")
	}
	f.callUse(admin, "/grants?accountId="+id, nil, 404)
	f.callUse(admin, "/mutation?idempotencyKey=list-grant", nil, 404)
	beforeAttempts := f.scalar("SELECT COALESCE(sum(count),0) FROM adtr.auth_attempts")
	beforeStep := f.proofStep(f.admin)
	for _, version := range []int{schemaversion.Current - 1, schemaversion.Current + 1} {
		f.exec("UPDATE adtr.schema_version SET version=$1", version)
		for _, path := range []string{"/accounts", "/grants?accountId=" + id, "/mutation?idempotencyKey=list-grant"} {
			if out := f.callUse(nil, path, nil, 503); out["error"] != "schema_incompatible" {
				t.Fatal("schema mismatch did not precede authentication")
			}
		}
		if out := f.callUse(nil, "/grant", f.proof(useMutationBody(id, "platform_admin", "0", "schema-grant")), 503); out["error"] != "schema_incompatible" {
			t.Fatal("mutation schema mismatch did not precede authentication")
		}
	}
	if f.proofStep(f.admin) != beforeStep || f.scalar("SELECT COALESCE(sum(count),0) FROM adtr.auth_attempts") != beforeAttempts {
		t.Fatal("schema mismatch reached proof or rate-limit mutations")
	}
}
