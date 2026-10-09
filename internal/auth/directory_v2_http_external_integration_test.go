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
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const directoryV2HTTPPrefix = "/api/directory/v2"
const directoryV2HTTPUsePrefix = "/api/directory-credential-use/v2"

type directoryV2HTTPFixture struct {
	*directoryHTTPFixture
	environment map[string]string
	fallback    http.Handler
}

// The shared fixture runs store.Migrate/Ready, real password/TOTP proofs and
// authenticated HTTP against an isolated PostgreSQL database. Missing DSN is a
// hard failure. No version stamps, schema fragments or security replacements.
func newDirectoryV2HTTPFixture(t *testing.T) *directoryV2HTTPFixture {
	t.Helper()
	base := newDirectoryHTTPFixture(t)
	f := &directoryV2HTTPFixture{directoryHTTPFixture: base, fallback: base.handler}
	if store.SchemaVersion != 16 || f.count(`SELECT version FROM adtr.schema_version WHERE singleton`) != 16 {
		t.Fatal("v2 HTTP requires the actual production schema 16 migration")
	}
	if f.count(`SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgenabled='O' AND tgname=ANY($1::text[])`, []string{"domain_directory_use_guard", "domain_directory_use_consistency", "domain_directory_task_reservation", "domain_directory_observation_guard", "domain_directory_observations_immutable", "domain_directory_observations_no_truncate"}) != 6 {
		t.Fatal("v2 HTTP requires enabled production ledger and observation guards")
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
	f.environment = map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic-directory-v2", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'d'}, 32)), "ADTR_LDAP_CA_FILE": ca, "ADTR_LDAP_EGRESS_POLICY_FILE": policy}
	f.installV2(f.runtimeWithGates(true, true), nil)
	return f
}

// Preserve identical key/trust/egress snapshots while changing only flags.
func (f *directoryV2HTTPFixture) runtimeWithGates(master, v2 bool) *domainconfig.Runtime {
	f.t.Helper()
	runtime, err := domainconfig.Load(func(key string) string {
		switch key {
		case "ADTR_DIRECTORY_READ_ENABLED":
			return strconv.FormatBool(master)
		case "ADTR_DIRECTORY_READ_V2_ENABLED":
			return strconv.FormatBool(v2)
		default:
			return f.environment[key]
		}
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return runtime
}

func (f *directoryV2HTTPFixture) installV2(runtime *domainconfig.Runtime, synthetic tasks.Executor) {
	f.t.Helper()
	ds := domains.New(runtime)
	kind := ds.DirectoryV2Kind()
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
	mux.Handle(directoryV2HTTPPrefix+"/", f.service.DirectoryV2Handler(ds, engine))
	mux.Handle(directoryV2HTTPUsePrefix+"/", f.service.DirectoryV2CredentialUseHandler(credentialuse.New()))
	mux.Handle("/api/directory/", f.service.DirectoryHandler(ds, engine))
	mux.Handle("/api/directory-credential-use/", f.service.DirectoryCredentialUseHandler(credentialuse.New()))
	mux.Handle("/api/domains/", f.service.DomainsHandler(ds, engine))
	mux.Handle("/api/tasks", f.service.TasksHandler(engine))
	mux.Handle("/api/tasks/", f.service.TasksHandler(engine))
	mux.Handle("/", f.fallback)
	f.handler, f.store, f.engine, f.runtime = mux, ds, engine, runtime
}

func directoryV2HTTPGrantBody(role, revision, key string) map[string]any {
	body := directoryHTTPGrantBody(role, revision, key)
	body["purpose"] = credentialuse.DirectoryV2Purpose
	return body
}

// Mutation responses flatten the durable receipt and add the fixed consumer
// capability. Keep strict decoding; do not discard other unknown fields.
type directoryV2HTTPGrantReceipt struct {
	credentialuse.Receipt
	ConsumerEnabled bool `json:"consumerEnabled"`
}

func (f *directoryV2HTTPFixture) grantV2(actor, role, key string) string {
	f.t.Helper()
	out := operationalDecode[directoryV2HTTPGrantReceipt](f.t, f.request(actor, directoryV2HTTPUsePrefix+"/grant", f.proof(actor, directoryV2HTTPGrantBody(role, "0", key)), 200).Body.Bytes())
	if !out.ConsumerEnabled || !out.Allowed || out.Purpose != credentialuse.DirectoryV2Purpose || out.GrantRevision == "" || out.GrantRevision == "0" || out.Replayed {
		f.t.Fatal("v2 HTTP grant did not commit its exact purpose")
	}
	return out.GrantRevision
}

func (f *directoryV2HTTPFixture) submitV2(actor, key string) tasks.Task {
	f.t.Helper()
	w := f.request(actor, directoryV2HTTPPrefix+"/sync", f.proof(actor, directoryHTTPSyncBody(key)), 200)
	out := operationalDecode[tasks.Submission](f.t, w.Body.Bytes())
	if out.Task.ID == "" || out.Task.Kind != domains.DirectoryV2KindName || out.Task.State != tasks.Queued || out.Replayed {
		f.t.Fatal("v2 HTTP admission did not persist a new queued task")
	}
	directoryHTTPRedacted(f.t, w.Body.Bytes())
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND payload_version=1 AND payload->'dictionaryVersion'='2'::jsonb AND (SELECT count(*) FROM jsonb_object_keys(payload))=9`, out.Task.ID) != 1 {
		f.t.Fatal("v2 task did not retain its closed nine-field immutable payload")
	}
	return out.Task
}

func (f *directoryV2HTTPFixture) unavailableV2(pin string) {
	f.t.Helper()
	path := directoryV2HTTPPrefix + "/observation?domainId=domain-one"
	out := operationalDecode[domains.DirectoryV2List](f.t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if out.DictionaryVersion != 2 || out.Available || out.List == nil || len(out.List) != 0 || out.Source != nil || out.ObservationID != "" {
		f.t.Fatal("v2 unavailable response lost its version or disclosed staged rows")
	}
	if pin != "" {
		f.error(f.request("directory-http-reader", path+"&observationId="+pin, nil, 409), "directory_observation_unavailable")
	}
}

func TestDirectoryV2MigratedHTTPReadAndPurposeBoundaries(t *testing.T) {
	f := newDirectoryV2HTTPFixture(t)
	path := directoryV2HTTPPrefix + "/observation?domainId=domain-one"
	f.request("", path, nil, 401)
	for _, actor := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, path, nil, 403)
	}
	f.request("directory-http-no-scope", path, nil, 404)
	f.request("directory-admin", directoryV2HTTPPrefix+"/observation?domainId=domain-two", nil, 404)
	f.unavailableV2("")
	for _, query := range []string{"pageIdx=2", "pageSize=26", "kind=all", "domainId=domain-two", "actorId=1", "pageIdx=01", "dictionaryVersion=2", "purpose=domain.directory_read", "pageSize=25&pageSize=50"} {
		f.request("directory-admin", path+"&"+query, nil, 400)
	}
	effective := directoryV2HTTPUsePrefix + "/effective?accountId=" + governanceHTTPAccount
	for _, actor := range []string{"directory-admin", directoryHTTPWriterRole, "directory-http-reader"} {
		out := operationalDecode[credentialuse.Effective](t, f.request(actor, effective, nil, 200).Body.Bytes())
		if out.ExplicitlyGranted || out.Eligible || out.GrantRevision != "0" || out.Purpose != credentialuse.DirectoryV2Purpose {
			t.Fatal("v2 builtin administrator or ordinary rights invented a credential grant")
		}
	}
	for _, suffix := range []string{"/accounts", "/grants?accountId=" + governanceHTTPAccount, "/roles?accountId=" + governanceHTTPAccount, "/mutation?idempotencyKey=absent"} {
		f.request(directoryHTTPWriterRole, directoryV2HTTPUsePrefix+suffix, nil, 403)
	}
	f.request("directory-http-no-scope", effective, nil, 404)
	f.request("directory-http-no-assets", effective, nil, 403)
	f.request("directory-http-reader", directoryV2HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=absent", nil, 403)

	// The same introspection path used by the UI must agree with execution.
	checks := []string{"GET /api/directory/v2/observation", "GET /api/directory-credential-use/v2/effective", "GET /api/directory/v2/task", "POST /api/directory/v2/sync", "POST /api/directory-credential-use/v2/grant", "GET /api/directory/v2/v2/observation"}
	check := operationalDecode[struct {
		Results []bool `json:"results"`
	}](t, f.request("directory-http-reader", "/api/access/check", map[string]any{"paths": checks}, 200).Body.Bytes())
	if !reflect.DeepEqual(check.Results, []bool{true, true, false, false, false, false}) {
		t.Fatal("v2 introspection widened exact paths or permissions", check.Results)
	}

	body := f.proof("directory-grant", directoryV2HTTPGrantBody(directoryHTTPWriterRole, "0", "separate-grant-receipt"))
	for _, prefix := range []string{"/api/credential-use", "/api/directory-credential-use"} {
		f.error(f.request("directory-grant", prefix+"/grant", body, 422), "unsupported_credential_purpose")
	}
	legacy := f.proof("directory-grant", directoryHTTPGrantBody(directoryHTTPWriterRole, "0", "separate-grant-receipt"))
	f.error(f.request("directory-grant", directoryV2HTTPUsePrefix+"/grant", legacy, 422), "unsupported_credential_purpose")
	f.request(directoryHTTPWriterRole, directoryV2HTTPUsePrefix+"/grant", f.proof(directoryHTTPWriterRole, directoryV2HTTPGrantBody(directoryHTTPWriterRole, "0", "self-grant-denied")), 403)
	f.request(directoryHTTPWriterRole, directoryV2HTTPUsePrefix+"/revoke", f.proof(directoryHTTPWriterRole, directoryV2HTTPGrantBody(directoryHTTPWriterRole, "1", "self-revoke-denied")), 403)
	if f.count(`SELECT count(*) FROM adtr.operation_account_use_grants`) != 0 || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != -1 {
		t.Fatal("wrong-purpose or non-admin request changed grants or consumed proof")
	}
	f.request("directory-grant", "/api/directory-credential-use/grant", legacy, 200)
	f.request("directory-grant", directoryV2HTTPUsePrefix+"/mutation?idempotencyKey=separate-grant-receipt", nil, 404)
	f.bind()
	f.grant("directory-grant-writer", "platform_admin")
	for _, actor := range []string{directoryHTTPWriterRole, "directory-admin"} {
		out := operationalDecode[credentialuse.Effective](t, f.request(actor, effective, nil, 200).Body.Bytes())
		if out.ExplicitlyGranted || out.Eligible {
			t.Fatal("connection-test/v1 allow silently authorized v2")
		}
		f.error(f.request(actor, directoryV2HTTPPrefix+"/sync", f.proof(actor, directoryHTTPSyncBody("v1-cannot-authorize-v2")), 403), "forbidden")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks`) != 0 {
		t.Fatal("denied v2 admission left a task")
	}
	body = f.proof("directory-grant", directoryV2HTTPGrantBody(directoryHTTPWriterRole, "0", "separate-grant-receipt"))
	beforeConflict := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id)
	f.error(f.request("directory-grant", directoryV2HTTPUsePrefix+"/grant", body, 409), "idempotency_conflict")
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != beforeConflict || f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v2'`) != 0 {
		t.Fatal("cross-purpose actor/key reuse changed v2 authority or consumed proof")
	}
	body = f.proof("directory-grant", directoryV2HTTPGrantBody(directoryHTTPWriterRole, "0", "separate-v2-grant-receipt"))
	granted := operationalDecode[directoryV2HTTPGrantReceipt](t, f.request("directory-grant", directoryV2HTTPUsePrefix+"/grant", body, 200).Body.Bytes())
	step := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id)
	replay := operationalDecode[directoryV2HTTPGrantReceipt](t, f.request("directory-grant", directoryV2HTTPUsePrefix+"/grant", body, 200).Body.Bytes())
	if !granted.ConsumerEnabled || !replay.ConsumerEnabled || granted.Purpose != credentialuse.DirectoryV2Purpose || !replay.Replayed || granted.GrantRevision != replay.GrantRevision || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-grant"].id) != step {
		t.Fatal("v2 committed grant replay changed purpose/revision or consumed proof twice")
	}
	for _, version := range []struct{ prefix, purpose, key string }{{"/api/directory-credential-use", credentialuse.DirectoryPurpose, "separate-grant-receipt"}, {directoryV2HTTPUsePrefix, credentialuse.DirectoryV2Purpose, "separate-v2-grant-receipt"}} {
		receipt := operationalDecode[struct {
			Receipt         credentialuse.Receipt `json:"receipt"`
			ConsumerEnabled bool                  `json:"consumerEnabled"`
		}](t, f.request("directory-grant", version.prefix+"/mutation?idempotencyKey="+version.key, nil, 200).Body.Bytes())
		if receipt.Receipt.Purpose != version.purpose || !receipt.ConsumerEnabled {
			t.Fatal("grant receipts crossed the trusted purpose")
		}
	}
	f.request("directory-admin-other", directoryV2HTTPUsePrefix+"/mutation?idempotencyKey=separate-v2-grant-receipt", nil, 404)
	f.request("directory-grant", "/api/credential-use/mutation?idempotencyKey=separate-v2-grant-receipt", nil, 404)
	allowed := operationalDecode[credentialuse.Effective](t, f.request(directoryHTTPWriterRole, effective, nil, 200).Body.Bytes())
	if !allowed.ExplicitlyGranted || !allowed.Eligible || allowed.Purpose != credentialuse.DirectoryV2Purpose {
		t.Fatal("qualified writer could not inspect its explicit v2 grant")
	}
	grants := operationalDecode[credentialuse.GrantList](t, f.request("directory-admin", directoryV2HTTPUsePrefix+"/grants?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
	if len(grants.Grants) != 1 || grants.Grants[0].Purpose != credentialuse.DirectoryV2Purpose || grants.Grants[0].RoleID != directoryHTTPWriterRole {
		t.Fatal("v2 grant listing mixed profiles")
	}
	roles := operationalDecode[credentialuse.RoleList](t, f.request("directory-admin", directoryV2HTTPUsePrefix+"/roles?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
	found := false
	for _, role := range roles.Roles {
		found = found || role.RoleID == directoryHTTPWriterRole
	}
	if !found {
		t.Fatal("v2 grant catalogue omitted a qualified minimal writer")
	}
	accounts := operationalDecode[credentialuse.AccountList](t, f.request("directory-admin", directoryV2HTTPUsePrefix+"/accounts", nil, 200).Body.Bytes())
	if len(accounts.List) != 1 || accounts.List[0].AccountID != governanceHTTPAccount || !accounts.ConsumerEnabled {
		t.Fatal("v2 account catalogue lost its exact-purpose grant")
	}
}

func TestDirectoryV2MigratedHTTPProofReceiptsAndGenericControls(t *testing.T) {
	f := newDirectoryV2HTTPFixture(t)
	f.bind()
	f.grant("directory-grant", "platform_admin")
	f.grantV2("directory-grant-writer", "platform_admin", "v2-admin-grant")
	body := f.proof("directory-admin", directoryHTTPSyncBody("protected-v2-intent"))
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	f.raw("", http.MethodPost, directoryV2HTTPPrefix+"/sync", raw, 401, nil)
	for _, edit := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("Origin") },
		func(r *http.Request) { r.Header.Set("Origin", "https://other.invalid") },
		func(r *http.Request) { r.Header.Add("Origin", governanceHTTPOrigin) },
		func(r *http.Request) { r.Header.Del("X-CSRF-Token") },
		func(r *http.Request) { r.Header.Set("X-CSRF-Token", f.clients["directory-admin-other"].csrf) },
	} {
		f.raw("directory-admin", http.MethodPost, directoryV2HTTPPrefix+"/sync", raw, 403, edit)
	}
	for _, field := range []string{`,"dictionaryVersion":2}`, `,"purpose":"domain.directory_read"}`, `,"attributes":["unicodePwd"]}`, `,"domainId":"domain-two"}`} {
		malformed := append(bytes.Clone(raw[:len(raw)-1]), []byte(field)...)
		f.raw("directory-admin", http.MethodPost, directoryV2HTTPPrefix+"/sync", malformed, 400, nil)
	}
	f.raw("directory-admin", http.MethodPost, directoryV2HTTPPrefix+"/sync?domainId=domain-one", raw, 400, nil)
	f.raw("directory-admin", http.MethodPost, directoryV2HTTPPrefix+"/sync", raw, 400, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") })
	f.request("directory-no-mfa", directoryV2HTTPPrefix+"/sync", body, 403)
	for _, actor := range []string{"directory-http-reader", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, directoryV2HTTPPrefix+"/sync", f.proof(actor, directoryHTTPSyncBody("no-rights")), 403)
	}
	body["actorPassword"] = "incorrect synthetic password"
	f.error(f.request("directory-admin", directoryV2HTTPPrefix+"/sync", body, 401), "invalid_credentials")
	stale := directoryHTTPSyncBody("stale-v2-pins")
	stale["expectedCredentialGeneration"] = "1"
	f.error(f.request("directory-admin", directoryV2HTTPPrefix+"/sync", f.proof("directory-admin", stale), 409), "revision_conflict")
	if f.count(`SELECT count(*) FROM adtr.tasks`) != 0 || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id) != -1 {
		t.Fatal("rejected v2 proof/pins admitted work or consumed proof")
	}
	v1 := f.submit("directory-submit-one", "shared-profile-intent")
	f.request("directory-submit-one", directoryV2HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 404)
	v2 := f.submitV2("directory-submit-one", "shared-profile-intent")
	if v1.ID == v2.ID {
		t.Fatal("v1/v2 idempotency keys collapsed into one task")
	}
	for _, version := range []struct{ prefix, id string }{{"/api/directory", v1.ID}, {directoryV2HTTPPrefix, v2.ID}} {
		receipt := operationalDecode[struct {
			Task tasks.Task `json:"task"`
		}](t, f.request("directory-submit-one", version.prefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 200).Body.Bytes())
		if receipt.Task.ID != version.id {
			t.Fatal("same-key task receipt crossed profiles")
		}
	}
	for _, mismatch := range []struct{ prefix, id string }{{"/api/directory", v2.ID}, {directoryV2HTTPPrefix, v1.ID}} {
		f.request("directory-admin", mismatch.prefix+"/task?taskUUID="+mismatch.id, nil, 404)
		f.request("directory-cancel", mismatch.prefix+"/cancel", f.proof("directory-cancel", map[string]any{"taskUUID": mismatch.id}), 404)
	}
	for _, actor := range []string{"directory-http-no-scope", "directory-foreign"} {
		f.request(actor, directoryV2HTTPPrefix+"/task?taskUUID="+v2.ID, nil, 404)
		f.request(actor, directoryV2HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 404)
	}
	f.request("directory-admin-other", directoryV2HTTPPrefix+"/receipt?domainId=domain-one&idempotencyKey=shared-profile-intent", nil, 404)
	f.request("directory-submit-one", directoryV2HTTPPrefix+"/receipt?domainId=domain-two&idempotencyKey=shared-profile-intent", nil, 404)
	f.request("directory-http-reader", directoryV2HTTPPrefix+"/task?taskUUID="+v2.ID, nil, 403)
	f.request("directory-http-reader", directoryV2HTTPPrefix+"/cancel", f.proof("directory-http-reader", map[string]any{"taskUUID": v2.ID}), 403)
	for _, path := range []string{"/api/tasks/cancel", "/api/tasks/recover"} {
		control := map[string]any{"taskUUID": v2.ID}
		if path == "/api/tasks/recover" {
			control["idempotencyKey"] = "no-generic-v2-recovery"
		}
		f.error(f.request("directory-cancel", path, f.proof("directory-cancel", control), 400), "directory_route_required")
	}
	f.error(f.request("directory-cancel", "/api/tasks/submit", f.proof("directory-cancel", map[string]any{"taskName": domains.DirectoryV2KindName, "domainId": "domain-one", "payloadVersion": 1, "payload": map[string]any{}, "idempotencyKey": "no-generic-v2-submit"}), 400), "directory_route_required")
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state='queued'`, v2.ID) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND state='reserved'`, v2.ID) != 1 {
		t.Fatal("generic or cross-profile controls changed the v2 lifecycle")
	}
	// A valid replay needs fresh proof; the previous committed proof is denied.
	proof := f.proof("directory-submit-two", directoryHTTPSyncBody("v2-replay"))
	first := operationalDecode[tasks.Submission](t, f.request("directory-submit-two", directoryV2HTTPPrefix+"/sync", proof, 200).Body.Bytes())
	f.error(f.request("directory-submit-two", directoryV2HTTPPrefix+"/sync", proof, 401), "invalid_credentials")
	replay := operationalDecode[tasks.Submission](t, f.request("directory-submit-two", directoryV2HTTPPrefix+"/sync", f.proof("directory-submit-two", directoryHTTPSyncBody("v2-replay")), 200).Body.Bytes())
	if !replay.Replayed || replay.Task.ID != first.Task.ID || f.count(`SELECT count(*) FROM adtr.tasks`) != 3 {
		t.Fatal("v2 replay duplicated admitted work")
	}
	f.request("directory-submit-two", "/api/directory/receipt?domainId=domain-one&idempotencyKey=v2-replay", nil, 404)
	result := operationalDecode[struct {
		Task tasks.Task `json:"task"`
	}](t, f.request("directory-cancel", directoryV2HTTPPrefix+"/cancel", f.proof("directory-cancel", map[string]any{"taskUUID": v2.ID}), 200).Body.Bytes())
	if result.Task.State != tasks.Cancelled || f.count(`SELECT count(*) FROM adtr.domain_audit WHERE task_id=$1 AND action='domain_directory_v2_cancel'`, v2.ID) != 1 {
		t.Fatal("dedicated v2 cancel failed to commit its outcome/audit")
	}
}

