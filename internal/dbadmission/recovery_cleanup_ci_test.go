//go:build adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func recoveryCancellationFacts() *operationErrorDiagnostic {
	return &operationErrorDiagnostic{
		Operation: "statement", StartPhase: "released", EndPhase: "released",
		ContextState: "canceled", ErrorCategory: "canceled", ErrorMatchesContext: true,
		NativeClosedAtEnd: true, RootContextState: "canceled", ConnectionMatched: true,
	}
}

// This fixture supplies synthetic operation observations only. Real cancellation,
// physical native cleanup and independent PostgreSQL proof are integration-only.
func recoveryQualificationFixture(t *testing.T) (*lifecycleGate, *lifecycleTicket) {
	t.Helper()
	g, target, _, writer, frame := captureEndFixture(t)
	if n, err := writer.Write(frame); err != nil || n != len(frame) {
		t.Fatal("capture end publication failed")
	}
	if err := g.release(1); err != nil || !g.seal.recoveryAuthorized || g.invalid {
		t.Fatal("exact capture end did not authorize release")
	}
	target.closed(true)
	ticket, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("released admission failed")
	}
	attachTestTicket(ticket)
	root, cancel := context.WithCancel(context.Background())
	cancel()
	g.workerContext = root
	first := recoveryCancellationFacts()
	ticket.firstOperationError, ticket.firstNativeClosedError = first, first
	return g, ticket
}

func TestRecoveryReleaseRequiresExactAuthorizationAndNewBirth(t *testing.T) {
	for _, kind := range []string{"exact", "absent", "old_epoch", "wrong_ticket", "wrong_registration", "existing_sticky"} {
		t.Run(kind, func(t *testing.T) {
			g, before, _, writer, frame := captureEndFixture(t)
			switch kind {
			case "old_epoch":
				binary.BigEndian.PutUint64(frame[24:32], 0)
			case "wrong_ticket":
				binary.BigEndian.PutUint64(frame[32:40], before.generation+1)
			case "wrong_registration":
				binary.BigEndian.PutUint64(frame[40:48], before.registration.generation+1)
			case "existing_sticky":
				g.invalidateTransport()
			}
			if kind != "absent" {
				if n, err := writer.Write(frame); err != nil || n != len(frame) {
					t.Fatal("capture end publication failed")
				}
			}
			if err := g.release(1); err != nil || !g.seal.released || g.closing {
				t.Fatal("rescue release failed to reopen admission")
			}
			wantAuthorized := kind == "exact"
			if g.seal.recoveryAuthorized != wantAuthorized {
				t.Fatal("release authorization did not match exact frame")
			}
			if before.recoveryAuthorized || before.recoveryEpoch != 0 {
				t.Fatal("pre-release ticket acquired retroactive qualification")
			}
			after, err := g.acquire(context.Background())
			if err != nil || after.recoveryAuthorized != wantAuthorized {
				t.Fatal("new birth qualification did not match release authorization")
			}
			if (wantAuthorized && after.recoveryEpoch != 1) || (!wantAuthorized && after.recoveryEpoch != 0) {
				t.Fatal("birth copied an unqualified epoch")
			}
			if kind != "exact" && kind != "absent" && (!g.invalid || g.witness.(*diagnosticWitness).stores == 0) {
				t.Fatal("bad authorization or existing sticky history was lost")
			}
		})
	}
}

func TestRecoveryRepeatedReleaseCannotUpgradeAuthorization(t *testing.T) {
	g, target, _, writer, frame := captureEndFixture(t)
	if g.release(1) != nil || g.seal.recoveryAuthorized || g.invalid {
		t.Fatal("absent authorization did not remain rescue-only")
	}
	if n, err := writer.Write(frame); err != nil || n != len(frame) {
		t.Fatal("late frame publication failed")
	}
	if g.release(1) != nil || g.seal.recoveryAuthorized || g.seal.captureEnded || g.invalid {
		t.Fatal("repeated release upgraded or reread late authorization")
	}
	after, err := g.acquire(context.Background())
	if err != nil || target.recoveryAuthorized || after.recoveryAuthorized || after.recoveryEpoch != 0 {
		t.Fatal("late authorization granted a recovery birth")
	}
}

