package operationaccounts

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/tasks"
)

func syntheticStore(t *testing.T) *Store {
	t.Helper()
	values := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))}
	r, e := domainconfig.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal(e)
	}
	if r.ProbeEnabled() {
		t.Fatal("storage unexpectedly enabled probes")
	}
	return New(r)
}
func TestOperationAccountFingerprintBindsActorAndCompleteIntent(t *testing.T) {
	s := syntheticStore(t)
	p := tasks.Principal{TenantID: "tenant", ActorID: 1}
	in := Input{DomainID: "domain", ExpectedDomainRevision: "1", IdempotencyKey: "key", Username: ptr("reader@example.test"), Password: ptr("original")}
	first, e := s.fingerprint(p, "create", in)
	if e != nil || len(first) != 64 {
		t.Fatal(first, e)
	}
	if repeated, e := s.fingerprint(p, "create", in); e != nil || repeated != first {
		t.Fatal("non-deterministic intent digest", e)
	}
	for _, change := range []func(*Input){func(i *Input) { i.DomainID = "other" }, func(i *Input) { i.ExpectedDomainRevision = "2" }, func(i *Input) { i.IdempotencyKey = "other" }, func(i *Input) { i.Password = ptr("original ") }, func(i *Input) { i.Username = ptr("other@example.test") }, func(i *Input) { i.Label = ptr("label") }} {
		next := in
		change(&next)
		fp, e := s.fingerprint(p, "create", next)
		if e != nil || fp == first {
			t.Fatal("intent field missing from fingerprint", e)
		}
	}
	for _, next := range []tasks.Principal{{TenantID: "other", ActorID: 1}, {TenantID: "tenant", ActorID: 2}} {
		fp, e := s.fingerprint(next, "create", in)
		if e != nil || fp == first {
			t.Fatal("principal not bound", e)
		}
	}
	if fp, e := s.fingerprint(p, "update", in); e != nil || fp == first {
		t.Fatal("operation not bound", e)
	}
	in.Label = ptr("")
	if fp, e := s.fingerprint(p, "create", in); e != nil || fp != first {
		t.Fatal("equivalent create empty label changed intent", e)
	}
	if _, e := New(nil).fingerprint(p, "create", in); e == nil {
		t.Fatal("missing key bypass")
	}
}
func TestOperationAccountSealsCompleteStrictPair(t *testing.T) {
	s := syntheticStore(t)
	in := Input{Username: ptr("reader@alternate.test"), Password: ptr(" * exact\n ")}
	sealed, e := s.seal("tenant", "domain", "account", 1, in)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(sealed.Ciphertext)
	plain, e := s.runtime.Vault().OpenOperationCredential("tenant", "domain", "account", 1, sealed)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain)
	var pair map[string]any
	if e = json.Unmarshal(plain, &pair); e != nil {
		t.Fatal(e)
	}
	if len(pair) != 3 || pair["version"] != float64(2) || pair["username"] != *in.Username || pair["password"] != *in.Password {
		t.Fatal("pair envelope schema or preservation mismatch")
	}
	if _, e = s.runtime.Vault().OpenOperationCredential("tenant", "domain", "other", 1, sealed); e == nil {
		t.Fatal("cross-account decryption")
	}
}
