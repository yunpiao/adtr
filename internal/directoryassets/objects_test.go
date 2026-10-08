package directoryassets

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func entry(id byte, classes ...string) Entry {
	guid := make([]byte, 16)
	guid[0] = id
	values := make([][]byte, len(classes))
	for i, v := range classes {
		values[i] = []byte(v)
	}
	return Entry{DN: "CN=Synthetic,DC=example,DC=test", Attributes: []Attribute{{"objectGUID", [][]byte{guid}}, {"objectClass", values}}}
}
func TestGUIDWindowsByteOrder(t *testing.T) {
	raw, _ := hex.DecodeString("33221100554477668899aabbccddeeff")
	got, err := DecodeGUID(raw)
	if err != nil || got != "00112233-4455-6677-8899-aabbccddeeff" {
		t.Fatal("GUID byte-order conversion differs", got, err)
	}
	for _, n := range []int{0, 15, 17} {
		if _, err := DecodeGUID(make([]byte, n)); !errors.Is(err, ErrGUID) {
			t.Fatal("invalid GUID length accepted")
		}
	}
}
func TestDictionaryClassificationAndMissingValues(t *testing.T) {
	for _, tc := range []struct {
		classes []string
		want    Kind
	}{{[]string{"top", "User"}, User}, {[]string{"top", "GROUP"}, Group}, {[]string{"Computer", "User", "top"}, Computer}} {
		got, err := Decode(entry(1, tc.classes...))
		if err != nil || got.Kind != tc.want {
			t.Fatal("classification failed", err)
		}
		if got.SAMAccountName != nil || got.UserAccountControl != nil {
			t.Fatal("missing optional values became defaults")
		}
	}
	e := entry(2, "user", "top")
	e.Attributes = append(e.Attributes, Attribute{"sAMAccountName", [][]byte{[]byte("Case.Preserved")}}, Attribute{"userAccountControl", [][]byte{[]byte("0")}})
	got, err := Decode(e)
	if err != nil || *got.SAMAccountName != "Case.Preserved" || *got.UserAccountControl != 0 || strings.Join(got.Classes, ",") != "top,user" {
		t.Fatal("typed optional values differ", err)
	}
	e.Attributes[3].Values[0] = []byte("4294967295")
	got, err = Decode(e)
	if err != nil || *got.UserAccountControl != ^uint32(0) {
		t.Fatal("unsigned bits lost", err)
	}
}
func TestDictionaryRejectsMalformedDataWithoutEcho(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Entry)
	}{
		{"missing GUID", func(e *Entry) { e.Attributes[0].Values = nil }},
		{"Unicode attribute fold", func(e *Entry) { e.Attributes[0].Name = "objectGUİD" }},
		{"multiple GUID", func(e *Entry) { e.Attributes[0].Values = append(e.Attributes[0].Values, make([]byte, 16)) }},
		{"duplicate attribute", func(e *Entry) {
			e.Attributes = append(e.Attributes, Attribute{"OBJECTguid", [][]byte{make([]byte, 16)}})
		}},
		{"unknown secret", func(e *Entry) {
			e.Attributes = append(e.Attributes, Attribute{"unicodePwd", [][]byte{[]byte("do-not-echo-secret")}})
		}},
		{"ranged partial", func(e *Entry) { e.Attributes = append(e.Attributes, Attribute{"member;range=0-1499", nil}) }},
		{"empty name", func(e *Entry) { e.Attributes = append(e.Attributes, Attribute{"samaccountname", [][]byte{nil}}) }},
		{"oversized name", func(e *Entry) {
			e.Attributes = append(e.Attributes, Attribute{"samaccountname", [][]byte{[]byte(strings.Repeat("a", 1025))}})
		}},
		{"multivalued name", func(e *Entry) {
			e.Attributes = append(e.Attributes, Attribute{"samaccountname", [][]byte{[]byte("a"), []byte("b")}})
		}},
		{"invalid UTF8", func(e *Entry) { e.DN = string([]byte{255}) }},
		{"control DN", func(e *Entry) { e.DN = "CN=bad\x00" }},
		{"large DN", func(e *Entry) { e.DN = strings.Repeat("a", 4097) }},
		{"conflicting kinds", func(e *Entry) { e.Attributes[1].Values = [][]byte{[]byte("user"), []byte("group")} }},
		{"unknown kind", func(e *Entry) { e.Attributes[1].Values = [][]byte{[]byte("contact")} }},
		{"duplicate classes", func(e *Entry) { e.Attributes[1].Values = [][]byte{[]byte("user"), []byte("USER")} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := entry(1, "user")
			tc.mutate(&e)
			_, err := Decode(e)
			if err == nil {
				t.Fatal("malformed record accepted")
			}
			if strings.Contains(err.Error(), "do-not-echo") {
				t.Fatal("input echoed")
			}
			if strings.Contains(fmt.Sprintf("%#v", e), "do-not-echo") {
				t.Fatal("entry formatter exposed values")
			}
		})
	}
	for _, n := range []string{"", "-1", "+1", "01", "4294967296", "１２"} {
		e := entry(1, "user")
		e.Attributes = append(e.Attributes, Attribute{"userAccountControl", [][]byte{[]byte(n)}})
		if _, err := Decode(e); !errors.Is(err, ErrNumber) {
			t.Fatal("invalid numeric encoding accepted")
		}
	}
}

func FuzzDecodeBoundedAttributes(f *testing.F) {
	f.Add(make([]byte, 16), []byte("Synthetic"), []byte("512"))
	f.Add([]byte{1}, []byte{255}, []byte("4294967296"))
	f.Fuzz(func(t *testing.T, guid, name, control []byte) {
		if len(guid) > 32 || len(name) > 2048 || len(control) > 32 {
			return
		}
		e := entry(1, "user")
		e.Attributes[0].Values = [][]byte{guid}
		e.Attributes = append(e.Attributes, Attribute{"samaccountname", [][]byte{name}}, Attribute{"useraccountcontrol", [][]byte{control}})
		result, err := Decode(e)
		if err == nil && (len(result.GUID) != 36 || result.SAMAccountName == nil || result.UserAccountControl == nil || result.Kind != User) {
			t.Fatal("invalid successful projection")
		}
	})
}
