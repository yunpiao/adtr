//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func connectFailureNetworkError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("private socket failure")}
}

func TestConnectFailureDiagnosticClassifiesOnlyTypedErrorEvidence(t *testing.T) {
	network := connectFailureNetworkError()
	authentication := &pgconn.PgError{Code: "28P01", Message: "private authentication details"}
	cases := []struct {
		name, category string
		err            error
	}{
		{"nil", "none", nil},
		{"canceled", "canceled", context.Canceled},
		{"wrapped_canceled", "canceled", fmt.Errorf("private wrapper: %w", context.Canceled)},
		{"deadline", "deadline_exceeded", context.DeadlineExceeded},
		{"wrapped_deadline", "deadline_exceeded", fmt.Errorf("private wrapper: %w", context.DeadlineExceeded)},
		{"network", "network_error", network},
		{"wrapped_network", "network_error", fmt.Errorf("private wrapper: %w", network)},
		{"network_timeout", "network_error", &net.DNSError{Err: "private DNS failure", IsTimeout: true}},
		{"network_wrapping_canceled", "canceled", &net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled}},
		{"network_wrapping_deadline", "deadline_exceeded", &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}},
		{"password_authentication", "authentication", authentication},
		{"authorization_specification", "authentication", &pgconn.PgError{Code: "28000", Message: "private role details"}},
		{"wrapped_authentication", "authentication", fmt.Errorf("private wrapper: %w", authentication)},
		{"plain_cancellation_text", "other", errors.New("context canceled")},
		{"plain_authentication_text", "other", errors.New("password authentication failed")},
		{"server_query_canceled", "other", &pgconn.PgError{Code: "57014"}},
		{"malformed_auth_code", "other", &pgconn.PgError{Code: "28"}},
		{"other", "other", errors.New("private unspecified error")},
		// Joined errors establish category precedence, not a unique root cause.
		{"joined_cancellation_network", "canceled", errors.Join(context.Canceled, network)},
		{"joined_network_cancellation", "canceled", errors.Join(network, context.Canceled)},
		{"joined_cancellation_authentication", "canceled", errors.Join(context.Canceled, authentication)},
		{"joined_authentication_cancellation", "canceled", errors.Join(authentication, context.Canceled)},
		{"joined_deadline_network", "deadline_exceeded", errors.Join(context.DeadlineExceeded, network)},
		{"joined_authentication_network", "authentication", errors.Join(authentication, network)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := diagnosticConnectErrorCategory(tc.err); got != tc.category {
				t.Fatalf("category %q, want %q", got, tc.category)
			}
		})
	}
}

func TestConnectFailureDiagnosticKeepsErrorContextAndRawFactsSeparate(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	cases := []struct {
		name, state, category, rootState string
		ctx, root                        context.Context
		err                              error
		matches, rawNil                  bool
	}{
		{"cancellation", "canceled", "canceled", "canceled", canceled, canceled, fmt.Errorf("private wrapper: %w", context.Canceled), true, true},
		{"network_with_canceled_context", "canceled", "network_error", "canceled", canceled, canceled, connectFailureNetworkError(), false, true},
		{"deadline", "deadline_exceeded", "deadline_exceeded", "deadline_exceeded", deadline, deadline, context.DeadlineExceeded, true, true},
		{"network_timeout_with_deadline_context", "deadline_exceeded", "network_error", "deadline_exceeded", deadline, deadline, &net.DNSError{IsTimeout: true}, false, true},
		{"active_context_cancellation_error", "active", "canceled", "active", context.Background(), context.Background(), context.Canceled, false, true},
		{"canceled_context_deadline_error", "canceled", "deadline_exceeded", "active", canceled, context.Background(), context.DeadlineExceeded, false, true},
		{"authentication_with_canceled_context", "canceled", "authentication", "canceled", canceled, canceled, &pgconn.PgError{Code: "28P01"}, false, true},
		{"unknown_context", "unknown", "network_error", "unknown", nil, nil, connectFailureNetworkError(), false, true},
		{"nil_error_nil_raw", "active", "none", "active", context.Background(), context.Background(), nil, false, true},
		{"error_with_non_nil_raw", "active", "other", "active", context.Background(), context.Background(), errors.New("private connector failure"), false, false},
		{"joined_network_and_cancellation", "canceled", "canceled", "canceled", canceled, canceled, errors.Join(connectFailureNetworkError(), context.Canceled), true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &lifecycleGate{workerContext: tc.root, witness: &diagnosticWitness{}}
			ticket, err := g.acquire(context.Background())
			if err != nil {
				t.Fatal("fixture admission failed")
			}
			observeLifecycleConnectFailure(tc.ctx, ticket, tc.err, tc.rawNil)
			d := g.connectFailureDiagnostic()
			if d == nil || d.Event != "owned_ci_first_connect_failure" || d.BirthPhase != "open" || d.BirthCaptureEnded || d.BirthRecoveryAuthorized || d.FailurePhase != "open" || d.ContextState != tc.state || d.ErrorCategory != tc.category || d.ErrorMatchesContext != tc.matches || d.RootContextState != tc.rootState || d.RawNil != tc.rawNil {
				t.Fatal("connector evidence was relabeled or omitted")
			}
			if !g.invalid || g.firstInvalidReason != invalidConnectFailed || ticket.state != 'f' || g.pending != 0 || g.open != 0 || g.recoveryQualified != 0 || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("connect diagnostic changed original fatal lifecycle accounting")
			}
		})
	}
}

