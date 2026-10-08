package directoryassets

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func supplementalHex(t testing.TB, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func supplementalAttribute(name string, values ...string) Attribute {
	a := Attribute{Name: name, Values: make([][]byte, len(values))}
	for i, value := range values {
		a.Values[i] = []byte(value)
	}
	return a
}

func assertSupplementalZero(t testing.TB, got Supplemental) {
	t.Helper()
	if got.ObjectSID != nil || got.Mail != nil || got.Descriptions != nil || got.WhenCreated != nil {
		t.Fatal("failure or absence returned populated supplemental fields")
	}
}

func TestSIDGoldenBinaryAndCanonicalText(t *testing.T) {
	for _, tc := range []struct{ name, hex, want string }{
		{"domain principal", "010500000000000515000000010000000200000003000000e8030000", "S-1-5-21-1-2-3-1000"},
		{"builtin group", "01020000000000052000000020020000", "S-1-5-32-544"},
		{"zero authority and subauthority", "010100000000000000000000", "S-1-0-0"},
		{"unsigned subauthorities", "0102000000000005ffffffff00000080", "S-1-5-4294967295-2147483648"},
		{"decimal authority boundary", "01010000ffffffff01000000", "S-1-4294967295-1"},
		{"hex authority boundary", "010100010000000001000000", "S-1-0x000100000000-1"},
		{"largest authority and byte order", "0101ffffffffffff04030201", "S-1-0xffffffffffff-16909060"},
		{"fifteen subauthorities", "010f000000000005" + strings.Repeat("ffffffff", 15), "S-1-5" + strings.Repeat("-4294967295", 15)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := supplementalHex(t, tc.hex)
			got, err := DecodeSID(raw)
			if err != nil || got != tc.want {
				t.Fatalf("SID differs: %q, %v", got, err)
			}
			clear(raw)
			if got != tc.want {
				t.Fatal("SID result aliases input")
			}
		})
	}
}

func TestSIDMalformedAndAttributeProfile(t *testing.T) {
	valid := supplementalHex(t, "010100000000000501000000")
	for _, raw := range [][]byte{
		nil, {}, make([]byte, 7), valid[:11], append(bytes.Clone(valid), 0),
		append([]byte{0}, valid[1:]...), append([]byte{2}, valid[1:]...),
		append([]byte{1, 2}, valid[2:]...), append([]byte{1, 255}, valid[2:]...),
		supplementalHex(t, "0110000000000005"+strings.Repeat("00000000", 16)),
		[]byte("S-1-5-32-544"),
	} {
		got, err := DecodeSID(raw)
		if !errors.Is(err, ErrSID) || got != "" {
			t.Fatal("malformed SID was not rejected safely", err)
		}
	}
	zero := supplementalHex(t, "0100000000000005")
	if got, err := DecodeSID(zero); got != "" || !errors.Is(err, ErrSIDProfile) {
		t.Fatal("zero-subauthority SID must be outside the text profile", err)
	}
	for _, tc := range []struct {
		count int
		want  error
	}{{0, ErrSIDProfile}, {1, nil}, {5, nil}, {6, ErrSupplementalLimit}, {15, ErrSupplementalLimit}} {
		raw := supplementalHex(t, fmt.Sprintf("01%02x000000000005", tc.count)+strings.Repeat("01000000", tc.count))
		got, err := DecodeSupplemental([]Attribute{{Name: "objectSid", Values: [][]byte{raw}}})
		if !errors.Is(err, tc.want) {
			t.Fatalf("objectSid profile count %d: %v", tc.count, err)
		}
		if err != nil {
			assertSupplementalZero(t, got)
		} else if got.ObjectSID == nil || *got.ObjectSID != "S-1-5"+strings.Repeat("-1", tc.count) {
			t.Fatal("objectSid projection differs")
		}
	}
}

