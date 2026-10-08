//go:build integration

package ldapconnection

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"net"
)

// ProbeWithDialForIntegrationTest only exists in integration-tag builds. It
// preserves the complete production LDAP/TLS path while mapping its one fixed
// logical destination to an isolated ephemeral synthetic listener.
func ProbeWithDialForIntegrationTest(ctx context.Context, cfg Config, credential Credential, dial func(context.Context, string, string) (net.Conn, error)) (Result, error) {
	return probe(ctx, cfg, credential, transport{lookup: net.DefaultResolver.LookupNetIP, dial: dial})
}

// ServeAccountWireForIntegrationTest performs only the fixed synthetic TLS,
// bind and RootDSE exchange. Pair equality is checked without logging values.
func ServeAccountWireForIntegrationTest(ctx context.Context, raw net.Conn, mode Mode, certificate tls.Certificate, username, password string) error {
	defer raw.Close()
	denied := errors.New("synthetic LDAP wire contract mismatch")
	read := func(conn net.Conn, id int, tag byte) ([]byte, error) {
		got, body, err := readMessage(conn, id)
		if err != nil {
			return nil, err
		}
		if got != tag {
			return nil, denied
		}
		return body, nil
	}
	result := func(conn net.Conn, id, tag byte) error {
		_, err := conn.Write(message(id, tag, []byte{0x0a, 1, 0, 4, 0, 4, 0}))
		return err
	}
	if mode == StartTLS {
		body, err := read(raw, 1, 0x77)
		if err != nil {
			return err
		}
		if !bytes.Equal(body, element(0x80, []byte(startTLSOID))) {
			return denied
		}
		if err = result(raw, 1, 0x78); err != nil {
			return err
		}
	}
	secured := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err := secured.HandshakeContext(ctx); err != nil {
		return err
	}
	body, err := read(secured, 2, 0x60)
	if err != nil {
		return err
	}
	defer clear(body)
	fields := berReader(body)
	version, err := fields.expect(2)
	if err != nil || !bytes.Equal(version, []byte{3}) {
		return denied
	}
	user, err := fields.expect(4)
	if err != nil {
		return denied
	}
	pass, err := fields.expect(0x80)
	if err != nil || len(fields) != 0 || subtle.ConstantTimeCompare(user, []byte(username)) != 1 || subtle.ConstantTimeCompare(pass, []byte(password)) != 1 {
		return denied
	}
	clear(body)
	if err = result(secured, 2, 0x61); err != nil {
		return err
	}
	body, err = read(secured, 3, 0x63)
	if err != nil {
		return err
	}
	prefix := append([]byte{4, 0, 10, 1, 0, 10, 1, 0, 2, 1, 1, 2, 1, 3, 1, 1, 0, 0x87, 11}, []byte("objectClass")...)
	if !bytes.HasPrefix(body, prefix) {
		return denied
	}
	fields = berReader(body[len(prefix):])
	attrs, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return denied
	}
	fields = berReader(attrs)
	for _, name := range []string{"dnsHostName", "defaultNamingContext", "supportedCapabilities", "supportedLDAPVersion"} {
		value, e := fields.expect(4)
		if e != nil || string(value) != name {
			return denied
		}
	}
	if len(fields) != 0 {
		return denied
	}
	attribute := func(name, value string) []byte { return element(0x30, octets(name), element(0x31, octets(value))) }
	entry := append(octets(""), element(0x30, attribute("dnsHostName", "dc1.example.test"), attribute("defaultNamingContext", "DC=example,DC=test"), attribute("supportedCapabilities", "1.2.840.113556.1.4.800"), attribute("supportedLDAPVersion", "3"))...)
	if _, err = secured.Write(message(3, 0x64, entry)); err != nil {
		return err
	}
	if err = result(secured, 3, 0x65); err != nil {
		return err
	}
	var end [1]byte
	n, err := secured.Read(end[:])
	if n != 0 || err == nil {
		return denied
	}
	if timed, ok := err.(net.Error); ok && timed.Timeout() {
		return errors.New("synthetic LDAP connection did not close")
	}
	return nil
}
