//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"sync"
)

var errAdmissionClosed = errors.New("database admission closed")

// Only package tests install a coordinator. No application installation API exists.
var coordinator struct {
	sync.Mutex
	gate *lifecycleGate
}

type lifecycleGate struct {
	mu               sync.Mutex
	closing, invalid bool
	pending, open    int
	next             uint64
	tickets          map[uint64]*lifecycleTicket
	changed          chan struct{}
	seal             *sealAttempt
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
	gate := coordinator.gate
	coordinator.Unlock()
	if gate == nil {
		return nil, nil
	}
	return gate.acquire(ctx)
}

func (g *lifecycleGate) notifyLocked() {
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
		g.invalid = true
		g.notifyLocked()
		return
	}
	for _, other := range g.tickets {
		if other != t && other.state == 'o' && (other.conn == conn || other.backend == backend) {
			g.invalid = true
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
	g.invalid = true
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
		g.invalid = true
		g.notifyLocked()
		return
	}
	g.open--
	if !clean || (g.seal != nil && !g.seal.released && g.seal.target == t) {
		g.invalid = true
	}
	t.state = 'c'
	t.registration = nil
	g.notifyLocked()
}

func (t *lifecycleTicket) closingConnection() {
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if t.state != 'o' {
		g.invalid = true
		g.notifyLocked()
		return
	}
	t.state = 'x'
	if g.seal != nil && !g.seal.released && g.seal.target == t {
		g.invalid = true
		g.notifyLocked()
	}
}
