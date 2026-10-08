package ldapconnection

import (
	"bytes"
	"context"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestDirectoryV2EntryBoundsAndErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs [][]byte
		code  Code
	}{
		{"unknown", [][]byte{fixtureAttribute("displayName", "private")}, CodeInvalidResponse},
		{"non-ascii", [][]byte{fixtureAttribute("maİl", "private")}, CodeInvalidResponse},
		{"binary-option", [][]byte{fixtureAttribute("objectSid;binary", "private")}, CodeInvalidResponse},
		{"range-option", [][]byte{fixtureAttribute("description;range=0-*", "private")}, CodeInvalidResponse},
		{"duplicate-core", [][]byte{fixtureAttribute("OBJECTguid", strings.Repeat("x", 16))}, CodeInvalidResponse},
		{"duplicate-case", [][]byte{fixtureAttribute("MAIL", "one"), fixtureAttribute("mail", "two")}, CodeInvalidResponse},
		{"ninth-descriptor", append(fixtureV2Supplemental(), fixtureAttribute("other", "private")), CodeInvalidResponse},
		{"empty-set", [][]byte{fixtureAttribute("mail")}, CodeInvalidResponse},
		{"empty-mail", [][]byte{fixtureAttribute("mail", "")}, CodeInvalidResponse},
		{"empty-time", [][]byte{fixtureAttribute("whenCreated", "")}, CodeInvalidResponse},
		{"two-mail", [][]byte{fixtureAttribute("mail", "one", "two")}, CodeInvalidResponse},
		{"two-descriptions", [][]byte{fixtureAttribute("description", "one", "two")}, CodeUnsupportedProfile},
		{"mail-byte-limit", [][]byte{fixtureAttribute("mail", strings.Repeat("x", 1025))}, CodeUnsupportedProfile},
		{"mail-units-limit", [][]byte{fixtureAttribute("mail", strings.Repeat("🧪", 129))}, CodeUnsupportedProfile},
		{"description-byte-limit", [][]byte{fixtureAttribute("description", strings.Repeat("x", 4097))}, CodeUnsupportedProfile},
		{"description-units-limit", [][]byte{fixtureAttribute("description", strings.Repeat("x", 1025))}, CodeUnsupportedProfile},
		{"invalid-utf8", [][]byte{fixtureAttribute("mail", string([]byte{0xed, 0xa0, 0x80}))}, CodeInvalidResponse},
		{"invalid-sid", [][]byte{fixtureAttribute("objectSid", string([]byte{2, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0}))}, CodeInvalidResponse},
		{"sid-profile", [][]byte{fixtureAttribute("objectSid", string([]byte{1, 0, 0, 0, 0, 0, 0, 5}))}, CodeUnsupportedProfile},
		{"sid-local-bound", [][]byte{fixtureAttribute("objectSid", string(append([]byte{1, 6, 0, 0, 0, 0, 0, 5}, make([]byte, 24)...)))}, CodeUnsupportedProfile},
		{"invalid-date", [][]byte{fixtureAttribute("whenCreated", "20260230010101.0Z")}, CodeInvalidResponse},
		{"invalid-second", [][]byte{fixtureAttribute("whenCreated", "20261231235961.0Z")}, CodeInvalidResponse},
		{"leap-second", [][]byte{fixtureAttribute("whenCreated", "20261231235960.0Z")}, CodeUnsupportedProfile},
		{"offset", [][]byte{fixtureAttribute("whenCreated", "20261231235959.0+0100")}, CodeUnsupportedProfile},
		{"other-time-shape", [][]byte{fixtureAttribute("whenCreated", "20261231235959Z")}, CodeUnsupportedProfile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fixtureDirectoryV2Body(1, "user", tc.attrs...)
			original := bytes.Clone(body)
			got, err := directoryEntryV2(body, fixtureDirectoryBase)
			assertError(t, err, tc.code, StageSearch)
			if !reflect.DeepEqual(got, directoryassets.Entry{}) {
				t.Fatal("invalid v2 entry returned partial data")
			}
			if !bytes.Equal(body, original) {
				t.Fatal("v2 parser cleared a packet it did not own")
			}
		})
	}
}

