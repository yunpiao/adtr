//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"sync"
)

var errAdmissionClosed = errors.New("database admission closed")

// Application installation is restricted to the explicit CI bootstrap.
var coordinator struct {
	sync.Mutex
	gate *lifecycleGate
	used bool
}

type lifecycleGate struct {
	mu                 sync.Mutex
	closing, invalid   bool
	pending, open      int
	next               uint64
	tickets            map[uint64]*lifecycleTicket
	changed            chan struct{}
	seal               *sealAttempt
	witness            invalidationWitness
	revision           uint64
	total              uint64
	firstInvalidReason invalidationReason
	firstInvalidPhase  string
}

type lifecycleTicket struct {
	gate         *lifecycleGate
	generation   uint64
	state        byte
	conn         *Conn
	backend      uint32
	registration *activityRegistration
}

func acquireLifecycle(ctx context.Context) (lifecyclePermit, error) {
	coordinator.Lock()
	coordinator.used = true
	gate := coordinator.gate
	coordinator.Unlock()
	if gate == nil {
		return nil, nil
	}
	return gate.acquire(ctx)
}

func (g *lifecycleGate) notifyLocked() {
	g.revision++
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}

func (g *lifecycleGate) acquire(ctx context.Context) (*lifecycleTicket, error) {
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			publish := g.invalidateWaitLocked()
			g.mu.Unlock()
			if publish != nil {
				publish()
			}
			return nil, err
		}
		released := g.seal != nil && g.seal.released
		if (g.invalid && !released && g.seal == nil) || (g.closing && g.seal == nil) {
			g.mu.Unlock()
			return nil, errAdmissionClosed
		}
		if g.closing {
			if g.changed == nil {
				g.changed = make(chan struct{})
			}
			changed := g.changed
			g.mu.Unlock()
			select {
			case <-changed:
			case <-ctx.Done():
			}
			continue
		}
		if (g.witness != nil && len(g.tickets) >= 4096) || g.next == ^uint64(0) || g.total == ^uint64(0) {
			g.invalidateBecauseLocked(invalidCapacity)
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return nil, errAdmissionClosed
		}
		g.total++
		g.next++
		if g.tickets == nil {
			g.tickets = make(map[uint64]*lifecycleTicket)
		}
		ticket := &lifecycleTicket{gate: g, generation: g.next, state: 'p'}
		g.tickets[ticket.generation] = ticket
		g.pending++
		g.notifyLocked()
		g.mu.Unlock()
		return ticket, nil
	}
}

func (g *lifecycleGate) closeAdmission() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closing = true
	g.notifyLocked()
}

// Local accounting never substitutes for independent PostgreSQL observation.
func (g *lifecycleGate) locallyDrained() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closing && !g.invalid && g.pending == 0 && g.open == 0
}

func (t *lifecycleTicket) connected(conn *Conn) {
	if conn == nil || conn.raw == nil {
		t.attach(conn, 0)
		return
	}
	t.attach(conn, conn.raw.PgConn().PID())
}

func (t *lifecycleTicket) attach(conn *Conn, backend uint32) {
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if t.state != 'p' || conn == nil || backend == 0 {
		g.invalidateBecauseLocked(invalidConnectState)
		g.notifyLocked()
		return
	}
	for _, other := range g.tickets {
		if other != t && other.state == 'o' && (other.conn == conn || other.backend == backend) {
			g.invalidateBecauseLocked(invalidDuplicateBackend)
			g.notifyLocked()
			return
		}
	}
	g.pending--
	g.open++
	t.state = 'o'
	t.conn = conn
	t.backend = backend
	g.notifyLocked()
}

func (t *lifecycleTicket) failed() {
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	g.invalidateBecauseLocked(invalidConnectFailed)
	if t.state == 'p' {
		g.pending--
	}
	t.state = 'f'
	g.notifyLocked()
}

func (t *lifecycleTicket) closed(clean bool) {
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if t.state != 'o' && t.state != 'x' {
		g.invalidateBecauseLocked(invalidCloseState)
		g.notifyLocked()
		return
	}
	// Inspect registration before clearing it. A closed candidate or any epoch
	// target remains retained; its release callback/ACK may still refer to it.
	prunable := clean && !g.invalid && t.registration == nil &&
		(g.seal == nil || g.seal.target != t)
	if !clean {
		g.invalidateBecauseLocked(invalidUncleanClose)
	} else if g.seal != nil && !g.seal.released && g.seal.target == t {
		g.invalidateBecauseLocked(invalidTargetClosed)
	}
	g.open--
	t.state = 'c'
	t.registration = nil
	if prunable {
		// No invalid publisher exists while !g.invalid. The mutex serializes
		// closure with pin, registration and complete snapshots. Preserve the
		// ticket's unique generation/state for any stale local handle.
		delete(g.tickets, t.generation)
		t.conn = nil
		t.backend = 0
	}
	g.notifyLocked()
}

func (t *lifecycleTicket) closingConnection() {
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if t.state != 'o' {
		g.invalidateBecauseLocked(invalidCloseState)
		g.notifyLocked()
		return
	}
	if g.seal != nil && !g.seal.released && g.seal.target == t {
		g.invalidateBecauseLocked(invalidTargetClosing)
		g.notifyLocked()
	}
	t.state = 'x'
	g.notifyLocked()
}

// The sticky store precedes every committed invalidation, including transport errors.
type invalidationWitness interface{ invalidate() }

func (g *lifecycleGate) markInvalidLocked() {
	invalidateWitness(g.witness)
	g.invalid = true
}

func invalidateWitness(w invalidationWitness) {
	if w != nil {
		w.invalidate()
	}
}
