package directoryassets

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func entryV2(id byte) Entry {
	e := entry(id, "top", "User")
	sid, _ := hex.DecodeString("010200000000000515000000ffffffff")
	e.Attributes = append(e.Attributes,
		Attribute{"sAMAccountName", [][]byte{[]byte("Case.Preserved")}},
		Attribute{"userAccountControl", [][]byte{[]byte("0")}},
		Attribute{"objectSid", [][]byte{sid}},
		Attribute{"mail", [][]byte{[]byte(" A\x00B\r\n\t😀@Example.TEST ")}},
		Attribute{"description", [][]byte{[]byte("\x00\x01\b\f\n\r\t\\u0000 😀 <script>& ")}},
		Attribute{"whenCreated", [][]byte{[]byte("00010101000000.0Z")}},
	)
	return e
}

func objectV2(t *testing.T) StoredObjectV2 {
	t.Helper()
	o, err := DecodeV2(entryV2(1))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func encodedV2(t *testing.T, o StoredObjectV2) []byte {
	t.Helper()
	raw, err := EncodeStoredObjectV2(o)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestV2LosslessStoredAndFlatPublicGolden(t *testing.T) {
	o := objectV2(t)
	raw := encodedV2(t, o)
	if bytes.Contains(raw, []byte(`\u0000`)) || bytes.Contains(raw, []byte{0}) || bytes.Contains(raw, []byte("Example.TEST")) {
		t.Fatal("raw supplemental text escaped into JSONB-facing storage")
	}
	want := `{"base":{"objectGUID":"00000001-0000-0000-0000-000000000000","distinguishedName":"CN=Synthetic,DC=example,DC=test","kind":"user","objectClass":["top","user"],"samAccountName":"Case.Preserved","userAccountControl":0},"supplemental":{"objectSidBytes":"AQIAAAAAAAUVAAAA/////w==","mailBytes":"IEEAQg0KCfCfmIBARXhhbXBsZS5URVNUIA==","descriptionBytes":["AAEIDAoNCVx1MDAwMCDwn5iAIDxzY3JpcHQ+JiA="],"whenCreatedBytes":"MDAwMTAxMDEwMDAwMDAuMFo="}}`
	if string(raw) != want {
		t.Fatalf("stored golden changed: %s", raw)
	}
	decoded, err := DecodeStoredObjectV2(raw)
	if err != nil || !reflect.DeepEqual(decoded, o) {
		t.Fatal("stored raw bytes changed", err)
	}
	public, err := ProjectV2(decoded)
	if err != nil || public.ObjectSID == nil || *public.ObjectSID != "S-1-5-21-4294967295" || public.Mail == nil || *public.Mail != string(o.Supplemental.MailBytes) || len(public.Description) != 1 || public.Description[0] != string(o.Supplemental.DescriptionBytes[0]) || public.WhenCreated == nil || *public.WhenCreated != "0001-01-01T00:00:00Z" {
		t.Fatal("public projection lost facts", err)
	}
	publicJSON, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	wantPublic := `{"objectGUID":"00000001-0000-0000-0000-000000000000","distinguishedName":"CN=Synthetic,DC=example,DC=test","kind":"user","objectClass":["top","user"],"samAccountName":"Case.Preserved","userAccountControl":0,"objectSid":"S-1-5-21-4294967295","mail":" A\u0000B\r\n\t😀@Example.TEST ","description":["\u0000\u0001\b\f\n\r\t\\u0000 😀 \u003cscript\u003e\u0026 "],"whenCreated":"0001-01-01T00:00:00Z"}`
	if string(publicJSON) != wantPublic {
		t.Fatalf("public golden changed: %s", publicJSON)
	}
}

func TestV2MissingFieldsStayNull(t *testing.T) {
	o, err := DecodeV2(entry(1, "group"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(o.Supplemental, RawSupplementalV2{}) {
		t.Fatal("omitted supplemental fields became present")
	}
	public, err := ProjectV2(o)
	if err != nil || public.ObjectSID != nil || public.Mail != nil || public.Description != nil || public.WhenCreated != nil {
		t.Fatal("omission became a default", err)
	}
	raw := encodedV2(t, o)
	if !bytes.Contains(raw, []byte(`"supplemental":{"objectSidBytes":null,"mailBytes":null,"descriptionBytes":null,"whenCreatedBytes":null}`)) {
		t.Fatal("missing raw fields are not explicit nulls")
	}
	decoded, err := DecodeStoredObjectV2(raw)
	if err != nil || !reflect.DeepEqual(decoded, o) {
		t.Fatal("omission failed roundtrip", err)
	}
}

func TestV2FixedAllowlistAndAtomicError(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Entry)
		want error
	}{
		{"unknown", func(e *Entry) { e.Attributes[7].Name = "unicodePwd" }, ErrAttribute},
		{"range", func(e *Entry) { e.Attributes[6].Name = "description;range=0-1" }, ErrAttribute},
		{"binary option", func(e *Entry) { e.Attributes[4].Name = "objectSid;binary" }, ErrAttribute},
		{"Unicode lookalike", func(e *Entry) { e.Attributes[5].Name = "maİl" }, ErrAttribute},
		{"duplicate core", func(e *Entry) { e.Attributes[7].Name = "OBJECTguid" }, ErrAttribute},
		{"duplicate supplemental", func(e *Entry) { e.Attributes[7].Name = "MAIL" }, ErrAttribute},
		{"nine attributes", func(e *Entry) { e.Attributes = append(e.Attributes, Attribute{"extra", nil}) }, ErrEntry},
		{"nil values", func(e *Entry) { e.Attributes[5].Values = nil }, ErrAttribute},
		{"empty scalar", func(e *Entry) { e.Attributes[5].Values = [][]byte{{}} }, ErrAttribute},
		{"empty description", func(e *Entry) { e.Attributes[6].Values = [][]byte{{}} }, ErrAttribute},
		{"SAM descriptions", func(e *Entry) { e.Attributes[6].Values = [][]byte{[]byte("first"), []byte("second")} }, ErrSupplementalLimit},
		{"unknown class", func(e *Entry) { e.Attributes[1].Values = [][]byte{[]byte("contact")} }, ErrKind},
		{"invalid UTF8", func(e *Entry) { e.Attributes[5].Values[0] = []byte{255} }, ErrAttribute},
		{"leap second", func(e *Entry) { e.Attributes[7].Values[0] = []byte("20161231235960.0Z") }, ErrWhenCreatedProfile},
		{"offset profile", func(e *Entry) { e.Attributes[7].Values[0] = []byte("20260101000000+00") }, ErrWhenCreatedProfile},
		{"invalid calendar", func(e *Entry) { e.Attributes[7].Values[0] = []byte("20260230000000.0Z") }, ErrWhenCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := entryV2(1)
			tc.edit(&e)
			o, err := DecodeV2(e)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(o, StoredObjectV2{}) {
				t.Fatal("nonzero or incorrectly classified failure", err)
			}
		})
	}
	e := entryV2(1)
	for i := range e.Attributes {
		e.Attributes[i].Name = strings.ToUpper(e.Attributes[i].Name)
	}
	if _, err := DecodeV2(e); err != nil {
		t.Fatal("ASCII case-insensitivity lost", err)
	}
}

func TestV2SAMCardinalityAppliesToEverySupportedClass(t *testing.T) {
	for _, kind := range []string{"user", "group", "computer"} {
		t.Run(kind, func(t *testing.T) {
			e := entryV2(1)
			e.Attributes[1].Values = [][]byte{[]byte(kind)}
			if kind == "computer" {
				e.Attributes[1].Values = append(e.Attributes[1].Values, []byte("user"))
			}
			if _, err := DecodeV2(e); err != nil {
				t.Fatal("one description rejected", err)
			}
			e.Attributes[6].Values = append(e.Attributes[6].Values, []byte("another description"))
			if _, err := DecodeSupplemental(e.Attributes[4:]); err != nil {
				t.Fatal("generic supplemental cardinality changed", err)
			}
			if o, err := DecodeV2(e); !errors.Is(err, ErrSupplementalLimit) || !reflect.DeepEqual(o, StoredObjectV2{}) {
				t.Fatal("SAM descriptions silently selected or joined", err)
			}
		})
	}
}

func TestV2PreservesLargestSIDAndYear9999(t *testing.T) {
	e := entryV2(1)
	// Five uint32 subauthorities and the largest 48-bit authority.
	sid := append([]byte{1, 5, 255, 255, 255, 255, 255, 255}, bytes.Repeat([]byte{255}, 20)...)
	e.Attributes[4].Values[0] = sid
	e.Attributes[7].Values[0] = []byte("99991231235959.0Z")
	o, err := DecodeV2(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStoredObjectV2(encodedV2(t, o))
	if err != nil || !bytes.Equal(decoded.Supplemental.ObjectSIDBytes, sid) {
		t.Fatal("largest supported SID failed raw roundtrip", err)
	}
	p, err := ProjectV2(decoded)
	if err != nil || p.ObjectSID == nil || *p.ObjectSID != "S-1-0xffffffffffff-4294967295-4294967295-4294967295-4294967295-4294967295" || p.WhenCreated == nil || *p.WhenCreated != "9999-12-31T23:59:59Z" {
		t.Fatal("maximum SID/time projection changed", err)
	}
	sid = append(sid, 255, 255, 255, 255)
	sid[1] = 6
	if _, err := DecodeSID(sid); err != nil {
		t.Fatal("generic SID unexpectedly rejects six subauthorities", err)
	}
	e.Attributes[4].Values[0] = sid
	if _, err := DecodeV2(e); !errors.Is(err, ErrSupplementalLimit) {
		t.Fatal("objectSid wrapper accepted generic-only SID", err)
	}
}

func TestV2OwnsInputCloneProjectionAndStoredDecode(t *testing.T) {
	e := entryV2(1)
	o, err := DecodeV2(e)
	if err != nil {
		t.Fatal(err)
	}
	before := encodedV2(t, o)
	for _, a := range e.Attributes {
		for _, value := range a.Values {
			clear(value)
		}
	}
	if !bytes.Equal(before, encodedV2(t, o)) {
		t.Fatal("LDAP input aliases result")
	}
	cloned := cloneStoredObjectV2(o)
	public, err := ProjectV2(o)
	if err != nil {
		t.Fatal(err)
	}
	cloned.Base.Classes[0] = "tampered"
	*cloned.Base.SAMAccountName = "tampered"
	*cloned.Base.UserAccountControl = 99
	cloned.Supplemental.ObjectSIDBytes[0] = 99
	cloned.Supplemental.MailBytes[0] = 99
	cloned.Supplemental.DescriptionBytes[0][0] = 99
	cloned.Supplemental.WhenCreatedBytes[0] = 99
	public.Classes[0] = "tampered"
	*public.SAMAccountName = "tampered"
	*public.UserAccountControl = 99
	*public.Mail = "tampered"
	public.Description[0] = "tampered"
	if !bytes.Equal(before, encodedV2(t, o)) {
		t.Fatal("clone or public projection aliases source")
	}
	decoded, err := DecodeStoredObjectV2(before)
	if err != nil {
		t.Fatal(err)
	}
	clear(before)
	if !reflect.DeepEqual(decoded, o) {
		t.Fatal("stored JSON input aliases output")
	}
	objects := []StoredObjectV2{o}
	aliases := [][]byte{o.Supplemental.ObjectSIDBytes, o.Supplemental.MailBytes, o.Supplemental.DescriptionBytes[0], o.Supplemental.WhenCreatedBytes}
	ClearStoredObjectsV2(objects)
	for _, alias := range aliases {
		if !bytes.Equal(alias, make([]byte, len(alias))) {
			t.Fatal("owned raw buffer was not cleared")
		}
	}
	if !reflect.DeepEqual(objects[0], StoredObjectV2{}) {
		t.Fatal("cleared object retains values")
	}
}

func TestV2RejectsPresentEmptyValuesEveryPath(t *testing.T) {
	cases := []struct {
		name string
		edit func(*RawSupplementalV2)
	}{
		{"empty SID", func(r *RawSupplementalV2) { r.ObjectSIDBytes = []byte{} }},
		{"empty mail", func(r *RawSupplementalV2) { r.MailBytes = []byte{} }},
		{"empty descriptions", func(r *RawSupplementalV2) { r.DescriptionBytes = [][]byte{} }},
		{"nil description", func(r *RawSupplementalV2) { r.DescriptionBytes = [][]byte{nil} }},
		{"empty description", func(r *RawSupplementalV2) { r.DescriptionBytes = [][]byte{{}} }},
		{"empty time", func(r *RawSupplementalV2) { r.WhenCreatedBytes = []byte{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := objectV2(t)
			tc.edit(&o.Supplemental)
			if p, err := ProjectV2(o); err == nil || !reflect.DeepEqual(p, PublicObjectV2{}) {
				t.Fatal("present-empty public value accepted")
			}
			if raw, err := EncodeStoredObjectV2(o); err == nil || raw != nil {
				t.Fatal("present-empty stored value encoded")
			}
			raw, _ := json.Marshal(o)
			if decoded, err := DecodeStoredObjectV2(raw); err == nil || !reflect.DeepEqual(decoded, StoredObjectV2{}) {
				t.Fatal("present-empty stored input accepted")
			}
		})
	}
}

func TestV2StoredRejectsNoncanonicalOrMalformedJSON(t *testing.T) {
	raw := string(encodedV2(t, objectV2(t)))
	replace := func(old, new string) string { return strings.Replace(raw, old, new, 1) }
	cases := map[string]string{
		"unknown outer":       replace(`{"base":`, `{"extra":null,"base":`),
		"duplicate outer":     replace(`{"base":`, `{"base":null,"base":`),
		"case outer":          replace(`"base":`, `"Base":`),
		"unknown base":        replace(`"objectGUID":`, `"secret":null,"objectGUID":`),
		"duplicate base":      replace(`"kind":"user"`, `"kind":"group","kind":"user"`),
		"case base":           replace(`"kind":`, `"Kind":`),
		"missing base field":  replace(`"userAccountControl":0`, `"omitted":0`),
		"kind mismatch":       replace(`"kind":"user"`, `"kind":"group"`),
		"unsorted class":      replace(`"top","user"`, `"user","top"`),
		"uppercase class":     replace(`"top","user"`, `"top","User"`),
		"duplicate class":     replace(`"top","user"`, `"user","user"`),
		"upper GUID":          replace(`00000001-0000-0000-0000-000000000000`, `0000000A-0000-0000-0000-000000000000`),
		"unknown raw":         replace(`"mailBytes":`, `"extra":null,"mailBytes":`),
		"duplicate raw":       replace(`"mailBytes":`, `"mailBytes":null,"mailBytes":`),
		"missing raw":         replace(`"objectSidBytes":`, `"omitted":`),
		"wrong raw type":      replace(`"objectSidBytes":"AQIAAAAAAAUVAAAA/////w=="`, `"objectSidBytes":42`),
		"numeric byte array":  replace(`"objectSidBytes":"AQIAAAAAAAUVAAAA/////w=="`, `"objectSidBytes":[1,2,3]`),
		"newline base64":      replace(`AQIAAAAAAAUVAAAA/////w==`, `AQIAAAAA\nAAUVAAAA/////w==`),
		"escaped base64":      replace(`AQIAAAAAAAUVAAAA/////w==`, `\u0041QIAAAAAAAUVAAAA/////w==`),
		"nonzero pad bits":    replace(`AQIAAAAAAAUVAAAA/////w==`, `AQIAAAAAAAUVAAAA/////x==`),
		"unpadded base64":     replace(`AQIAAAAAAAUVAAAA/////w==`, `AQIAAAAAAAUVAAAA/////w`),
		"extra padding":       replace(`AQIAAAAAAAUVAAAA/////w==`, `AQIAAAAAAAUVAAAA/////w===`),
		"unknown alphabet":    replace(`AQIAAAAAAAUVAAAA/////w==`, `AQIAAAAAAAUVAAAA_____w==`),
		"leading whitespace":  " " + raw,
		"trailing whitespace": raw + "\n",
		"second JSON object":  raw + `{}`,
		"truncated":           raw[:len(raw)-1],
		"null":                `null`,
		"array":               `[]`,
		"empty":               ``,
		"oversize total":      strings.Repeat(" ", MaxStoredObjectV2Bytes+1),
	}
	for name, mutated := range cases {
		t.Run(name, func(t *testing.T) {
			if mutated == raw {
				t.Fatal("test mutation did not apply")
			}
			out, err := DecodeStoredObjectV2([]byte(mutated))
			if err == nil || !reflect.DeepEqual(out, StoredObjectV2{}) {
				t.Fatal("noncanonical or malformed stored data accepted", err)
			}
		})
	}
}

func TestV2StoredChecksEncodedAndDecodedLimitsBeforeSemanticAcceptance(t *testing.T) {
	o := objectV2(t)
	cases := []struct {
		name string
		edit func(*StoredObjectV2)
	}{
		{"SID decoded 29 shares 40 encoded cap", func(o *StoredObjectV2) { o.Supplemental.ObjectSIDBytes = make([]byte, 29) }},
		{"mail decoded 1025 shares 1368 encoded cap", func(o *StoredObjectV2) { o.Supplemental.MailBytes = bytes.Repeat([]byte{'m'}, 1025) }},
		{"description decoded 4097 shares 5464 encoded cap", func(o *StoredObjectV2) { o.Supplemental.DescriptionBytes = [][]byte{bytes.Repeat([]byte{'d'}, 4097)} }},
		{"time decoded 18 shares 24 encoded cap", func(o *StoredObjectV2) { o.Supplemental.WhenCreatedBytes = []byte("00010101000000.00Z") }},
		{"SID encoded", func(o *StoredObjectV2) { o.Supplemental.ObjectSIDBytes = make([]byte, 31) }},
		{"mail encoded", func(o *StoredObjectV2) { o.Supplemental.MailBytes = bytes.Repeat([]byte{'m'}, 1027) }},
		{"description encoded", func(o *StoredObjectV2) { o.Supplemental.DescriptionBytes = [][]byte{bytes.Repeat([]byte{'d'}, 4099)} }},
		{"time encoded", func(o *StoredObjectV2) { o.Supplemental.WhenCreatedBytes = make([]byte, 19) }},
		{"description count", func(o *StoredObjectV2) { o.Supplemental.DescriptionBytes = [][]byte{[]byte("a"), []byte("b")} }},
		{"base class count", func(o *StoredObjectV2) { o.Base.Classes = make([]string, 33) }},
		{"base oversized string", func(o *StoredObjectV2) { o.Base.DN = strings.Repeat("a", 4097) }},
		{"base oversized raw JSON", func(o *StoredObjectV2) { o.Base.DN = strings.Repeat("a", 32769) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := cloneStoredObjectV2(o)
			tc.edit(&mutated)
			raw, _ := json.Marshal(mutated)
			decoded, err := DecodeStoredObjectV2(raw)
			if err == nil || !reflect.DeepEqual(decoded, StoredObjectV2{}) {
				t.Fatal("stored local bound bypassed")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value []byte
		index int
	}{
		{"mail exact UTF16 BMP", []byte(strings.Repeat("界", 256)), 5},
		{"mail exact UTF16 non-BMP", []byte(strings.Repeat("😀", 128)), 5},
		{"description exact UTF16 BMP", []byte(strings.Repeat("界", 1024)), 6},
		{"description exact UTF16 non-BMP", []byte(strings.Repeat("😀", 512)), 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := entryV2(1)
			e.Attributes[tc.index].Values = [][]byte{tc.value}
			o, err := DecodeV2(e)
			if err != nil {
				t.Fatal("exact UTF16 bound rejected", err)
			}
			if _, err := DecodeStoredObjectV2(encodedV2(t, o)); err != nil {
				t.Fatal("exact bound did not roundtrip", err)
			}
			e.Attributes[tc.index].Values[0] = append(tc.value, 'x')
			if _, err := DecodeV2(e); !errors.Is(err, ErrSupplementalLimit) {
				t.Fatal("UTF16 cap ignored", err)
			}
		})
	}
}

func TestV2FormattingRedactsRawAndPublicValues(t *testing.T) {
	o := objectV2(t)
	public, _ := ProjectV2(o)
	for _, value := range []any{o, o.Supplemental, public} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			got := fmt.Sprintf(format, value)
			if !strings.Contains(got, "redacted") || strings.Contains(got, "Example.TEST") || strings.Contains(got, "Synthetic") || strings.Contains(got, "AQIA") {
				t.Fatal("aggregate formatter exposes data")
			}
		}
	}
}

func FuzzDecodeStoredObjectV2(f *testing.F) {
	o, _ := DecodeV2(entryV2(1))
	raw, _ := EncodeStoredObjectV2(o)
	f.Add(raw)
	f.Add([]byte(`{"base":null,"supplemental":null}`))
	f.Add([]byte(`{"base":{},"base":{},"supplemental":{}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxStoredObjectV2Bytes+1 {
			return
		}
		o, err := DecodeStoredObjectV2(raw)
		if err != nil {
			if !reflect.DeepEqual(o, StoredObjectV2{}) {
				t.Fatal("failed stored decode returned data")
			}
			return
		}
		encoded, err := EncodeStoredObjectV2(o)
		if err != nil || !bytes.Equal(encoded, raw) {
			t.Fatal("accepted noncanonical stored data")
		}
		if _, err := ProjectV2(o); err != nil {
			t.Fatal("accepted semantically invalid stored data")
		}
	})
}

func FuzzDecodeV2RawSupplemental(f *testing.F) {
	f.Add([]byte(" A\x00😀 "), []byte("\x00\r\n\t😀"), []byte("00010101000000.0Z"))
	f.Add([]byte{255}, []byte{}, []byte("20161231235960.0Z"))
	f.Fuzz(func(t *testing.T, mail, description, created []byte) {
		if len(mail) > 1025 || len(description) > 4097 || len(created) > 18 {
			return
		}
		e := entryV2(1)
		e.Attributes[5].Values[0], e.Attributes[6].Values[0], e.Attributes[7].Values[0] = mail, description, created
		o, err := DecodeV2(e)
		if err != nil {
			if !reflect.DeepEqual(o, StoredObjectV2{}) {
				t.Fatal("failed raw decode returned data")
			}
			return
		}
		encoded, err := EncodeStoredObjectV2(o)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeStoredObjectV2(encoded)
		if err != nil || !reflect.DeepEqual(o, decoded) || !bytes.Equal(decoded.Supplemental.MailBytes, mail) || !bytes.Equal(decoded.Supplemental.DescriptionBytes[0], description) || !bytes.Equal(decoded.Supplemental.WhenCreatedBytes, created) {
			t.Fatal("raw facts did not roundtrip", err)
		}
		if !bytes.Contains(encoded, []byte(base64.StdEncoding.EncodeToString(mail))) {
			t.Fatal("mail raw bytes not stored as base64")
		}
	})
}
