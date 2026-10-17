//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

func installTestGate(t *testing.T) *lifecycleGate {
	t.Helper()
	coordinator.Lock()
	if coordinator.gate != nil {
		coordinator.Unlock()
		t.Fatal("test coordinator already installed")
	}
	g := &lifecycleGate{}
	coordinator.gate = g
	coordinator.Unlock()
	t.Cleanup(func() {
		coordinator.Lock()
		coordinator.gate = nil
		coordinator.Unlock()
	})
	return g
}

func TestRejectedAdmissionNeverCallsConnector(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "canceled"}[canceled], func(t *testing.T) {
			g := installTestGate(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			} else {
				g.closeAdmission()
			}
			calls := 0
			conn, err := connectWith(ctx, nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
				calls++
				return nil, errors.New("unexpected connector")
			})
			if conn != nil || err == nil || calls != 0 {
				t.Fatal("rejected admission dispatched")
			}
		})
	}
}

func TestPendingAndOpenLifetimePreventDrain(t *testing.T) {
	g := &lifecycleGate{}
	ticket, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("acquire failed")
	}
	g.closeAdmission()
	if g.locallyDrained() {
		t.Fatal("pending startup counted as drained")
	}
	ticket.connected()
	if g.locallyDrained() {
		t.Fatal("open lifetime counted as drained")
	}
	ticket.closed(true)
	if !g.locallyDrained() {
		t.Fatal("clean lifecycle did not drain")
	}
	ticket.closed(true)
	if g.locallyDrained() {
		t.Fatal("duplicate lifecycle release not rejected")
	}
}

func TestFailedStartupAndUncleanClosePoisonAttempt(t *testing.T) {
	for _, startup := range []bool{false, true} {
		g := &lifecycleGate{}
		ticket, _ := g.acquire(context.Background())
		if startup {
			ticket.failed()
		} else {
			ticket.connected()
			ticket.closed(false)
		}
		g.closeAdmission()
		if g.locallyDrained() {
			t.Fatal("failed lifecycle counted as drained")
		}
		if _, err := g.acquire(context.Background()); err == nil {
			t.Fatal("invalid attempt admitted connection")
		}
	}
}

func TestConnectorFailureAndPanicCannotDrain(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			g := installTestGate(t)
			func() {
				defer func() { _ = recover() }()
				_, _ = connectWith(context.Background(), nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
					if panics {
						panic("synthetic connector panic")
					}
					return nil, errors.New("synthetic connector failure")
				})
			}()
			g.closeAdmission()
			if g.locallyDrained() {
				t.Fatal("failed or panicked startup counted as drained")
			}
		})
	}
}

func TestAcquireAndClosureAreSerialized(t *testing.T) {
	for i := 0; i < 100; i++ {
		g := &lifecycleGate{}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			ticket, err := g.acquire(context.Background())
			if err == nil {
				ticket.connected()
				ticket.closed(true)
			}
		}()
		go func() { defer wg.Done(); g.closeAdmission() }()
		wg.Wait()
		if !g.locallyDrained() {
			t.Fatal("serialized clean race did not drain")
		}
		if _, err := g.acquire(context.Background()); err == nil {
			t.Fatal("post-closure acquisition succeeded")
		}
	}
}

func TestTaggedBuildWithoutCoordinatorDelegates(t *testing.T) {
	coordinator.Lock()
	absent := coordinator.gate == nil
	coordinator.Unlock()
	if !absent {
		t.Fatal("unexpected application coordinator")
	}
	want := errors.New("synthetic failure")
	calls := 0
	_, err := connectWith(context.Background(), nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
		calls++
		return nil, want
	})
	if err != want || calls != 1 {
		t.Fatal("inactive tagged build changed delegate behavior")
	}
}

func TestNilConnectorResultCannotDrain(t *testing.T) {
	g := installTestGate(t)
	conn, err := connectWith(context.Background(), nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
		return nil, nil
	})
	g.closeAdmission()
	if conn != nil || err == nil || g.locallyDrained() {
		t.Fatal("invalid connector result accepted")
	}
}
