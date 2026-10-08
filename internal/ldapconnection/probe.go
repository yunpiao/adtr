package ldapconnection

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	totalTimeout = 10 * time.Second
	dialTimeout  = 2 * time.Second
	stepTimeout  = 3 * time.Second
)

// Hooks are private and passed by value, so tests can exercise the wire protocol
// without a listener or changing any production port/security configuration.
type transport struct {
	lookup func(context.Context, string, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

// Probe uses one connection, verified TLS, one simple bind and one base-object
// RootDSE search. It never retries an address/operation or follows a referral.
func Probe(ctx context.Context, cfg Config, credential Credential) (Result, error) {
	dialer := &net.Dialer{}
	return probe(ctx, cfg, credential, transport{
		lookup: net.DefaultResolver.LookupNetIP,
		dial:   dialer.DialContext,
	})
}

func probe(parent context.Context, cfg Config, credential Credential, network transport) (Result, error) {
	started := time.Now()
	if parent == nil || !validConfig(cfg, credential) {
		return Result{}, failure(CodeInvalidConfig, StageValidate)
	}
	ctx, cancel := context.WithTimeout(parent, totalTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, classifyIO(ctx, err, StageDial)
	}
	var result Result
	err := withBoundConnection(ctx, cfg, credential, network, func(ctx context.Context, secured net.Conn) error {
		searchCtx, stopSearch := context.WithTimeout(ctx, stepTimeout)
		defer stopSearch()
		if err := authorize(searchCtx, cfg, StageSearch); err != nil {
			return err
		}
		if err := setStepDeadline(searchCtx, secured); err != nil {
			return classifyIO(searchCtx, err, StageSearch)
		}
		var err error
		result, err = search(searchCtx, secured)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return classifyIO(ctx, err, StageSearch)
		}
		result.ElapsedMilliseconds = time.Since(started).Milliseconds()
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func validConfig(cfg Config, credential Credential) bool {
	// A nil RootCAs would silently select the host system trust store. This
	// adapter requires the domain's explicitly supplied, nonempty CA bundle.
	if cfg.Roots == nil || len(cfg.Roots.Subjects()) == 0 || cfg.Authorize == nil ||
		len(cfg.AllowedNetworks) == 0 || len(cfg.AllowedNetworks) > 64 {
		return false
	}
	for _, prefix := range cfg.AllowedNetworks {
		if !prefix.IsValid() || prefix.Addr().Is4In6() || prefix != prefix.Masked() {
			return false
		}
	}
	return (cfg.Mode == StartTLS || cfg.Mode == LDAPS) && validDNSName(cfg.ServerName) &&
		len(credential.Username) > 0 && len(credential.Username) <= 1024 &&
		utf8.ValidString(credential.Username) && !strings.ContainsRune(credential.Username, 0) &&
		len(credential.Password) > 0 && len(credential.Password) <= 4096
}

func validAddress(address netip.Addr) bool {
	if address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	if address == netip.MustParseAddr("168.63.129.16") || address == netip.MustParseAddr("100.100.100.200") || address == netip.MustParseAddr("fd00:ec2::254") || netip.MustParsePrefix("0.0.0.0/8").Contains(address) || netip.MustParsePrefix("240.0.0.0/4").Contains(address) {
		return false
	}
	return address.IsValid() && !address.IsUnspecified() && !address.IsMulticast() &&
		!address.IsLinkLocalUnicast()
}

func allowedAddress(prefixes []netip.Prefix, address netip.Addr) bool {
	if !validAddress(address) {
		return false
	}
	address = address.Unmap()
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func authorize(ctx context.Context, cfg Config, stage Stage) error {
	if err := ctx.Err(); err != nil {
		return classifyIO(ctx, err, stage)
	}
	err := cfg.Authorize(ctx, stage)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return classifyIO(ctx, ctxErr, stage)
	}
	if err != nil {
		return failure(CodeAuthorizationRevoked, stage)
	}
	return nil
}

func validDNSName(name string) bool {
	name = strings.TrimSuffix(name, ".")
	if len(name) == 0 || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	return true
}

func setStepDeadline(ctx context.Context, conn net.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(stepTimeout)
	if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
		deadline = outer
	}
	return conn.SetDeadline(deadline)
}

func classifyIO(ctx context.Context, err error, stage Stage) *Error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return failure(CodeCancelled, stage)
	}
	var netErr net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		if stage == StageBind || stage == StageSearch || stage == StageStartTLS {
			return failure(CodeLDAPTimeout, stage)
		}
		return failure(CodeConnectionTimeout, stage)
	}
	if stage == StageDNS {
		return failure(CodeDNSFailed, stage)
	}
	if stage == StageDial {
		return failure(CodeConnectFailed, stage)
	}
	if stage == StageTLS {
		return failure(CodeTLSFailed, stage)
	}
	return failure(CodeInvalidResponse, stage)
}

func classifyTLS(ctx context.Context, err error) *Error {
	classified := classifyIO(ctx, err, StageTLS)
	if classified.Code != CodeTLSFailed {
		return classified
	}
	var hostname x509.HostnameError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &hostname):
		return failure(CodeTLSHostname, StageTLS)
	case errors.As(err, &unknown):
		return failure(CodeTLSUntrusted, StageTLS)
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return failure(CodeTLSExpired, StageTLS)
		}
		return failure(CodeTLSUntrusted, StageTLS)
	default:
		return classified
	}
}

func validOID(value string) bool {
	if len(value) == 0 || len(value) > maxOIDBytes || !strings.Contains(value, ".") {
		return false
	}
	for _, component := range strings.Split(value, ".") {
		if component == "" || (len(component) > 1 && component[0] == '0') {
			return false
		}
		for _, c := range component {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func validVersion(value string) bool {
	version, err := strconv.Atoi(value)
	return len(value) <= 8 && err == nil && version > 0 && strconv.Itoa(version) == value
}
