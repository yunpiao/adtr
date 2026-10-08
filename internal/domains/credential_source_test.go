package domains

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/tasks"
)

func sourceTestStore(t *testing.T, key byte) *Store {
	t.Helper()
	r, err := domainconfig.Load(func(name string) string {
		switch name {
		case "ADTR_DOMAIN_KEY_ID":
			return "synthetic-source"
		case "ADTR_DOMAIN_KEY":
			return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(key), 32)))
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(r)
}
func sourceString(v string) *string { return &v }
func sourceInput() SourceInput {
	return SourceInput{DomainID: "domain", ExpectedRevision: "7", ExpectedConnectionCredentialGeneration: "3", AccountID: "account", ExpectedAccountRevision: "4", ExpectedAccountCredentialRevision: "2", ExpectedGrantRevision: "19", IdempotencyKey: "intent"}
}

func TestSourceValidationRejectsMixedInputsAndNoncanonicalPins(t *testing.T) {
	in := sourceInput()
	if err := ValidateSourceInput("reference", &in); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*SourceInput){func(i *SourceInput) { i.DomainID = "platform" }, func(i *SourceInput) { i.AccountID = "foreign/account" }, func(i *SourceInput) { i.ExpectedRevision = "07" }, func(i *SourceInput) { i.ExpectedConnectionCredentialGeneration = "0" }, func(i *SourceInput) { i.ExpectedAccountRevision = "+4" }, func(i *SourceInput) { i.ExpectedAccountCredentialRevision = " 2" }, func(i *SourceInput) { i.ExpectedGrantRevision = "9223372036854775808" }, func(i *SourceInput) { i.Username = sourceString("reader") }, func(i *SourceInput) { i.Password = sourceString("secret") }, func(i *SourceInput) { i.IdempotencyKey = "schedule_reserved" }} {
		bad := in
		alter(&bad)
		if ValidateSourceInput("reference", &bad) == nil {
			t.Fatal("invalid source request accepted")
		}
	}
	custom := SourceInput{DomainID: "domain", ExpectedRevision: "1", ExpectedConnectionCredentialGeneration: "1", IdempotencyKey: "custom", Username: sourceString("SYNTHETIC\\reader"), Password: sourceString(" exact synthetic value ")}
	if err := ValidateSourceInput("custom", &custom); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"detach", "reference", "unknown", "/custom"} {
		if ValidateSourceInput(op, &custom) == nil {
			t.Fatal("cross-operation fields accepted", op)
		}
	}
	custom.AccountID = "account"
	if ValidateSourceInput("custom", &custom) == nil {
		t.Fatal("custom accepted account pointer")
	}
	detach := SourceInput{DomainID: "domain", ExpectedRevision: "1", ExpectedConnectionCredentialGeneration: "1", IdempotencyKey: "detach"}
	if err := ValidateSourceInput("detach", &detach); err != nil {
		t.Fatal(err)
	}
}
func TestSourceFingerprintBindsEveryNonproofFieldAndUsesKeyForPair(t *testing.T) {
	s := sourceTestStore(t, 's')
	p := tasks.Principal{TenantID: "tenant", ActorID: 23}
	in := sourceInput()
	fp, err := s.sourceFingerprint(p, "reference", in)
	if err != nil || len(fp) != 64 {
		t.Fatal(fp, err)
	}
	for _, alter := range []func(*SourceInput){func(i *SourceInput) { i.DomainID += "x" }, func(i *SourceInput) { i.ExpectedRevision = "8" }, func(i *SourceInput) { i.ExpectedConnectionCredentialGeneration = "4" }, func(i *SourceInput) { i.AccountID += "x" }, func(i *SourceInput) { i.ExpectedAccountRevision = "5" }, func(i *SourceInput) { i.ExpectedAccountCredentialRevision = "3" }, func(i *SourceInput) { i.ExpectedGrantRevision = "20" }, func(i *SourceInput) { i.IdempotencyKey += "x" }} {
		next := in
		alter(&next)
		got, err := s.sourceFingerprint(p, "reference", next)
		if err != nil || got == fp {
			t.Fatal("intent field unbound", err)
		}
	}
	for _, principal := range []tasks.Principal{{TenantID: "other", ActorID: 23}, {TenantID: "tenant", ActorID: 24}} {
		got, err := s.sourceFingerprint(principal, "reference", in)
		if err != nil || got == fp {
			t.Fatal("principal unbound", err)
		}
	}
	if got, err := New(nil).sourceFingerprint(p, "reference", in); err != nil || got != fp {
		t.Fatal("nonsecret reference fingerprint requires key", err)
	}
	if got, err := s.sourceFingerprint(p, "detach", in); err != nil || got == fp {
		t.Fatal("operation unbound", err)
	}
	custom := SourceInput{DomainID: "domain", ExpectedRevision: "1", ExpectedConnectionCredentialGeneration: "1", IdempotencyKey: "pair", Username: sourceString("synthetic-reader"), Password: sourceString("synthetic-password")}
	first, err := s.sourceFingerprint(p, "custom", custom)
	if err != nil {
		t.Fatal(err)
	}
	changedKey, err := sourceTestStore(t, 't').sourceFingerprint(p, "custom", custom)
	if err != nil || changedKey == first {
		t.Fatal("custom pair was not keyed", err)
	}
	changed := custom
	changed.Password = sourceString("synthetic-password ")
	different, err := s.sourceFingerprint(p, "custom", changed)
	if err != nil || different == first {
		t.Fatal("password bytes not bound", err)
	}
	changed = custom
	changed.Username = sourceString("other-reader")
	different, err = s.sourceFingerprint(p, "custom", changed)
	if err != nil || different == first {
		t.Fatal("username not bound", err)
	}
	if _, err = New(nil).sourceFingerprint(p, "custom", custom); err == nil {
		t.Fatal("custom fingerprint bypassed missing key")
	}
}
func TestSourceInputAndReceiptsAreSafeToFormat(t *testing.T) {
	in := sourceInput()
	in.Username = sourceString("sentinel-user")
	in.Password = sourceString("sentinel-password")
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, formatted := range []string{string(raw), fmt.Sprintf("%v", in), fmt.Sprintf("%#v", in)} {
		if strings.Contains(formatted, "sentinel") || strings.Contains(formatted, "account") {
			t.Fatal("source input leaked", formatted)
		}
	}
	receipt := SourceReceipt{Result: "SUCCESS", Operation: "reference", DomainID: "domain", Revision: "8", ConnectionCredentialGeneration: "4", CredentialSource: SourceOperationAccount, CurrentRevision: "8", CurrentConnectionCredentialGeneration: "4", CurrentCredentialSource: SourceOperationAccount}
	raw, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	expected := []string{"result", "operation", "domainId", "revision", "connectionCredentialGeneration", "credentialSource", "replayed", "deleted", "currentRevision", "currentConnectionCredentialGeneration", "currentCredentialSource"}
	if len(fields) != len(expected) {
		t.Fatal("receipt shape changed", string(raw))
	}
	for _, k := range expected {
		if _, ok := fields[k]; !ok {
			t.Fatal("receipt field absent", k)
		}
	}
}
func TestCreateUnconfiguredValidationAndFingerprint(t *testing.T) {
	in := Input{Domain: "EXAMPLE.Test.", DCHostName: "DC.EXAMPLE.Test.", Port: "389", IdempotencyKey: "bootstrap"}
	if err := ValidateInput("/create-unconfigured", &in); err != nil {
		t.Fatal(err)
	}
	if in.Domain != "example.test" || in.DCHostName != "dc.example.test" {
		t.Fatal("endpoint not canonicalized")
	}
	p := tasks.Principal{TenantID: "tenant", ActorID: 5}
	first := unconfiguredFingerprint(p, in)
	again := unconfiguredFingerprint(p, in)
	if first != again || len(first) != 64 {
		t.Fatal("unstable unconfigured fingerprint")
	}
	changed := in
	changed.Port = "636"
	if first == unconfiguredFingerprint(p, changed) {
		t.Fatal("endpoint intent unbound")
	}
	for _, bad := range []Input{{Domain: in.Domain, DCHostName: in.DCHostName, Port: in.Port, IdempotencyKey: in.IdempotencyKey, Username: sourceString("u"), Password: sourceString("p")}, {Domain: in.Domain, DCHostName: in.DCHostName, Port: in.Port, IdempotencyKey: in.IdempotencyKey, DomainID: "chosen"}, {Domain: in.Domain, DCHostName: in.DCHostName, Port: in.Port, IdempotencyKey: in.IdempotencyKey, ExpectedRevision: "1"}} {
		if ValidateInput("/create-unconfigured", &bad) == nil {
			t.Fatal("unconfigured accepted foreign fields")
		}
	}
}