func directoryV2HTTPObservation(users int) ldapconnection.DirectoryV2Observation {
	base := directoryHTTPObservation(users)
	out := ldapconnection.DirectoryV2Observation{Source: base.Source, Objects: []directoryassets.StoredObjectV2{}}
	for _, object := range base.Objects {
		stored := directoryassets.StoredObjectV2{Base: object}
		if object.GUID == "00000000-0000-0000-0000-000000000001" {
			stored.Supplemental = directoryassets.RawSupplementalV2{ObjectSIDBytes: []byte{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0}, MailBytes: []byte(" Raw\x00😀@one.invalid "), DescriptionBytes: [][]byte{[]byte("line\r\n\t\x00<script>😀")}, WhenCreatedBytes: []byte("00010101000000.0Z")}
		}
		out.Objects = append(out.Objects, stored)
	}
	return out
}

// Explicitly synthetic producer: no credential decryption, LDAP or network.
// This is HTTP/SQL boundary evidence, NOT real TLS producer evidence. The real
// engine admits/claims/fences the task; all schema16 guards stay enabled. Only
// its original executor-return witness may acknowledge opened-use quiescence.
func (f *directoryV2HTTPFixture) stageV2(observation ldapconnection.DirectoryV2Observation) *directoryHTTPSyntheticRun {
	f.t.Helper()
	objects := make([]json.RawMessage, 0, len(observation.Objects))
	for _, object := range observation.Objects {
		raw, err := directoryassets.EncodeStoredObjectV2(object)
		if err != nil {
			f.t.Fatal("validate synthetic object", err)
		}
		objects = append(objects, raw)
	}
	raw, err := json.Marshal(struct {
		DictionaryVersion int                            `json:"dictionaryVersion"`
		Objects           []json.RawMessage              `json:"objects"`
		Source            ldapconnection.DirectorySource `json:"source"`
	}{2, objects, observation.Source})
	for _, object := range objects {
		clear(object)
	}
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { clear(raw) })
	digest := sha256.Sum256(raw)
	staged, release, done := make(chan error, 1), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	run := &directoryHTTPSyntheticRun{release: func() { once.Do(func() { close(release) }) }, done: done}
	f.installV2(f.runtime, func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		version, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			tag, err := tx.Exec(ctx, `UPDATE adtr.domain_directory_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$4,opener_fencing_token=$5,opener_attempt=$6 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state='reserved' AND purpose='domain.directory_read.v2' AND dictionary_version=2`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt)
			if err == nil && tag.RowsAffected() != 1 {
				err = fmt.Errorf("synthetic opened-use transition affected %d rows", tag.RowsAffected())
			}
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err == nil {
			_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
				_, err := tx.Exec(ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256,dictionary_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,2)`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.ActorID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt, len(observation.Objects), raw, hex.EncodeToString(digest[:]))
				if err == nil {
					// This synthetic executor stages both outputs that the real LDAP
					// producer commits atomically. The audit view must still wait for
					// the real engine finish before exposing a successful result.
					var tag pgconn.CommandTag
					tag, err = tx.Exec(ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result)
 SELECT tenant_id,actor_id,domain_id,'domain_directory_v2_result',(payload->>'connectionRevision')::bigint,(payload->>'connectionRevision')::bigint,(payload->>'connectionCredentialGeneration')::bigint,task_id,'success'
 FROM adtr.tasks WHERE task_id=$1 AND kind='domain.directory_read.v2'`, ex.Task.ID)
					if err == nil && tag.RowsAffected() != 1 {
						err = fmt.Errorf("synthetic v2 audit stage did not match the admitted task")
					}
				}
				return 95, json.RawMessage(`{"policyRevision":"synthetic-v2-private-cursor"}`), json.RawMessage(`{"objectGUID":"synthetic-v2-private-result"}`), err
			})
		}
		staged <- err
		if err != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_stage_failed"}
		}
		select {
		case <-release:
			return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{"objectGUID":"synthetic-v2-private-result"}`)}
		case <-ctx.Done():
			<-release
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_cancelled"}
		}
	})
	ctx, cancel := context.WithCancel(f.ctx)
	engine := f.engine
	go func() {
		worked, err := engine.RunOne(ctx, "synthetic-directory-v2-http")
		if err == nil && !worked {
			err = fmt.Errorf("synthetic v2 executor found no admitted task")
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
				f.t.Error("synthetic v2 executor did not return during cleanup")
			}
		}
	})
	select {
	case err := <-staged:
		if err != nil {
			f.t.Fatal("real guarded v2 publication", err)
		}
	case err := <-done:
		run.joined = true
		f.t.Fatal("real engine returned before v2 staging", err)
	case <-f.ctx.Done():
		f.t.Fatal("v2 staging timed out", f.ctx.Err())
	}
	return run
}

func (f *directoryV2HTTPFixture) finishV2(run *directoryHTTPSyntheticRun, task tasks.Task, state tasks.State) {
	f.t.Helper()
	run.release()
	select {
	case err := <-run.done:
		run.joined = true
		if err != nil {
			f.t.Fatal("real v2 engine finish/return acknowledgement", err)
		}
	case <-f.ctx.Done():
		f.t.Fatal("actual v2 executor return not observed", f.ctx.Err())
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state=$2`, task.ID, string(state)) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND purpose='domain.directory_read.v2' AND dictionary_version=2 AND state='quiesced' AND quiescence_reason='executor_returned'`, task.ID) != 1 || f.count(`SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read.v2' AND object_id=$1`, task.ID) != 0 {
		f.t.Fatal("actual v2 return did not persist the outcome and original-opener acknowledgement")
	}
}

