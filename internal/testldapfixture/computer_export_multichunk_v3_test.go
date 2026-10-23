//go:build integration

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/computerassetxlsx"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func TestComputerExportMultichunkV3RequiresExactIsolatedMode(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-directory-mode", "-directory-v3", "-computer-assets-v3", "-computer-export-multichunk-v3"}
	cfg, err := readConfiguration(base, fixtureEnvironment)
	if err != nil || !cfg.computerExportMultichunkV3 {
		t.Fatal("explicit multichunk fixture rejected", err)
	}
	conflicts := []string{
		"-directory-v2", "-directory-v4", "-user-assets-v2", "-group-assets-v3",
		"-directory-empty", "-directory-slow", "-control-dir=/private", "-empty-transition-dir=/private",
		"-group-members-v4-pagination", "-group-members-v4-refresh", "-user-export-failure-v3",
		"-group-export-failure-v3", "-computer-export-failure-v3", "-directory-mode=false",
		"-directory-v3=false", "-computer-assets-v3=false",
	}
	for _, conflict := range conflicts {
		t.Run(conflict, func(t *testing.T) {
			if _, err := readConfiguration(append(append([]string{}, base...), conflict), fixtureEnvironment); err == nil {
				t.Fatal("mixed multichunk fixture accepted")
			}
		})
	}
	for _, pair := range [][]string{
		{"-directory-v2", "-directory-v3=false"},
		{"-directory-v4", "-group-members-v4-pagination"},
		{"-directory-v4", "-group-members-v4-refresh", "-empty-transition-dir=/private"},
		{"-user-export-failure-v3", "-computer-assets-v3=false"},
		{"-group-assets-v3", "-group-export-failure-v3", "-computer-assets-v3=false"},
	} {
		if _, err := readConfiguration(append(append([]string{}, base...), pair...), fixtureEnvironment); err == nil {
			t.Fatal("mixed multichunk pair accepted")
		}
	}
	for _, flags := range [][]string{nil, {"-directory-mode"}, {"-directory-mode", "-directory-v3"}, {"-directory-mode", "-computer-assets-v3"}} {
		args := append([]string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-computer-export-multichunk-v3"}, flags...)
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatal("incomplete multichunk fixture accepted")
		}
	}
	for _, flags := range [][]string{nil, {"-directory-mode", "-directory-v3", "-computer-assets-v3"}} {
		args := append([]string{"-cert", "synthetic.crt", "-key", "synthetic.key"}, flags...)
		cfg, err := readConfiguration(args, fixtureEnvironment)
		if err != nil || cfg.computerExportMultichunkV3 {
			t.Fatal("default mode changed", err)
		}
		cfg, err = readConfiguration(append(args, "-computer-export-multichunk-v3=false"), fixtureEnvironment)
		if err != nil || cfg.computerExportMultichunkV3 {
			t.Fatal("explicit disabled mode changed", err)
		}
	}
}

// This independent reconstruction uses the literal published fixture contract,
// not the builder's counts or helper functions.
func expectedMultichunkV3DN(row int) string {
	if row > 1022 {
		return fmt.Sprintf("CN=group-capacity-%04d,OU=Groups,DC=synthetic,DC=invalid", row)
	}
	if row > 1020 {
		return fmt.Sprintf("CN=user-capacity-%04d,OU=Users,DC=synthetic,DC=invalid", row)
	}
	parts := []string{fmt.Sprintf("CN=computer-capacity-%04d", row)}
	for index := 0; index != 48; index++ {
		raw := fmt.Sprintf("adtr-computer-capacity-v1|%04d|%02d", row, index)
		digest := sha256.Sum256([]byte(raw))
		parts = append(parts, fmt.Sprintf("OU=%x", digest))
	}
	return strings.Join(parts, ",") + ",DC=synthetic,DC=invalid"
}

