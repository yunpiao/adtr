//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

// Independently encodes the frozen ordered request, without the fixture matcher.
func testDirectoryV2Body() []byte {
	return bytes.Join([][]byte{
		octets("dc=synthetic,dc=invalid"), {10, 1, 2, 10, 1, 0, 2, 1, 0, 2, 1, 3, 1, 1, 0},
		element(0xa1, element(0xa3, octets("objectClass"), octets("user")), element(0xa3, octets("objectClass"), octets("group"))),
		element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl"),
			octets("objectSid"), octets("mail"), octets("description"), octets("whenCreated")),
	}, nil)
}

func TestDirectoryV2PreservesLegacyBytesAndBaseAttributes(t *testing.T) {
	// Captured from the unmodified HEAD fixture before this implementation:
	// exact v1 request, initial paging control and all 30 entries with id 4.
	hash := sha256.New()
	hash.Write(directorySearchBody)
	hash.Write(pagingRequestControl(nil))
	for row := 1; row <= 30; row++ {
		legacy := directoryEntry(4, row)
		hash.Write(legacy)
		v1, err := readRequest(bytes.NewReader(legacy))
		if err != nil {
			t.Fatal(err)
		}
		v2, err := readRequest(bytes.NewReader(directoryEntryForDictionary(4, row, true)))
		if err != nil {
			t.Fatal(err)
		}
		before, after := decodeTestEntry(t, v1.body), decodeTestEntry(t, v2.body)
		if before.DN != after.DN || len(after.Attributes) < len(before.Attributes) ||
			!reflect.DeepEqual(before.Attributes, after.Attributes[:len(before.Attributes)]) {
			t.Fatalf("dictionary 2 altered legacy attributes at row %d", row)
		}
		supplemental := after.Attributes[len(before.Attributes):]
		if row == 24 {
			if len(supplemental) != 0 {
				t.Fatal("row 24 must omit every supplemental attribute")
			}
			continue
		}
		if len(supplemental) != 4 {
			t.Fatal("dictionary 2 must emit only four supplemental attributes")
		}
		for i, name := range []string{"objectSid", "mail", "description", "whenCreated"} {
			if supplemental[i].Name != name || len(supplemental[i].Values) != 1 {
				t.Fatalf("row %d supplemental attribute %d changed order or cardinality", row, i)
			}
		}
		if _, err := directoryassets.Decode(after); !errors.Is(err, directoryassets.ErrEntry) {
			t.Fatalf("legacy decoder changed its four-attribute bound: %v", err)
		}
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != "7fc29f9c55b75d44318fa9a873c1b6e638c877486662755f8985a823318adc68" {
		t.Fatal("legacy request or response bytes changed")
	}
}

func receiveDirectoryV2Page(t *testing.T, conn io.Reader, id, wantEntries, wantEstimate int) ([]directoryassets.StoredObjectV2, []byte) {
	t.Helper()
	var objects []directoryassets.StoredObjectV2
	for range wantEntries {
		req, err := readRequest(conn)
		if err != nil || req.id != id || req.tag != 0x64 || len(req.controls) != 0 {
			t.Fatalf("invalid dictionary 2 entry response: %v", err)
		}
		object, err := directoryassets.DecodeV2(decodeTestEntry(t, req.body))
		if err != nil {
			t.Fatalf("entry failed production dictionary 2 decoder: %v", err)
		}
		objects = append(objects, object)
	}
	// The legacy helper validates the complete successful result envelope,
	// estimate and opaque cookie. It receives no entries here.
	_, cookie := receiveDirectoryPageForCounts(t, conn, id, 0, wantEstimate)
	return objects, cookie
}

func assertDictionaryV2(t *testing.T, objects []directoryassets.StoredObjectV2) {
	t.Helper()
	base := make([]directoryassets.Object, len(objects))
	for i, object := range objects {
		base[i] = object.Base
	}
	assertDictionary(t, base)
	for i, object := range objects {
		row := i + 1
		public, err := directoryassets.ProjectV2(object)
		if err != nil {
			t.Fatalf("row %d projection failed: %v", row, err)
		}
		if row == 24 {
			if !reflect.DeepEqual(object.Supplemental, directoryassets.RawSupplementalV2{}) || public.ObjectSID != nil || public.Mail != nil || public.Description != nil || public.WhenCreated != nil {
				t.Fatal("omitted supplemental attributes must remain null")
			}
		} else {
			mail, description := fmt.Sprintf("row-%02d@example.test", row), fmt.Sprintf("Synthetic row %02d", row)
			created, rawCreated := "2026-10-01T02:03:04Z", "20261001020304.0Z"
			if row == 1 {
				mail, description = " Mixed@example.test ", "Line 1\x00\n\t😀\u202e"
				created, rawCreated = "0001-01-01T00:00:00Z", "00010101000000.0Z"
			}
			wantSID := fmt.Sprintf("S-1-5-21-1-2-3-%d", 1000+row)
			if public.ObjectSID == nil || *public.ObjectSID != wantSID || public.Mail == nil || *public.Mail != mail ||
				!reflect.DeepEqual(public.Description, []string{description}) || public.WhenCreated == nil || *public.WhenCreated != created {
				t.Fatalf("row %d changed the frozen supplemental projection", row)
			}
			// Independent exact bytes lock in binary SID ordering and raw LDAP
			// time/text, not just the public formatter's interpretation.
			rid := 1000 + row
			wantSIDBytes := []byte{1, 5, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, byte(rid), byte(rid >> 8), 0, 0}
			if !bytes.Equal(object.Supplemental.ObjectSIDBytes, wantSIDBytes) || string(object.Supplemental.MailBytes) != mail ||
				!reflect.DeepEqual(object.Supplemental.DescriptionBytes, [][]byte{[]byte(description)}) || string(object.Supplemental.WhenCreatedBytes) != rawCreated {
				t.Fatalf("row %d changed raw supplemental bytes", row)
			}
		}
		stored, err := directoryassets.EncodeStoredObjectV2(object)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := directoryassets.DecodeStoredObjectV2(stored)
		if err != nil || !reflect.DeepEqual(object, decoded) {
			t.Fatalf("row %d failed canonical byte storage round trip: %v", row, err)
		}
		encoded, err := json.Marshal(public)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil || len(fields) != 10 {
			t.Fatal("public projection must contain exactly ten fields")
		}
		if row == 24 {
			for _, field := range []string{"objectSid", "mail", "description", "whenCreated"} {
				if string(fields[field]) != "null" {
					t.Fatalf("omitted %s did not serialize as null", field)
				}
			}
		}
		var roundTrip directoryassets.PublicObjectV2
		if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(public, roundTrip) {
			t.Fatalf("row %d lost public bytes in JSON round trip", row)
		}
	}
}

func TestDirectoryV2RealTLSAndBERPages(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(map[bool]string{false: "StartTLS", true: "LDAPS"}[ldaps], func(t *testing.T) {
			fixture, config := testServer(t)
			fixture.directoryMode, fixture.directoryV2 = true, true
			conn := directoryConnection(t, fixture, config, ldaps)
			send(t, conn, testDirectoryRequest(4, testDirectoryV2Body(), testPagingControl(1000, nil)))
			first, cookie := receiveDirectoryV2Page(t, conn, 4, 15, 30)
			if len(cookie) != 20 || !bytes.Equal(cookie[:4], []byte{0, 255, 128, 0}) {
				t.Fatal("dictionary 2 lost binary cookie opacity")
			}
			send(t, conn, testDirectoryRequest(5, testDirectoryV2Body(), testPagingControl(1000, cookie)))
			second, end := receiveDirectoryV2Page(t, conn, 5, 15, 30)
			if len(end) != 0 {
				t.Fatal("dictionary 2 did not terminate paging")
			}
			assertDictionaryV2(t, append(first, second...))
			for i, replay := range [][]byte{nil, cookie} {
				send(t, conn, testDirectoryRequest(i+6, testDirectoryV2Body(), testPagingControl(1000, replay)))
				requireCode(t, conn, i+6, 0x65, 53)
			}
		})
	}
}

