package domains

const (
	SourceCustom           = "custom"
	SourceOperationAccount = "operation_account"
	SourceUnconfigured     = "unconfigured"
)

// SourceInput is write-only. Proof is held separately by the authenticated HTTP
// adapter and never enters persistence or a request fingerprint.
type SourceInput struct {
	DomainID                               string  `json:"domainId"`
	ExpectedRevision                       string  `json:"expectedRevision"`
	ExpectedConnectionCredentialGeneration string  `json:"expectedConnectionCredentialGeneration"`
	AccountID                              string  `json:"accountId"`
	ExpectedAccountRevision                string  `json:"expectedAccountRevision"`
	ExpectedAccountCredentialRevision      string  `json:"expectedAccountCredentialRevision"`
	ExpectedGrantRevision                  string  `json:"expectedGrantRevision"`
	Username                               *string `json:"username"`
	Password                               *string `json:"password"`
	IdempotencyKey                         string  `json:"idempotencyKey"`
}

func (SourceInput) String() string   { return "[domain credential source mutation]" }
func (SourceInput) GoString() string { return "[domain credential source mutation]" }
func (SourceInput) MarshalJSON() ([]byte, error) {
	return []byte(`"[domain credential source mutation]"`), nil
}

type SourceReceipt struct {
	Result                                string `json:"result"`
	Operation                             string `json:"operation"`
	DomainID                              string `json:"domainId"`
	Revision                              string `json:"revision"`
	ConnectionCredentialGeneration        string `json:"connectionCredentialGeneration"`
	CredentialSource                      string `json:"credentialSource"`
	Replayed                              bool   `json:"replayed"`
	Deleted                               bool   `json:"deleted"`
	CurrentRevision                       string `json:"currentRevision"`
	CurrentConnectionCredentialGeneration string `json:"currentConnectionCredentialGeneration"`
	CurrentCredentialSource               string `json:"currentCredentialSource"`
}

type SourceReference struct {
	AccountID                 string `json:"accountId"`
	Label                     string `json:"label"`
	AccountRevision           string `json:"accountRevision"`
	AccountCredentialRevision string `json:"accountCredentialRevision"`
	Purpose                   string `json:"purpose"`
	ExplicitlyGranted         bool   `json:"explicitlyGranted"`
	Eligible                  bool   `json:"eligible"`
	GrantRevision             string `json:"grantRevision"`
}

type SourceDetail struct {
	DomainID                       string           `json:"domainId"`
	Revision                       string           `json:"revision"`
	ConnectionCredentialGeneration string           `json:"connectionCredentialGeneration"`
	CredentialSource               string           `json:"credentialSource"`
	CredentialConfigured           bool             `json:"credentialConfigured"`
	TestEligible                   bool             `json:"testEligible"`
	Reference                      *SourceReference `json:"reference"`
}
