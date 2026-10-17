//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type diagnosticWitness struct {
	stores int
	before func()
}

func (w *diagnosticWitness) invalidate() {
	if w.before != nil {
		w.before()
	}
	w.stores++
}

func TestDiagnosticFirstCauseStickyOrderAndClosedOutput(t *testing.T) {
	reasons := []invalidationReason{invalidCapacity, invalidConnectFailed, invalidConnectState,
		invalidDuplicateBackend, invalidCloseState, invalidUncleanClose, invalidTargetClosed,
		invalidTargetClosing, invalidTargetRevoked, invalidWaitCancelled, invalidTransport,
		invalidRecordCapacity, invalidAbort, invalidExplicit}
	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			g, _ := newSealProtocolTestGate()
			witness := &diagnosticWitness{before: func() {
				if g.invalid || g.firstInvalidReason != "" {
					t.Fatal("diagnostic mutation preceded sticky store")
				}
			}}
			g.witness = witness
			g.mu.Lock()
			g.invalidateBecauseLocked(reason)
			g.mu.Unlock()
			witness.before = nil
			g.invalidateTransport()
			proof := g.applicationDiagnostic(4)
			if witness.stores != 2 || !g.invalid || proof.Reason != reason || proof.Phase != "open" {
				t.Fatal("first cause or sticky state lost")
			}
			raw, err := json.Marshal(proof)
			if err != nil || strings.Contains(string(raw), "private") {
				t.Fatal("private identity leaked")
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || len(fields) != 10 {
				t.Fatal("diagnostic schema changed")
			}
			for _, name := range []string{"version", "epoch", "seq", "kind", "reason", "phase", "pending", "open", "total", "retained"} {
				if _, ok := fields[name]; !ok {
					t.Fatal("closed field missing")
				}
			}
		})
	}
}

func TestDiagnosticRetainedCapacityDoesNotRaiseOrResetLimit(t *testing.T) {
	g := &lifecycleGate{witness: &diagnosticWitness{}}
	for i := 0; i < 4096; i++ {
		ticket, err := g.acquire(context.Background())
		if err != nil {
			t.Fatal("capacity changed")
		}
		_ = ticket // Pending startup retains its place in the bounded registry.
	}
	if _, err := g.acquire(context.Background()); err != errAdmissionClosed {
		t.Fatal("limit no longer fails closed")
	}
	d := g.applicationDiagnostic(1)
	if d.Reason != invalidCapacity || d.Total != 4096 || d.Retained != 4096 || d.Open != 0 || d.Pending != 4096 || d.Phase != "open" {
		t.Fatal("capacity evidence unavailable")
	}
	if _, err := g.acquire(context.Background()); err != errAdmissionClosed {
		t.Fatal("diagnostic reopened gate")
	}
}

func TestDiagnosticActualPrePinFailureBranches(t *testing.T) {
	for _, failure := range []string{"connect", "close", "transport", "record_limit"} {
		t.Run(failure, func(t *testing.T) {
			g := &lifecycleGate{}
			want := invalidConnectFailed
			switch failure {
			case "connect":
				ticket, _ := g.acquire(context.Background())
				ticket.failed()
			case "close":
				ticket, _ := g.acquire(context.Background())
				attachTestTicket(ticket)
				ticket.closed(false)
				want = invalidUncleanClose
			case "transport":
				g.invalidateTransport()
				want = invalidTransport
			case "record_limit":
				for i := 0; i < 129; i++ {
					g.acquire(context.Background())
				}
				g.applicationSnapshot(1, "reply")
				want = invalidRecordCapacity
			}
			if g.applicationDiagnostic(1).Reason != want {
				t.Fatal("wrong branch category")
			}
			if _, err := g.acquire(context.Background()); err != errAdmissionClosed {
				t.Fatal("diagnostic changed refusal")
			}
		})
	}
}

