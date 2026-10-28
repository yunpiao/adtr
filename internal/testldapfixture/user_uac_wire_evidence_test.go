//go:build integration

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func uacRecorderForTest(t *testing.T) (*userUACWireRecorder, string, uint64) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := newUserUACWireRecorder(userUACMixed33, root, uacTestNonce)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.root.Close() })
	id, err := r.OpenConnection(uacStartTLS)
	if err != nil {
		t.Fatal(err)
	}
	return r, root, id
}
func uacEvent(page int, cookie []byte) uacRequestEvent {
	relation := uacCookieMatches
	if page == 1 {
		relation = uacCookieEmpty
	}
	return uacRequestEvent{kind: uacDirectory, messageID: page + 3, tlsVerified: true, bindSucceeded: true, rootResponseComplete: true, fixedBody: true, pagingValid: true, cookieRelation: relation, requestCookieSHA256: uacSHA(cookie)}
}
func uacDoneFrame(id int, cookie []byte) []byte {
	return element(0x30, integer(2, id), element(0x65, integer(10, 0), octets(""), octets("")), element(0xa0, element(0x30, octets(pagingOID), element(4, element(0x30, integer(2, 51), element(4, cookie))))))
}
func uacRecordPage(t *testing.T, r *userUACWireRecorder, id uint64, page int, cookie []byte) []byte {
	t.Helper()
	if err := r.ObserveRequest(id, uacEvent(page, cookie)); err != nil {
		t.Fatal(err)
	}
	count := 15
	if page == 4 {
		count = 6
	}
	for i := 0; i < count; i++ {
		ordinal := (page-1)*15 + i
		frame := userUACV3Entry(page+3, ordinal+1)
		ticket, err := r.BeginWrite(id, page, ordinal, uacEntry, len(frame))
		if err != nil {
			t.Fatal(err)
		}
		if err = r.FinishWrite(ticket, frame, true); err != nil {
			t.Fatal(err)
		}
	}
	next := bytes.Repeat([]byte{byte(page)}, 20)
	if page == 4 {
		next = nil
	}
	frame := uacDoneFrame(page+3, next)
	ticket, err := r.BeginWrite(id, page, -1, uacDone, len(frame))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.FinishWrite(ticket, frame, true); err != nil {
		t.Fatal(err)
	}
	return next
}
func uacComplete(t *testing.T, r *userUACWireRecorder, id uint64) {
	t.Helper()
	var cookie []byte
	for page := 1; page <= 4; page++ {
		cookie = uacRecordPage(t, r, id, page, cookie)
	}
}
func uacNoSeal(t *testing.T, r *userUACWireRecorder, root string, id uint64) {
	t.Helper()
	r.CloseConnection(id, uacCleanEOF)
	r.BeginDrain()
	if r.SealAfterJoin() == nil {
		t.Fatal("poisoned/incomplete transcript sealed")
	}
	if _, err := os.Stat(filepath.Join(root, "terminal-seal.json")); !os.IsNotExist(err) {
		t.Fatal("success seal exists")
	}
}

