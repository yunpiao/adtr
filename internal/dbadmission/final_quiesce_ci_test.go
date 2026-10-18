//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The release authorization is a synthetic registry precondition here. Capture
// authentication and physical PostgreSQL lifetimes have separate tests.
func finalQuiesceFixture(t *testing.T) (*lifecycleGate, context.CancelFunc, *atomic.Int32) {
	t.Helper()
	root, cancel := context.WithCancel(context.Background())
	calls := &atomic.Int32{}
	g := &lifecycleGate{
		workerContext: root, changed: make(chan struct{}), tickets: make(map[uint64]*lifecycleTicket),
		witness: &diagnosticWitness{}, seal: &sealAttempt{epoch: 1, released: true, recoveryAuthorized: true, publishDone: make(chan struct{})},
	}
	g.finalQuiesce.stop = func() { calls.Add(1); cancel() }
	t.Cleanup(cancel)
	t.Cleanup(g.abortFinalQuiesce)
	return g, cancel, calls
}

func startFinalQuiesceTestPrepare(t *testing.T, g *lifecycleGate, budget int64) <-chan error {
	t.Helper()
	result, done := make(chan error, 1), make(chan struct{})
	go func() {
		result <- g.prepareFinalQuiesce(budget)
		close(done)
	}()
	t.Cleanup(func() {
		g.abortFinalQuiesce()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("final prepare did not join after abort")
		}
	})
	return result
}

func waitFinalQuiesceTestState(t *testing.T, g *lifecycleGate, want string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		g.mu.Lock()
		state, changed := g.finalQuiesce.state, g.changed
		g.mu.Unlock()
		if state == want {
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("final quiesce did not reach %q", want)
		}
	}
}

func assertFinalQuiesceTestWaiting(t *testing.T, g *lifecycleGate, result <-chan error, calls *atomic.Int32) {
	t.Helper()
	select {
	case <-result:
		t.Fatal("final prepare returned while an admitted lifetime remained")
	default:
	}
	g.mu.Lock()
	parked, state, invalid := g.finalParked, g.finalQuiesce.state, g.invalid
	g.mu.Unlock()
	if !parked || state != "parking" || invalid || g.workerContext.Err() != nil || calls.Load() != 0 {
		t.Fatal("parking cancelled or invalidated admitted work")
	}
}

func TestFinalQuiesceWaitsForPendingConnectCommitAndManagedClose(t *testing.T) {
	g, _, calls := finalQuiesceFixture(t)
	ticket, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("pending fixture admission failed")
	}
	prepared := startFinalQuiesceTestPrepare(t, g, 5000)
	waitFinalQuiesceTestState(t, g, "parking")
	assertFinalQuiesceTestWaiting(t, g, prepared, calls)
	attachTestTicket(ticket)
	assertFinalQuiesceTestWaiting(t, g, prepared, calls)
	o := &operationObserver{ticket: ticket}
	commit := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{SQL: "commit"})
	assertFinalQuiesceTestWaiting(t, g, prepared, calls)
	o.TraceQueryEnd(commit, nil, pgx.TraceQueryEndData{})
	assertFinalQuiesceTestWaiting(t, g, prepared, calls)
	ticket.closed(true)
	select {
	case err := <-prepared:
		if err != nil {
			t.Fatal("cleanly drained fixture rejected final prepare")
		}
	case <-time.After(time.Second):
		t.Fatal("cleanly closed lifetime did not unblock final prepare")
	}
	if g.finalQuiesce.state != "drained" || !g.finalParked || g.workerContext.Err() != nil || calls.Load() != 0 || g.invalid {
		t.Fatal("prepare acknowledgment cancelled root or reopened admission")
	}
	if g.cancelFinalQuiesce() != nil || g.workerContext.Err() != context.Canceled || !g.finalParked || g.finalQuiesce.state != "cancelled" || calls.Load() != 1 || g.invalid {
		t.Fatal("final cancellation did not preserve the two-stage boundary")
	}
	if g.completeFinalQuiesce() != nil || g.finalParked || g.finalQuiesce.state != "complete" || g.invalid {
		t.Fatal("post-acknowledgment completion did not open admission")
	}
}