func decodeMultichunkV3Entry(t *testing.T, wire []byte, id int) (directoryassets.Entry, directoryassets.StoredObjectV3) {
	t.Helper()
	reader := bytes.NewReader(wire)
	req, err := readRequest(reader)
	if err != nil || reader.Len() != 0 || req.id != id || req.tag != 0x64 || len(req.controls) != 0 {
		t.Fatal("invalid bounded multichunk entry", err)
	}
	entry := decodeTestEntry(t, req.body)
	object, err := directoryassets.DecodeV3(entry)
	if err != nil {
		t.Fatal("multichunk entry failed real decoder", err)
	}
	return entry, object
}

func assertMultichunkV3Objects(t *testing.T, objects []directoryassets.StoredObjectV3) [][]string {
	t.Helper()
	if len(objects) != 1024 {
		t.Fatal("multichunk population changed", len(objects))
	}
	counts := map[directoryassets.Kind]int{}
	guids, sids := map[string]bool{}, map[string]bool{}
	var matrix [][]string
	pieces := make([]json.RawMessage, 0, len(objects))
	storedTotal, maxStored, maxPublic := 0, 0, 0
	for i, object := range objects {
		row := i + 1
		counts[object.Base.Kind]++
		stored, err := directoryassets.EncodeStoredObjectV3(object)
		if err != nil || len(stored) == 0 || len(stored) > 8192 {
			t.Fatal("stored fixture object exceeded its own bound", row, len(stored), err)
		}
		restored, err := directoryassets.DecodeStoredObjectV3(stored)
		if err != nil || !reflect.DeepEqual(object, restored) {
			t.Fatal("real stored codec round trip changed fixture", row, err)
		}
		public, err := directoryassets.ProjectV3(restored)
		if err != nil {
			t.Fatal("real projection rejected fixture", row, err)
		}
		encoded, err := json.Marshal(public)
		if err != nil || len(encoded) == 0 || len(encoded) > 8192 {
			t.Fatal("public fixture object exceeded its own bound", row, len(encoded), err)
		}
		if !utf8.Valid(stored) || !utf8.Valid(encoded) || !utf8.ValidString(public.DN) || len(public.DN) > 4096 {
			t.Fatal("fixture field or JSON text bound changed", row)
		}
		wantGUID, wantSID := fmt.Sprintf("%08x-1122-3344-5566-778899aabbcc", row), fmt.Sprintf("S-1-5-%d", row)
		if public.GUID != wantGUID || guids[public.GUID] || public.ObjectSID == nil || *public.ObjectSID != wantSID || sids[*public.ObjectSID] {
			t.Fatal("fixture identity collision or mixed-endian mismatch", row)
		}
		guids[public.GUID], sids[*public.ObjectSID] = true, true
		kind := "computer"
		classes := []string{"computer", "organizationalperson", "person", "top", "user"}
		if row > 1020 {
			kind, classes = "user", []string{"organizationalperson", "person", "top", "user"}
		}
		if row > 1022 {
			kind, classes = "group", []string{"group", "top"}
		}
		wantSAM := fmt.Sprintf("%s-capacity-%04d", kind, row)
		if row <= 1020 {
			wantSAM += "$"
		}
		if string(public.Kind) != kind || !slices.Equal(public.Classes, classes) || public.SAMAccountName == nil || *public.SAMAccountName != wantSAM || len(*public.SAMAccountName) > 256 || public.DN != expectedMultichunkV3DN(row) {
			t.Fatal("fixture fixed classes or text changed", row)
		}
		if public.WhenCreated == nil || *public.WhenCreated != "2026-01-01T00:00:00Z" || string(object.Supplemental.WhenCreatedBytes) != "20260101000000.0Z" {
			t.Fatal("fixture timestamp changed", row)
		}
		if public.Mail != nil || public.Description != nil || public.PrimaryGroupID != nil || public.ServicePrincipalName != nil {
			t.Fatal("absent fixture fields were filled", row)
		}
		if row <= 1020 {
			if public.UserAccountControl == nil || *public.UserAccountControl != 4096 {
				t.Fatal("computer UAC changed", row)
			}
			matrix = append(matrix, []string{public.GUID, *public.SAMAccountName, *public.ObjectSID, public.DN, *public.WhenCreated})
		} else if public.UserAccountControl != nil {
			t.Fatal("noncomputer UAC must be absent", row)
		}
		pieces = append(pieces, stored)
		storedTotal += len(stored)
		maxStored, maxPublic = max(maxStored, len(stored)), max(maxPublic, len(encoded))
		directoryassets.ClearStoredObjectsV3([]directoryassets.StoredObjectV3{restored})
	}
	if counts[directoryassets.Computer] != 1020 || counts[directoryassets.User] != 2 || counts[directoryassets.Group] != 2 || len(guids) != 1024 || len(sids) != 1024 {
		t.Fatal("fixture class population changed")
	}
	// The production observation codec is package-private. Independently encode
	// its exact envelope with real encoded objects, then also budget the full
	// permitted 8192-byte source, so timing/source variation cannot exceed 16 MiB.
	source := ldapconnection.DirectorySource{
		ServerName: "dc.synthetic.invalid", DCHostName: "dc.synthetic.invalid",
		Domain: "synthetic.invalid", NamingContext: "DC=synthetic,DC=invalid",
		StartedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), ElapsedMilliseconds: 1000, Pages: 4,
	}
	envelope := struct {
		DictionaryVersion int                            `json:"dictionaryVersion"`
		Objects           []json.RawMessage              `json:"objects"`
		Source            ldapconnection.DirectorySource `json:"source"`
	}{3, pieces, source}
	raw, err := json.Marshal(envelope)
	worstEnvelopeBytes := len(`{"dictionaryVersion":3,"objects":[`) + storedTotal + 1023 + len(`],"source":`) + 8192 + 1
	if err != nil || !json.Valid(raw) || len(raw) >= 16<<20 || worstEnvelopeBytes >= 16<<20 {
		t.Fatal("fixture observation exceeded existing storage bound", len(raw), worstEnvelopeBytes, err)
	}
	t.Logf("synthetic fixture only: max stored=%d, max public=%d, fixed-source envelope=%d, maximum-source envelope=%d", maxStored, maxPublic, len(raw), worstEnvelopeBytes)
	return matrix
}