func TestDirectoryV2RejectsWrongDictionaryAndChangedRequests(t *testing.T) {
	body, controls := testDirectoryV2Body(), testPagingControl(1000, nil)
	for name, test := range map[string]struct{ body, controls []byte }{
		"legacy dictionary": {testDirectoryBody(), controls},
		"attribute order":   {bytes.Replace(body, append(octets("objectSid"), octets("mail")...), append(octets("mail"), octets("objectSid")...), 1), controls},
		"attribute alias":   {bytes.Replace(body, []byte("objectSid"), []byte("objectSID"), 1), controls},
		"unknown attribute": {bytes.Replace(body, []byte("description"), []byte("displayName"), 1), controls},
		"base":              {bytes.Replace(body, []byte("synthetic"), []byte("outside"), 1), controls},
		"scope":             {bytes.Replace(body, []byte{10, 1, 2}, []byte{10, 1, 0}, 1), controls},
		"filter":            {bytes.Replace(body, []byte("group"), []byte("other"), 1), controls},
		"size limit":        {bytes.Replace(body, []byte{2, 1, 0}, []byte{2, 1, 1}, 1), controls},
		"time limit":        {bytes.Replace(body, []byte{2, 1, 3}, []byte{2, 1, 9}, 1), controls},
		"types only":        {bytes.Replace(body, []byte{1, 1, 0}, []byte{1, 1, 255}, 1), controls},
		"missing paging":    {body, nil},
		"unknown control":   {body, bytes.Replace(controls, []byte("319"), []byte("999"), 1)},
		"noncritical":       {body, bytes.Replace(controls, []byte{1, 1, 255}, []byte{1, 1, 0}, 1)},
		"duplicate":         {body, bytes.Repeat(controls, 2)},
		"zero page size":    {body, testPagingControl(0, nil)},
		"other page size":   {body, testPagingControl(15, nil)},
		"initial cookie":    {body, testPagingControl(1000, []byte("not-issued"))},
		"large cookie":      {body, testPagingControl(1000, bytes.Repeat([]byte{1}, 32000))},
	} {
		t.Run(name, func(t *testing.T) {
			fixture, config := testServer(t)
			fixture.directoryMode, fixture.directoryV2 = true, true
			conn := directoryConnection(t, fixture, config, true)
			send(t, conn, testDirectoryRequest(4, test.body, test.controls))
			requireCode(t, conn, 4, 0x65, 53)
			// A rejection must not advance the paging session.
			send(t, conn, testDirectoryRequest(5, body, controls))
			receiveDirectoryV2Page(t, conn, 5, 15, 30)
		})
	}
	for _, ldaps := range []bool{false, true} {
		fixture, config := testServer(t)
		fixture.directoryMode = true
		conn := directoryConnection(t, fixture, config, ldaps)
		send(t, conn, testDirectoryRequest(4, body, controls))
		requireCode(t, conn, 4, 0x65, 53)
		send(t, conn, testDirectoryRequest(5, testDirectoryBody(), controls))
		receiveDirectoryPage(t, conn, 5)
	}
}

