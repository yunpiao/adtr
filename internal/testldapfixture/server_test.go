//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testUsername = "fixture-user@synthetic.invalid"
	testPassword = "synthetic-fixture-password-only"
)

func testServer(t *testing.T) (*server, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic fixture only"},
		DNSNames: []string{"dc.synthetic.invalid"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &server{
		tlsConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
		username:  []byte(testUsername), password: []byte(testPassword),
	}, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "dc.synthetic.invalid", RootCAs: roots}
}

func startPipe(t *testing.T, fixture *server, ldaps bool) net.Conn {
	t.Helper()
	client, peer := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.handle(ctx, peer, ldaps)
	}()
	if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		_ = peer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fixture handler did not stop")
		}
	})
	return client
}

func send(t *testing.T, conn net.Conn, packet []byte) {
	t.Helper()
	if err := writeAll(conn, packet); err != nil {
		t.Fatal(err)
	}
}

func receive(t *testing.T, conn net.Conn, id int, tag byte) []byte {
	t.Helper()
	response, err := readRequest(conn)
	if err != nil {
		t.Fatal(err)
	}
	if response.id != id || response.tag != tag {
		t.Fatalf("unexpected response: id=%d tag=%x", response.id, response.tag)
	}
	return response.body
}

func requireCode(t *testing.T, conn net.Conn, id int, tag byte, want int) string {
	t.Helper()
	fields := berReader(receive(t, conn, id, tag))
	code, err := fields.integer(0x0a)
	if err != nil || code != want {
		t.Fatalf("response code=%d, want %d (parse error=%v)", code, want, err)
	}
	matched, err := fields.expect(0x04)
	if err != nil || len(matched) != 0 {
		t.Fatal("unexpected matched DN")
	}
	diagnostic, err := fields.expect(0x04)
	if err != nil || len(fields) != 0 {
		t.Fatal("unexpected response fields, controls, or referral")
	}
	if strings.Contains(string(diagnostic), testUsername) || strings.Contains(string(diagnostic), testPassword) {
		t.Fatal("credential leaked in diagnostic")
	}
	return string(diagnostic)
}

func securePipe(t *testing.T, fixture *server, clientTLS *tls.Config, ldaps bool) net.Conn {
	t.Helper()
	conn := startPipe(t, fixture, ldaps)
	if !ldaps {
		send(t, conn, message(1, 0x77, element(0x80, []byte(startTLSOID))))
		requireCode(t, conn, 1, 0x78, 0)
	}
	secured := tls.Client(conn, clientTLS)
	if err := secured.Handshake(); err != nil {
		t.Fatal(err)
	}
	if !secured.ConnectionState().HandshakeComplete {
		t.Fatal("real TLS handshake did not complete")
	}
	return secured
}

func bindRequest(id int, username, password string) []byte {
	return message(id, 0x60, integer(0x02, 3), octets(username), element(0x80, []byte(password)))
}

func searchRequest(id int, base string, scope int, attributes ...string) []byte {
	var selection []byte
	for _, attribute := range attributes {
		selection = append(selection, octets(attribute)...)
	}
	return message(id, 0x63, octets(base), integer(0x0a, scope), integer(0x0a, 0),
		integer(0x02, 1), integer(0x02, 3), element(0x01, []byte{0}),
		element(0x87, []byte("objectClass")), element(0x30, selection))
}