func TestDirectoryV2EntryCoreLimitsAndCompleteSemanticValidation(t *testing.T) {
	guid := fixtureAttribute("objectGUID", strings.Repeat("x", 16))
	classes := fixtureAttribute("objectClass", "top", "user")
	for _, attrs := range [][][]byte{
		{guid},
		{guid, fixtureAttribute("objectClass", "top", "other")},
		{guid, fixtureAttribute("objectClass", "top", "user", "group")},
		{guid, fixtureAttribute("objectClass", strings.Repeat("x", 65))},
		{fixtureAttribute("objectGUID", strings.Repeat("x", 17)), classes},
		{fixtureAttribute("objectGUID", "short"), classes},
		{guid, classes, fixtureAttribute("sAMAccountName", strings.Repeat("x", 1025))},
		{guid, classes, fixtureAttribute("sAMAccountName", "has\x00control")},
		{guid, classes, fixtureAttribute("userAccountControl", "01")},
		{guid, classes, fixtureAttribute("userAccountControl", "4294967296")},
		{guid, classes, fixtureAttribute("userAccountControl", "12345678901")},
	} {
		body := append(octets("CN=x,"+fixtureDirectoryBase), element(0x30, attrs...)...)
		got, err := directoryEntryV2(body, fixtureDirectoryBase)
		assertError(t, err, CodeInvalidResponse, StageSearch)
		if !reflect.DeepEqual(got, directoryassets.Entry{}) {
			t.Fatal("invalid core returned partial v2 entry")
		}
	}
}

func TestDirectoryV2EntryOwnsAllReturnedValues(t *testing.T) {
	body := fixtureDirectoryV2Body(1, "computer", fixtureV2Supplemental()...)
	entry, err := directoryEntryV2(body, fixtureDirectoryBase)
	if err != nil {
		t.Fatal(err)
	}
	defer clearDirectoryEntries([]directoryassets.Entry{entry})
	clear(body)
	object, err := directoryassets.DecodeV2(entry)
	if err != nil {
		t.Fatal("clearing wire packet changed returned entry", err)
	}
	defer directoryassets.ClearStoredObjectsV2([]directoryassets.StoredObjectV2{object})
	clearDirectoryEntries([]directoryassets.Entry{entry})
	public, err := directoryassets.ProjectV2(object)
	if err != nil || public.Mail == nil || *public.Mail != " Mail\x00@Synthetic.Test\t" {
		t.Fatal("clearing entry changed owned codec result", err)
	}
}

func TestDirectoryV2PacketGolden(t *testing.T) {
	const golden = "3081ea0201046381bc041464633d73796e7468657469632c64633d74657374" +
		"0a01020a0100020100020103010100a12ba313040b6f626a656374436c617373" +
		"040475736572a314040b6f626a656374436c617373040567726f75703068" +
		"040a6f626a65637447554944040b6f626a656374436c617373" +
		"040e73414d4163636f756e744e616d650412757365724163636f756e74436f6e74726f6c" +
		"04096f626a65637453696404046d61696c040b6465736372697074696f6e040b7768656e43726561746564" +
		"a02630240416312e322e3834302e3131333535362e312e342e3331390101ff040730050201040400"
	want, err := hex.DecodeString(golden)
	if err != nil {
		t.Fatal(err)
	}
	packet := directorySearchPacketV2(fixtureDirectoryBase, 4, 4, nil)
	defer clear(packet)
	if !bytes.Equal(packet, want) {
		t.Fatalf("fixed v2 packet changed: got %x; want %x", packet, want)
	}
}

func TestDirectoryV2PageRequestPanicClearsCookiePacket(t *testing.T) {
	w := &directoryPanicWriter{}
	cfg := fixtureDirectoryConfig(t, LDAPS, nil)
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_, _, _ = directoryPageProfile(context.Background(), w, cfg, fixtureDirectoryBase, 4, []byte("opaque-cookie"), directoryProfileV2)
	}()
	if !panicked || !w.sawCookie || len(w.retained) == 0 || !bytes.Equal(w.retained, make([]byte, len(w.retained))) {
		t.Fatal("v2 request retained cookie after write panic")
	}
}