func TestSupplementalAbsentAndCompleteOwnedProjection(t *testing.T) {
	for _, attributes := range [][]Attribute{nil, {}} {
		got, err := DecodeSupplemental(attributes)
		if err != nil {
			t.Fatal(err)
		}
		assertSupplementalZero(t, got)
	}
	attributes := []Attribute{
		supplementalAttribute("WhEnCrEaTeD", "20240229123456.0Z"),
		supplementalAttribute("DeScRiPtIoN", "zeta", "Alpha", "alpha", " \r\n\t\x00\x7f\u0085 "),
		supplementalAttribute("MaIl", " Mixed.Case,not-an-address \r\n\t\x00\x7f\u0085 "),
		{Name: "ObJeCtSiD", Values: [][]byte{supplementalHex(t, "01020000000000052000000020020000")}},
	}
	got, err := DecodeSupplemental(attributes)
	if err != nil {
		t.Fatal(err)
	}
	wantDescriptions := []string{" \r\n\t\x00\x7f\u0085 ", "Alpha", "alpha", "zeta"}
	if got.ObjectSID == nil || *got.ObjectSID != "S-1-5-32-544" || got.Mail == nil || *got.Mail != string(attributes[2].Values[0]) || !reflect.DeepEqual(got.Descriptions, wantDescriptions) {
		t.Fatal("exact text, multiplicity or SID projection differs")
	}
	wantTime := time.Date(2024, time.February, 29, 12, 34, 56, 0, time.UTC)
	if got.WhenCreated == nil || *got.WhenCreated != wantTime {
		t.Fatal("creation instant differs")
	}
	if string(attributes[1].Values[0]) != "zeta" {
		t.Fatal("description sorting modified caller order")
	}
	mail := *got.Mail
	for i := range attributes {
		attributes[i].Name = "changed"
		for _, value := range attributes[i].Values {
			clear(value)
		}
		clear(attributes[i].Values)
	}
	if *got.Mail != mail || *got.ObjectSID != "S-1-5-32-544" || !reflect.DeepEqual(got.Descriptions, wantDescriptions) || *got.WhenCreated != wantTime {
		t.Fatal("successful result aliases caller data")
	}
}

func TestSupplementalDescriptorsAndCardinality(t *testing.T) {
	for _, name := range []string{"", "mAİl", "deſcription", "мail", "mail ", " mail", "mail\x00", "mail;binary", "description;range=0-*", "*", "+", "1.2.3", "whenChanged", "objectGUID", "unicodePwd", strings.Repeat("a", 10000)} {
		got, err := DecodeSupplemental([]Attribute{supplementalAttribute(name, "do-not-echo-secret")})
		if !errors.Is(err, ErrAttribute) {
			t.Fatalf("invalid descriptor accepted or misclassified: %v", err)
		}
		assertSupplementalZero(t, got)
	}
	for _, name := range []string{"objectSid", "mail", "description", "whenCreated"} {
		for _, values := range [][][]byte{nil, {}} {
			got, err := DecodeSupplemental([]Attribute{{Name: name, Values: values}})
			if !errors.Is(err, ErrAttribute) {
				t.Fatal("zero-value attribute accepted", err)
			}
			assertSupplementalZero(t, got)
		}
		got, err := DecodeSupplemental([]Attribute{supplementalAttribute(name, "a"), supplementalAttribute(strings.ToUpper(name), "b")})
		if !errors.Is(err, ErrAttribute) {
			t.Fatal("case-folded duplicate descriptor accepted", err)
		}
		assertSupplementalZero(t, got)
		if name != "description" {
			got, err = DecodeSupplemental([]Attribute{supplementalAttribute(name, "a", "b")})
			if !errors.Is(err, ErrAttribute) {
				t.Fatal("multivalued single attribute accepted", err)
			}
			assertSupplementalZero(t, got)
		}
	}
	got, err := DecodeSupplemental(make([]Attribute, 5))
	if !errors.Is(err, ErrSupplementalLimit) {
		t.Fatal("attribute count was not bounded", err)
	}
	assertSupplementalZero(t, got)
}

