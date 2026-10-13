package ldapconnection

import (
	"context"
	"crypto/x509"
	"net"
	"net/netip"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

// DirectoryV4ReadAuthorization is a separate trusted authority for the fixed
// eleven-attribute dictionary. A legacy directory or probe grant cannot imply
// this authority. The caller owns its current purpose/revision checks.
type DirectoryV4ReadAuthorization func(context.Context, DirectoryStage) error

// DirectoryV4Config selects only the fixed dictionary-4 primitive. It offers
// no caller-selected LDAP attributes, filter, base, controls or TLS policy.
// Limits may tighten, but never exceed, the dictionary-4 resource ceilings.
type DirectoryV4Config struct {
	Mode            Mode
	ServerName      string
	Domain          string
	DialIP          netip.Addr
	Roots           *x509.CertPool
	AllowedNetworks []netip.Prefix
	Limits          directoryassets.Limits
	Authorize       DirectoryV4ReadAuthorization
}

// DirectoryV4Observation owns the retained supplemental bytes. Its objects use
// the lossless internal stored DTO, not the public JSON projection. The caller
// must call Discard after storage or projection. Failure returns a zero result.
// It has the same observation and class-identity limits as DirectoryObservation.
type DirectoryV4Observation struct {
	Objects []directoryassets.StoredObjectV4
	Source  DirectorySource
}

func (DirectoryV4Observation) String() string     { return "directory v4 observation (redacted)" }
func (o DirectoryV4Observation) GoString() string { return o.String() }

// Discard clears owned raw supplemental bytes and drops all retained results.
// It is idempotent and safe to defer immediately after ReadDirectoryV4.
func (o *DirectoryV4Observation) Discard() {
	if o != nil {
		directoryassets.ClearStoredObjectsV4(o.Objects)
		*o = DirectoryV4Observation{}
	}
}

// ReadDirectoryV4 is a fixed dictionary-4 transport primitive, not a task registration or
// credential-use grant. It consumes and clears credential.Password on all exits
// and joins owned I/O before its final authority check, including panic cleanup.
func ReadDirectoryV4(ctx context.Context, cfg DirectoryV4Config, credential Credential) (DirectoryV4Observation, error) {
	dialer := &net.Dialer{}
	return readDirectoryV4(ctx, cfg, credential, transport{lookup: net.DefaultResolver.LookupNetIP, dial: dialer.DialContext})
}

func readDirectoryV4(ctx context.Context, cfg DirectoryV4Config, credential Credential, network transport) (DirectoryV4Observation, error) {
	fixed := DirectoryConfig{Mode: cfg.Mode, ServerName: cfg.ServerName, Domain: cfg.Domain,
		DialIP: cfg.DialIP, Roots: cfg.Roots, AllowedNetworks: cfg.AllowedNetworks,
		Limits: cfg.Limits, Authorize: DirectoryReadAuthorization(cfg.Authorize)}
	result, err := readDirectoryProfile(ctx, fixed, credential, network, directoryProfileV4)
	return DirectoryV4Observation{Objects: result.objectsV4, Source: result.Source}, err
}
