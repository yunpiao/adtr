//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

type operationAccountFixture struct {
	*domainFixture
	accounts *operationaccounts.Store
	domain   string
}

func newOperationAccountFixture(t *testing.T) *operationAccountFixture {
	t.Helper()
	d := newDomainFixture(t)
	id := d.createDomain("parent-create")
	d.grantDomain(id)
	d.exec(audit.AccountReferenceViewSchema)
	d.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	d.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'operation_accounts',true,true)", narrowResourceRole)
	values := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic-account-key", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))}
	r, e := domainconfig.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal(e)
	}
	if !r.Enabled() || r.ProbeEnabled() {
		t.Fatal("storage-only runtime")
	}
	d.runtime = r
	d.engine, e = tasks.New(d.s.database, tasks.ProductionRegistry(d.store.Kind()), taskAuthorizer(d.s.now), schemaversion.Current)
	if e != nil {
		t.Fatal(e)
	}
	return &operationAccountFixture{d, operationaccounts.New(r), id}
}
func (f *operationAccountFixture) request(c *resourceTestClient, path string, body map[string]any) *httptest.ResponseRecorder {
	method := "GET"
	var raw []byte
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "/api/operation-accounts"+path, bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:5566"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-ID", "synthetic-account-request")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.OperationAccountsHandler(f.accounts).ServeHTTP(w, r)
	return w
}
func (f *operationAccountFixture) callAccount(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	w := f.request(c, path, body)
	if w.Code != want {
		f.t.Fatalf("operation accounts %s: got %d want %d: %s", path, w.Code, want, w.Body)
	}
	if want == 200 {
		if c == nil || w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(c.id, 10) {
			f.t.Fatal("operation-account response lost actor binding")
		}
	} else if w.Header().Get("X-ADTR-User-ID") != "" {
		f.t.Fatal("failed response exposed actor binding")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("cacheable credential metadata")
	}
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		f.t.Fatal(e)
	}
	return out
}
func (f *operationAccountFixture) createBody(key string) map[string]any {
	return map[string]any{"domainId": f.domain, "expectedDomainRevision": "1", "username": "registry-reader@alternate.test", "password": " Synthetic Secret * 42 ", "label": "Registry label", "idempotencyKey": key}
}
func (f *operationAccountFixture) createAccount(key string) string {
	f.t.Helper()
	return f.callAccount(f.client(f.admin), "/create", f.proof(f.createBody(key)), 200)["accountId"].(string)
}
func (f *operationAccountFixture) accountBody(id, revision, key string) map[string]any {
	return map[string]any{"accountId": id, "expectedRevision": revision, "idempotencyKey": key}
}
func (f *operationAccountFixture) deleteBody(id, revision, key string) map[string]any {
	b := f.accountBody(id, revision, key)
	b["confirmAccountId"] = id
	return b
}
func (f *operationAccountFixture) encrypted(id string) (int64, domainconfig.OperationSealedCredential) {
	f.t.Helper()
	var rev int64
	var sealed domainconfig.OperationSealedCredential
	if e := f.conn.QueryRow(f.ctx, `SELECT credential_revision,key_id,ciphertext FROM adtr.operation_account_credentials WHERE tenant_id='one' AND domain_id=$1 AND account_id=$2`, f.domain, id).Scan(&rev, &sealed.KeyID, &sealed.Ciphertext); e != nil {
		f.t.Fatal(e)
	}
	return rev, sealed
}

