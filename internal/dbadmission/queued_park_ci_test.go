//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type queuedParkObservedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *queuedParkObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

// Application/controller installation is synthetic; physical PostgreSQL behavior
// is covered separately. No capture seal or authorization is invented here.
func queuedParkTestFixture(t *testing.T) (*lifecycleGate, context.CancelFunc) {
	t.Helper()
	root, cancel := context.WithCancel(context.Background())
	g := &lifecycleGate{application: true, workerContext: root, witness: &diagnosticWitness{}, changed: make(chan struct{}), tickets: make(map[uint64]*lifecycleTicket)}
	t.Cleanup(cancel)
	t.Cleanup(g.abortQueuedSelection)
	return g, cancel
}

func startQueuedParkTestPrepare(t *testing.T, g *lifecycleGate, budget int64) <-chan error {
	t.Helper()
	result, done := make(chan error, 1), make(chan struct{})
	go func() { result <- g.prepareQueuedSelection(testSealIdentity().Kind, budget); close(done) }()
	t.Cleanup(func() {
		g.abortQueuedSelection()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("queued preparation failed to join after abort")
		}
	})
	return result
}

func waitQueuedParkTestState(t *testing.T, g *lifecycleGate, want string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		g.mu.Lock()
		state, changed := g.queuedState, g.changed
		g.mu.Unlock()
		if state == want {
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("queued preparation did not reach %q", want)
		}
	}
}

func assertQueuedParkTestWaiting(t *testing.T, g *lifecycleGate, result <-chan error) {
	t.Helper()
	select {
	case <-result:
		t.Fatal("queued preparation acknowledged an outstanding physical lifetime")
	default:
	}
	g.mu.Lock()
	parked, state, invalid, selection := g.queuedParked, g.queuedState, g.invalid, g.selection
	g.mu.Unlock()
	if !parked || state != "parking" || invalid || selection != 1 || g.workerContext.Err() != nil {
		t.Fatal("queued parking cancelled or invalidated admitted work")
	}
}

func TestQueuedParkPendingConnectCommitAndClosePrecedeReady(t *testing.T) {
	g, _ := queuedParkTestFixture(t)
	ticket, err := g.acquire(g.workerContext)
	if err != nil {
		t.Fatal("pending connection fixture failed")
	}
	prepared := startQueuedParkTestPrepare(t, g, 2000)
	waitQueuedParkTestState(t, g, "parking")
	assertQueuedParkTestWaiting(t, g, prepared)
	attachTestTicket(ticket)
	assertQueuedParkTestWaiting(t, g, prepared)
	o := &operationObserver{ticket: ticket}
	commit := o.TraceQueryStart(g.workerContext, nil, pgx.TraceQueryStartData{SQL: "commit"})
	assertQueuedParkTestWaiting(t, g, prepared)
	o.TraceQueryEnd(commit, nil, pgx.TraceQueryEndData{})
	assertQueuedParkTestWaiting(t, g, prepared)
	ticket.closed(true)
	select {
	case err := <-prepared:
		if err != nil {
			t.Fatal("cleanly closed connection rejected queued readiness")
		}
	case <-time.After(time.Second):
		t.Fatal("clean physical closure did not unblock queued readiness")
	}
	if g.invalid || !g.queuedParked || g.queuedState != "drained" || g.selection != 1 || g.pending != 0 || g.open != 0 || g.workerContext.Err() != nil {
		t.Fatal("queued readiness changed the parked/root-active boundary")
	}
	if g.configureActivitySelection(testSealIdentity().TaskID, "private-worker") != nil || g.completeQueuedSelection() != nil || g.queuedParked || g.queuedState != "complete" || g.invalid {
		t.Fatal("acknowledged queued selection did not complete")
	}
}

