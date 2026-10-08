package audit

import (
	"strings"
	"testing"
)

func TestAccountReferenceAuditRetainsProtectedScopedProjection(t *testing.T) {
	for _, part := range []string{"source NOT IN ('audit','domain','operation_account','credential_use')", "t.domain_id=a.domain_id", "t.kind IN ('domain.connection_test','domain.account_connection_test')", "'credentialSource',a.credential_source", "'domain_credential_reference','domain_credential_custom','domain_credential_detach'"} {
		if !strings.Contains(AccountReferenceViewSchema, part) {
			t.Fatalf("missing migration13 audit invariant: %s", part)
		}
	}
	// Inspect only the domain arm: operation-account and governance producers have
	// their own existing permissions and intentionally retain their metadata.
	arm := strings.Split(strings.Split(AccountReferenceViewSchema, "SELECT 'domain',")[1], "UNION ALL")[0]
	for _, private := range []string{"operation_account_id", "accountCredentialRevision", "grantRevision", "ciphertext", "password"} {
		if strings.Contains(arm, private) {
			t.Fatal("domain projection widened account disclosure", private)
		}
	}
	if strings.Contains(CredentialUseViewSchema, "domain_credential_reference") {
		t.Fatal("historical migration projection was rewritten")
	}
}
