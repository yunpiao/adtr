//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/asn1"
	"fmt"
	"io"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

// Requests are independently built from the documented production contract,
// without calling the fixture's request matcher or paging-control generator.
func testDirectoryBody() []byte {
	return bytes.Join([][]byte{
		octets("dc=synthetic,dc=invalid"), {10, 1, 2, 10, 1, 0, 2, 1, 0, 2, 1, 3, 1, 1, 0},
		element(0xa1, element(0xa3, octets("objectClass"), octets("user")), element(0xa3, octets("objectClass"), octets("group"))),
		element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl")),
	}, nil)
}

func testPagingControl(size int, cookie []byte) []byte {
	encoded, err := asn1.Marshal(size)
	if err != nil {
		panic(err)
	}
	return element(0x30, octets("1.2.840.113556.1.4.319"), []byte{1, 1, 255},
		element(0x04, element(0x30, encoded, element(0x04, cookie))))
}

func testDirectoryRequest(id int, body, controls []byte) []byte {
	fields := [][]byte{integer(2, id), element(0x63, body)}
	if controls != nil {
		fields = append(fields, element(0xa0, controls))
	}
	return element(0x30, fields...)
}

func directoryConnection(t *testing.T, fixture *server, clientTLS *tls.Config, ldaps bool) net.Conn {
	t.Helper()
	conn := securePipe(t, fixture, clientTLS, ldaps)
	send(t, conn, bindRequest(2, testUsername, testPassword))
	requireCode(t, conn, 2, 0x61, 0)
	send(t, conn, searchRequest(3, "", 0))
	entry := decodeTestEntry(t, receive(t, conn, 3, 0x64))
	wantHost := "dc.synthetic.invalid"
	if fixture.directoryMode {
		wantHost = "DC.Synthetic.Invalid."
	}
	foundHost := false
	for _, attribute := range entry.Attributes {
		if attribute.Name == "dnsHostName" {
			foundHost = len(attribute.Values) == 1 && string(attribute.Values[0]) == wantHost
		}
	}
	if !foundHost {
		t.Fatal("RootDSE hostname mode changed")
	}
	requireCode(t, conn, 3, 0x65, 0)
	return conn
}

func decodeTestEntry(t *testing.T, body []byte) directoryassets.Entry {
	t.Helper()
	fields := berReader(body)
	dn, err := fields.expect(4)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		t.Fatal("invalid entry envelope")
	}
	entry := directoryassets.Entry{DN: string(dn)}
	attributes := berReader(encoded)
	for len(attributes) != 0 {
		encoded, err := attributes.expect(0x30)
		if err != nil {
			t.Fatal(err)
		}
		attribute := berReader(encoded)
		name, err := attribute.expect(4)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err = attribute.expect(0x31)
		if err != nil || len(attribute) != 0 {
			t.Fatal("invalid attribute envelope")
		}
		values := berReader(encoded)
		decoded := directoryassets.Attribute{Name: string(name)}
		for len(values) != 0 {
			value, err := values.expect(4)
			if err != nil {
				t.Fatal(err)
			}
			decoded.Values = append(decoded.Values, append([]byte(nil), value...))
		}
		entry.Attributes = append(entry.Attributes, decoded)
	}
	return entry
}

func receiveDirectoryPage(t *testing.T, conn net.Conn, id int) ([]directoryassets.Object, []byte) {
	t.Helper()
	return receiveDirectoryPageForCounts(t, conn, id, 15, 30)
}

