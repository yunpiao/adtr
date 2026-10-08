//go:build integration

package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	startTLSOID    = "1.3.6.1.4.1.1466.20037"
	maxConnections = 64
	maxRequests    = 8
	connectionTime = 15 * time.Second
)

type server struct {
	tlsConfig          *tls.Config
	username, password []byte
	control            *fixtureControl
	directoryMode      bool
	directoryEmpty     bool
	directorySlow      bool
}

type endpoint struct {
	listener net.Listener
	ldaps    bool
}

// serve owns the listeners and all accepted connections. Shutdown closes the
// raw transports (including stalled TLS handshakes), then joins every goroutine.
func (s *server) serve(ctx context.Context, endpoints []endpoint) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var acceptors, handlers sync.WaitGroup
	var mu sync.Mutex
	connections := make(map[net.Conn]struct{})
	capacity := make(chan struct{}, maxConnections)
	failures := make(chan error, len(endpoints))
	for _, endpoint := range endpoints {
		acceptors.Add(1)
		go func() {
			defer acceptors.Done()
			for {
				conn, err := endpoint.listener.Accept()
				if err != nil {
					failures <- errors.New("synthetic LDAP fixture listener stopped")
					return
				}
				select {
				case capacity <- struct{}{}:
				default:
					_ = conn.Close()
					continue
				}
				mu.Lock()
				connections[conn] = struct{}{}
				mu.Unlock()
				handlers.Add(1)
				go func() {
					defer handlers.Done()
					defer func() {
						_ = conn.Close()
						mu.Lock()
						delete(connections, conn)
						mu.Unlock()
						<-capacity
					}()
					s.handle(ctx, conn, endpoint.ldaps)
				}()
			}
		}()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-failures:
	}
	cancel()
	for _, endpoint := range endpoints {
		_ = endpoint.listener.Close()
	}
	acceptors.Wait()
	mu.Lock()
	for conn := range connections {
		_ = conn.Close()
	}
	mu.Unlock()
	handlers.Wait()
	return err
}

func (s *server) handle(ctx context.Context, raw net.Conn, ldaps bool) {
	ctx, cancel := context.WithTimeout(ctx, connectionTime)
	defer cancel()
	stopClose := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stopClose()
	var heldTicket string
	var responseWritten bool
	defer func() {
		_ = raw.Close()
		if heldTicket != "" {
			s.control.closed(heldTicket)
		}
	}()
	if raw.SetDeadline(time.Now().Add(connectionTime)) != nil {
		return
	}
	conn := raw
	secure, bound := false, false
	rootRead := false
	directory := directorySession{empty: s.directoryEmpty, slow: s.directorySlow}
	defer directory.reset()
	upgrade := func() bool {
		tlsConn := tls.Server(raw, s.tlsConfig)
		if tlsConn.HandshakeContext(ctx) != nil {
			return false
		}
		conn, secure = tlsConn, true
		return true
	}
	if ldaps && !upgrade() {
		return
	}
	for range maxRequests {
		req, err := readRequest(conn)
		if err != nil {
			return
		}
		// Only the explicit directory mode accepts a single fixed paging
		// control, and only on Search. Existing RootDSE-only behavior stays shut.
		if len(req.controls) != 0 && (!s.directoryMode || req.tag != 0x63) {
			clear(req.packet)
			return
		}
		switch req.tag {
		case 0x77:
			fields := berReader(req.body)
			oid, parseErr := fields.expect(0x80)
			valid := parseErr == nil && string(oid) == startTLSOID && len(fields) == 0
			clear(req.packet)
			if !valid || secure || bound {
				if result(conn, req.id, 0x78, 53, "synthetic operation rejected") != nil {
					return
				}
				continue
			}
			if result(conn, req.id, 0x78, 0, "") != nil || !upgrade() {
				return
			}
		case 0x60:
			bound = false
			rootRead = false
			directory.reset()
			code, diagnostic := s.bind(req.body, secure)
			clear(req.packet)
			if result(conn, req.id, 0x61, code, diagnostic) != nil {
				return
			}
			bound = code == 0
		case 0x63:
			attributes, valid := parseSearch(req.body)
			valid = valid && len(req.controls) == 0
			directorySearch := s.directoryMode && rootRead && directory.matches(req)
			clear(req.packet)
			if !secure || !bound {
				err = result(conn, req.id, 0x65, 50, "synthetic bind required")
			} else if directorySearch {
				err = directory.writePage(ctx, conn, req.id)
			} else if !valid {
				err = result(conn, req.id, 0x65, 53, "unsupported synthetic search")
			} else {
				// Search parsing and clearing its request bytes happen before the
				// optional control can publish any observation.
				if heldTicket == "" {
					heldTicket = s.control.claim()
					if heldTicket != "" && !s.control.wait(ctx, heldTicket) {
						return
					}
				}
				err = writeAll(conn, rootDSEEntryForMode(req.id, attributes, s.directoryMode))
				if err == nil {
					err = result(conn, req.id, 0x65, 0, "")
				}
				rootRead = err == nil
				if err == nil && heldTicket != "" && !responseWritten {
					if s.control.mark(heldTicket, "response_written") != nil {
						return
					}
					responseWritten = true
				}
			}
			if err != nil {
				return
			}
		default:
			// Unbind and all unsupported directory operations simply close. No
			// writes, referrals, or general directory operations exist.
			clear(req.packet)
			return
		}
	}
}