func TestFinalQuiesceRootDoneDoesNotOpenUntilStopReturns(t *testing.T) {
	g, cancelRoot, calls := finalQuiesceFixture(t)
	rootDone, allowCompletion, childCompleted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(allowCompletion) }) }
	t.Cleanup(unblock)
	g.finalQuiesce.stop = func() {
		calls.Add(1)
		cancelRoot()
		close(rootDone)
		<-allowCompletion
		// This controlled completion stands for synchronous child propagation;
		// the test claims the stop-return barrier, not context internals.
		close(childCompleted)
	}
	if g.prepareFinalQuiesce(5000) != nil {
		t.Fatal("empty fixture prepare failed")
	}
	cancelResult, cancelDone := make(chan error, 1), make(chan struct{})
	go func() { cancelResult <- g.cancelFinalQuiesce(); close(cancelDone) }()
	t.Cleanup(func() {
		unblock()
		select {
		case <-cancelDone:
		case <-time.After(time.Second):
			t.Error("final cancel did not join after stop release")
		}
	})
	select {
	case <-rootDone:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel root")
	}
	g.mu.Lock()
	parked, state := g.finalParked, g.finalQuiesce.state
	g.mu.Unlock()
	if g.workerContext.Err() != context.Canceled || !parked || state != "cancelling" {
		t.Fatal("root.Done opened admission before stop returned")
	}
	select {
	case <-childCompleted:
		t.Fatal("controlled child completion escaped its latch")
	case <-cancelResult:
		t.Fatal("cancel acknowledgment preceded synchronous stop completion")
	default:
	}
	waitCtx, stopWaiter := context.WithCancel(context.Background())
	defer stopWaiter()
	admitted := make(chan *lifecycleTicket, 1)
	waiterDone := make(chan struct{})
	go func() { ticket, _ := g.acquire(waitCtx); admitted <- ticket; close(waiterDone) }()
	t.Cleanup(func() {
		stopWaiter()
		select {
		case <-waiterDone:
		case <-time.After(time.Second):
			t.Error("parked admission did not join")
		}
	})
	select {
	case <-admitted:
		t.Fatal("uncancelled waiter entered while stop was still running")
	default:
	}
	unblock()
	select {
	case err := <-cancelResult:
		if err != nil {
			t.Fatal("completed stop rejected final cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("completed stop did not return cancellation acknowledgment")
	}
	if !g.finalParked || g.finalQuiesce.state != "cancelled" {
		t.Fatal("completed stop opened admission before acknowledgment completion")
	}
	select {
	case <-admitted:
		t.Fatal("uncancelled waiter entered before acknowledgment completion")
	default:
	}
	if g.completeFinalQuiesce() != nil {
		t.Fatal("post-acknowledgment completion rejected drained fixture")
	}
	select {
	case ticket := <-admitted:
		if ticket == nil {
			t.Fatal("completed stop did not unpark uncancelled admission")
		}
	case <-time.After(time.Second):
		t.Fatal("completed stop left uncancelled admission parked")
	}
	if ticket, err := g.acquire(g.workerContext); ticket != nil || err != context.Canceled || calls.Load() != 1 || g.invalid {
		t.Fatal("cancelled worker root entered a new native lifetime")
	}
}

func TestFinalQuiescePrepareRejectsInvalidPrerequisites(t *testing.T) {
	for _, kind := range []string{"zero_budget", "oversized_budget", "missing_stop", "missing_root", "cancelled_root", "missing_seal", "unreleased", "unauthorized", "sticky", "repeated"} {
		t.Run(kind, func(t *testing.T) {
			g, cancelRoot, calls := finalQuiesceFixture(t)
			budget := int64(5000)
			switch kind {
			case "zero_budget":
				budget = 0
			case "oversized_budget":
				budget = 30001
			case "missing_stop":
				g.finalQuiesce.stop = nil
			case "missing_root":
				g.workerContext = nil
			case "cancelled_root":
				cancelRoot()
			case "missing_seal":
				g.seal = nil
			case "unreleased":
				g.seal.released = false
				g.seal.target = &lifecycleTicket{state: 'c'}
			case "unauthorized":
				g.seal.recoveryAuthorized = false
			case "sticky":
				g.invalidateTransport()
			case "repeated":
				if g.prepareFinalQuiesce(budget) != nil {
					t.Fatal("initial prepare fixture failed")
				}
			}
			if g.prepareFinalQuiesce(budget) == nil || !g.invalid || calls.Load() != 0 || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("invalid prerequisite produced final prepare acknowledgment")
			}
			if kind == "sticky" && g.firstInvalidReason != invalidTransport {
				t.Fatal("prepare replaced original sticky cause")
			}
			g.abortFinalQuiesce()
			if g.finalParked || !g.invalid {
				t.Fatal("abort failed to unpark while preserving prepare failure")
			}
		})
	}
}

