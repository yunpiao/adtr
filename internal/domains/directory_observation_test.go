package domains

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func sampleDirectoryObservation() ldapconnection.DirectoryObservation {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	return ldapconnection.DirectoryObservation{Objects: []directoryassets.Object{{GUID: "00112233-4455-6677-8899-aabbccddeeff", DN: "CN=User,DC=example,DC=test", Kind: directoryassets.User, Classes: []string{"top", "user"}}}, Source: ldapconnection.DirectorySource{ServerName: "dc.example.test", DCHostName: "dc.example.test", Domain: "example.test", NamingContext: "DC=example,DC=test", StartedAt: now, CompletedAt: now.Add(time.Second), ElapsedMilliseconds: 1000, Pages: 1}}
}
func directoryDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func TestDirectoryObservationRoundTripPreservesAbsentAndZero(t *testing.T) {
	o := sampleDirectoryObservation()
	raw, err := encodeDirectoryObservation(o)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeDirectoryObservation(raw, directoryDigest(raw), 1)
	if err != nil || got.Objects[0].UserAccountControl != nil || got.Objects[0].SAMAccountName != nil {
		t.Fatal("missing fields changed", err)
	}
	zero := uint32(0)
	o.Objects[0].UserAccountControl = &zero
	raw, err = encodeDirectoryObservation(o)
	if err != nil {
		t.Fatal(err)
	}
	got, err = decodeDirectoryObservation(raw, directoryDigest(raw), 1)
	if err != nil || got.Objects[0].UserAccountControl == nil || *got.Objects[0].UserAccountControl != 0 {
		t.Fatal("explicit zero changed", err)
	}
	o.Objects = []directoryassets.Object{}
	raw, err = encodeDirectoryObservation(o)
	if err != nil {
		t.Fatal(err)
	}
	got, err = decodeDirectoryObservation(raw, directoryDigest(raw), 0)
	if err != nil || got.Objects == nil || len(got.Objects) != 0 {
		t.Fatal("empty observation not preserved", err)
	}
}
func TestDirectoryObservationRejectsInvalidOrPartialData(t *testing.T) {
	cases := []func(*ldapconnection.DirectoryObservation){
		func(o *ldapconnection.DirectoryObservation) { o.Objects = nil },
		func(o *ldapconnection.DirectoryObservation) { o.Objects = append(o.Objects, o.Objects[0]) },
		func(o *ldapconnection.DirectoryObservation) { o.Objects[0].GUID = strings.ToUpper(o.Objects[0].GUID) },
		func(o *ldapconnection.DirectoryObservation) { o.Objects[0].Kind = directoryassets.Group },
		func(o *ldapconnection.DirectoryObservation) { o.Objects[0].Classes = []string{"user", "top"} },
		func(o *ldapconnection.DirectoryObservation) { o.Objects[0].DN = strings.Repeat("x", 4097) },
		func(o *ldapconnection.DirectoryObservation) { o.Source.Pages = 0 },
		func(o *ldapconnection.DirectoryObservation) {
			o.Source.CompletedAt = o.Source.StartedAt.Add(-time.Second)
		},
		func(o *ldapconnection.DirectoryObservation) { o.Source.NamingContext = "DC=other,DC=test" },
	}
	for i, change := range cases {
		o := sampleDirectoryObservation()
		change(&o)
		if _, err := encodeDirectoryObservation(o); err == nil {
			t.Fatalf("accepted invalid observation %d", i)
		}
	}
}
func TestDirectoryObservationRejectsCorruptCanonicalBody(t *testing.T) {
	raw, err := encodeDirectoryObservation(sampleDirectoryObservation())
	if err != nil {
		t.Fatal(err)
	}
	for i, body := range [][]byte{append([]byte(" "), raw...), bytes.Replace(raw, []byte(`"kind":"user"`), []byte(`"kind":"group"`), 1), bytes.Replace(raw, []byte(`"objectGUID"`), []byte(`"extra":1,"objectGUID"`), 1), bytes.Replace(raw, []byte(`"objects":[`), []byte(`"objects": [{},`), 1)} {
		if _, err := decodeDirectoryObservation(body, directoryDigest(body), 1); err == nil {
			t.Fatalf("accepted corrupt body %d", i)
		}
	}
	if _, err := decodeDirectoryObservation(raw, strings.Repeat("0", 64), 1); err == nil {
		t.Fatal("accepted wrong digest")
	}
	if _, err := decodeDirectoryObservation(raw, directoryDigest(raw), 0); err == nil {
		t.Fatal("accepted wrong count")
	}
}

func TestDirectoryObservationScopeAndAggregateBudget(t *testing.T) {
	for _, dn := range []string{"CN=User,DC=other,DC=test", "CN=User,DC=child,DC=example,DC=test", "CN=Fake\\,DC=example,DC=test"} {
		o := sampleDirectoryObservation()
		o.Objects[0].DN = dn
		if _, err := encodeDirectoryObservation(o); err == nil {
			t.Fatalf("accepted out-of-scope DN %q", dn)
		}
	}
	o := sampleDirectoryObservation()
	base := o.Objects[0]
	o.Objects = make([]directoryassets.Object, 5000)
	base.DN = "CN=" + strings.Repeat("x", 4000) + ",DC=example,DC=test"
	for i := range o.Objects {
		o.Objects[i] = base
		o.Objects[i].GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
	}
	if _, err := encodeDirectoryObservation(o); err == nil {
		t.Fatal("accepted aggregate bytes beyond storage budget")
	}
	o.Objects = make([]directoryassets.Object, 10001)
	if _, err := encodeDirectoryObservation(o); err == nil {
		t.Fatal("accepted row budget overflow")
	}
}

func TestDirectoryObservationAcceptsRootDSEHostCaseAndRootDot(t *testing.T) {
	for _, host := range []string{"DC.Example.Test", "DC.Example.Test."} {
		o := sampleDirectoryObservation()
		o.Source.DCHostName = host
		raw, err := encodeDirectoryObservation(o)
		if err != nil {
			t.Fatal("valid RootDSE host rejected", err)
		}
		got, err := decodeDirectoryObservation(raw, directoryDigest(raw), 1)
		if err != nil || got.Source.DCHostName != "dc.example.test" {
			t.Fatal("RootDSE host not canonical in persisted bytes", err)
		}
		noncanonical := bytes.Replace(raw, []byte(`"dc_host_name":"dc.example.test"`), []byte(`"dc_host_name":"DC.Example.Test."`), 1)
		if _, err := decodeDirectoryObservation(noncanonical, directoryDigest(noncanonical), 1); err == nil {
			t.Fatal("noncanonical stored bytes accepted")
		}
	}
}
