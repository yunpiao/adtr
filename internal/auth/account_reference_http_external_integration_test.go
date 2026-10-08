//go:build integration

package auth_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const sourceHTTPViewerRole = "source-view-role-0000001"

func accountSourceHTTPFixture(t *testing.T) *governanceHTTPFixture {
	t.Helper()
	f := newGovernanceHTTPFixture(t)
	key := bytes.Repeat([]byte{'h'}, 32)
	// New synthetic actors precede every allow; each mutation uses a distinct
	// unused proof. No replay counter is reset to make the suite pass.
	for _, name := range []string{"source-bind", "source-detach", "source-custom", "source-create", "source-grant", "source-revoke", "source-expire"} {
		f.seedActor(name, "platform_admin", "", false, false, false, true, key)
	}
	f.exec(`INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,'Source view only')`, governanceHTTPTenant, sourceHTTPViewerRole)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'domains',true,true)`, governanceHTTPTenant, sourceHTTPViewerRole)
	f.exec(`INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,$2,'admin-group')`, governanceHTTPTenant, sourceHTTPViewerRole)
	f.seedActor("source-view", "viewer", sourceHTTPViewerRole, false, false, false, true, key)
	s, err := auth.New(f.conn.Config(), key, governanceHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := domainconfig.Load(func(k string) string {
		switch k {
		case "ADTR_DOMAIN_KEY_ID":
			return "synthetic-source"
		case "ADTR_DOMAIN_KEY":
			return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'s'}, 32))
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	ds := domains.New(runtime)
	engine, err := tasks.New(f.conn.Config(), tasks.ProductionRegistry(ds.Kind(), ds.AccountKind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/domains", s.DomainsHandler(ds, engine))
	mux.Handle("/api/domains/", s.DomainsHandler(ds, engine))
	mux.Handle("/", f.handler)
	f.handler = mux
	return f
}
func sourceReferenceBody(grant string) map[string]any {
	return map[string]any{"domainId": "domain-one", "expectedRevision": "1", "expectedConnectionCredentialGeneration": "1", "accountId": governanceHTTPAccount, "expectedAccountRevision": "1", "expectedAccountCredentialRevision": "1", "expectedGrantRevision": grant, "idempotencyKey": "source-reference-intent"}
}
func TestAccountSourceHTTPRequiresExplicitGrantAndRecoversCommittedReceipt(t *testing.T) {
	f := accountSourceHTTPFixture(t)
	denied := f.mutate("source-bind", "/api/domains/credential-source/reference", sourceReferenceBody("1"), 404)
	if denied["error"] != "not_found" {
		t.Fatal("ungranted account existence disclosed")
	}
	// Failed mutation rolled proof back; grant uses another actor.
	grant := f.allow("source-grant", "platform_admin")
	body := sourceReferenceBody(grant)
	out := f.mutate("source-bind", "/api/domains/credential-source/reference", body, 200)
	if out["credentialSource"] != "operation_account" || out["revision"] != "2" || out["connectionCredentialGeneration"] != "2" {
		t.Fatal("reference mutation returned wrong incarnation")
	}
	if f.count(`SELECT count(*) FROM adtr.domain_credentials WHERE tenant_id=$1 AND domain_id='domain-one'`, governanceHTTPTenant) != 0 {
		t.Fatal("reference copied domain ciphertext")
	}
	before := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["source-bind"].id)
	replay := f.call(f.clients["source-bind"], "/api/domains/credential-source/reference", body, 200)
	if replay["replayed"] != true || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["source-bind"].id) != before {
		t.Fatal("committed source replay consumed proof again")
	}
	changed := sourceReferenceBody(grant)
	changed["expectedRevision"] = "2"
	f.mutate("source-bind", "/api/domains/credential-source/reference", changed, 409)
	receipt := f.call(f.clients["source-bind"], "/api/domains/credential-source/mutation?idempotencyKey=source-reference-intent", nil, 200)["receipt"].(map[string]any)
	for _, field := range []string{"accountId", "grantRevision", "username", "password", "ciphertext"} {
		if _, ok := receipt[field]; ok {
			t.Fatal("source receipt discloses pointer or secret", field)
		}
	}
	detail := f.call(f.clients["source-bind"], "/api/domains/credential-source?domainId=domain-one", nil, 200)
	if detail["reference"] == nil || detail["testEligible"] != false {
		t.Fatal("source detail lost permitted pointer or invented deployment readiness")
	}
	privateDetail := f.call(f.clients["source-view"], "/api/domains/credential-source?domainId=domain-one", nil, 200)
	if privateDetail["reference"] != nil || privateDetail["testEligible"] != false || privateDetail["credentialSource"] != "operation_account" {
		t.Fatal("source reader without account metadata permission received private reference")
	}
	f.mutate("source-revoke", "/api/credential-use/revoke", governanceHTTPUseBody("platform_admin", grant, "source-revoke-intent"), 200)
	replay = f.call(f.clients["source-bind"], "/api/domains/credential-source/reference", body, 200)
	if replay["replayed"] != true {
		t.Fatal("grant revocation hid safe committed recovery")
	}
	f.mutate("source-expire", "/api/resources/tenant/save", map[string]any{"maxAdCount": 10, "expireTime": time.Now().Add(-time.Hour).Unix(), "uid": "synthetic-http", "name": "HTTP governance"}, 200)
	// Tenant-save intentionally revokes every session. Establish a new
	// synthetic session without resetting proof or actor authority, as the
	// existing external fixture does; this test covers cleanup, not login.
	reissueSourceHTTPSession(t, f, f.clients["source-view"])
	expired := f.call(f.clients["source-view"], "/api/domains/credential-source?domainId=domain-one", nil, 200)
	if expired["reference"] != nil || expired["testEligible"] != false {
		t.Fatal("expired tenant source leaked live account authority")
	}
	f.mutate("source-view", "/api/domains/credential-source/detach", map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedConnectionCredentialGeneration": "2", "idempotencyKey": "source-detach-intent"}, 200)
	if f.count(`SELECT count(*) FROM adtr.operation_account_dependencies WHERE tenant_id=$1 AND consumer_kind='domain.connection_binding'`, governanceHTTPTenant) != 0 {
		t.Fatal("detach retained binding dependency")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE kind='domain.account_connection_test'`) != 0 {
		t.Fatal("source selection silently submitted a task")
	}
}
func TestAccountSourceHTTPUnconfiguredCreationHasNoCredentialOrTask(t *testing.T) {
	f := accountSourceHTTPFixture(t)
	out := f.mutate("source-create", "/api/domains/create-unconfigured", map[string]any{"domain": "bootstrap.invalid", "dcHostName": "dc.bootstrap.invalid", "port": "389", "idempotencyKey": "unconfigured-http-create"}, 200)
	id, ok := out["domainId"].(string)
	if !ok || id == "" || out["requiresResourceAssignment"] != true {
		t.Fatal("bootstrap did not require explicit resource assignment")
	}
	if f.count(`SELECT count(*) FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id=$2 AND credential_mode='unconfigured' AND operation_account_id IS NULL`, governanceHTTPTenant, id) != 1 {
		t.Fatal("bootstrap did not persist exclusive unconfigured source")
	}
	if f.count(`SELECT count(*) FROM adtr.domain_credentials WHERE tenant_id=$1 AND domain_id=$2`, governanceHTTPTenant, id) != 0 || f.count(`SELECT count(*) FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2`, governanceHTTPTenant, id) != 0 {
		t.Fatal("bootstrap invented a credential or remote task")
	}
	f.call(f.clients["source-create"], "/api/domains/credential-source?domainId="+id, nil, 404)
}

func reissueSourceHTTPSession(t *testing.T, f *governanceHTTPFixture, c *governanceHTTPClient) {
	t.Helper()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	f.exec(`INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, hex.EncodeToString(hash[:]), c.id)
	c.cookie = &http.Cookie{Name: "adtr_session", Value: token}
	c.csrf = ""
	f.call(c, "/api/auth/me", nil, http.StatusOK)
	if c.csrf == "" {
		t.Fatal("fresh cleanup session missing CSRF")
	}
}
