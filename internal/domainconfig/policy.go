package domainconfig

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/netip"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxTargets     = 1000
	maxTargetNames = 32
	maxTargetCIDRs = 32
	maxJSONNesting = 16
)

type policyKey struct{ tenantID, domain string }
type targetRule struct {
	serverNames map[string]struct{}
	prefixes    []netip.Prefix
}

// Policy is a startup-only allowlist and trust-root snapshot. It has no mutation
// or resolver API. Requests cannot provide policy documents or additional CAs.
type Policy struct {
	targets  map[policyKey]targetRule
	roots    *x509.CertPool
	revision string
}

func (Policy) String() string   { return "[domain egress policy]" }
func (Policy) GoString() string { return "[domain egress policy]" }
func (p *Policy) Revision() string {
	if p == nil {
		return ""
	}
	return p.revision
}

// Roots returns a clone so modifying a probe's pool cannot change startup trust.
func (p *Policy) Roots() *x509.CertPool {
	if p == nil || p.roots == nil {
		return nil
	}
	return p.roots.Clone()
}

// AuthorizeTarget requires the exact normalized tenant/domain/server tuple. A
// supplied address must also be permitted. With no address, the adapter must
// resolve once, reject prohibited addresses, and enforce the returned prefixes
// against every resolved address before dialing its pinned result. No hostname
// lookup is performed here. The returned slice is owned by the caller.
func (p *Policy) AuthorizeTarget(tenantID, domain, serverName string, dialIP netip.Addr) ([]netip.Prefix, error) {
	if p == nil || !validIdentity(tenantID) {
		return nil, ErrTargetDenied
	}
	domain, ok := normalizeDNS(domain)
	if !ok {
		return nil, ErrTargetDenied
	}
	serverName, ok = normalizeDNS(serverName)
	if !ok {
		return nil, ErrTargetDenied
	}
	rule, ok := p.targets[policyKey{tenantID, domain}]
	if !ok {
		return nil, ErrTargetDenied
	}
	if _, ok := rule.serverNames[serverName]; !ok {
		return nil, ErrTargetDenied
	}
	if dialIP.IsValid() {
		if dialIP.Zone() != "" {
			return nil, ErrTargetDenied
		}
		dialIP = dialIP.Unmap()
		if !safeAddress(dialIP) {
			return nil, ErrTargetDenied
		}
		allowed := false
		for _, prefix := range rule.prefixes {
			if prefix.Contains(dialIP) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, ErrTargetDenied
		}
	}
	return append([]netip.Prefix(nil), rule.prefixes...), nil
}

type wirePolicy struct {
	Version int          `json:"version"`
	Targets []wireTarget `json:"targets"`
}
type wireTarget struct {
	TenantID    string   `json:"tenantId"`
	Domain      string   `json:"domain"`
	ServerNames []string `json:"serverNames"`
	CIDRs       []string `json:"cidrs"`
}

