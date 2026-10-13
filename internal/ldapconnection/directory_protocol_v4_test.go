package ldapconnection

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestDirectoryV4WireMemberStatesOwnershipAndOldIsolation(t *testing.T) {
	for _, attrs := range [][][]byte{nil, {fixtureAttribute("member")}, {fixtureAttribute("member", "CN=foreign,DC=other", " raw\x00\t😀\n ")}} {
		body := fixtureDirectoryV3Body(1, "group", attrs...)
		e, err := directoryEntryV4(body, fixtureDirectoryBase)
		if err != nil {
			t.Fatal(err)
		}
		if attrs != nil {
			if _, err := directoryEntryV3(body, fixtureDirectoryBase); err == nil {
				t.Fatal("v3 widened")
			}
		}
		clear(body)
		o, err := directoryassets.DecodeV4(e)
		if err != nil {
			t.Fatal(err)
		}
		clearDirectoryEntries([]directoryassets.Entry{e})
		p, err := directoryassets.ProjectV4(o)
		if err != nil {
			t.Fatal(err)
		}
		if attrs == nil {
			if p.RawMember != nil || p.ReturnedMemberCount != nil || p.MemberReadState != "not_returned" {
				t.Fatal("missing")
			}
		} else {
			if p.RawMember == nil || p.ReturnedMemberCount == nil || p.MemberReadState != "returned" {
				t.Fatal("present")
			}
			if len(p.RawMember) > 0 && !reflect.DeepEqual(p.RawMember, []string{"CN=foreign,DC=other", " raw\x00\t😀\n "}) {
				t.Fatal("raw changed")
			}
		}
		directoryassets.ClearStoredObjectsV4([]directoryassets.StoredObjectV4{o})
	}
}
func TestDirectoryV4WireFailClosedMemberCases(t *testing.T) {
	many := make([]string, 257)
	for i := range many {
		many[i] = fmt.Sprint(i)
	}
	aggregate := make([]string, 17)
	for i := range aggregate {
		aggregate[i] = fmt.Sprintf("%04096d", i)
	}
	for _, tc := range []struct {
		name, kind string
		attrs      [][]byte
		code       Code
	}{
		{"range", "group", [][]byte{fixtureAttribute("member;range=0-*", "x")}, CodeUnsupportedProfile},
		{"rangeMore", "group", [][]byte{fixtureAttribute("member;range=1500-*", "x")}, CodeUnsupportedProfile},
		{"placeholderRange", "group", [][]byte{fixtureAttribute("member"), fixtureAttribute("MEMBER;RANGE=0-1499", "x")}, CodeUnsupportedProfile},
		{"fullDictionaryRange", "group", append(append(fixtureV3Supplemental(), fixtureAttribute("member")), fixtureAttribute("member;range=0-*", "x")), CodeUnsupportedProfile},
		{"malformedRange", "group", [][]byte{fixtureAttribute("member;range=invalid", "x")}, CodeUnsupportedProfile},
		{"userEmpty", "user", [][]byte{fixtureAttribute("member")}, CodeUnsupportedProfile},
		{"computer", "computer", [][]byte{fixtureAttribute("member", "x")}, CodeUnsupportedProfile},
		{"foreign", "foreignSecurityPrincipal", [][]byte{fixtureAttribute("member", "x")}, CodeUnsupportedProfile},
		{"emptyValue", "group", [][]byte{fixtureAttribute("member", "")}, CodeInvalidResponse},
		{"invalidUTF8", "group", [][]byte{fixtureAttribute("member", string([]byte{255}))}, CodeInvalidResponse},
		{"duplicateValue", "group", [][]byte{fixtureAttribute("member", "x", "x")}, CodeInvalidResponse},
		{"duplicateAlias", "group", [][]byte{fixtureAttribute("member"), fixtureAttribute("MEMBER")}, CodeInvalidResponse},
		{"unknown", "group", [][]byte{fixtureAttribute("memberCount", "0")}, CodeInvalidResponse},
		{"option", "group", [][]byte{fixtureAttribute("member;binary", "x")}, CodeInvalidResponse},
		{"byteLimit", "group", [][]byte{fixtureAttribute("member", strings.Repeat("x", 4097))}, CodeUnsupportedProfile},
		{"countLimit", "group", [][]byte{fixtureAttribute("member", many...)}, CodeUnsupportedProfile},
		{"aggregateLimit", "group", [][]byte{fixtureAttribute("member", aggregate...)}, CodeUnsupportedProfile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fixtureDirectoryV3Body(1, tc.kind, tc.attrs...)
			prior := bytes.Clone(body)
			got, err := directoryEntryV4(body, fixtureDirectoryBase)
			assertError(t, err, tc.code, StageSearch)
			if !reflect.DeepEqual(got, directoryassets.Entry{}) || !bytes.Equal(body, prior) {
				t.Fatal("failed parser retained/modified data")
			}
		})
	}
}
func TestDirectoryV4TLSRawMembersAcrossPages(t *testing.T) {
	for _, mode := range []Mode{LDAPS, StartTLS} {
		t.Run(string(mode), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryV4Config(t, mode, roots)
			network := pipeTransport(t, mode, func(raw net.Conn) error {
				conn, err := fixtureDirectorySession(raw, mode, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				for page := 0; page < 3; page++ {
					var request, response []byte
					if page > 0 {
						request = []byte("cookie")
					}
					if page < 2 {
						response = []byte("cookie")
					}
					if err := fixtureExpectDirectoryV4Request(conn, page+4, cfg.Limits.MaxPageEntries, request); err != nil {
						return err
					}
					var attrs [][]byte
					if page == 1 {
						attrs = [][]byte{fixtureAttribute("member")}
					}
					if page == 2 {
						attrs = [][]byte{fixtureAttribute("member", "CN=foreign,DC=other", " Raw\x00\n😀 ")}
					}
					for _, packet := range [][]byte{fixtureDirectoryMessage(page+4, 0x64, fixtureDirectoryV3Body(byte(page+1), "group", attrs...)), fixtureDirectoryDone(page+4, response)} {
						if _, err := conn.Write(packet); err != nil {
							return err
						}
						clear(packet)
					}
				}
				return expectNoApplicationBytes(conn)
			})
			result, err := fixtureReadDirectoryV4(t, context.Background(), cfg, network)
			if err != nil {
				t.Fatal(err)
			}
			defer result.Discard()
			if len(result.Objects) != 3 || result.Source.Pages != 3 {
				t.Fatal("observation")
			}
			for i, o := range result.Objects {
				p, err := directoryassets.ProjectV4(o)
				if err != nil {
					t.Fatal(err)
				}
				switch i {
				case 0:
					if p.RawMember != nil || p.ReturnedMemberCount != nil || p.MemberReadState != "not_returned" {
						t.Fatal("absent")
					}
				case 1:
					if p.RawMember == nil || len(p.RawMember) != 0 || p.ReturnedMemberCount == nil || *p.ReturnedMemberCount != 0 || p.MemberReadState != "returned" {
						t.Fatal("empty")
					}
				case 2:
					if !reflect.DeepEqual(p.RawMember, []string{"CN=foreign,DC=other", " Raw\x00\n😀 "}) || *p.ReturnedMemberCount != 2 {
						t.Fatal("raw")
					}
				}
			}
			alias := result.Objects[2].Membership.MemberBytes[0]
			result.Discard()
			if !bytes.Equal(alias, make([]byte, len(alias))) {
				t.Fatal("member not scrubbed")
			}
		})
	}
}
func TestDirectoryV4TLSRangeFailureClearsPriorPages(t *testing.T) {
	certificate, roots := trustedFixture(t)
	cfg := fixtureDirectoryV4Config(t, LDAPS, roots)
	network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
		conn, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
		if err != nil {
			return err
		}
		if err = fixtureExpectDirectoryV4Request(conn, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
			return err
		}
		for _, p := range [][]byte{fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV3Body(1, "group", fixtureAttribute("member", "prior-secret"))), fixtureDirectoryDone(4, []byte("c"))} {
			if _, err = conn.Write(p); err != nil {
				return err
			}
		}
		if err = fixtureExpectDirectoryV4Request(conn, 5, cfg.Limits.MaxPageEntries, []byte("c")); err != nil {
			return err
		}
		_, err = conn.Write(fixtureDirectoryMessage(5, 0x64, fixtureDirectoryV3Body(2, "group", fixtureAttribute("member"), fixtureAttribute("member;range=0-*", "last-secret"))))
		if err != nil {
			return err
		}
		return expectNoApplicationBytes(conn)
	})
	result, err := fixtureReadDirectoryV4(t, context.Background(), cfg, network)
	assertError(t, err, CodeUnsupportedProfile, StageSearch)
	assertEmptyDirectoryV4Observation(t, result)
}