func TestOperationAccountPersistentCRUDAndNoNetwork(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin := f.client(f.admin)
	oldEpoch := f.version(f.admin)
	id := f.createAccount("create")
	if f.version(f.admin) <= oldEpoch {
		t.Fatal("creation lifecycle failed to bump authorization epoch")
	}
	if f.scalar("SELECT count(*) FROM adtr.tasks") != 0 || f.scalar("SELECT count(*) FROM adtr.operation_account_dependencies") != 0 {
		t.Fatal("save admitted a remote consumer/task")
	}
	if f.scalar("SELECT count(*) FROM adtr.domain_credentials") != 1 || f.scalar("SELECT connection_revision FROM adtr.domain_connections WHERE domain_id=$1", f.domain) != 1 {
		t.Fatal("F01 credential/configuration changed")
	}
	rev, sealed := f.encrypted(id)
	raw, e := f.runtime.Vault().OpenOperationCredential("one", f.domain, id, rev, sealed)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(raw, []byte(`"password":" Synthetic Secret * 42 "`)) || !bytes.Contains(raw, []byte(`"username":"registry-reader@alternate.test"`)) {
		t.Fatal("credential bytes changed")
	}
	clear(raw)
	if _, e = f.runtime.Vault().OpenOperationCredential("one", f.domain, "other-account", rev, sealed); e == nil {
		t.Fatal("cross-account envelope substitution")
	}
	restarted := operationaccounts.New(f.runtime)
	f.accounts = restarted
	detail := f.callAccount(admin, "/detail?accountId="+id, nil, 200)["account"].(map[string]any)
	if detail["revision"] != "1" || detail["credentialRevision"] != "1" || detail["credentialConfigured"] != true || detail["verificationState"] != "unverified" || detail["storageState"] != "saved" {
		t.Fatal(detail)
	}
	oldEpoch = f.version(f.admin)
	update := f.accountBody(id, "1", "label")
	update["label"] = "changed"
	f.callAccount(admin, "/update", f.proof(update), 200)
	if f.version(f.admin) != oldEpoch {
		t.Fatal("label-only edit revoked unrelated work")
	}
	_, same := f.encrypted(id)
	if !bytes.Equal(sealed.Ciphertext, same.Ciphertext) {
		t.Fatal("metadata edit resealed credential")
	}
	update = f.accountBody(id, "2", "pair")
	update["username"] = "second@example.test"
	update["password"] = "*"
	out := f.callAccount(admin, "/update", f.proof(update), 200)
	if out["revision"] != "3" || out["credentialRevision"] != "2" || f.version(f.admin) <= oldEpoch {
		t.Fatal("pair generation", out)
	}
	_, replacement := f.encrypted(id)
	if bytes.Equal(sealed.Ciphertext, replacement.Ciphertext) {
		t.Fatal("pair did not replace ciphertext")
	}
	if f.scalar("SELECT count(*) FROM adtr.operation_account_credentials") != 1 {
		t.Fatal("secret history retained")
	}
	f.exec("INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES('one',$1,'synthetic','retain')", f.domain)
	out = f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "3", "delete")), 200)
	if out["deleted"] != true || out["revision"] != "4" || out["credentialRevision"] != "3" {
		t.Fatal(out)
	}
	if f.scalar("SELECT count(*) FROM adtr.operation_account_credentials") != 0 || f.scalar("SELECT count(*) FROM adtr.domain_dependencies WHERE module='operation_accounts'") != 0 || f.scalar("SELECT count(*) FROM adtr.domain_dependencies WHERE module='synthetic'") != 1 {
		t.Fatal("local delete did not remove exactly its pair/pin")
	}
	f.callAccount(admin, "/detail?accountId="+id, nil, 404)
	receipt := f.callAccount(admin, "/mutation?idempotencyKey=create", nil, 200)["receipt"].(map[string]any)
	if receipt["revision"] != "1" || receipt["credentialRevision"] != "1" || receipt["currentRevision"] != "4" || receipt["deleted"] != true {
		t.Fatal("receipt confused original/current generations", receipt)
	}
	if f.scalar("SELECT count(*) FROM adtr.operation_account_audit") != 4 || f.scalar("SELECT count(*) FROM adtr.operation_account_mutations") != 4 {
		t.Fatal("missing durable receipts/audit")
	}
	var records string
	if e = f.conn.QueryRow(f.ctx, `SELECT string_agg(row_to_json(a)::text,'') FROM adtr.operation_account_audit a`).Scan(&records); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"registry-reader", "Synthetic Secret", "Registry label", "changed"} {
		if strings.Contains(records, secret) {
			t.Fatal("secret/input in audit")
		}
	}
	if strings.Contains(records, "synthetic-account-request") || f.scalar(`SELECT count(*) FROM adtr.operation_account_audit WHERE audit_username='admin' AND audit_ip='127.0.0.1' AND audit_path LIKE '/api/operation-accounts/%' AND length(audit_request_id)>0`) != 4 {
		t.Fatal("trusted audit request metadata missing or caller correlation ID trusted")
	}
}

