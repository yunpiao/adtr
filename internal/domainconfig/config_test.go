package domainconfig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const syntheticPolicy = `{"version":1,"targets":[{"tenantId":"tenant-a","domain":"example.test","serverNames":["dc.example.test"],"cidrs":["10.20.0.0/16","fd12:3456::/48"]}]}`

func syntheticCertificate(t *testing.T, isCA bool) []byte {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic test certificate"},
		NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA: isCA, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if isCA {
		template.KeyUsage |= x509.KeyUsageCertSign
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func syntheticKeyConfig() map[string]string {
	return map[string]string{
		"ADTR_DOMAIN_KEY_ID": "synthetic-key-1",
		"ADTR_DOMAIN_KEY":    base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x54}, 32)),
	}
}

func syntheticProbeConfig(t *testing.T) map[string]string {
	t.Helper()
	env := syntheticKeyConfig()
	dir := t.TempDir()
	env["ADTR_LDAP_CA_FILE"] = filepath.Join(dir, "ca.pem")
	env["ADTR_LDAP_EGRESS_POLICY_FILE"] = filepath.Join(dir, "policy.json")
	env["ADTR_DOMAIN_PROBE_ENABLED"] = "true"
	if err := os.WriteFile(env["ADTR_LDAP_CA_FILE"], syntheticCertificate(t, true), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env["ADTR_LDAP_EGRESS_POLICY_FILE"], []byte(syntheticPolicy), 0600); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestLoadDisabledAndInventoryModes(t *testing.T) {
	for _, env := range []map[string]string{
		{},
		{"ADTR_DOMAIN_PROBE_ENABLED": "false"},
		{"ADTR_AUTH_KEY": "auth key is never a domain fallback"},
	} {
		runtime, err := Load(func(name string) string { return env[name] })
		if err != nil || runtime == nil || runtime.Enabled() || runtime.ProbeEnabled() || runtime.Vault() != nil || runtime.Policy() != nil {
			t.Fatal("absent domain configuration did not disable module", err)
		}
	}
	for _, runtime := range []*Runtime{nil, {}} {
		if runtime.Enabled() || runtime.ProbeEnabled() || runtime.Vault() != nil || runtime.Policy() != nil {
			t.Fatal("zero/nil runtime did not fail closed")
		}
	}
	env := syntheticKeyConfig()
	runtime, err := Load(func(name string) string { return env[name] })
	if err != nil || !runtime.Enabled() || runtime.ProbeEnabled() || runtime.Vault() == nil || runtime.Policy() != nil {
		t.Fatal("inventory-only config failed", err)
	}
	if _, err := Load(nil); !errors.Is(err, ErrConfiguration) {
		t.Fatal("nil environment accessor accepted")
	}
}

func TestLoadRejectsPartialMalformedAndReusedKeys(t *testing.T) {
	validKey := syntheticKeyConfig()["ADTR_DOMAIN_KEY"]
	tests := []struct {
		name string
		edit func(map[string]string)
	}{
		{"key missing", func(e map[string]string) { delete(e, "ADTR_DOMAIN_KEY") }},
		{"ID missing", func(e map[string]string) { delete(e, "ADTR_DOMAIN_KEY_ID") }},
		{"ID invalid", func(e map[string]string) { e["ADTR_DOMAIN_KEY_ID"] = "unsafe:key" }},
		{"ID oversized", func(e map[string]string) { e["ADTR_DOMAIN_KEY_ID"] = strings.Repeat("a", 65) }},
		{"key malformed", func(e map[string]string) { e["ADTR_DOMAIN_KEY"] = "private-key-text-not-base64" }},
		{"key short", func(e map[string]string) { e["ADTR_DOMAIN_KEY"] = base64.StdEncoding.EncodeToString(make([]byte, 31)) }},
		{"key long", func(e map[string]string) { e["ADTR_DOMAIN_KEY"] = base64.StdEncoding.EncodeToString(make([]byte, 33)) }},
		{"key unpadded", func(e map[string]string) { e["ADTR_DOMAIN_KEY"] = strings.TrimSuffix(validKey, "=") }},
		{"key whitespace", func(e map[string]string) { e["ADTR_DOMAIN_KEY"] = validKey + "\n" }},
		{"auth key reused", func(e map[string]string) { e["ADTR_AUTH_KEY"] = validKey }},
		{"auth decoded key reused", func(e map[string]string) { e["ADTR_AUTH_KEY"] = validKey + "\n" }},
		{"malformed auth key", func(e map[string]string) { e["ADTR_AUTH_KEY"] = "!malformed" }},
		{"short auth key", func(e map[string]string) { e["ADTR_AUTH_KEY"] = base64.StdEncoding.EncodeToString(make([]byte, 31)) }},
		{"oversize auth key", func(e map[string]string) { e["ADTR_AUTH_KEY"] = strings.Repeat("a", 129) }},
		{"probe without policy", func(e map[string]string) { e["ADTR_DOMAIN_PROBE_ENABLED"] = "true" }},
		{"CA only", func(e map[string]string) { e["ADTR_LDAP_CA_FILE"] = "/synthetic/private-path" }},
		{"policy only", func(e map[string]string) { e["ADTR_LDAP_EGRESS_POLICY_FILE"] = "/synthetic/private-path" }},
	}
	for _, flag := range []string{"TRUE", "True", "1", "0", "False", " false", "true\n", "yes"} {
		tests = append(tests, struct {
			name string
			edit func(map[string]string)
		}{"probe flag " + flag, func(e map[string]string) { e["ADTR_DOMAIN_PROBE_ENABLED"] = flag }})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := syntheticKeyConfig()
			tc.edit(env)
			runtime, err := Load(func(name string) string { return env[name] })
			if runtime != nil || !errors.Is(err, ErrConfiguration) || err.Error() != "invalid domain security configuration" {
				t.Fatal("malformed configuration accepted or error was not redacted", err)
			}
		})
	}
	env := syntheticKeyConfig()
	env["ADTR_AUTH_KEY"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 32))
	if _, err := Load(func(name string) string { return env[name] }); err != nil {
		t.Fatal("distinct auth and domain keys rejected", err)
	}
}

