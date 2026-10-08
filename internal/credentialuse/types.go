// Package credentialuse manages explicit, metadata-only credential permission
// records. It has no credential vault, network client, or execution adapter.
package credentialuse

import "time"

const Purpose = "domain.connection_test"

// ConsumerEnabled describes compiled support for the fixed account-backed
// connection test. It does not grant use or assert deployment/network readiness.
const ConsumerEnabled = true

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func problem(status int, code string) error { return &Error{status, code} }

// Input deliberately cannot contain proof, credentials, or caller authority.
type Input struct {
	AccountID                  string `json:"accountId"`
	RoleID                     string `json:"roleId"`
	Purpose                    string `json:"purpose"`
	ExpectedAccountRevision    string `json:"expectedAccountRevision"`
	ExpectedCredentialRevision string `json:"expectedCredentialRevision"`
	ExpectedGrantRevision      string `json:"expectedGrantRevision"`
	IdempotencyKey             string `json:"idempotencyKey"`
}

func (Input) String() string               { return "[credential use mutation]" }
func (Input) GoString() string             { return "[credential use mutation]" }
func (Input) MarshalJSON() ([]byte, error) { return []byte(`"[credential use mutation]"`), nil }

type Account struct {
	AccountID          string `json:"accountId"`
	DomainID           string `json:"domainId"`
	Domain             string `json:"domain"`
	Label              string `json:"label"`
	Revision           string `json:"revision"`
	CredentialRevision string `json:"credentialRevision"`
}
type Grant struct {
	RoleID                    string    `json:"roleId"`
	RoleName                  string    `json:"roleName"`
	Purpose                   string    `json:"purpose"`
	Allowed                   bool      `json:"allowed"`
	GrantRevision             string    `json:"grantRevision"`
	AccountCredentialRevision string    `json:"accountCredentialRevision"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}
type Role struct {
	RoleID      string `json:"roleId"`
	RoleName    string `json:"roleName"`
	MemberCount int    `json:"memberCount"`
}
type GrantList struct {
	Account         Account `json:"account"`
	Grants          []Grant `json:"grants"`
	ConsumerEnabled bool    `json:"consumerEnabled"`
}
type RoleList struct {
	Roles           []Role `json:"roles"`
	ConsumerEnabled bool   `json:"consumerEnabled"`
}
type Effective struct {
	AccountID                 string `json:"accountId"`
	DomainID                  string `json:"domainId"`
	Purpose                   string `json:"purpose"`
	ExplicitlyGranted         bool   `json:"explicitlyGranted"`
	Eligible                  bool   `json:"eligible"`
	GrantRevision             string `json:"grantRevision"`
	AccountCredentialRevision string `json:"accountCredentialRevision"`
	ConsumerEnabled           bool   `json:"consumerEnabled"`
}
type Receipt struct {
	Result                    string `json:"result"`
	Operation                 string `json:"operation"`
	AccountID                 string `json:"accountId"`
	DomainID                  string `json:"domainId"`
	RoleID                    string `json:"roleId"`
	Purpose                   string `json:"purpose"`
	GrantRevision             string `json:"grantRevision"`
	AccountCredentialRevision string `json:"accountCredentialRevision"`
	Allowed                   bool   `json:"allowed"`
	Replayed                  bool   `json:"replayed"`
	AccountDeleted            bool   `json:"accountDeleted"`
	CurrentGrantRevision      string `json:"currentGrantRevision"`
	CurrentAllowed            bool   `json:"currentAllowed"`
}
type Store struct{}

func New() *Store { return &Store{} }

type Filter struct {
	PageIdx, PageSize int
	Keyword           string
}
type Page struct {
	Index int `json:"pageIdx"`
	Size  int `json:"pageSize"`
	Total int `json:"total"`
	Pages int `json:"totalPage"`
}
type AccountList struct {
	Page            Page      `json:"page"`
	List            []Account `json:"List"`
	Exhausted       bool      `json:"exhausted"`
	ConsumerEnabled bool      `json:"consumerEnabled"`
}