func TestSupplementalTextUTF16AndByteBounds(t *testing.T) {
	for _, field := range []struct {
		name     string
		maxUnits int
		maxBytes int
	}{{"mail", 256, 1024}, {"description", 1024, 4096}} {
		for _, tc := range []struct {
			name, text string
			want       error
		}{
			{"empty", "", ErrAttribute},
			{"invalid UTF8", "\xff", ErrAttribute},
			{"overlong UTF8", "\xc0\x80", ErrAttribute},
			{"surrogate UTF8", "\xed\xa0\x80", ErrAttribute},
			{"replacement rune", "\ufffd", nil},
			{"space", " ", nil},
			{"controls", "\x00\x01\t\n\r\x7f\u0085", nil},
			{"ASCII at limit", strings.Repeat("a", field.maxUnits), nil},
			{"ASCII over units", strings.Repeat("a", field.maxUnits+1), ErrSupplementalLimit},
			{"BMP at limit", strings.Repeat("\u0800", field.maxUnits), nil},
			{"BMP over units", strings.Repeat("\u0800", field.maxUnits+1), ErrSupplementalLimit},
			{"supplementary at limit", strings.Repeat("😀", field.maxUnits/2), nil},
			{"supplementary over units", strings.Repeat("😀", field.maxUnits/2+1), ErrSupplementalLimit},
			{"mixed at limit", strings.Repeat("😀", field.maxUnits/2-1) + "ab", nil},
			{"mixed over units", strings.Repeat("😀", field.maxUnits/2-1) + "abc", ErrSupplementalLimit},
			{"at byte bound malformed", strings.Repeat("\xff", field.maxBytes), ErrAttribute},
			{"over byte bound malformed", strings.Repeat("\xff", field.maxBytes+1), ErrSupplementalLimit},
		} {
			t.Run(field.name+"/"+tc.name, func(t *testing.T) {
				got, err := DecodeSupplemental([]Attribute{supplementalAttribute(field.name, tc.text)})
				if !errors.Is(err, tc.want) {
					t.Fatalf("text outcome: got %v, want %v", err, tc.want)
				}
				if err != nil {
					assertSupplementalZero(t, got)
				} else if field.name == "mail" {
					if got.Mail == nil || *got.Mail != tc.text {
						t.Fatal("mail changed")
					}
				} else if !reflect.DeepEqual(got.Descriptions, []string{tc.text}) {
					t.Fatal("description changed")
				}
			})
		}
	}
}

func TestSupplementalDescriptionMultiplicity(t *testing.T) {
	values := make([]string, 16)
	for i := range values {
		values[i] = strings.Repeat("\u0800", 1023) + string(rune('z'-i))
	}
	got, err := DecodeSupplemental([]Attribute{supplementalAttribute("description", values...), supplementalAttribute("mail", strings.Repeat("\u0800", 256))})
	if err != nil || len(got.Descriptions) != 16 || !slices.IsSorted(got.Descriptions) {
		t.Fatal("bounded complete description set failed", err)
	}
	slices.Sort(values)
	if !reflect.DeepEqual(got.Descriptions, values) {
		t.Fatal("description set lost data")
	}
	for _, tc := range []struct {
		values []string
		want   error
	}{
		{append(slices.Clone(values), "seventeenth"), ErrSupplementalLimit},
		{[]string{"same", "other", "same"}, ErrAttribute},
		{[]string{"a", ""}, ErrAttribute},
		{[]string{"a", "\xff"}, ErrAttribute},
		{[]string{"a", strings.Repeat("b", 4097)}, ErrSupplementalLimit},
	} {
		got, err := DecodeSupplemental([]Attribute{supplementalAttribute("description", tc.values...)})
		if !errors.Is(err, tc.want) {
			t.Fatal("bad description set accepted or misclassified", err)
		}
		assertSupplementalZero(t, got)
	}
	// Exact byte duplicates only: neither case folding nor Unicode normalization.
	got, err = DecodeSupplemental([]Attribute{supplementalAttribute("description", "a", "A", "é", "e\u0301")})
	if err != nil || len(got.Descriptions) != 4 {
		t.Fatal("distinct descriptions were normalized or discarded", err)
	}
}

