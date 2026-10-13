//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

// Independently encodes the fixed eleven attributes and unchanged search scope,
// filter and limits. Do not obtain expected bytes from the fixture matcher.
func testDirectoryV4Body() []byte {
	return bytes.Join([][]byte{
		octets("dc=synthetic,dc=invalid"), {10, 1, 2, 10, 1, 0, 2, 1, 0, 2, 1, 3, 1, 1, 0},
		element(0xa1, element(0xa3, octets("objectClass"), octets("user")), element(0xa3, octets("objectClass"), octets("group"))),
		element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl"), octets("objectSid"), octets("mail"), octets("description"), octets("whenCreated"), octets("primaryGroupID"), octets("servicePrincipalName"), octets("member")),
	}, nil)
}
func TestDirectoryV4FixtureConfigurationExclusivity(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	for _, args := range [][]string{
		{"-directory-v4"},
		{"-directory-mode", "-directory-v4", "-directory-v2"},
		{"-directory-mode", "-directory-v4", "-directory-v3"},
		{"-directory-mode", "-directory-v4", "-user-assets-v2"},
		{"-directory-mode", "-directory-v4", "-computer-assets-v3"},
		{"-directory-mode", "-directory-v4", "-group-assets-v3"},
		{"-directory-mode", "-directory-v4", "-control-dir", "/synthetic"},
		{"-directory-mode", "-directory-v4", "-empty-transition-dir", "/synthetic"},
	} {
		if _, err := readConfiguration(append(append([]string{}, base...), args...), fixtureEnvironment); err == nil {
			t.Fatal("accepted incompatible mode", args)
		}
	}
	for _, args := range [][]string{{}, {"-directory-empty"}, {"-directory-slow"}, {"-directory-empty", "-directory-slow"}} {
		flags := append(append([]string{}, base...), "-directory-mode", "-directory-v4")
		flags = append(flags, args...)
		cfg, err := readConfiguration(flags, fixtureEnvironment)
		if err != nil || !cfg.directoryMode || !cfg.directoryV4 || cfg.directoryV2 || cfg.directoryV3 {
			t.Fatal("valid V4 mode", err)
		}
		clear(cfg.username)
		clear(cfg.password)
	}
}
func receiveDirectoryV4Page(t *testing.T, conn io.Reader, id int) ([]directoryassets.StoredObjectV4, []byte) {
	t.Helper()
	rows := make([]directoryassets.StoredObjectV4, 0, 15)
	for range 15 {
		r, err := readRequest(conn)
		if err != nil || r.id != id || r.tag != 0x64 || len(r.controls) != 0 {
			t.Fatal("invalid V4 entry", err)
		}
		o, err := directoryassets.DecodeV4(decodeTestEntry(t, r.body))
		if err != nil {
			t.Fatal("production V4 decode", err)
		}
		rows = append(rows, o)
	}
	_, cookie := receiveDirectoryPageForCounts(t, conn, id, 0, 30)
	return rows, cookie
}
func assertDirectoryV4Rows(t *testing.T, rows []directoryassets.StoredObjectV4) {
	t.Helper()
	if len(rows) != 30 {
		t.Fatalf("row count %d", len(rows))
	}
	base := make([]directoryassets.Object, len(rows))
	legacy := make([]directoryassets.StoredObjectV2, len(rows))
	seen := map[string]bool{}
	for i, o := range rows {
		base[i] = o.Base
		legacy[i] = directoryassets.StoredObjectV2{Base: o.Base, Supplemental: o.Supplemental}
		if seen[o.Base.GUID] {
			t.Fatal("duplicate GUID")
		}
		seen[o.Base.GUID] = true
		p, err := directoryassets.ProjectV4(o)
		if err != nil {
			t.Fatal(err)
		}
		row := i + 1
		var want []string
		if row > 12 && row <= 24 {
			switch row {
			case 13:
			case 14:
				want = make([]string, 0)
			case 15:
				want = []string{
					"CN=user-01,OU=Users,DC=synthetic,DC=invalid",
					"CN=group-02,OU=Groups,DC=synthetic,DC=invalid",
					"CN=S-1-5-21-foreign,CN=ForeignSecurityPrincipals,DC=elsewhere,DC=invalid",
					"CN=raw\x00\n😀<script>,DC=synthetic,DC=invalid",
				}
			default:
				want = []string{fmt.Sprintf("CN=member-%02d,OU=Users,DC=synthetic,DC=invalid", row)}
			}
		}
		if !reflect.DeepEqual(p.RawMember, want) {
			t.Fatalf("row %d member order/value/presence", row)
		}
		if want == nil {
			if o.Membership.MemberBytes != nil || p.ReturnedMemberCount != nil || p.MemberReadState != "not_returned" {
				t.Fatalf("row %d absence", row)
			}
		} else {
			if o.Membership.MemberBytes == nil || p.ReturnedMemberCount == nil || *p.ReturnedMemberCount != len(want) || p.MemberReadState != "returned" {
				t.Fatalf("row %d presence", row)
			}
			for j, v := range want {
				if !bytes.Equal(o.Membership.MemberBytes[j], []byte(v)) {
					t.Fatalf("row %d raw value", row)
				}
			}
		}
		stored, err := directoryassets.EncodeStoredObjectV4(o)
		if err != nil {
			t.Fatal(err)
		}
		got, err := directoryassets.DecodeStoredObjectV4(stored)
		if err != nil || !reflect.DeepEqual(got, o) {
			t.Fatalf("row %d storage roundtrip %v", row, err)
		}
		directoryassets.ClearStoredObjectsV4([]directoryassets.StoredObjectV4{got})
		if bytes.Contains(stored, []byte(`\u0000`)) {
			t.Fatal("NUL must remain base64 in storage")
		}
		public, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(public, &fields); err != nil || len(fields) != 15 {
			t.Fatal("public closed fields", err)
		}
		if _, exists := fields["memberCount"]; exists {
			t.Fatal("unapproved memberCount alias")
		}
		if row == 13 && (string(fields["rawMember"]) != "null" || string(fields["returnedMemberCount"]) != "null") {
			t.Fatal("missing JSON")
		}
		if row == 14 && (string(fields["rawMember"]) != "[]" || string(fields["returnedMemberCount"]) != "0") {
			t.Fatal("empty JSON")
		}
		if row == 15 && (!bytes.Contains(public, []byte(`\u0000`)) || bytes.Contains(public, []byte("<script>"))) {
			t.Fatal("unsafe/lossy raw public text")
		}
	}
	assertDictionary(t, base)
	assertDictionaryV2(t, legacy)
	if !reflect.DeepEqual(rows[0].Identity.ServicePrincipalNameBytes, [][]byte{[]byte("HOST/Z"), []byte("host/a\x00\n😀<script>"), []byte("HOST/a")}) {
		t.Fatal("prior identity raw bytes changed")
	}
}
func TestDirectoryV4FixtureRealTLSBERAndExactRows(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(map[bool]string{false: "StartTLS", true: "LDAPS"}[ldaps], func(t *testing.T) {
			f, cfg := testServer(t)
			f.directoryMode, f.directoryV4 = true, true
			conn := directoryConnection(t, f, cfg, ldaps)
			if !bytes.Equal(testDirectoryV4Body(), directoryV4SearchBody) {
				t.Fatal("fixed eleven-attribute request changed")
			}
			send(t, conn, testDirectoryRequest(4, testDirectoryV4Body(), testPagingControl(1000, nil)))
			first, cookie := receiveDirectoryV4Page(t, conn, 4)
			if len(cookie) != 20 || !bytes.Equal(cookie[:4], []byte{0, 255, 128, 0}) {
				t.Fatal("opaque cookie")
			}
			send(t, conn, testDirectoryRequest(5, testDirectoryV4Body(), testPagingControl(1000, cookie)))
			second, end := receiveDirectoryV4Page(t, conn, 5)
			if len(end) != 0 {
				t.Fatal("nonterminal page")
			}
			rows := append(first, second...)
			defer directoryassets.ClearStoredObjectsV4(rows)
			assertDirectoryV4Rows(t, rows)
			for i, replay := range [][]byte{nil, cookie} {
				send(t, conn, testDirectoryRequest(i+6, testDirectoryV4Body(), testPagingControl(1000, replay)))
				requireCode(t, conn, i+6, 0x65, 53)
			}
		})
	}
}
func TestDirectoryV4FixtureRejectsOldProfilesAndChangedRequest(t *testing.T) {
	body := testDirectoryV4Body()
	for _, ldaps := range []bool{false, true} {
		for name, requestBody := range map[string][]byte{
			"v1": testDirectoryBody(), "v2": testDirectoryV2Body(), "v3": directoryV3SearchBody,
			"member-option":   bytes.Replace(body, octets("member"), octets("member;range=0-*"), 1),
			"memberOf":        bytes.Replace(body, octets("member"), octets("memberOf"), 1),
			"attribute-order": bytes.Replace(body, append(octets("servicePrincipalName"), octets("member")...), append(octets("member"), octets("servicePrincipalName")...), 1),
		} {
			t.Run(fmt.Sprintf("%t/%s", ldaps, name), func(t *testing.T) {
				f, cfg := testServer(t)
				f.directoryMode, f.directoryV4 = true, true
				conn := directoryConnection(t, f, cfg, ldaps)
				send(t, conn, testDirectoryRequest(4, requestBody, testPagingControl(1000, nil)))
				requireCode(t, conn, 4, 0x65, 53)
				send(t, conn, testDirectoryRequest(5, body, testPagingControl(1000, nil)))
				rows, _ := receiveDirectoryV4Page(t, conn, 5)
				directoryassets.ClearStoredObjectsV4(rows)
			})
		}
	}
	for _, profile := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("old-mode-%d", profile), func(t *testing.T) {
			f, cfg := testServer(t)
			f.directoryMode = true
			f.directoryV2 = profile == 2
			f.directoryV3 = profile == 3
			conn := directoryConnection(t, f, cfg, true)
			send(t, conn, testDirectoryRequest(4, body, testPagingControl(1000, nil)))
			requireCode(t, conn, 4, 0x65, 53)
		})
	}
}
func TestDirectoryV4FixtureActualProductionReaderTLS(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		t.Run(string(mode), func(t *testing.T) {
			f, clientTLS := testServer(t)
			f.directoryMode, f.directoryV4 = true, true
			var stages []ldapconnection.DirectoryStage
			cfg := ldapconnection.DirectoryV4Config{Mode: mode, ServerName: "dc.synthetic.invalid", Domain: "SYNTHETIC.INVALID.", DialIP: netip.MustParseAddr("127.0.0.1"), Roots: clientTLS.RootCAs, AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, Limits: directoryassets.Limits{MaxRows: 10000, MaxPages: 100, MaxPageEntries: 1000, MaxBytes: 16 << 20, MaxCookieBytes: 65536}, Authorize: func(_ context.Context, stage ldapconnection.DirectoryStage) error {
				stages = append(stages, stage)
				return nil
			}}
			done := make(chan struct{})
			dials := 0
			credential := ldapconnection.Credential{Username: testUsername, Password: []byte(testPassword)}
			result, err := ldapconnection.ReadDirectoryV4WithDialForIntegrationTest(context.Background(), cfg, credential, func(_ context.Context, network, address string) (net.Conn, error) {
				dials++
				port := "636"
				if mode == ldapconnection.StartTLS {
					port = "389"
				}
				if network != "tcp" || address != "127.0.0.1:"+port || dials != 1 {
					return nil, fmt.Errorf("unexpected fixed destination")
				}
				client, peer := net.Pipe()
				go func() { defer close(done); f.handle(context.Background(), peer, mode == ldapconnection.LDAPS) }()
				return client, nil
			})
			defer result.Discard()
			if err != nil {
				t.Fatal("production reader rejected fixture", err)
			}
			if !bytes.Equal(credential.Password, make([]byte, len(testPassword))) {
				t.Fatal("credential not cleared")
			}
			assertDirectoryV4Rows(t, result.Objects)
			if result.Source.Pages != 2 || result.Source.Domain != "synthetic.invalid" || result.Source.NamingContext != "dc=synthetic,dc=invalid" || result.Source.DCHostName != "DC.Synthetic.Invalid." {
				t.Fatal("wrong source metadata")
			}
			want := []ldapconnection.DirectoryStage{ldapconnection.DirectoryValidate, ldapconnection.DirectoryDial, ldapconnection.DirectoryTLS, ldapconnection.DirectoryBind, ldapconnection.DirectoryRootDSE, ldapconnection.DirectoryPage, ldapconnection.DirectoryPage, ldapconnection.DirectoryReturn}
			if !reflect.DeepEqual(stages, want) {
				t.Fatal("authority checkpoints", stages)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("transport did not stop")
			}
			alias := result.Objects[14].Membership.MemberBytes[3]
			result.Discard()
			if !bytes.Equal(alias, make([]byte, len(alias))) {
				t.Fatal("membership buffer retained")
			}
		})
	}
}