func TestFinalQuiesceTimeoutRetainsFailureUntilExplicitAbortUnparks(t *testing.T) {
	g, _, calls := finalQuiesceFixture(t)
	g.tickets[1] = &lifecycleTicket{gate: g, generation: 1, state: 'q'}
	if g.prepareFinalQuiesce(1) == nil || !g.invalid || !g.finalParked || g.finalQuiesce.state != "failed" || calls.Load() != 0 || g.workerContext.Err() != nil {
		t.Fatal("pending physical cleanup timed out without preserving failure")
	}
	g.abortFinalQuiesce()
	if g.finalParked || !g.invalid || calls.Load() != 0 {
		t.Fatal("abort erased timeout or retained the admission park")
	}
	stores := g.witness.(*diagnosticWitness).stores
	g.abortFinalQuiesce()
	if g.finalParked || !g.invalid || g.witness.(*diagnosticWitness).stores != stores {
		t.Fatal("repeated abort changed sticky history")
	}
}

func TestFinalQuiesceDrainedPredicateIncludesRetainedStatesAndActiveOperations(t *testing.T) {
	for _, state := range []byte{'p', 'o', 'x', 'q', 'r', 'c'} {
		g, _, _ := finalQuiesceFixture(t)
		ticket := &lifecycleTicket{gate: g, generation: 1, state: state}
		g.tickets[1] = ticket
		g.mu.Lock()
		drained := g.finalDrainedLocked()
		g.mu.Unlock()
		if drained != (state == 'r' || state == 'c') {
			t.Fatalf("retained %q state was misclassified for final drain", state)
		}
		ticket.operationInFlight = 1
		g.mu.Lock()
		drained = g.finalDrainedLocked()
		g.mu.Unlock()
		if drained {
			t.Fatal("active operation disappeared from final drain proof")
		}
	}
}

func TestFinalQuiesceCancelRechecksDeadlineDrainAndRoot(t *testing.T) {
	for _, kind := range []string{"expired", "root_cancelled", "pending", "retained_q", "active_operation", "explicit_sticky", "transport_sticky", "stop_did_not_cancel", "stop_completed_after_deadline"} {
		t.Run(kind, func(t *testing.T) {
			g, cancelRoot, calls := finalQuiesceFixture(t)
			if g.prepareFinalQuiesce(5000) != nil {
				t.Fatal("empty fixture prepare failed")
			}
			wantCalls := int32(0)
			wantParked := true
			wantReason := invalidExplicit
			switch kind {
			case "expired":
				g.finalQuiesce.deadline = time.Now().Add(-time.Second)
			case "root_cancelled":
				cancelRoot()
			case "pending":
				g.pending = 1
			case "retained_q":
				g.tickets[1] = &lifecycleTicket{gate: g, state: 'q'}
			case "active_operation":
				g.tickets[1] = &lifecycleTicket{gate: g, state: 'r', operationInFlight: 1}
			case "explicit_sticky":
				g.invalidate()
			case "transport_sticky":
				g.invalidateTransport()
				wantParked = false
				wantReason = invalidTransport
				if g.finalParked || g.finalQuiesce.state != "failed" {
					t.Fatal("transport failure did not atomically fail and unpark final admission")
				}
			case "stop_did_not_cancel":
				wantCalls = 1
				g.finalQuiesce.stop = func() { calls.Add(1) }
			case "stop_completed_after_deadline":
				wantCalls = 1
				g.finalQuiesce.stop = func() {
					calls.Add(1)
					cancelRoot()
					g.mu.Lock()
					g.finalQuiesce.deadline = time.Now().Add(-time.Second)
					g.mu.Unlock()
				}
			}
			if err := g.cancelFinalQuiesce(); err != errSealProtocol {
				t.Fatal("changed final-cancel precondition did not return the protocol rejection")
			}
			if !g.invalid {
				t.Fatal("rejected final cancellation did not retain sticky invalidation")
			}
			if calls.Load() != wantCalls {
				t.Fatal("rejected final cancellation invoked an unexpected number of root cancellations")
			}
			if g.finalParked != wantParked {
				t.Fatal("rejected final cancellation changed the expected admission park state")
			}
			if (kind == "explicit_sticky" || kind == "transport_sticky") && g.firstInvalidReason != wantReason {
				t.Fatal("cancel replaced the original sticky cause")
			}
			if kind == "transport_sticky" && g.finalQuiesce.state != "failed" {
				t.Fatal("rejected final cancellation changed the transport-failed state")
			}
			g.abortFinalQuiesce()
			if g.finalParked || !g.invalid {
				t.Fatal("abort did not preserve failed final-cancel state")
			}
		})
	}
}