func TestOperationAccountIdempotencyRecoveryAndCurrentAuthorization(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin := f.client(f.admin)
	in := f.createBody("same-key")
	created := f.callAccount(admin, "/create", f.proof(in), 200)
	id := created["accountId"].(string)
	update := f.accountBody(id, "1", "edit")
	update["label"] = "later"
	f.callAccount(admin, "/update", f.proof(update), 200)
	replay := f.callAccount(admin, "/create", f.proof(in), 200)
	if replay["replayed"] != true || replay["revision"] != "1" || replay["currentRevision"] != "2" || replay["accountId"] != id {
		t.Fatal(replay)
	}
	in["password"] = "different"
	if out := f.callAccount(admin, "/create", f.proof(in), 409); out["error"] != "idempotency_conflict" {
		t.Fatal(out)
	}
	if out := f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "2", "same-key")), 409); out["error"] != "idempotency_conflict" {
		t.Fatal(out)
	}
	f.callAccount(f.client(f.operator), "/mutation?idempotencyKey=same-key", nil, 404)
	f.callAccount(f.client(f.other), "/mutation?idempotencyKey=same-key", nil, 404)
	f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "2", "delete")), 200)
	deletedReplay := f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "2", "delete")), 200)
	if deletedReplay["replayed"] != true || deletedReplay["deleted"] != true {
		t.Fatal(deletedReplay)
	}
	f.exec("DELETE FROM adtr.resource_role_groups WHERE tenant_id='one' AND role_id='platform_admin'")
	f.callAccount(admin, "/mutation?idempotencyKey=same-key", nil, 404)
	f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "2", "delete")), 404)
	if f.scalar("SELECT count(*) FROM adtr.operation_accounts") != 1 || f.scalar("SELECT count(*) FROM adtr.operation_account_mutations") != 3 {
		t.Fatal("replay mutated storage")
	}
}

func TestOperationAccountCASDependenciesAndAtomicAudit(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin := f.client(f.admin)
	id := f.createAccount("create")
	f.exec("INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES('one',$1,$2,'reserved.synthetic','fixture')", f.domain, id)
	update := f.accountBody(id, "1", "pair")
	update["username"] = "second@example.test"
	update["password"] = "new"
	for _, req := range []struct {
		path string
		body map[string]any
	}{{"/update", update}, {"/delete", f.deleteBody(id, "1", "delete")}} {
		if out := f.callAccount(admin, req.path, f.proof(req.body), 409); out["error"] != "account_in_use" {
			t.Fatal(out)
		}
	}
	metadata := f.accountBody(id, "1", "label")
	metadata["label"] = "allowed while bound"
	f.callAccount(admin, "/update", f.proof(metadata), 200)
	f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "1", "stale")), 409)
	f.callDomain(admin, "/delete", f.proof(map[string]any{"domainId": f.domain, "expectedRevision": "1", "confirmDomain": "example.test"}), 409)
	f.exec("DELETE FROM adtr.operation_account_dependencies WHERE tenant_id='one' AND account_id=$1", id)
	f.exec(`CREATE FUNCTION adtr.synthetic_operation_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic rollback'; END; $$; CREATE TRIGGER synthetic_operation_audit_failure BEFORE INSERT ON adtr.operation_account_audit FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_operation_audit_failure();`)
	beforeEpoch := f.version(f.admin)
	beforeRev, beforeSecret := f.encrypted(id)
	update["expectedRevision"] = "2"
	body := f.proof(update)
	f.callAccount(admin, "/update", body, 500)
	afterRev, afterSecret := f.encrypted(id)
	if beforeRev != afterRev || !bytes.Equal(beforeSecret.Ciphertext, afterSecret.Ciphertext) || f.version(f.admin) != beforeEpoch || f.scalar("SELECT count(*) FROM adtr.operation_account_mutations") != 2 {
		t.Fatal("audit failure left partial replacement")
	}
	f.exec("DROP TRIGGER synthetic_operation_audit_failure ON adtr.operation_account_audit")
	f.callAccount(admin, "/update", body, 200) // exact same proof remains unused after rollback
	unchanged := f.accountBody(id, "3", "no-change")
	if out := f.callAccount(admin, "/update", f.proof(unchanged), 409); out["error"] != "no_change" {
		t.Fatal(out)
	}
}