func receiveDirectoryPageForCounts(t *testing.T, conn io.Reader, id, wantEntries, wantEstimate int) ([]directoryassets.Object, []byte) {
	t.Helper()
	var objects []directoryassets.Object
	for range wantEntries + 1 {
		req, err := readRequest(conn)
		if err != nil || req.id != id {
			t.Fatalf("invalid page response: %v", err)
		}
		if req.tag == 0x64 {
			if len(req.controls) != 0 {
				t.Fatal("entry carried controls")
			}
			object, err := directoryassets.Decode(decodeTestEntry(t, req.body))
			if err != nil {
				t.Fatalf("entry failed actual dictionary decoder: %v", err)
			}
			objects = append(objects, object)
			continue
		}
		if req.tag != 0x65 || !bytes.Equal(req.body, []byte{10, 1, 0, 4, 0, 4, 0}) || len(objects) != wantEntries {
			t.Fatalf("page must have exactly %d entries followed by success", wantEntries)
		}
		fields := berReader(req.controls)
		encoded, err := fields.expect(0x30)
		if err != nil || len(fields) != 0 {
			t.Fatal("invalid response controls")
		}
		fields = berReader(encoded)
		oid, err := fields.expect(4)
		if err != nil || string(oid) != "1.2.840.113556.1.4.319" {
			t.Fatal("missing RFC2696 response control")
		}
		encoded, err = fields.expect(4)
		if err != nil || len(fields) != 0 {
			t.Fatal("invalid control value")
		}
		fields = berReader(encoded)
		encoded, err = fields.expect(0x30)
		if err != nil || len(fields) != 0 {
			t.Fatal("invalid paging sequence")
		}
		fields = berReader(encoded)
		size, err := fields.integer(2)
		if err != nil || size != wantEstimate {
			t.Fatal("invalid result estimate")
		}
		cookie, err := fields.expect(4)
		if err != nil || len(fields) != 0 {
			t.Fatal("invalid continuation cookie")
		}
		return objects, append([]byte(nil), cookie...)
	}
	t.Fatal("unbounded page")
	return nil, nil
}

func assertDictionary(t *testing.T, objects []directoryassets.Object) {
	t.Helper()
	if len(objects) != 30 {
		t.Fatalf("object count=%d, want 30", len(objects))
	}
	for index, object := range objects {
		row := index + 1
		hex := fmt.Sprintf("%02x", row)
		guid := strings.Repeat(hex, 4) + "-" + strings.Repeat(hex, 2) + "-" + strings.Repeat(hex, 2) + "-" + strings.Repeat(hex, 2) + "-" + strings.Repeat(hex, 6)
		kind, number, ou := directoryassets.User, row, "Users"
		classes := []string{"organizationalperson", "person", "top", "user"}
		uac := uint32(512)
		if row == 1 {
			uac = 0
		}
		if row > 12 {
			kind, number, ou = directoryassets.Group, row-12, "Groups"
			classes = []string{"group", "top"}
		}
		if row > 24 {
			kind, number, ou, uac = directoryassets.Computer, row-24, "Computers", 4096
			classes = []string{"computer", "organizationalperson", "person", "top", "user"}
		}
		name := fmt.Sprintf("%s-%02d", kind, number)
		dn := fmt.Sprintf("CN=%s,OU=%s,DC=synthetic,DC=invalid", name, ou)
		if object.GUID != guid || object.DN != dn || object.Kind != kind || !reflect.DeepEqual(object.Classes, classes) {
			t.Fatalf("dictionary identity mismatch at row %d", row)
		}
		if kind == directoryassets.Computer {
			name += "$"
		}
		if row == 24 {
			if object.SAMAccountName != nil {
				t.Fatal("absent SAM was populated")
			}
		} else if object.SAMAccountName == nil || *object.SAMAccountName != name {
			t.Fatalf("SAM mismatch at row %d", row)
		}
		if kind == directoryassets.Group {
			if object.UserAccountControl != nil {
				t.Fatal("absent UAC was populated")
			}
		} else if object.UserAccountControl == nil || *object.UserAccountControl != uac {
			t.Fatalf("UAC mismatch at row %d", row)
		}
	}
}

func TestDirectoryRealTLSAndBERPages(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(map[bool]string{false: "StartTLS", true: "LDAPS"}[ldaps], func(t *testing.T) {
			fixture, config := testServer(t)
			fixture.directoryMode = true
			conn := directoryConnection(t, fixture, config, ldaps)
			send(t, conn, testDirectoryRequest(4, testDirectoryBody(), testPagingControl(1000, nil)))
			objects, cookie := receiveDirectoryPage(t, conn, 4)
			if len(cookie) != 20 || !bytes.Equal(cookie[:4], []byte{0, 255, 128, 0}) {
				t.Fatal("continuation cookie lost binary opacity")
			}
			send(t, conn, testDirectoryRequest(5, testDirectoryBody(), testPagingControl(1000, cookie)))
			second, terminal := receiveDirectoryPage(t, conn, 5)
			if len(terminal) != 0 {
				t.Fatal("last page did not terminate paging")
			}
			assertDictionary(t, append(objects, second...))
			// Neither restarting nor replaying a completed paging session is valid.
			send(t, conn, testDirectoryRequest(6, testDirectoryBody(), testPagingControl(1000, nil)))
			requireCode(t, conn, 6, 0x65, 53)
			send(t, conn, testDirectoryRequest(7, testDirectoryBody(), testPagingControl(1000, cookie)))
			requireCode(t, conn, 7, 0x65, 53)
		})
	}
}

