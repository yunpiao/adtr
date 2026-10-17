//go:build adtr_ci_dbadmission

package dbadmission

// These compile-time categories describe the first invalidating branch only.
// They never contain an error string, connection identity or SQL text.
type invalidationReason string

const (
	invalidCapacity         invalidationReason = "capacity"
	invalidConnectFailed    invalidationReason = "connect_failed"
	invalidConnectState     invalidationReason = "invalid_connect_state"
	invalidDuplicateBackend invalidationReason = "duplicate_backend"
	invalidCloseState       invalidationReason = "invalid_close_state"
	invalidUncleanClose     invalidationReason = "unclean_close"
	invalidTargetClosed     invalidationReason = "target_closed"
	invalidTargetClosing    invalidationReason = "target_closing"
	invalidTargetRevoked    invalidationReason = "target_revoked"
	invalidWaitCancelled    invalidationReason = "wait_cancelled"
	invalidTransport        invalidationReason = "transport"
	invalidRecordCapacity   invalidationReason = "record_capacity"
	invalidAbort            invalidationReason = "abort"
	invalidExplicit         invalidationReason = "explicit_invalidation"
)

func (g *lifecycleGate) diagnosticPhaseLocked() string {
	if g.seal == nil {
		return "open"
	}
	if g.seal.released {
		return "released"
	}
	if g.closing && g.pending == 0 && g.open == 1 && g.seal.target.state == 'o' && g.seal.target.registration == g.seal.registration {
		return "sealed"
	}
	return "draining"
}

func (g *lifecycleGate) invalidateBecauseLocked(reason invalidationReason) {
	phase := g.diagnosticPhaseLocked()
	// Preserve the original sticky-before-mutation linearization. Diagnostics
	// are recorded only after the invalid witness and original invalid flag.
	g.markInvalidLocked()
	if g.firstInvalidReason == "" {
		g.firstInvalidReason = reason
		g.firstInvalidPhase = phase
	}
}

type applicationDiagnostic struct {
	Version  int                `json:"version"`
	Epoch    uint64             `json:"epoch"`
	Seq      uint64             `json:"seq"`
	Kind     string             `json:"kind"`
	Reason   invalidationReason `json:"reason"`
	Phase    string             `json:"phase"`
	Pending  int                `json:"pending"`
	Open     int                `json:"open"`
	Total    uint64             `json:"total"`
	Retained int                `json:"retained"`
}

func (g *lifecycleGate) applicationDiagnostic(seq uint64) applicationDiagnostic {
	g.mu.Lock()
	defer g.mu.Unlock()
	reason, phase := g.firstInvalidReason, g.firstInvalidPhase
	if reason == "" {
		reason, phase = "none", g.diagnosticPhaseLocked()
		if g.invalid {
			reason = invalidExplicit
		}
	}
	return applicationDiagnostic{2, 1, seq, "diagnostic", reason, phase, g.pending, g.open, g.total, len(g.tickets)}
}
