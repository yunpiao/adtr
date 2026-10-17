//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestOperationObserverPreservesConfigurationAndExistingTracer(t *testing.T) {
	config, err := pgx.ParseConfig("postgres://localhost/test")
	if err != nil {
		t.Fatal(err)
	}
	g := &lifecycleGate{}
	ticket := &lifecycleTicket{gate: g}
	if observeLifecycleConfig(config, nil) != config {
		t.Fatal("inactive config copied")
	}
	copy := observeLifecycleConfig(config, ticket)
	if copy == config || config.Tracer != nil {
		t.Fatal("caller config modified")
	}
	observer, ok := copy.Tracer.(*operationObserver)
	if !ok || observer.ticket != ticket {
		t.Fatal("wrong ticket observer")
	}
	if observeLifecycleConfig(copy, ticket) != copy || copy.Tracer != observer {
		t.Fatal("existing tracer replaced")
	}
}

func TestOperationObserverKeepsIndependentErrorAndContextFacts(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	cases := []struct {
		ctx             context.Context
		err             error
		state, category string
		matches         bool
	}{
		{context.Background(), context.Canceled, "active", "canceled", false},
		{canceled, fmt.Errorf("wrapped: %w", context.Canceled), "canceled", "canceled", true},
		{deadline, context.DeadlineExceeded, "deadline_exceeded", "deadline_exceeded", true},
		{canceled, errors.New("private network error"), "canceled", "other", false},
		{canceled, context.DeadlineExceeded, "canceled", "deadline_exceeded", false},
	}
	for _, tc := range cases {
		g := &lifecycleGate{workerContext: context.Background()}
		ticket := &lifecycleTicket{gate: g, state: 'o'}
		o := &operationObserver{ticket: ticket}
		traced := o.TraceQueryStart(tc.ctx, nil, pgx.TraceQueryStartData{SQL: "private SQL", Args: []any{"private argument"}})
		if traced.Done() != tc.ctx.Done() || traced.Err() != tc.ctx.Err() {
			t.Fatal("context cancellation changed")
		}
		o.TraceQueryEnd(traced, nil, pgx.TraceQueryEndData{Err: tc.err})
		d := ticket.firstOperationError
		if d == nil || d.Operation != "statement" || d.ContextState != tc.state || d.ErrorCategory != tc.category || d.ErrorMatchesContext != tc.matches || d.ConnectionMatched || d.NativeClosedAtEnd || d.RootContextState != "active" {
			t.Fatal("operation facts changed")
		}
		rollback := o.TraceQueryStart(canceled, nil, pgx.TraceQueryStartData{SQL: "rollback"})
		o.TraceQueryEnd(rollback, nil, pgx.TraceQueryEndData{Err: context.Canceled})
		if ticket.firstOperationError != d || ticket.firstNativeClosedError != nil {
			t.Fatal("later error replaced first observation")
		}
		raw, _ := json.Marshal(d)
		if strings.Contains(string(raw), "private") {
			t.Fatal("private data retained")
		}
	}
}

func TestOperationObserverDoesNotAttributeAnotherTraceOrSuccessfulQuery(t *testing.T) {
	g := &lifecycleGate{}
	first := &lifecycleTicket{gate: g}
	second := &lifecycleTicket{gate: g}
	a := &operationObserver{ticket: first}
	b := &operationObserver{ticket: second}
	ctx := a.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "begin"})
	b.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: context.Canceled})
	a.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{Err: context.Canceled})
	a.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if first.firstOperationError != nil || second.firstOperationError != nil {
		t.Fatal("missing or foreign trace attributed")
	}
}

func TestOperationDiagnosticSnapshotClosedSchemaAndFirstCause(t *testing.T) {
	g, _ := newSealProtocolTestGate()
	var ticket *lifecycleTicket
	for _, current := range g.tickets {
		ticket = current
	}
	ticket.birthPhase = "open"
	root, cancel := context.WithCancel(context.Background())
	cancel()
	g.workerContext = root
	o := &operationObserver{ticket: ticket}
	ctx := o.TraceQueryStart(root, nil, pgx.TraceQueryStartData{SQL: "begin"})
	o.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: context.Canceled})
	ticket.closedObserved(closeObservation{alreadyClosed: true})
	d := g.uncleanOperationDiagnostic()
	if d == nil || d.RootAtClose != "canceled" || d.FirstError == nil || d.FirstNativeClosedError != nil {
		t.Fatal("close snapshot missing")
	}
	raw, _ := json.Marshal(d)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 6 {
		t.Fatal("top schema")
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(fields["firstError"], &nested) != nil || len(nested) != 9 {
		t.Fatal("operation schema")
	}
	g.firstInvalidReason = invalidTransport
	if g.uncleanOperationDiagnostic() != nil {
		t.Fatal("wrong first cause emitted")
	}
	g.firstInvalidReason = invalidUncleanClose
	g.firstUncleanOperation.RootAtClose = "private"
	if g.uncleanOperationDiagnostic() != nil {
		t.Fatal("unknown raw category emitted")
	}
}

func TestOperationBirthFactsAreImmutableCachedObservations(t *testing.T) {
	for _, ended := range []bool{false, true} {
		g, target := newSealProtocolTestGate()
		if g.pin(1, target, nil) != nil || g.release(1) != nil {
			t.Fatal("release fixture")
		}
		g.seal.captureEnded = ended // Synthetic cached observation, not authorization.
		ticket, err := g.acquire(context.Background())
		if err != nil || ticket.birthPhase != "released" || ticket.birthCaptureEnded != ended {
			t.Fatal("birth facts missing")
		}
		g.seal.captureEnded = !ended
		if ticket.birthCaptureEnded != ended {
			t.Fatal("birth facts changed later")
		}
	}
}

func TestOperationErrorPrecedesLaterRootCancellation(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := &lifecycleGate{workerContext: root}
	ticket := &lifecycleTicket{gate: g}
	o := &operationObserver{ticket: ticket}
	ctx := o.TraceQueryStart(root, nil, pgx.TraceQueryStartData{SQL: "SELECT private"})
	o.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("private network failure")})
	first := ticket.firstOperationError
	cancel()
	rollback := o.TraceQueryStart(root, nil, pgx.TraceQueryStartData{SQL: "rollback"})
	o.TraceQueryEnd(rollback, nil, pgx.TraceQueryEndData{Err: context.Canceled})
	if ticket.firstOperationError != first || first.ContextState != "active" || first.RootContextState != "active" || first.ErrorCategory != "other" || first.ErrorMatchesContext {
		t.Fatal("late cancellation rewrote earlier network observation")
	}
}

func TestOperationRootContextOnlyInstalledApplicationAndFirstValue(t *testing.T) {
	g := installTestGate(t)
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	ObserveCIWorkerContext(first)
	if g.workerContext != nil {
		t.Fatal("synthetic gate accepted application root")
	}
	g.application = true
	ObserveCIWorkerContext(nil)
	if g.workerContext != nil {
		t.Fatal("nil root invented")
	}
	ObserveCIWorkerContext(first)
	ObserveCIWorkerContext(context.Background())
	cancel()
	if g.workerContext != first || diagnosticContextState(g.workerContext) != "canceled" {
		t.Fatal("application root replaced")
	}
}
