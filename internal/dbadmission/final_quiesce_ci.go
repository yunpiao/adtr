//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"time"
)

// Final shutdown is a one-shot admission barrier, separate from capture sealing.
// It never cancels an admitted operation to manufacture a drained registry.
type finalQuiesce struct {
	state    string
	deadline time.Time
	stop     context.CancelFunc
}

func ConfigureCIShutdown(stop context.CancelFunc) {
	coordinator.Lock()
	defer coordinator.Unlock()
	g := coordinator.gate
	if g == nil || !g.application || stop == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalQuiesce.stop != nil {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return
	}
	g.finalQuiesce.stop = stop
}

func (g *lifecycleGate) finalDrainedLocked() bool {
	if g.invalid || g.pending != 0 || g.open != 0 {
		return false
	}
	for _, t := range g.tickets {
		if t.state == 'p' || t.state == 'o' || t.state == 'x' || t.state == 'q' || t.operationInFlight != 0 {
			return false
		}
	}
	return true
}

func (g *lifecycleGate) prepareFinalQuiesce(budgetMillis int64) error {
	g.mu.Lock()
	if budgetMillis <= 0 || budgetMillis > 30000 || g.finalQuiesce.state != "" || g.finalQuiesce.stop == nil || g.workerContext == nil || g.workerContext.Err() != nil || g.seal == nil || !g.seal.released || !g.seal.recoveryAuthorized || g.invalid {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return errSealProtocol
	}
	g.finalQuiesce.state = "parking"
	g.finalQuiesce.deadline = time.Now().Add(time.Duration(budgetMillis) * time.Millisecond)
	g.finalParked = true
	g.notifyLocked()
	for {
		if g.invalid || g.workerContext.Err() != nil || !time.Now().Before(g.finalQuiesce.deadline) {
			g.finalQuiesce.state = "failed"
			g.invalidateBecauseLocked(invalidExplicit)
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return errSealProtocol
		}
		if g.finalDrainedLocked() {
			g.finalQuiesce.state = "drained"
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		remaining := time.Until(g.finalQuiesce.deadline)
		g.mu.Unlock()
		timer := time.NewTimer(remaining)
		select {
		case <-changed:
		case <-timer.C:
		case <-g.workerContext.Done():
		}
		timer.Stop()
		g.mu.Lock()
	}
}

// The caller owns finalTransportMu through the bounded ACK write. The same lock
// protects controller cleanup's Close, so a quick normal exit cannot cut the ACK.
func (g *lifecycleGate) cancelFinalQuiesce() error {
	g.mu.Lock()
	if g.finalQuiesce.state != "drained" || !g.finalParked || g.finalQuiesce.stop == nil || g.workerContext == nil || g.workerContext.Err() != nil || !time.Now().Before(g.finalQuiesce.deadline) || !g.finalDrainedLocked() {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return errSealProtocol
	}
	g.finalQuiesce.state = "cancelling"
	stop := g.finalQuiesce.stop
	g.mu.Unlock()
	// The installed logical root CancelFunc returns only after synchronous child
	// cancellation propagation. Observing root.Done alone does not supply this.
	stop()
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if g.invalid || g.workerContext.Err() != context.Canceled || !time.Now().Before(g.finalQuiesce.deadline) {
		g.finalQuiesce.state = "failed"
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return errSealProtocol
	}
	g.finalQuiesce.state = "cancelled"
	g.notifyLocked()
	return nil
}

func (g *lifecycleGate) abortFinalQuiesce() {
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if !g.finalParked {
		return
	}
	g.invalidateBecauseLocked(invalidExplicit)
	g.finalQuiesce.state = "failed"
	g.finalParked = false
	g.notifyLocked()
}

func (g *lifecycleGate) completeFinalQuiesce() error {
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if g.finalQuiesce.state != "cancelled" || !g.finalParked || g.invalid || !g.finalDrainedLocked() || !time.Now().Before(g.finalQuiesce.deadline) {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return errSealProtocol
	}
	g.finalQuiesce.state = "complete"
	g.finalParked = false
	g.notifyLocked()
	return nil
}
