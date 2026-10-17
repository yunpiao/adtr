//go:build adtr_ci_dbadmission

package dbadmission

import (
	"errors"
	"sync"
)

var errSealProtocol = errors.New("database seal unavailable")

// sealAttempt is installed only by package tests in this verification slice.
// LOCAL_SEALED is a local lifetime assertion, never PostgreSQL eligibility.
type sealAttempt struct {
	epoch          uint64
	target         *lifecycleTicket
	registration   *activityRegistration
	released       bool
	publishInvalid func()
	publishOnce    sync.Once
	publishDone    chan struct{}
}

type sealSnapshot struct {
	generation uint64
	identity   ActivityIdentity
	backend    uint32
}

func (g *lifecycleGate) candidates() []sealSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	var result []sealSnapshot
	if g.invalid {
		return result
	}
	for _, t := range g.tickets {
		if t.state == 'o' && t.registration != nil {
			result = append(result, sealSnapshot{t.registration.generation, t.registration.identity, t.backend})
		}
	}
	return result
}

func (g *lifecycleGate) registerActivityRow(conn *Conn, identity ActivityIdentity, workerID string) (func(), string, error) {
	g.mu.Lock()
	ticket, ok := conn.permit.(*lifecycleTicket)
	if !ok || ticket.gate != g || ticket.conn != conn || ticket.state != 'o' || ticket.backend == 0 || ticket.registration != nil || g.invalid || g.closing {
		g.mu.Unlock()
		return nil, "", errSealProtocol
	}
	if g.next == ^uint64(0) {
		g.invalidateBecauseLocked(invalidCapacity)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return nil, "", errSealProtocol
	}
	key := workerID
	if g.application && g.selection == 1 {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return nil, "", errSealProtocol
	}
	if g.application && g.selection == 2 && identity.TaskID == g.selectionTask {
		if identity.Kind != g.selectionKind || workerID != g.selectionOwner || g.selectionConsumed {
			g.invalidateBecauseLocked(invalidExplicit)
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return nil, "", errSealProtocol
		}
		key = g.selectionKey
		g.selectionConsumed = true
	}
	g.next++
	registration := &activityRegistration{identity: identity, backend: ticket.backend, generation: g.next, activityKey: key}
	ticket.registration = registration
	g.notifyLocked()
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		defer g.unlockAndPublishInvalid()
		if ticket.registration != registration {
			return
		}
		if g.seal != nil && !g.seal.released && g.seal.registration == registration {
			g.invalidateBecauseLocked(invalidTargetRevoked)
		}
		ticket.registration = nil
		g.notifyLocked()
	}, key, nil
}

func (g *lifecycleGate) pin(epoch, generation uint64, publish func()) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if epoch != 1 || generation == 0 || g.seal != nil || g.invalid || g.closing {
		return errSealProtocol
	}
	var target *lifecycleTicket
	for _, t := range g.tickets {
		if t.state == 'o' && t.registration != nil && t.registration.generation == generation {
			if target != nil {
				return errSealProtocol
			}
			target = t
		}
	}
	if target == nil {
		return errSealProtocol
	}
	g.seal = &sealAttempt{epoch: epoch, target: target, registration: target.registration, publishInvalid: publish, publishDone: make(chan struct{})}
	g.closing = true
	g.notifyLocked()
	return nil
}

func (g *lifecycleGate) frame(epoch uint64) sealFrame {
	g.mu.Lock()
	defer g.mu.Unlock()
	frame := sealFrame{Version: 1, Epoch: epoch, Status: "open", Pending: g.pending, Open: g.open}
	if g.seal == nil {
		if epoch != 1 || g.invalid {
			frame.Status = "invalid"
		}
		return frame
	}
	if epoch != g.seal.epoch {
		frame.Status = "invalid"
		return frame
	}
	if g.seal.released {
		frame.Status = "released"
		return frame
	}
	if g.invalid || g.seal.target.state != 'o' || g.seal.target.registration != g.seal.registration {
		frame.Status = "invalid"
		return frame
	}
	frame.Status = "draining"
	if g.closing && g.pending == 0 && g.open == 1 {
		frame.Status = "sealed"
	}
	return frame
}

func (g *lifecycleGate) release(epoch uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seal == nil || epoch != g.seal.epoch {
		return errSealProtocol
	}
	g.seal.released = true
	g.closing = false
	g.notifyLocked()
	return nil
}

func (g *lifecycleGate) invalidate() {
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	g.invalidateBecauseLocked(invalidExplicit)
	g.notifyLocked()
}

// Caller publishes outside the registry lock, before propagating ctx.Err.
func (g *lifecycleGate) invalidateWaitLocked() func() {
	if g.seal == nil || g.seal.released {
		return nil
	}
	g.invalidateBecauseLocked(invalidWaitCancelled)
	g.notifyLocked()
	return g.invalidPublisherLocked()
}

// Reserve one publication under the state lock; every returning invalidator
// waits for that same bounded control-record publication outside the lock.
func (g *lifecycleGate) invalidPublisherLocked() func() {
	if !g.invalid || g.seal == nil || g.seal.released {
		return nil
	}
	s := g.seal
	return func() {
		s.publishOnce.Do(func() {
			defer close(s.publishDone)
			if s.publishInvalid != nil {
				s.publishInvalid()
			}
		})
		<-s.publishDone
	}
}
func (g *lifecycleGate) unlockAndPublishInvalid() {
	publish := g.invalidPublisherLocked()
	g.mu.Unlock()
	if publish != nil {
		publish()
	}
}

// A broken transport cannot publish on itself. Its EOF is terminal to the parent.
func (g *lifecycleGate) invalidateTransport() {
	g.mu.Lock()
	g.invalidateBecauseLocked(invalidTransport)
	g.notifyLocked()
	g.mu.Unlock()
}