// Inspection changes context state deterministically. Sampling context before
// classifying the original error would produce an incorrect "active" fact.
type connectFailureCancelOnInspectError struct{ cancel context.CancelFunc }

func (e *connectFailureCancelOnInspectError) Error() string { return "private network failure" }
func (e *connectFailureCancelOnInspectError) Is(error) bool { e.cancel(); return false }
func (*connectFailureCancelOnInspectError) Timeout() bool   { return false }
func (*connectFailureCancelOnInspectError) Temporary() bool { return false }

func TestConnectFailureDiagnosticClassifiesErrorBeforeSamplingContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := &lifecycleGate{workerContext: ctx}
	ticket, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("fixture admission failed")
	}
	ticket.failedObserved(ctx, &connectFailureCancelOnInspectError{cancel: cancel}, true)
	d := g.connectFailureDiagnostic()
	if d == nil || d.ContextState != "canceled" || d.RootContextState != "canceled" || d.ErrorCategory != "network_error" || d.ErrorMatchesContext {
		t.Fatal("cancellation sampling changed unrelated error identity or ordering")
	}
}

func TestConnectFailureLateCancellationAndFailuresNeverRewriteFirstObservation(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := &lifecycleGate{workerContext: root, witness: &diagnosticWitness{}}
	first, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("first fixture admission failed")
	}
	second, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("second fixture admission failed")
	}
	first.failedObserved(root, connectFailureNetworkError(), true)
	original := g.firstConnectFailure
	if original == nil {
		t.Fatal("first connect observation missing")
	}
	before := *original
	cancel()
	second.failedObserved(root, context.Canceled, false)
	first.failedObserved(root, context.DeadlineExceeded, false)
	if g.firstConnectFailure != original || *g.firstConnectFailure != before || g.pending != 0 || g.firstInvalidReason != invalidConnectFailed || !g.invalid {
		t.Fatal("later cancellation or repeated failure rewrote first observation/accounting")
	}
	if before.ContextState != "active" || before.RootContextState != "active" || before.ErrorCategory != "network_error" || before.ErrorMatchesContext || !before.RawNil {
		t.Fatal("first network failure inherited later cancellation facts")
	}
}

func TestConnectFailureEarlierInvalidationNeverAcquiresLaterDetails(t *testing.T) {
	for _, prior := range []string{"transport", "explicit", "unobserved_connect_failure"} {
		t.Run(prior, func(t *testing.T) {
			g := &lifecycleGate{witness: &diagnosticWitness{}}
			ticket, err := g.acquire(context.Background())
			if err != nil {
				t.Fatal("fixture admission failed")
			}
			switch prior {
			case "transport":
				g.invalidateTransport()
			case "explicit":
				g.invalidate()
			case "unobserved_connect_failure":
				ticket.failed()
			}
			reason := g.firstInvalidReason
			ticket.failedObserved(context.Background(), connectFailureNetworkError(), true)
			if !g.invalid || g.firstInvalidReason != reason || g.firstConnectFailure != nil || g.connectFailureDiagnostic() != nil || ticket.state != 'f' || g.pending != 0 {
				t.Fatal("later connect failure invented details for an earlier invalidation")
			}
		})
	}
}

func TestConnectFailureStickyPrecedesDiagnosticAndTicketMutation(t *testing.T) {
	g := &lifecycleGate{}
	ticket, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("fixture admission failed")
	}
	witness := &diagnosticWitness{before: func() {
		if g.invalid || g.firstInvalidReason != "" || g.firstConnectFailure != nil || ticket.state != 'p' || g.pending != 1 {
			t.Fatal("connect observation or lifecycle mutation preceded sticky store")
		}
	}}
	g.witness = witness
	ticket.failedObserved(context.Background(), connectFailureNetworkError(), true)
	if !g.invalid || witness.stores != 1 || g.firstConnectFailure == nil || ticket.state != 'f' || g.pending != 0 {
		t.Fatal("connect failure did not commit original sticky transition")
	}
}