func TestComputerExportMultichunkV3DeterministicRowsAndRealCodecs(t *testing.T) {
	if computerExportMultichunkV3ObjectCount != 1024 || computerExportMultichunkV3PageEntries != 256 || computerExportMultichunkV3ComputerCount != 1020 || computerExportMultichunkV3OUCount != 48 {
		t.Fatal("fixture constants drifted")
	}
	for _, row := range []int{-1, 0, 1025} {
		if computerExportMultichunkV3Entry(4, row) != nil {
			t.Fatal("out-of-range fixture row accepted")
		}
	}
	objects := make([]directoryassets.StoredObjectV3, 0, 1024)
	defer func() { directoryassets.ClearStoredObjectsV3(objects) }()
	for row := 1; row <= 1024; row++ {
		wire := computerExportMultichunkV3Entry(4, row)
		if !bytes.Equal(wire, computerExportMultichunkV3Entry(4, row)) {
			t.Fatal("nondeterministic entry bytes", row)
		}
		entry, object := decodeMultichunkV3Entry(t, wire, 4)
		wantNames := []string{"objectGUID", "objectClass", "sAMAccountName", "userAccountControl", "objectSid", "whenCreated"}
		if row > 1020 {
			wantNames = []string{"objectGUID", "objectClass", "sAMAccountName", "objectSid", "whenCreated"}
		}
		if len(entry.Attributes) != len(wantNames) {
			t.Fatal("unexpected attribute set", row)
		}
		for i, attribute := range entry.Attributes {
			if attribute.Name != wantNames[i] || len(attribute.Values) == 0 || attribute.Name != "objectClass" && len(attribute.Values) != 1 {
				t.Fatal("unexpected attribute order/cardinality", row, i)
			}
			if attribute.Name == "objectGUID" {
				want := []byte{byte(row), byte(row >> 8), byte(row >> 16), byte(row >> 24), 0x22, 0x11, 0x44, 0x33, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc}
				if !bytes.Equal(attribute.Values[0], want) {
					t.Fatal("raw GUID byte order changed", row)
				}
			}
			if attribute.Name == "objectSid" {
				want := []byte{1, 1, 0, 0, 0, 0, 0, 5, byte(row), byte(row >> 8), byte(row >> 16), byte(row >> 24)}
				if !bytes.Equal(attribute.Values[0], want) {
					t.Fatal("raw SID byte order changed", row)
				}
			}
		}
		objects = append(objects, object)
	}
	assertMultichunkV3Objects(t, objects)
}