func TestOperationAccountScopeBeforePageAndSafeParentPicker(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin := f.client(f.admin)
	operator := f.client(f.operator)
	for _, key := range []string{"a", "b", "c"} {
		f.createAccount(key)
	}
	second := createDomainBody("second-parent")
	second["domain"] = "hidden.test"
	hidden := f.callDomain(admin, "/create", f.proof(second), 200)["domainId"].(string)
	f.seedGroup("one", "admin-hidden", "platform_admin", hidden)
	body := f.createBody("hidden")
	body["domainId"] = hidden
	body["label"] = "hidden"
	f.callAccount(admin, "/create", f.proof(body), 200)
	for _, page := range []string{"1", "2", "99"} {
		out := f.callAccount(operator, "?pageSize=2&pageIdx="+page, nil, 200)
		p := out["page"].(map[string]any)
		if p["total"] != float64(3) || p["totalPage"] != float64(2) {
			t.Fatal(out)
		}
		for _, item := range out["List"].([]any) {
			if item.(map[string]any)["domainId"] != f.domain {
				t.Fatal("page leaked hidden scope")
			}
		}
	}
	for _, query := range []string{"?filterDomain=HIDDEN.TEST.", "?filterKeyword=hidden", "?filterKeyword=%25_"} {
		out := f.callAccount(operator, query, nil, 200)
		if out["page"].(map[string]any)["total"] != float64(0) {
			t.Fatal("filter leaked hidden count or wildcard", out)
		}
	}
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
	parents := f.callAccount(operator, "/domains", nil, 200)
	if parents["page"].(map[string]any)["total"] != float64(1) {
		t.Fatal(parents)
	}
	parent := parents["domains"].([]any)[0].(map[string]any)
	if len(parent) != 3 || parent["domainId"] != f.domain || parent["revision"] != "1" {
		t.Fatal("unsafe parent projection", parent)
	}
	for _, query := range []string{"?isPlaintext=1", "?filterStatus=unverified", "?filterStatus=other"} {
		f.callAccount(operator, query, nil, 422)
	}
	f.callAccount(operator, "/domains?filterDomain=example.test", nil, 400)
	f.callAccount(f.client(f.other), "", nil, 200)
	f.exec("DELETE FROM adtr.resource_role_groups WHERE tenant_id='one' AND role_id='platform_admin'")
	if out := f.callAccount(admin, "", nil, 200); out["page"].(map[string]any)["total"] != float64(0) {
		t.Fatal("builtin scope bypass")
	}
	f.callAccount(admin, "/create", f.proof(f.createBody("denied")), 404)
}

func TestOperationAccountProofSessionAndPolicyGates(t *testing.T) {
	for _, gate := range []string{"anonymous", "session", "csrf", "origin", "proof", "mfa", "forced", "function", "expiry", "capacity", "schema", "key"} {
		t.Run(gate, func(t *testing.T) {
			f := newOperationAccountFixture(t)
			admin := f.client(f.admin)
			body := f.proof(f.createBody("gated"))
			want := 403
			switch gate {
			case "anonymous":
				admin = nil
				want = 401
			case "session":
				f.exec("DELETE FROM adtr.sessions WHERE user_id=$1", f.admin)
				want = 401
			case "csrf":
				admin.csrf = "wrong"
			case "origin":
				f.s.origin = "https://other.example" // request below pins original origin
			case "proof":
				body["actorPassword"] = "wrong"
				want = 401
			case "mfa":
				f.exec("UPDATE adtr.users SET mfa_secret='' WHERE id=$1", f.admin)
			case "forced":
				f.exec("UPDATE adtr.users SET must_change=true WHERE id=$1", f.admin)
			case "function":
				admin = f.client(f.operator)
				f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='operation_accounts'", narrowResourceRole)
			case "expiry":
				f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'", f.now.Unix())
			case "capacity":
				f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=1 WHERE tenant_id='one'")
			case "schema":
				f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current-1)
				want = 503
			case "key":
				f.accounts = operationaccounts.New(nil)
				want = 503
			}
			if gate == "origin" {
				raw, _ := json.Marshal(body)
				r := httptest.NewRequest("POST", "/api/operation-accounts/create", bytes.NewReader(raw))
				r.Header.Set("Origin", "http://localhost:8080")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				f.s.OperationAccountsHandler(f.accounts).ServeHTTP(w, r)
				if w.Code != 403 {
					t.Fatal(w.Code)
				}
			} else {
				f.callAccount(admin, "/create", body, want)
			}
			if f.scalar("SELECT count(*) FROM adtr.operation_accounts") != 0 || f.scalar("SELECT count(*) FROM adtr.operation_account_credentials") != 0 {
				t.Fatal("gate left side effects")
			}
		})
	}
}

