// Package domains persists local, tenant-bound connection configuration. A saved
// connection and an observed bind never imply directory inventory or ownership.
package domains

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

const KindName = "domain.connection_test"

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func problem(status int, code string) error { return &Error{status, code} }

// Input is write-only. It deliberately has no actor or tenant assertions.
type Input struct {
	DomainID         string  `json:"domainId"`
	Domain           string  `json:"domain"`
	DCHostName       string  `json:"dcHostName"`
	LDAPAddr         string  `json:"ldapAddr"`
	Port             string  `json:"port"`
	Username         *string `json:"username"`
	Password         *string `json:"password"`
	ExpectedRevision string  `json:"expectedRevision"`
	ConfirmDomain    string  `json:"confirmDomain"`
	IdempotencyKey   string  `json:"idempotencyKey"`
}

func (Input) String() string   { return "[domain mutation]" }
func (Input) GoString() string { return "[domain mutation]" }

type Diagnostic struct {
	TaskUUID              string    `json:"taskUUID"`
	Revision              string    `json:"revision"`
	CredentialRevision    string    `json:"credentialRevision"`
	Code                  string    `json:"code"`
	Stage                 string    `json:"stage"`
	ObservedAt            time.Time `json:"observedAt"`
	ElapsedMilliseconds   int64     `json:"elapsedMilliseconds"`
	SuccessfulObservation bool      `json:"-"`
	DCHostName            string    `json:"dcHostName"`
	NamingContext         string    `json:"-"`
}
type Connection struct {
	DomainID                       string      `json:"domainId"`
	Domain                         string      `json:"domain"`
	DCHostName                     string      `json:"dcHostName"`
	LDAPAddr                       string      `json:"ldapAddr"`
	Port                           string      `json:"port"`
	Mode                           string      `json:"mode"`
	Revision                       string      `json:"revision"`
	CredentialRevision             string      `json:"credentialRevision"`
	ConnectionCredentialGeneration string      `json:"connectionCredentialGeneration"`
	CredentialSource               string      `json:"credentialSource"`
	CredentialConfigured           bool        `json:"credentialConfigured"`
	CreatedAt                      time.Time   `json:"createdAt"`
	UpdatedAt                      time.Time   `json:"updatedAt"`
	ConnectionState                string      `json:"connectionState"`
	LatestTaskUUID                 string      `json:"latestTaskUUID"`
	LastDiagnostic                 *Diagnostic `json:"lastDiagnostic"`
}
type Receipt struct {
	DomainID                   string `json:"domainId"`
	Domain                     string `json:"domain"`
	Revision                   string `json:"revision"`
	Deleted                    bool   `json:"deleted"`
	RequiresResourceAssignment bool   `json:"requiresResourceAssignment"`
}
type Mutation struct {
	Result                     string `json:"result"`
	DomainID                   string `json:"domainId"`
	Revision                   string `json:"revision,omitempty"`
	RequiresResourceAssignment bool   `json:"requiresResourceAssignment,omitempty"`
	Replayed                   bool   `json:"replayed"`
}
type Page struct {
	Index int `json:"pageIdx"`
	Size  int `json:"pageSize"`
	Total int `json:"total"`
	Pages int `json:"totalPage"`
}
type List struct {
	Page      Page         `json:"page"`
	List      []Connection `json:"List"`
	Exhausted bool         `json:"exhausted"`
}
type Filter struct {
	PageIdx, PageSize int
	Domains, Statuses []string
	Keyword           string
	Ascending         bool
}

type probeFunc func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error)
type Store struct {
	runtime *domainconfig.Runtime
	probe   probeFunc
}

func New(runtime *domainconfig.Runtime) *Store {
	return &Store{runtime: runtime, probe: ldapconnection.Probe}
}
func (s *Store) policyRevision() string { return s.runtime.Policy().Revision() }

type pinnedPayload struct {
	ConnectionRevision   string `json:"connectionRevision"`
	CredentialRevision   string `json:"credentialRevision"`
	PolicyRevision       string `json:"policyRevision"`
	DiagnosticGeneration string `json:"diagnosticGeneration"`
}

func (p pinnedPayload) json() json.RawMessage { b, _ := json.Marshal(p); return b }