func TestUACRecorderCandidateThenExactTerminal(t *testing.T) {
	r, root, id := uacRecorderForTest(t)
	uacComplete(t, r, id)
	if _, err := os.Stat(filepath.Join(root, "terminal-seal.json")); !os.IsNotExist(err) {
		t.Fatal("early terminal")
	}
	candidate, _ := os.ReadFile(filepath.Join(root, "candidate-wire.json"))
	var value uacCandidateEvidence
	if json.Unmarshal(candidate, &value) != nil {
		t.Fatal("candidate JSON")
	}
	if len(value.Pages) != 4 || value.EntryCount != 51 || value.CompletionSequence == 0 {
		t.Fatal("candidate counts")
	}
	for page, p := range value.Pages {
		if p.PageIdx != page+1 || p.RequestMessageID != page+4 || p.Done == nil || p.RequestCookieSHA256 != p.Done.RequestCookieSHA256 {
			t.Fatal("page binding")
		}
		frame, err := base64.StdEncoding.DecodeString(p.Done.RedactedFrameBase64)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(frame[p.Done.CookieValueOffset:], make([]byte, p.Done.CookieValueLength)) {
			t.Fatal("cookie retained")
		}
		if page == 3 && p.Done.OriginalFrameSHA256 != p.Done.RedactedFrameSHA256 {
			t.Fatal("empty cookie must preserve frame")
		}
		if page > 0 && p.RequestCookieSHA256 != value.Pages[page-1].Done.CookieSHA256 {
			t.Fatal("continuation link")
		}
	}
	r.CloseConnection(id, uacCleanEOF)
	r.BeginDrain()
	if err := r.SealAfterJoin(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "terminal-seal.json"))
	var seal uacSealEvidence
	if json.Unmarshal(raw, &seal) != nil {
		t.Fatal("seal JSON")
	}
	if seal.CandidateTranscriptSHA256 != uacSHA(candidate) || seal.CompletionSequence != value.CompletionSequence || seal.FinalSequence < seal.CompletionSequence || !seal.ParticipantClosed || seal.ActiveHandlers != 0 || seal.InflightWrites != 0 || seal.TerminalState != "sealed_ok" {
		t.Fatal("terminal bindings")
	}
	if r.SealAfterJoin() == nil {
		t.Fatal("seal repeated")
	}
	for _, name := range []string{"candidate-wire.json", "terminal-seal.json"} {
		info, _ := os.Stat(filepath.Join(root, name))
		if info.Mode().Perm() != 0600 || info.Sys() == nil {
			t.Fatal("private file")
		}
	}
}

