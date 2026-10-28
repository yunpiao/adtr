//go:build integration

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

var (
	errUACConfiguration = errors.New("uac_evidence_invalid:configuration")
	errUACTranscript    = errors.New("uac_evidence_invalid:raw_transcript")
	errUACSeal          = errors.New("uac_evidence_invalid:terminal_seal")
)

type uacTransport string
type uacRequestKind string
type uacCookieRelation string
type uacFrameKind string
type uacCloseReason string

const (
	uacStartTLS          uacTransport      = "starttls"
	uacLDAPS             uacTransport      = "ldaps"
	uacBind              uacRequestKind    = "bind"
	uacStartTLSRequest   uacRequestKind    = "start_tls"
	uacRootDSE           uacRequestKind    = "root_dse"
	uacDirectory         uacRequestKind    = "directory"
	uacOther             uacRequestKind    = "other"
	uacCookieEmpty       uacCookieRelation = "empty"
	uacCookieMatches     uacCookieRelation = "matches_expected"
	uacCookieMismatch    uacCookieRelation = "mismatch"
	uacCookieUnavailable uacCookieRelation = "unavailable"
	uacEntry             uacFrameKind      = "entry"
	uacDone              uacFrameKind      = "done"
	uacCleanEOF          uacCloseReason    = "clean_eof"
	uacOwnedDrain        uacCloseReason    = "owned_drain"
	uacIncompleteRead    uacCloseReason    = "incomplete_read"
	uacProtocolFailure   uacCloseReason    = "protocol_failure"
	uacTransportFailure  uacCloseReason    = "transport_failure"
)

type uacRequestEvent struct {
	kind                                                                     uacRequestKind
	messageID                                                                int
	tlsVerified, bindSucceeded, rootResponseComplete, fixedBody, pagingValid bool
	cookieRelation                                                           uacCookieRelation
	requestCookieSHA256                                                      string
}
type uacConnection struct {
	transport uacTransport
	rootReady bool
}
type uacWriteTicket struct {
	recorder *userUACWireRecorder
	serial   uint64
}
type uacPendingWrite struct {
	connID                   uint64
	pageIdx, ordinal, length int
	kind                     uacFrameKind
}
type uacEntryEvidence struct {
	EntryOrdinal int    `json:"entryOrdinal"`
	MessageID    int    `json:"messageID"`
	FrameBase64  string `json:"frameBase64"`
	SHA256       string `json:"sha256"`
}
type uacDoneEvidence struct {
	RedactedFrameBase64 string `json:"redactedFrameBase64"`
	CookieValueOffset   int    `json:"cookieValueOffset"`
	CookieValueLength   int    `json:"cookieValueLength"`
	OriginalFrameSHA256 string `json:"originalFrameSHA256"`
	RedactedFrameSHA256 string `json:"redactedFrameSHA256"`
	CookieSHA256        string `json:"cookieSHA256"`
	RequestCookieSHA256 string `json:"requestCookieSHA256"`
}
type uacPageEvidence struct {
	PageIdx             int                `json:"pageIdx"`
	RequestMessageID    int                `json:"requestMessageID"`
	RequestCookieSHA256 string             `json:"requestCookieSHA256"`
	Entries             []uacEntryEvidence `json:"entries"`
	Done                *uacDoneEvidence   `json:"done"`
}
type uacCandidateEvidence struct {
	Format             int               `json:"format"`
	Profile            userUACV3Profile  `json:"profile"`
	Nonce              string            `json:"nonce"`
	Transport          uacTransport      `json:"transport"`
	ClaimOrdinal       int               `json:"claimOrdinal"`
	EntryCount         int               `json:"entryCount"`
	PageCount          int               `json:"pageCount"`
	CompletionSequence uint64            `json:"completionSequence"`
	Pages              []uacPageEvidence `json:"pages"`
}
type uacSealEvidence struct {
	Format                    int              `json:"format"`
	Profile                   userUACV3Profile `json:"profile"`
	Nonce                     string           `json:"nonce"`
	CandidateTranscriptSHA256 string           `json:"candidateTranscriptSHA256"`
	ClaimCount                int              `json:"claimCount"`
	CompletedExchangeCount    int              `json:"completedExchangeCount"`
	DirectoryAttemptCount     int              `json:"directoryAttemptCount"`
	PageCount                 int              `json:"pageCount"`
	EntryCount                int              `json:"entryCount"`
	ActiveHandlers            int              `json:"activeHandlers"`
	InflightWrites            int              `json:"inflightWrites"`
	ListenersClosed           bool             `json:"listenersClosed"`
	ParticipantClosed         bool             `json:"participantClosed"`
	Poisoned                  bool             `json:"poisoned"`
	PoisonCode                string           `json:"poisonCode"`
	LateDirectoryAttempts     int              `json:"lateDirectoryAttempts"`
	ParticipantRebinds        int              `json:"participantRebinds"`
	TerminalState             string           `json:"terminalState"`
	CompletionSequence        uint64           `json:"completionSequence"`
	FinalSequence             uint64           `json:"finalSequence"`
}