func TestQueuedParkConfigureBindsWithoutAdmittingUntilCompletion(t *testing.T) {
	g, _ := queuedParkTestFixture(t)
	if g.prepareQueuedSelection(testSealIdentity().Kind, 2000) != nil {
		t.Fatal("empty registry preparation failed")
	}
	waitCtx, cancel := context.WithCancel(g.workerContext)
	defer cancel()
	type admissionResult struct {
		ticket *lifecycleTicket
		err    error
	}
	admitted, done := make(chan admissionResult, 1), make(chan struct{})
	observed := &queuedParkObservedContext{Context: waitCtx, waiting: make(chan struct{})}
	go func() { ticket, err := g.acquire(observed); admitted <- admissionResult{ticket, err}; close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("parked admission did not join")
		}
	})
	select {
	case <-observed.waiting:
	case <-admitted:
		t.Fatal("new caller bypassed queued admission park")
	case <-time.After(time.Second):
		t.Fatal("new caller did not reach queued admission wait")
	}
	if g.configureActivitySelection(testSealIdentity().TaskID, "private-worker") != nil {
		t.Fatal("drained selection binding failed")
	}
	g.mu.Lock()
	parked := g.queuedParked && g.queuedState == "configured" && g.selection == 2 && g.selectionTask == testSealIdentity().TaskID && g.selectionOwner == "private-worker" && g.total == 0 && g.pending == 0 && g.open == 0 && len(g.tickets) == 0
	g.mu.Unlock()
	if !parked {
		t.Fatal("configure opened physical admission or failed to bind identity")
	}
	select {
	case <-admitted:
		t.Fatal("physical caller entered before configuration acknowledgment completion")
	default:
	}
	if g.completeQueuedSelection() != nil {
		t.Fatal("acknowledgment completion rejected drained binding")
	}
	select {
	case result := <-admitted:
		if result.err != nil || result.ticket == nil {
			t.Fatal("acknowledged selection failed to admit original caller")
		}
		attachTestTicket(result.ticket)
		result.ticket.closed(true)
	case <-time.After(time.Second):
		t.Fatal("completed selection left physical caller parked")
	}
	if g.queuedParked || g.queuedState != "complete" || g.invalid || g.workerContext.Err() != nil || g.total != 1 || g.pending != 0 || g.open != 0 {
		t.Fatal("completion changed original context or clean lifecycle accounting")
	}
}

func TestQueuedParkRetainedPhysicalStatesAndActiveTraceCannotBecomeReady(t *testing.T) {
	for _, state := range []byte{'p', 'o', 'x', 'q', 'r'} {
		t.Run(string(state), func(t *testing.T) {
			g, _ := queuedParkTestFixture(t)
			ticket := &lifecycleTicket{gate: g, generation: 1, state: state}
			if state == 'r' {
				ticket.operationInFlight = 1
			}
			g.tickets[1] = ticket
			if g.prepareQueuedSelection(testSealIdentity().Kind, 1) == nil || !g.invalid || g.queuedParked || g.queuedState != "failed" || g.workerContext.Err() != nil || g.tickets[1] != ticket {
				t.Fatal("zero business counts hid retained physical work or active trace")
			}
		})
	}
}

func TestQueuedParkRootCancellationTransportAndAbortWakePreparation(t *testing.T) {
	for _, cause := range []string{"root_cancelled", "transport", "abort"} {
		t.Run(cause, func(t *testing.T) {
			g, cancelRoot := queuedParkTestFixture(t)
			ticket, err := g.acquire(g.workerContext)
			if err != nil {
				t.Fatal("pending fixture admission failed")
			}
			prepared := startQueuedParkTestPrepare(t, g, 2000)
			waitQueuedParkTestState(t, g, "parking")
			switch cause {
			case "root_cancelled":
				cancelRoot()
			case "transport":
				g.invalidateTransport()
			case "abort":
				g.abortQueuedSelection()
			}
			select {
			case err := <-prepared:
				if err == nil {
					t.Fatal("failed preparation acknowledged queued readiness")
				}
			case <-time.After(time.Second):
				t.Fatal("failure did not wake queued preparation")
			}
			if !g.invalid || g.queuedParked || g.queuedState != "failed" || g.pending != 1 || ticket.state != 'p' || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("queued failure hid pending lifetime or retained admission park")
			}
			if cause == "transport" && g.firstInvalidReason != invalidTransport {
				t.Fatal("queued failure replaced original transport cause")
			}
			if cause != "root_cancelled" && g.workerContext.Err() != nil {
				t.Fatal("preparation failure cancelled the original root")
			}
			stores := g.witness.(*diagnosticWitness).stores
			g.abortQueuedSelection()
			if !g.invalid || g.queuedParked || g.witness.(*diagnosticWitness).stores != stores {
				t.Fatal("repeated abort changed committed sticky history")
			}
		})
	}
}

