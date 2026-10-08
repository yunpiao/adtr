package domains

import "encoding/json"

// DirectoryKindName is reserved for a separately authorized directory consumer.
// It is not a connection-test kind; production registration is explicit.
const DirectoryKindName = "domain.directory_read"

// directoryPinnedPayload never depends on the latest connection-test task or
// diagnostic generation. All values are immutable admission snapshots; knowing
// them alone does not grant authority or permit opening a credential.
type directoryPinnedPayload struct {
	CredentialSource               string `json:"credentialSource"`
	ConnectionRevision             string `json:"connectionRevision"`
	ConnectionCredentialGeneration string `json:"connectionCredentialGeneration"`
	AccountID                      string `json:"accountId"`
	AccountCredentialRevision      string `json:"accountCredentialRevision"`
	GrantRoleID                    string `json:"grantRoleId"`
	GrantRevision                  string `json:"grantRevision"`
	PolicyRevision                 string `json:"policyRevision"`
}

func (p directoryPinnedPayload) json() json.RawMessage { b, _ := json.Marshal(p); return b }

func validateDirectoryPayload(raw json.RawMessage) (json.RawMessage, error) {
	bad := func() (json.RawMessage, error) { return nil, problem(400, "invalid_payload") }
	if len(raw) > 2048 || !exactObject(raw, "credentialSource", "connectionRevision", "connectionCredentialGeneration", "accountId", "accountCredentialRevision", "grantRoleId", "grantRevision", "policyRevision") {
		return bad()
	}
	var p directoryPinnedPayload
	if json.Unmarshal(raw, &p) != nil || p.CredentialSource != "operation_account" || !ValidID(p.AccountID) || !validAccountGrantRole(p.GrantRoleID) {
		return bad()
	}
	for _, revision := range []string{p.ConnectionRevision, p.ConnectionCredentialGeneration, p.AccountCredentialRevision, p.GrantRevision} {
		if _, err := Revision(revision); err != nil {
			return bad()
		}
	}
	if len(p.PolicyRevision) != 64 {
		return bad()
	}
	for _, b := range []byte(p.PolicyRevision) {
		if !(b >= 'a' && b <= 'f' || b >= '0' && b <= '9') {
			return bad()
		}
	}
	return p.json(), nil
}

// directoryV2PinnedPayload extends only the separately named dictionary-2 task.
// The legacy payload remains the exact original eight fields.
type directoryV2PinnedPayload struct {
	directoryPinnedPayload
	DictionaryVersion int `json:"dictionaryVersion"`
}

func (p directoryV2PinnedPayload) json() json.RawMessage { b, _ := json.Marshal(p); return b }

func validateDirectoryV2Payload(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 2048 || !exactObject(raw, "credentialSource", "connectionRevision", "connectionCredentialGeneration", "accountId", "accountCredentialRevision", "grantRoleId", "grantRevision", "policyRevision", "dictionaryVersion") {
		return nil, problem(400, "invalid_payload")
	}
	var p directoryV2PinnedPayload
	if json.Unmarshal(raw, &p) != nil || p.DictionaryVersion != 2 {
		return nil, problem(400, "invalid_payload")
	}
	if _, err := validateDirectoryPayload(p.directoryPinnedPayload.json()); err != nil {
		return nil, err
	}
	return p.json(), nil
}

func validateDirectoryPayloadForProfile(profile directoryTaskProfile, raw json.RawMessage) (json.RawMessage, error) {
	switch profile {
	case directoryTaskV1:
		return validateDirectoryPayload(raw)
	case directoryTaskV2:
		return validateDirectoryV2Payload(raw)
	default:
		return nil, problem(400, "invalid_payload")
	}
}
