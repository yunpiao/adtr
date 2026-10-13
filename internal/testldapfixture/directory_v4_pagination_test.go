//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"reflect"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestGroupMembersV4PaginationConfiguration(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-group-members-v4-pagination"}
	valid := append(append([]string{}, base...), "-directory-mode", "-directory-v4")
	cfg, err := readConfiguration(valid, fixtureEnvironment)
	if err != nil || !cfg.groupMembersV4Pagination {
		t.Fatal("valid pagination mode", err)
	}
	for _, extra := range [][]string{{"-directory-empty"}, {"-directory-slow"}, {"-directory-v2"}, {"-directory-v3"}, {"-user-assets-v2"}, {"-computer-assets-v3"}, {"-group-assets-v3"}, {"-control-dir", "/synthetic"}, {"-empty-transition-dir", "/synthetic"}} {
		if _, err := readConfiguration(append(append([]string{}, valid...), extra...), fixtureEnvironment); err == nil {
			t.Fatal("accepted incompatible pagination mode", extra)
		}
	}
	for _, args := range [][]string{base, append(append([]string{}, base...), "-directory-mode"), append(append([]string{}, base...), "-directory-v4")} {
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatal("accepted missing directory/v4", args)
		}
	}
}

// In-memory BER transport only: this test opens no network listeners.
func TestGroupMembersV4PaginationTwoPagesAndRawBytes(t *testing.T) {
	d := directorySession{v4: true, groupMembersV4Pagination: true}
	if d.pages() != 2 || d.objects() != 30 {
		t.Fatal("fixture count changed")
	}
	var rows []directoryassets.StoredObjectV4
	for page := 0; page < 2; page++ {
		if !d.matches(request{body: testDirectoryV4Body(), controls: pagingRequestControl(d.cookie)}) {
			t.Fatal("exact eleven-attribute request rejected")
		}
		if d.matches(request{body: directoryV3SearchBody, controls: pagingRequestControl(d.cookie)}) {
			t.Fatal("old dictionary request accepted")
		}
		var out bytes.Buffer
		if err := d.writePage(context.Background(), &out, 4); err != nil {
			t.Fatal(err)
		}
		objects, cookie := receiveDirectoryV4Page(t, &out, 4)
		rows = append(rows, objects...)
		if (page == 0) != (len(cookie) > 0) {
			t.Fatal("continuation boundary")
		}
	}
	if len(rows) != 30 {
		t.Fatal("total")
	}
	for i, o := range rows {
		n := i + 1
		p, err := directoryassets.ProjectV4(o)
		if err != nil {
			t.Fatal(err)
		}
		hex := fmt.Sprintf("%02x", n)
		guid := fmt.Sprintf("%s-%s-%s-%s-%s", repeat(hex, 4), repeat(hex, 2), repeat(hex, 2), repeat(hex, 2), repeat(hex, 6))
		if p.GUID != guid {
			t.Fatal("GUID order", n, p.GUID)
		}
		wantKind := directoryassets.Group
		if n > 26 {
			wantKind = directoryassets.User
		}
		if n > 28 {
			wantKind = directoryassets.Computer
		}
		if p.Kind != wantKind {
			t.Fatal("non-group filtering witness", n)
		}
		var want []string
		switch {
		case n == 1 || n > 26:
		case n == 2:
			want = []string{}
		case n == 26:
			want = []string{"CN=page-two-only,OU=Users,DC=synthetic,DC=invalid", "CN=raw\x00\n😀\u202e<script>,DC=synthetic,DC=invalid", "\\u0000", "CN=page-two-last,OU=Users,DC=synthetic,DC=invalid"}
		default:
			want = []string{fmt.Sprintf("CN=page-member-%02d,OU=Users,DC=synthetic,DC=invalid", n)}
		}
		if !reflect.DeepEqual(p.RawMember, want) {
			t.Fatal("member values/order/presence", n)
		}
		for j, v := range want {
			if !bytes.Equal(o.Membership.MemberBytes[j], []byte(v)) {
				t.Fatal("raw bytes", n, j)
			}
		}
		if n == 26 && base64.StdEncoding.EncodeToString(o.Membership.MemberBytes[2]) != "XHUwMDAw" {
			t.Fatal("literal backslash witness")
		}
		if want == nil {
			if p.ReturnedMemberCount != nil || p.MemberReadState != "not_returned" {
				t.Fatal("not-returned semantics")
			}
		} else if p.ReturnedMemberCount == nil || *p.ReturnedMemberCount != len(want) || p.MemberReadState != "returned" {
			t.Fatal("returned semantics")
		}
	}
	if d.matches(request{body: testDirectoryV4Body(), controls: pagingRequestControl(nil)}) {
		t.Fatal("terminal page accepted replay")
	}
}
func repeat(s string, n int) string { return string(bytes.Repeat([]byte(s), n)) }

// Recorded against unmodified main 5d136e00169e799686c59dbcbe7a427684b96eaf.
// Pin the complete 30 BER entries (message ID 4), including attribute presence,
// original raw bytes and order; the opt-in dataset must not alter old modes.
func TestGroupMembersV4PaginationPreservesOldFixtureBytes(t *testing.T) {
	for _, test := range []struct {
		name   string
		entry  func(int, int) []byte
		digest string
	}{
		{"v1", directoryEntry, "58a9232d3b542f50b129cd6cd9c3ac7468a6f931c5399c666ed02d2f58af0c59"},
		{"v2", func(id, row int) []byte { return directoryEntryForDictionary(id, row, true) }, "51eaa47f885a1a9e91c1c516b8643260b60ddcb39676e3d5f88ac521e3094660"},
		{"v3", directoryV3Entry, "8f15b9207539b8b48d1d460c7ac1f9279a5ac202489fadf1b171359a6d535955"},
		{"v4", directoryV4Entry, "c071bab4b47746dad6be50fcc41c007dd7aa377f06bd014868dade18ded8f94f"},
	} {
		t.Run(test.name, func(t *testing.T) {
			hash := sha256.New()
			for row := 1; row <= 30; row++ {
				hash.Write(test.entry(4, row))
			}
			if fmt.Sprintf("%x", hash.Sum(nil)) != test.digest {
				t.Fatal("legacy BER bytes changed")
			}
		})
	}
}
