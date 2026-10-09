//go:build integration

package main

import (
	"context"
	"io"
	"testing"
)

func fixtureEnvironment(name string) string {
	switch name {
	case "ADTR_LDAP_FIXTURE_USERNAME":
		return testUsername
	case "ADTR_LDAP_FIXTURE_PASSWORD":
		return testPassword
	default:
		return ""
	}
}

func TestConfigurationAcceptsOnlyFixedLDAPPorts(t *testing.T) {
	for _, test := range []struct {
		startTLS, ldaps string
		valid           bool
	}{
		{"127.0.0.1:389", "127.0.0.1:636", true},
		{"0.0.0.0:389", "0.0.0.0:636", true},
		{"[::1]:389", "[::]:636", true},
		{"127.0.0.1:1389", "127.0.0.1:636", false},
		{"127.0.0.1:389", "127.0.0.1:1636", false},
		{"127.0.0.1:636", "127.0.0.1:389", false},
		{"dc.synthetic.invalid:389", "127.0.0.1:636", false},
		{":389", ":636", false},
		{"[fe80::1%eth0]:389", "127.0.0.1:636", false},
		{"224.0.0.1:389", "127.0.0.1:636", false},
	} {
		_, err := readConfiguration([]string{"-cert", "synthetic.crt", "-key", "synthetic.key",
			"-starttls-listen", test.startTLS, "-ldaps-listen", test.ldaps}, fixtureEnvironment)
		if (err == nil) != test.valid {
			t.Fatalf("addresses %s/%s validity=%t: %v", test.startTLS, test.ldaps, test.valid, err)
		}
	}
}

func TestConfigurationRequiresSyntheticCertificateAndCredentials(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-cert", "synthetic.crt"}, {"-key", "synthetic.key"},
		{"-cert", "synthetic.crt", "-key", "synthetic.key", "unexpected"},
		{"-cert", "synthetic.crt", "-key", "synthetic.key", "-password", "not-a-credential-flag"},
	} {
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	args := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	for _, missing := range []string{"ADTR_LDAP_FIXTURE_USERNAME", "ADTR_LDAP_FIXTURE_PASSWORD"} {
		if _, err := readConfiguration(args, func(name string) string {
			if name == missing {
				return ""
			}
			return fixtureEnvironment(name)
		}); err == nil {
			t.Fatalf("missing %s accepted", missing)
		}
	}
	cfg, err := readConfiguration(args, fixtureEnvironment)
	if err != nil || cfg.startTLSAddress != "127.0.0.1:389" || cfg.ldapsAddress != "127.0.0.1:636" {
		t.Fatalf("unexpected defaults: %v", err)
	}
}

func TestCertificateLoadErrorIsFixedAndDoesNotExposePaths(t *testing.T) {
	err := run(context.Background(), []string{"-cert", "/missing/synthetic.crt", "-key", "/missing/synthetic.key"}, fixtureEnvironment, io.Discard)
	if err == nil || err.Error() != "synthetic LDAP fixture certificate unavailable" {
		t.Fatalf("unexpected startup error: %v", err)
	}
}

func TestOptionalControlConfiguration(t *testing.T) {
	args := []string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-control-dir", "/control"}
	cfg, err := readConfiguration(args, fixtureEnvironment)
	if err != nil || cfg.controlDir != "/control" {
		t.Fatalf("control configuration rejected: %v", err)
	}
	if control, err := openFixtureControl(""); err != nil || control != nil {
		t.Fatal("disabled control changed fixture startup")
	}
	if _, err := openFixtureControl("/missing/synthetic-control"); err == nil || err.Error() != "synthetic LDAP fixture control unavailable" {
		t.Fatalf("unexpected control startup error: %v", err)
	}
}

func TestDirectoryConfigurationIsExplicitAndDefaultOff(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	cfg, err := readConfiguration(base, fixtureEnvironment)
	if err != nil || cfg.directoryMode || cfg.directoryV2 || cfg.directoryEmpty || cfg.directorySlow {
		t.Fatal("directory enumeration must be disabled by default")
	}
	cfg, err = readConfiguration(append(append([]string(nil), base...), "-directory-mode"), fixtureEnvironment)
	if err != nil || !cfg.directoryMode {
		t.Fatal("explicit directory mode was not enabled")
	}
	if _, err = readConfiguration(append(append([]string(nil), base...), "-directory-mode=arbitrary"), fixtureEnvironment); err == nil {
		t.Fatal("invalid directory mode accepted")
	}
}

func TestDirectoryV2ConfigurationRequiresExplicitDirectoryMode(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	for _, modifier := range [][]string{
		{"--directory-v2"},
		{"--directory-v2", "--directory-empty"},
		{"--directory-v2", "--directory-slow"},
		{"--directory-v2", "--directory-empty", "--directory-slow"},
	} {
		args := append(append([]string(nil), base...), modifier...)
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatal("dictionary 2 accepted without directory mode")
		}
		cfg, err := readConfiguration(append(args, "--directory-mode"), fixtureEnvironment)
		if err != nil || !cfg.directoryMode || !cfg.directoryV2 {
			t.Fatalf("explicit dictionary 2 rejected: %v", err)
		}
	}
	for _, flag := range []string{"--directory-v2=2", "--directory-v2=arbitrary", "--dictionary-version=2", "--directory-attributes=mail"} {
		args := append(append([]string(nil), base...), "--directory-mode", flag)
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatalf("non-boolean or arbitrary dictionary selection accepted: %s", flag)
		}
	}
	cfg, err := readConfiguration(append(append([]string(nil), base...), "--directory-mode", "--directory-v2=false"), fixtureEnvironment)
	if err != nil || !cfg.directoryMode || cfg.directoryV2 {
		t.Fatal("explicit false changed the legacy dictionary")
	}
}

func TestDirectoryEmptyAndSlowConfigurationRequiresDirectoryMode(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	for _, test := range []struct {
		args        []string
		empty, slow bool
	}{
		{[]string{"-directory-empty"}, true, false},
		{[]string{"-directory-slow"}, false, true},
		{[]string{"-directory-empty", "-directory-slow"}, true, true},
	} {
		args := append(append([]string(nil), base...), test.args...)
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatalf("directory modifiers accepted without directory mode: %v", test.args)
		}
		cfg, err := readConfiguration(append(args, "-directory-mode"), fixtureEnvironment)
		if err != nil || !cfg.directoryMode || cfg.directoryEmpty != test.empty || cfg.directorySlow != test.slow {
			t.Fatalf("explicit directory modifiers rejected: %v: %v", test.args, err)
		}
	}
	for _, flag := range []string{"-directory-empty=1s", "-directory-slow=2s", "-directory-delay=2s", "-directory-pages=5", "-directory-filter=arbitrary"} {
		args := append(append([]string(nil), base...), "-directory-mode", flag)
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatalf("arbitrary directory input accepted: %s", flag)
		}
	}
}
