package ldapconnection

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

// DirectoryV2ReadAuthorization is a separate trusted authority for the fixed
// eight-attribute dictionary. A legacy directory or probe grant cannot imply
// this authority. The caller owns its current purpose/revision checks.
type DirectoryV2ReadAuthorization func(context.Context, DirectoryStage) error

// DirectoryV2Config selects only the fixed dictionary-2 primitive. It offers
// no caller-selected LDAP attributes, filter, base, controls or TLS policy.
// Limits may tighten, but never exceed, the dictionary-2 resource ceilings.
type DirectoryV2Config struct {
	Mode            Mode
	ServerName      string
	Domain          string
	DialIP          netip.Addr
	Roots           *x509.CertPool
	AllowedNetworks []netip.Prefix
	Limits          directoryassets.Limits
	Authorize       DirectoryV2ReadAuthorization
}

// DirectoryV2Observation owns the retained supplemental bytes. Its objects use
// the lossless internal stored DTO, not the public JSON projection. The caller
// must call Discard after storage or projection. Failure returns a zero result.
// It has the same observation and class-identity limits as DirectoryObservation.
type DirectoryV2Observation struct {
	Objects []directoryassets.StoredObjectV2
	Source  DirectorySource
}

func (DirectoryV2Observation) String() string     { return "directory v2 observation (redacted)" }
func (o DirectoryV2Observation) GoString() string { return o.String() }

// Discard clears owned raw supplemental bytes and drops all retained results.
// It is idempotent and safe to defer immediately after ReadDirectoryV2.
func (o *DirectoryV2Observation) Discard() {
	if o != nil {
		directoryassets.ClearStoredObjectsV2(o.Objects)
		*o = DirectoryV2Observation{}
	}
}

// CodeUnsupportedProfile denotes a local dictionary representation limit or
// unsupported SID/time form. It does not assert malformed directory data.
const CodeUnsupportedProfile Code = "unsupported_directory_profile"

// CodeDirectoryLimit means a configured resource ceiling was reached, not that
// the source values or protocol were malformed. Legacy error codes are unchanged.
const CodeDirectoryLimit Code = "directory_limit_exceeded"

// ReadDirectoryV2 is a non-wired transport primitive, not a task registration or
// credential-use grant. It consumes and clears credential.Password on all exits
// and joins owned I/O before its final authority check, including panic cleanup.
func ReadDirectoryV2(ctx context.Context, cfg DirectoryV2Config, credential Credential) (DirectoryV2Observation, error) {
	dialer := &net.Dialer{}
	return readDirectoryV2(ctx, cfg, credential, transport{lookup: net.DefaultResolver.LookupNetIP, dial: dialer.DialContext})
}

func readDirectoryV2(ctx context.Context, cfg DirectoryV2Config, credential Credential, network transport) (DirectoryV2Observation, error) {
	fixed := DirectoryConfig{Mode: cfg.Mode, ServerName: cfg.ServerName, Domain: cfg.Domain,
		DialIP: cfg.DialIP, Roots: cfg.Roots, AllowedNetworks: cfg.AllowedNetworks,
		Limits: cfg.Limits, Authorize: DirectoryReadAuthorization(cfg.Authorize)}
	result, err := readDirectoryProfile(ctx, fixed, credential, network, directoryProfileV2)
	return DirectoryV2Observation{Objects: result.objectsV2, Source: result.Source}, err
}

// Profiles are closed package-private identities, not pluggable serializers or
// arbitrary search definitions. Every selector rejects an unknown identity.
type directoryProfile uint8

const (
	directoryProfileV1 directoryProfile = 1
	directoryProfileV2 directoryProfile = 2
)

func (p directoryProfile) valid() bool { return p == directoryProfileV1 || p == directoryProfileV2 }

func (p directoryProfile) searchPacket(base string, id, size int, cookie []byte) []byte {
	switch p {
	case directoryProfileV1:
		return directorySearchPacket(base, id, size, cookie)
	case directoryProfileV2:
		return directorySearchPacketV2(base, id, size, cookie)
	default:
		return nil
	}
}

func (p directoryProfile) entry(body []byte, base string) (directoryassets.Entry, error) {
	switch p {
	case directoryProfileV1:
		return directoryEntry(body, base)
	case directoryProfileV2:
		return directoryEntryV2(body, base)
	default:
		return directoryassets.Entry{}, failure(CodeInvalidConfig, StageValidate)
	}
}

func directoryProfileError(profile directoryProfile, err error) error {
	if profile == directoryProfileV2 && errors.Is(err, directoryassets.ErrLimit) {
		return failure(CodeDirectoryLimit, StageSearch)
	}
	if profile == directoryProfileV2 && (errors.Is(err, directoryassets.ErrSupplementalLimit) ||
		errors.Is(err, directoryassets.ErrSIDProfile) || errors.Is(err, directoryassets.ErrWhenCreatedProfile)) {
		return failure(CodeUnsupportedProfile, StageSearch)
	}
	return failure(CodeInvalidResponse, StageSearch)
}

type directoryReadResult struct {
	DirectoryObservation
	objectsV2 []directoryassets.StoredObjectV2
}

func (r *directoryReadResult) discard() {
	directoryassets.ClearStoredObjectsV2(r.objectsV2)
	*r = directoryReadResult{}
}

type directoryAccumulator struct {
	v1 *directoryassets.Accumulator
	v2 *directoryassets.AccumulatorV2
}

func newDirectoryAccumulator(profile directoryProfile, limits directoryassets.Limits) (directoryAccumulator, error) {
	var out directoryAccumulator
	var err error
	switch profile {
	case directoryProfileV1:
		out.v1, err = directoryassets.NewAccumulator(limits)
	case directoryProfileV2:
		if limits.MaxRows > 10000 || limits.MaxPages > 100 || limits.MaxBytes > 16<<20 {
			return out, directoryassets.ErrLimits
		}
		out.v2, err = directoryassets.NewAccumulatorV2(limits)
	default:
		err = directoryassets.ErrLimits
	}
	return out, err
}

func (a directoryAccumulator) AddPage(entries []directoryassets.Entry, request, response []byte) error {
	if a.v1 != nil {
		return a.v1.AddPage(entries, request, response)
	}
	return a.v2.AddPage(entries, request, response)
}

func (a directoryAccumulator) Result() (directoryReadResult, error) {
	var out directoryReadResult
	var err error
	if a.v1 != nil {
		out.Objects, err = a.v1.Result()
	} else {
		out.objectsV2, err = a.v2.Result()
	}
	return out, err
}

func (a directoryAccumulator) Discard() {
	if a.v1 != nil {
		a.v1.Discard()
	}
	if a.v2 != nil {
		a.v2.Discard()
	}
}
