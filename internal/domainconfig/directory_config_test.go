package domainconfig

import (
	"errors"
	"testing"
)

func TestDirectoryReadDeploymentGateIsIndependent(t *testing.T) {
	for _, runtime := range []*Runtime{nil, {}} {
		if runtime.DirectoryReadEnabled() {
			t.Fatal("zero runtime enabled directory reads")
		}
	}
	for _, flags := range []struct {
		probe, directory         string
		wantProbe, wantDirectory bool
	}{{"true", "", true, false}, {"false", "true", false, true}, {"true", "true", true, true}, {"false", "false", false, false}} {
		env := syntheticProbeConfig(t)
		env["ADTR_DOMAIN_PROBE_ENABLED"] = flags.probe
		env["ADTR_DIRECTORY_READ_ENABLED"] = flags.directory
		runtime, err := Load(func(k string) string { return env[k] })
		if err != nil || runtime.ProbeEnabled() != flags.wantProbe || runtime.DirectoryReadEnabled() != flags.wantDirectory {
			t.Fatal("deployment gates coupled", err)
		}
		env["ADTR_DIRECTORY_READ_ENABLED"] = "false"
		if runtime.DirectoryReadEnabled() != flags.wantDirectory {
			t.Fatal("startup snapshot mutated")
		}
	}
}
func TestDirectoryReadRequiresExplicitKeysTrustAndEgress(t *testing.T) {
	for _, value := range []string{"1", "TRUE", "yes", " true", "true "} {
		env := syntheticProbeConfig(t)
		env["ADTR_DIRECTORY_READ_ENABLED"] = value
		if _, err := Load(func(k string) string { return env[k] }); !errors.Is(err, ErrConfiguration) {
			t.Fatal("ambiguous directory flag accepted")
		}
	}
	env := map[string]string{"ADTR_DIRECTORY_READ_ENABLED": "true"}
	if _, err := Load(func(k string) string { return env[k] }); !errors.Is(err, ErrConfiguration) {
		t.Fatal("directory reads enabled without key/trust/egress")
	}
	env = syntheticKeyConfig()
	env["ADTR_DIRECTORY_READ_ENABLED"] = "true"
	if _, err := Load(func(k string) string { return env[k] }); !errors.Is(err, ErrConfiguration) {
		t.Fatal("directory reads enabled without trust/egress")
	}
	env = map[string]string{"ADTR_DIRECTORY_READ_ENABLED": "false"}
	runtime, err := Load(func(k string) string { return env[k] })
	if err != nil || runtime.Enabled() || runtime.DirectoryReadEnabled() {
		t.Fatal("disabled directory-only config failed", err)
	}
}