func TestStartTLSAndLDAPSRealWireExchange(t *testing.T) {
	for _, mode := range []struct {
		name  string
		ldaps bool
	}{{"StartTLS", false}, {"LDAPS", true}} {
		t.Run(mode.name, func(t *testing.T) {
			fixture, clientTLS := testServer(t)
			conn := securePipe(t, fixture, clientTLS, mode.ldaps)
			send(t, conn, bindRequest(128, testUsername, testPassword))
			requireCode(t, conn, 128, 0x61, 0)
			send(t, conn, searchRequest(2147483647, "", 0,
				"dnsHostName", "defaultNamingContext", "supportedCapabilities", "supportedLDAPVersion"))
			fields := berReader(receive(t, conn, 2147483647, 0x64))
			dn, err := fields.expect(0x04)
			if err != nil || len(dn) != 0 {
				t.Fatal("RootDSE must use an empty DN")
			}
			encoded, err := fields.expect(0x30)
			if err != nil || len(fields) != 0 {
				t.Fatal("invalid RootDSE entry")
			}
			attributes := berReader(encoded)
			want := map[string]string{
				"dnsHostName": "dc.synthetic.invalid", "defaultNamingContext": "DC=synthetic,DC=invalid",
				"supportedCapabilities": "1.2.840.113556.1.4.800", "supportedLDAPVersion": "3",
			}
			for len(attributes) != 0 {
				encoded, err := attributes.expect(0x30)
				if err != nil {
					t.Fatal(err)
				}
				attribute := berReader(encoded)
				name, err := attribute.expect(0x04)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err = attribute.expect(0x31)
				if err != nil || len(attribute) != 0 {
					t.Fatal("invalid attribute values")
				}
				values := berReader(encoded)
				value, err := values.expect(0x04)
				if err != nil || len(values) != 0 || string(value) != want[string(name)] {
					t.Fatalf("unexpected attribute %q=%q", name, value)
				}
				delete(want, string(name))
			}
			if len(want) != 0 {
				t.Fatalf("missing attributes: %v", want)
			}
			requireCode(t, conn, 2147483647, 0x65, 0)
		})
	}
}

func TestBindRequiresTLSAndNonAnonymousCorrectCredentials(t *testing.T) {
	for _, test := range []struct {
		name, username, password string
		tls                      bool
		code                     int
	}{
		{"plaintext", testUsername, testPassword, false, 13},
		{"anonymous", "", "", true, 49},
		{"empty password", testUsername, "", true, 49},
		{"empty username", "", testPassword, true, 49},
		{"wrong username", "other@synthetic.invalid", testPassword, true, 49},
		{"wrong password", testUsername, "wrong-synthetic-password", true, 49},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, clientTLS := testServer(t)
			var conn net.Conn
			if test.tls {
				conn = securePipe(t, fixture, clientTLS, true)
			} else {
				conn = startPipe(t, fixture, false)
			}
			send(t, conn, bindRequest(2, test.username, test.password))
			requireCode(t, conn, 2, 0x61, test.code)
			send(t, conn, searchRequest(3, "", 0))
			requireCode(t, conn, 3, 0x65, 50)
		})
	}
}

func TestFailedRebindRemovesSearchAuthorization(t *testing.T) {
	fixture, clientTLS := testServer(t)
	conn := securePipe(t, fixture, clientTLS, true)
	send(t, conn, bindRequest(1, testUsername, testPassword))
	requireCode(t, conn, 1, 0x61, 0)
	send(t, conn, bindRequest(2, testUsername, "wrong"))
	requireCode(t, conn, 2, 0x61, 49)
	send(t, conn, searchRequest(3, "", 0))
	requireCode(t, conn, 3, 0x65, 50)
}

func TestRejectsNonRootSearchesAndStartTLSInsideTLS(t *testing.T) {
	fixture, clientTLS := testServer(t)
	conn := securePipe(t, fixture, clientTLS, true)
	send(t, conn, bindRequest(1, testUsername, testPassword))
	requireCode(t, conn, 1, 0x61, 0)
	for i, packet := range [][]byte{
		searchRequest(2, "DC=synthetic,DC=invalid", 0),
		searchRequest(3, "", 2),
		searchRequest(4, "", 0, "userPassword"),
		searchRequest(5, "", 0, "dnsHostName", "dnsHostName"),
	} {
		send(t, conn, packet)
		requireCode(t, conn, i+2, 0x65, 53)
	}
	send(t, conn, message(6, 0x77, element(0x80, []byte(startTLSOID))))
	requireCode(t, conn, 6, 0x78, 53)
}