func TestDirectoryV2MigratedHTTPLosslessStoredReadsAndDisabledGates(t *testing.T) {
	f := newDirectoryV2HTTPFixture(t)
	f.bind()
	grant := f.grantV2("directory-grant", "platform_admin", "lossless-v2-grant")
	task := f.submitV2("directory-submit-one", "lossless-v2-observation")
	f.unavailableV2(task.ID)
	o := directoryV2HTTPObservation(30)
	defer o.Discard()
	run := f.stageV2(o)
	f.unavailableV2(task.ID)
	if f.count(`SELECT count(*) FROM adtr.audit_source WHERE source='domain' AND event='domain_directory_v2_result' AND event_args->>'taskUUID'=$1 AND event_result='NONE' AND result_available IS FALSE`, task.ID) != 1 {
		t.Fatal("synthetic staged audit was exposed before the actual task finish")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND cursor->>'policyRevision'='synthetic-v2-private-cursor' AND result->>'objectGUID'='synthetic-v2-private-result'`, task.ID) != 1 {
		t.Fatal("privacy test lacks actual persisted private result/cursor")
	}
	for _, path := range []string{directoryV2HTTPPrefix + "/task?taskUUID=" + task.ID, "/api/tasks/detail?taskUUID=" + task.ID, "/api/tasks?domainId=domain-one"} {
		directoryHTTPRedacted(t, f.request("directory-admin", path, nil, 200).Body.Bytes())
	}
	f.finishV2(run, task, tasks.Succeeded)
	path := directoryV2HTTPPrefix + "/observation?domainId=domain-one&pageSize=25"
	w := f.request("directory-http-reader", path, nil, 200)
	page := operationalDecode[domains.DirectoryV2List](t, w.Body.Bytes())
	if !page.Available || page.DictionaryVersion != 2 || page.ObservationID != task.ID || page.Source == nil || page.Source.Domain != "one.invalid" || page.Page.Total != 30 || page.Page.Pages != 2 || len(page.List) != 25 {
		t.Fatal("authorized reader without task/credential grants could not read stored v2 data")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("v2 public response lost content protections")
	}
	first := page.List[0]
	if first.Mail == nil || *first.Mail != " Raw\x00😀@one.invalid " || first.ObjectSID == nil || *first.ObjectSID != "S-1-5-21" || first.WhenCreated == nil || *first.WhenCreated != "0001-01-01T00:00:00Z" || !reflect.DeepEqual(first.Description, []string{"line\r\n\t\x00<script>😀"}) {
		t.Fatal("PostgreSQL/HTTP changed accepted NUL, control, non-BMP, SID or year0001 values")
	}
	for i, object := range page.List {
		if object.GUID != fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1) {
			t.Fatal("v2 public page lost stable GUID ordering")
		}
	}
	if page.List[1].Mail != nil || page.List[1].Description != nil || page.List[1].WhenCreated != nil || page.List[1].ObjectSID != nil {
		t.Fatal("requested-but-absent supplemental fields stopped being null")
	}
	shape := operationalDecode[struct {
		domains.DirectoryV2List
		List []map[string]json.RawMessage `json:"list"`
	}](t, w.Body.Bytes())
	for _, object := range shape.List {
		if len(object) != 10 {
			t.Fatal("v2 public object is not exactly the frozen ten fields")
		}
		for _, name := range []string{"objectGUID", "distinguishedName", "kind", "objectClass", "samAccountName", "userAccountControl", "objectSid", "mail", "description", "whenCreated"} {
			if _, ok := object[name]; !ok {
				t.Fatal("v2 public projection omitted field", name)
			}
		}
	}
	for _, private := range []string{"mailBytes", "descriptionBytes", "objectSidBytes", "whenCreatedBytes", "supplemental", "synthetic-v2-private"} {
		if bytes.Contains(w.Body.Bytes(), []byte(private)) {
			t.Fatal("public projection disclosed storage/task metadata", private)
		}
	}
	var stored []byte
	if err := f.conn.QueryRow(f.ctx, `SELECT body FROM adtr.domain_directory_observations WHERE task_id=$1 AND dictionary_version=2`, task.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte{0}) || bytes.Contains(stored, []byte(`\u0000`)) || !bytes.Contains(stored, []byte(base64.StdEncoding.EncodeToString([]byte(" Raw\x00😀@one.invalid ")))) {
		t.Fatal("storage envelope parsed public NUL text instead of canonical raw base64")
	}
	clear(stored)
	if f.count(`SELECT count(*) FROM adtr.domain_audit WHERE task_id=$1 AND action='domain_directory_v2_result'`, task.ID) != 1 || f.count(`SELECT count(*) FROM adtr.audit_source WHERE source='domain' AND event='domain_directory_v2_result' AND event_args->>'taskUUID'=$1 AND event_result='SUCCESS' AND result_available IS TRUE`, task.ID) != 1 {
		t.Fatal("v2 staged audit was not projected from the actual task finish")
	}
	second := operationalDecode[domains.DirectoryV2List](t, f.request("directory-http-reader", path+"&pageIdx=2&observationId="+task.ID, nil, 200).Body.Bytes())
	if len(second.List) != 5 || second.List[0].GUID <= page.List[24].GUID || second.ObservationID != task.ID {
		t.Fatal("v2 snapshot continuation changed identity/order")
	}
	legacy := operationalDecode[domains.DirectoryList](t, f.request("directory-http-reader", "/api/directory/observation?domainId=domain-one", nil, 200).Body.Bytes())
	if legacy.Available || len(legacy.List) != 0 {
		t.Fatal("v1 reader fell forward to v2")
	}
	foreign := operationalDecode[domains.DirectoryV2List](t, f.request("directory-foreign", directoryV2HTTPPrefix+"/observation?domainId=domain-one", nil, 200).Body.Bytes())
	if foreign.Available || foreign.DictionaryVersion != 2 || len(foreign.List) != 0 {
		t.Fatal("v2 observation crossed same-domain-ID tenant boundary")
	}
	f.request("directory-foreign", directoryV2HTTPPrefix+"/observation?domainId=domain-one&observationId="+task.ID, nil, 409)
	f.request("directory-http-no-scope", path+"&observationId="+task.ID, nil, 404)

	newer := f.submitV2("directory-submit-two", "newer-empty-v2-observation")
	empty := directoryV2HTTPObservation(0)
	f.finishV2(f.stageV2(empty), newer, tasks.Succeeded)
	latest := operationalDecode[domains.DirectoryV2List](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	pinned := operationalDecode[domains.DirectoryV2List](t, f.request("directory-http-reader", path+"&pageIdx=2&observationId="+task.ID, nil, 200).Body.Bytes())
	if !latest.Available || latest.ObservationID != newer.ID || len(latest.List) != 0 || pinned.ObservationID != task.ID || len(pinned.List) != 5 || pinned.Page.Total != 30 {
		t.Fatal("new observation changed a pinned v2 continuation")
	}
	queued := f.submitV2("directory-submit-three", "cancel-with-gates-off")
	for _, gates := range [][2]bool{{true, false}, {false, true}, {false, false}} {
		f.installV2(f.runtimeWithGates(gates[0], gates[1]), nil)
		f.error(f.request("directory-admin", directoryV2HTTPPrefix+"/sync", f.proof("directory-admin", directoryHTTPSyncBody("disabled-v2")), 503), "directory_read_disabled")
		read := operationalDecode[domains.DirectoryV2List](t, f.request("directory-http-reader", path+"&observationId="+task.ID, nil, 200).Body.Bytes())
		if !read.Available || read.ObservationID != task.ID {
			t.Fatal("disabled collection hid stored v2 data")
		}
		for _, safe := range []string{directoryV2HTTPPrefix + "/receipt?domainId=domain-one&idempotencyKey=lossless-v2-observation", directoryV2HTTPPrefix + "/task?taskUUID=" + task.ID} {
			directoryHTTPRedacted(t, f.request("directory-submit-one", safe, nil, 200).Body.Bytes())
		}
		capability := operationalDecode[map[string]any](t, f.request("directory-admin", directoryV2HTTPUsePrefix+"/effective?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
		if capability["consumerEnabled"] != true {
			t.Fatal("deployment flag changed compiled consumer capability")
		}
	}
	cancel := operationalDecode[struct {
		Task tasks.Task `json:"task"`
	}](t, f.request("directory-cancel", directoryV2HTTPPrefix+"/cancel", f.proof("directory-cancel", map[string]any{"taskUUID": queued.ID}), 200).Body.Bytes())
	if cancel.Task.State != tasks.Cancelled {
		t.Fatal("disabled collection prevented dedicated cancellation")
	}
	if n, err := f.store.ReconcileReservedDirectoryV2Uses(f.ctx, f.conn.Config(), 10); err != nil || n != 1 {
		t.Fatal("disabled original cleanup failed", n, err)
	}
	f.request("directory-revoke", directoryV2HTTPUsePrefix+"/revoke", f.proof("directory-revoke", directoryV2HTTPGrantBody("platform_admin", grant, "revoke-collected-v2")), 200)
	f.request("directory-http-reader", path+"&observationId="+task.ID, nil, 200)
	f.request("directory-detach", "/api/domains/credential-source/detach", f.proof("directory-detach", map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedConnectionCredentialGeneration": "2", "idempotencyKey": "detach-v2-source"}), 200)
	f.unavailableV2(task.ID)
}

func TestDirectoryV2MigratedHTTPStoredEmptyAndOpenedCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			f := newDirectoryV2HTTPFixture(t)
			f.bind()
			f.grantV2("directory-grant", "platform_admin", "empty-or-cancelled-grant")
			task := f.submitV2("directory-submit-one", "empty-or-cancelled-v2")
			o := directoryV2HTTPObservation(0)
			if cancelled {
				o = directoryV2HTTPObservation(1)
			}
			defer o.Discard()
			run := f.stageV2(o)
			if cancelled {
				f.installV2(f.runtimeWithGates(false, false), nil)
				out := operationalDecode[struct {
					Task tasks.Task `json:"task"`
				}](t, f.request("directory-cancel", directoryV2HTTPPrefix+"/cancel", f.proof("directory-cancel", map[string]any{"taskUUID": task.ID}), 200).Body.Bytes())
				if out.Task.State != tasks.CancelRequested || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND state='opened'`, task.ID) != 1 || f.count(`SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read.v2' AND object_id=$1`, task.ID) != 1 {
					t.Fatal("cancel acknowledgment invented opened-use quiescence")
				}
				f.unavailableV2(task.ID)
				f.finishV2(run, task, tasks.Cancelled)
				f.unavailableV2(task.ID)
				return
			}
			f.finishV2(run, task, tasks.Succeeded)
			out := operationalDecode[domains.DirectoryV2List](t, f.request("directory-http-reader", directoryV2HTTPPrefix+"/observation?domainId=domain-one", nil, 200).Body.Bytes())
			if !out.Available || out.DictionaryVersion != 2 || out.ObservationID != task.ID || out.List == nil || len(out.List) != 0 || out.Page.Total != 0 || out.Page.Pages != 0 || out.Source == nil {
				t.Fatal("successful empty v2 observation was confused with unavailable")
			}
		})
	}
}