func TestFinalQuiesceRepeatedCancelCannotInvokeStopTwice(t *testing.T) {
	g, _, calls := finalQuiesceFixture(t)
	if g.prepareFinalQuiesce(5000) != nil || g.cancelFinalQuiesce() != nil || g.completeFinalQuiesce() != nil {
		t.Fatal("initial final shutdown failed")
	}
	if g.cancelFinalQuiesce() == nil || calls.Load() != 1 || !g.invalid || g.finalParked {
		t.Fatal("repeated cancellation repeated stop or cleared failure")
	}
}

func TestFinalQuiesceCompleteRejectsWrongPhaseRepeatedAndChangedProof(t *testing.T) {
	for _, kind := range []string{"unprepared", "drained", "failed", "repeated", "expired", "pending", "active_operation", "sticky"} {
		t.Run(kind, func(t *testing.T) {
			g, _, calls := finalQuiesceFixture(t)
			wantCalls := int32(0)
			if kind != "unprepared" {
				if g.prepareFinalQuiesce(5000) != nil {
					t.Fatal("completion fixture prepare failed")
				}
				if kind != "drained" {
					if g.cancelFinalQuiesce() != nil {
						t.Fatal("completion fixture cancellation failed")
					}
					wantCalls = 1
				}
			}
			switch kind {
			case "failed":
				g.abortFinalQuiesce()
			case "repeated":
				if g.completeFinalQuiesce() != nil {
					t.Fatal("initial completion failed")
				}
			case "expired":
				g.finalQuiesce.deadline = time.Now().Add(-time.Second)
			case "pending":
				g.pending = 1
			case "active_operation":
				g.tickets[1] = &lifecycleTicket{gate: g, state: 'r', operationInFlight: 1}
			case "sticky":
				g.invalidateTransport()
			}
			if g.completeFinalQuiesce() == nil || !g.invalid || calls.Load() != wantCalls {
				t.Fatal("invalid completion phase or changed proof opened admission")
			}
			if kind == "sticky" && g.firstInvalidReason != invalidTransport {
				t.Fatal("completion replaced original sticky cause")
			}
			g.abortFinalQuiesce()
			if g.finalParked || !g.invalid {
				t.Fatal("abort did not preserve failed completion history")
			}
		})
	}
}

func TestFinalQuiesceInvalidationAbortAndRootCancellationWakePrepare(t *testing.T) {
	for _, failure := range []string{"transport", "abort", "root_cancelled"} {
		g, cancelRoot, calls := finalQuiesceFixture(t)
		if _, err := g.acquire(context.Background()); err != nil {
			t.Fatal("pending fixture admission failed")
		}
		prepared := startFinalQuiesceTestPrepare(t, g, 5000)
		waitFinalQuiesceTestState(t, g, "parking")
		switch failure {
		case "abort":
			g.abortFinalQuiesce()
		case "transport":
			g.invalidateTransport()
		case "root_cancelled":
			cancelRoot()
		}
		select {
		case err := <-prepared:
			if err == nil {
				t.Fatal("invalidated final prepare acknowledged success")
			}
		case <-time.After(time.Second):
			t.Fatal("invalidation did not wake final prepare")
		}
		g.abortFinalQuiesce()
		wantRootCancelled := failure == "root_cancelled"
		if g.finalParked || !g.invalid || calls.Load() != 0 || (g.workerContext.Err() == context.Canceled) != wantRootCancelled {
			t.Fatal("failed prepare lost sticky history or cancelled admitted work")
		}
	}
}

func TestFinalQuiesceShutdownConfigurationIsApplicationOnlyAndOneShot(t *testing.T) {
	g := installTestGate(t)
	var calls atomic.Int32
	stop := func() { calls.Add(1) }
	ConfigureCIShutdown(stop)
	if g.finalQuiesce.stop != nil || g.invalid {
		t.Fatal("non-application gate accepted shutdown callback")
	}
	g.application = true
	ConfigureCIShutdown(nil)
	if g.finalQuiesce.stop != nil || g.invalid {
		t.Fatal("nil callback configured shutdown")
	}
	ConfigureCIShutdown(stop)
	if g.finalQuiesce.stop == nil || g.invalid || calls.Load() != 0 {
		t.Fatal("shutdown configuration invoked or lost callback")
	}
	ConfigureCIShutdown(func() { calls.Add(100) })
	if !g.invalid || calls.Load() != 0 {
		t.Fatal("duplicate shutdown configuration did not fail closed")
	}
	g.finalQuiesce.stop()
	if calls.Load() != 1 {
		t.Fatal("duplicate shutdown configuration replaced original callback")
	}
}