func TestLoadProbeStartupSnapshot(t *testing.T) {
	env := syntheticProbeConfig(t)
	reads := make(map[string]int)
	runtime, err := Load(func(name string) string {
		reads[name]++
		return env[name]
	})
	if err != nil || !runtime.Enabled() || !runtime.ProbeEnabled() || runtime.Policy() == nil || runtime.Policy().Roots() == nil {
		t.Fatal("valid probe configuration failed", err)
	}
	for name, count := range reads {
		if count != 1 {
			t.Fatalf("%s was read %d times", name, count)
		}
	}
	revision := runtime.Policy().Revision()
	env["ADTR_DOMAIN_PROBE_ENABLED"] = "false"
	env["ADTR_DOMAIN_KEY"] = "changed"
	if err := os.WriteFile(env["ADTR_LDAP_CA_FILE"], []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env["ADTR_LDAP_EGRESS_POLICY_FILE"], []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if !runtime.ProbeEnabled() || runtime.Policy().Revision() != revision {
		t.Fatal("runtime reread startup configuration")
	}
	if _, err := runtime.Policy().AuthorizeTarget("tenant-a", "example.test", "dc.example.test", netip.MustParseAddr("10.20.1.2")); err != nil {
		t.Fatal("startup policy changed", err)
	}
}

func TestLoadPolicyFilesBoundedAndRequired(t *testing.T) {
	for _, target := range []string{"ADTR_LDAP_CA_FILE", "ADTR_LDAP_EGRESS_POLICY_FILE"} {
		for _, mode := range []string{"missing", "empty", "oversize", "directory", "malformed"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				env := syntheticProbeConfig(t)
				var data []byte
				switch mode {
				case "missing":
					env[target] += ".missing-secret-path"
				case "directory":
					env[target] = t.TempDir()
				case "oversize":
					data = make([]byte, maxConfigBytes+1)
				case "malformed":
					data = []byte("private-invalid-payload")
				}
				if mode != "missing" && mode != "directory" {
					if err := os.WriteFile(env[target], data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if runtime, err := Load(func(name string) string { return env[name] }); runtime != nil || !errors.Is(err, ErrConfiguration) || err.Error() != "invalid domain security configuration" {
					t.Fatal("invalid startup file accepted or leaked details", err)
				}
			})
		}
	}
	env := syntheticProbeConfig(t)
	if err := os.WriteFile(env["ADTR_LDAP_EGRESS_POLICY_FILE"], []byte(`{"version":1,"targets":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(func(name string) string { return env[name] }); !errors.Is(err, ErrConfiguration) {
		t.Fatal("probe enabled with an empty allowlist")
	}
	env["ADTR_DOMAIN_PROBE_ENABLED"] = "false"
	if runtime, err := Load(func(name string) string { return env[name] }); err != nil || runtime.ProbeEnabled() {
		t.Fatal("explicitly disabled probe with empty allowlist rejected", err)
	}
	delete(env, "ADTR_DOMAIN_KEY_ID")
	delete(env, "ADTR_DOMAIN_KEY")
	if _, err := Load(func(name string) string { return env[name] }); !errors.Is(err, ErrConfiguration) {
		t.Fatal("egress config accepted without domain key")
	}
}