func TestSupplementalCreationTimeProfile(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"20010928060000.0Z", "2001-09-28T06:00:00Z"},
		{"20240229123456.0Z", "2024-02-29T12:34:56Z"},
		{"20000229000000.0Z", "2000-02-29T00:00:00Z"},
		{"00010101000000.0Z", "0001-01-01T00:00:00Z"},
		{"99991231235959.0Z", "9999-12-31T23:59:59Z"},
		{"19700101000000.0Z", "1970-01-01T00:00:00Z"},
	} {
		got, err := DecodeSupplemental([]Attribute{supplementalAttribute("whenCreated", tc.raw)})
		if err != nil || got.WhenCreated == nil || got.WhenCreated.Format(time.RFC3339Nano) != tc.want || got.WhenCreated.Location() != time.UTC || got.WhenCreated.Nanosecond() != 0 {
			t.Fatal("creation time changed or defaulted", err)
		}
		if strings.HasPrefix(tc.raw, "0001") && !got.WhenCreated.IsZero() {
			t.Fatal("minimum observed instant differs from Go zero time")
		}
	}
	for _, raw := range []string{
		"", "00000101000000.0Z", "20230229000000.0Z", "19000229000000.0Z", "20240431000000.0Z", "20240100000000.0Z", "20240132000000.0Z", "20240001000000.0Z", "20241301000000.0Z", "20240101240000.0Z", "20240101006000.0Z", "20240101000061.0Z", "20240101000099.0Z", "202A0101000000.0Z", "20240101 00000.0Z", "2024010100000\xff.0Z", "20230229235960.0Z",
	} {
		got, err := DecodeSupplemental([]Attribute{supplementalAttribute("whenCreated", raw)})
		if !errors.Is(err, ErrWhenCreated) {
			t.Fatalf("supported-shape malformed date misclassified: %q, %v", raw, err)
		}
		assertSupplementalZero(t, got)
	}
	for _, raw := range []string{
		"20240101000000.0+0200", "20240101000000.0-0200", "20240101000000.1Z", "20240101000000.00Z", "20240101000000,0Z", "20240101000000Z", "202401010000Z", "2024010100Z", "20240101000000.0z", " 20240101000000.0Z", "20240101000000.0Z ", "100000101000000.0Z", "２０２４0101000000.0Z", "20161231235960.0Z", "20240101000060.0Z",
	} {
		got, err := DecodeSupplemental([]Attribute{supplementalAttribute("whenCreated", raw)})
		if !errors.Is(err, ErrWhenCreatedProfile) {
			t.Fatalf("unsupported time representation misclassified: %q, %v", raw, err)
		}
		assertSupplementalZero(t, got)
	}
}

func TestSupplementalFailuresAreAtomicBoundedAndRedacted(t *testing.T) {
	const secret = "synthetic-private-marker"
	good := []Attribute{
		{Name: "objectSid", Values: [][]byte{supplementalHex(t, "010100000000000501000000")}},
		supplementalAttribute("mail", secret),
		supplementalAttribute("description", secret),
		supplementalAttribute("whenCreated", "20240101000000.0Z"),
	}
	for i := range good {
		bad := slices.Clone(good)
		bad[i].Values = [][]byte{nil}
		for range len(bad) {
			got, err := DecodeSupplemental(bad)
			if err == nil {
				t.Fatal("bad field accepted")
			}
			assertSupplementalZero(t, got)
			if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), secret) {
				t.Fatal("error leaked input")
			}
			bad = append(bad[1:], bad[0])
		}
	}
	got, err := DecodeSupplemental(good)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{got, &got, good, &good[1]} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if strings.Contains(fmt.Sprintf(format, value), secret) {
				t.Fatal("formatting leaked input")
			}
		}
	}
	// A later oversized field is rejected in preflight, before the earlier
	// successful mail could be copied. Count bounds likewise precede iteration.
	for _, oversized := range [][]Attribute{
		{supplementalAttribute("mail", secret), supplementalAttribute("description", strings.Repeat("x", 4097))},
		{supplementalAttribute("mail", secret), supplementalAttribute("description", strings.Repeat("😀", 513))},
		{supplementalAttribute("mail", secret), {Name: "description", Values: make([][]byte, 10000)}},
		make([]Attribute, 10000),
	} {
		allocations := testing.AllocsPerRun(100, func() {
			got, err := DecodeSupplemental(oversized)
			if err != ErrSupplementalLimit || got.Mail != nil {
				panic("unexpected bounded result")
			}
		})
		if allocations != 0 {
			t.Fatalf("oversized input allocated before rejection: %v", allocations)
		}
	}
}

