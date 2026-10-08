package domains

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func str(v string) *string { return &v }
func TestCanonicalDomainRejectsAliasesAndMalformedIdentity(t *testing.T) {
	for _, v := range []string{"example.test", "EXAMPLE.TEST.", "a-b.example"} {
		got, e := CanonicalDNS(v)
		if e != nil || got != strings.ToLower(strings.TrimSuffix(v, ".")) {
			t.Fatalf("valid DNS %q", v)
		}
	}
	for _, v := range []string{"localhost", " example.test", "example.test ", "example.test..", ".example.test", "-a.example", "a-.example", "a_1.example", "K.example", "é.example", "127.0.0.1", strings.Repeat("x", 64) + ".example", strings.Repeat("x.", 127) + "xx"} {
		if _, e := CanonicalDNS(v); e == nil {
			t.Fatalf("accepted invalid DNS %q", v)
		}
	}
}
func TestCredentialInputPreservesSecretAndPairSemantics(t *testing.T) {
	valid := Input{Domain: "EXAMPLE.TEST.", DCHostName: "DC1.EXAMPLE.TEST.", LDAPAddr: "10.20.0.8", Port: "389", Username: str(`OTHER\reader`), Password: str(" secret "), IdempotencyKey: "request-1"}
	if e := ValidateInput("/create", &valid); e != nil || *valid.Password != " secret " || valid.Domain != "example.test" {
		t.Fatal("valid credentials changed", e)
	}
	for _, name := range []string{`reader`, `\reader`, `DOMAIN\`, `DOMAIN\\reader`, ` user@example.test`, `user@localhost`, `a@b@example.test`, `DOMAIN\a b`, `a\b@c.test`} {
		if credentialValid(name, "x") {
			t.Fatalf("accepted username %q", name)
		}
	}
	for _, pwd := range []string{"", "a\x00b", strings.Repeat("a", 51)} {
		if credentialValid("reader@alternate.test", pwd) {
			t.Fatal("accepted invalid password")
		}
	}
	if !credentialValid("reader@alternate.test", strings.Repeat("密", 50)) {
		t.Fatal("valid Unicode credential")
	}
	edit := Input{DomainID: "opaque-id", ExpectedRevision: "1", DCHostName: "dc1.example.test", Port: "636"}
	if e := ValidateInput("/update", &edit); e != nil {
		t.Fatal(e)
	}
	edit.Username = str(`DOMAIN\reader`)
	if e := ValidateInput("/update", &edit); e == nil {
		t.Fatal("half credential accepted")
	}
}
func TestEndpointAndRevisionBounds(t *testing.T) {
	for _, v := range []string{"0", "01", "-1", "1.0", "9223372036854775808"} {
		if _, e := Revision(v); e == nil {
			t.Fatalf("revision %q", v)
		}
	}
	if _, e := Revision("9223372036854775807"); e != nil {
		t.Fatal(e)
	}
	for _, ip := range []string{" 10.1.2.3", "http://10.1.2.3", "10.1.2.3:389", "dc.example.test", "fe80::1%eth0", "::", "127.0.0.1", "169.254.169.254", "224.1.2.3"} {
		in := Input{DomainID: "id", ExpectedRevision: "1", DCHostName: "dc.example.test", LDAPAddr: ip, Port: "389"}
		if e := ValidateInput("/update", &in); e == nil {
			t.Fatalf("invalid IP %q", ip)
		}
	}
}
func TestDomainListFilterContract(t *testing.T) {
	q := url.Values{"filterDomain": {"EXAMPLE.TEST.", "example.test", "other.test"}, "filterStatus": {"verified", "testing", "verified"}, "filterKeyword": {"%_"}, "pageSize": {"-1"}, "tmSort": {"1"}}
	f, e := ParseFilter(q)
	if e != nil || len(f.Domains) != 2 || len(f.Statuses) != 2 || f.Keyword != "%_" || !f.Ascending || f.PageIdx != 1 {
		t.Fatal(f, e)
	}
	for _, q := range []url.Values{{"pageIdx": {"0"}}, {"pageIdx": {"01"}}, {"pageIdx": {"2"}, "pageSize": {"-1"}}, {"pageSize": {"101"}}, {"pageSize": {"1", "2"}}, {"filterKeyword": {strings.Repeat("界", 51)}}, {"filterStatus": {"run"}}, {"tmSort": {"2"}}, {"tenant": {"one"}}} {
		if _, e := ParseFilter(q); e == nil {
			t.Fatal("invalid filter", q)
		}
	}
}
func TestDiagnosticPayloadHasOnlyCanonicalPins(t *testing.T) {
	p := pinnedPayload{"1", "1", strings.Repeat("a", 64), "2"}
	if _, e := validatePayload(p.json()); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{}`, `{"connectionRevision":1}`, `{"connectionRevision":"1","credentialRevision":"1","policyRevision":"` + strings.Repeat("a", 64) + `","diagnosticGeneration":"01"}`, `{"connectionRevision":"1","credentialRevision":"1","policyRevision":"` + strings.Repeat("a", 64) + `","diagnosticGeneration":"1","password":"secret"}`} {
		if _, e := validatePayload(json.RawMessage(raw)); e == nil {
			t.Fatal("invalid payload", raw)
		}
	}
	k := New(nil).Kind()
	if !k.SingleAttemptOnly || k.ReplaySafe || k.Schedulable || k.MaxAttempts != 1 || len(k.RetryCodes) != 0 || k.OwnerScoped || k.CancelDiscardsResult {
		t.Fatal("unsafe diagnostic policy")
	}
}
func TestNamingContextMatchesStructuralDNSIdentity(t *testing.T) {
	for _, dn := range []string{`DC=example,DC=test`, `dc=EXAMPLE, dc=TEST`, `DC=\65xample,DC=test`} {
		if !matchesNamingContext(dn, "example.test") {
			t.Fatalf("valid DN %q", dn)
		}
	}
	for _, dn := range []string{`DC=example,DC=test,DC=other`, `CN=example,DC=test`, `DC=example+UID=1,DC=test`, `DC=example\,other,DC=test`, `DC=exampl,DC=test`, `DC=example,DC=test\`, `DC=example,DC=test;CN=other`, `DC=example,DC=te\st`, `DC=example,DC=K`} {
		if matchesNamingContext(dn, "example.test") {
			t.Fatalf("mismatched DN %q", dn)
		}
	}
	if matchesNamingContext(`DC=\e2\84\aa,DC=test`, "k.test") {
		t.Fatal("Unicode folding changed directory identity")
	}
}

func TestDiagnosticDTOExposesOnlyFrozenSafeFields(t *testing.T) {
	raw, e := json.Marshal(Diagnostic{TaskUUID: "task", Revision: "1", CredentialRevision: "2", Code: "tls_untrusted", Stage: "tls", SuccessfulObservation: false, NamingContext: "internal-only"})
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil || len(fields) != 8 {
		t.Fatal("diagnostic contract changed", string(raw), e)
	}
	for _, key := range []string{"taskUUID", "revision", "credentialRevision", "stage", "code", "observedAt", "elapsedMilliseconds", "dcHostName"} {
		if _, ok := fields[key]; !ok {
			t.Fatal("missing required diagnostic field", key)
		}
	}
	if string(fields["dcHostName"]) != `""` || strings.Contains(string(raw), "internal-only") || strings.Contains(string(raw), "successfulObservation") {
		t.Fatal("internal observation leaked", string(raw))
	}
}