func TestOperationAccountConcurrentCASAndDuplicate(t *testing.T) {
	f := newOperationAccountFixture(t)
	id := f.createAccount("create")
	clients := []*resourceTestClient{f.client(f.admin), f.client(f.operator)}
	bodies := []map[string]any{f.accountBody(id, "1", "edit-a"), f.accountBody(id, "1", "edit-b")}
	bodies[0]["label"] = "a"
	bodies[1]["label"] = "b"
	f.proof(bodies[0])
	bodies[1]["actorPassword"] = bodies[0]["actorPassword"]
	bodies[1]["totpCode"] = bodies[0]["totpCode"]
	start := make(chan struct{})
	codes := make(chan int, 2)
	var group sync.WaitGroup
	for i := range clients {
		group.Add(1)
		go func(i int) { defer group.Done(); <-start; codes <- f.request(clients[i], "/update", bodies[i]).Code }(i)
	}
	close(start)
	group.Wait()
	a, b := <-codes, <-codes
	if !(a == 200 && b == 409 || a == 409 && b == 200) {
		t.Fatal("concurrent CAS", a, b)
	}
	// Same actor/key admission serializes under tenant + actor locks. This tests
	// the durable duplicate race independently of one-use HTTP proof semantics.
	var in operationaccounts.Input
	username, password := "duplicate@example.test", "same"
	in = operationaccounts.Input{DomainID: f.domain, ExpectedDomainRevision: "1", IdempotencyKey: "duplicate", Username: &username, Password: &password}
	results := make(chan operationaccounts.Receipt, 2)
	errs := make(chan error, 2)
	start = make(chan struct{})
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			conn, e := pgx.ConnectConfig(context.Background(), f.s.database.Copy())
			if e != nil {
				errs <- e
				return
			}
			defer conn.Close(context.Background())
			tx, e := conn.Begin(context.Background())
			if e != nil {
				errs <- e
				return
			}
			defer tx.Rollback(context.Background())
			if e = tasks.CheckSchemaTx(context.Background(), tx, schemaversion.Current); e == nil {
				e = lockIdentityTenant(context.Background(), tx, "one")
			}
			if e == nil {
				_, e = tx.Exec(context.Background(), "SELECT id FROM adtr.users WHERE id=$1 FOR UPDATE", f.admin)
			}
			var out operationaccounts.Receipt
			if e == nil {
				out, e = f.accounts.MutateTx(context.Background(), tx, tasks.Principal{TenantID: "one", ActorID: f.admin}, "/create", in, []string{f.domain})
			}
			if e == nil {
				e = tx.Commit(context.Background())
			}
			if e != nil {
				errs <- e
			} else {
				results <- out
			}
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	first, second := <-results, <-results
	if first.AccountID != second.AccountID || first.Replayed == second.Replayed {
		t.Fatal("concurrent duplicate created twice", first, second)
	}
}

