// Package domainconfig owns immutable startup security configuration for domain
// credentials and LDAP egress. It never resolves hosts or opens LDAP connections.
package domainconfig

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"os"
)

const maxConfigBytes = 1 << 20

var (
	ErrConfiguration  = errors.New("invalid domain security configuration")
	ErrUnavailable    = errors.New("domain security unavailable")
	ErrKeyUnavailable = errors.New("domain credential key unavailable")
	ErrCredential     = errors.New("invalid domain credential")
	ErrTargetDenied   = errors.New("domain target denied")
)

// Runtime is a startup snapshot. Its fields and the objects it owns cannot be
// changed through this package's public API. Zero and nil values are disabled.
type Runtime struct {
	vault                  *Vault
	policy                 *Policy
	probeEnabled           bool
	directoryReadEnabled   bool
	directoryReadV2Enabled bool
}

func (r *Runtime) Enabled() bool      { return r != nil && r.vault != nil }
func (r *Runtime) ProbeEnabled() bool { return r.Enabled() && r.probeEnabled }

// DirectoryReadEnabled is a separate deployment gate, never implied by probes.
// An enabled deployment still needs an explicit live directory-purpose grant.
func (r *Runtime) DirectoryReadEnabled() bool { return r.Enabled() && r.directoryReadEnabled }

// DirectoryReadV2Enabled requires both immutable deployment switches. Neither
// this compiled capability nor either flag supplies a credential-purpose grant.
func (r *Runtime) DirectoryReadV2Enabled() bool {
	return r.DirectoryReadEnabled() && r.directoryReadV2Enabled
}
func (r *Runtime) Vault() *Vault {
	if r == nil {
		return nil
	}
	return r.vault
}
func (r *Runtime) Policy() *Policy {
	if r == nil {
		return nil
	}
	return r.policy
}
func (Runtime) String() string   { return "[domain security configuration]" }
func (Runtime) GoString() string { return "[domain security configuration]" }

// Load reads each relevant setting once. A separate domain key enables storage;
// outbound probes require an explicit true flag, CA bundle and egress policy.
// The domain key is never generated and never inherited from ADTR_AUTH_KEY.
func Load(getenv func(string) string) (*Runtime, error) {
	if getenv == nil {
		return nil, ErrConfiguration
	}
	keyID := getenv("ADTR_DOMAIN_KEY_ID")
	encodedKey := getenv("ADTR_DOMAIN_KEY")
	probeFlag := getenv("ADTR_DOMAIN_PROBE_ENABLED")
	directoryFlag := getenv("ADTR_DIRECTORY_READ_ENABLED")
	directoryV2Flag := getenv("ADTR_DIRECTORY_READ_V2_ENABLED")
	caPath := getenv("ADTR_LDAP_CA_FILE")
	policyPath := getenv("ADTR_LDAP_EGRESS_POLICY_FILE")
	if probeFlag != "" && probeFlag != "true" && probeFlag != "false" {
		return nil, ErrConfiguration
	}
	if directoryFlag != "" && directoryFlag != "true" && directoryFlag != "false" {
		return nil, ErrConfiguration
	}
	if directoryV2Flag != "" && directoryV2Flag != "true" && directoryV2Flag != "false" {
		return nil, ErrConfiguration
	}
	if keyID == "" && encodedKey == "" && caPath == "" && policyPath == "" && probeFlag != "true" && directoryFlag != "true" && directoryV2Flag != "true" {
		return &Runtime{}, nil
	}
	if !validToken(keyID, 64) || len(encodedKey) != base64.StdEncoding.EncodedLen(32) || (caPath == "") != (policyPath == "") {
		return nil, ErrConfiguration
	}
	// Re-encoding rejects whitespace, noncanonical pad bits and other aliases.
	key, err := base64.StdEncoding.Strict().DecodeString(encodedKey)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != encodedKey {
		clear(key)
		return nil, ErrConfiguration
	}
	defer clear(key)
	if authEncoded := getenv("ADTR_AUTH_KEY"); authEncoded != "" {
		if len(authEncoded) > 128 {
			return nil, ErrConfiguration
		}
		authKey, decodeErr := base64.StdEncoding.DecodeString(authEncoded)
		same := subtle.ConstantTimeCompare(key, authKey) == 1
		validLength := len(authKey) == 32
		clear(authKey)
		if decodeErr != nil || !validLength || same {
			return nil, ErrConfiguration
		}
	}
	vault, err := newVault(keyID, key)
	if err != nil {
		return nil, ErrConfiguration
	}
	runtime := &Runtime{vault: vault}
	if caPath != "" {
		ca, err := readConfigFile(caPath)
		if err != nil {
			return nil, ErrConfiguration
		}
		policy, err := readConfigFile(policyPath)
		if err != nil {
			return nil, ErrConfiguration
		}
		runtime.policy, err = loadPolicy(policy, ca)
		if err != nil {
			return nil, ErrConfiguration
		}
	}
	if probeFlag == "true" || directoryFlag == "true" || directoryV2Flag == "true" {
		if runtime.policy == nil || len(runtime.policy.targets) == 0 {
			return nil, ErrConfiguration
		}
		runtime.probeEnabled = probeFlag == "true"
		runtime.directoryReadEnabled = directoryFlag == "true"
		runtime.directoryReadV2Enabled = directoryV2Flag == "true"
	}
	return runtime, nil
}

func readConfigFile(path string) ([]byte, error) {
	// Opening only regular startup files avoids unbounded reads from devices or
	// FIFOs. Symlinks to ordinary read-only secret mounts remain supported.
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxConfigBytes {
		return nil, ErrConfiguration
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrConfiguration
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfigBytes {
		return nil, ErrConfiguration
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxConfigBytes {
		return nil, ErrConfiguration
	}
	return data, nil
}

func validToken(value string, max int) bool {
	if len(value) < 1 || len(value) > max {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
