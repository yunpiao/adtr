//go:build integration

package auth_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const directoryV3HTTPPrefix = "/api/directory/v3"
const directoryV3HTTPUsePrefix = "/api/directory-credential-use/v3"

type directoryV3HTTPFixture struct {
	*directoryHTTPFixture
	environment map[string]string
	fallback    http.Handler
}

// The shared fixture runs store.Migrate/Ready, real password/TOTP proofs and
// authenticated HTTP against an isolated PostgreSQL database. Missing DSN is a
// hard failure. No version stamps, schema fragments or security replacements.
func newDirectoryV3HTTPFixture(t *testing.T) *directoryV3HTTPFixture {
	t.Helper()
	base := newDirectoryHTTPFixture(t)
	f := &directoryV3HTTPFixture{directoryHTTPFixture: base, fallback: base.handler}
	if store.SchemaVersion != schemaversion.Current || f.count(`SELECT version FROM adtr.schema_version WHERE singleton`) != schemaversion.Current {
		t.Fatal("v3 HTTP requires the exact current production schema migration")
	}
	if f.count(`SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgenabled='O' AND tgname=ANY($1::text[])`, []string{"domain_directory_use_guard", "domain_directory_use_consistency", "domain_directory_task_reservation", "domain_directory_observation_guard", "domain_directory_observations_immutable", "domain_directory_observations_no_truncate"}) != 6 {
		t.Fatal("v3 HTTP requires enabled production ledger and observation guards")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ca, policy := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "policy.json")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(policy, []byte(`{"version":1,"targets":[{"tenantId":"governance-http","domain":"one.invalid","serverNames":["dc.one.invalid"],"cidrs":["192.0.2.0/24"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.environment = map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic-directory-v3", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'d'}, 32)), "ADTR_LDAP_CA_FILE": ca, "ADTR_LDAP_EGRESS_POLICY_FILE": policy}
	f.installV3(f.runtimeWithGates(true, true), nil)
	return f
}

// Preserve identical key/trust/egress snapshots while changing only flags.
func (f *directoryV3HTTPFixture) runtimeWithGates(master, v3 bool) *domainconfig.Runtime {
	f.t.Helper()
	runtime, err := domainconfig.Load(func(key string) string {
		switch key {
		case "ADTR_DIRECTORY_READ_ENABLED":
			return strconv.FormatBool(master)
		case "ADTR_DIRECTORY_READ_V3_ENABLED":
			return strconv.FormatBool(v3)
		default:
			return f.environment[key]
		}
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return runtime
}

func (f *directoryV3HTTPFixture) installV3(runtime *domainconfig.Runtime, synthetic tasks.Executor) {
	f.t.Helper()
	ds := domains.New(runtime)
	kind := ds.DirectoryV3Kind()
	if synthetic != nil {
		// Only Execute is synthetic. Keep production admission, validation,
		// authorization, fencing and original-return OnQuiesced callbacks.
		kind.Execute = synthetic
	}
	engine, err := tasks.New(f.conn.Config(), tasks.ProductionRegistry(ds.Kind(), ds.AccountKind(), ds.DirectoryKind(), kind), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		f.t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(directoryV3HTTPPrefix+"/", f.service.DirectoryV3Handler(ds, engine))
	mux.Handle(directoryV3HTTPUsePrefix+"/", f.service.DirectoryV3CredentialUseHandler(credentialuse.New()))
	mux.Handle("/api/directory/", f.service.DirectoryHandler(ds, engine))
	mux.Handle("/api/directory-credential-use/", f.service.DirectoryCredentialUseHandler(credentialuse.New()))
	mux.Handle("/api/domains/", f.service.DomainsHandler(ds, engine))
	mux.Handle("/api/tasks", f.service.TasksHandler(engine))
	mux.Handle("/api/tasks/", f.service.TasksHandler(engine))
	mux.Handle("/", f.fallback)
	f.handler, f.store, f.engine, f.runtime = mux, ds, engine, runtime
}

func directoryV3HTTPGrantBody(role, revision, key string) map[string]any {
	body := directoryHTTPGrantBody(role, revision, key)
	body["purpose"] = credentialuse.DirectoryV3Purpose
	return body
}

// Mutation responses flatten the durable receipt and add the fixed consumer
// capability. Keep strict decoding; do not discard other unknown fields.
type directoryV3HTTPGrantReceipt struct {
	credentialuse.Receipt
	ConsumerEnabled bool `json:"consumerEnabled"`
}

func (f *directoryV3HTTPFixture) grantV3(actor, role, key string) string {
	f.t.Helper()
	out := operationalDecode[directoryV3HTTPGrantReceipt](f.t, f.request(actor, directoryV3HTTPUsePrefix+"/grant", f.proof(actor, directoryV3HTTPGrantBody(role, "0", key)), 200).Body.Bytes())
	if !out.ConsumerEnabled || !out.Allowed || out.Purpose != credentialuse.DirectoryV3Purpose || out.GrantRevision == "" || out.GrantRevision == "0" || out.Replayed {
		f.t.Fatal("v3 HTTP grant did not commit its exact purpose")
	}
	return out.GrantRevision
}

func (f *directoryV3HTTPFixture) submitV3(actor, key string) tasks.Task {
	f.t.Helper()
	w := f.request(actor, directoryV3HTTPPrefix+"/sync", f.proof(actor, directoryHTTPSyncBody(key)), 200)
	out := operationalDecode[tasks.Submission](f.t, w.Body.Bytes())
	if out.Task.ID == "" || out.Task.Kind != domains.DirectoryV3KindName || out.Task.State != tasks.Queued || out.Replayed {
		f.t.Fatal("v3 HTTP admission did not persist a new queued task")
	}
	directoryHTTPRedacted(f.t, w.Body.Bytes())
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND payload_version=1 AND payload->'dictionaryVersion'='3'::jsonb AND (SELECT count(*) FROM jsonb_object_keys(payload))=9`, out.Task.ID) != 1 {
		f.t.Fatal("v3 task did not retain its closed nine-field immutable payload")
	}
	return out.Task
}

func (f *directoryV3HTTPFixture) unavailableV3(pin string) {
	f.t.Helper()
	path := directoryV3HTTPPrefix + "/observation?domainId=domain-one"
	raw := f.request("directory-http-reader", path, nil, 200).Body.Bytes()
	if !bytes.Contains(raw, []byte(`"observedCounts":null`)) {
		f.t.Fatal("unavailable response omitted explicit null counts")
	}
	out := operationalDecode[domains.DirectoryV3List](f.t, raw)
	if out.DictionaryVersion != 3 || out.Available || out.List == nil || len(out.List) != 0 || out.Source != nil || out.ObservedCounts != nil || out.ObservationID != "" {
		f.t.Fatal("v3 unavailable response lost its version or disclosed staged rows")
	}
	if pin != "" {
		f.error(f.request("directory-http-reader", path+"&observationId="+pin, nil, 409), "directory_observation_unavailable")
	}
}

func TestDirectoryV3MigratedHTTPReadAndPurposeBoundaries(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	path := directoryV3HTTPPrefix + "/observation?domainId=domain-one"
	f.request("", path, nil, 401)
	for _, actor := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, path, nil, 403)
	}
	f.request("directory-http-no-scope", path, nil, 404)
	f.request("directory-admin", directoryV3HTTPPrefix+"/observation?domainId=domain-two", nil, 404)
	f.unavailableV3("")
	for _, query := range []string{"pageIdx=2", "pageSize=26", "kind=all", "domainId=domain-two", "actorId=1", "pageIdx=01", "dictionaryVersion=2", "purpose=domain.directory_read", "pageSize=25&pageSize=50"} {
		f.request("directory-admin", path+"&"+query, nil, 400)
	}
	effective := directoryV3HTTPUsePrefix + "/effective?accountId=" + governanceHTTPAccount
	for _, actor := range []string{"directory-admin", directoryHTTPWriterRole, "directory-http-reader"} {
		out := operationalDecode[credentialuse.Effective](t, f.request(actor, effective, nil, 200).Body.Bytes())
		if out.ExplicitlyGranted || out.Eligible || out.GrantRevision != "0" || out.Purpose != credentialuse.DirectoryV3Purpose {
			t.Fatal("v3 builtin administrator or ordinary rights invented a credential grant")
		}
	}
	for _, suffix := range []string{"/accounts", "/grants?accountId=" + governanceHTTPAccount, "/roles?accountId=" + governanceHTTPAccount, "/mutation?idempotencyKey=absent"} {
		f.request(directoryHTTPWriterRole, directoryV3HTTPUsePrefix+suffix, nil, 403)
	}
	f.request("directory-http-no-scope", effective, nil, 404)
	f.request("directory-http-no-assets", effective, nil, 403)
	f.request("directory-http-reader", directoryV3HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=absent", nil, 403)

	// The same introspection path used by the UI must agree with execution.
	checks := []string{"GET /api/directory/v3/observation", "GET /api/directory-credential-use/v3/effective", "GET /api/directory/v3/task", "POST /api/directory/v3/sync", "POST /api/directory-credential-use/v3/grant", "GET /api/directory/v3/v3/observation"}
	check := operationalDecode[struct {
		Results []bool `json:"results"`
	}](t, f.request("directory-http-reader", "/api/access/check", map[string]any{"paths": checks}, 200).Body.Bytes())
	if !reflect.DeepEqual(check.Results, []bool{true, true, false, false, false, false}) {
		t.Fatal("v3 introspection widened exact paths or permissions", check.Results)
	}

	body := f.proof("directory-grant", directoryV3HTTPGrantBody(directoryHTTPWriterRole, "0", "separate-grant-receipt"))
	for _, prefix := range []string{"/api/credential-use", "/api/directory-credential-use"} {
		f.error(f.request("directory-grant", prefix+"/grant", body, 422), "unsupported_credential_purpose")
	}
	legacy := f.proof("directory-grant", directoryHTTPGrantBody(directoryHTTPWriterRole, "0", "separate-grant-receipt"))
	f.error(f.request("directory-grant", directoryV3HTTPUsePrefix+"/grant", legacy, 422), "unsupported_credential_purpose")
	f.request(directoryHTTPWriterRole, directoryV3HTTPUsePrefix+"/grant", f.proof(directoryHTTPWriterRole, directoryV3HTTPGrantBody(directoryHTTPWriterRole, "0", "self-grant-denied")), 403)
	f.request(directoryHTTPWriterRole, directoryV3HTTPUsePrefix+"/revoke", f.proof(directoryHTTPWriterRole, directoryV3HTTPGrantBody(directoryHTTPWriterRole, "1", "self-revoke-denied")), 403)
	if f.count(`SELECT count(*) FROM adtr.operation_account_use_grants`) != 0 || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != -1 {
		t.Fatal("wrong-purpose or non-admin request changed grants or consumed proof")
	}
	f.request("directory-grant", "/api/directory-credential-use/grant", legacy, 200)
	f.request("directory-grant", directoryV3HTTPUsePrefix+"/mutation?idempotencyKey=separate-grant-receipt", nil, 404)
	f.bind()
	f.grant("directory-grant-writer", "platform_admin")
	for _, actor := range []string{directoryHTTPWriterRole, "directory-admin"} {
		out := operationalDecode[credentialuse.Effective](t, f.request(actor, effective, nil, 200).Body.Bytes())
		if out.ExplicitlyGranted || out.Eligible {
			t.Fatal("connection-test/v1 allow silently authorized v3")
		}
		f.error(f.request(actor, directoryV3HTTPPrefix+"/sync", f.proof(actor, directoryHTTPSyncBody("v1-cannot-authorize-v3")), 403), "forbidden")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks`) != 0 {
		t.Fatal("denied v3 admission left a task")
	}
	body = f.proof("directory-grant", directoryV3HTTPGrantBody(directoryHTTPWriterRole, "0", "separate-grant-receipt"))
	beforeConflict := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id)
	f.error(f.request("directory-grant", directoryV3HTTPUsePrefix+"/grant", body, 409), "idempotency_conflict")
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != beforeConflict || f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v3'`) != 0 {
		t.Fatal("cross-purpose actor/key reuse changed v3 authority or consumed proof")
	}
	body = f.proof("directory-grant", directoryV3HTTPGrantBody(directoryHTTPWriterRole, "0", "separate-v3-grant-receipt"))
	granted := operationalDecode[directoryV3HTTPGrantReceipt](t, f.request("directory-grant", directoryV3HTTPUsePrefix+"/grant", body, 200).Body.Bytes())
	step := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id)
	replay := operationalDecode[directoryV3HTTPGrantReceipt](t, f.request("directory-grant", directoryV3HTTPUsePrefix+"/grant", body, 200).Body.Bytes())
	if !granted.ConsumerEnabled || !replay.ConsumerEnabled || granted.Purpose != credentialuse.DirectoryV3Purpose || !replay.Replayed || granted.GrantRevision != replay.GrantRevision || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != step {
		t.Fatal("v3 committed grant replay changed purpose/revision or consumed proof twice")
	}
	for _, version := range []struct{ prefix, purpose, key string }{{"/api/directory-credential-use", credentialuse.DirectoryPurpose, "separate-grant-receipt"}, {directoryV3HTTPUsePrefix, credentialuse.DirectoryV3Purpose, "separate-v3-grant-receipt"}} {
		receipt := operationalDecode[struct {
			Receipt         credentialuse.Receipt `json:"receipt"`
			ConsumerEnabled bool                  `json:"consumerEnabled"`
		}](t, f.request("directory-grant", version.prefix+"/mutation?idempotencyKey="+version.key, nil, 200).Body.Bytes())
		if receipt.Receipt.Purpose != version.purpose || !receipt.ConsumerEnabled {
			t.Fatal("grant receipts crossed the trusted purpose")
		}
	}
	f.request("directory-admin-other", directoryV3HTTPUsePrefix+"/mutation?idempotencyKey=separate-v3-grant-receipt", nil, 404)
	f.request("directory-grant", "/api/credential-use/mutation?idempotencyKey=separate-v3-grant-receipt", nil, 404)
	allowed := operationalDecode[credentialuse.Effective](t, f.request(directoryHTTPWriterRole, effective, nil, 200).Body.Bytes())
	if !allowed.ExplicitlyGranted || !allowed.Eligible || allowed.Purpose != credentialuse.DirectoryV3Purpose {
		t.Fatal("qualified writer could not inspect its explicit v3 grant")
	}
	grants := operationalDecode[credentialuse.GrantList](t, f.request("directory-admin", directoryV3HTTPUsePrefix+"/grants?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
	if len(grants.Grants) != 1 || grants.Grants[0].Purpose != credentialuse.DirectoryV3Purpose || grants.Grants[0].RoleID != directoryHTTPWriterRole {
		t.Fatal("v3 grant listing mixed profiles")
	}
	roles := operationalDecode[credentialuse.RoleList](t, f.request("directory-admin", directoryV3HTTPUsePrefix+"/roles?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
	found := false
	for _, role := range roles.Roles {
		found = found || role.RoleID == directoryHTTPWriterRole
	}
	if !found {
		t.Fatal("v3 grant catalogue omitted a qualified minimal writer")
	}
	accounts := operationalDecode[credentialuse.AccountList](t, f.request("directory-admin", directoryV3HTTPUsePrefix+"/accounts", nil, 200).Body.Bytes())
	if len(accounts.List) != 1 || accounts.List[0].AccountID != governanceHTTPAccount || !accounts.ConsumerEnabled {
		t.Fatal("v3 account catalogue lost its exact-purpose grant")
	}
}

func TestDirectoryV3MigratedHTTPProofReceiptsAndGenericControls(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	f.bind()
	f.grant("directory-grant", "platform_admin")
	f.grantV3("directory-grant-writer", "platform_admin", "v3-admin-grant")
	body := f.proof("directory-admin", directoryHTTPSyncBody("protected-v3-intent"))
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	f.raw("", http.MethodPost, directoryV3HTTPPrefix+"/sync", raw, 401, nil)
	for _, edit := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("Origin") },
		func(r *http.Request) { r.Header.Set("Origin", "https://other.invalid") },
		func(r *http.Request) { r.Header.Add("Origin", governanceHTTPOrigin) },
		func(r *http.Request) { r.Header.Del("X-CSRF-Token") },
		func(r *http.Request) { r.Header.Set("X-CSRF-Token", f.clients["directory-admin-other"].csrf) },
	} {
		f.raw("directory-admin", http.MethodPost, directoryV3HTTPPrefix+"/sync", raw, 403, edit)
	}
	for _, field := range []string{`,"dictionaryVersion":2}`, `,"purpose":"domain.directory_read"}`, `,"attributes":["unicodePwd"]}`, `,"domainId":"domain-two"}`} {
		malformed := append(bytes.Clone(raw[:len(raw)-1]), []byte(field)...)
		f.raw("directory-admin", http.MethodPost, directoryV3HTTPPrefix+"/sync", malformed, 400, nil)
	}
	f.raw("directory-admin", http.MethodPost, directoryV3HTTPPrefix+"/sync?domainId=domain-one", raw, 400, nil)
	f.raw("directory-admin", http.MethodPost, directoryV3HTTPPrefix+"/sync", raw, 400, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") })
	f.request("directory-no-mfa", directoryV3HTTPPrefix+"/sync", body, 403)
	for _, actor := range []string{"directory-http-reader", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, directoryV3HTTPPrefix+"/sync", f.proof(actor, directoryHTTPSyncBody("no-rights")), 403)
	}
	body["actorPassword"] = "incorrect synthetic password"
	f.error(f.request("directory-admin", directoryV3HTTPPrefix+"/sync", body, 401), "invalid_credentials")
	stale := directoryHTTPSyncBody("stale-v3-pins")
	stale["expectedCredentialGeneration"] = "1"
	f.error(f.request("directory-admin", directoryV3HTTPPrefix+"/sync", f.proof("directory-admin", stale), 409), "revision_conflict")
	if f.count(`SELECT count(*) FROM adtr.tasks`) != 0 || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id) != -1 {
		t.Fatal("rejected v3 proof/pins admitted work or consumed proof")
	}
	v1 := f.submit("directory-submit-one", "shared-profile-intent")
	f.request("directory-submit-one", directoryV3HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 404)
	v3 := f.submitV3("directory-submit-one", "shared-profile-intent")
	if v1.ID == v3.ID {
		t.Fatal("v1/v3 idempotency keys collapsed into one task")
	}
	for _, version := range []struct{ prefix, id string }{{"/api/directory", v1.ID}, {directoryV3HTTPPrefix, v3.ID}} {
		receipt := operationalDecode[struct {
			Task tasks.Task `json:"task"`
		}](t, f.request("directory-submit-one", version.prefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 200).Body.Bytes())
		if receipt.Task.ID != version.id {
			t.Fatal("same-key task receipt crossed profiles")
		}
	}
	for _, mismatch := range []struct{ prefix, id string }{{"/api/directory", v3.ID}, {directoryV3HTTPPrefix, v1.ID}} {
		f.request("directory-admin", mismatch.prefix+"/task?taskUUID="+mismatch.id, nil, 404)
		f.request("directory-cancel", mismatch.prefix+"/cancel", f.proof("directory-cancel", map[string]any{"taskUUID": mismatch.id}), 404)
	}
	for _, actor := range []string{"directory-http-no-scope", "directory-foreign"} {
		f.request(actor, directoryV3HTTPPrefix+"/task?taskUUID="+v3.ID, nil, 404)
		f.request(actor, directoryV3HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 404)
	}
	f.request("directory-admin-other", directoryV3HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 404)
	f.request("directory-submit-one", directoryV3HTTPPrefix+"/receipt?domainId=domain-two&idempotencyKey=shared-profile-intent", nil, 404)
	f.request("directory-http-reader", directoryV3HTTPPrefix+"/task?taskUUID="+v3.ID, nil, 403)
	f.request("directory-http-reader", directoryV3HTTPPrefix+"/cancel", f.proof("directory-http-reader", map[string]any{"taskUUID": v3.ID}), 403)
	for _, path := range []string{"/api/tasks/cancel", "/api/tasks/recover"} {
		control := map[string]any{"taskUUID": v3.ID}
		if path == "/api/tasks/recover" {
			control["idempotencyKey"] = "no-generic-v3-recovery"
		}
		f.error(f.request("directory-cancel", path, f.proof("directory-cancel", control), 400), "directory_route_required")
	}
	f.error(f.request("directory-cancel", "/api/tasks/submit", f.proof("directory-cancel", map[string]any{"taskName": domains.DirectoryV3KindName, "domainId": "domain-one", "payloadVersion": 1, "payload": map[string]any{}, "idempotencyKey": "no-generic-v3-submit"}), 400), "directory_route_required")
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state='queued'`, v3.ID) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND state='reserved'`, v3.ID) != 1 {
		t.Fatal("generic or cross-profile controls changed the v3 lifecycle")
	}
	// A valid replay needs fresh proof; the previous committed proof is denied.
	proof := f.proof("directory-submit-two", directoryHTTPSyncBody("v3-replay"))
	first := operationalDecode[tasks.Submission](t, f.request("directory-submit-two", directoryV3HTTPPrefix+"/sync", proof, 200).Body.Bytes())
	f.error(f.request("directory-submit-two", directoryV3HTTPPrefix+"/sync", proof, 401), "invalid_credentials")
	replay := operationalDecode[tasks.Submission](t, f.request("directory-submit-two", directoryV3HTTPPrefix+"/sync", f.proof("directory-submit-two", directoryHTTPSyncBody("v3-replay")), 200).Body.Bytes())
	if !replay.Replayed || replay.Task.ID != first.Task.ID || f.count(`SELECT count(*) FROM adtr.tasks`) != 3 {
		t.Fatal("v3 replay duplicated admitted work")
	}
	f.request("directory-submit-two", "/api/directory/receipt?domainId=domain-one&idempotencyKey=v3-replay", nil, 404)
	result := operationalDecode[struct {
		Task tasks.Task `json:"task"`
	}](t, f.request("directory-cancel", directoryV3HTTPPrefix+"/cancel", f.proof("directory-cancel", map[string]any{"taskUUID": v3.ID}), 200).Body.Bytes())
	if result.Task.State != tasks.Cancelled || f.count(`SELECT count(*) FROM adtr.domain_audit WHERE task_id=$1 AND action='domain_directory_v3_cancel'`, v3.ID) != 1 {
		t.Fatal("dedicated v3 cancel failed to commit its outcome/audit")
	}
}