func receiveMultichunkV3Page(t *testing.T, conn io.Reader, id int) []directoryassets.StoredObjectV3 {
	t.Helper()
	objects := make([]directoryassets.StoredObjectV3, 0, 256)
	for range 256 {
		req, err := readRequest(conn)
		if err != nil || req.id != id || req.tag != 0x64 || len(req.controls) != 0 {
			t.Fatal("invalid multichunk page entry", err)
		}
		object, err := directoryassets.DecodeV3(decodeTestEntry(t, req.body))
		if err != nil {
			t.Fatal("multichunk page rejected by real decoder", err)
		}
		objects = append(objects, object)
	}
	return objects
}

func TestComputerExportMultichunkV3RealTLSFourPages(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(fmt.Sprintf("LDAPS=%t", ldaps), func(t *testing.T) {
			fixture, cfg := testServer(t)
			fixture.directoryMode, fixture.directoryV3, fixture.computerAssetsV3, fixture.computerExportMultichunkV3 = true, true, true, true
			conn := directoryConnection(t, fixture, cfg, ldaps)
			var objects []directoryassets.StoredObjectV3
			defer func() { directoryassets.ClearStoredObjectsV3(objects) }()
			var cookie []byte
			seenCookies := map[string]bool{}
			for page := 0; page < 4; page++ {
				id := page + 4
				send(t, conn, testDirectoryRequest(id, directoryV3SearchBody, testPagingControl(1000, cookie)))
				objects = append(objects, receiveMultichunkV3Page(t, conn, id)...)
				_, next := receiveDirectoryPageForCounts(t, conn, id, 0, 1024)
				if page < 3 {
					if len(next) != 20 || !bytes.Equal(next[:4], []byte{0, 255, 128, 0}) || seenCookies[string(next)] {
						t.Fatal("multichunk cookie is not fresh and opaque")
					}
					seenCookies[string(next)] = true
				} else if len(next) != 0 {
					t.Fatal("fourth page failed to terminate")
				}
				cookie = next
			}
			// StartTLS + bind + RootDSE + four searches is seven requests. The
			// eighth request can reject a replay without changing the old ceiling.
			send(t, conn, testDirectoryRequest(8, directoryV3SearchBody, testPagingControl(1000, nil)))
			requireCode(t, conn, 8, 0x65, 53)
			assertMultichunkV3Objects(t, objects)
		})
	}
}

