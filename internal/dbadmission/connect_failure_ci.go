//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"

	"github.com/jackc/pgx/v5/pgconn"
)

// These are observations of one failed Connect call, not a cleanup exemption.
// No error, address, SQLSTATE, configuration or connection identity is retained.
type connectFailureDiagnostic struct {
	Event                   string `json:"event"`
	BirthPhase              string `json:"birthPhase"`
	BirthCaptureEnded       bool   `json:"birthCaptureEnded"`
	BirthRecoveryAuthorized bool   `json:"birthRecoveryAuthorized"`
	FailurePhase            string `json:"failurePhase"`
	ContextState            string `json:"contextState"`
	ErrorCategory           string `json:"errorCategory"`
	ErrorMatchesContext     bool   `json:"errorMatchesContext"`
	RootContextState        string `json:"rootContextState"`
	RawNil                  bool   `json:"rawNil"`
}

func observeLifecycleConnectFailure(ctx context.Context, permit lifecyclePermit, err error, rawNil bool) {
	if t, ok := permit.(*lifecycleTicket); ok {
		t.failedObserved(ctx, err, rawNil)
	} else if permit != nil {
		permit.failed()
	}
}

func diagnosticConnectErrorCategory(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError != nil && len(pgError.Code) == 5 && pgError.Code[:2] == "28" {
		return "authentication"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return "network_error"
	}
	return "other"
}

func (t *lifecycleTicket) failedObserved(ctx context.Context, err error, rawNil bool) {
	// Classify the returned error before sampling cancellation. A later cancel
	// must not relabel an unrelated network/authentication error as cancellation.
	category := diagnosticConnectErrorCategory(err)
	contextState := diagnosticContextState(ctx)
	matches := (contextState == "canceled" && errors.Is(err, context.Canceled)) || (contextState == "deadline_exceeded" && errors.Is(err, context.DeadlineExceeded))
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	first := g.firstInvalidReason == ""
	g.invalidateBecauseLocked(invalidConnectFailed)
	if first {
		g.firstConnectFailure = &connectFailureDiagnostic{
			"owned_ci_first_connect_failure", t.birthPhase, t.birthCaptureEnded, t.recoveryAuthorized,
			g.firstInvalidPhase, contextState, category, matches, diagnosticContextState(g.workerContext), rawNil,
		}
	}
	if t.state == 'p' {
		g.pending--
	}
	t.state = 'f'
	g.notifyLocked()
}

func (g *lifecycleGate) connectFailureDiagnostic() *connectFailureDiagnostic {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.firstInvalidReason != invalidConnectFailed || g.firstConnectFailure == nil {
		return nil
	}
	d := *g.firstConnectFailure
	if d.Event != "owned_ci_first_connect_failure" || !validOperationPhase(d.BirthPhase) || !validOperationPhase(d.FailurePhase) || !validOperationContext(d.ContextState) || !validOperationContext(d.RootContextState) {
		return nil
	}
	switch d.ErrorCategory {
	case "none", "canceled", "deadline_exceeded", "authentication", "network_error", "other":
	default:
		return nil
	}
	return &d
}

func (g *lifecycleGate) writeConnectFailureDiagnostic() {
	d := g.connectFailureDiagnostic()
	if d == nil {
		return
	}
	data, err := json.Marshal(d)
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}