// The mutex linearizes every event, including rejected requests. No network I/O
// happens under it. Tickets exist before Write and are consumed synchronously.
// No raw request, bind body, credential, or credential digest is retained.
type userUACWireRecorder struct {
	mu                                                   sync.Mutex
	root                                                 *os.Root
	nonce                                                string
	connections                                          map[uint64]*uacConnection
	pending                                              map[uint64]uacPendingWrite
	nextConnection, nextTicket, sequence, participant    uint64
	closing, sealed, participantClosed                   bool
	poison                                               string
	attempts, lateAttempts, rebinds, rawBytes, doneBytes int
	candidate                                            uacCandidateEvidence
	candidateHash                                        string
}

func (*userUACWireRecorder) String() string     { return "uac wire recorder (redacted)" }
func (r *userUACWireRecorder) GoString() string { return r.String() }

func uacHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func uacSHA(value []byte) string { hash := sha256.Sum256(value); return hex.EncodeToString(hash[:]) }
func newUserUACWireRecorder(profile userUACV3Profile, evidenceDir, nonce string) (*userUACWireRecorder, error) {
	if profile == userUACNone {
		if evidenceDir != "" || nonce != "" {
			return nil, errUACConfiguration
		}
		return nil, nil
	}
	if profile != userUACMixed33 || evidenceDir == "" || !uacHex(nonce, 32) {
		return nil, errUACConfiguration
	}
	info, err := os.Lstat(evidenceDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errUACConfiguration
	}
	root, err := os.OpenRoot(evidenceDir)
	if err != nil {
		return nil, errUACConfiguration
	}
	for _, name := range []string{"candidate-wire.json", "terminal-seal.json", ".candidate-wire.json.tmp", ".terminal-seal.json.tmp"} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			root.Close()
			return nil, errUACConfiguration
		}
	}
	return &userUACWireRecorder{root: root, nonce: nonce, connections: make(map[uint64]*uacConnection), pending: make(map[uint64]uacPendingWrite), poison: "none", candidate: uacCandidateEvidence{Format: 1, Profile: profile, Nonce: nonce, ClaimOrdinal: 1, Pages: make([]uacPageEvidence, 0, 4)}}, nil
}
func (r *userUACWireRecorder) fail(code string) error {
	if r.poison == "none" {
		r.poison = code
	}
	return errUACTranscript
}
func (r *userUACWireRecorder) OpenConnection(transport uacTransport) (uint64, error) {
	if r == nil {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	if r.closing || r.sealed || transport != uacStartTLS && transport != uacLDAPS || len(r.connections) >= maxConnections {
		return 0, r.fail("handler_failure")
	}
	r.nextConnection++
	r.connections[r.nextConnection] = &uacConnection{transport: transport}
	return r.nextConnection, nil
}
func (r *userUACWireRecorder) ObserveRequest(connID uint64, event uacRequestEvent) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	conn, ok := r.connections[connID]
	if !ok || r.closing || r.sealed {
		return r.fail("handler_failure")
	}
	// This records historical RootDSE completion, not the live bind session.
	// Rebind may reset request eligibility, never the ambiguous-read obligation.
	conn.rootReady = conn.rootReady || event.rootResponseComplete
	if event.kind != uacDirectory {
		if connID == r.participant {
			if event.kind == uacBind {
				r.rebinds++
				return r.fail("participant_rebind")
			}
			return r.fail("unexpected_participant_request")
		}
		switch event.kind {
		case uacBind, uacStartTLSRequest, uacRootDSE, uacOther:
			return nil
		default:
			return r.fail("handler_failure")
		}
	}
	r.attempts++
	if r.candidateHash != "" {
		r.lateAttempts++
		return r.fail("invalid_continuation")
	}
	if !event.tlsVerified || !event.bindSucceeded || !event.rootResponseComplete || !event.fixedBody || !event.pagingValid || !uacHex(event.requestCookieSHA256, 64) {
		return r.fail("invalid_directory_request")
	}
	page := len(r.candidate.Pages)
	if r.participant == 0 {
		if event.messageID != 4 || event.cookieRelation != uacCookieEmpty || event.requestCookieSHA256 != uacSHA(nil) {
			return r.fail("invalid_directory_request")
		}
		r.participant = connID
		r.candidate.Transport = conn.transport
	} else if connID != r.participant {
		return r.fail("duplicate_claim")
	}
	if page >= 4 || event.messageID != page+4 || len(r.pending) != 0 {
		return r.fail("invalid_continuation")
	}
	if page > 0 {
		previous := r.candidate.Pages[page-1].Done
		if previous == nil || event.cookieRelation != uacCookieMatches || previous.CookieSHA256 != event.requestCookieSHA256 {
			return r.fail("invalid_continuation")
		}
	}
	if r.poison != "none" {
		return errUACTranscript
	}
	r.candidate.Pages = append(r.candidate.Pages, uacPageEvidence{PageIdx: page + 1, RequestMessageID: event.messageID, RequestCookieSHA256: event.requestCookieSHA256, Entries: make([]uacEntryEvidence, 0, 15)})
	return nil
}
func (r *userUACWireRecorder) rootResponseCompleted(connID uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	if conn := r.connections[connID]; conn != nil {
		conn.rootReady = true
	} else {
		r.fail("handler_failure")
	}
}
func (r *userUACWireRecorder) BeginWrite(connID uint64, pageIdx, entryOrdinal int, kind uacFrameKind, frameBytes int) (uacWriteTicket, error) {
	if r == nil {
		return uacWriteTicket{}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	if r.poison != "none" || r.closing || r.sealed || connID != r.participant || len(r.pending) != 0 || pageIdx < 1 || pageIdx > 4 || pageIdx != len(r.candidate.Pages) {
		return uacWriteTicket{}, r.fail("handler_failure")
	}
	page := &r.candidate.Pages[pageIdx-1]
	count := 15
	if pageIdx == 4 {
		count = 6
	}
	if page.Done != nil || frameBytes < 1 || r.rawBytes+frameBytes > 131072 {
		return uacWriteTicket{}, r.fail("handler_failure")
	}
	switch kind {
	case uacEntry:
		if frameBytes > 2048 || len(page.Entries) >= count || entryOrdinal != r.candidate.EntryCount {
			return uacWriteTicket{}, r.fail("partial_entry_write")
		}
	case uacDone:
		if entryOrdinal != -1 || len(page.Entries) != count || r.doneBytes+frameBytes > 4096 {
			return uacWriteTicket{}, r.fail("invalid_done")
		}
	default:
		return uacWriteTicket{}, r.fail("handler_failure")
	}
	r.nextTicket++
	r.pending[r.nextTicket] = uacPendingWrite{connID: connID, pageIdx: pageIdx, ordinal: entryOrdinal, length: frameBytes, kind: kind}
	return uacWriteTicket{recorder: r, serial: r.nextTicket}, nil
}
func (r *userUACWireRecorder) FinishWrite(ticket uacWriteTicket, actualFrame []byte, complete bool) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	pending, ok := r.pending[ticket.serial]
	if ticket.recorder != r || !ok {
		return r.fail("handler_failure")
	}
	delete(r.pending, ticket.serial)
	if !complete || len(actualFrame) != pending.length {
		code := "partial_entry_write"
		if pending.kind == uacDone {
			code = "partial_done_write"
		}
		return r.fail(code)
	}
	if r.poison != "none" || r.closing {
		return r.fail("handler_failure")
	}
	page := &r.candidate.Pages[pending.pageIdx-1]
	if pending.kind == uacEntry {
		reader := bytes.NewReader(actualFrame)
		req, err := readRequest(reader)
		defer clear(req.packet)
		if err != nil || reader.Len() != 0 || req.tag != 0x64 || req.id != page.RequestMessageID || len(req.controls) != 0 {
			return r.fail("partial_entry_write")
		}
		page.Entries = append(page.Entries, uacEntryEvidence{EntryOrdinal: pending.ordinal, MessageID: req.id, FrameBase64: base64.StdEncoding.EncodeToString(actualFrame), SHA256: uacSHA(actualFrame)})
		r.candidate.EntryCount++
	} else {
		done, err := uacRedactDone(actualFrame, page.RequestMessageID, pending.pageIdx, page.RequestCookieSHA256)
		if err != nil {
			return r.fail("invalid_done")
		}
		page.Done = &done
		r.doneBytes += len(actualFrame)
		r.candidate.PageCount++
	}
	r.rawBytes += len(actualFrame)
	if r.candidate.PageCount == 4 {
		if r.candidate.EntryCount != 51 || r.attempts != 4 {
			return r.fail("incomplete_exchange")
		}
		r.candidate.CompletionSequence = r.sequence
		data, err := json.Marshal(r.candidate)
		if err != nil || len(data) > 163840 || r.publish("candidate-wire.json", data) != nil {
			return r.fail("seal_io_failure")
		}
		r.candidateHash = uacSHA(data)
	}
	return nil
}

