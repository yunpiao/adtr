package domains

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func sampleDirectoryV2Observation() ldapconnection.DirectoryV2Observation {
	legacy := sampleDirectoryObservation()
	return ldapconnection.DirectoryV2Observation{Source: legacy.Source, Objects: []directoryassets.StoredObjectV2{{
		Base: legacy.Objects[0],
		Supplemental: directoryassets.RawSupplementalV2{
			ObjectSIDBytes:   []byte{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0},
			MailBytes:        []byte(" Case\x00😀@example.test "),
			DescriptionBytes: [][]byte{[]byte("line\r\n\t\x00<script>😀\\u0000")},
			WhenCreatedBytes: []byte("00010101000000.0Z"),
		},
	}}}
}

func TestDirectoryV2EnvelopeLosslessOwnedRoundTrip(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	original := sampleDirectoryV2Observation()
	defer original.Discard()
	raw, err := encodeDirectoryObservationV2(o)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	if !reflect.DeepEqual(o, original) {
		t.Fatal("encode mutated input")
	}
	if !bytes.HasPrefix(raw, []byte(directoryObservationV2Prefix)) || bytes.Contains(raw, []byte(`\u0000`)) || bytes.Contains(raw, []byte{0}) {
		t.Fatal("raw supplemental text leaked into stored envelope")
	}
	got, err := decodeDirectoryObservationV2(raw, directoryDigest(raw), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Discard()
	if !reflect.DeepEqual(got, original) {
		t.Fatal("stored facts changed")
	}
	public, err := directoryassets.ProjectV2(got.Objects[0])
	if err != nil || public.ObjectSID == nil || *public.ObjectSID != "S-1-5-21" || public.WhenCreated == nil || *public.WhenCreated != "0001-01-01T00:00:00Z" || public.Mail == nil || *public.Mail != string(original.Objects[0].Supplemental.MailBytes) || !reflect.DeepEqual(public.Description, []string{string(original.Objects[0].Supplemental.DescriptionBytes[0])}) {
		t.Fatal("typed projection lost exact observed values", err)
	}
	clear(raw)
	o.Discard()
	if !reflect.DeepEqual(got, original) {
		t.Fatal("decoded values alias caller buffers")
	}
	alias := got.Objects[0].Supplemental.MailBytes
	got.Discard()
	if !bytes.Equal(alias, make([]byte, len(alias))) || !reflect.DeepEqual(got, ldapconnection.DirectoryV2Observation{}) {
		t.Fatal("discard did not clear owned result")
	}
}

func TestDirectoryV2EnvelopeMissingEmptyAndZero(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	directoryassets.ClearStoredObjectsV2(o.Objects)
	zero := uint32(0)
	base := sampleDirectoryObservation().Objects[0]
	base.UserAccountControl = &zero
	o.Objects = []directoryassets.StoredObjectV2{{Base: base}}
	for _, empty := range []bool{false, true} {
		if empty {
			o.Objects = []directoryassets.StoredObjectV2{}
		}
		raw, err := encodeDirectoryObservationV2(o)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeDirectoryObservationV2(raw, directoryDigest(raw), len(o.Objects))
		clear(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got.Objects == nil || len(got.Objects) != len(o.Objects) {
			t.Fatal("successful empty observation changed")
		}
		if !empty && (got.Objects[0].Base.UserAccountControl == nil || *got.Objects[0].Base.UserAccountControl != 0 || !reflect.DeepEqual(got.Objects[0].Supplemental, directoryassets.RawSupplementalV2{})) {
			t.Fatal("absence or observed zero changed")
		}
		got.Discard()
	}
}

func TestDirectoryV2EnvelopeRejectsCorruptionAtomically(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	raw, err := encodeDirectoryObservationV2(o)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	for name, body := range map[string][]byte{
		"version":            bytes.Replace(raw, []byte(`"dictionaryVersion":2`), []byte(`"dictionaryVersion":1`), 1),
		"fractional version": bytes.Replace(raw, []byte(`"dictionaryVersion":2`), []byte(`"dictionaryVersion":2.0`), 1),
		"duplicate":          bytes.Replace(raw, []byte(`"dictionaryVersion":2`), []byte(`"dictionaryVersion":2,"dictionaryVersion":2`), 1),
		"unknown":            bytes.Replace(raw, []byte(`"dictionaryVersion":2`), []byte(`"extra":0,"dictionaryVersion":2`), 1),
		"whitespace":         append([]byte(" "), raw...),
		"trailing":           append(append([]byte(nil), raw...), []byte(` {}`)...),
		"base64":             bytes.Replace(raw, []byte(`"mailBytes":"`), []byte(`"mailBytes":"!`), 1),
		"empty":              bytes.Replace(raw, []byte(`"descriptionBytes":[`), []byte(`"descriptionBytes":["",`), 1),
		"scope":              bytes.Replace(raw, []byte("CN=User,DC=example,DC=test"), []byte("CN=User,DC=other,DC=test"), 1),
		"source":             bytes.Replace(raw, []byte(`"domain":"example.test"`), []byte(`"domain":"other.test"`), 1),
		"source size":        bytes.Replace(raw, []byte(`"server_name":"dc.example.test"`), []byte(`"server_name":"`+strings.Repeat("A", 8193)+`"`), 1),
		"piece size":         bytes.Replace(raw, []byte("CN=User,DC=example,DC=test"), []byte(strings.Repeat("A", directoryassets.MaxStoredObjectV2Bytes)), 1),
	} {
		t.Run(name, func(t *testing.T) {
			before := append([]byte(nil), body...)
			got, err := decodeDirectoryObservationV2(body, directoryDigest(body), 1)
			if err == nil || !reflect.DeepEqual(got, ldapconnection.DirectoryV2Observation{}) {
				t.Fatal("corruption returned data")
			}
			if !bytes.Equal(before, body) {
				t.Fatal("decoder changed input on failure")
			}
			clear(before)
			clear(body)
		})
	}
	for _, count := range []int{-1, 0, 2, 10001} {
		got, err := decodeDirectoryObservationV2(raw, directoryDigest(raw), count)
		if err == nil || !reflect.DeepEqual(got, ldapconnection.DirectoryV2Observation{}) {
			t.Fatal("invalid count returned data")
		}
	}
	if _, err := decodeDirectoryObservationV2(raw, strings.Repeat("0", 64), 1); err == nil {
		t.Fatal("wrong hash accepted")
	}
	// Decode a valid first object before failing on the second; no partial raw fields escape.
	body := bytes.Replace(raw, []byte(`],"source":`), []byte(`,{}],"source":`), 1)
	defer clear(body)
	got, err := decodeDirectoryObservationV2(body, directoryDigest(body), 2)
	if err == nil || !reflect.DeepEqual(got, ldapconnection.DirectoryV2Observation{}) {
		t.Fatal("partial observation escaped")
	}
}

func TestDirectoryV2EnvelopeSourceBoundsAndCanonicalHost(t *testing.T) {
	for _, field := range []string{"server", "domain", "host", "context"} {
		o := sampleDirectoryV2Observation()
		switch field {
		case "server":
			o.Source.ServerName = strings.Repeat("A", 1<<20)
		case "domain":
			o.Source.Domain = strings.Repeat("A", 1<<20)
		case "host":
			o.Source.DCHostName = strings.Repeat("A", 1<<20)
		case "context":
			o.Source.NamingContext = strings.Repeat("A", 1<<20)
		}
		if raw, err := encodeDirectoryObservationV2(o); err == nil || raw != nil {
			t.Fatal("unbounded source accepted")
		}
		o.Discard()
	}
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	o.Source.DCHostName = "DC.Example.Test."
	raw, err := encodeDirectoryObservationV2(o)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	if o.Source.DCHostName != "DC.Example.Test." {
		t.Fatal("input source mutated")
	}
	got, err := decodeDirectoryObservationV2(raw, directoryDigest(raw), 1)
	if err != nil || got.Source.DCHostName != "dc.example.test" {
		t.Fatal("host canonicalization changed", err)
	}
	got.Discard()
	for _, change := range []func(*ldapconnection.DirectoryV2Observation){
		func(o *ldapconnection.DirectoryV2Observation) { o.Source.Pages = 0 },
		func(o *ldapconnection.DirectoryV2Observation) {
			o.Source.CompletedAt = o.Source.StartedAt.Add(-time.Second)
		},
		func(o *ldapconnection.DirectoryV2Observation) { o.Source.ElapsedMilliseconds = 125001 },
		func(o *ldapconnection.DirectoryV2Observation) { o.Source.NamingContext = "DC=other,DC=test" },
	} {
		o := sampleDirectoryV2Observation()
		change(&o)
		if _, err := encodeDirectoryObservationV2(o); err == nil {
			t.Fatal("invalid source accepted")
		}
		o.Discard()
	}
}

func TestDirectoryV2EnvelopeAggregateAndLegacyIsolation(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	legacy, err := encodeDirectoryObservation(sampleDirectoryObservation())
	if err != nil {
		t.Fatal(err)
	}
	v2, err := encodeDirectoryObservationV2(o)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(v2)
	if _, err := decodeDirectoryObservationV2(legacy, directoryDigest(legacy), 1); err == nil {
		t.Fatal("v1 silently upgraded")
	}
	if _, err := decodeDirectoryObservation(v2, directoryDigest(v2), 1); err == nil {
		t.Fatal("v2 silently downgraded")
	}
	again, err := encodeDirectoryObservation(sampleDirectoryObservation())
	if err != nil || !bytes.Equal(again, legacy) {
		t.Fatal("v1 bytes changed")
	}
	if _, err := decodeDirectoryObservation(legacy, directoryDigest(legacy), 1); err != nil {
		t.Fatal(err)
	}
	clear(again)
	clear(legacy)
	base := sampleDirectoryObservation().Objects[0]
	base.DN = "CN=" + strings.Repeat("x", 4000) + ",DC=example,DC=test"
	large := ldapconnection.DirectoryV2Observation{Source: o.Source, Objects: make([]directoryassets.StoredObjectV2, 5000)}
	for i := range large.Objects {
		large.Objects[i].Base = base
		large.Objects[i].Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
	}
	if raw, err := encodeDirectoryObservationV2(large); err == nil || raw != nil {
		t.Fatal("aggregate overflow accepted")
	}
	large.Objects = make([]directoryassets.StoredObjectV2, 10001)
	if _, err := encodeDirectoryObservationV2(large); err == nil {
		t.Fatal("row overflow accepted")
	}
	oversize := bytes.Repeat([]byte("x"), directoryObservationMaxBytes+1)
	if _, err := decodeDirectoryObservationV2(oversize, strings.Repeat("0", 64), 0); err == nil {
		t.Fatal("body overflow accepted")
	}
}
