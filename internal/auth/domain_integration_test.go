//go:build integration

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

type domainFixture struct {
	*taskFixture
	store   *domains.Store
	runtime *domainconfig.Runtime
}

func newDomainFixture(t *testing.T) *domainFixture {
	t.Helper()
	f := newTaskFixture(t)
	f.exec(audit.Schema + DomainPermissionSchema + domains.Schema + OperationAccountPermissionSchema + operationaccounts.Schema + credentialuse.Schema + domains.AccountReferenceSchema + OperationalLogPermissionMarks + DirectoryPermissionMarks)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'domains',true,true)", narrowResourceRole)
	runtime := domainTestRuntime(t)
	store := domains.New(runtime)
	engine, e := tasks.New(f.s.database, tasks.ProductionRegistry(store.Kind(), store.AccountKind()), taskAuthorizer(f.s.now), schemaversion.Current)
	if e != nil {
		t.Fatal(e)
	}
	f.engine = engine
	return &domainFixture{f, store, runtime}
}

// Startup files are synthetic and owned by this test. Enabling the policy does
// not itself perform DNS or network calls; tests below never reach a bind.
func domainTestRuntime(t *testing.T) *domainconfig.Runtime {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic domain test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	policy := filepath.Join(dir, "policy.json")
	if e = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(policy, []byte(`{"version":1,"targets":[{"tenantId":"one","domain":"example.test","serverNames":["dc1.example.test"],"cidrs":["10.20.0.0/24"]}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	values := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic-key", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("d", 32))), "ADTR_DOMAIN_PROBE_ENABLED": "true", "ADTR_LDAP_CA_FILE": ca, "ADTR_LDAP_EGRESS_POLICY_FILE": policy}
	r, e := domainconfig.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func (f *domainFixture) callDomain(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method := "GET"
	var raw []byte
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "/api/domains"+path, strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:4444"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.DomainsHandler(f.store, f.engine).ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("domain %s: got %d want %d %s", path, w.Code, want, w.Body)
	}
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		f.t.Fatal(e)
	}
	return out
}
func createDomainBody(key string) map[string]any {
	return map[string]any{"domain": "EXAMPLE.TEST.", "dcHostName": "DC1.EXAMPLE.TEST.", "ldapAddr": "10.20.0.8", "port": "389", "username": "synthetic-reader@example.test", "password": "Synthetic AD Secret 42", "idempotencyKey": key}
}
func (f *domainFixture) createDomain(key string) string {
	f.t.Helper()
	out := f.callDomain(f.client(f.admin), "/create", f.proof(createDomainBody(key)), 200)
	return out["domainId"].(string)
}
func (f *domainFixture) grantDomain(id string) {
	f.t.Helper()
	f.seedGroup("one", "grant-"+id, "platform_admin", id)
	f.exec("INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('one',$1,$2)", "grant-"+id, narrowResourceRole)
}
func (f *domainFixture) scalar(q string, args ...any) int {
	f.t.Helper()
	var n int
	if e := f.conn.QueryRow(f.ctx, q, args...).Scan(&n); e != nil {
		f.t.Fatal(e)
	}
	return n
}

func TestDomainEnrollmentReceiptIsolationAndCredentialPersistence(t *testing.T) {
	f := newDomainFixture(t)
	admin := f.client(f.admin)
	operator := f.client(f.operator)
	other := f.client(f.other)
	f.callDomain(nil, "", nil, 401)
	f.callDomain(f.client(f.viewer), "", nil, 403)
	f.callDomain(operator, "/create", f.proof(createDomainBody("forbidden")), 403)
	id := f.createDomain("lost-response")
	if f.scalar("SELECT count(*) FROM adtr.resource_group_members WHERE tenant_id='one' AND domain_id=$1", id) != 0 {
		t.Fatal("enrollment granted implicit scope")
	}
	f.callDomain(admin, "/detail?domainId="+id, nil, 404)
	receipt := f.callDomain(admin, "/creation?idempotencyKey=lost-response", nil, 200)["receipt"].(map[string]any)
	if receipt["domainId"] != id || receipt["domain"] != "example.test" || receipt["requiresResourceAssignment"] != true || len(receipt) != 5 {
		t.Fatal("unsafe creator receipt", receipt)
	}
	f.callDomain(operator, "/creation?idempotencyKey=lost-response", nil, 403)
	f.callDomain(other, "/creation?idempotencyKey=lost-response", nil, 404)
	replay := f.callDomain(admin, "/create", f.proof(createDomainBody("lost-response")), 200)
	if replay["domainId"] != id || replay["replayed"] != true {
		t.Fatal("lost response replay")
	}
	changed := createDomainBody("lost-response")
	changed["password"] = "Changed synthetic secret"
	f.callDomain(admin, "/create", f.proof(changed), 409)
	f.callDomain(admin, "/create", f.proof(createDomainBody("duplicate-domain")), 409)
	f.grantDomain(id)
	detail := f.callDomain(operator, "/detail?domainId="+id, nil, 200)["connection"].(map[string]any)
	if detail["credentialConfigured"] != true || detail["connectionState"] != "unverified" || detail["revision"] != "1" {
		t.Fatal(detail)
	}
	serialized, _ := json.Marshal(detail)
	for _, secret := range []string{"Synthetic AD Secret 42", "synthetic-reader", "ciphertext", "key_id"} {
		if strings.Contains(string(serialized), secret) {
			t.Fatal("credential response leak")
		}
	}
	var sealed domainconfig.SealedCredential
	if e := f.conn.QueryRow(f.ctx, "SELECT key_id,ciphertext FROM adtr.domain_credentials WHERE tenant_id='one' AND domain_id=$1", id).Scan(&sealed.KeyID, &sealed.Ciphertext); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(sealed.Ciphertext), "Synthetic AD Secret") || strings.Contains(string(sealed.Ciphertext), "synthetic-reader") {
		t.Fatal("plaintext credential storage")
	}
	raw, e := f.runtime.Vault().Open("one", id, 1, sealed)
	if e != nil || !strings.Contains(string(raw), "Synthetic AD Secret 42") {
		t.Fatal("durable ciphertext did not recover exact pair", e)
	}
	clear(raw)
	for _, scope := range []struct {
		tenant, id string
		rev        int64
	}{{"two", id, 1}, {"one", "other", 1}, {"one", id, 2}} {
		if _, e = f.runtime.Vault().Open(scope.tenant, scope.id, scope.rev, sealed); e == nil {
			t.Fatal("credential identity/revision substitution")
		}
	}
	f.callDomain(other, "/detail?domainId="+id, nil, 404)
	f.callDomain(operator, "/detail?domainId=unknown", nil, 404)
	empty := f.callDomain(operator, "?filterKeyword=%25_", nil, 200)
	if empty["page"].(map[string]any)["total"] != float64(0) {
		t.Fatal("wildcard search semantics")
	}
}

func TestDomainCASDeletionDependencyAndFreshIdentity(t *testing.T) {
	f := newDomainFixture(t)
	id := f.createDomain("create")
	f.grantDomain(id)
	admin := f.client(f.admin)
	before := f.version(f.operator)
	update := map[string]any{"domainId": id, "expectedRevision": "1", "dcHostName": "dc1.example.test", "ldapAddr": "10.20.0.8", "port": "389"}
	f.callDomain(admin, "/update", f.proof(update), 409)
	update["username"] = "synthetic-reader@example.test"
	update["password"] = "Rotated synthetic secret"
	updated := f.callDomain(admin, "/update", f.proof(update), 200)
	if updated["revision"] != "2" || f.version(f.operator) <= before {
		t.Fatal("credential edit failed revision/epoch advance")
	}
	f.callDomain(admin, "/update", f.proof(update), 409)
	detail := f.callDomain(admin, "/detail?domainId="+id, nil, 200)["connection"].(map[string]any)
	if detail["credentialRevision"] != "2" {
		t.Fatal(detail)
	}
	f.exec("INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES('one',$1,'synthetic-module','dependency')", id)
	remove := map[string]any{"domainId": id, "expectedRevision": "2", "confirmDomain": "example.test"}
	f.callDomain(admin, "/delete", f.proof(remove), 409)
	f.exec("DELETE FROM adtr.domain_dependencies WHERE tenant_id='one' AND domain_id=$1", id)
	f.callDomain(admin, "/delete", f.proof(remove), 200)
	f.callDomain(admin, "/detail?domainId="+id, nil, 404)
	if f.scalar("SELECT count(*) FROM adtr.domain_credentials WHERE tenant_id='one' AND domain_id=$1", id) != 0 || f.scalar("SELECT count(*) FROM adtr.resource_group_members WHERE tenant_id='one' AND domain_id=$1", id) != 0 || f.scalar("SELECT count(*) FROM adtr.resource_domains WHERE tenant_id='one' AND id=$1 AND active", id) != 0 {
		t.Fatal("delete retained live credential/catalogue/grant")
	}
	receipt := f.callDomain(admin, "/creation?idempotencyKey=create", nil, 200)["receipt"].(map[string]any)
	if receipt["deleted"] != true {
		t.Fatal("receipt lost tombstone")
	}
	fresh := f.createDomain("recreated")
	if fresh == id {
		t.Fatal("deleted ID resurrected")
	}
	f.callDomain(admin, "/detail?domainId="+fresh, nil, 404)
	if _, e := f.conn.Exec(f.ctx, "UPDATE adtr.domain_connections SET deleted_at=NULL WHERE tenant_id='one' AND domain_id=$1", id); e == nil {
		t.Fatal("tombstone revived")
	}
	if _, e := f.conn.Exec(f.ctx, "UPDATE adtr.domain_audit SET result='rewritten'"); e == nil {
		t.Fatal("audit mutable")
	}
}

func TestDomainAdmissionIdempotencyEpochAndCancellation(t *testing.T) {
	f := newDomainFixture(t)
	id := f.createDomain("create")
	f.grantDomain(id)
	admin := f.client(f.admin)
	operator := f.client(f.operator)
	epoch := f.version(f.admin)
	request := func(key string) map[string]any {
		return map[string]any{"domainId": id, "expectedRevision": "1", "idempotencyKey": key}
	}
	first := f.callDomain(admin, "/test", f.proof(request("probe")), 200)
	task := taskOutputID(t, first)
	if f.version(f.admin) != epoch {
		t.Fatal("diagnostic submission bumped authorization epoch")
	}
	again := f.callDomain(admin, "/test", f.proof(request("probe")), 200)
	if again["replayed"] != true || taskOutputID(t, again) != task {
		t.Fatal("diagnostic replay duplicated")
	}
	f.callDomain(operator, "/test", f.proof(request("probe")), 409)
	if f.scalar("SELECT diagnostic_generation FROM adtr.domain_connections WHERE tenant_id='one' AND domain_id=$1", id) != 2 {
		t.Fatal("replay advanced generation")
	}
	var payload, result string
	var attempts int
	if e := f.conn.QueryRow(f.ctx, "SELECT payload::text,result::text,max_attempts FROM adtr.tasks WHERE task_id=$1", task).Scan(&payload, &result, &attempts); e != nil {
		t.Fatal(e)
	}
	if attempts != 1 || strings.Contains(payload, "password") || strings.Contains(payload, "10.20") || strings.Contains(payload, "dc1") {
		t.Fatal("unsafe task admission")
	}
	detail := f.callDomain(admin, "/detail?domainId="+id, nil, 200)["connection"].(map[string]any)
	if detail["latestTaskUUID"] != task || detail["connectionState"] != "testing" || detail["lastDiagnostic"] != nil {
		t.Fatal("pending checkpoint falsely published", detail)
	}
	f.callTask(operator, "/cancel", f.proof(map[string]any{"taskUUID": task}), 200)
	f.callTask(admin, "/recover", f.proof(map[string]any{"taskUUID": task, "idempotencyKey": "recover"}), 400)
	f.callTask(admin, "/submit", f.proof(map[string]any{"taskName": domains.KindName, "domainId": id, "payloadVersion": 1, "payload": json.RawMessage(payload), "idempotencyKey": "generic"}), 400)
	next := taskOutputID(t, f.callDomain(admin, "/test", f.proof(request("next")), 200))
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
	f.callDomain(operator, "/test", f.proof(request("revoked")), 403)
	// Configuration mutation invalidates even a builtin actor's already queued task.
	f.callDomain(admin, "/update", f.proof(map[string]any{"domainId": id, "expectedRevision": "1", "dcHostName": "dc1.example.test", "ldapAddr": "10.20.0.9", "port": "389"}), 200)
	lease, e := f.engine.Claim(context.Background(), "synthetic-worker")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.engine.Start(context.Background(), lease); e != tasks.ErrAuthorization {
		t.Fatal("stale admission started", e)
	}
	state, code, _ := f.taskState(next)
	if state != "failed" || code != "authorization_revoked" {
		t.Fatal(state, code)
	}
}

func TestDomainTenantEligibilityAndSchemaGateBeforeProof(t *testing.T) {
	f := newDomainFixture(t)
	admin := f.client(f.admin)
	f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=2 WHERE tenant_id='one'")
	f.callDomain(admin, "/create", f.proof(createDomainBody("capacity")), 409)
	f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=0 WHERE tenant_id='one'")
	f.callDomain(admin, "/create", f.proof(createDomainBody("zero")), 403)
	f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=10,expire_time=1 WHERE tenant_id='one'")
	f.callDomain(admin, "/create", f.proof(createDomainBody("expired")), 403)
	f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'", f.now.Add(time.Hour).Unix())
	proof := f.proof(createDomainBody("schema"))
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current+1)
	f.callDomain(admin, "/create", proof, 503)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.callDomain(admin, "/create", proof, 200)
	// Expired/exhausted eligibility blocks new enrollment, not the original
	// creator's safe recovery receipt or exact same-request replay.
	f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=0,expire_time=1 WHERE tenant_id='one'")
	f.callDomain(admin, "/creation?idempotencyKey=schema", nil, 200)
	replay := f.callDomain(admin, "/create", f.proof(createDomainBody("schema")), 200)
	if replay["replayed"] != true {
		t.Fatal("safe creator replay depended on a new capacity reservation")
	}
	f.store = domains.New(nil)
	f.callDomain(admin, "/creation?idempotencyKey=schema", nil, 200)
}

func TestDomainCipherTamperFailsBeforeNetwork(t *testing.T) {
	for _, variant := range []string{"ciphertext", "key"} {
		t.Run(variant, func(t *testing.T) {
			f := newDomainFixture(t)
			id := f.createDomain("create")
			f.grantDomain(id)
			admin := f.client(f.admin)
			// Same-generation corruption is now rejected at the database boundary.
			if _, e := f.conn.Exec(f.ctx, "UPDATE adtr.domain_credentials SET key_id='missing-key' WHERE tenant_id='one' AND domain_id=$1", id); e == nil {
				t.Fatal("same-generation credential mutation accepted")
			}
			raw := []byte(`{"version":1,"username":"synthetic-reader","password":"Synthetic AD Secret 42"}`)
			sealed, e := f.runtime.Vault().Seal("one", id, 2, raw)
			clear(raw)
			if e != nil {
				t.Fatal(e)
			}
			if variant == "key" {
				sealed.KeyID = "missing-key"
			} else {
				sealed.Ciphertext[30] ^= 1
			}
			// Model a bad sealed value supplied by a faulty writer while honoring
			// all generation/source constraints. No trigger is disabled.
			tx, e := f.conn.Begin(f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(f.ctx, "UPDATE adtr.domain_credentials SET credential_revision=2,key_id=$2,ciphertext=$3 WHERE tenant_id='one' AND domain_id=$1", id, sealed.KeyID, sealed.Ciphertext); e != nil {
				tx.Rollback(f.ctx)
				t.Fatal(e)
			}
			if _, e = tx.Exec(f.ctx, "UPDATE adtr.domain_connections SET connection_revision=2,credential_revision=2,diagnostic_generation=diagnostic_generation+1,latest_test_task_id=NULL WHERE tenant_id='one' AND domain_id=$1", id); e != nil {
				tx.Rollback(f.ctx)
				t.Fatal(e)
			}
			if e = tx.Commit(f.ctx); e != nil {
				t.Fatal(e)
			}
			task := taskOutputID(t, f.callDomain(admin, "/test", f.proof(map[string]any{"domainId": id, "expectedRevision": "2", "idempotencyKey": "probe"}), 200))

			worked, e := f.engine.RunOne(context.Background(), "synthetic-worker")
			if e != nil || !worked {
				t.Fatal(worked, e)
			}
			state, code, _ := f.taskState(task)
			want := "credential_unavailable"
			if variant == "key" {
				want = "credential_key_unavailable"
			}
			if state != "failed" || code != want {
				t.Fatal(state, code)
			}
			if f.scalar("SELECT count(*) FROM adtr.domain_diagnostics WHERE task_id=$1", task) != 0 {
				t.Fatal("tampered credential became observation")
			}
		})
	}
}

func TestDomainAuditFailureRollsBackConfigurationAndProof(t *testing.T) {
	f := newDomainFixture(t)
	admin := f.client(f.admin)
	f.exec(`CREATE FUNCTION adtr.synthetic_domain_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic rollback'; END; $$; CREATE TRIGGER synthetic_domain_audit_failure BEFORE INSERT ON adtr.domain_audit FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_domain_audit_failure();`)
	body := f.proof(createDomainBody("rollback"))
	f.callDomain(admin, "/create", body, 500)
	if f.scalar("SELECT count(*) FROM adtr.domain_connections") != 0 || f.scalar("SELECT count(*) FROM adtr.domain_creation_receipts") != 0 {
		t.Fatal("audit failure left partial configuration")
	}
	f.exec("DROP TRIGGER synthetic_domain_audit_failure ON adtr.domain_audit")
	f.callDomain(admin, "/create", body, 200)
}

func TestDomainConcurrentDuplicateAndFinalSlotAdmission(t *testing.T) {
	for _, scenario := range []string{"duplicate", "last_slot"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDomainFixture(t)
			f.exec("UPDATE adtr.users SET role='platform_admin',role_id='' WHERE id=$1", f.operator)
			if scenario == "last_slot" {
				f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=3 WHERE tenant_id='one'")
			}
			clients := []*resourceTestClient{f.client(f.admin), f.client(f.operator)}
			bodies := []map[string]any{createDomainBody("concurrent-a"), createDomainBody("concurrent-b")}
			f.proof(bodies[0])
			bodies[1]["actorPassword"], bodies[1]["totpCode"] = bodies[0]["actorPassword"], bodies[0]["totpCode"]
			if scenario == "last_slot" {
				bodies[1]["domain"] = "second.test"
			}
			start := make(chan struct{})
			status := make(chan int, 2)
			var group sync.WaitGroup
			for i := range clients {
				group.Add(1)
				go func(i int) {
					defer group.Done()
					raw, _ := json.Marshal(bodies[i])
					r := httptest.NewRequest("POST", "/api/domains/create", strings.NewReader(string(raw)))
					r.RemoteAddr = "127.0.0.1:4422"
					r.Header.Set("Origin", f.s.origin)
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("X-CSRF-Token", clients[i].csrf)
					r.AddCookie(clients[i].cookie)
					w := httptest.NewRecorder()
					<-start
					f.s.DomainsHandler(f.store, f.engine).ServeHTTP(w, r)
					status <- w.Code
				}(i)
			}
			close(start)
			group.Wait()
			a, b := <-status, <-status
			if !(a == 200 && b == 409 || b == 200 && a == 409) {
				t.Fatalf("concurrent %s: %d, %d", scenario, a, b)
			}
			if f.scalar("SELECT count(*) FROM adtr.domain_connections") != 1 || f.scalar("SELECT count(*) FROM adtr.domain_creation_receipts") != 1 || f.scalar("SELECT count(*) FROM adtr.domain_credentials") != 1 {
				t.Fatal("concurrent admission produced partial or duplicate storage")
			}
		})
	}
}