func TestDirectoryActualReaderOverIsolatedTLSConnection(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		t.Run(string(mode), func(t *testing.T) {
			fixture, clientTLS := testServer(t)
			fixture.directoryMode = true
			var stages []ldapconnection.DirectoryStage
			cfg := ldapconnection.DirectoryConfig{
				Mode: mode, ServerName: "dc.synthetic.invalid", Domain: "SYNTHETIC.INVALID.",
				DialIP: netip.MustParseAddr("127.0.0.1"), Roots: clientTLS.RootCAs,
				AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
				Limits:          directoryassets.Limits{MaxRows: 10000, MaxPages: 100, MaxPageEntries: 1000, MaxBytes: 16 << 20, MaxCookieBytes: 65536},
				Authorize: func(_ context.Context, stage ldapconnection.DirectoryStage) error {
					stages = append(stages, stage)
					return nil
				},
			}
			credential := ldapconnection.Credential{Username: testUsername, Password: []byte(testPassword)}
			var dialCount int
			done := make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result, err := ldapconnection.ReadDirectoryWithDialForIntegrationTest(ctx, cfg, credential,
				func(_ context.Context, network, address string) (net.Conn, error) {
					dialCount++
					port := "636"
					if mode == ldapconnection.StartTLS {
						port = "389"
					}
					if network != "tcp" || address != "127.0.0.1:"+port || dialCount != 1 {
						return nil, fmt.Errorf("unexpected reader destination")
					}
					client, peer := net.Pipe()
					go func() {
						defer close(done)
						fixture.handle(ctx, peer, mode == ldapconnection.LDAPS)
					}()
					return client, nil
				})
			if err != nil {
				t.Fatalf("actual directory reader rejected synthetic fixture: %v", err)
			}
			if !bytes.Equal(credential.Password, make([]byte, len(testPassword))) {
				t.Fatal("reader did not consume and clear credential")
			}
			assertDictionary(t, result.Objects)
			if result.Source.Pages != 2 || result.Source.Domain != "synthetic.invalid" || result.Source.NamingContext != "dc=synthetic,dc=invalid" || result.Source.DCHostName != "DC.Synthetic.Invalid." {
				t.Fatalf("unexpected directory source: %+v", result.Source)
			}
			wantStages := []ldapconnection.DirectoryStage{
				ldapconnection.DirectoryValidate, ldapconnection.DirectoryDial, ldapconnection.DirectoryTLS,
				ldapconnection.DirectoryBind, ldapconnection.DirectoryRootDSE,
				ldapconnection.DirectoryPage, ldapconnection.DirectoryPage, ldapconnection.DirectoryReturn,
			}
			if !reflect.DeepEqual(stages, wantStages) {
				t.Fatalf("unexpected reader authorization sequence: %v", stages)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reader returned without closing synthetic server transport")
			}
		})
	}
}

func TestDirectoryModeDisabledByDefault(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		fixture, config := testServer(t)
		conn := directoryConnection(t, fixture, config, ldaps)
		send(t, conn, testDirectoryRequest(4, testDirectoryBody(), nil))
		requireCode(t, conn, 4, 0x65, 53)
		send(t, conn, testDirectoryRequest(5, testDirectoryBody(), testPagingControl(1000, nil)))
		if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("default mode accepted controls: %v", err)
		}
	}
}

