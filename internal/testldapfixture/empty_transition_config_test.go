//go:build integration

package main

import "testing"

func TestEmptyTransitionConfigurationModeMatrix(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	cfg, err := readConfiguration(base, fixtureEnvironment)
	if err != nil || cfg.emptyTransitionDir != "" {
		t.Fatal("empty transition must be disabled by default", err)
	}
	flags := []string{"-directory-mode", "-directory-v2", "-directory-v3", "-user-assets-v2",
		"-computer-assets-v3", "-group-assets-v3", "-directory-empty", "-directory-slow"}
	for mask := range 1 << len(flags) {
		for _, control := range []bool{false, true} {
			args := append(append([]string(nil), base...), "-empty-transition-dir", "/private-fixture")
			for i, flag := range flags {
				if mask&(1<<i) != 0 {
					args = append(args, flag)
				}
			}
			if control {
				args = append(args, "-control-dir", "/control")
			}
			want := !control && (mask == 1|4|16 || mask == 1|4|32)
			cfg, err := readConfiguration(args, fixtureEnvironment)
			if (err == nil) != want {
				t.Fatalf("mask=%08b control=%t valid=%t: %v", mask, control, want, err)
			}
			if want && cfg.emptyTransitionDir != "/private-fixture" {
				t.Fatal("transition directory was not retained")
			}
		}
	}
}

func TestEmptyTransitionDisabledOnEveryPlatform(t *testing.T) {
	transition, err := openFixtureEmptyTransition("")
	if err != nil || transition != nil {
		t.Fatal("disabled transition changed fixture startup")
	}
	if !emptyTransitionSupported {
		if _, err := openFixtureEmptyTransition(t.TempDir()); err != errEmptyTransition {
			t.Fatal("unsupported opt-in transition did not fail closed")
		}
	}
}