func TestUACRecorderEveryInvalidAttemptPoisonsPermanently(t *testing.T) {
	for _, mutation := range []string{"tls", "bind", "root", "body", "control", "cookie", "id", "unknown_hash"} {
		t.Run(mutation, func(t *testing.T) {
			r, root, id := uacRecorderForTest(t)
			event := uacEvent(1, nil)
			switch mutation {
			case "tls":
				event.tlsVerified = false
			case "bind":
				event.bindSucceeded = false
			case "root":
				event.rootResponseComplete = false
			case "body":
				event.fixedBody = false
			case "control":
				event.pagingValid = false
			case "cookie":
				event.cookieRelation = uacCookieMismatch
			case "id":
				event.messageID = 5
			case "unknown_hash":
				event.requestCookieSHA256 = "invalid"
			}
			if r.ObserveRequest(id, event) == nil {
				t.Fatal("invalid attempt accepted")
			}
			first := r.poison
			if r.ObserveRequest(id, uacEvent(1, nil)) == nil || r.poison != first {
				t.Fatal("later valid request cleared poison")
			}
			uacNoSeal(t, r, root, id)
		})
	}
}
func TestUACRecorderPartialWritesAndOneUseInstanceTickets(t *testing.T) {
	for _, mutation := range []string{"partial", "wrong_length", "reused", "foreign", "bad_id", "oversize", "wrong_ordinal", "done_early"} {
		t.Run(mutation, func(t *testing.T) {
			r, root, id := uacRecorderForTest(t)
			if r.ObserveRequest(id, uacEvent(1, nil)) != nil {
				t.Fatal("claim")
			}
			frame := userUACV3Entry(4, 1)
			switch mutation {
			case "oversize":
				if _, err := r.BeginWrite(id, 1, 0, uacEntry, 2049); err == nil {
					t.Fatal("oversize")
				}
			case "wrong_ordinal":
				if _, err := r.BeginWrite(id, 1, 1, uacEntry, len(frame)); err == nil {
					t.Fatal("wrong ordinal")
				}
			case "done_early":
				if _, err := r.BeginWrite(id, 1, -1, uacDone, 50); err == nil {
					t.Fatal("early done")
				}
			default:
				ticket, err := r.BeginWrite(id, 1, 0, uacEntry, len(frame))
				if err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "partial":
					err = r.FinishWrite(ticket, frame, false)
				case "wrong_length":
					err = r.FinishWrite(ticket, frame[:len(frame)-1], true)
				case "reused":
					if r.FinishWrite(ticket, frame, true) != nil {
						t.Fatal("first ticket")
					}
					err = r.FinishWrite(ticket, frame, true)
				case "foreign":
					other, _, _ := uacRecorderForTest(t)
					ticket.recorder = other
					err = r.FinishWrite(ticket, frame, true)
				case "bad_id":
					err = r.FinishWrite(ticket, userUACV3Entry(5, 1), true)
				}
				if err == nil {
					t.Fatal("write mutation accepted")
				}
			}
			uacNoSeal(t, r, root, id)
		})
	}
}
func TestUACRecorderPostCompletionAndParticipantNonContinuation(t *testing.T) {
	for _, kind := range []uacRequestKind{uacBind, uacRootDSE, uacStartTLSRequest, uacOther, uacDirectory} {
		t.Run(string(kind), func(t *testing.T) {
			r, root, id := uacRecorderForTest(t)
			uacComplete(t, r, id)
			candidate, _ := os.ReadFile(filepath.Join(root, "candidate-wire.json"))
			event := uacEvent(1, nil)
			event.kind = kind
			if r.ObserveRequest(id, event) == nil {
				t.Fatal("late request accepted")
			}
			uacNoSeal(t, r, root, id)
			after, _ := os.ReadFile(filepath.Join(root, "candidate-wire.json"))
			if !bytes.Equal(candidate, after) {
				t.Fatal("early candidate overwritten")
			}
		})
	}
}
func TestUACRecorderConcurrentClaimIsStickyAndNotBlockedByWrite(t *testing.T) {
	r, root, id := uacRecorderForTest(t)
	second, _ := r.OpenConnection(uacLDAPS)
	if r.ObserveRequest(id, uacEvent(1, nil)) != nil {
		t.Fatal("first claim")
	}
	frame := userUACV3Entry(4, 1)
	ticket, err := r.BeginWrite(id, 1, 0, uacEntry, len(frame))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if r.ObserveRequest(second, uacEvent(1, nil)) == nil {
			t.Error("second claim accepted")
		}
	}()
	wg.Wait()
	if r.FinishWrite(ticket, frame, true) == nil {
		t.Fatal("concurrent poison ignored")
	}
	r.CloseConnection(second, uacCleanEOF)
	uacNoSeal(t, r, root, id)
}
func TestUACRecorderReadFailuresAndRootOnlyExceptions(t *testing.T) {
	for _, reason := range []uacCloseReason{uacIncompleteRead, uacProtocolFailure, uacTransportFailure} {
		t.Run(string(reason), func(t *testing.T) {
			r, root, id := uacRecorderForTest(t)
			uacComplete(t, r, id)
			other, _ := r.OpenConnection(uacLDAPS)
			r.rootResponseCompleted(other)
			for _, ready := range []bool{true, false} {
				if err := r.ObserveRequest(other, uacRequestEvent{kind: uacBind, rootResponseComplete: ready}); err != nil {
					t.Fatal(err)
				}
			}
			r.CloseConnection(other, reason)
			uacNoSeal(t, r, root, id)
		})
	}
	for _, drain := range []bool{false, true} {
		t.Run(fmt.Sprintf("drain=%t", drain), func(t *testing.T) {
			r, _, id := uacRecorderForTest(t)
			uacComplete(t, r, id)
			other, _ := r.OpenConnection(uacLDAPS)
			r.rootResponseCompleted(other)
			if err := r.ObserveRequest(other, uacRequestEvent{kind: uacBind}); err != nil {
				t.Fatal(err)
			}
			reason := uacCleanEOF
			if drain {
				r.BeginDrain()
				reason = uacOwnedDrain
			}
			r.CloseConnection(other, reason)
			r.CloseConnection(id, reason)
			if !drain {
				r.BeginDrain()
			}
			if r.SealAfterJoin() != nil {
				t.Fatal("legal root-only/drain rejected")
			}
		})
	}
}
func TestUACRecorderDoneRedactionIsOnlyCookiePayload(t *testing.T) {
	cookie := bytes.Repeat([]byte{0xa5}, 20)
	frame := uacDoneFrame(4, cookie)
	done, err := uacRedactDone(frame, 4, 1, uacSHA(nil))
	if err != nil {
		t.Fatal(err)
	}
	redacted, _ := base64.StdEncoding.DecodeString(done.RedactedFrameBase64)
	if !bytes.Equal(frame[:done.CookieValueOffset], redacted[:done.CookieValueOffset]) || done.CookieValueLength != 20 || !bytes.Equal(frame[done.CookieValueOffset:], cookie) || done.CookieSHA256 != uacSHA(cookie) {
		t.Fatal("redaction changed non-cookie bytes")
	}
	for _, bad := range [][]byte{append(append([]byte{}, frame...), 0), uacDoneFrame(5, cookie), uacDoneFrame(4, nil), uacDoneFrame(4, bytes.Repeat([]byte{1}, 19)), message(4, 0x65, integer(10, 53), octets(""), octets(""))} {
		if _, err := uacRedactDone(bad, 4, 1, uacSHA(nil)); err == nil {
			t.Fatal("invalid Done accepted")
		}
	}
}
func TestUACRecorderPublicationRefusesExistingAndPoisonsIO(t *testing.T) {
	for _, name := range []string{"candidate-wire.json", "terminal-seal.json"} {
		t.Run(name, func(t *testing.T) {
			r, root, id := uacRecorderForTest(t)
			if name == "terminal-seal.json" {
				uacComplete(t, r, id)
			}
			old := []byte("owned preexisting must survive")
			if os.WriteFile(filepath.Join(root, name), old, 0600) != nil {
				t.Fatal("setup")
			}
			if name == "candidate-wire.json" {
				var cookie []byte
				for page := 1; page <= 3; page++ {
					cookie = uacRecordPage(t, r, id, page, cookie)
				}
				if r.ObserveRequest(id, uacEvent(4, cookie)) != nil {
					t.Fatal("page4")
				}
				for i := 45; i < 51; i++ {
					f := userUACV3Entry(7, i+1)
					ticket, _ := r.BeginWrite(id, 4, i, uacEntry, len(f))
					if r.FinishWrite(ticket, f, true) != nil {
						t.Fatal("entry")
					}
				}
				f := uacDoneFrame(7, nil)
				ticket, _ := r.BeginWrite(id, 4, -1, uacDone, len(f))
				if r.FinishWrite(ticket, f, true) == nil {
					t.Fatal("overwritten candidate")
				}
			}
			r.CloseConnection(id, uacCleanEOF)
			r.BeginDrain()
			if r.SealAfterJoin() == nil {
				t.Fatal("existing publication accepted")
			}
			after, _ := os.ReadFile(filepath.Join(root, name))
			if !bytes.Equal(after, old) {
				t.Fatal("old file changed")
			}
		})
	}
}