func TestDirectoryRejectsChangedRequestAndControls(t *testing.T) {
	body := testDirectoryBody()
	controls := testPagingControl(1000, nil)
	for name, test := range map[string]struct{ body, controls []byte }{
		"base":                    {bytes.Replace(body, []byte("synthetic"), []byte("outside"), 1), controls},
		"scope":                   {bytes.Replace(body, []byte{10, 1, 2}, []byte{10, 1, 0}, 1), controls},
		"filter":                  {bytes.Replace(body, []byte("group"), []byte("other"), 1), controls},
		"attributes":              {bytes.Replace(body, []byte("objectGUID"), []byte("secretGUID"), 1), controls},
		"size limit":              {bytes.Replace(body, []byte{2, 1, 0}, []byte{2, 1, 1}, 1), controls},
		"time limit":              {bytes.Replace(body, []byte{2, 1, 3}, []byte{2, 1, 9}, 1), controls},
		"types only":              {bytes.Replace(body, []byte{1, 1, 0}, []byte{1, 1, 255}, 1), controls},
		"missing paging":          {body, nil},
		"unknown control":         {body, bytes.Replace(controls, []byte("319"), []byte("999"), 1)},
		"noncritical":             {body, bytes.Replace(controls, []byte{1, 1, 255}, []byte{1, 1, 0}, 1)},
		"duplicate":               {body, bytes.Repeat(controls, 2)},
		"zero page size":          {body, testPagingControl(0, nil)},
		"other page size":         {body, testPagingControl(15, nil)},
		"larger page size":        {body, testPagingControl(1001, nil)},
		"initial cookie":          {body, testPagingControl(1000, []byte("not-issued"))},
		"large cookie":            {body, testPagingControl(1000, bytes.Repeat([]byte{1}, 32000))},
		"trailing control fields": {body, append(append([]byte(nil), controls...), octets("extra")...)},
	} {
		t.Run(name, func(t *testing.T) {
			fixture, config := testServer(t)
			fixture.directoryMode = true
			conn := directoryConnection(t, fixture, config, true)
			send(t, conn, testDirectoryRequest(4, test.body, test.controls))
			requireCode(t, conn, 4, 0x65, 53)
		})
	}
}

func TestDirectoryRequiresTLSBindAndRootDSE(t *testing.T) {
	for _, state := range []string{"plaintext", "unbound", "bad bind", "no root", "failed rebind"} {
		t.Run(state, func(t *testing.T) {
			fixture, config := testServer(t)
			fixture.directoryMode = true
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
			send(t, conn, testDirectoryRequest(5, testDirectoryBody(), testPagingControl(1000, nil)))
			requireCode(t, conn, 5, 0x65, want)
		})
	}
}

func TestDirectoryCookieBoundToSessionAndCannotRewind(t *testing.T) {
	fixture, config := testServer(t)
	fixture.directoryMode = true
	first := directoryConnection(t, fixture, config, true)
	second := directoryConnection(t, fixture, config, true)
	send(t, first, testDirectoryRequest(4, testDirectoryBody(), testPagingControl(1000, nil)))
	_, firstCookie := receiveDirectoryPage(t, first, 4)
	send(t, second, testDirectoryRequest(4, testDirectoryBody(), testPagingControl(1000, nil)))
	_, secondCookie := receiveDirectoryPage(t, second, 4)
	if bytes.Equal(firstCookie, secondCookie) {
		t.Fatal("separate sessions reused the same cookie")
	}
	for i, cookie := range [][]byte{firstCookie, nil, bytes.ToValidUTF8(secondCookie, []byte("?"))} {
		send(t, second, testDirectoryRequest(i+5, testDirectoryBody(), testPagingControl(1000, cookie)))
		requireCode(t, second, i+5, 0x65, 53)
	}
	send(t, second, testDirectoryRequest(8, testDirectoryBody(), testPagingControl(1000, secondCookie)))
	_, end := receiveDirectoryPage(t, second, 8)
	if len(end) != 0 {
		t.Fatal("valid opaque continuation failed after rejected requests")
	}
}

