//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This observer preserves cancellation, deadlines, query results, admission and cleanup.
// Existing tracers retain every interface and behavior; when one is installed,
// this extra observer abstains and its absent evidence remains unknown.
func observeLifecycleConfig(config *pgx.ConnConfig, permit lifecyclePermit) *pgx.ConnConfig {
	ticket, ok := permit.(*lifecycleTicket)
	if !ok || config == nil || config.Tracer != nil {
		return config
	}
	copy := config.Copy()
	copy.Tracer = &operationObserver{ticket: ticket}
	return copy
}

func ObserveCIWorkerContext(ctx context.Context) {
	coordinator.Lock()
	defer coordinator.Unlock()
	g := coordinator.gate
	if g == nil || !g.application || ctx == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.workerContext == nil {
		g.workerContext = ctx
	}
}

type operationObserver struct{ ticket *lifecycleTicket }
type operationTraceKey struct{}
type operationTrace struct {
	observer              *operationObserver
	ctx                   context.Context
	operation, startPhase string
	sequence              uint64
	closedRollback        bool
}
type operationErrorDiagnostic struct {
	Operation           string `json:"operation"`
	StartPhase          string `json:"startPhase"`
	EndPhase            string `json:"endPhase"`
	ContextState        string `json:"contextState"`
	ErrorCategory       string `json:"errorCategory"`
	ErrorMatchesContext bool   `json:"errorMatchesContext"`
	NativeClosedAtEnd   bool   `json:"nativeClosedAtEnd"`
	RootContextState    string `json:"rootContextState"`
	ConnectionMatched   bool   `json:"connectionMatched"`
}
type uncleanOperationDiagnostic struct {
	Event                  string                    `json:"event"`
	BirthPhase             string                    `json:"birthPhase"`
	BirthCaptureEnded      bool                      `json:"birthCaptureEnded"`
	FirstError             *operationErrorDiagnostic `json:"firstError"`
	FirstNativeClosedError *operationErrorDiagnostic `json:"firstNativeClosedError"`
	RootAtClose            string                    `json:"rootAtClose"`
}

func diagnosticContextState(ctx context.Context) string {
	if ctx == nil {
		return "unknown"
	}
	switch ctx.Err() {
	case nil:
		return "active"
	case context.Canceled:
		return "canceled"
	case context.DeadlineExceeded:
		return "deadline_exceeded"
	default:
		return "other"
	}
}
func diagnosticErrorCategory(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "other"
	}
}
func (o *operationObserver) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	operation := "statement"
	switch data.SQL {
	case "begin":
		operation = "begin"
	case "commit":
		operation = "commit"
	case "rollback":
		operation = "rollback"
	}
	t := o.ticket
	g := t.gate
	nativeClosed := conn != nil && conn.IsClosed()
	g.mu.Lock()
	matched := t.conn != nil && t.conn.raw == conn && t.state == 'o'
	closedRollback := operation == "rollback" && nativeClosed && matched && t.firstOperationError != nil && t.firstOperationError == t.firstNativeClosedError
	if t.operationInFlight != 0 || t.operationSequence == ^uint64(0) || g.recoveryFinalized || t.state == 'q' || t.state == 'r' || (t.firstOperationError != nil && !closedRollback) {
		t.contaminateRecoveryLocked()
	}
	if t.operationSequence != ^uint64(0) {
		t.operationSequence++
	}
	t.activeOperation = t.operationSequence
	t.operationInFlight++
	trace := operationTrace{o, ctx, operation, g.diagnosticPhaseLocked(), t.activeOperation, closedRollback}
	g.mu.Unlock()
	return context.WithValue(ctx, operationTraceKey{}, trace)
}
func (o *operationObserver) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	// Error identity is sampled independently before observing cancellation.
	category := diagnosticErrorCategory(data.Err)
	trace, ok := ctx.Value(operationTraceKey{}).(operationTrace)
	t := o.ticket
	g := t.gate
	if !ok || trace.observer != o {
		g.mu.Lock()
		t.contaminateRecoveryLocked()
		g.mu.Unlock()
		return
	}
	state := diagnosticContextState(trace.ctx)
	matches := (state == "canceled" && category == "canceled") || (state == "deadline_exceeded" && category == "deadline_exceeded")
	nativeClosed := conn != nil && conn.IsClosed()
	closedRollbackResult := data.Err != nil && errors.Is(data.Err, pgconn.ErrConnClosed)
	g.mu.Lock()
	defer g.mu.Unlock()
	matched := t.conn != nil && t.conn.raw == conn && t.state == 'o'
	if t.operationInFlight != 1 || trace.sequence != t.activeOperation || !matched || g.recoveryFinalized {
		t.contaminateRecoveryLocked()
	}
	if t.operationInFlight > 0 {
		t.operationInFlight--
	}
	if t.firstOperationError != nil && !(trace.closedRollback && closedRollbackResult && nativeClosed && matched) {
		t.contaminateRecoveryLocked()
	}
	if data.Err == nil {
		return
	}
	d := &operationErrorDiagnostic{trace.operation, trace.startPhase, g.diagnosticPhaseLocked(), state, category, matches, nativeClosed, diagnosticContextState(g.workerContext), matched}
	if t.firstOperationError == nil {
		t.firstOperationError = d
	}
	if nativeClosed && t.firstNativeClosedError == nil {
		t.firstNativeClosedError = d
	}
}

func validOperationPhase(phase string) bool {
	switch phase {
	case "open", "draining", "sealed", "released", "unknown":
		return true
	}
	return false
}
func validOperationContext(state string) bool {
	switch state {
	case "active", "canceled", "deadline_exceeded", "other", "unknown":
		return true
	}
	return false
}
func validOperationError(d *operationErrorDiagnostic) bool {
	if d == nil {
		return true
	}
	if !validOperationPhase(d.StartPhase) || !validOperationPhase(d.EndPhase) || !validOperationContext(d.ContextState) || !validOperationContext(d.RootContextState) {
		return false
	}
	switch d.Operation {
	case "begin", "commit", "rollback", "statement":
	default:
		return false
	}
	switch d.ErrorCategory {
	case "none", "canceled", "deadline_exceeded", "other":
	default:
		return false
	}
	return true
}
func (g *lifecycleGate) uncleanOperationDiagnostic() *uncleanOperationDiagnostic {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.firstInvalidReason != invalidUncleanClose || g.firstUncleanOperation == nil {
		return nil
	}
	d := *g.firstUncleanOperation
	if d.Event != "owned_ci_unclean_operation" || !validOperationPhase(d.BirthPhase) || !validOperationContext(d.RootAtClose) || !validOperationError(d.FirstError) || !validOperationError(d.FirstNativeClosedError) {
		return nil
	}
	return &d
}
func (g *lifecycleGate) writeUncleanOperationDiagnostic() {
	d := g.uncleanOperationDiagnostic()
	if d == nil {
		return
	}
	data, err := json.Marshal(d)
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}