func TestDirectoryV3HTTPObservedCountsPinnedFullSnapshotAndReadOnlyAuthority(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	f.bind()
	grant := f.grantV3("directory-grant", "platform_admin", "counts-grant")
	path := directoryV3HTTPPrefix + "/observation?domainId=domain-one&pageSize=25"
	f.unavailableV3("")
	observation := directoryV3HTTPObservation(30)
	defer observation.Discard()
	observation.Objects = append(observation.Objects,
		directoryassets.StoredObjectV3{Base: directoryassets.Object{GUID: "10000000-0000-0000-0000-000000000001", DN: "CN=Group,DC=one,DC=invalid", Kind: directoryassets.Group, Classes: []string{"group", "top"}}},
		directoryassets.StoredObjectV3{Base: directoryassets.Object{GUID: "20000000-0000-0000-0000-000000000001", DN: "CN=Computer,DC=one,DC=invalid", Kind: directoryassets.Computer, Classes: []string{"computer", "top", "user"}}})
	first := f.submitV3("directory-submit-one", "counts-mixed")
	f.finishV3(f.stageV3(observation), first, tasks.Succeeded)
	want := domains.DirectoryV3ObservedCounts{User: 30, Group: 1, Computer: 1}
	for _, tc := range []struct {
		query       string
		total, rows int
	}{
		{"", 32, 25}, {"&pageIdx=2", 32, 7}, {"&kind=user", 30, 25}, {"&kind=user&pageIdx=2", 30, 5}, {"&kind=group", 1, 1}, {"&kind=computer", 1, 1}, {"&pageIdx=10000", 32, 0},
	} {
		out := operationalDecode[domains.DirectoryV3List](t, f.request("directory-http-reader", path+"&observationId="+first.ID+tc.query, nil, 200).Body.Bytes())
		if out.ObservedCounts == nil || *out.ObservedCounts != want || out.Page.Total != tc.total || len(out.List) != tc.rows || out.ObservationID != first.ID || !reflect.DeepEqual(*out.Source, observation.Source) {
			t.Fatal("counts did not follow complete pinned source", tc.query, out)
		}
	}
	newer := f.submitV3("directory-submit-two", "counts-empty")
	f.finishV3(f.stageV3(directoryV3HTTPObservation(0)), newer, tasks.Succeeded)
	latest := operationalDecode[domains.DirectoryV3List](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if !latest.Available || latest.ObservationID != newer.ID || latest.ObservedCounts == nil || *latest.ObservedCounts != (domains.DirectoryV3ObservedCounts{}) || latest.Page.Total != 0 {
		t.Fatal("new empty observation retained old counts", latest)
	}
	f.installV3(f.runtimeWithGates(false, false), nil)
	f.request("directory-revoke", directoryV3HTTPUsePrefix+"/revoke", f.proof("directory-revoke", directoryV3HTTPGrantBody("platform_admin", grant, "counts-revoke-producer")), 200)
	tasksBefore, usesBefore := f.count(`SELECT count(*) FROM adtr.tasks`), f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses`)
	pinned := operationalDecode[domains.DirectoryV3List](t, f.request("directory-http-reader", path+"&observationId="+first.ID+"&pageIdx=2", nil, 200).Body.Bytes())
	if pinned.ObservedCounts == nil || *pinned.ObservedCounts != want || pinned.ObservationID != first.ID {
		t.Fatal("producer grant/gates changed stored count authority", pinned)
	}
	for _, actor := range []string{"directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, path, nil, 403)
	}
	f.request("directory-http-no-scope", path, nil, 404)
	f.request("directory-http-no-scope", path+"&observationId="+first.ID, nil, 404)
	// This actor is entitled to its own tenant's identically named domain,
	// which has no observation. Neither unpinned nor pinned reads may cross tenants.
	foreignRaw := f.request("directory-foreign", path, nil, 200).Body.Bytes()
	foreign := operationalDecode[domains.DirectoryV3List](t, foreignRaw)
	if foreign.DictionaryVersion != 3 || foreign.Available || foreign.ObservedCounts != nil || foreign.ObservationID != "" || foreign.Source != nil || foreign.List == nil || len(foreign.List) != 0 || foreign.Page.Total != 0 || !bytes.Contains(foreignRaw, []byte(`"observedCounts":null`)) {
		t.Fatal("same domain ID exposed another tenant's observation or counts", foreign)
	}
	f.error(f.request("directory-foreign", path+"&observationId="+first.ID, nil, 409), "directory_observation_unavailable")
	if f.count(`SELECT count(*) FROM adtr.tasks`) != tasksBefore || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses`) != usesBefore {
		t.Fatal("count reads mutated producer ledger")
	}
	f.exec(`DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, governanceHTTPTenant, directoryHTTPReaderRole)
	f.request("directory-http-reader", path+"&observationId="+first.ID, nil, 404)
}