func TestDirectoryV2ModeShapesAndDelay(t *testing.T) {
	for _, test := range []struct {
		name        string
		empty, slow bool
		pages, rows int
	}{
		{"default", false, false, 2, 30},
		{"empty", true, false, 1, 0},
		{"slow", false, true, 5, 30},
		{"empty slow", true, true, 5, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session := directorySession{v2: true, empty: test.empty, slow: test.slow}
				var objects []directoryassets.StoredObjectV2
				var cookie []byte
				for page := range test.pages {
					if !session.matches(request{body: testDirectoryV2Body(), controls: testPagingControl(1000, cookie)}) {
						t.Fatal("dictionary 2 rejected its current page request")
					}
					started := time.Now()
					var wire bytes.Buffer
					if err := session.writePage(context.Background(), &wire, page+4); err != nil {
						t.Fatal(err)
					}
					wantDelay := time.Duration(0)
					if test.slow {
						wantDelay = 2 * time.Second
					}
					if time.Since(started) != wantDelay {
						t.Fatal("dictionary 2 changed fixed per-page delay")
					}
					rows, next := receiveDirectoryV2Page(t, &wire, page+4, test.rows/test.pages, test.rows)
					objects = append(objects, rows...)
					if wire.Len() != 0 {
						t.Fatal("dictionary 2 emitted trailing page bytes")
					}
					if page+1 < test.pages {
						if len(next) != 20 || !bytes.Equal(next[:4], []byte{0, 255, 128, 0}) || bytes.Equal(next, cookie) {
							t.Fatal("dictionary 2 must issue fresh opaque cookies")
						}
					} else if len(next) != 0 || len(session.cookie) != 0 {
						t.Fatal("dictionary 2 failed to clear terminal cookie")
					}
					cookie = next
				}
				if test.rows != 0 {
					assertDictionaryV2(t, objects)
				} else if len(objects) != 0 {
					t.Fatal("empty dictionary 2 returned rows")
				}
			})
		})
	}
}