func TestDirectoryV2MigratedHTTPLivePermissionEpochAndReceipts(t *testing.T) {
	f := newDirectoryV2HTTPFixture(t)
	f.bind()
	f.grantV2("directory-grant", directoryHTTPWriterRole, "epoch-v2-grant")
	task := f.submitV2(directoryHTTPWriterRole, "epoch-v2-intent")
	path := directoryV2HTTPPrefix + "/receipt?domainId=domain-one&idempotencyKey=epoch-v2-intent"
	f.request(directoryHTTPWriterRole, path, nil, 200)
	f.request("directory-admin", "/api/access/permissions/save", f.proof("directory-admin", map[string]any{"roleID": directoryHTTPWriterRole, "permissions": []any{}}), 200)
	f.request(directoryHTTPWriterRole, path, nil, 401)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients[directoryHTTPWriterRole])
	f.request(directoryHTTPWriterRole, path, nil, 403)
	f.request(directoryHTTPWriterRole, directoryV2HTTPPrefix+"/task?taskUUID="+task.ID, nil, 403)
	f.request(directoryHTTPWriterRole, directoryV2HTTPPrefix+"/observation?domainId=domain-one", nil, 403)
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1`, task.ID) != 1 {
		t.Fatal("permission/epoch revocation erased persisted v2 intent")
	}
	// Restore the exact role permissions through the protected production API.
	// A newly authenticated actor can read the old intent, but the old execution
	// epoch must never resurrect even after permission is restored.
	permissions := []map[string]any{}
	for _, mark := range []string{"domains", "directory_assets", "tasks"} {
		permissions = append(permissions, map[string]any{"mark": mark, "auth": map[string]bool{"readable": true, "writeable": mark != "domains"}})
	}
	f.request("directory-admin-other", "/api/access/permissions/save", f.proof("directory-admin-other", map[string]any{"roleID": directoryHTTPWriterRole, "permissions": permissions}), 200)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients[directoryHTTPWriterRole])
	f.request(directoryHTTPWriterRole, path, nil, 200)
	live := operationalDecode[credentialuse.Effective](t, f.request(directoryHTTPWriterRole, directoryV2HTTPUsePrefix+"/effective?accountId="+governanceHTTPAccount, nil, 200).Body.Bytes())
	if !live.ExplicitlyGranted || !live.Eligible {
		t.Fatal("stale-epoch test did not restore otherwise valid current use authority")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks t JOIN adtr.users u ON u.id=t.actor_id WHERE t.task_id=$1 AND t.authorization_version<>u.authorization_version::text`, task.ID) != 1 {
		t.Fatal("fixture did not establish a genuinely stale authorization epoch")
	}
	executed := make(chan struct{}, 1)
	f.installV2(f.runtime, func(context.Context, tasks.Execution) tasks.Outcome {
		executed <- struct{}{}
		return tasks.Outcome{State: tasks.Failed, Code: "unexpected_stale_epoch_execution"}
	})
	if worked, err := f.engine.RunOne(f.ctx, "v2-stale-epoch-owner"); err != nil || !worked {
		t.Fatal("real stale-epoch claim failed", worked, err)
	}
	if len(executed) != 0 || f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state='failed' AND error_code='authorization_revoked' AND attempt=0`, task.ID) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND opened_at IS NOT NULL`, task.ID) != 0 {
		t.Fatal("restored permissions resurrected an old v2 execution epoch")
	}
}

