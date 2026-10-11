package directoryassets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func entryV3() Entry {
	e := entryV2(1)
	e.Attributes = append(e.Attributes, Attribute{"primaryGroupID", [][]byte{[]byte("4294967295")}}, Attribute{"servicePrincipalName", [][]byte{[]byte("HOST/Z"), []byte("host/a\x00\n😀<script>"), []byte("HOST/a")}})
	return e
}
func TestV3LosslessProjectionAndVersionIsolation(t *testing.T) {
	e := entryV3()
	o, err := DecodeV3(e)
	if err != nil {
		t.Fatal(err)
	}
	defer ClearStoredObjectsV3([]StoredObjectV3{o})
	raw, err := EncodeStoredObjectV3(o)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("HOST/Z")) || bytes.Contains(raw, []byte(`\u0000`)) {
		t.Fatal("raw identity leaked into storage text")
	}
	got, err := DecodeStoredObjectV3(raw)
	if err != nil || !reflect.DeepEqual(got, o) {
		t.Fatal("lossless roundtrip", err)
	}
	p, err := ProjectV3(got)
	if err != nil || p.PrimaryGroupID == nil || *p.PrimaryGroupID != 4294967295 || !reflect.DeepEqual(p.ServicePrincipalName, []string{"HOST/Z", "host/a\x00\n😀<script>", "HOST/a"}) {
		t.Fatal("projection", err)
	}
	wire, _ := json.Marshal(p)
	if !bytes.Contains(wire, []byte(`\u0000`)) || bytes.Contains(wire, []byte("<script>")) {
		t.Fatal("unsafe public JSON")
	}
	if _, err = DecodeV2(e); err == nil {
		t.Fatal("v2 accepted v3 attributes")
	}
	if _, err = DecodeStoredObjectV2(raw); err == nil {
		t.Fatal("v2 accepted v3 storage")
	}
	old := objectV2(t)
	oldRaw := encodedV2(t, old)
	if _, err = DecodeStoredObjectV3(oldRaw); err == nil {
		t.Fatal("old observation was backfilled")
	}
	for _, a := range e.Attributes {
		for _, v := range a.Values {
			clear(v)
		}
	}
	again, err := EncodeStoredObjectV3(o)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("source alias retained")
	}
}
func TestV3MissingAndObservedZeroAreDistinct(t *testing.T) {
	for _, present := range []bool{false, true} {
		e := entry(1, "user")
		if present {
			e.Attributes = append(e.Attributes, Attribute{"primaryGroupID", [][]byte{[]byte("0")}})
		}
		o, err := DecodeV3(e)
		if err != nil {
			t.Fatal(err)
		}
		p, err := ProjectV3(o)
		if err != nil || p.ServicePrincipalName != nil || (p.PrimaryGroupID != nil) != present || present && *p.PrimaryGroupID != 0 {
			t.Fatal("null/zero changed")
		}
		raw, err := EncodeStoredObjectV3(o)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeStoredObjectV3(raw); err != nil {
			t.Fatal(err)
		}
	}
}
func TestV3PrimaryGroupStrictUnsignedProfile(t *testing.T) {
	for _, v := range []string{"", "00", "01", "+1", "-1", "2147483648 ", " 513", "4294967296", "10000000000", "1.0", "1e2", "１", "513\x00", string([]byte{0xff})} {
		t.Run(fmt.Sprintf("%q", v), func(t *testing.T) {
			e := entry(1, "user")
			e.Attributes = append(e.Attributes, Attribute{"primaryGroupID", [][]byte{[]byte(v)}})
			o, err := DecodeV3(e)
			if err == nil || !reflect.DeepEqual(o, StoredObjectV3{}) {
				t.Fatal("invalid number retained")
			}
		})
	}
	for _, v := range []string{"0", "513", "2147483647", "2147483648", "4294967295"} {
		if _, err := primaryGroupV3([]byte(v)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestV3SPNBoundsDuplicatesAndEncoding(t *testing.T) {
	values := func(n int) [][]byte {
		v := make([][]byte, n)
		for i := range v {
			v[i] = []byte(fmt.Sprintf("HOST/%d", i))
		}
		return v
	}
	for _, tc := range []struct {
		name   string
		values [][]byte
		valid  bool
	}{
		{"one", [][]byte{[]byte("HOST/a")}, true}, {"128", values(128), true}, {"129", values(129), false},
		{"units1024", [][]byte{[]byte(strings.Repeat("😀", 512))}, true}, {"units1025", [][]byte{[]byte(strings.Repeat("😀", 512) + "a")}, false},
		{"bytes4097", [][]byte{bytes.Repeat([]byte("a"), 4097)}, false}, {"exactDuplicate", [][]byte{[]byte("a"), []byte("a")}, false}, {"caseDistinct", [][]byte{[]byte("a"), []byte("A")}, true},
		{"emptyset", [][]byte{}, false}, {"empty", [][]byte{[]byte{}}, false}, {"nilvalue", [][]byte{nil}, false}, {"overlong", [][]byte{{0xc0, 0x80}}, false}, {"surrogate", [][]byte{{0xed, 0xa0, 0x80}}, false}, {"invalid", [][]byte{{0xff}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := entry(1, "user")
			e.Attributes = append(e.Attributes, Attribute{"servicePrincipalName", tc.values})
			o, err := DecodeV3(e)
			if (err == nil) != tc.valid {
				t.Fatal("unexpected validation", err)
			}
			if err != nil && !reflect.DeepEqual(o, StoredObjectV3{}) {
				t.Fatal("partial failure")
			}
		})
	}
	good := make([][]byte, 32)
	for i := range good {
		good[i] = []byte(fmt.Sprintf("%04d", i) + strings.Repeat("x", 1020))
	}
	if err := validateIdentityV3(RawIdentityV3{ServicePrincipalNameBytes: good}); err != nil {
		t.Fatal(err)
	}
	good = append(good, []byte("a"))
	if err := validateIdentityV3(RawIdentityV3{ServicePrincipalNameBytes: good}); err == nil {
		t.Fatal("aggregate bound bypassed")
	}
}
func TestV3StrictStorageFailuresAreAtomic(t *testing.T) {
	o, _ := DecodeV3(entryV3())
	raw, _ := EncodeStoredObjectV3(o)
	for _, mut := range [][]byte{
		append([]byte(" "), raw...), append(bytes.Clone(raw), ' '), append(bytes.Clone(raw), []byte(`{}`)...),
		bytes.Replace(raw, []byte(`"identity":`), []byte(`"unknown":`), 1),
		bytes.Replace(raw, []byte(`"primaryGroupIDBytes":`), []byte(`"primaryGroupIDBytes":null,"primaryGroupIDBytes":`), 1),
		bytes.Replace(raw, []byte(`"servicePrincipalNameBytes":[`), []byte(`"servicePrincipalNameBytes":[null,`), 1),
		bytes.Repeat([]byte{' '}, MaxStoredObjectV3Bytes+1),
	} {
		got, err := DecodeStoredObjectV3(mut)
		if err == nil || !reflect.DeepEqual(got, StoredObjectV3{}) {
			t.Fatal("noncanonical storage accepted")
		}
	}
}
func TestV3AccumulatorAtomicLimitsAndDiscard(t *testing.T) {
	a, err := NewAccumulatorV3(Limits{MaxRows: 2, MaxPages: 2, MaxPageEntries: 2, MaxBytes: 1 << 20, MaxCookieBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AddPage([]Entry{entryV3()}, nil, []byte("next")); err != nil {
		t.Fatal(err)
	}
	alias := a.rows[0].Identity.ServicePrincipalNameBytes[0]
	if err = a.AddPage([]Entry{entryV3()}, []byte("next"), nil); err == nil {
		t.Fatal("duplicate GUID accepted")
	}
	if !bytes.Equal(alias, make([]byte, len(alias))) {
		t.Fatal("retained bytes not scrubbed")
	}
	if _, err = a.Result(); err == nil {
		t.Fatal("failed observation visible")
	}
}