func readV2ModeFixture(ctx context.Context, fixture *server, clientTLS *tls.Config, mode ldapconnection.Mode,
	credential ldapconnection.Credential, authorize ldapconnection.DirectoryV2ReadAuthorization,
	wrap func(net.Conn) net.Conn) (ldapconnection.DirectoryV2Observation, error, <-chan struct{}) {
	cfg := ldapconnection.DirectoryV2Config{
		Mode: mode, ServerName: "dc.synthetic.invalid", Domain: "SYNTHETIC.INVALID.",
		DialIP: netip.MustParseAddr("127.0.0.1"), Roots: clientTLS.RootCAs,
		AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Limits: directoryassets.Limits{MaxRows: 10000, MaxPages: 100, MaxPageEntries: 1000,
			MaxBytes: 16 << 20, MaxCookieBytes: 65536},
		Authorize: authorize,
	}
	done := make(chan struct{})
	dials := 0
	result, err := ldapconnection.ReadDirectoryV2WithDialForIntegrationTest(ctx, cfg, credential,
		func(_ context.Context, network, address string) (net.Conn, error) {
			dials++
			port := "636"
			if mode == ldapconnection.StartTLS {
				port = "389"
			}
			if network != "tcp" || address != "127.0.0.1:"+port || dials != 1 {
				return nil, fmt.Errorf("unexpected reader destination")
			}
			client, peer := net.Pipe()
			if wrap != nil {
				peer = wrap(peer)
			}
			go func() {
				defer close(done)
				fixture.handle(context.Background(), peer, mode == ldapconnection.LDAPS)
			}()
			return client, nil
		})
	return result, err, done
}

func TestDirectoryV2ActualReaderModesOverTLS(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		for _, test := range []struct {
			name        string
			empty, slow bool
			pages       int
		}{
			{"default", false, false, 2},
			{"empty", true, false, 1},
			{"slow", false, true, 5},
			{"empty slow", true, true, 5},
		} {
			t.Run(string(mode)+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				fixture, clientTLS := testServer(t)
				fixture.directoryMode, fixture.directoryV2 = true, true
				fixture.directoryEmpty, fixture.directorySlow = test.empty, test.slow
				var stages []ldapconnection.DirectoryStage
				credential := ldapconnection.Credential{Username: testUsername, Password: []byte(testPassword)}
				started := time.Now()
				result, err, done := readV2ModeFixture(context.Background(), fixture, clientTLS, mode, credential,
					func(_ context.Context, stage ldapconnection.DirectoryStage) error {
						stages = append(stages, stage)
						return nil
					}, nil)
				defer result.Discard()
				if err != nil {
					t.Fatalf("production dictionary 2 reader rejected fixture: %v", err)
				}
				if test.slow && time.Since(started) < 10*time.Second {
					t.Fatal("five dictionary 2 pages did not each incur their fixed delay")
				}
				if !bytes.Equal(credential.Password, make([]byte, len(testPassword))) {
					t.Fatal("dictionary 2 reader did not clear credential")
				}
				if test.empty {
					if len(result.Objects) != 0 {
						t.Fatal("empty dictionary 2 returned objects")
					}
				} else {
					assertDictionaryV2(t, result.Objects)
				}
				if result.Source.Pages != test.pages || result.Source.Domain != "synthetic.invalid" ||
					result.Source.NamingContext != "dc=synthetic,dc=invalid" || result.Source.DCHostName != "DC.Synthetic.Invalid." {
					t.Fatalf("unexpected dictionary 2 source: %+v", result.Source)
				}
				want := []ldapconnection.DirectoryStage{ldapconnection.DirectoryValidate, ldapconnection.DirectoryDial,
					ldapconnection.DirectoryTLS, ldapconnection.DirectoryBind, ldapconnection.DirectoryRootDSE}
				for range test.pages {
					want = append(want, ldapconnection.DirectoryPage)
				}
				want = append(want, ldapconnection.DirectoryReturn)
				if !reflect.DeepEqual(stages, want) {
					t.Fatalf("dictionary 2 authority stages changed: %v", stages)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("dictionary 2 reader did not close and join transport")
				}
			})
		}
	}
}