func TestQueuedParkExpiredAdmissionContextsDoNotAllocateOrInvalidate(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			g, _ := queuedParkTestFixture(t)
			if g.prepareQueuedSelection(testSealIdentity().Kind, 2000) != nil {
				t.Fatal("empty registry preparation failed")
			}
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			if ticket, err := g.acquire(ctx); ticket != nil || err != want || g.invalid || g.total != 0 || g.pending != 0 || g.open != 0 || len(g.tickets) != 0 || g.witness.(*diagnosticWitness).stores != 0 || !g.queuedParked {
				t.Fatal("expired no-ticket caller was renewed, admitted, or marked sticky")
			}
		})
	}
}

func TestQueuedParkWaitingAdmissionCancellationPreservesOriginalError(t *testing.T) {
	g, _ := queuedParkTestFixture(t)
	if g.prepareQueuedSelection(testSealIdentity().Kind, 2000) != nil {
		t.Fatal("empty registry preparation failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &queuedParkObservedContext{Context: ctx, waiting: make(chan struct{})}
	returned := make(chan error, 1)
	go func() {
		ticket, err := g.acquire(observed)
		if ticket != nil {
			returned <- errSealProtocol
			return
		}
		returned <- err
	}()
	select {
	case <-observed.waiting:
	case <-returned:
		t.Fatal("caller returned before entering queued admission wait")
	case <-time.After(time.Second):
		t.Fatal("caller did not reach queued admission wait")
	}
	cancel()
	select {
	case err := <-returned:
		if err != context.Canceled {
			t.Fatal("parked caller lost original context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("parked caller ignored cancellation")
	}
	if g.invalid || g.total != 0 || len(g.tickets) != 0 || !g.queuedParked || g.witness.(*diagnosticWitness).stores != 0 {
		t.Fatal("no-ticket cancellation altered queued protocol authority")
	}
}

func TestQueuedParkCandidateIdentityWaitCancellationRemainsFatal(t *testing.T) {
	g, _ := queuedParkTestFixture(t)
	if g.prepareQueuedSelection(testSealIdentity().Kind, 2000) != nil {
		t.Fatal("empty registry preparation failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.awaitActivitySelection(ctx, testSealIdentity()); err != context.Canceled || !g.invalid || g.firstInvalidReason != invalidWaitCancelled || g.total != 0 || len(g.tickets) != 0 {
		t.Fatal("candidate identity wait lost its original hard-failure contract")
	}
	g.abortQueuedSelection()
	if g.queuedParked || !g.invalid {
		t.Fatal("abort erased candidate wait failure")
	}
}

func TestQueuedParkPreparationBudgetAndRootPrerequisites(t *testing.T) {
	for _, kind := range []string{"zero_budget", "oversized_budget", "invalid_kind", "not_application", "missing_root", "cancelled_root", "repeated"} {
		t.Run(kind, func(t *testing.T) {
			g, cancelRoot := queuedParkTestFixture(t)
			budget, taskKind := int64(2000), testSealIdentity().Kind
			switch kind {
			case "zero_budget":
				budget = 0
			case "oversized_budget":
				budget = 2001
			case "invalid_kind":
				taskKind = "unselected.kind"
			case "not_application":
				g.application = false
			case "missing_root":
				g.workerContext = nil
			case "cancelled_root":
				cancelRoot()
			case "repeated":
				if g.prepareQueuedSelection(taskKind, budget) != nil {
					t.Fatal("initial queued preparation failed")
				}
			}
			if g.prepareQueuedSelection(taskKind, budget) == nil || g.total != 0 || len(g.tickets) != 0 {
				t.Fatal("invalid or repeated preparation acquired queued authority")
			}
			if (kind == "missing_root" || kind == "cancelled_root") && (!g.invalid || g.queuedParked) {
				t.Fatal("unavailable worker root was not rejected before parking")
			}
			if kind == "zero_budget" || kind == "oversized_budget" || kind == "invalid_kind" || kind == "not_application" {
				if g.selection != 0 || g.queuedState != "" || g.queuedParked || g.invalid {
					t.Fatal("rejected initial request mutated application admission")
				}
			}
		})
	}
}

func TestQueuedParkAbortReleasesParkButPreservesFailure(t *testing.T) {
	g, _ := queuedParkTestFixture(t)
	g.abortQueuedSelection()
	if g.invalid {
		t.Fatal("abort without queued work invented a failure")
	}
	if g.prepareQueuedSelection(testSealIdentity().Kind, 2000) != nil {
		t.Fatal("empty registry preparation failed")
	}
	g.abortQueuedSelection()
	if !g.invalid || g.queuedParked || g.queuedState != "failed" {
		t.Fatal("abort did not release park with sticky failure")
	}
	if ticket, err := g.acquire(context.Background()); ticket != nil || err != errAdmissionClosed || g.total != 0 {
		t.Fatal("aborted unsealed gate allowed a new physical lifetime")
	}
}

func TestQueuedParkCompletionRejectsUnboundOrChangedDrainProof(t *testing.T) {
	for _, kind := range []string{"unbound", "pending", "retained_q", "active_trace", "sticky", "repeated"} {
		t.Run(kind, func(t *testing.T) {
			g, _ := queuedParkTestFixture(t)
			if g.prepareQueuedSelection(testSealIdentity().Kind, 2000) != nil {
				t.Fatal("empty registry preparation failed")
			}
			if kind != "unbound" && g.configureActivitySelection(testSealIdentity().TaskID, "private-worker") != nil {
				t.Fatal("drained selection binding failed")
			}
			switch kind {
			case "pending":
				g.pending = 1
			case "retained_q":
				g.tickets[1] = &lifecycleTicket{gate: g, state: 'q'}
			case "active_trace":
				g.tickets[1] = &lifecycleTicket{gate: g, state: 'r', operationInFlight: 1}
			case "sticky":
				g.invalidateTransport()
			case "repeated":
				if g.completeQueuedSelection() != nil {
					t.Fatal("initial completion failed")
				}
			}
			if g.completeQueuedSelection() == nil || !g.invalid || g.total != 0 {
				t.Fatal("unbound or changed physical proof completed queued admission")
			}
			if kind == "sticky" && g.firstInvalidReason != invalidTransport {
				t.Fatal("completion erased original sticky cause")
			}
			g.abortQueuedSelection()
			if g.queuedParked || !g.invalid {
				t.Fatal("abort lost failed completion history")
			}
		})
	}
}

func TestQueuedParkLegacySelectionPreparationKeepsOriginalAdmission(t *testing.T) {
	g, _ := queuedParkTestFixture(t)
	if g.prepareActivitySelection(testSealIdentity().Kind) != nil || g.queuedParked || g.queuedState != "" {
		t.Fatal("legacy selection preparation acquired new physical parking")
	}
	ticket, err := g.acquire(g.workerContext)
	if err != nil {
		t.Fatal("legacy selection path changed physical admission")
	}
	attachTestTicket(ticket)
	ticket.closed(true)
	if g.configureActivitySelection(testSealIdentity().TaskID, "private-worker") != nil || g.queuedParked || g.queuedState != "" || g.invalid {
		t.Fatal("legacy selection configuration required new queued completion")
	}
}
