//go:build adtr_ci_dbadmission

package operationallogs

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestActivityCandidateJournalFailureClassification(t *testing.T) {
	network := &net.OpError{Op: "private-operation", Net: "private-network", Err: errors.New("private-detail")}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"cancel", context.Canceled, "canceled"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
		{"postgres", &pgconn.PgError{Code: "private-code", Message: "private-detail"}, "postgres"},
		{"network", network, "network"},
		{"validation", problem(400, "invalid_observation"), "validation"},
		{"conflict", problem(409, "event_conflict"), "conflict"},
		{"other", errors.New("private-detail"), "other"},
		{"joined", errors.Join(network, context.Canceled), "canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := newStoreAppendError(context.Background(), "report", tc.err, true)
			if failure.Uncertain != true || !reflect.DeepEqual(failure.Err, safeError(tc.err)) {
				t.Fatal("append semantics changed")
			}
			d := observeAppendFailure(context.Background(), context.Background(), failure)
			if d.category != tc.want || d.stage != "report" || d.contextState != "active" || d.recorderContextState != "active" {
				t.Fatal("classification mismatch")
			}
			r := &Recorder{}
			r.recordAppendFailureLocked(d)
			raw, err := json.Marshal(r.CIJournalFirstFailure())
			var fields map[string]json.RawMessage
			if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 5 || strings.Contains(string(raw), "private-") {
				t.Fatal("diagnostic is not closed")
			}
		})
	}
}

func TestActivityCandidateJournalFailureSnapshotsBeforeCancellation(t *testing.T) {
	attempt, cancel := context.WithCancel(context.Background())
	root, stop := context.WithCancel(context.Background())
	defer stop()
	d := observeAppendFailure(attempt, root, errors.New("private-detail"))
	cancel()
	stop()
	if d.contextState != "active" || d.recorderContextState != "active" || d.category != "other" {
		t.Fatal("late cancellation rewrote observation")
	}
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	d = observeAppendFailure(expired, root, context.DeadlineExceeded)
	if d.contextState != "deadline_exceeded" || d.recorderContextState != "canceled" {
		t.Fatal("context snapshot mismatch")
	}
	ack := invalidACKDiagnostic(d)
	if ack.stage != "ack" || ack.category != "invalid_ack" || ack.contextState != d.contextState || ack.recorderContextState != d.recorderContextState {
		t.Fatal("ack snapshot changed")
	}
}

func TestActivityCandidateJournalFailureClosedStagesAndFirstWins(t *testing.T) {
	for _, stage := range []string{"validation", "connect", "begin", "schema", "report", "event_insert", "event_read", "commit", "ack", "store_unknown"} {
		r := &Recorder{}
		r.recordAppendFailureLocked(appendDiagnostic{stage, "other", "active", "active"})
		r.recordAppendFailureLocked(appendDiagnostic{"commit", "canceled", "canceled", "canceled"})
		if got := r.CIJournalFirstFailure(); got.Stage != stage || got.ErrorCategory != "other" {
			t.Fatal("first failure replaced")
		}
	}
	r := &Recorder{}
	if got := r.CIJournalFirstFailure(); got.Stage != "none" || got.ErrorCategory != "none" {
		t.Fatal("missing empty observation")
	}
	r.recordAppendFailureLocked(appendDiagnostic{"private-stage", "private-category", "private-context", "private-root"})
	got := r.CIJournalFirstFailure()
	if got.Stage != "unknown" || got.ErrorCategory != "unknown" || got.ContextState != "unknown" || got.RecorderContextState != "unknown" {
		t.Fatal("unknown value escaped")
	}
}

func TestActivityCandidateJournalFailureRetryCountersRemain(t *testing.T) {
	calls := 0
	r := newTestRecorder(t, appendFunc(func(ctx context.Context, e Event, o RecorderObservation) (Event, error) {
		calls++
		if calls == 1 {
			return Event{}, newStoreAppendError(ctx, "report", errors.New("private-detail"), false)
		}
		return recorded(e), nil
	}))
	if !r.Emit(ServiceStartRequested, Attempted, NoReason) {
		t.Fatal("emit rejected")
	}
	closeRecorder(t, r)
	stats := r.Stats()
	if calls != 2 || stats.Accepted != 1 || stats.Acknowledged != 1 || stats.WriteFailures != 1 || stats.Unacknowledged != 0 || stats.Abandoned != 0 {
		t.Fatal("retry counters changed")
	}
	d := r.CIJournalFirstFailure()
	if d.Stage != "report" || d.ErrorCategory != "other" || d.ContextState != "active" || d.RecorderContextState != "active" {
		t.Fatal("first attempt lost or own cancel polluted snapshot")
	}
}

func TestActivityCandidateJournalInvalidACKStillFailsAndRetries(t *testing.T) {
	calls := 0
	r := newTestRecorder(t, appendFunc(func(_ context.Context, e Event, _ RecorderObservation) (Event, error) {
		calls++
		if calls == 1 {
			return Event{}, nil
		}
		return recorded(e), nil
	}))
	if !r.Emit(ServiceStartRequested, Attempted, NoReason) {
		t.Fatal("emit rejected")
	}
	closeRecorder(t, r)
	if r.Stats().WriteFailures != 1 || r.Stats().Acknowledged != 1 || calls != 2 {
		t.Fatal("invalid ACK semantics changed")
	}
	d := r.CIJournalFirstFailure()
	if d.Stage != "ack" || d.ErrorCategory != "invalid_ack" || d.ContextState != "active" {
		t.Fatal("invalid ACK evidence missing")
	}
}
