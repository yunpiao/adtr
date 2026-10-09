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

func TestDirectoryV2RequiresBothIndependentStartupGates(t *testing.T) {
	for _, runtime := range []*Runtime{nil, {}} {
		if runtime.DirectoryReadV2Enabled() {
			t.Fatal("zero runtime enabled dictionary 2")
		}
	}
	for _, master := range []string{"", "false", "true"} {
		for _, versioned := range []string{"", "false", "true"} {
			env := syntheticProbeConfig(t)
			env["ADTR_DOMAIN_PROBE_ENABLED"] = "false"
			env["ADTR_DIRECTORY_READ_ENABLED"] = master
			env["ADTR_DIRECTORY_READ_V2_ENABLED"] = versioned
			runtime, err := Load(func(k string) string { return env[k] })
			want := master == "true" && versioned == "true"
			if err != nil || runtime.DirectoryReadV2Enabled() != want || runtime.DirectoryReadEnabled() != (master == "true") || runtime.ProbeEnabled() {
				t.Fatalf("deployment gates coupled: master=%q versioned=%q error=%v", master, versioned, err)
			}
			env["ADTR_DIRECTORY_READ_ENABLED"], env["ADTR_DIRECTORY_READ_V2_ENABLED"] = "false", "false"
			if runtime.DirectoryReadV2Enabled() != want {
				t.Fatal("dictionary 2 startup snapshot mutated")
			}
		}
	}
}

func TestDirectoryV2RejectsAmbiguousOrUnsecuredConfiguration(t *testing.T) {
	for _, value := range []string{"1", "TRUE", "yes", " true", "true ", "2"} {
		env := syntheticProbeConfig(t)
		env["ADTR_DIRECTORY_READ_V2_ENABLED"] = value
		if _, err := Load(func(k string) string { return env[k] }); !errors.Is(err, ErrConfiguration) {
			t.Fatal("ambiguous dictionary 2 switch accepted")
		}
	}
	for _, env := range []map[string]string{{}, syntheticKeyConfig()} {
		env["ADTR_DIRECTORY_READ_V2_ENABLED"] = "true"
		if _, err := Load(func(k string) string { return env[k] }); !errors.Is(err, ErrConfiguration) {
			t.Fatal("dictionary 2 configured without key/trust/egress")
		}
	}
	runtime, err := Load(func(k string) string {
		if k == "ADTR_DIRECTORY_READ_V2_ENABLED" {
			return "false"
		}
		return ""
	})
	if err != nil || runtime.Enabled() || runtime.DirectoryReadV2Enabled() {
		t.Fatal("disabled dictionary 2-only configuration failed", err)
	}
}