func TestComputerExportMultichunkV3RequestAndCookieIsolation(t *testing.T) {
	session := directorySession{v3: true, computerAssetsV3: true, computerExportMultichunkV3: true}
	defer session.reset()
	if session.pages() != 4 || session.objects() != 1024 || maxRequests != 8 || connectionTime != 15*time.Second || directoryRequestPageSize != 1000 {
		t.Fatal("fixture count or preexisting transport budget changed")
	}
	body, controls := directoryV3SearchBody, testPagingControl(1000, nil)
	for _, invalid := range []request{
		{body: directorySearchBody, controls: controls},
		{body: bytes.Replace(body, []byte("objectSid"), []byte("objectSID"), 1), controls: controls},
		{body: bytes.Replace(body, []byte{2, 1, 3}, []byte{2, 1, 9}, 1), controls: controls},
		{body: body},
		{body: body, controls: testPagingControl(256, nil)},
		{body: body, controls: testPagingControl(0, nil)},
		{body: body, controls: testPagingControl(1000, []byte("not-issued"))},
		{body: body, controls: bytes.Repeat(controls, 2)},
		{body: body, controls: bytes.Replace(controls, []byte("319"), []byte("999"), 1)},
		{body: body, controls: bytes.Replace(controls, []byte{1, 1, 255}, []byte{1, 1, 0}, 1)},
	} {
		if session.matches(invalid) {
			t.Fatal("changed production request/control accepted")
		}
	}
	var prior []byte
	for page := 0; page < 4; page++ {
		current := append([]byte(nil), session.cookie...)
		if !session.matches(request{body: body, controls: testPagingControl(1000, current)}) {
			t.Fatal("current cookie rejected")
		}
		if page > 0 && session.matches(request{body: body, controls: testPagingControl(1000, prior)}) {
			t.Fatal("stale cookie accepted")
		}
		other := directorySession{v3: true, computerAssetsV3: true, computerExportMultichunkV3: true, page: page}
		if page > 0 && other.matches(request{body: body, controls: testPagingControl(1000, current)}) {
			t.Fatal("foreign connection cookie accepted")
		}
		var wire bytes.Buffer
		if err := session.writePage(context.Background(), &wire, page+4); err != nil {
			t.Fatal(err)
		}
		objects := receiveMultichunkV3Page(t, &wire, page+4)
		for i, object := range objects {
			if object.Base.GUID != fmt.Sprintf("%08x-1122-3344-5566-778899aabbcc", page*256+i+1) {
				t.Fatal("page duplicated or skipped a row")
			}
		}
		directoryassets.ClearStoredObjectsV3(objects)
		_, cookie := receiveDirectoryPageForCounts(t, &wire, page+4, 0, 1024)
		if wire.Len() != 0 || !bytes.Equal(cookie, session.cookie) || page < 3 && len(cookie) != 20 || page == 3 && len(cookie) != 0 {
			t.Fatal("page framing or cookie mismatch")
		}
		prior = current
	}
	if session.matches(request{body: body, controls: testPagingControl(1000, nil)}) {
		t.Fatal("completed session accepted another page")
	}
	session.reset()
	if session.page != 0 || session.cookie != nil || !session.matches(request{body: body, controls: controls}) {
		t.Fatal("reset did not restore a fresh bounded session")
	}
}

func TestComputerExportMultichunkV3DisabledPreservesSmallWire(t *testing.T) {
	for _, test := range []struct {
		name    string
		session directorySession
		entry   func(int, int) []byte
	}{
		{"v1", directorySession{}, directoryEntry},
		{"v2", directorySession{v2: true}, func(id, row int) []byte { return directoryEntryForDictionary(id, row, true) }},
		{"v3", directorySession{v3: true}, directoryV3Entry},
		{"v4", directorySession{v4: true}, directoryV4Entry},
		{"computer", directorySession{v3: true, computerAssetsV3: true}, legacyComputerAssetsV3Entry},
		{"computer failure", directorySession{v3: true, computerAssetsV3: true, computerExportFailureV3: true}, func(id, row int) []byte { return computerAssetsV3EntryWithExportFailure(id, row, true) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := test.session
			defer session.reset()
			if session.computerExportMultichunkV3 || session.pages() != 2 || session.objects() != 30 {
				t.Fatal("flagless fixture population changed")
			}
			for page := 0; page < 2; page++ {
				var wire bytes.Buffer
				if err := session.writePage(context.Background(), &wire, page+4); err != nil {
					t.Fatal(err)
				}
				for row := page*15 + 1; row <= (page+1)*15; row++ {
					want := test.entry(page+4, row)
					actual := make([]byte, len(want))
					if _, err := io.ReadFull(&wire, actual); err != nil || !bytes.Equal(actual, want) {
						t.Fatal("flagless entry bytes changed", row, err)
					}
				}
				_, cookie := receiveDirectoryPageForCounts(t, &wire, page+4, 0, 30)
				if wire.Len() != 0 || page == 0 && len(cookie) != 20 || page == 1 && len(cookie) != 0 {
					t.Fatal("flagless pagination changed")
				}
			}
		})
	}
	for _, test := range []struct {
		session directorySession
		pages   int
		objects int
	}{{directorySession{empty: true}, 1, 0}, {directorySession{slow: true}, 5, 30}, {directorySession{empty: true, slow: true}, 5, 0}} {
		if test.session.pages() != test.pages || test.session.objects() != test.objects {
			t.Fatal("flagless empty/slow shape changed")
		}
	}
}

