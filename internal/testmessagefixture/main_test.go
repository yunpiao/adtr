//go:build integration

package main

import (
	"os"
	"testing"
)

func TestFixtureRefusesUnownedOrNonLocalDatabase(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	os.Args = []string{"fixture", "seed"}
	t.Setenv("ADTR_E2E_MESSAGE_FIXTURE", "")
	if run() == nil {
		t.Fatal("unowned fixture accepted")
	}
	t.Setenv("ADTR_E2E_MESSAGE_FIXTURE", "owned-isolated-health")
	for _, dsn := range []string{"postgres://postgres@127.0.0.1/production", "postgres://postgres@remote/adtr_e2e"} {
		t.Setenv("ADTR_DATABASE_URL", dsn)
		if run() == nil {
			t.Fatal("non-isolated database accepted")
		}
	}
}