func TestUACRecorderConstructorRejectsUnsafeDirectoriesAndExistingPaths(t *testing.T) {
	for _, scenario := range []string{"mode", "symlink", "missing", "candidate", "seal", "temporary", "nonce", "none_with_path"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			os.Chmod(root, 0700)
			path := root
			profile := userUACMixed33
			nonce := uacTestNonce
			switch scenario {
			case "mode":
				os.Chmod(root, 0755)
			case "symlink":
				path = filepath.Join(t.TempDir(), "link")
				os.Symlink(root, path)
			case "missing":
				path = filepath.Join(root, "missing")
			case "candidate":
				os.WriteFile(filepath.Join(root, "candidate-wire.json"), []byte("old"), 0600)
			case "seal":
				os.Mkdir(filepath.Join(root, "terminal-seal.json"), 0700)
			case "temporary":
				os.Symlink("absent", filepath.Join(root, ".candidate-wire.json.tmp"))
			case "nonce":
				nonce = "not-a-nonce"
			case "none_with_path":
				profile = userUACNone
				nonce = ""
			}
			if r, err := newUserUACWireRecorder(profile, path, nonce); err == nil || r != nil {
				t.Fatal("unsafe recorder configuration accepted")
			}
		})
	}
}
func TestUACRecorderIncompleteInflightAndUnjoinedCannotSeal(t *testing.T) {
	for _, scenario := range []string{"no_claim", "partial_page", "missing_page", "active_handler", "inflight", "no_drain"} {
		t.Run(scenario, func(t *testing.T) {
			r, root, id := uacRecorderForTest(t)
			switch scenario {
			case "no_claim":
			case "partial_page":
				r.ObserveRequest(id, uacEvent(1, nil))
			case "missing_page":
				uacRecordPage(t, r, id, 1, nil)
			case "active_handler", "no_drain":
				uacComplete(t, r, id)
			case "inflight":
				r.ObserveRequest(id, uacEvent(1, nil))
				frame := userUACV3Entry(4, 1)
				r.BeginWrite(id, 1, 0, uacEntry, len(frame))
			}
			if scenario != "active_handler" {
				r.CloseConnection(id, uacCleanEOF)
			}
			if scenario != "no_drain" {
				r.BeginDrain()
			}
			if r.SealAfterJoin() == nil {
				t.Fatal("incomplete or unjoined instance sealed")
			}
			if _, err := os.Stat(filepath.Join(root, "terminal-seal.json")); !os.IsNotExist(err) {
				t.Fatal("terminal exists")
			}
		})
	}
}
func TestUACRecorderPartialAndMalformedDoneWrites(t *testing.T) {
	for _, scenario := range []string{"partial", "wrong_estimate", "extra_control", "explicit_critical", "early_terminal", "oversize", "entry_after_done"} {
		t.Run(scenario, func(t *testing.T) {
			r, root, id := uacRecorderForTest(t)
			r.ObserveRequest(id, uacEvent(1, nil))
			for i := 0; i < 15; i++ {
				frame := userUACV3Entry(4, i+1)
				ticket, err := r.BeginWrite(id, 1, i, uacEntry, len(frame))
				if err != nil || r.FinishWrite(ticket, frame, true) != nil {
					t.Fatal("entry setup")
				}
			}
			cookie := bytes.Repeat([]byte{1}, 20)
			frame := uacDoneFrame(4, cookie)
			switch scenario {
			case "wrong_estimate":
				frame = bytes.Replace(frame, []byte{2, 1, 51}, []byte{2, 1, 50}, 1)
			case "extra_control":
				control := element(0x30, octets(pagingOID), element(4, element(0x30, integer(2, 51), element(4, cookie))))
				frame = element(0x30, integer(2, 4), element(0x65, integer(10, 0), octets(""), octets("")), element(0xa0, control, control))
			case "explicit_critical":
				frame = element(0x30, integer(2, 4), element(0x65, integer(10, 0), octets(""), octets("")), element(0xa0, element(0x30, octets(pagingOID), element(1, []byte{0}), element(4, element(0x30, integer(2, 51), element(4, cookie))))))
			case "early_terminal":
				frame = uacDoneFrame(4, nil)
			case "oversize":
				frame = make([]byte, 4097)
			}
			ticket, err := r.BeginWrite(id, 1, -1, uacDone, len(frame))
			if err == nil {
				err = r.FinishWrite(ticket, frame, scenario != "partial")
			}
			if scenario == "entry_after_done" {
				if err != nil {
					t.Fatal("done setup")
				}
				_, err = r.BeginWrite(id, 1, 15, uacEntry, 20)
			}
			if err == nil {
				t.Fatal("bad Done accepted")
			}
			uacNoSeal(t, r, root, id)
		})
	}
}
