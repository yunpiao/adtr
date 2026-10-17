//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"sync"
)

var errAdmissionClosed = errors.New("database admission closed")

// No application installation API is provided in this verification slice.
// Only package tests install a coordinator; a tagged binary remains inactive.
var coordinator struct {
	sync.Mutex
	gate *lifecycleGate
}

type lifecycleGate struct {
	mu      sync.Mutex
	closing bool
	invalid bool
	pending int
	open    int
}

type lifecycleTicket struct {
	gate  *lifecycleGate
	state byte // p: starting, o: connected, c: normally closed, f: failed
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

func (g *lifecycleGate) acquire(ctx context.Context) (*lifecycleTicket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g.closing || g.invalid {
		return nil, errAdmissionClosed
	}
	g.pending++
	return &lifecycleTicket{gate: g, state: 'p'}, nil
}

func (g *lifecycleGate) closeAdmission() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closing = true
}

// locallyDrained says nothing about PostgreSQL backend cleanup or safe capture.
// A future integration must ALSO require an independent complete PG observation.
func (g *lifecycleGate) locallyDrained() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closing && !g.invalid && g.pending == 0 && g.open == 0
}

func (t *lifecycleTicket) connected() {
	g := t.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if t.state != 'p' {
		g.invalid = true
		return
	}
	g.pending--
	g.open++
	t.state = 'o'
}

func (t *lifecycleTicket) failed() {
	g := t.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	g.invalid = true
	if t.state == 'p' {
		g.pending--
	}
	t.state = 'f'
}

func (t *lifecycleTicket) closed(clean bool) {
	g := t.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if t.state != 'o' {
		g.invalid = true
		return
	}
	g.open--
	if !clean {
		g.invalid = true
	}
	t.state = 'c'
}
