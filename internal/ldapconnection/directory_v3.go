package ldapconnection

import (
	"context"
	"crypto/x509"
	"net"
	"net/netip"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

// DirectoryV3ReadAuthorization is a separate trusted authority for the fixed
// ten-attribute dictionary. A legacy directory or probe grant cannot imply
// this authority. The caller owns its current purpose/revision checks.
type DirectoryV3ReadAuthorization func(context.Context, DirectoryStage) error

// DirectoryV3Config selects only the fixed dictionary-3 primitive. It offers
// no caller-selected LDAP attributes, filter, base, controls or TLS policy.
// Limits may tighten, but never exceed, the dictionary-3 resource ceilings.
type DirectoryV3Config struct {
	Mode            Mode
	ServerName      string
	Domain          string
	DialIP          netip.Addr
	Roots           *x509.CertPool
	AllowedNetworks []netip.Prefix
	Limits          directoryassets.Limits
	Authorize       DirectoryV3ReadAuthorization
}

// DirectoryV3Observation owns the retained supplemental bytes. Its objects use
// the lossless internal stored DTO, not the public JSON projection. The caller
// must call Discard after storage or projection. Failure returns a zero result.
// It has the same observation and class-identity limits as DirectoryObservation.
type DirectoryV3Observation struct {
	Objects []directoryassets.StoredObjectV3
	Source  DirectorySource
}

func (DirectoryV3Observation) String() string     { return "directory v3 observation (redacted)" }
func (o DirectoryV3Observation) GoString() string { return o.String() }

// Discard clears owned raw supplemental bytes and drops all retained results.
// It is idempotent and safe to defer immediately after ReadDirectoryV3.
func (o *DirectoryV3Observation) Discard() {
	if o != nil {
		directoryassets.ClearStoredObjectsV3(o.Objects)
		*o = DirectoryV3Observation{}
	}
}

// ReadDirectoryV3 is a fixed dictionary-3 transport primitive, not a task registration or
// credential-use grant. It consumes and clears credential.Password on all exits
// and joins owned I/O before its final authority check, including panic cleanup.
func ReadDirectoryV3(ctx context.Context, cfg DirectoryV3Config, credential Credential) (DirectoryV3Observation, error) {
	dialer := &net.Dialer{}
	return readDirectoryV3(ctx, cfg, credential, transport{lookup: net.DefaultResolver.LookupNetIP, dial: dialer.DialContext})
}

func readDirectoryV3(ctx context.Context, cfg DirectoryV3Config, credential Credential, network transport) (DirectoryV3Observation, error) {
	fixed := DirectoryConfig{Mode: cfg.Mode, ServerName: cfg.ServerName, Domain: cfg.Domain,
		DialIP: cfg.DialIP, Roots: cfg.Roots, AllowedNetworks: cfg.AllowedNetworks,
		Limits: cfg.Limits, Authorize: DirectoryReadAuthorization(cfg.Authorize)}
	result, err := readDirectoryProfile(ctx, fixed, credential, network, directoryProfileV3)
	return DirectoryV3Observation{Objects: result.objectsV3, Source: result.Source}, err
}