func TestDirectoryV2HTTPUninitializedSchemaGatePrecedesIdentity(t *testing.T) {
	// A genuinely uninitialized isolated database, never a forged old stamp.
	f := newOperationalHTTPFixture(t, false)
	cfg := f.cfg.Copy()
	cfg.Tracer = f.trace
	s, err := auth.New(cfg, f.key, operationalHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	ds := domains.New(nil)
	engine, err := tasks.New(cfg, tasks.ProductionRegistry(ds.DirectoryKind(), ds.DirectoryV2Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(directoryV2HTTPPrefix+"/", s.DirectoryV2Handler(ds, engine))
	mux.Handle(directoryV2HTTPUsePrefix+"/", s.DirectoryV2CredentialUseHandler(credentialuse.New()))
	f.handler = mux
	f.trace.take()
	for _, path := range []string{directoryV2HTTPPrefix + "/observation?domainId=domain-one", directoryV2HTTPPrefix + "/receipt?domainId=domain-one&idempotencyKey=unknown", directoryV2HTTPPrefix + "/task?taskUUID=" + operationalHTTPMissingTask, directoryV2HTTPUsePrefix + "/effective?accountId=" + governanceHTTPAccount} {
		f.error(f.call(nil, path, nil, 503), "schema_incompatible")
	}
	body := directoryHTTPSyncBody("schema-gated-v2-intent")
	body["actorPassword"], body["totpCode"] = governanceHTTPPassword, "123456"
	f.error(f.call(nil, directoryV2HTTPPrefix+"/sync", body, 503), "schema_incompatible")
	for _, query := range f.trace.take() {
		lower := strings.ToLower(query)
		if strings.Contains(lower, "adtr.users") || strings.Contains(lower, "adtr.sessions") || strings.Contains(lower, "insert ") || strings.Contains(lower, "update ") || strings.Contains(lower, "delete ") {
			t.Fatalf("v2 schema gate reached authentication/proof/failure-audit writes: %s", query)
		}
	}
}