func TestDiagnosticPhaseAndUnknownInvalidAreNeverNone(t *testing.T) {
	for _, phase := range []string{"open", "draining", "sealed", "released"} {
		g, target := newSealProtocolTestGate()
		if phase == "draining" {
			g.acquire(context.Background())
		}
		if phase != "open" {
			if g.pin(1, target, nil) != nil {
				t.Fatal("pin")
			}
		}
		if phase == "released" {
			g.release(1)
		}
		g.invalidateTransport()
		d := g.applicationDiagnostic(1)
		if d.Phase != phase || d.Reason != invalidTransport {
			t.Fatal("first phase lost")
		}
	}
	g := &lifecycleGate{}
	if g.applicationDiagnostic(1).Reason != "none" {
		t.Fatal("fresh state")
	}
	g.mu.Lock()
	g.markInvalidLocked()
	g.mu.Unlock()
	if g.applicationDiagnostic(2).Reason != invalidExplicit {
		t.Fatal("unknown invalid mislabeled valid")
	}
}

func TestFinalInvalidationProjectionRejectsUnknownData(t *testing.T) {
	g := &lifecycleGate{}
	d := g.finalInvalidationDiagnostic()
	if d.Event != "owned_ci_first_invalidation" || d.Reason != "none" || d.Phase != "open" {
		t.Fatal("fresh diagnostic changed")
	}
	g.firstInvalidReason = invalidationReason("private SQL nonce identity")
	g.firstInvalidPhase = "private phase"
	raw, err := json.Marshal(g.finalInvalidationDiagnostic())
	if err != nil || string(raw) != `{"event":"owned_ci_first_invalidation","reason":"unknown","phase":"unknown"}` {
		t.Fatal("unknown diagnostic leaked")
	}
}

func TestResumeReleaseOrderingPreservesFirstInvalidation(t *testing.T) {
	for _, releaseFirst := range []bool{false, true} {
		witness := &diagnosticWitness{}
		g := &lifecycleGate{witness: witness}
		ticket, err := g.acquire(context.Background())
		if err != nil {
			t.Fatal("fixture admission failed")
		}
		conn := attachTestTicket(ticket)
		revoke, err := g.register(conn, testSealIdentity())
		if err != nil {
			t.Fatal("fixture registration failed")
		}
		if g.pin(1, g.candidates()[0].generation, nil) != nil || g.frame(1).Status != "sealed" {
			t.Fatal("fixture not sealed")
		}
		if releaseFirst && g.release(1) != nil {
			t.Fatal("release failed")
		}
		// Deterministic resumed business completion before/after controller release.
		revoke()
		ticket.closingConnection()
		ticket.closed(true)
		storesBeforeRelease := witness.stores
		if g.release(1) != nil {
			t.Fatal("release acknowledgement failed")
		}
		if witness.stores != storesBeforeRelease || (releaseFirst && witness.stores != 0) || (!releaseFirst && witness.stores == 0) {
			t.Fatal("sticky write history changed")
		}
		d := g.finalInvalidationDiagnostic()
		if releaseFirst {
			if g.invalid || d.Reason != "none" || d.Phase != "released" {
				t.Fatal("release-first became invalid")
			}
		} else if !g.invalid || d.Reason != "target_revoked" || d.Phase != "sealed" || g.frame(1).Status != "released" {
			t.Fatal("release acknowledgement erased earlier invalidation")
		}
	}
}

func TestCancelledWaiterReleaseOrderingPreservesFirstInvalidation(t *testing.T) {
	for _, releaseFirst := range []bool{false, true} {
		g, generation := newSealProtocolTestGate()
		witness := &diagnosticWitness{}
		g.witness = witness
		if g.pin(1, generation, nil) != nil {
			t.Fatal("fixture pin failed")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if releaseFirst && g.release(1) != nil {
			t.Fatal("fixture release failed")
		}
		if _, err := g.acquire(ctx); err != context.Canceled {
			t.Fatal("wait cancellation changed")
		}
		storesBeforeRelease := witness.stores
		if g.release(1) != nil {
			t.Fatal("release acknowledgement failed")
		}
		if witness.stores != storesBeforeRelease || (releaseFirst && witness.stores != 0) || (!releaseFirst && witness.stores == 0) {
			t.Fatal("sticky write history changed")
		}
		d := g.finalInvalidationDiagnostic()
		if releaseFirst {
			if g.invalid || d.Reason != "none" {
				t.Fatal("released waiter invalidated old epoch")
			}
		} else if !g.invalid || d.Reason != "wait_cancelled" || d.Phase != "sealed" {
			t.Fatal("waiter cancellation lost first cause")
		}
	}
}