func TestDomainCreatorReceiptDoesNotBecomeSameTenantAdminMetadata(t *testing.T) {
	f := newDomainFixture(t)
	id := f.createDomain("creator-bound")
	f.exec("UPDATE adtr.users SET role='platform_admin',role_id='' WHERE id=$1", f.operator)
	secondAdmin := f.client(f.operator)
	f.callDomain(secondAdmin, "/creation?idempotencyKey=creator-bound", nil, 404)
	response := f.callDomain(secondAdmin, "/create", f.proof(createDomainBody("creator-bound")), 409)
	if _, exposed := response["domainId"]; exposed {
		t.Fatal("different creator recovered original ID")
	}
	f.callDomain(secondAdmin, "/detail?domainId="+id, nil, 404)
	if f.scalar("SELECT count(*) FROM adtr.domain_creation_receipts") != 1 {
		t.Fatal("second creator obtained a receipt")
	}
}

func TestDomainListScopePrecedesCountFiltersAndPage(t *testing.T) {
	f := newDomainFixture(t)
	a := f.createDomain("first")
	admin := f.client(f.admin)
	second, third := createDomainBody("second"), createDomainBody("third")
	second["domain"], third["domain"] = "second.test", "third.test"
	b := f.callDomain(admin, "/create", f.proof(second), 200)["domainId"].(string)
	c := f.callDomain(admin, "/create", f.proof(third), 200)["domainId"].(string)
	f.seedGroup("one", "operator-scope", narrowResourceRole, a, b)
	operator := f.client(f.operator)
	for _, client := range []*resourceTestClient{admin, f.client(f.other)} {
		out := f.callDomain(client, "", nil, 200)
		if out["page"].(map[string]any)["total"] != float64(0) || len(out["List"].([]any)) != 0 || out["exhausted"] != true {
			t.Fatal("list scope bypass", out)
		}
	}
	for _, page := range []string{"1", "2"} {
		out := f.callDomain(operator, "?pageSize=1&pageIdx="+page, nil, 200)
		if out["page"].(map[string]any)["total"] != float64(2) || out["page"].(map[string]any)["totalPage"] != float64(2) || len(out["List"].([]any)) != 1 {
			t.Fatal("scope applied after page/count", out)
		}
		if out["List"].([]any)[0].(map[string]any)["domainId"] == c {
			t.Fatal("ungranted row escaped list")
		}
	}
	out := f.callDomain(operator, "?pageSize=1&pageIdx=99", nil, 200)
	if len(out["List"].([]any)) != 0 || out["page"].(map[string]any)["total"] != float64(2) || out["exhausted"] != true {
		t.Fatal("out of range count lost", out)
	}
	f.callDomain(operator, "/test", f.proof(map[string]any{"domainId": a, "expectedRevision": "1", "idempotencyKey": "test"}), 200)
	for _, query := range []string{"?filterStatus=unverified", "?filterDomain=third.test&filterDomain=SECOND.TEST."} {
		out = f.callDomain(operator, query, nil, 200)
		if out["page"].(map[string]any)["total"] != float64(1) || out["List"].([]any)[0].(map[string]any)["domainId"] != b {
			t.Fatal("scope/filter intersection", out)
		}
	}
	out = f.callDomain(operator, "?filterKeyword=THIRD", nil, 200)
	if out["page"].(map[string]any)["total"] != float64(0) {
		t.Fatal("keyword count leaked ungranted domain")
	}
}