func TestOperationAccountDatabaseGuardsAndReceiptRevocation(t *testing.T) {
	f := newOperationAccountFixture(t)
	id := f.createAccount("created")
	admin := f.client(f.admin)
	for _, sql := range []string{
		`UPDATE adtr.operation_accounts SET revision=revision+1 WHERE account_id=$1`,
		`UPDATE adtr.operation_accounts SET domain_id='domain-b' WHERE account_id=$1`,
		`DELETE FROM adtr.operation_accounts WHERE account_id=$1`,
		`DELETE FROM adtr.operation_account_credentials WHERE account_id=$1`,
		`DELETE FROM adtr.operation_account_mutations WHERE account_id=$1`,
		`UPDATE adtr.operation_account_audit SET result='deleted' WHERE account_id=$1`,
		`DELETE FROM adtr.domain_dependencies WHERE module='operation_accounts' AND object_id=$1`,
	} {
		if _, e := f.conn.Exec(f.ctx, sql, id); e == nil {
			t.Fatal("direct SQL bypass", sql)
		}
	}
	for _, table := range []string{"operation_accounts", "operation_account_credentials", "operation_account_mutations", "operation_account_audit", "operation_account_dependencies", "domain_dependencies"} {
		if _, e := f.conn.Exec(f.ctx, "TRUNCATE adtr."+table+" CASCADE"); e == nil {
			t.Fatal("truncate bypass", table)
		}
	}
	if _, e := f.conn.Exec(f.ctx, `UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id=$1`, f.domain); e == nil {
		t.Fatal("catalogue deactivation orphaned registration")
	}
	f.callAccount(admin, "/delete", f.proof(f.deleteBody(id, "1", "deleted")), 200)
	f.callDomain(admin, "/delete", f.proof(map[string]any{"domainId": f.domain, "expectedRevision": "1", "confirmDomain": "example.test"}), 200)
	f.callAccount(admin, "/mutation?idempotencyKey=created", nil, 404)
}

func TestOperationAccountAllRowsCapAndRevisionOverflow(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin := f.client(f.admin)
	id := f.createAccount("template")
	// Synthetic bulk fixtures retain real encrypted-row and dependency invariants.
	tx, e := f.conn.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	for _, q := range []string{
		`INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) SELECT 'one',$1,'bulk-'||n,'bulk' FROM generate_series(1,1000) n`,
		`INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) SELECT 'one',$1,'bulk-'||n,1,c.key_id,c.ciphertext FROM generate_series(1,1000) n CROSS JOIN adtr.operation_account_credentials c WHERE c.tenant_id='one' AND c.domain_id=$1 AND c.account_id='` + id + `'`,
		`INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) SELECT 'one',$1,'operation_accounts','bulk-'||n FROM generate_series(1,1000) n`,
	} {
		if _, e = tx.Exec(f.ctx, q, f.domain); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	if out := f.callAccount(admin, "?pageSize=-1", nil, 422); out["error"] != "result_too_large" {
		t.Fatal(out)
	}
	// Put only a synthetic fixture at the bigint boundary; reenabling the guard
	// proves the application rejects overflow before SQL arithmetic or sealing.
	f.exec(`ALTER TABLE adtr.operation_accounts DISABLE TRIGGER operation_account_guard`)
	f.exec(`UPDATE adtr.operation_accounts SET revision=9223372036854775807 WHERE account_id=$1`, id)
	f.exec(`ALTER TABLE adtr.operation_accounts ENABLE TRIGGER operation_account_guard`)
	b := f.accountBody(id, "9223372036854775807", "overflow")
	b["label"] = "overflow"
	if out := f.callAccount(admin, "/update", f.proof(b), 409); out["error"] != "revision_exhausted" {
		t.Fatal(out)
	}
	if f.scalar("SELECT count(*) FROM adtr.operation_account_mutations") != 1 {
		t.Fatal("overflow retained receipt")
	}
}

func TestOperationAccountHTTPSecretSurfacesAndGrantEpoch(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin := f.client(f.admin)
	id := f.createAccount("secret")
	for _, path := range []string{"", "/domains", "/detail?accountId=" + id, "/mutation?idempotencyKey=secret"} {
		out := f.request(admin, path, nil)
		for _, forbidden := range []string{"Synthetic Secret", "registry-reader", "ciphertext", "keyId", "username", "password", "userInfo"} {
			if strings.Contains(out.Body.String(), forbidden) {
				t.Fatal("unsafe HTTP projection", path, forbidden)
			}
		}
	}
	old := f.version(f.operator)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='operation_accounts'", narrowResourceRole)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='operation_accounts'", narrowResourceRole)
	if f.version(f.operator) <= old {
		t.Fatal("revoke/regrant revived old authorization")
	}
	operator := f.client(f.operator)
	b := f.createBody("operator-owned")
	f.callAccount(operator, "/create", f.proof(b), 200)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='operation_accounts'", narrowResourceRole)
	f.callAccount(operator, "/mutation?idempotencyKey=operator-owned", nil, 403)
	f.now = f.now.Add(31 * 24 * time.Hour)
	// Renew only the session to isolate current tenant expiry from session expiry.
	operator = f.client(f.operator)
	admin = f.client(f.admin)
	f.callAccount(admin, "/mutation?idempotencyKey=secret", nil, 403)
	_ = operator
}

