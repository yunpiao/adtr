package domainconfig

import (
	"errors"
	"testing"
)

func TestDirectoryV3RequiresBothIndependentStartupGates(t *testing.T) {
	for _, runtime := range []*Runtime{nil, {}} {
		if runtime.DirectoryReadV3Enabled() {
			t.Fatal("zero runtime enabled dictionary 3")
		}
	}
	for _, master := range []string{"", "false", "true"} {
		for _, versioned := range []string{"", "false", "true"} {
			env := syntheticProbeConfig(t)
			env["ADTR_DOMAIN_PROBE_ENABLED"] = "false"
			env["ADTR_DIRECTORY_READ_ENABLED"] = master
			env["ADTR_DIRECTORY_READ_V3_ENABLED"] = versioned
			runtime, err := Load(func(k string) string { return env[k] })
			want := master == "true" && versioned == "true"
			if err != nil || runtime.DirectoryReadV3Enabled() != want || runtime.DirectoryReadEnabled() != (master == "true") || runtime.ProbeEnabled() {
				t.Fatalf("deployment gates coupled: master=%q versioned=%q error=%v", master, versioned, err)
			}
			env["ADTR_DIRECTORY_READ_ENABLED"], env["ADTR_DIRECTORY_READ_V3_ENABLED"] = "false", "false"
			if runtime.DirectoryReadV3Enabled() != want {
				t.Fatal("dictionary 3 startup snapshot mutated")
			}
		}
	}
}

func TestDirectoryV3RejectsAmbiguousOrUnsecuredConfiguration(t *testing.T) {
	for _, value := range []string{"1", "TRUE", "yes", " true", "true ", "2"} {
		env := syntheticProbeConfig(t)
		env["ADTR_DIRECTORY_READ_V3_ENABLED"] = value
		if _, err := Load(func(k string) string { return env[k] }); !errors.Is(err, ErrConfiguration) {
			t.Fatal("ambiguous dictionary 3 switch accepted")
		}
	}
	for _, env := range []map[string]string{{}, syntheticKeyConfig()} {
		env["ADTR_DIRECTORY_READ_V3_ENABLED"] = "true"
		if _, err := Load(func(k string) string { return env[k] }); !errors.Is(err, ErrConfiguration) {
			t.Fatal("dictionary 3 configured without key/trust/egress")
		}
	}
	runtime, err := Load(func(k string) string {
		if k == "ADTR_DIRECTORY_READ_V3_ENABLED" {
			return "false"
		}
		return ""
	})
	if err != nil || runtime.Enabled() || runtime.DirectoryReadV3Enabled() {
		t.Fatal("disabled dictionary 3-only configuration failed", err)
	}
}
