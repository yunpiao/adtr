package ldapconnection

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestLegacyDirectorySearchPacketRemainsExact(t *testing.T) {
	// Frozen BER for the original four-attribute search: message 4, subtree,
	// no dereferencing, critical page size 4, and an empty first-page cookie.
	const golden = "3081bf020104638191041464633d73796e7468657469632c64633d74657374" +
		"0a01020a0100020100020103010100a12ba313040b6f626a656374436c617373" +
		"040475736572a314040b6f626a656374436c617373040567726f7570303d" +
		"040a6f626a65637447554944040b6f626a656374436c617373" +
		"040e73414d4163636f756e744e616d650412757365724163636f756e74436f6e74726f6c" +
		"a02630240416312e322e3834302e3131333535362e312e342e3331390101ff040730050201040400"
	want, err := hex.DecodeString(golden)
	if err != nil {
		t.Fatal(err)
	}
	packet := directorySearchPacket(fixtureDirectoryBase, 4, 4, nil)
	defer clear(packet)
	if !bytes.Equal(packet, want) {
		t.Fatalf("legacy LDAP request bytes changed\ngot:  %x\nwant: %x", packet, want)
	}
	m, err := readDirectoryMessage(bytes.NewReader(packet), 4, maxDirectoryMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(m.packet)
	if m.tag != 0x63 {
		t.Fatal("legacy request is no longer a search")
	}
	fields := directoryBER(m.body)
	// Parse the production encoder's actual attribute sequence, not source text
	// or a separately assembled expected packet.
	for _, tag := range []byte{4, 10, 10, 2, 2, 1, 0xa1} {
		if _, err := fields.expect(tag); err != nil {
			t.Fatal("invalid legacy search field", err)
		}
	}
	encoded, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		t.Fatal("invalid legacy attribute sequence", err)
	}
	attributes := directoryBER(encoded)
	var names []string
	for len(attributes) > 0 {
		name, err := attributes.expect(4)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, string(name))
	}
	if !reflect.DeepEqual(names, []string{"objectGUID", "objectClass", "sAMAccountName", "userAccountControl"}) {
		t.Fatalf("legacy collection allowlist changed: %v", names)
	}
}

func TestLegacyDirectoryEntryRejectsSupplementalAttributes(t *testing.T) {
	for _, attr := range []struct{ name, value string }{
		{"objectSid", string([]byte{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0})},
		{"mail", "Synthetic@Example.Test"},
		{"description", "synthetic description"},
		{"whenCreated", "20261007120000.0Z"},
	} {
		for _, name := range []string{attr.name, strings.ToUpper(attr.name)} {
			t.Run(name, func(t *testing.T) {
				// Keep three descriptors so failure tests the name allowlist,
				// rather than only the existing four-attribute count limit.
				body := append(octets("CN=Synthetic,"+fixtureDirectoryBase), element(0x30,
					fixtureAttribute("objectGUID", string(bytes.Repeat([]byte{1}, 16))),
					fixtureAttribute("objectClass", "top", "user"),
					fixtureAttribute(name, attr.value))...)
				packet := fixtureDirectoryMessage(4, 0x64, body)
				m, err := readDirectoryMessage(bytes.NewReader(packet), 4, maxDirectoryMessageBytes)
				if err != nil {
					t.Fatal("supplemental response fixture is not valid BER", err)
				}
				defer clear(m.packet)
				got, err := directoryEntry(m.body, fixtureDirectoryBase)
				assertError(t, err, CodeInvalidResponse, StageSearch)
				if !reflect.DeepEqual(got, directoryassets.Entry{}) {
					t.Fatal("rejected supplemental response exposed a partial legacy entry")
				}
			})
		}
	}
}
