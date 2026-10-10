//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func TestUserAssetsV2ConfigurationIsExplicitAndIsolated(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	for _, modes := range [][]string{
		{"-user-assets-v2"},
		{"-directory-mode", "-user-assets-v2"},
		{"-directory-mode", "-directory-v2", "-user-assets-v2", "-directory-empty"},
		{"-directory-mode", "-directory-v2", "-user-assets-v2", "-directory-slow"},
	} {
		if _, err := readConfiguration(append(append([]string(nil), base...), modes...), fixtureEnvironment); err == nil {
			t.Fatalf("invalid isolated user fixture accepted: %v", modes)
		}
	}
	cfg, err := readConfiguration(append(base, "-directory-mode", "-directory-v2", "-user-assets-v2"), fixtureEnvironment)
	if err != nil || !cfg.directoryMode || !cfg.directoryV2 || !cfg.userAssetsV2 || cfg.directoryEmpty || cfg.directorySlow {
		t.Fatalf("explicit user fixture rejected: %v", err)
	}
}

func TestUserAssetsV2ActualReaderTLSFixturePreservesRawProjection(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		t.Run(string(mode), func(t *testing.T) {
			fixture, clientTLS := testServer(t)
			fixture.directoryMode, fixture.directoryV2, fixture.userAssetsV2 = true, true, true
			result, err, done := readV2ModeFixture(context.Background(), fixture, clientTLS, mode,
				ldapconnection.Credential{Username: testUsername, Password: []byte(testPassword)},
				func(context.Context, ldapconnection.DirectoryStage) error { return nil }, nil)
			if err != nil {
				t.Fatalf("actual TLS reader failed: %v", err)
			}
			defer result.Discard()
			if len(result.Objects) != 30 || result.Source.Pages != 2 {
				t.Fatal("fixture lost the actual two-page thirty-row chain")
			}
			counts := map[directoryassets.Kind]int{}
			for _, stored := range result.Objects {
				public, err := directoryassets.ProjectV2(stored)
				if err != nil {
					t.Fatal(err)
				}
				counts[public.Kind]++
				canonical, err := directoryassets.EncodeStoredObjectV2(stored)
				if err != nil {
					t.Fatal(err)
				}
				roundTrip, err := directoryassets.DecodeStoredObjectV2(canonical)
				if err != nil || !reflect.DeepEqual(stored, roundTrip) {
					t.Fatal("raw fixture lost canonical round trip")
				}
			}
			if counts["user"] != 12 || counts["group"] != 12 || counts["computer"] != 6 {
				t.Fatalf("wrong class-derived counts: %v", counts)
			}
			hostile, _ := directoryassets.ProjectV2(result.Objects[0])
			if hostile.UserAccountControl == nil || *hostile.UserAccountControl != 0 || hostile.WhenCreated == nil || *hostile.WhenCreated != "0001-01-01T00:00:00Z" ||
				hostile.ObjectSID == nil || *hostile.ObjectSID != "S-1-5-21-1-2-3-1001" || hostile.Mail == nil || *hostile.Mail != " Mixed+%_&*?'\"\\()[]@example.test \x00\n\t😀\u202e" ||
				!reflect.DeepEqual(hostile.Description, []string{"<img src=x onerror=alert(1)>\x00\n\t😀\u202e"}) {
				t.Fatal("hostile/null boundary values changed")
			}
			null, _ := directoryassets.ProjectV2(result.Objects[11])
			if null.Kind != "user" || null.SAMAccountName != nil || null.UserAccountControl != nil || null.ObjectSID != nil || null.Mail != nil || null.Description != nil || null.WhenCreated != nil {
				t.Fatal("twelfth user must have six factual nulls")
			}
			encoded, _ := json.Marshal(null)
			if bytes.Count(encoded, []byte(":null")) != 6 {
				t.Fatal("null values were omitted or defaulted")
			}
			late, _ := directoryassets.ProjectV2(result.Objects[10])
			if late.SAMAccountName == nil || *late.SAMAccountName != "user-11" || late.Mail == nil || *late.Mail != "row-11@example.test" || late.ObjectSID == nil || *late.ObjectSID != "S-1-5-21-1-2-3-1011" {
				t.Fatal("post-first-page search witnesses changed")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("fixture did not join")
			}
		})
	}
}