func TestRecoveryQualificationRejectsIncompleteOrDifferentEvidence(t *testing.T) {
	cases := []struct {
		name string
		edit func(*lifecycleGate, *lifecycleTicket, *closeObservation)
	}{
		{"before_release_birth", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.birthPhase, ticket.birthCaptureEnded = "open", false
			ticket.recoveryAuthorized, ticket.recoveryEpoch = false, 0
		}},
		{"capture_observation_without_authority", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.birthPhase, ticket.birthCaptureEnded = "released", true
			ticket.recoveryAuthorized = false
		}},
		{"target", func(g *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) { g.seal.target = ticket }},
		{"registration", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.registration = &activityRegistration{}
		}},
		{"missing_authorization", func(g *lifecycleGate, _ *lifecycleTicket, _ *closeObservation) { g.seal.recoveryAuthorized = false }},
		{"old_epoch", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) { ticket.recoveryEpoch-- }},
		{"unreleased", func(g *lifecycleGate, _ *lifecycleTicket, _ *closeObservation) { g.seal.released = false }},
		{"missing_first_error", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) { ticket.firstOperationError = nil }},
		{"missing_native_error", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstNativeClosedError = nil
		}},
		{"equal_fields_different_observation", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			copy := *ticket.firstOperationError
			ticket.firstNativeClosedError = &copy
		}},
		{"first_network_then_cancellation", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError = &operationErrorDiagnostic{Operation: "statement", ErrorCategory: "other"}
		}},
		{"begin", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.Operation = "begin"
		}},
		{"commit", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.Operation = "commit"
		}},
		{"other_error", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.ErrorCategory = "other"
		}},
		{"deadline", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.ContextState, ticket.firstOperationError.ErrorCategory = "deadline_exceeded", "deadline_exceeded"
		}},
		{"original_context_active", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.ContextState = "active"
		}},
		{"root_active_at_error", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.RootContextState = "active"
		}},
		{"root_active_at_close", func(g *lifecycleGate, _ *lifecycleTicket, _ *closeObservation) {
			g.workerContext = context.Background()
		}},
		{"root_unknown", func(g *lifecycleGate, _ *lifecycleTicket, _ *closeObservation) { g.workerContext = nil }},
		{"error_context_mismatch", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.ErrorMatchesContext = false
		}},
		{"native_open_at_first_error", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.NativeClosedAtEnd = false
		}},
		{"foreign_connection", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.ConnectionMatched = false
		}},
		{"before_release_start", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.StartPhase = "sealed"
		}},
		{"before_release_end", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.firstOperationError.EndPhase = "sealed"
		}},
		{"overlap", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) { ticket.operationInFlight = 1 }},
		{"contamination", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) {
			ticket.recoveryContaminated = true
		}},
		{"missing_connection", func(_ *lifecycleGate, ticket *lifecycleTicket, _ *closeObservation) { ticket.conn = nil }},
		{"close_error", func(_ *lifecycleGate, _ *lifecycleTicket, observation *closeObservation) {
			observation.closeFailed = true
		}},
		{"not_already_closed", func(_ *lifecycleGate, _ *lifecycleTicket, observation *closeObservation) {
			observation.alreadyClosed = false
		}},
		{"counter_overflow", func(g *lifecycleGate, _ *lifecycleTicket, _ *closeObservation) { g.recoveryQualified = ^uint64(0) }},
		{"finalized", func(g *lifecycleGate, _ *lifecycleTicket, _ *closeObservation) { g.recoveryFinalized = true }},
		{"sticky", func(g *lifecycleGate, _ *lifecycleTicket, _ *closeObservation) { g.invalidateTransport() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, ticket := recoveryQualificationFixture(t)
			observation := closeObservation{alreadyClosed: true}
			g.mu.Lock()
			eligible := ticket.canAwaitRecoveryLocked(observation)
			g.mu.Unlock()
			if !eligible {
				t.Fatal("complete synthetic qualification fixture rejected")
			}
			tc.edit(g, ticket, &observation)
			g.mu.Lock()
			eligible = ticket.canAwaitRecoveryLocked(observation)
			g.mu.Unlock()
			if eligible {
				t.Fatal("incomplete or different observation qualified")
			}
			qualified := g.recoveryQualified
			ticket.closedObserved(observation)
			if !g.invalid || ticket.state != 'c' || g.open != 0 || g.recoveryQualified != qualified || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("ineligible close escaped original fatal accounting")
			}
		})
	}
}