// Parse the actual successful frame, never reconstruct a Done. Every enclosing
// field must be consumed. Thus the final cookie is the last value in the frame;
// only its payload is zeroed, preserving every original tag/length/control byte.
func uacRedactDone(frame []byte, id, page int, requestHash string) (uacDoneEvidence, error) {
	bad := func() (uacDoneEvidence, error) { return uacDoneEvidence{}, errUACTranscript }
	outer := berReader(frame)
	body, err := outer.expect(0x30)
	if err != nil || len(outer) != 0 {
		return bad()
	}
	fields := berReader(body)
	actualID, err := fields.integer(2)
	if err != nil || actualID != id {
		return bad()
	}
	result, err := fields.expect(0x65)
	if err != nil || !bytes.Equal(result, []byte{10, 1, 0, 4, 0, 4, 0}) {
		return bad()
	}
	controls, err := fields.expect(0xa0)
	if err != nil || len(fields) != 0 {
		return bad()
	}
	fields = berReader(controls)
	control, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return bad()
	}
	fields = berReader(control)
	oid, err := fields.expect(4)
	if err != nil || string(oid) != pagingOID {
		return bad()
	}
	value, err := fields.expect(4)
	if err != nil || len(fields) != 0 {
		return bad()
	}
	fields = berReader(value)
	sequence, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return bad()
	}
	fields = berReader(sequence)
	estimate, err := fields.integer(2)
	if err != nil || estimate != 51 {
		return bad()
	}
	cookie, err := fields.expect(4)
	if err != nil || len(fields) != 0 || page < 1 || page > 4 || page < 4 && len(cookie) != 20 || page == 4 && len(cookie) != 0 {
		return bad()
	}
	offset := len(frame) - len(cookie)
	redacted := append([]byte(nil), frame...)
	clear(redacted[offset:])
	defer clear(redacted)
	return uacDoneEvidence{RedactedFrameBase64: base64.StdEncoding.EncodeToString(redacted), CookieValueOffset: offset, CookieValueLength: len(cookie), OriginalFrameSHA256: uacSHA(frame), RedactedFrameSHA256: uacSHA(redacted), CookieSHA256: uacSHA(cookie), RequestCookieSHA256: requestHash}, nil
}
func (r *userUACWireRecorder) CloseConnection(connID uint64, reason uacCloseReason) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	conn, ok := r.connections[connID]
	if !ok {
		r.fail("handler_failure")
		return
	}
	delete(r.connections, connID)
	complete := r.candidateHash != ""
	if connID == r.participant {
		r.participantClosed = true
		if !complete {
			r.fail("incomplete_exchange")
		} else if reason != uacCleanEOF && !(reason == uacOwnedDrain && r.closing) {
			r.fail("ambiguous_post_root_read")
		}
	} else if conn.rootReady && reason != uacCleanEOF && !(reason == uacOwnedDrain && r.closing) {
		r.fail("ambiguous_post_root_read")
	}
	switch reason {
	case uacCleanEOF, uacOwnedDrain, uacIncompleteRead, uacProtocolFailure, uacTransportFailure:
	default:
		r.fail("handler_failure")
	}
}
func (r *userUACWireRecorder) BeginDrain() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	if r.sealed {
		r.fail("shutdown_incomplete")
	}
	r.closing = true
}
func (r *userUACWireRecorder) draining() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closing
}
func (r *userUACWireRecorder) SealAfterJoin() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	if r.sealed || !r.closing || r.poison != "none" || len(r.connections) != 0 || len(r.pending) != 0 || r.participant == 0 || !r.participantClosed || r.candidateHash == "" || r.candidate.PageCount != 4 || r.candidate.EntryCount != 51 || r.attempts != 4 || r.lateAttempts != 0 || r.rebinds != 0 {
		r.fail("shutdown_incomplete")
		return errUACSeal
	}
	seal := uacSealEvidence{Format: 1, Profile: userUACMixed33, Nonce: r.nonce, CandidateTranscriptSHA256: r.candidateHash, ClaimCount: 1, CompletedExchangeCount: 1, DirectoryAttemptCount: r.attempts, PageCount: r.candidate.PageCount, EntryCount: r.candidate.EntryCount, ActiveHandlers: len(r.connections), InflightWrites: len(r.pending), ListenersClosed: true, ParticipantClosed: r.participantClosed, Poisoned: false, PoisonCode: "none", LateDirectoryAttempts: r.lateAttempts, ParticipantRebinds: r.rebinds, TerminalState: "sealed_ok", CompletionSequence: r.candidate.CompletionSequence, FinalSequence: r.sequence}
	data, err := json.Marshal(seal)
	if err != nil || len(data) > 8192 || r.publish("terminal-seal.json", data) != nil {
		r.fail("seal_io_failure")
		return errUACSeal
	}
	r.sealed = true
	return nil
}
func (r *userUACWireRecorder) publish(name string, data []byte) error {
	temporary := "." + name + ".tmp"
	f, err := r.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errUACTranscript
	}
	defer r.root.Remove(temporary)
	err = writeAll(f, data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errUACTranscript
	}
	// Link is an atomic no-replace publication. Existing files, symlinks and FIFOs
	// all fail; no unredacted Done is ever written to a staging file.
	if r.root.Link(temporary, name) != nil {
		return errUACTranscript
	}
	if r.root.Remove(temporary) != nil {
		return errUACTranscript
	}
	directory, err := r.root.Open(".")
	if err != nil {
		return errUACTranscript
	}
	syncErr := directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil || closeErr != nil {
		return errUACTranscript
	}
	return nil
}

