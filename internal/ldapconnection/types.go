// Package ldapconnection implements an authenticated RootDSE probe and a
// separately authorized, bounded directory reader. It deliberately has no
// general LDAP, referral, plaintext, or insecure TLS API.
package ldapconnection

import (
	"context"
	"crypto/x509"
	"net/netip"
)

type Mode string

const (
	StartTLS Mode = "starttls"
	LDAPS    Mode = "ldaps"
)

// Config must come from authorized, revision-checked domain configuration.
// The caller owns egress authorization and matching the returned naming context
// to the intended domain. DialIP pins the connection without changing the TLS
// DNS identity; it cannot select a port.
type Config struct {
	Mode       Mode
	ServerName string
	DialIP     netip.Addr
	// Roots is the mandatory nonempty, explicitly configured CA pool.
	// There is no implicit fallback to the operating system trust store.
	Roots           *x509.CertPool
	AllowedNetworks []netip.Prefix
	// Authorize is a mandatory trusted callback, never supplied by an HTTP
	// client. It must honor its context and finish its fenced revision and
	// authorization check without retaining a database lock across Probe I/O.
	Authorize func(context.Context, Stage) error
}

// Credential must never be logged. The caller owns and clears Password after
// Probe returns; ReadDirectory instead consumes and clears that slice itself.
// Probe clears its own bind request buffers and never serializes
// either field. String/GoString also redact common accidental formatting.
type Credential struct {
	Username string `json:"-"`
	Password []byte `json:"-"`
}

func (Credential) String() string   { return "[redacted credential]" }
func (Credential) GoString() string { return "[redacted credential]" }

type Result struct {
	DCHostName            string   `json:"dc_host_name"`
	DefaultNamingContext  string   `json:"default_naming_context"`
	SupportedCapabilities []string `json:"supported_capabilities"`
	ElapsedMilliseconds   int64    `json:"elapsed_milliseconds"`
}

type Code string
type Stage string

const (
	CodeInvalidConfig        Code = "invalid_config"
	CodeDNSFailed            Code = "dns_failed"
	CodeConnectFailed        Code = "connect_failed"
	CodeConnectionTimeout    Code = "connection_timeout"
	CodeTLSUntrusted         Code = "tls_untrusted"
	CodeTLSHostname          Code = "tls_hostname"
	CodeTLSExpired           Code = "tls_expired"
	CodeTLSFailed            Code = "tls_failed"
	CodeStartTLSRequired     Code = "starttls_required"
	CodeCredentialsRejected  Code = "credentials_rejected"
	CodeLDAPAccessDenied     Code = "ldap_access_denied"
	CodeLDAPTimeout          Code = "ldap_timeout"
	CodeReferralRejected     Code = "referral_rejected"
	CodeInvalidResponse      Code = "invalid_response"
	CodeCancelled            Code = "cancelled"
	CodeAuthorizationRevoked Code = "authorization_revoked"
	CodeEgressDenied         Code = "egress_denied"
)

const (
	StageValidate Stage = "validate"
	StageDNS      Stage = "dns"
	StageDial     Stage = "dial"
	StageStartTLS Stage = "starttls"
	StageTLS      Stage = "tls"
	StageBind     Stage = "bind"
	StageSearch   Stage = "search"
)

// Error contains only stable public classifications. Raw network, TLS, LDAP
// diagnostics, identities and credentials are never retained or wrapped.
type Error struct {
	Code  Code  `json:"code"`
	Stage Stage `json:"stage"`
}

func (e *Error) Error() string { return string(e.Code) + ":" + string(e.Stage) }

func failure(code Code, stage Stage) *Error { return &Error{Code: code, Stage: stage} }