func TestConnectFailureRecordsImmutableBirthQualification(t *testing.T) {
	for _, kind := range []string{"before_release", "released_unauthorized", "capture_observed_only", "released_authorized"} {
		t.Run(kind, func(t *testing.T) {
			g := &lifecycleGate{}
			wantPhase, wantCapture, wantAuthorized := "open", false, false
			if kind != "before_release" {
				wantPhase = "released"
				wantCapture = kind == "capture_observed_only" || kind == "released_authorized"
				wantAuthorized = kind == "released_authorized"
				// Synthetic cached birth facts test diagnostic fidelity only;
				// capture-end protocol authorization has separate pipe tests.
				g.seal = &sealAttempt{epoch: 1, released: true, captureEnded: wantCapture, recoveryAuthorized: wantAuthorized}
			}
			ticket, err := g.acquire(context.Background())
			if err != nil {
				t.Fatal("fixture admission failed")
			}
			g.seal = &sealAttempt{epoch: 1, released: true, captureEnded: !wantCapture, recoveryAuthorized: !wantAuthorized}
			ticket.failedObserved(context.Background(), connectFailureNetworkError(), true)
			d := g.connectFailureDiagnostic()
			if d == nil || d.BirthPhase != wantPhase || d.BirthCaptureEnded != wantCapture || d.BirthRecoveryAuthorized != wantAuthorized || d.FailurePhase != "released" || !g.invalid || g.recoveryQualified != 0 {
				t.Fatal("failure-time seal facts rewrote birth evidence or excused failure")
			}
		})
	}
}

func TestConnectFailureConnectWithPreservesErrorIdentityAndNonNilRawFact(t *testing.T) {
	for _, rawNil := range []bool{false, true} {
		t.Run(fmt.Sprint(rawNil), func(t *testing.T) {
			g := installTestGate(t)
			root, cancel := context.WithCancel(context.Background())
			defer cancel()
			g.workerContext = root
			want := connectFailureNetworkError()
			calls := 0
			conn, err := connectWith(root, nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
				calls++
				cancel()
				if rawNil {
					return nil, want
				}
				return &pgx.Conn{}, want // No methods are called on the error result.
			})
			d := g.connectFailureDiagnostic()
			if conn != nil || err != want || calls != 1 || d == nil || d.RawNil != rawNil || d.ErrorCategory != "network_error" || d.ContextState != "canceled" || d.RootContextState != "canceled" || d.ErrorMatchesContext || !g.invalid || g.pending != 0 {
				t.Fatal("observation changed original connector error or native-result fact")
			}
		})
	}
}

func TestConnectFailureDiagnosticClosedSchemaCopyAndUnknownRejection(t *testing.T) {
	g := &lifecycleGate{}
	ticket, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("fixture admission failed")
	}
	ticket.failedObserved(context.Background(), &pgconn.PgError{Code: "28P01", Message: "private host user password details"}, true)
	d := g.connectFailureDiagnostic()
	if d == nil {
		t.Fatal("diagnostic missing")
	}
	data, err := json.Marshal(d)
	if err != nil || strings.Contains(string(data), "private") || strings.Contains(string(data), "28P01") {
		t.Fatal("raw authentication or connection data retained")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 10 {
		t.Fatal("connect diagnostic field set changed")
	}
	for _, field := range []string{"event", "birthPhase", "birthCaptureEnded", "birthRecoveryAuthorized", "failurePhase", "contextState", "errorCategory", "errorMatchesContext", "rootContextState", "rawNil"} {
		if _, ok := fields[field]; !ok {
			t.Fatal("closed diagnostic field missing")
		}
	}
	d.ContextState = "private"
	if g.connectFailureDiagnostic().ContextState != "active" {
		t.Fatal("snapshot mutation altered retained first observation")
	}
	original := *g.firstConnectFailure
	for _, edit := range []func(*connectFailureDiagnostic){
		func(d *connectFailureDiagnostic) { d.Event = "private" },
		func(d *connectFailureDiagnostic) { d.BirthPhase = "private" },
		func(d *connectFailureDiagnostic) { d.FailurePhase = "private" },
		func(d *connectFailureDiagnostic) { d.ContextState = "private" },
		func(d *connectFailureDiagnostic) { d.RootContextState = "private" },
		func(d *connectFailureDiagnostic) { d.ErrorCategory = "private" },
	} {
		*g.firstConnectFailure = original
		edit(g.firstConnectFailure)
		if g.connectFailureDiagnostic() != nil {
			t.Fatal("unknown diagnostic category escaped closed-schema guard")
		}
	}
	*g.firstConnectFailure = original
	g.firstInvalidReason = invalidTransport
	if g.connectFailureDiagnostic() != nil {
		t.Fatal("connect details emitted for a different first cause")
	}
}