func TestOperationAccountTenantSameDomainAndActorKeysStayIsolated(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin, other := f.client(f.admin), f.client(f.other)
	id := f.createAccount("shared-key")
	foreignDomain := f.callDomain(other, "/create", f.proof(createDomainBody("foreign-parent")), 200)["domainId"].(string)
	f.seedGroup("two", "foreign-operation-scope", "platform_admin", foreignDomain)
	foreign := f.createBody("shared-key")
	foreign["domainId"] = foreignDomain
	foreignID := f.callAccount(other, "/create", f.proof(foreign), 200)["accountId"].(string)
	if id == foreignID {
		t.Fatal("reused account identity")
	}
	for _, test := range []struct {
		client       *resourceTestClient
		own, foreign string
	}{{admin, id, foreignID}, {other, foreignID, id}} {
		f.callAccount(test.client, "/detail?accountId="+test.foreign, nil, 404)
		f.callAccount(test.client, "/update", f.proof(f.accountBody(test.foreign, "1", "foreign-edit")), 404)
		out := f.callAccount(test.client, "?filterDomain=example.test&pageSize=1", nil, 200)
		if out["page"].(map[string]any)["total"] != float64(1) || out["List"].([]any)[0].(map[string]any)["accountId"] != test.own {
			t.Fatal("same DNS scope leak", out)
		}
		receipt := f.callAccount(test.client, "/mutation?idempotencyKey=shared-key", nil, 200)["receipt"].(map[string]any)
		if receipt["accountId"] != test.own {
			t.Fatal("tenant/actor idempotency collision", receipt)
		}
	}
}

func TestOperationAccountCreateAndDeleteAuditRollback(t *testing.T) {
	for _, kind := range []string{"create", "delete"} {
		t.Run(kind, func(t *testing.T) {
			f := newOperationAccountFixture(t)
			admin := f.client(f.admin)
			body := f.createBody("rollback")
			before := 0
			if kind == "delete" {
				id := f.createAccount("live")
				body = f.deleteBody(id, "1", "rollback")
				before = 1
			}
			f.exec(`CREATE FUNCTION adtr.synthetic_operation_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic rollback'; END; $$; CREATE TRIGGER synthetic_operation_fail BEFORE INSERT ON adtr.operation_account_audit FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_operation_fail();`)
			body = f.proof(body)
			epoch := f.version(f.admin)
			f.callAccount(admin, "/"+kind, body, 500)
			if f.scalar("SELECT count(*) FROM adtr.operation_accounts WHERE deleted_at IS NULL") != before || f.scalar("SELECT count(*) FROM adtr.operation_account_credentials") != before || f.scalar("SELECT count(*) FROM adtr.domain_dependencies WHERE module='operation_accounts'") != before || f.scalar("SELECT count(*) FROM adtr.operation_account_mutations") != before || f.version(f.admin) != epoch {
				t.Fatal("partial lifecycle survived audit rollback")
			}
			f.exec("DROP TRIGGER synthetic_operation_fail ON adtr.operation_account_audit")
			f.callAccount(admin, "/"+kind, body, 200)
		})
	}
}