func TestDirectoryV2ActualReaderCancellationDuringSlowPage(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			fixture, clientTLS := testServer(t)
			fixture.directoryMode, fixture.directoryV2, fixture.directorySlow = true, true, true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			armed, pageRead := make(chan struct{}), make(chan struct{})
			type outcome struct {
				result ldapconnection.DirectoryV2Observation
				err    error
				done   <-chan struct{}
			}
			returned := make(chan outcome, 1)
			pages := 0
			credential := ldapconnection.Credential{Username: testUsername, Password: []byte(testPassword)}
			go func() {
				result, err, done := readV2ModeFixture(ctx, fixture, clientTLS, mode, credential,
					func(_ context.Context, stage ldapconnection.DirectoryStage) error {
						if stage == ldapconnection.DirectoryPage {
							pages++
							if pages == 2 {
								close(armed)
							}
						}
						return nil
					}, func(conn net.Conn) net.Conn { return &directoryReadSignal{Conn: conn, armed: armed, read: pageRead} })
				returned <- outcome{result, err, done}
			}()
			select {
			case <-pageRead:
			case got := <-returned:
				t.Fatalf("dictionary 2 returned before second page I/O: %v", got.err)
			case <-time.After(6 * time.Second):
				t.Fatal("dictionary 2 did not reach second slow page I/O")
			}
			cancel()
			var got outcome
			select {
			case got = <-returned:
			case <-time.After(time.Second):
				t.Fatal("dictionary 2 did not cancel page I/O promptly")
			}
			var failure *ldapconnection.Error
			if !errors.As(got.err, &failure) || failure.Code != ldapconnection.CodeCancelled || failure.Stage != ldapconnection.StageSearch {
				t.Fatalf("unexpected dictionary 2 cancellation: %v", got.err)
			}
			if pages != 2 || !reflect.DeepEqual(got.result, ldapconnection.DirectoryV2Observation{}) ||
				!bytes.Equal(credential.Password, make([]byte, len(testPassword))) {
				t.Fatal("cancelled dictionary 2 retained partial data or credentials")
			}
			select {
			case <-got.done:
			case <-time.After(3 * time.Second):
				t.Fatal("dictionary 2 handler did not join within bounded page delay")
			}
		})
	}
}

func TestDirectoryV2RequiresTLSBindAndRootDSE(t *testing.T) {
	for _, state := range []string{"plaintext", "unbound", "bad bind", "no root", "failed rebind"} {
		t.Run(state, func(t *testing.T) {
			fixture, config := testServer(t)
			fixture.directoryMode, fixture.directoryV2 = true, true
			var conn net.Conn
			if state == "plaintext" {
				conn = startPipe(t, fixture, false)
			} else if state == "failed rebind" {
				conn = directoryConnection(t, fixture, config, true)
			} else {
				conn = securePipe(t, fixture, config, true)
			}
			if state == "bad bind" || state == "failed rebind" {
				send(t, conn, bindRequest(4, testUsername, "incorrect"))
				requireCode(t, conn, 4, 0x61, 49)
			}
			want := 50
			if state == "no root" {
				send(t, conn, bindRequest(4, testUsername, testPassword))
				requireCode(t, conn, 4, 0x61, 0)
				want = 53
			}
			send(t, conn, testDirectoryRequest(5, testDirectoryV2Body(), testPagingControl(1000, nil)))
			requireCode(t, conn, 5, 0x65, want)
		})
	}
}

