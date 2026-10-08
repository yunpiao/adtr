// Package operationaccounts stores supplied credentials locally. It has no
// network adapter, credential resolver, task executor, or remote AD operation.
package operationaccounts

import (
	"time"

	"github.com/yunpiao/adtr/internal/domainconfig"
)

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func problem(status int, code string) error { return &Error{status, code} }

// Input is write-only; proofs and caller-supplied authority are never stored.
type Input struct {
	DomainID               string  `json:"domainId"`
	AccountID              string  `json:"accountId"`
	ExpectedDomainRevision string  `json:"expectedDomainRevision"`
	ExpectedRevision       string  `json:"expectedRevision"`
	ConfirmAccountID       string  `json:"confirmAccountId"`
	IdempotencyKey         string  `json:"idempotencyKey"`
	Label                  *string `json:"label"`
	Username               *string `json:"username"`
	Password               *string `json:"password"`
}

func (Input) String() string   { return "[operation account mutation]" }
func (Input) GoString() string { return "[operation account mutation]" }

// Public marshaling cannot accidentally disclose credential inputs. Fingerprint
// encoding uses a private, exact request projection instead.
func (Input) MarshalJSON() ([]byte, error) { return []byte(`"[operation account mutation]"`), nil }

type Account struct {
	AccountID            string    `json:"accountId"`
	DomainID             string    `json:"domainId"`
	Domain               string    `json:"domain"`
	Label                string    `json:"label"`
	Revision             string    `json:"revision"`
	CredentialRevision   string    `json:"credentialRevision"`
	CredentialConfigured bool      `json:"credentialConfigured"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
	StorageState         string    `json:"storageState"`
	VerificationState    string    `json:"verificationState"`
}

// Revision fields without the Current prefix describe the immutable original
// action. Current fields and Deleted reflect the authorized present lifecycle.
type Receipt struct {
	Result                    string `json:"result"`
	Operation                 string `json:"operation"`
	AccountID                 string `json:"accountId"`
	DomainID                  string `json:"domainId"`
	Revision                  string `json:"revision"`
	CredentialRevision        string `json:"credentialRevision"`
	Replayed                  bool   `json:"replayed"`
	VerificationState         string `json:"verificationState"`
	Deleted                   bool   `json:"deleted"`
	CurrentRevision           string `json:"currentRevision"`
	CurrentCredentialRevision string `json:"currentCredentialRevision"`
}
type Page struct {
	Index int `json:"pageIdx"`
	Size  int `json:"pageSize"`
	Total int `json:"total"`
	Pages int `json:"totalPage"`
}
type List struct {
	Page      Page      `json:"page"`
	List      []Account `json:"List"`
	Exhausted bool      `json:"exhausted"`
}
type Filter struct {
	PageIdx, PageSize int
	Domains           []string
	Keyword           string
}
type Store struct{ runtime *domainconfig.Runtime }

func New(runtime *domainconfig.Runtime) *Store { return &Store{runtime: runtime} }
func (s *Store) Available() bool               { return s != nil && s.runtime.Enabled() }