func TestDomainGrantRevokeRegrantNeverRevivesOldTaskEpoch(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "running"}[running], func(t *testing.T) {
			f := newDomainFixture(t)
			id := f.createDomain("created")
			f.grantDomain(id)
			operator := f.client(f.operator)
			task := taskOutputID(t, f.callDomain(operator, "/test", f.proof(map[string]any{"domainId": id, "expectedRevision": "1", "idempotencyKey": "probe"}), 200))
			lease, e := f.engine.Claim(context.Background(), "synthetic-worker")
			if e != nil {
				t.Fatal(e)
			}
			if running {
				lease, e = f.engine.Start(context.Background(), lease)
				if e != nil {
					t.Fatal(e)
				}
			}
			old := f.version(f.operator)
			f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
			f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
			if f.version(f.operator) <= old {
				t.Fatal("revoke/regrant failed to advance durable epoch")
			}
			if running {
				called := false
				_, e = f.engine.WithTx(context.Background(), lease, lease.Task.ResultVersion, func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
					called = true
					return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
				})
				if e != tasks.ErrAuthorization || called {
					t.Fatal("stale running epoch reached module callback", e)
				}
				if _, e = f.engine.Finish(context.Background(), lease, tasks.Outcome{State: tasks.Failed, Code: "authorization_revoked"}); e != nil {
					t.Fatal(e)
				}
			} else if _, e = f.engine.Start(context.Background(), lease); e != tasks.ErrAuthorization {
				t.Fatal("regrant revived queued task", e)
			}
			state, code, _ := f.taskState(task)
			if state != "failed" || code != "authorization_revoked" {
				t.Fatal(state, code)
			}
		})
	}
}
