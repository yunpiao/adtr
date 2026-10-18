//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCleanLifetimesBeyondCapacityArePrunedWithoutGenerationReuse(t *testing.T) {
	g := &lifecycleGate{witness: &diagnosticWitness{}}
	var prior uint64
	for i := 0; i < 8193; i++ {
		ticket, err := g.acquire(context.Background())
		if err != nil || ticket.generation <= prior {
			t.Fatal("clean history exhausted capacity or reused generation")
		}
		prior = ticket.generation
		attachTestTicket(ticket)
		ticket.closingConnection()
		ticket.closed(true)
		if len(g.tickets) != 0 || ticket.conn != nil || ticket.backend != 0 || ticket.state != 'c' || g.invalid {
			t.Fatal("clean terminal ticket retained or history erased")
		}
	}
	d := g.applicationDiagnostic(1)
	if d.Total != 8193 || d.Retained != 0 || d.Open != 0 || d.Pending != 0 || d.Reason != "none" {
		t.Fatal("cumulative diagnostic changed")
	}
}

func TestPruningRetainsEveryUnprovenOrEpochReferencedLifetime(t *testing.T) {
	for _, state := range []string{"pending", "open", "closing", "failed", "dirty", "registered", "invalid", "pinned", "released"} {
		t.Run(state, func(t *testing.T) {
			g := &lifecycleGate{}
			ticket, _ := g.acquire(context.Background())
			if state != "pending" && state != "failed" {
				attachTestTicket(ticket)
			}
			switch state {
			case "closing":
				ticket.closingConnection()
			case "failed":
				ticket.failed()
			case "dirty":
				ticket.closed(false)
			case "registered":
				g.register(ticket.conn, testSealIdentity())
				ticket.closed(true)
			case "invalid":
				g.invalidate()
				ticket.closed(true)
			case "pinned", "released":
				g.register(ticket.conn, testSealIdentity())
				if g.pin(1, ticket.registration.generation, nil) != nil {
					t.Fatal("pin")
				}
				if state == "released" {
					g.release(1)
				}
				ticket.closed(true)
			}
			if len(g.tickets) != 1 || g.tickets[ticket.generation] != ticket {
				t.Fatal("unproven lifetime pruned")
			}
			if state != "pending" && state != "failed" && ticket.conn == nil {
				t.Fatal("retained lifetime reference cleared")
			}
			if state == "failed" || state == "dirty" || state == "invalid" || state == "pinned" {
				if !g.invalid {
					t.Fatal("invalid history erased")
				}
			}
		})
	}
}

func TestPrunedRevokedCandidateCannotAffectFreshGeneration(t *testing.T) {
	g := &lifecycleGate{}
	old, _ := g.acquire(context.Background())
	oldConn := attachTestTicket(old)
	revoke, err := g.register(oldConn, testSealIdentity())
	if err != nil {
		t.Fatal("old register")
	}
	oldRegistration := old.registration.generation
	revoke()
	old.closed(true)
	if len(g.tickets) != 0 || old.conn != nil {
		t.Fatal("revoked clean candidate retained")
	}
	fresh, _ := g.acquire(context.Background())
	freshConn := attachTestTicket(fresh)
	freshRevoke, err := g.register(freshConn, testSealIdentity())
	if err != nil {
		t.Fatal("fresh register")
	}
	freshRegistration := fresh.registration
	if fresh.generation <= oldRegistration || freshRegistration.generation <= fresh.generation {
		t.Fatal("generation reused")
	}
	revoke() // The old closure compares the old registration pointer only.
	if fresh.registration != freshRegistration || g.invalid {
		t.Fatal("stale revoke affected new candidate")
	}
	if _, err = g.register(oldConn, testSealIdentity()); err == nil {
		t.Fatal("closed handle rebound")
	}
	if g.pin(1, oldRegistration, nil) == nil {
		t.Fatal("stale generation pinned")
	}
	if g.pin(1, freshRegistration.generation, nil) != nil {
		t.Fatal("fresh generation unavailable")
	}
	if g.release(1) != nil || g.release(1) != nil {
		t.Fatal("release replay changed")
	}
	freshRevoke()
	fresh.closed(true)
	if g.tickets[fresh.generation] != fresh || g.seal.target != fresh || g.invalid {
		t.Fatal("released epoch target lost")
	}
	if g.release(1) != nil {
		t.Fatal("release replay needs removed state")
	}
}

func TestGenerationAndDiagnosticCountersNeverWrap(t *testing.T) {
	for _, which := range []string{"ticket_generation", "registration_generation", "total"} {
		t.Run(which, func(t *testing.T) {
			g := &lifecycleGate{}
			if which == "registration_generation" {
				ticket, _ := g.acquire(context.Background())
				conn := attachTestTicket(ticket)
				g.next = ^uint64(0)
				if _, err := g.register(conn, testSealIdentity()); err == nil {
					t.Fatal("registration counter wrapped")
				}
			} else {
				if which == "total" {
					g.total = ^uint64(0)
				} else {
					g.next = ^uint64(0)
				}
				if _, err := g.acquire(context.Background()); err != errAdmissionClosed {
					t.Fatal("allocation counter wrapped")
				}
			}
			if !g.invalid || g.applicationDiagnostic(1).Reason != invalidCapacity {
				t.Fatal("overflow did not fail closed")
			}
			if which == "total" && g.total != ^uint64(0) || which != "total" && g.next != ^uint64(0) {
				t.Fatal("counter reset")
			}
		})
	}
}

func TestInvalidPublisherKeepsTargetAndOtherClosingTicket(t *testing.T) {
	g, target := newSealProtocolTestGate()
	other, _ := g.acquire(context.Background())
	attachTestTicket(other)
	entered, proceed, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(proceed) }) }
	defer unblock()
	if g.pin(1, target, func() { close(entered); <-proceed }) != nil {
		t.Fatal("pin")
	}
	go func() { g.invalidate(); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("publisher did not start")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); other.closed(true) }()
	// The publication is in flight. All ticket mutations still share g.mu.
	g.mu.Lock()
	targetRetained := g.tickets[g.seal.target.generation] == g.seal.target
	g.mu.Unlock()
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publisher did not finish")
	}
	joined := make(chan struct{})
	go func() { wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("close did not join publisher")
	}
	if !targetRetained || g.tickets[other.generation] != other || !g.invalid {
		t.Fatal("in-flight publisher lost retained state")
	}
}