func TestRejectsTLSCertificateWithoutClientTrust(t *testing.T) {
	fixture, clientTLS := testServer(t)
	clientTLS.RootCAs = x509.NewCertPool()
	conn := tls.Client(startPipe(t, fixture, true), clientTLS)
	var unknown x509.UnknownAuthorityError
	if err := conn.Handshake(); !errors.As(err, &unknown) {
		t.Fatalf("expected certificate trust rejection, got %v", err)
	}
}

func TestConnectionRequestBudgetClosesConnection(t *testing.T) {
	fixture, _ := testServer(t)
	conn := startPipe(t, fixture, false)
	for i := range maxRequests {
		send(t, conn, bindRequest(i+1, testUsername, testPassword))
		requireCode(t, conn, i+1, 0x61, 13)
	}
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("request budget did not close connection: %v", err)
	}
}

func TestMalformedBERAndUnsupportedOperationsClose(t *testing.T) {
	for name, packet := range map[string][]byte{
		"wrong envelope":    {0x31, 0},
		"indefinite length": {0x30, 0x80},
		"oversized":         {0x30, 0x83, 0x01, 0x00, 0x01},
		"invalid id":        message(0, 0x60),
		"controls":          element(0x30, integer(0x02, 1), element(0x60), element(0xa0)),
		"directory modify":  message(1, 0x66, octets("DC=synthetic,DC=invalid")),
		"unbind":            message(1, 0x42),
	} {
		t.Run(name, func(t *testing.T) {
			fixture, _ := testServer(t)
			conn := startPipe(t, fixture, false)
			send(t, conn, packet)
			if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("invalid operation did not close connection: %v", err)
			}
		})
	}
}

func TestBoundedBERParser(t *testing.T) {
	for _, packet := range [][]byte{
		{}, {0x30}, {0x30, 0x81, 0x7f}, {0x30, 0x82, 0, 0x80}, {0x30, 0x84},
		{0x30, 0x83, 0x01, 0x00, 0x01}, {0x30, 0x80},
		element(0x30, element(0x02, []byte{0x80}), element(0x60)),
		element(0x30, element(0x02, []byte{0, 1}), element(0x60)),
		element(0x30, integer(0x02, 1), []byte{0x7f, 0}),
		element(0x30, integer(0x02, 1), []byte{0x60, 0x80}),
		element(0x30, integer(0x02, 1), []byte{0x60, 5}),
	} {
		if _, err := readRequest(bytes.NewReader(packet)); err == nil {
			t.Fatalf("invalid BER accepted: %x", packet)
		}
	}
}

// A channel listener uses real net.Conn transports without opening OS sockets.
type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddress{} }

type pipeAddress struct{}

func (pipeAddress) Network() string { return "pipe" }
func (pipeAddress) String() string  { return "synthetic fixture pipe" }

func TestShutdownClosesIdleAndTLSConnectionsAndJoins(t *testing.T) {
	fixture, _ := testServer(t)
	plain, secure := newPipeListener(), newPipeListener()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- fixture.serve(ctx, []endpoint{{listener: plain}, {listener: secure, ldaps: true}}) }()
	for _, listener := range []*pipeListener{plain, secure} {
		client, peer := net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
		listener.connections <- peer
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join acceptors and active handlers")
	}
	for _, listener := range []*pipeListener{plain, secure} {
		select {
		case <-listener.closed:
		default:
			t.Fatal("shutdown left listener open")
		}
	}
}

func TestConnectionLimitClosesExcessPeer(t *testing.T) {
	fixture, _ := testServer(t)
	listener := newPipeListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fixture.serve(ctx, []endpoint{{listener: listener}}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("shutdown did not complete")
		}
	})
	var excess net.Conn
	for range maxConnections + 1 {
		client, peer := net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		listener.connections <- peer
		excess = client
	}
	if _, err := excess.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("excess connection did not close: %v", err)
	}
}