func TestDirectorySuccessfulRebindInvalidatesOldCookie(t *testing.T) {
	fixture, config := testServer(t)
	fixture.directoryMode = true
	conn := directoryConnection(t, fixture, config, true)
	send(t, conn, testDirectoryRequest(4, testDirectoryBody(), testPagingControl(1000, nil)))
	_, stale := receiveDirectoryPage(t, conn, 4)
	send(t, conn, bindRequest(5, testUsername, testPassword))
	requireCode(t, conn, 5, 0x61, 0)
	send(t, conn, searchRequest(6, "", 0))
	receive(t, conn, 6, 0x64)
	requireCode(t, conn, 6, 0x65, 0)
	send(t, conn, testDirectoryRequest(7, testDirectoryBody(), testPagingControl(1000, stale)))
	requireCode(t, conn, 7, 0x65, 53)
	send(t, conn, testDirectoryRequest(8, testDirectoryBody(), testPagingControl(1000, nil)))
	_, fresh := receiveDirectoryPage(t, conn, 8)
	if bytes.Equal(stale, fresh) {
		t.Fatal("rebind reused a stale cookie")
	}
	send(t, conn, testDirectoryRequest(9, testDirectoryBody(), testPagingControl(1000, fresh)))
	receiveDirectoryPage(t, conn, 9)
}

func TestDirectoryDoesNotExpandOtherOperationsOrRootControls(t *testing.T) {
	for name, operation := range map[string][]byte{
		"modify":          element(0x66, octets("DC=synthetic,DC=invalid")),
		"add":             element(0x68, octets("CN=extra,DC=synthetic,DC=invalid")),
		"delete":          element(0x4a, []byte("CN=user-01,OU=Users,DC=synthetic,DC=invalid")),
		"controlled bind": element(0x60, integer(2, 3), octets(testUsername), element(0x80, []byte(testPassword))),
	} {
		t.Run(name, func(t *testing.T) {
			fixture, config := testServer(t)
			fixture.directoryMode = true
			conn := directoryConnection(t, fixture, config, true)
			parts := [][]byte{integer(2, 4), operation}
			if name == "controlled bind" {
				parts = append(parts, element(0xa0, testPagingControl(1000, nil)))
			}
			send(t, conn, element(0x30, parts...))
			if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("unsupported operation did not close: %v", err)
			}
		})
	}
	fixture, config := testServer(t)
	fixture.directoryMode = true
	conn := directoryConnection(t, fixture, config, true)
	root, err := readRequest(bytes.NewReader(searchRequest(4, "", 0)))
	if err != nil {
		t.Fatal(err)
	}
	send(t, conn, testDirectoryRequest(4, root.body, testPagingControl(1000, nil)))
	requireCode(t, conn, 4, 0x65, 53)
}

func TestDirectoryConnectionRequestBudgetAndOversizeEnvelope(t *testing.T) {
	fixture, config := testServer(t)
	fixture.directoryMode = true
	conn := directoryConnection(t, fixture, config, true)
	for id := 4; id <= maxRequests+1; id++ { // LDAPS bind + RootDSE consumed two requests.
		send(t, conn, testDirectoryRequest(id, testDirectoryBody(), testPagingControl(0, nil)))
		requireCode(t, conn, id, 0x65, 53)
	}
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("directory mode widened request budget: %v", err)
	}
	oversize := securePipe(t, fixture, config, true)
	send(t, oversize, []byte{0x30, 0x83, 0x01, 0x00, 0x01})
	if _, err := oversize.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("directory mode widened message bound: %v", err)
	}
}

func TestDirectoryCancellationClosesContinuationAndJoins(t *testing.T) {
	fixture, config := testServer(t)
	fixture.directoryMode = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, peer := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { defer close(done); fixture.handle(ctx, peer, true) }()
	conn := tls.Client(client, config)
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Handshake(); err != nil {
		t.Fatal(err)
	}
	send(t, conn, bindRequest(2, testUsername, testPassword))
	requireCode(t, conn, 2, 0x61, 0)
	send(t, conn, searchRequest(3, "", 0))
	receive(t, conn, 3, 0x64)
	requireCode(t, conn, 3, 0x65, 0)
	send(t, conn, testDirectoryRequest(4, testDirectoryBody(), testPagingControl(1000, nil)))
	receiveDirectoryPage(t, conn, 4)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("directory continuation handler did not close and join")
	}
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("cancelled directory connection stayed open: %v", err)
	}
}
