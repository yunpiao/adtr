package domains

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func validDirectoryPins() directoryPinnedPayload {
	return directoryPinnedPayload{"operation_account", "1", "2", "directory_account", "3", "platform_admin", "4", strings.Repeat("a", 64)}
}

func TestDirectoryPayloadIndependentOfDiagnosticIdentity(t *testing.T) {
	p := validDirectoryPins()
	canonical, err := validateDirectoryPayload(p.json())
	if err != nil || !bytes.Equal(canonical, p.json()) {
		t.Fatal("valid directory pins rejected", err)
	}
	if IsConnectionTestKind(DirectoryKindName) || DirectoryKindName == AccountKindName {
		t.Fatal("directory consumer inherited connection-test identity")
	}
	if _, err = validateAccountPayload(canonical); err == nil {
		t.Fatal("directory pins accepted by diagnostic consumer")
	}
	withDiagnostic := append(append([]byte{}, canonical[:len(canonical)-1]...), []byte(`,"diagnosticGeneration":"1"}`)...)
	if _, err = validateDirectoryPayload(withDiagnostic); err == nil {
		t.Fatal("diagnostic generation must not enter directory identity")
	}
	var fields map[string]any
	if err = json.Unmarshal(canonical, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"connectionRevision", "connectionCredentialGeneration", "accountCredentialRevision", "grantRevision"} {
		for _, value := range []any{"0", "-1", "+1", "01", "1.0", "9223372036854775808", 1, nil, true} {
			copyFields := make(map[string]any)
			for k, v := range fields {
				copyFields[k] = v
			}
			copyFields[key] = value
			raw, _ := json.Marshal(copyFields)
			if _, err := validateDirectoryPayload(raw); err == nil {
				t.Fatalf("accepted invalid %s revision %v", key, value)
			}
		}
	}
}

func TestDirectoryPayloadRejectsAmbiguousOrUntrustedPins(t *testing.T) {
	base := validDirectoryPins().json()
	cases := [][]byte{
		nil, []byte(`null`), []byte(`[]`), append(append([]byte{}, base...), []byte(` {}`)...),
		append(append([]byte{}, base[:len(base)-1]...), []byte(`,"accountId":"other"}`)...),
		append(append([]byte{}, base[:len(base)-1]...), []byte(`,"baseDN":"DC=other"}`)...),
		bytes.Replace(base, []byte(`"accountId"`), []byte(`"AccountId"`), 1),
		bytes.Replace(base, []byte(`"accountId":"directory_account",`), nil, 1),
		bytes.Replace(base, []byte(`"operation_account"`), []byte(`"custom"`), 1),
		bytes.Replace(base, []byte(`"platform_admin"`), []byte(`"viewer"`), 1),
		bytes.Replace(base, []byte(strings.Repeat("a", 64)), []byte(strings.Repeat("A", 64)), 1),
		append(bytes.Repeat([]byte(" "), 2049), base...),
	}
	for i, raw := range cases {
		if _, err := validateDirectoryPayload(raw); err == nil {
			t.Fatalf("accepted ambiguous payload case %d", i)
		}
	}
	p := validDirectoryPins()
	p.GrantRoleID = strings.Repeat("r", 24)
	if _, err := validateDirectoryPayload(p.json()); err != nil {
		t.Fatal("valid custom role rejected", err)
	}
}
