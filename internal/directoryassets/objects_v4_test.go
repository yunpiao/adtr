package directoryassets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func memberEntryV4(values ...string) Entry {
	e := entry(1, "group")
	a := Attribute{Name: "member", Values: make([][]byte, 0, len(values))}
	for _, v := range values {
		a.Values = append(a.Values, []byte(v))
	}
	e.Attributes = append(e.Attributes, a)
	return e
}
func TestV4MissingEmptyAndRawMembersStayDistinct(t *testing.T) {
	for _, e := range []Entry{entry(1, "group"), memberEntryV4(), memberEntryV4("CN=foreign,DC=other", " cn=X\x00\n😀 ", "CN=Foreign,DC=other")} {
		o, err := DecodeV4(e)
		if err != nil {
			t.Fatal(err)
		}
		defer ClearStoredObjectsV4([]StoredObjectV4{o})
		raw, err := EncodeStoredObjectV4(o)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeStoredObjectV4(raw)
		if err != nil || !reflect.DeepEqual(got, o) {
			t.Fatal("roundtrip", err)
		}
		defer ClearStoredObjectsV4([]StoredObjectV4{got})
		p, err := ProjectV4(got)
		if err != nil {
			t.Fatal(err)
		}
		if o.Membership.MemberBytes == nil {
			if p.RawMember != nil || p.ReturnedMemberCount != nil || p.MemberReadState != "not_returned" {
				t.Fatal("missing changed")
			}
		} else {
			if p.RawMember == nil || p.ReturnedMemberCount == nil || *p.ReturnedMemberCount != len(o.Membership.MemberBytes) || p.MemberReadState != "returned" {
				t.Fatal("returned changed")
			}
			for i, v := range o.Membership.MemberBytes {
				if p.RawMember[i] != string(v) {
					t.Fatal("raw value changed")
				}
			}
		}
		if bytes.Contains(raw, []byte(`\u0000`)) {
			t.Fatal("NUL stored outside base64")
		}
		public, _ := json.Marshal(p)
		if !bytes.Contains(public, []byte(`"memberReadState":`)) || bytes.Contains(public, []byte(`"memberCount":`)) {
			t.Fatal("projection keys")
		}
		for _, a := range e.Attributes {
			for _, v := range a.Values {
				clear(v)
			}
		}
		again, err := EncodeStoredObjectV4(o)
		if err != nil || !bytes.Equal(raw, again) {
			t.Fatal("source alias")
		}
		if _, err = DecodeStoredObjectV3(raw); err == nil {
			t.Fatal("v3 accepts v4")
		}
	}
	old, err := DecodeV3(entry(1, "group"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := EncodeStoredObjectV3(old)
	if _, err = DecodeStoredObjectV4(raw); err == nil {
		t.Fatal("v3 upgraded")
	}
}
func TestV4MembershipRejectsMalformedForeignAndAllRanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    Entry
		want error
	}{
		{"emptyvalue", memberEntryV4(""), ErrAttribute},
		{"utf8", memberEntryV4(string([]byte{255})), ErrAttribute},
		{"duplicate", memberEntryV4("x", "x"), ErrAttribute},
		{"pervalue", memberEntryV4(strings.Repeat("x", 4097)), ErrMembershipProfile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := DecodeV4(tc.e)
			if err != tc.want || !reflect.DeepEqual(o, StoredObjectV4{}) {
				t.Fatalf("got %v", err)
			}
		})
	}
	for _, kind := range []string{"user", "computer", "foreignSecurityPrincipal"} {
		for _, values := range [][][]byte{nil, {}, {[]byte("x")}} {
			e := entry(1, kind)
			e.Attributes = append(e.Attributes, Attribute{"member", values})
			if _, err := DecodeV4(e); err != ErrMembershipProfile {
				t.Fatalf("foreign %s: %v", kind, err)
			}
		}
	}
	for _, name := range []string{"member;range=0-*", "MEMBER;RANGE=0-1499", "member;range=1500-*", "member;range=oops", "member;binary;range=0-*"} {
		e := memberEntryV4()
		e.Attributes = append(e.Attributes, Attribute{name, [][]byte{[]byte("x")}})
		if _, err := DecodeV4(e); err != ErrMembershipProfile {
			t.Fatal(name, err)
		}
	}
	for _, name := range []string{"member;binary", "memberCount", "memberOf", "membér", "member;range", "member;"} {
		e := entry(1, "group")
		e.Attributes = append(e.Attributes, Attribute{name, [][]byte{[]byte("x")}})
		if _, err := DecodeV4(e); err == nil {
			t.Fatal(name)
		}
	}
	e := memberEntryV4()
	e.Attributes = append(e.Attributes, Attribute{"MEMBER", nil})
	if _, err := DecodeV4(e); err != ErrAttribute {
		t.Fatal("alias", err)
	}
}
func TestV4ExactMemberBudgets(t *testing.T) {
	values := make([]string, 256)
	for i := range values {
		values[i] = fmt.Sprintf("%0256d", i)
	}
	for _, v := range [][]string{{strings.Repeat("😀", 1024)}, values} {
		if _, err := DecodeV4(memberEntryV4(v...)); err != nil {
			t.Fatal("boundary", err)
		}
	}
	values[255] += "x"
	if _, err := DecodeV4(memberEntryV4(values...)); err != ErrMembershipProfile {
		t.Fatal("aggregate", err)
	}
	values = make([]string, 257)
	for i := range values {
		values[i] = fmt.Sprint(i)
	}
	if _, err := DecodeV4(memberEntryV4(values...)); err != ErrMembershipProfile {
		t.Fatal("count", err)
	}
}
func TestV4StoredClosedCanonicalMembershipAndClear(t *testing.T) {
	o, err := DecodeV4(memberEntryV4("secret\x00member"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := EncodeStoredObjectV4(o)
	for _, s := range []string{
		strings.Replace(string(raw), `"memberBytes":`, `"unknown":`, 1),
		strings.Replace(string(raw), `"membership":{`, `"membership":{"extra":null,`, 1),
		strings.Replace(string(raw), `"membership":{`, `"membership":null,"membership":{`, 1),
		strings.Replace(string(raw), `"memberBytes":["c2VjcmV0AG1lbWJlcg=="]`, `"memberBytes":[null]`, 1),
		strings.Replace(string(raw), `"memberBytes":["c2VjcmV0AG1lbWJlcg=="]`, `"memberBytes":[""]`, 1),
		string(raw) + " ", string(raw[:len(raw)-1]),
	} {
		if got, err := DecodeStoredObjectV4([]byte(s)); err == nil || !reflect.DeepEqual(got, StoredObjectV4{}) {
			t.Fatal("accepted bad storage", s)
		}
	}
	owned := o.Membership.MemberBytes[0]
	list := []StoredObjectV4{o}
	ClearStoredObjectsV4(list)
	if !bytes.Equal(owned, make([]byte, len(owned))) || !reflect.DeepEqual(list[0], StoredObjectV4{}) {
		t.Fatal("uncleared ownership")
	}
}
func TestV4AccumulatorOwnershipAtomicFailureAndNilEmpty(t *testing.T) {
	limits := Limits{MaxRows: 10, MaxPages: 5, MaxPageEntries: 5, MaxBytes: 1 << 20, MaxCookieBytes: 10}
	a, _ := NewAccumulatorV4(limits)
	defer a.Discard()
	e := memberEntryV4("raw")
	if err := a.AddPage([]Entry{e}, nil, []byte("cookie")); err != nil {
		t.Fatal(err)
	}
	for _, attr := range e.Attributes {
		for _, v := range attr.Values {
			clear(v)
		}
	}
	next := memberEntryV4()
	next.Attributes[0].Values[0][0] = 2
	if err := a.AddPage([]Entry{next}, []byte("cookie"), nil); err != nil {
		t.Fatal(err)
	}
	result, err := a.Result()
	if err != nil || string(result[0].Membership.MemberBytes[0]) != "raw" || result[1].Membership.MemberBytes == nil {
		t.Fatal("ownership/null", err)
	}
	a.Discard()
	if string(result[0].Membership.MemberBytes[0]) != "raw" {
		t.Fatal("result aliased")
	}
	ClearStoredObjectsV4(result)
	for _, bad := range []Entry{memberEntryV4("x", "x"), entry(1, "user")} {
		b, _ := NewAccumulatorV4(limits)
		if err := b.AddPage([]Entry{memberEntryV4("secret")}, nil, []byte("c")); err != nil {
			t.Fatal(err)
		}
		alias := b.rows[0].Membership.MemberBytes[0]
		if err := b.AddPage([]Entry{bad}, []byte("c"), nil); err == nil {
			t.Fatal("accepted failing page")
		}
		if got, err := b.Result(); err == nil || got != nil || !bytes.Equal(alias, make([]byte, len(alias))) {
			t.Fatal("partial failure retained")
		}
	}
}

func TestV4StoredMemberLimitsBeforeDecodeAndCanonicalBase64(t *testing.T) {
	o, err := DecodeV4(memberEntryV4())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := EncodeStoredObjectV4(o)
	values := make([][]byte, 257)
	for i := range values {
		values[i] = []byte(fmt.Sprint(i))
	}
	huge := make([][]byte, 17)
	for i := range huge {
		huge[i] = []byte(fmt.Sprintf("%04096d", i))
	}
	for _, members := range [][][]byte{values, huge, {bytes.Repeat([]byte("a"), 4097)}, {[]byte{255}}, {[]byte("same"), []byte("same")}} {
		field, _ := json.Marshal(members)
		candidate := bytes.Replace(raw, []byte(`"memberBytes":[]`), append([]byte(`"memberBytes":`), field...), 1)
		got, err := DecodeStoredObjectV4(candidate)
		if err == nil || !reflect.DeepEqual(got, StoredObjectV4{}) {
			t.Fatal("unbounded/invalid member storage")
		}
	}
	for _, replacement := range []string{`["eB=="]`, `["eA="]`, `["eA==",null]`, `{}`, `""`} {
		candidate := strings.Replace(string(raw), `"memberBytes":[]`, `"memberBytes":`+replacement, 1)
		if _, err := DecodeStoredObjectV4([]byte(candidate)); err == nil {
			t.Fatal("noncanonical base64/container accepted")
		}
	}
	for _, size := range []int{0, MaxStoredObjectV4Bytes + 1} {
		if got, err := DecodeStoredObjectV4(make([]byte, size)); err == nil || !reflect.DeepEqual(got, StoredObjectV4{}) {
			t.Fatal("stored envelope cap")
		}
	}
}

func TestV4ResultMemberCopiesAreIndependent(t *testing.T) {
	a, _ := NewAccumulatorV4(limits())
	defer a.Discard()
	if err := a.AddPage([]Entry{memberEntryV4("owned")}, nil, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := a.Result()
	first[0].Membership.MemberBytes[0][0] = 'X'
	second, _ := a.Result()
	if string(second[0].Membership.MemberBytes[0]) != "owned" {
		t.Fatal("result member alias")
	}
	ClearStoredObjectsV4(first)
	if string(second[0].Membership.MemberBytes[0]) != "owned" {
		t.Fatal("clear member alias")
	}
	ClearStoredObjectsV4(second)
}
