//go:build adtr_ci_dbadmission

package operationallogs

import (
	"context"
	"errors"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/dbadmission"
)

// This is an observation of the first failed attempt, not a delivery verdict.
// In particular, an errors.Is match can coexist with other joined causes.
// Raw errors, SQL, event identities and configuration are never retained here.
// contextState is sampled at the Store error boundary (or after an unknown
// store returns). recorderContextState is the Recorder.run context after Append
// returns, before its attempt cancel; it is not the worker business root.
type appendDiagnostic struct {
	connectPhase         string
	stage                string
	category             string
	contextState         string
	recorderContextState string
}
type recorderDiagnostic struct {
	first appendDiagnostic
	set   bool
}

type CIJournalFailure struct {
	ConnectPhase         string `json:"connectPhase"`
	Event                string `json:"event"`
	Stage                string `json:"stage"`
	ErrorCategory        string `json:"errorCategory"`
	ContextState         string `json:"contextState"`
	RecorderContextState string `json:"recorderContextState"`
}

func journalErrorCategory(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return "postgres"
	}
	var network net.Error
	if errors.As(err, &network) {
		return "network"
	}
	var public *Error
	if errors.As(err, &public) {
		switch public.Code {
		case "invalid_event", "invalid_observation", "invalid_stored_event":
			return "validation"
		case "process_conflict", "event_conflict":
			return "conflict"
		}
	}
	return "other"
}
func journalContextState(ctx context.Context) string {
	switch ctx.Err() {
	case nil:
		return "active"
	case context.Canceled:
		return "canceled"
	case context.DeadlineExceeded:
		return "deadline_exceeded"
	default:
		return "unknown"
	}
}
func newStoreAppendError(ctx context.Context, stage string, err error, uncertain bool) *AppendError {
	category := journalErrorCategory(err)
	diagnostic := appendDiagnostic{stage: stage, category: category, contextState: journalContextState(ctx)}
	return &AppendError{Err: safeError(err), Uncertain: uncertain, diagnostic: diagnostic}
}
func observeAppendFailure(ctx, root context.Context, err error) appendDiagnostic {
	if err == nil {
		return appendDiagnostic{contextState: journalContextState(ctx), recorderContextState: journalContextState(root)}
	}
	var failure *AppendError
	if errors.As(err, &failure) && failure.diagnostic.stage != "" {
		out := failure.diagnostic
		out.recorderContextState = journalContextState(root)
		return out
	}
	category := journalErrorCategory(err)
	return appendDiagnostic{stage: "store_unknown", category: category, contextState: journalContextState(ctx), recorderContextState: journalContextState(root)}
}
func invalidACKDiagnostic(observed appendDiagnostic) appendDiagnostic {
	observed.stage, observed.category = "ack", "invalid_ack"
	return observed
}
func (r *Recorder) recordAppendFailureLocked(diagnostic appendDiagnostic) {
	if !r.diagnostic.set {
		r.diagnostic.first = diagnostic
		r.diagnostic.set = true
	}
}

// CIJournalFirstFailure returns a fresh closed projection under the same mutex
// as WriteFailures. It neither changes counters nor interprets a retry as clean.
func (r *Recorder) CIJournalFirstFailure() CIJournalFailure {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := CIJournalFailure{Event: "owned_ci_first_journal_failure", Stage: "none", ErrorCategory: "none", ContextState: "unknown", RecorderContextState: "unknown", ConnectPhase: "none"}
	if !r.diagnostic.set {
		return out
	}
	out.Stage, out.ErrorCategory = "unknown", "unknown"
	d := r.diagnostic.first
	switch d.stage {
	case "validation", "connect", "begin", "schema", "report", "event_insert", "event_read", "commit", "ack", "store_unknown":
		out.Stage = d.stage
	}
	switch d.category {
	case "canceled", "deadline_exceeded", "postgres", "network", "invalid_ack", "validation", "conflict", "other":
		out.ErrorCategory = d.category
	}
	switch d.contextState {
	case "active", "canceled", "deadline_exceeded":
		out.ContextState = d.contextState
	}
	switch d.recorderContextState {
	case "active", "canceled", "deadline_exceeded":
		out.RecorderContextState = d.recorderContextState
	}
	if out.Stage == "connect" {
		out.ConnectPhase = "unknown"
		switch d.connectPhase {
		case "admission", "native":
			out.ConnectPhase = d.connectPhase
		}
	}
	return out
}

func connectJournal(ctx context.Context, config *pgx.ConnConfig) (*dbadmission.Conn, error, string) {
	return dbadmission.ConnectConfigWithCIPhase(ctx, config)
}
func newStoreConnectError(ctx context.Context, err error, phase string) *AppendError {
	out := newStoreAppendError(ctx, "connect", err, false)
	switch phase {
	case "admission", "native":
		out.diagnostic.connectPhase = phase
	}
	return out
}