type uacCountingReader struct {
	reader io.Reader
	count  int
}

func (r *uacCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.count += n
	return n, err
}

// Inspect request controls before the request buffer is cleared. Only synthetic
// continuation cookies may be hashed, and only after the exact paging parse.
func uacDirectoryEvent(req request, d *directorySession, secure, bound, root bool) uacRequestEvent {
	event := uacRequestEvent{kind: uacDirectory, messageID: req.id, tlsVerified: secure, bindSucceeded: bound, rootResponseComplete: root, fixedBody: bytes.Equal(req.body, directoryV3SearchBody), cookieRelation: uacCookieUnavailable}
	fields := berReader(req.controls)
	control, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return event
	}
	fields = berReader(control)
	oid, err := fields.expect(4)
	if err != nil || string(oid) != pagingOID {
		return event
	}
	critical, err := fields.expect(1)
	if err != nil || !bytes.Equal(critical, []byte{255}) {
		return event
	}
	value, err := fields.expect(4)
	if err != nil || len(fields) != 0 {
		return event
	}
	fields = berReader(value)
	sequence, err := fields.expect(0x30)
	if err != nil || len(fields) != 0 {
		return event
	}
	fields = berReader(sequence)
	size, err := fields.integer(2)
	if err != nil || size != 1000 {
		return event
	}
	cookie, err := fields.expect(4)
	if err != nil || len(fields) != 0 || len(cookie) != 0 && len(cookie) != 20 {
		return event
	}
	event.pagingValid = true
	event.requestCookieSHA256 = uacSHA(cookie)
	event.cookieRelation = uacCookieMismatch
	if len(cookie) == 0 {
		event.cookieRelation = uacCookieEmpty
	} else if bytes.Equal(cookie, d.cookie) {
		event.cookieRelation = uacCookieMatches
	}
	return event
}

func (r *userUACWireRecorder) recordFailure(code string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	r.fail(code)
}
