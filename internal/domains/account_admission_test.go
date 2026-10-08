package domains

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validAccountPins() accountPinnedPayload {
	return accountPinnedPayload{"operation_account", "8", "4", "account-id", "2", "platform_admin", "19", strings.Repeat("a", 64), "6"}
}

func TestAccountPayloadIsSeparateExactContract(t *testing.T) {
	p := validAccountPins()
	raw, err := validateAccountPayload(p.json())
	if err != nil || string(raw) != string(p.json()) {
		t.Fatal("valid account pins rejected", err)
	}
	if _, err = validatePayload(raw); err == nil {
		t.Fatal("account payload accepted as custom")
	}
	if _, err = validateAccountPayload((pinnedPayload{"8", "4", p.PolicyRevision, "6"}).json()); err == nil {
		t.Fatal("custom payload accepted as account")
	}
	var original map[string]any
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for key := range original {
		t.Run(key, func(t *testing.T) {
			for _, replacement := range []any{nil, 1, true, []string{"1"}, map[string]string{"a": "1"}} {
				edited := map[string]any{}
				for k, v := range original {
					edited[k] = v
				}
				edited[key] = replacement
				b, _ := json.Marshal(edited)
				if _, err = validateAccountPayload(b); err == nil {
					t.Fatalf("nonstring %s accepted", key)
				}
			}
			edited := map[string]any{}
			for k, v := range original {
				edited[k] = v
			}
			delete(edited, key)
			b, _ := json.Marshal(edited)
			if _, err = validateAccountPayload(b); err == nil {
				t.Fatalf("missing %s accepted", key)
			}
		})
	}
	for _, suffix := range []string{`,"accountId":"second"}`, `,"accountRevision":"1"}`, `,"username":"synthetic"}`} {
		bad := append(append([]byte{}, raw[:len(raw)-1]...), suffix...)
		if _, err = validateAccountPayload(bad); err == nil {
			t.Fatal("extended/duplicate account payload accepted")
		}
	}
	for _, bad := range []string{"0", "01", "+1", "-1", "9223372036854775808", "1.0", " 1"} {
		p := validAccountPins()
		p.GrantRevision = bad
		if _, err = validateAccountPayload(p.json()); err == nil {
			t.Fatal("noncanonical revision accepted")
		}
	}
	customRole := validAccountPins()
	customRole.GrantRoleID = strings.Repeat("r", 24)
	if _, err = validateAccountPayload(customRole.json()); err != nil {
		t.Fatal("valid custom role rejected", err)
	}
	for _, role := range []string{"made-up-role", strings.Repeat("r", 23), strings.Repeat("r", 25), strings.Repeat(".", 24), "viewer", "platform"} {
		p := validAccountPins()
		p.GrantRoleID = role
		if _, err = validateAccountPayload(p.json()); err == nil {
			t.Fatal("invalid role ID accepted")
		}
	}
	for _, change := range []func(*accountPinnedPayload){
		func(p *accountPinnedPayload) { p.CredentialSource = "custom" }, func(p *accountPinnedPayload) { p.CredentialSource = "unconfigured" },
		func(p *accountPinnedPayload) { p.AccountID = "platform" }, func(p *accountPinnedPayload) { p.AccountID = "account/id" },
		func(p *accountPinnedPayload) { p.GrantRoleID = "viewer" }, func(p *accountPinnedPayload) { p.GrantRoleID = "role/id" },
		func(p *accountPinnedPayload) { p.PolicyRevision = strings.Repeat("A", 64) },
	} {
		p := validAccountPins()
		change(&p)
		if _, err = validateAccountPayload(p.json()); err == nil {
			t.Fatal("invalid pins accepted")
		}
	}
}

func TestConnectionTestFamilyAndAccountKindCapabilities(t *testing.T) {
	for _, kind := range []string{KindName, AccountKindName} {
		if !IsConnectionTestKind(kind) {
			t.Fatal("missing exact kind")
		}
	}
	for _, kind := range []string{"domain.other", "domain.account_connection_test.v2", "domain.connection_test.extra", "", "domain."} {
		if IsConnectionTestKind(kind) {
			t.Fatal("broad kind classification")
		}
	}
	s := New(nil)
	a, c := s.AccountKind(), s.Kind()
	if a.OnQuiesced == nil || c.OnQuiesced != nil || a.Version != 1 || a.MaxAttempts != 1 || !a.SingleAttemptOnly || a.ReplaySafe || a.Schedulable || a.OwnerScoped || a.Lease != 30*time.Second || a.Heartbeat != time.Second || a.Timeout != 10*time.Second || len(a.RetryCodes) != 0 {
		t.Fatal("account task capability drift")
	}
}
