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

func TestDiagnosticCapacityDoesNotRaiseOrResetLimit(t *testing.T) {
	g := &lifecycleGate{witness: &diagnosticWitness{}}
	for i := 0; i < 4096; i++ {
		ticket, err := g.acquire(context.Background())
		if err != nil {
			t.Fatal("capacity changed")
		}
		attachTestTicket(ticket)
		ticket.closed(true)
	}
	if _, err := g.acquire(context.Background()); err != errAdmissionClosed {
		t.Fatal("limit no longer fails closed")
	}
	d := g.applicationDiagnostic(1)
	if d.Reason != invalidCapacity || d.Total != 4096 || d.Retained != 4096 || d.Open != 0 || d.Pending != 0 || d.Phase != "open" {
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