func (s *server) bind(body []byte, secure bool) (int, string) {
	if !secure {
		return 13, "synthetic TLS required"
	}
	fields := berReader(body)
	version, err := fields.integer(0x02)
	if err != nil || version != 3 {
		return 2, "invalid synthetic bind"
	}
	username, err := fields.expect(0x04)
	if err != nil || len(username) > 1024 {
		return 2, "invalid synthetic bind"
	}
	password, err := fields.expect(0x80)
	if err != nil || len(password) > 4096 || len(fields) != 0 {
		return 2, "invalid synthetic bind"
	}
	if len(username) == 0 || len(password) == 0 ||
		subtle.ConstantTimeCompare(username, s.username)&subtle.ConstantTimeCompare(password, s.password) != 1 {
		return 49, "synthetic credentials rejected"
	}
	return 0, ""
}

var rootAttributes = []struct{ name, value string }{
	{"dnsHostName", "dc.synthetic.invalid"},
	{"defaultNamingContext", "DC=synthetic,DC=invalid"},
	{"supportedCapabilities", "1.2.840.113556.1.4.800"},
	{"supportedLDAPVersion", "3"},
}

func parseSearch(body []byte) (map[string]bool, bool) {
	fields := berReader(body)
	base, err := fields.expect(0x04)
	if err != nil || len(base) != 0 {
		return nil, false
	}
	for range 2 { // baseObject scope and neverDerefAliases only.
		value, err := fields.integer(0x0a)
		if err != nil || value != 0 {
			return nil, false
		}
	}
	for range 2 { // LDAP size/time limits; the fixture always returns one entry.
		if _, err := fields.integer(0x02); err != nil {
			return nil, false
		}
	}
	typesOnly, err := fields.expect(0x01)
	if err != nil || len(typesOnly) != 1 || typesOnly[0] != 0 {
		return nil, false
	}
	filter, err := fields.expect(0x87)
	if err != nil || !strings.EqualFold(string(filter), "objectClass") {
		return nil, false
	}
	encoded, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return nil, false
	}
	selected := make(map[string]bool, len(rootAttributes))
	attributes := berReader(encoded)
	for len(attributes) != 0 {
		if len(selected) >= len(rootAttributes) {
			return nil, false
		}
		name, err := attributes.expect(0x04)
		if err != nil || len(name) > 64 {
			return nil, false
		}
		key := strings.ToLower(string(name))
		allowed := false
		for _, attribute := range rootAttributes {
			allowed = allowed || strings.ToLower(attribute.name) == key
		}
		if !allowed || selected[key] {
			return nil, false
		}
		selected[key] = true
	}
	return selected, true
}

func rootDSEEntry(id int, selected map[string]bool) []byte {
	return rootDSEEntryForMode(id, selected, false)
}

func rootDSEEntryForMode(id int, selected map[string]bool, directoryMode bool) []byte {
	var attributes []byte
	for _, attribute := range rootAttributes {
		if len(selected) == 0 || selected[strings.ToLower(attribute.name)] {
			if directoryMode && attribute.name == "dnsHostName" {
				attribute.value = "DC.Synthetic.Invalid."
			}
			attributes = append(attributes, element(0x30, octets(attribute.name), element(0x31, octets(attribute.value)))...)
		}
	}
	return message(id, 0x64, octets(""), element(0x30, attributes))
}
