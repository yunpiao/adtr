//go:build adtr_ci_dbadmission

package dbadmission

import (
	"encoding/json"
	"os"
	"time"
)

// Caller holds g.mu. Contamination is permanent, including during native cleanup.
func (t *lifecycleTicket) contaminateRecoveryLocked() {
	t.recoveryContaminated = true
	if t.state == 'q' || t.state == 'r' || t.gate.recoveryFinalized {
		t.gate.invalidateBecauseLocked(invalidUncleanClose)
		t.gate.notifyLocked()
	}
}

func (t *lifecycleTicket) canAwaitRecoveryLocked(observation closeObservation) bool {
	g := t.gate
	if g.invalid || g.recoveryFinalized || g.seal == nil || !g.seal.released || !g.seal.recoveryAuthorized ||
		!t.recoveryAuthorized || t.recoveryEpoch != g.seal.epoch || g.seal.target == t || t.registration != nil || t.recoveryEverRegistered ||
		t.recoveryContaminated || t.operationInFlight != 0 || t.conn == nil ||
		!observation.alreadyClosed || observation.closeFailed || g.recoveryQualified == ^uint64(0) {
		return false
	}
	first := t.firstOperationError
	return first != nil && first == t.firstNativeClosedError && first.Operation == "statement" &&
		first.ContextState == "canceled" && first.ErrorCategory == "canceled" && first.ErrorMatchesContext &&
		first.NativeClosedAtEnd && first.ConnectionMatched && first.RootContextState == "canceled" &&
		first.StartPhase == "released" && first.EndPhase == "released" && diagnosticContextState(g.workerContext) == "canceled"
}

type recoveryCleanupDiagnostic struct {
	Event             string `json:"event"`
	CaptureAuthorized bool   `json:"captureAuthorized"`
	Released          bool   `json:"released"`
	Qualified         uint64 `json:"qualified"`
	Completed         uint64 `json:"completed"`
	Pending           int    `json:"pending"`
	Open              int    `json:"open"`
	ConnectPending    int    `json:"connectPending"`
	Failed            int    `json:"failed"`
	Settled           bool   `json:"settled"`
}

// Called only after producer and journal shutdown, outside registry mutex. This
// single budget covers all pending native lifetimes; the parent's Wait ceiling
// and capture deadline are unchanged.
func (g *lifecycleGate) finishRecoveryCleanup() {
	deadlineAt := time.Now().Add(2 * time.Second)
	deadline := time.NewTimer(time.Until(deadlineAt))
	defer deadline.Stop()
	g.mu.Lock()
	pending := make([]*lifecycleTicket, 0)
	for _, t := range g.tickets {
		if t.state == 'q' {
			pending = append(pending, t)
		}
	}
	g.mu.Unlock()
	timedOut := false
	for _, t := range pending {
		if !time.Now().Before(deadlineAt) {
			timedOut = true
		}
		if !timedOut {
			select {
			case <-t.cleanupDone:
			case <-deadline.C:
				timedOut = true
			}
		}
		if !time.Now().Before(deadlineAt) {
			timedOut = true
		}
		g.mu.Lock()
		complete := false
		if !timedOut && t.cleanupDone != nil {
			select {
			case <-t.cleanupDone:
				complete = true
			default:
			}
		}
		if !complete || g.tickets[t.generation] != t || t.state != 'q' || t.recoveryContaminated || t.operationInFlight != 0 ||
			t.conn == nil || !t.conn.raw.IsClosed() || g.invalid {
			g.invalidateBecauseLocked(invalidUncleanClose)
		} else {
			t.state = 'r'
			g.recoveryCompleted++
		}
		g.notifyLocked()
		g.mu.Unlock()
	}
	g.mu.Lock()
	// No new qualification or callbacks may change the final success snapshot.
	g.recoveryFinalized = true
	if g.pending != 0 || g.open != 0 {
		g.invalidateBecauseLocked(invalidCloseState)
	}
	for _, t := range g.tickets {
		if t.state == 'p' || t.state == 'o' || t.state == 'x' || t.state == 'q' || t.operationInFlight != 0 {
			g.invalidateBecauseLocked(invalidCloseState)
		}
	}
	if g.recoveryCompleted != g.recoveryQualified {
		g.invalidateBecauseLocked(invalidUncleanClose)
	}
	g.notifyLocked()
	g.mu.Unlock()
}

func (g *lifecycleGate) writeRecoveryCleanupDiagnostic() {
	g.mu.Lock()
	d := recoveryCleanupDiagnostic{Event: "owned_ci_recovery_cleanup", Qualified: g.recoveryQualified, Completed: g.recoveryCompleted, Open: g.open, ConnectPending: g.pending}
	if g.seal != nil {
		d.CaptureAuthorized = g.seal.recoveryAuthorized
		d.Released = g.seal.released
	}
	active := false
	for _, t := range g.tickets {
		if t.state == 'q' {
			d.Pending++
		}
		if t.operationInFlight != 0 {
			active = true
		}
	}
	if g.invalid {
		d.Failed = 1
	}
	d.Settled = g.recoveryFinalized && !active && d.Pending == 0 && d.Open == 0 && d.ConnectPending == 0 && d.Qualified == d.Completed && d.Failed == 0
	g.mu.Unlock()
	data, err := json.Marshal(d)
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}
