//go:build adtr_ci_dbadmission

package dbadmission

import "time"

// Queued parking only prevents physical database work from spanning SIGSTOP.
// It does not renew producer contexts or park producers before they create them.
func (g *lifecycleGate) prepareQueuedSelection(kind string, budgetMillis int64) error {
	if budgetMillis <= 0 || budgetMillis > 2000 {
		return errSealProtocol
	}
	deadline := time.Now().Add(time.Duration(budgetMillis) * time.Millisecond)
	if err := g.prepareActivitySelection(kind); err != nil {
		return err
	}
	g.mu.Lock()
	if g.queuedState != "" || g.invalid || g.workerContext == nil || g.workerContext.Err() != nil {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return errSealProtocol
	}
	g.queuedState = "parking"
	g.queuedParked = true
	g.notifyLocked()
	for {
		if g.invalid || g.queuedState != "parking" || g.workerContext.Err() != nil || !time.Now().Before(deadline) {
			g.invalidateBecauseLocked(invalidExplicit)
			g.queuedState = "failed"
			g.queuedParked = false
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return errSealProtocol
		}
		if g.finalDrainedLocked() {
			g.queuedState = "drained"
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		remaining := time.Until(deadline)
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

func (g *lifecycleGate) completeQueuedSelection() error {
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if g.queuedState != "configured" || !g.queuedParked || g.selection != 2 || g.invalid || !g.finalDrainedLocked() {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return errSealProtocol
	}
	g.queuedState = "complete"
	g.queuedParked = false
	g.notifyLocked()
	return nil
}

func (g *lifecycleGate) abortQueuedSelection() {
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if !g.queuedParked {
		return
	}
	g.invalidateBecauseLocked(invalidExplicit)
	g.queuedState = "failed"
	g.queuedParked = false
	g.notifyLocked()
}
