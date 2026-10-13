package domains

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"reflect"
	"strings"
	"testing"
)

func sampleDirectoryV4StorageObservation() ldapconnection.DirectoryV4Observation {
	old := sampleDirectoryV3Observation()
	out := ldapconnection.DirectoryV4Observation{Source: old.Source, Objects: make([]directoryassets.StoredObjectV4, len(old.Objects))}
	for i, o := range old.Objects {
		out.Objects[i] = directoryassets.StoredObjectV4{Base: o.Base, Supplemental: o.Supplemental, Identity: o.Identity}
	}
	out.Objects[0].Base.Kind = "group"
	out.Objects[0].Base.Classes = []string{"group"}
	return out
}
func TestDirectoryV4StorageCanonicalMembershipAndIsolation(t *testing.T) {
	for _, members := range [][][]byte{nil, {}, {[]byte("CN=z,DC=foreign,DC=invalid"), []byte("cn=A\x00\n😀<>&")}} {
		o := sampleDirectoryV4StorageObservation()
		o.Objects[0].Membership.MemberBytes = members
		raw, err := encodeDirectoryObservationV4(o)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(raw, []byte(`{"dictionaryVersion":4,"objects":[{"base":`)) {
			t.Fatal("canonical envelope order")
		}
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		got, err := decodeDirectoryObservationV4(raw, digest, len(o.Objects))
		if err != nil || !reflect.DeepEqual(got, o) {
			t.Fatal("roundtrip", err)
		}
		got.Discard()
		if members != nil && len(members) > 0 && len(members[0]) > 0 && members[0][0] == 0 {
			t.Fatal("decoded values alias input")
		}
		if _, err := decodeDirectoryObservationV3(raw, digest, len(o.Objects)); err == nil {
			t.Fatal("v4 read as v3")
		}
		for _, body := range [][]byte{append([]byte(" "), raw...), bytes.Replace(raw, []byte(`"dictionaryVersion":4`), []byte(`"dictionaryVersion":4,"dictionaryVersion":4`), 1), bytes.Replace(raw, []byte(`"memberBytes"`), []byte(`"unknown"`), 1), append(append([]byte{}, raw...), ' ')} {
			sum := sha256.Sum256(body)
			out, err := decodeDirectoryObservationV4(body, hex.EncodeToString(sum[:]), len(o.Objects))
			if err == nil || !reflect.DeepEqual(out, ldapconnection.DirectoryV4Observation{}) {
				t.Fatal("corruption retained output")
			}
		}
		if _, err := decodeDirectoryObservationV4(raw, strings.Repeat("0", 64), len(o.Objects)); err == nil {
			t.Fatal("bad digest")
		}
		if _, err := decodeDirectoryObservationV4(raw, digest, len(o.Objects)+1); err == nil {
			t.Fatal("bad count")
		}
		o.Discard()
	}
	old := sampleDirectoryV3Observation()
	defer old.Discard()
	raw, err := encodeDirectoryObservationV3(old)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if _, err = decodeDirectoryObservationV4(raw, hex.EncodeToString(sum[:]), len(old.Objects)); err == nil {
		t.Fatal("v3 backfilled")
	}
}
func TestDirectoryV4StorageRejectsWholeObservationOffRow(t *testing.T) {
	o := sampleDirectoryV4StorageObservation()
	defer o.Discard()
	other := o.Objects[0]
	other.Base.GUID = "11111111-1111-1111-1111-111111111111"
	other.Membership.MemberBytes = [][]byte{[]byte("a"), []byte("a")}
	o.Objects = append(o.Objects, other)
	forged, err := json.Marshal(struct {
		DictionaryVersion int                              `json:"dictionaryVersion"`
		Objects           []directoryassets.StoredObjectV4 `json:"objects"`
		Source            ldapconnection.DirectorySource   `json:"source"`
	}{4, o.Objects, o.Source})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(forged)
	if decoded, err := decodeDirectoryObservationV4(forged, hex.EncodeToString(sum[:]), len(o.Objects)); err == nil || !reflect.DeepEqual(decoded, ldapconnection.DirectoryV4Observation{}) {
		t.Fatal("off-row corruption exposed partial observation")
	}

	if raw, err := encodeDirectoryObservationV4(o); err == nil || raw != nil {
		t.Fatal("invalid off-row object retained partial body")
	}
	o.Objects[1].Membership.MemberBytes = nil
	o.Objects[1].Base.GUID = o.Objects[0].Base.GUID
	if _, err := encodeDirectoryObservationV4(o); err == nil {
		t.Fatal("duplicate GUID")
	}
	o.Objects[1].Base.GUID = other.Base.GUID
	o.Objects[1].Base.DN = "CN=outside,DC=outside,DC=invalid"
	if _, err := encodeDirectoryObservationV4(o); err == nil {
		t.Fatal("foreign source object")
	}
	if err := validateDirectoryV4PageSize(DirectoryV4List{DictionaryVersion: 4, ObservationID: strings.Repeat("x", DirectoryV4PageMaxBytes)}); err == nil {
		t.Fatal("whole response budget")
	}
}
