package ldapconnection

import (
	"context"
	"crypto/x509"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

// DirectoryReadAuthorization is a separate trusted directory-read authority.
// A connection-test authorization must never be adapted into this capability.
// The callback must respect its context and release all database locks before
// returning; a future task adapter owns live role/domain/account/purpose checks.
type DirectoryReadAuthorization func(context.Context, DirectoryStage) error

type DirectoryStage string

const (
	DirectoryValidate DirectoryStage = "validate"
	DirectoryDNS      DirectoryStage = "dns"
	DirectoryDial     DirectoryStage = "dial"
	// DirectoryTLS covers StartTLS negotiation (when selected) and the verified
	// TLS handshake in the same fixed three-second connection phase.
	DirectoryTLS     DirectoryStage = "tls"
	DirectoryBind    DirectoryStage = "bind"
	DirectoryRootDSE DirectoryStage = "root_dse"
	DirectoryPage    DirectoryStage = "page"
	DirectoryReturn  DirectoryStage = "return"
)

// DirectoryConfig is intentionally distinct from the connection-test Config.
// Domain is an ASCII multi-label DNS domain; no caller controls the LDAP base,
// filter, attributes, port, referrals, plaintext or TLS verification settings.
type DirectoryConfig struct {
	Mode            Mode
	ServerName      string
	Domain          string
	DialIP          netip.Addr
	Roots           *x509.CertPool
	AllowedNetworks []netip.Prefix
	Limits          directoryassets.Limits
	Authorize       DirectoryReadAuthorization
}

// DirectoryObservation is complete only when ReadDirectory succeeds. It is
// neither an AD point-in-time snapshot nor evidence that absent objects were
// deleted. Kind follows structural objectClass inheritance, not physical-device
// identity (computer subclasses can include managed service accounts).
type DirectoryObservation struct {
	Objects []directoryassets.Object `json:"objects"`
	Source  DirectorySource          `json:"source"`
}

type DirectorySource struct {
	ServerName          string    `json:"server_name"`
	DCHostName          string    `json:"dc_host_name"`
	Domain              string    `json:"domain"`
	NamingContext       string    `json:"naming_context"`
	StartedAt           time.Time `json:"started_at"`
	CompletedAt         time.Time `json:"completed_at"`
	ElapsedMilliseconds int64     `json:"elapsed_milliseconds"`
	Pages               int       `json:"pages"`
}

const directoryTimeout = 120 * time.Second

// ReadDirectory is an internal transport primitive, not a product producer or
// a credential-use grant. The caller must supply separately authorized trusted
// directory authority. Password is consumed and cleared, including on failure
// or panic. A caller retaining another credential buffer must clear that too.
// All owned I/O and the cancellation watcher have stopped before return.
func ReadDirectory(ctx context.Context, cfg DirectoryConfig, credential Credential) (DirectoryObservation, error) {
	dialer := &net.Dialer{}
	return readDirectory(ctx, cfg, credential, transport{lookup: net.DefaultResolver.LookupNetIP, dial: dialer.DialContext})
}

func readDirectory(parent context.Context, cfg DirectoryConfig, credential Credential, network transport) (DirectoryObservation, error) {
	result, err := readDirectoryProfile(parent, cfg, credential, network, directoryProfileV1)
	return result.DirectoryObservation, err
}

// Only the two fixed wrappers select a profile. Network, authority, TLS,
// RootDSE, page/cookie and completion semantics remain a single implementation.
func readDirectoryProfile(parent context.Context, cfg DirectoryConfig, credential Credential, network transport, profile directoryProfile) (directoryReadResult, error) {
	defer clear(credential.Password)
	started := time.Now()
	base, validDomain := directoryBaseDN(cfg.Domain)
	accumulator, err := newDirectoryAccumulator(profile, cfg.Limits)
	connection := Config{Mode: cfg.Mode, ServerName: cfg.ServerName, DialIP: cfg.DialIP,
		Roots: cfg.Roots, AllowedNetworks: cfg.AllowedNetworks,
		Authorize: func(ctx context.Context, stage Stage) error {
			return cfg.Authorize(ctx, DirectoryStage(stage))
		}}
	if parent == nil || cfg.Authorize == nil || !validDomain || err != nil || !validConfig(connection, credential) {
		return directoryReadResult{}, failure(CodeInvalidConfig, StageValidate)
	}
	defer accumulator.Discard()
	ctx, cancel := context.WithTimeout(parent, directoryTimeout)
	defer cancel()
	if err := directoryAuthorization(ctx, cfg, DirectoryValidate); err != nil {
		return directoryReadResult{}, err
	}
	var result directoryReadResult
	complete := false
	defer func() {
		if !complete {
			result.discard()
		}
	}()
	err = withBoundConnection(ctx, connection, credential, network, func(ctx context.Context, secured net.Conn) error {
		clear(credential.Password)
		rootCtx, stopRoot := context.WithTimeout(ctx, stepTimeout)
		defer stopRoot()
		if err := directoryAuthorization(rootCtx, cfg, DirectoryRootDSE); err != nil {
			return err
		}
		if err := setStepDeadline(rootCtx, secured); err != nil {
			return classifyIO(rootCtx, err, StageSearch)
		}
		root, err := search(rootCtx, secured)
		if err != nil {
			return err
		}
		stopRoot()
		isAD := false
		for _, capability := range root.SupportedCapabilities {
			isAD = isAD || capability == "1.2.840.113556.1.4.800"
		}
		if !isAD || !directoryDNEqual(root.DefaultNamingContext, base) {
			return failure(CodeInvalidResponse, StageSearch)
		}
		var cookie []byte
		defer func() { clear(cookie) }()
		for page := 0; page < cfg.Limits.MaxPages; page++ {
			entries, next, err := directoryPageProfile(ctx, secured, cfg, base, page+4, cookie, profile)
			if err != nil {
				return err
			}
			err = func() error {
				defer clearDirectoryEntries(entries)
				defer clear(next)
				if err := accumulator.AddPage(entries, cookie, next); err != nil {
					return directoryProfileError(profile, err)
				}
				clear(cookie)
				cookie = append([]byte(nil), next...)
				return nil
			}()
			if err != nil {
				return err
			}
			if len(cookie) == 0 {
				result, err = accumulator.Result()
				if err != nil {
					return failure(CodeInvalidResponse, StageSearch)
				}
				result.Source = DirectorySource{ServerName: cfg.ServerName, DCHostName: root.DCHostName,
					Domain: strings.TrimSuffix(strings.ToLower(cfg.Domain), "."), NamingContext: base,
					StartedAt: started.UTC(), Pages: page + 1}
				return nil
			}
		}
		return directoryProfileError(profile, directoryassets.ErrLimit)
	})
	if err != nil {
		return directoryReadResult{}, err
	}
	// Recheck after connection close/join, immediately before returning data.
	if err := directoryAuthorization(ctx, cfg, DirectoryReturn); err != nil {
		return directoryReadResult{}, err
	}
	result.Source.CompletedAt = time.Now().UTC()
	result.Source.ElapsedMilliseconds = time.Since(started).Milliseconds()
	complete = true
	return result, nil
}

func directoryAuthorization(parent context.Context, cfg DirectoryConfig, stage DirectoryStage) error {
	ctx, cancel := context.WithTimeout(parent, stepTimeout)
	defer cancel()
	errorStage := StageSearch
	if stage == DirectoryValidate {
		errorStage = StageValidate
	}
	if err := ctx.Err(); err != nil {
		return classifyIO(ctx, err, errorStage)
	}
	err := cfg.Authorize(ctx, stage)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return classifyIO(ctx, ctxErr, errorStage)
	}
	if err != nil {
		return failure(CodeAuthorizationRevoked, errorStage)
	}
	return nil
}

func clearDirectoryEntries(entries []directoryassets.Entry) {
	for i := range entries {
		for j := range entries[i].Attributes {
			for _, value := range entries[i].Attributes[j].Values {
				clear(value)
			}
		}
	}
}