func FuzzDecodeSIDProfile(f *testing.F) {
	for _, seed := range []string{"010500000000000515000000010000000200000003000000e8030000", "0101ffffffffffffffffffff", "0100000000000005", "01", "010f000000000005" + strings.Repeat("ffffffff", 15)} {
		f.Add(supplementalHex(f, seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 256 {
			return
		}
		got, err := DecodeSID(raw)
		if err != nil {
			if got != "" || err != ErrSID && err != ErrSIDProfile {
				t.Fatal("SID error returned unsafe or partial result")
			}
			return
		}
		parts := strings.Split(got, "-")
		if len(raw) < 12 || len(raw) > 68 || raw[0] != 1 || len(parts) != 3+int(raw[1]) || len(raw) != 8+4*int(raw[1]) || parts[0] != "S" || parts[1] != "1" {
			t.Fatal("successful SID violated structural profile")
		}
		authority, parseErr := strconv.ParseUint(parts[2], 0, 48)
		if parseErr != nil {
			t.Fatal("unparseable authority")
		}
		for i := 7; i >= 2; i-- {
			if byte(authority) != raw[i] {
				t.Fatal("authority byte order differs")
			}
			authority >>= 8
		}
		for i, part := range parts[3:] {
			n, parseErr := strconv.ParseUint(part, 10, 32)
			if parseErr != nil || strconv.FormatUint(n, 10) != part {
				t.Fatal("noncanonical subauthority")
			}
			for j := range 4 {
				if byte(n>>(8*j)) != raw[8+4*i+j] {
					t.Fatal("subauthority byte order differs")
				}
			}
		}
	})
}

func FuzzDecodeSupplementalProfile(f *testing.F) {
	f.Add("mail", []byte(" Mixed@example.test "), uint8(1))
	f.Add("description", []byte("\x00\r\n😀"), uint8(2))
	f.Add("whenCreated", []byte("00010101000000.0Z"), uint8(1))
	f.Add("whenCreated", []byte("20230229000000.0Z"), uint8(1))
	f.Add("whenCreated", []byte("20161231235960.0Z"), uint8(1))
	f.Add("objectSid", supplementalHex(f, "01020000000000052000000020020000"), uint8(1))
	f.Add("mAİl", []byte{255}, uint8(0))
	f.Fuzz(func(t *testing.T, name string, raw []byte, count uint8) {
		if len(name) > 128 || len(raw) > 8192 {
			return
		}
		values := make([][]byte, int(count)%20)
		for i := range values {
			values[i] = raw
		}
		got, err := DecodeSupplemental([]Attribute{{Name: name, Values: values}})
		if err != nil {
			assertSupplementalZero(t, got)
			return
		}
		if len(values) != 1 {
			t.Fatal("repeated identical values unexpectedly accepted")
		}
		switch strings.ToLower(name) {
		case "mail":
			if got.Mail == nil || *got.Mail != string(raw) || !utf8.ValidString(*got.Mail) || len(utf16.Encode([]rune(*got.Mail))) > 256 {
				t.Fatal("invalid successful mail projection")
			}
		case "description":
			if len(got.Descriptions) != 1 || got.Descriptions[0] != string(raw) || !utf8.ValidString(got.Descriptions[0]) || len(utf16.Encode([]rune(got.Descriptions[0]))) > 1024 {
				t.Fatal("invalid successful description projection")
			}
		case "objectsid":
			if got.ObjectSID == nil || len(raw) > 28 || len(raw) < 12 {
				t.Fatal("invalid successful objectSid projection")
			}
		case "whencreated":
			if got.WhenCreated == nil || got.WhenCreated.Year() < 1 || got.WhenCreated.Year() > 9999 || got.WhenCreated.Format("20060102150405.0Z") != string(raw) || got.WhenCreated.Location() != time.UTC {
				t.Fatal("invalid successful creation time projection")
			}
		default:
			t.Fatal("unknown descriptor accepted")
		}
	})
}