func TestDirectoryV2OpaqueCookiesStayConnectionAndBindScoped(t *testing.T) {
	t.Run("foreign and stale cookies", func(t *testing.T) {
		fixture, config := testServer(t)
		fixture.directoryMode, fixture.directoryV2 = true, true
		first := directoryConnection(t, fixture, config, true)
		second := directoryConnection(t, fixture, config, true)
		send(t, first, testDirectoryRequest(4, testDirectoryV2Body(), testPagingControl(1000, nil)))
		_, foreign := receiveDirectoryV2Page(t, first, 4, 15, 30)
		send(t, second, testDirectoryRequest(4, testDirectoryV2Body(), testPagingControl(1000, nil)))
		_, current := receiveDirectoryV2Page(t, second, 4, 15, 30)
		if bytes.Equal(foreign, current) {
			t.Fatal("dictionary 2 issued the same continuation to two connections")
		}
		for i, cookie := range [][]byte{foreign, nil, bytes.ToValidUTF8(current, []byte("?"))} {
			send(t, second, testDirectoryRequest(i+5, testDirectoryV2Body(), testPagingControl(1000, cookie)))
			requireCode(t, second, i+5, 0x65, 53)
		}
		send(t, second, testDirectoryRequest(8, testDirectoryV2Body(), testPagingControl(1000, current)))
		_, end := receiveDirectoryV2Page(t, second, 8, 15, 30)
		if len(end) != 0 {
			t.Fatal("dictionary 2 rejected its current opaque continuation")
		}
	})
	t.Run("successful rebind", func(t *testing.T) {
		fixture, config := testServer(t)
		fixture.directoryMode, fixture.directoryV2 = true, true
		conn := directoryConnection(t, fixture, config, true)
		send(t, conn, testDirectoryRequest(4, testDirectoryV2Body(), testPagingControl(1000, nil)))
		_, stale := receiveDirectoryV2Page(t, conn, 4, 15, 30)
		send(t, conn, bindRequest(5, testUsername, testPassword))
		requireCode(t, conn, 5, 0x61, 0)
		send(t, conn, searchRequest(6, "", 0))
		receive(t, conn, 6, 0x64)
		requireCode(t, conn, 6, 0x65, 0)
		send(t, conn, testDirectoryRequest(7, testDirectoryV2Body(), testPagingControl(1000, stale)))
		requireCode(t, conn, 7, 0x65, 53)
		send(t, conn, testDirectoryRequest(8, testDirectoryV2Body(), testPagingControl(1000, nil)))
		_, current := receiveDirectoryV2Page(t, conn, 8, 15, 30)
		if bytes.Equal(stale, current) {
			t.Fatal("dictionary 2 reused a continuation across binds")
		}
		send(t, conn, testDirectoryRequest(9, testDirectoryV2Body(), testPagingControl(1000, current)))
		receiveDirectoryV2Page(t, conn, 9, 15, 30)
	})
}

func TestDirectoryV2RetainsConnectionRequestAndEnvelopeBudgets(t *testing.T) {
	fixture, config := testServer(t)
	fixture.directoryMode, fixture.directoryV2 = true, true
	conn := directoryConnection(t, fixture, config, true)
	for id := 4; id <= maxRequests+1; id++ {
		send(t, conn, testDirectoryRequest(id, testDirectoryV2Body(), testPagingControl(0, nil)))
		requireCode(t, conn, id, 0x65, 53)
	}
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("dictionary 2 widened request budget: %v", err)
	}
	oversize := securePipe(t, fixture, config, true)
	send(t, oversize, []byte{0x30, 0x83, 0x01, 0x00, 0x01})
	if _, err := oversize.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("dictionary 2 widened BER envelope budget: %v", err)
	}
}