func TestComputerExportMultichunkV3RealWriterCrossesChunkBoundary(t *testing.T) {
	objects := make([]directoryassets.StoredObjectV3, 0, 1024)
	defer func() { directoryassets.ClearStoredObjectsV3(objects) }()
	for row := 1; row <= 1024; row++ {
		_, object := decodeMultichunkV3Entry(t, computerExportMultichunkV3Entry(4, row), 4)
		objects = append(objects, object)
	}
	matrix := assertMultichunkV3Objects(t, objects)
	headers := []string{"GUID", "SAM", "SID", "DN", "whenCreated"}
	var workbook bytes.Buffer
	index := 0
	if err := computerassetxlsx.Write(context.Background(), &workbook, headers, func() ([]string, error) {
		if index == len(matrix) {
			return nil, io.EOF
		}
		row := matrix[index]
		index++
		return row, nil
	}); err != nil {
		t.Fatal("fixed synthetic rows failed real writer", err)
	}
	const chunkBytes = 1 << 20
	chunkCount := (workbook.Len() + chunkBytes - 1) / chunkBytes
	if len(matrix) != 1020 || workbook.Len() <= chunkBytes || workbook.Len() > 32<<20 || chunkCount < 2 || chunkCount > 32 {
		t.Fatal("fixed fixture did not exercise the required chunk size", workbook.Len(), chunkCount)
	}
	archive, err := zip.NewReader(bytes.NewReader(workbook.Bytes()), int64(workbook.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, part := range archive.File {
		if part.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		if found || part.UncompressedSize64 > 16<<20 {
			t.Fatal("invalid synthetic worksheet size/identity")
		}
		found = true
		reader, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		sheet, err := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || len(sheet) > 16<<20 || bytes.Count(sheet, []byte("<row ")) != 1021 {
			t.Fatal("invalid real worksheet", err, closeErr)
		}
		var expected strings.Builder
		expected.WriteString("<sheetData>")
		for i, values := range append([][]string{headers}, matrix...) {
			fmt.Fprintf(&expected, `<row r="%d">`, i+1)
			for column, value := range values {
				// All fixed values are ASCII without XML/OOXML escapes. Compare
				// the entire ordered matrix once, rather than rescanning per cell.
				fmt.Fprintf(&expected, `<c r="%c%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, 'A'+column, i+1, value)
			}
			expected.WriteString("</row>")
		}
		expected.WriteString("</sheetData>")
		if !bytes.Contains(sheet, []byte(expected.String())) {
			t.Fatal("real writer changed the complete ordered synthetic matrix")
		}
	}
	if !found {
		t.Fatal("real writer omitted worksheet")
	}
	// This is a local writer test, not evidence of HTTP, Worker or DB chunks.
	t.Logf("synthetic writer only: rows=%d, bytes=%d, expected 1 MiB chunks=%d", len(matrix), workbook.Len(), chunkCount)
}