func TestRecoveryLaterOperationsPermanentlyContaminate(t *testing.T) {
	for _, sql := range []string{"SELECT 1", "begin", "commit", "rollback"} {
		t.Run(sql, func(t *testing.T) {
			g, ticket := recoveryQualificationFixture(t)
			first := ticket.firstOperationError
			o := &operationObserver{ticket: ticket}
			traced := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{SQL: sql})
			o.TraceQueryEnd(traced, nil, pgx.TraceQueryEndData{})
			if !ticket.recoveryContaminated || ticket.firstOperationError != first || ticket.firstNativeClosedError != first || ticket.operationInFlight != 0 {
				t.Fatal("later business query or non-closed rollback erased first-error history")
			}
			ticket.closedObserved(closeObservation{alreadyClosed: true})
			if !g.invalid || g.recoveryQualified != 0 {
				t.Fatal("contaminated lifetime entered recovery")
			}
		})
	}
}

func TestRecoveryRevokedRegistrationNeverBecomesOrdinary(t *testing.T) {
	g, ticket := recoveryQualificationFixture(t)
	revoke, err := g.register(ticket.conn, testSealIdentity())
	if err != nil || !ticket.recoveryEverRegistered || ticket.registration == nil {
		t.Fatal("post-release registration fixture failed")
	}
	revoke()
	if ticket.registration != nil || !ticket.recoveryEverRegistered {
		t.Fatal("revocation erased registration history")
	}
	g.mu.Lock()
	eligible := ticket.canAwaitRecoveryLocked(closeObservation{alreadyClosed: true})
	g.mu.Unlock()
	if eligible {
		t.Fatal("previously registered lifetime qualified as ordinary")
	}
	ticket.closedObserved(closeObservation{alreadyClosed: true, cleanupDone: true})
	if !g.invalid || ticket.state != 'c' || g.recoveryQualified != 0 {
		t.Fatal("revoked registration escaped original fatal close path")
	}
}

func TestRecoveryTraceOverlapMissingTraceAndOverflowFailClosed(t *testing.T) {
	for _, kind := range []string{"overlap", "missing_trace", "foreign_trace", "missing_connection", "sequence_overflow"} {
		t.Run(kind, func(t *testing.T) {
			g, ticket := recoveryQualificationFixture(t)
			ticket.firstOperationError, ticket.firstNativeClosedError = nil, nil
			o := &operationObserver{ticket: ticket}
			switch kind {
			case "overlap":
				first := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
				second := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{SQL: "SELECT 2"})
				o.TraceQueryEnd(second, nil, pgx.TraceQueryEndData{})
				o.TraceQueryEnd(first, nil, pgx.TraceQueryEndData{})
			case "missing_trace":
				o.TraceQueryEnd(g.workerContext, nil, pgx.TraceQueryEndData{Err: context.Canceled})
			case "foreign_trace":
				foreign := &operationObserver{ticket: &lifecycleTicket{gate: g}}
				traced := foreign.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{})
				o.TraceQueryEnd(traced, nil, pgx.TraceQueryEndData{Err: context.Canceled})
			case "missing_connection":
				ticket.conn = nil
				traced := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{})
				o.TraceQueryEnd(traced, nil, pgx.TraceQueryEndData{Err: context.Canceled})
			case "sequence_overflow":
				ticket.operationSequence = ^uint64(0)
				traced := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{})
				o.TraceQueryEnd(traced, nil, pgx.TraceQueryEndData{})
				if ticket.operationSequence != ^uint64(0) {
					t.Fatal("operation sequence wrapped")
				}
			}
			if !ticket.recoveryContaminated {
				t.Fatal("unexplained or overlapping operation remained recoverable")
			}
		})
	}
}

