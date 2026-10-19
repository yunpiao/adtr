//go:build adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"testing"
)

func captureEndFixture(t *testing.T) (*lifecycleGate, *lifecycleTicket, func(), *os.File, []byte) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal("owned pipe")
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	fd, err := prepareCaptureEnd(reader)
	if err != nil {
		t.Fatal("owned nonblocking pipe")
	}
	g := &lifecycleGate{captureEndFD: fd, witness: &diagnosticWitness{}, next: 1}
	for i := range g.nonce {
		g.nonce[i] = 7
	}
	ticket, _ := g.acquire(context.Background())
	conn := attachTestTicket(ticket)
	revoke, err := g.register(conn, testSealIdentity())
	if err != nil || g.pin(1, ticket.registration.generation, nil) != nil {
		t.Fatal("sealed target")
	}
	frame := make([]byte, 48)
	copy(frame[:8], "ADTREND1")
	copy(frame[8:24], g.nonce[:])
	binary.BigEndian.PutUint64(frame[24:32], 1)
	binary.BigEndian.PutUint64(frame[32:40], ticket.generation)
	binary.BigEndian.PutUint64(frame[40:48], ticket.registration.generation)
	return g, ticket, revoke, writer, frame
}

func TestCaptureEndOnlyEndsLivenessAndNeverOpensAdmission(t *testing.T) {
	for _, action := range []string{"revoke", "closing", "closed", "waiter"} {
		t.Run(action, func(t *testing.T) {
			g, ticket, revoke, writer, frame := captureEndFixture(t)
			if n, err := writer.Write(frame); err != nil || n != 48 {
				t.Fatal("authorization write")
			}
			switch action {
			case "revoke":
				revoke()
			case "closing":
				ticket.closingConnection()
			case "closed":
				ticket.closed(true)
			case "waiter":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, err := g.acquire(ctx); err != context.Canceled {
					t.Fatal("original cancellation lost")
				}
			}
			if g.invalid || !g.seal.captureEnded || !g.closing || g.seal.released || g.witness.(*diagnosticWitness).stores != 0 {
				t.Fatal("capture end changed admission or sticky")
			}
			// Normal EOF after the one consumed frame does not revoke the saved proof.
			writer.Close()
			g.mu.Lock()
			ended := g.captureObligationsEndedLocked()
			g.mu.Unlock()
			if !ended || g.invalid {
				t.Fatal("consumed authorization was reread")
			}
			if g.release(1) != nil || g.closing {
				t.Fatal("authenticated release no longer opens")
			}
		})
	}
}

func TestCaptureEndRejectsMissingMalformedAndStaleAuthorization(t *testing.T) {
	for _, kind := range []string{"absent", "eof", "short", "extra", "magic", "nonce", "epoch", "ticket", "registration", "invalid", "not_sealed"} {
		t.Run(kind, func(t *testing.T) {
			g, _, revoke, writer, frame := captureEndFixture(t)
			switch kind {
			case "absent":
			case "eof":
				writer.Close()
			case "short":
				writer.Write(frame[:47])
			case "extra":
				writer.Write(append(frame, frame...))
			default:
				switch kind {
				case "magic":
					frame[0] ^= 1
				case "nonce":
					frame[8] ^= 1
				case "epoch":
					frame[31] ^= 1
				case "ticket":
					frame[39] ^= 1
				case "registration":
					frame[47] ^= 1
				case "invalid":
					g.invalidateTransport()
				case "not_sealed":
					g.open++
				}
				writer.Write(frame)
			}
			revoke()
			if !g.invalid || g.seal.captureEnded || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("invalid authorization ended obligation")
			}
			g.release(1)
			if !g.invalid || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("release erased failure")
			}
		})
	}
}

func TestCaptureEndNeverSuppressesHardFailures(t *testing.T) {
	for _, cause := range []string{"transport", "dirty_close", "bad_close_state", "connect_failed", "explicit"} {
		for _, consumeFirst := range []bool{false, true} {
			t.Run(cause+map[bool]string{false: "_before", true: "_after"}[consumeFirst], func(t *testing.T) {
				g, ticket, _, writer, frame := captureEndFixture(t)
				writer.Write(frame)
				if consumeFirst {
					g.mu.Lock()
					ok := g.captureObligationsEndedLocked()
					g.mu.Unlock()
					if !ok {
						t.Fatal("valid authorization")
					}
				}
				switch cause {
				case "transport":
					g.invalidateTransport()
				case "dirty_close":
					ticket.closed(false)
				case "bad_close_state":
					ticket.state = 'c'
					ticket.closingConnection()
				case "connect_failed":
					ticket.failed()
				case "explicit":
					g.invalidate()
				}
				if !g.invalid || g.witness.(*diagnosticWitness).stores == 0 {
					t.Fatal("hard failure suppressed")
				}
				stores := g.witness.(*diagnosticWitness).stores
				g.release(1)
				if !g.invalid || g.witness.(*diagnosticWitness).stores != stores {
					t.Fatal("sticky history erased")
				}
			})
		}
	}
}

func TestCaptureEndCompleteResumedTargetLifecycle(t *testing.T) {
	g, ticket, revoke, writer, frame := captureEndFixture(t)
	if n, err := writer.Write(frame); err != nil || n != 48 {
		t.Fatal("authorization write")
	}
	revoke()
	ticket.closingConnection()
	ticket.closed(true)
	if g.invalid || !g.seal.captureEnded || !g.closing || g.open != 0 || g.seal.released || g.witness.(*diagnosticWitness).stores != 0 {
		t.Fatal("resumed target invalidated capture")
	}
	if g.release(1) != nil || g.closing || g.invalid {
		t.Fatal("release changed validity")
	}
}

func TestCaptureEndCrossLanguageFrameVector(t *testing.T) {
	g, _, revoke, writer, frame := captureEndFixture(t)
	const expected = "41445452454e443107070707070707070707070707070707000000000000000100000000000000020000000000000003"
	if hex.EncodeToString(frame) != expected {
		t.Fatal("fixed authorization vector changed")
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil {
		t.Fatal("vector decode")
	}
	if n, err := writer.Write(decoded); err != nil || n != 48 {
		t.Fatal("vector publication")
	}
	revoke()
	if g.invalid || !g.seal.captureEnded {
		t.Fatal("cross language vector rejected")
	}
}
