//go:build integration

package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"io"
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
	userUACV3Profile         userUACV3Profile
	uacRecorder              *userUACWireRecorder
	tlsConfig                *tls.Config
	username, password       []byte
	control                  *fixtureControl
	emptyTransition          *fixtureEmptyTransition
	directoryMode            bool
	directoryV3              bool
	userExportFailureV3      bool
	groupExportFailureV3     bool
	computerExportFailureV3  bool
	directoryV4              bool
	groupMembersV4Pagination bool
	directoryV2              bool
	userAssetsV2             bool
	computerAssetsV3         bool
	groupAssetsV3            bool
	directoryEmpty           bool
	directorySlow            bool
}

type endpoint struct {
	listener net.Listener
	ldaps    bool
}

// serve owns the listeners and all accepted connections. Shutdown closes the
// raw transports (including stalled TLS handshakes), then joins every goroutine.
func (s *server) serve(ctx context.Context, endpoints []endpoint) error {
	parent := ctx
	if s.uacRecorder != nil {
		// The C ledger must mark owned drain before cancellation can close a
		// transport. Direct parent propagation would race the handler watcher
		// and misclassify our own close as a post-root transport failure.
		// We still select the real parent below and keep every existing limit.
		ctx = context.WithoutCancel(ctx)
	}
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
					s.uacRecorder.recordFailure("handler_failure")
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
	case <-parent.Done():
	case err = <-failures:
	}
	s.uacRecorder.BeginDrain()
	cancel()
	for _, endpoint := range endpoints {
		if closeErr := endpoint.listener.Close(); closeErr != nil {
			s.uacRecorder.recordFailure("shutdown_incomplete")
		}
	}
	acceptors.Wait()
	mu.Lock()
	for conn := range connections {
		_ = conn.Close()
	}
	mu.Unlock()
	handlers.Wait()
	if err != nil {
		s.uacRecorder.recordFailure("handler_failure")
	}
	if sealErr := s.uacRecorder.SealAfterJoin(); sealErr != nil {
		return sealErr
	}
	return err
}

func (s *server) handle(ctx context.Context, raw net.Conn, ldaps bool) {
	transport := uacStartTLS
	if ldaps {
		transport = uacLDAPS
	}
	uacConnectionID, recordErr := s.uacRecorder.OpenConnection(transport)
	if recordErr != nil {
		return
	}
	closeReason := uacTransportFailure
	defer func() {
		if s.uacRecorder.draining() && closeReason == uacTransportFailure {
			closeReason = uacOwnedDrain
		}
		s.uacRecorder.CloseConnection(uacConnectionID, closeReason)
	}()

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
	directory := directorySession{userUACV3Profile: s.userUACV3Profile, uacRecorder: s.uacRecorder, uacConnectionID: uacConnectionID, v2: s.directoryV2, v3: s.directoryV3, userExportFailureV3: s.userExportFailureV3, groupExportFailureV3: s.groupExportFailureV3, computerExportFailureV3: s.computerExportFailureV3, v4: s.directoryV4, groupMembersV4Pagination: s.groupMembersV4Pagination, userAssetsV2: s.userAssetsV2, computerAssetsV3: s.computerAssetsV3, groupAssetsV3: s.groupAssetsV3, empty: s.directoryEmpty, slow: s.directorySlow, emptyTransition: s.emptyTransition}
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
		reader := &uacCountingReader{reader: conn}
		req, err := readRequest(reader)
		if err != nil {
			if errors.Is(err, io.EOF) && reader.count == 0 {
				closeReason = uacCleanEOF
			} else if reader.count > 0 {
				closeReason = uacIncompleteRead
			} else if errors.Is(err, errProtocol) {
				closeReason = uacProtocolFailure
			}
			return
		}
		// Observe every request before clearing or any matcher rejection. This
		// includes stale cookies, rebinds, and post-completion attempts.
		if s.uacRecorder != nil {
			event := uacRequestEvent{kind: uacOther, messageID: req.id, tlsVerified: secure, bindSucceeded: bound, rootResponseComplete: rootRead}
			switch req.tag {
			case 0x60:
				event.kind = uacBind
			case 0x77:
				event.kind = uacStartTLSRequest
			case 0x63:
				_, rootRequest := parseSearch(req.body)
				if rootRequest && len(req.controls) == 0 {
					event.kind = uacRootDSE
				} else {
					event = uacDirectoryEvent(req, &directory, secure, bound, rootRead)
				}
			}
			if s.uacRecorder.ObserveRequest(uacConnectionID, event) != nil {
				clear(req.packet)
				closeReason = uacProtocolFailure
				return
			}
		}

		// Only the explicit directory mode accepts a single fixed paging
		// control, and only on Search. Existing RootDSE-only behavior stays shut.
		if len(req.controls) != 0 && (!s.directoryMode || req.tag != 0x63) {
			closeReason = uacProtocolFailure
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
				// Sample only after TLS, bind, RootDSE and the exact search and
				// paging control have passed. A malformed marker fails closed;
				// continuation pages keep the first page's immutable snapshot.
				err = directory.sampleEmptyTransition()
				if err == nil {
					err = directory.writePage(ctx, conn, req.id)
				}
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
				if rootRead {
					s.uacRecorder.rootResponseCompleted(uacConnectionID)
				}
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
			closeReason = uacProtocolFailure
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
