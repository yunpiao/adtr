//go:build integration

package store

import (
	"encoding/json"
	"fmt"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"strings"
	"testing"
)

func TestSchema20MembershipGuardMatchesGoRawByteLimits(t *testing.T) {
	f := newDirectoryV4UpgradeFixture(t)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	type testcase struct {
		name       string
		membership any
		kind       string
		valid      bool
	}
	raw := func(v [][]byte) directoryassets.RawMembershipV4 {
		return directoryassets.RawMembershipV4{MemberBytes: v}
	}
	cases := []testcase{
		{"missing", raw(nil), "group", true}, {"empty", raw([][]byte{}), "group", true},
		{"ordered NUL foreign", raw([][]byte{[]byte("CN=z,DC=foreign"), []byte("cn=A\x00\n😀<>&")}), "group", true},
		{"user absent", raw(nil), "user", true}, {"computer empty", raw([][]byte{}), "computer", false},
		{"user member", raw([][]byte{[]byte("a")}), "user", false},
		{"nil value", raw([][]byte{nil}), "group", false}, {"empty value", raw([][]byte{{}}), "group", false},
		{"duplicate", raw([][]byte{[]byte("a"), []byte("a")}), "group", false},
		{"case is distinct", raw([][]byte{[]byte("A"), []byte("a")}), "group", true},
		{"invalid UTF8", raw([][]byte{{0xff}}), "group", false}, {"overlong UTF8", raw([][]byte{{0xc0, 0x80}}), "group", false},
		{"missing key", map[string]any{}, "group", false}, {"unknown key", map[string]any{"memberBytes": nil, "extra": nil}, "group", false},
		{"noncanonical base64", map[string]any{"memberBytes": []string{"YQ==\n"}}, "group", false},
		{"wrong shape", map[string]any{"memberBytes": "YQ=="}, "group", false},
	}
	for _, n := range []int{4096, 4097} {
		cases = append(cases, testcase{fmt.Sprintf("bytes-%d", n), raw([][]byte{[]byte(strings.Repeat("x", n))}), "group", n == 4096})
	}
	for _, n := range []int{256, 257} {
		v := make([][]byte, n)
		for i := range v {
			v[i] = []byte(fmt.Sprintf("CN=%d", i))
		}
		cases = append(cases, testcase{fmt.Sprintf("count-%d", n), raw(v), "group", n == 256})
	}
	values := make([][]byte, 16)
	for i := range values {
		values[i] = []byte(fmt.Sprintf("%02d", i) + strings.Repeat("x", 4094))
	}
	cases = append(cases, testcase{"aggregate65536", raw(values), "group", true}, testcase{"aggregate65537", raw(append(append([][]byte{}, values...), []byte("y"))), "group", false})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.membership)
			if err != nil {
				t.Fatal(err)
			}
			var valid bool
			if err = f.conn.QueryRow(f.ctx, `SELECT adtr.domain_directory_membership_v4($1::jsonb,$2)`, string(b), tc.kind).Scan(&valid); err != nil || valid != tc.valid {
				t.Fatalf("valid=%v want=%v err=%v", valid, tc.valid, err)
			}
		})
	}
}

func TestSchema20CanonicalMembershipPreservesNullEmptyOrderAndLegacyBytes(t *testing.T) {
	f := newDirectoryV4UpgradeFixture(t)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	for _, members := range [][][]byte{nil, {}, {[]byte("CN=z,DC=foreign"), []byte("cn=A\x00\n😀<>&")}} {
		entry := directoryassets.Entry{DN: "CN=Group,DC=first,DC=invalid", Attributes: []directoryassets.Attribute{{Name: "objectGUID", Values: [][]byte{make([]byte, 16)}}, {Name: "objectClass", Values: [][]byte{[]byte("group")}}}}
		if members != nil {
			entry.Attributes = append(entry.Attributes, directoryassets.Attribute{Name: "member", Values: members})
		}
		object, err := directoryassets.DecodeV4(entry)
		if err != nil {
			t.Fatal(err)
		}
		body, err := directoryassets.EncodeStoredObjectV4(object)
		if err != nil {
			t.Fatal(err)
		}
		var canonical string
		if err = f.conn.QueryRow(f.ctx, `SELECT adtr.domain_directory_observation_canonical_v4($1::jsonb)`, string(body)).Scan(&canonical); err != nil || canonical != string(body) {
			t.Fatal("SQL/Go canonical mismatch", err)
		}
		directoryassets.ClearStoredObjectsV4([]directoryassets.StoredObjectV4{object})
		clear(body)
	}
}