func TestDirectoryV2ReadResultFailureCleanupClearsOwnedBytes(t *testing.T) {
	entry, err := directoryEntryV2(fixtureDirectoryV2Body(1, "user", fixtureV2Supplemental()...), fixtureDirectoryBase)
	if err != nil {
		t.Fatal(err)
	}
	defer clearDirectoryEntries([]directoryassets.Entry{entry})
	object, err := directoryassets.DecodeV2(entry)
	if err != nil {
		t.Fatal(err)
	}
	raw := object.Supplemental
	result := directoryReadResult{objectsV2: []directoryassets.StoredObjectV2{object}}
	result.discard()
	result.discard()
	for _, value := range [][]byte{raw.ObjectSIDBytes, raw.MailBytes, raw.DescriptionBytes[0], raw.WhenCreatedBytes} {
		if !bytes.Equal(value, make([]byte, len(value))) {
			t.Fatal("failed-result cleanup retained supplemental bytes")
		}
	}
}

func FuzzDirectoryV2Entry(f *testing.F) {
	f.Add(fixtureDirectoryV2Body(1, "user", fixtureV2Supplemental()...))
	f.Add(fixtureDirectoryV2Body(1, "computer"))
	f.Add([]byte{4, 0, 0x30, 0})
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > maxDirectoryMessageBytes {
			return
		}
		entry, err := directoryEntryV2(body, fixtureDirectoryBase)
		if err != nil {
			if !reflect.DeepEqual(entry, directoryassets.Entry{}) {
				t.Fatal("failed parse exposed partial data")
			}
			return
		}
		defer clearDirectoryEntries([]directoryassets.Entry{entry})
		if len(entry.Attributes) > 8 {
			t.Fatal("v2 descriptor limit widened")
		}
		clear(body)
		decoded, err := directoryassets.DecodeV2(entry)
		if err != nil {
			t.Fatal("accepted entry is not valid/owned", err)
		}
		directoryassets.ClearStoredObjectsV2([]directoryassets.StoredObjectV2{decoded})
	})
}

func TestDirectoryV2ResourceLimitsAreNotMalformedData(t *testing.T) {
	for _, tc := range []struct {
		err error
		v2  Code
	}{
		{directoryassets.ErrLimit, CodeDirectoryLimit},
		{directoryassets.ErrSupplementalLimit, CodeUnsupportedProfile},
		{directoryassets.ErrSIDProfile, CodeUnsupportedProfile},
		{directoryassets.ErrWhenCreatedProfile, CodeUnsupportedProfile},
		{directoryassets.ErrAttribute, CodeInvalidResponse},
		{directoryassets.ErrWhenCreated, CodeInvalidResponse},
		{directoryassets.ErrSID, CodeInvalidResponse},
		{directoryassets.ErrPage, CodeInvalidResponse},
	} {
		assertError(t, directoryProfileError(directoryProfileV2, tc.err), tc.v2, StageSearch)
		assertError(t, directoryProfileError(directoryProfileV1, tc.err), CodeInvalidResponse, StageSearch)
	}
	for _, profile := range []directoryProfile{directoryProfileV1, directoryProfileV2} {
		r := &directoryHeaderOnlyReader{header: []byte{0x30, 0x82, 0x10, 0}}
		_, err := readDirectoryMessageProfile(r, 4, 4095, profile)
		want := errBER
		if profile == directoryProfileV2 {
			want = directoryassets.ErrLimit
		}
		if err != want || r.bodyRead {
			t.Fatal("wire budget classification or before-allocation bound changed")
		}
	}
	r := &directoryHeaderOnlyReader{}
	_, err := readDirectoryMessageProfile(r, 4, 4095, 255)
	assertError(t, err, CodeInvalidConfig, StageValidate)
	if r.bodyRead {
		t.Fatal("unknown wire profile reached I/O")
	}
}