func TestRecoveryLateTracePoisonsPendingCompletedAndFinalizedTickets(t *testing.T) {
	for _, state := range []byte{'q', 'r', 'c'} {
		t.Run(string(state), func(t *testing.T) {
			g, ticket := recoveryQualificationFixture(t)
			ticket.state, g.open = state, 0
			g.recoveryFinalized = state == 'c'
			o := &operationObserver{ticket: ticket}
			traced := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{SQL: "rollback"})
			o.TraceQueryEnd(traced, nil, pgx.TraceQueryEndData{})
			if !g.invalid || !ticket.recoveryContaminated || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("late trace escaped retained-ticket sticky invalidation")
			}
		})
	}
}

func TestRecoveryCleanupRejectsMissingNativeConnection(t *testing.T) {
	done := make(chan struct{})
	close(done)
	g := &lifecycleGate{tickets: make(map[uint64]*lifecycleTicket), witness: &diagnosticWitness{}, recoveryQualified: 1}
	ticket := &lifecycleTicket{gate: g, generation: 1, state: 'q', cleanupDone: done}
	g.tickets[1] = ticket
	g.finishRecoveryCleanup()
	if !g.invalid || !g.recoveryFinalized || ticket.state != 'q' || g.recoveryCompleted != 0 || g.recoveryQualified != 1 || g.witness.(*diagnosticWitness).stores == 0 {
		t.Fatal("completed channel without native connection counted as recovery")
	}
}

func TestRecoveryCleanupUsesOneBudgetForAllPendingTickets(t *testing.T) {
	g := &lifecycleGate{tickets: make(map[uint64]*lifecycleTicket), witness: &diagnosticWitness{}, recoveryQualified: 2}
	for generation := uint64(1); generation <= 2; generation++ {
		var done <-chan struct{}
		if generation == 1 {
			done = make(chan struct{})
		}
		g.tickets[generation] = &lifecycleTicket{gate: g, generation: generation, state: 'q', cleanupDone: done}
	}
	started := time.Now()
	g.finishRecoveryCleanup()
	if elapsed := time.Since(started); elapsed >= 3500*time.Millisecond {
		t.Fatal("cleanup allocated a fresh budget per retained ticket")
	}
	if !g.invalid || !g.recoveryFinalized || g.recoveryCompleted != 0 || len(g.tickets) != 2 {
		t.Fatal("pending cleanup timed out without retaining failure")
	}
	for _, ticket := range g.tickets {
		if ticket.state != 'q' {
			t.Fatal("timeout was counted as completed physical cleanup")
		}
	}
}

func TestRecoveryCleanupAndRepeatedReleaseNeverClearSticky(t *testing.T) {
	g, ticket := recoveryQualificationFixture(t)
	g.invalidateTransport()
	ticket.closedObserved(closeObservation{alreadyClosed: true, cleanupDone: true})
	g.finishRecoveryCleanup()
	stores := g.witness.(*diagnosticWitness).stores
	if g.release(1) != nil || !g.invalid || g.firstInvalidReason != invalidTransport || stores == 0 || g.witness.(*diagnosticWitness).stores != stores || g.recoveryCompleted != 0 {
		t.Fatal("cleanup or repeated release cleared sticky failure history")
	}
}

func TestRecoveryLocallyDrainedRequiresCompletedPhysicalLifetimes(t *testing.T) {
	g := &lifecycleGate{tickets: make(map[uint64]*lifecycleTicket)}
	ticket := &lifecycleTicket{gate: g, generation: 1, state: 'q', cleanupDone: make(chan struct{})}
	g.tickets[1] = ticket
	g.closeAdmission()
	if g.locallyDrained() {
		t.Fatal("zero business counts hid pending physical cleanup")
	}
	ticket.state = 'r'
	if !g.locallyDrained() {
		t.Fatal("completed retained lifetime blocked otherwise drained registry")
	}
	ticket.operationInFlight = 1
	if g.locallyDrained() {
		t.Fatal("completed ticket with active operation counted as drained")
	}
	ticket.operationInFlight = 0
	for _, state := range []byte{'p', 'o', 'x'} {
		ticket.state = state
		if g.locallyDrained() {
			t.Fatalf("retained %q lifetime bypassed conservative drain proof", state)
		}
	}
	ticket.state = 'r'
	if !g.locallyDrained() {
		t.Fatal("settled fixture did not return to drained state")
	}
}