func TestOperationAccountCreateVersusDomainDeletionSerializes(t *testing.T) {
	f := newOperationAccountFixture(t)
	admin, operator := f.client(f.admin), f.client(f.operator)
	create := f.proof(f.createBody("concurrent-account"))
	del := map[string]any{"domainId": f.domain, "expectedRevision": "1", "confirmDomain": "example.test", "actorPassword": create["actorPassword"], "totpCode": create["totpCode"]}
	start := make(chan struct{})
	codes := make(chan int, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); <-start; codes <- f.request(operator, "/create", create).Code }()
	go func() {
		defer group.Done()
		raw, _ := json.Marshal(del)
		r := httptest.NewRequest("POST", "/api/domains/delete", bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:5522"
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", admin.csrf)
		r.AddCookie(admin.cookie)
		w := httptest.NewRecorder()
		<-start
		f.s.DomainsHandler(f.store, f.engine).ServeHTTP(w, r)
		codes <- w.Code
	}()
	close(start)
	group.Wait()
	a, b := <-codes, <-codes
	if !((a == 200 && (b == 409 || b == 404)) || (b == 200 && (a == 409 || a == 404))) {
		t.Fatal("domain/account race", a, b)
	}
	if f.scalar(`SELECT count(*) FROM adtr.operation_accounts a JOIN adtr.domain_connections d USING(tenant_id,domain_id) JOIN adtr.resource_domains r ON r.tenant_id=d.tenant_id AND r.id=d.domain_id WHERE a.deleted_at IS NULL AND (d.deleted_at IS NOT NULL OR NOT r.active)`) != 0 {
		t.Fatal("orphan account after domain race")
	}
}

func TestOperationAccountDeferredConsistencyAndBoundSQLGuards(t *testing.T) {
	f := newOperationAccountFixture(t)
	id := f.createAccount("base")
	for _, kind := range []string{"missing_pair", "missing_pin", "revision_pair_mismatch"} {
		t.Run(kind, func(t *testing.T) {
			tx, e := f.conn.Begin(f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(f.ctx)
			if e = tasks.CheckSchemaTx(f.ctx, tx, schemaversion.Current); e != nil {
				t.Fatal(e)
			}
			if e = lockIdentityTenant(f.ctx, tx, "one"); e != nil {
				t.Fatal(e)
			}
			if kind == "revision_pair_mismatch" {
				_, e = tx.Exec(f.ctx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE tenant_id='one' AND domain_id=$1 AND account_id=$2`, f.domain, id)
			} else {
				account := "incomplete-" + kind
				_, e = tx.Exec(f.ctx, `INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id) VALUES('one',$1,$2)`, f.domain, account)
				if e == nil && kind == "missing_pair" {
					_, e = tx.Exec(f.ctx, `INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES('one',$1,'operation_accounts',$2)`, f.domain, account)
				}
				if e == nil && kind == "missing_pin" {
					_, e = tx.Exec(f.ctx, `INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) SELECT tenant_id,domain_id,$3,1,key_id,ciphertext FROM adtr.operation_account_credentials WHERE tenant_id='one' AND domain_id=$1 AND account_id=$2`, f.domain, id, account)
				}
			}
			if e != nil {
				t.Fatal("expected deferred check but statement failed", e)
			}
			if e = tx.Commit(f.ctx); e == nil {
				t.Fatal("inconsistent account committed", kind)
			}
			if f.scalar("SELECT count(*) FROM adtr.operation_accounts") != 1 || f.scalar("SELECT count(*) FROM adtr.operation_account_credentials") != 1 || f.scalar("SELECT revision FROM adtr.operation_accounts WHERE account_id=$1", id) != 1 {
				t.Fatal("failed commit left partial storage")
			}
		})
	}
	f.exec(`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES('one',$1,$2,'reserved.synthetic','bound')`, f.domain, id)
	for _, query := range []string{
		`UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE tenant_id='one' AND account_id=$1`,
		`UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1,deleted_at=clock_timestamp() WHERE tenant_id='one' AND account_id=$1`,
		`UPDATE adtr.operation_account_credentials SET ciphertext=decode(repeat('00',40),'hex') WHERE tenant_id='one' AND account_id=$1`,
	} {
		if _, e := f.conn.Exec(f.ctx, query, id); e == nil {
			t.Fatal("bound or same-generation direct SQL change accepted")
		}
	}
	if _, e := f.conn.Exec(f.ctx, `UPDATE adtr.domain_dependencies SET object_id='retargeted' WHERE tenant_id='one' AND module='operation_accounts' AND object_id=$1`, id); e == nil {
		t.Fatal("domain pin retargeted")
	}
}