func loadPolicy(data, ca []byte) (*Policy, error) {
	if len(data) == 0 || len(data) > maxConfigBytes || !utf8.Valid(data) || len(ca) == 0 || len(ca) > maxConfigBytes || !uniqueJSONKeys(data) {
		return nil, ErrConfiguration
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || !exactFields(root, "version", "targets") {
		return nil, ErrConfiguration
	}
	var version int
	var rawTargets []json.RawMessage
	if json.Unmarshal(root["version"], &version) != nil || version != 1 || json.Unmarshal(root["targets"], &rawTargets) != nil || bytes.Equal(bytes.TrimSpace(root["targets"]), []byte("null")) || len(rawTargets) > maxTargets {
		return nil, ErrConfiguration
	}
	policy := &Policy{targets: make(map[policyKey]targetRule)}
	canonical := wirePolicy{Version: 1, Targets: make([]wireTarget, 0, len(rawTargets))}
	for _, raw := range rawTargets {
		var fields map[string]json.RawMessage
		var target wireTarget
		if json.Unmarshal(raw, &fields) != nil || !exactFields(fields, "tenantId", "domain", "serverNames", "cidrs") || json.Unmarshal(raw, &target) != nil || !validIdentity(target.TenantID) {
			return nil, ErrConfiguration
		}
		domain, ok := normalizeDNS(target.Domain)
		if !ok || len(target.ServerNames) == 0 || len(target.ServerNames) > maxTargetNames || len(target.CIDRs) == 0 || len(target.CIDRs) > maxTargetCIDRs {
			return nil, ErrConfiguration
		}
		target.Domain = domain
		key := policyKey{target.TenantID, domain}
		if _, duplicate := policy.targets[key]; duplicate {
			return nil, ErrConfiguration
		}
		rule := targetRule{serverNames: make(map[string]struct{}), prefixes: make([]netip.Prefix, 0, len(target.CIDRs))}
		for i, name := range target.ServerNames {
			name, ok = normalizeDNS(name)
			if !ok {
				return nil, ErrConfiguration
			}
			if _, duplicate := rule.serverNames[name]; duplicate {
				return nil, ErrConfiguration
			}
			rule.serverNames[name] = struct{}{}
			target.ServerNames[i] = name
		}
		seenCIDRs := make(map[netip.Prefix]struct{})
		for i, value := range target.CIDRs {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix != prefix.Masked() || !safePrefix(prefix) {
				return nil, ErrConfiguration
			}
			if _, duplicate := seenCIDRs[prefix]; duplicate {
				return nil, ErrConfiguration
			}
			seenCIDRs[prefix] = struct{}{}
			rule.prefixes = append(rule.prefixes, prefix)
			target.CIDRs[i] = prefix.String()
		}
		sort.Strings(target.ServerNames)
		sort.Strings(target.CIDRs)
		policy.targets[key] = rule
		canonical.Targets = append(canonical.Targets, target)
	}
	sort.Slice(canonical.Targets, func(i, j int) bool {
		a, b := canonical.Targets[i], canonical.Targets[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		return a.Domain < b.Domain
	})
	roots, err := parseRoots(ca)
	if err != nil {
		return nil, ErrConfiguration
	}
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return nil, ErrConfiguration
	}
	hash := sha256.New()
	hash.Write([]byte("ADTR/domain-egress-policy/v1"))
	for _, part := range [][]byte{canonicalJSON, ca} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		hash.Write(length[:])
		hash.Write(part)
	}
	policy.roots = roots
	policy.revision = hex.EncodeToString(hash.Sum(nil))
	return policy, nil
}

func parseRoots(data []byte) (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	seen := make(map[[sha256.Size]byte]struct{})
	for len(bytes.TrimSpace(data)) > 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, ErrConfiguration
		}
		endMarker := []byte("-----END CERTIFICATE-----")
		end := bytes.Index(data, endMarker)
		if end < 0 {
			return nil, ErrConfiguration
		}
		end += len(endMarker)
		encoded := data[:end]
		if bytes.Count(encoded, []byte("-----BEGIN ")) != 1 {
			return nil, ErrConfiguration
		}
		block, rest := pem.Decode(encoded)
		if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrConfiguration
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, ErrConfiguration
		}
		digest := sha256.Sum256(cert.Raw)
		if _, duplicate := seen[digest]; duplicate {
			return nil, ErrConfiguration
		}
		seen[digest] = struct{}{}
		roots.AddCert(cert)
		data = data[end:]
	}
	if len(seen) == 0 {
		return nil, ErrConfiguration
	}
	return roots, nil
}

func exactFields(fields map[string]json.RawMessage, names ...string) bool {
	if len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	return true
}

// encoding/json permits duplicate keys; reject those before decoding the schema,
// including aliases written with JSON Unicode escapes and nested duplicates.
func uniqueJSONKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !consumeJSONValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func consumeJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > maxJSONNesting {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return false
			}
			if _, duplicate := seen[key]; duplicate {
				return false
			}
			seen[key] = struct{}{}
			if !consumeJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !consumeJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func normalizeDNS(value string) (string, bool) {
	// DNS policy uses ASCII wire names. Reject Unicode before lowercasing so
	// compatibility characters cannot silently turn into an allowed ASCII name.
	for _, c := range []byte(value) {
		if c > 127 {
			return "", false
		}
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if len(value) < 1 || len(value) > 253 || !strings.Contains(value, ".") {
		return "", false
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return "", false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	return value, true
}

var prohibitedPrefixes = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("::ffff:0:0/96"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func safeAddress(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Is4In6() || addr.Zone() != "" || !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range prohibitedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func safePrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
		return false
	}
	for _, prohibited := range prohibitedPrefixes {
		if prefix.Overlaps(prohibited) {
			return false
		}
	}
	return true
}
