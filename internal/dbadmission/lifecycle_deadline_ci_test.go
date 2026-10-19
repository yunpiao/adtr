//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Model a deadline whose timer callback has not yet published ctx.Err().
// All transitions are synchronized; no scheduler timing is needed.
type delayedDeadlineContext struct {
	context.Context
	mu          sync.Mutex
	deadline    time.Time
	hasDeadline bool
	err         error
	waiting     chan struct{}
	once        sync.Once
}

func (c *delayedDeadlineContext) Deadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline, c.hasDeadline
}
func (c *delayedDeadlineContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
func (c *delayedDeadlineContext) Done() <-chan struct{} {
	if c.waiting != nil {
		c.once.Do(func() { close(c.waiting) })
	}
	return nil
}

func TestAcquireRejectsElapsedDeadlineBeforeConnector(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "canceled_precedence"}[canceled], func(t *testing.T) {
			g := installTestGate(t)
			ctx := &delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Hour), hasDeadline: true}
			want := context.DeadlineExceeded
			if canceled {
				ctx.err = context.Canceled
				want = context.Canceled
			}
			calls := 0
			conn, err := connectWith(ctx, nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
				calls++
				return nil, errors.New("unexpected connector")
			})
			if conn != nil || err != want || calls != 0 {
				t.Fatal("expired admission did not preserve error or dispatched connector")
			}
			if g.next != 0 || g.total != 0 || g.pending != 0 || g.open != 0 || len(g.tickets) != 0 || g.invalid {
				t.Fatal("pre-ticket rejection changed lifecycle accounting")
			}
			if !canceled && ctx.Err() != nil {
				t.Fatal("test context unexpectedly published cancellation")
			}
		})
	}
}

func TestAcquireFutureAndAbsentDeadlinesRemainAdmissible(t *testing.T) {
	for _, has := range []bool{false, true} {
		ctx := &delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Hour), hasDeadline: has}
		g := &lifecycleGate{}
		ticket, err := g.acquire(ctx)
		if err != nil || ticket == nil || g.pending != 1 || g.total != 1 || g.next != 1 || len(g.tickets) != 1 {
			t.Fatal("valid deadline rejected")
		}
	}
}

func TestQueuedWakeRechecksAbsoluteDeadlineWithoutDispatch(t *testing.T) {
	g := installTestGate(t)
	g.queuedParked = true
	ctx := &delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Hour), hasDeadline: true, waiting: make(chan struct{})}
	type observed struct {
		err   error
		phase string
	}
	result := make(chan observed, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		ctx.mu.Lock()
		ctx.deadline = time.Now().Add(-time.Hour)
		ctx.mu.Unlock()
		g.mu.Lock()
		g.queuedParked = false
		g.notifyLocked()
		g.mu.Unlock()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Error("queued waiter cleanup did not join")
		}
	})
	calls := 0
	go func() {
		defer close(finished)
		_, err, phase := connectWithPhase(ctx, nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
			calls++
			return nil, errors.New("unexpected connector")
		})
		result <- observed{err, phase}
	}()
	select {
	case <-ctx.waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not reach queued gate")
	}
	ctx.mu.Lock()
	ctx.deadline = time.Now().Add(-time.Hour)
	ctx.mu.Unlock()
	g.mu.Lock()
	g.queuedParked = false
	g.notifyLocked()
	g.mu.Unlock()
	select {
	case got := <-result:
		if got.err != context.DeadlineExceeded || got.phase != "admission" {
			t.Fatal("wake did not preserve elapsed deadline")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wake did not finish")
	}
	if calls != 0 || g.next != 0 || g.total != 0 || g.pending != 0 || len(g.tickets) != 0 || g.invalid {
		t.Fatal("expired queued waiter dispatched or poisoned admission")
	}
}

func TestElapsedDeadlineRetainsSealedPublication(t *testing.T) {
	g, generation := newSealProtocolTestGate()
	published := false
	if g.pin(1, generation, func() { published = true }) != nil || g.frame(1).Status != "sealed" {
		t.Fatal("sealed registry precondition failed")
	}
	next, total, pending, open, retained := g.next, g.total, g.pending, g.open, len(g.tickets)
	target, registration := g.seal.target, g.seal.registration
	ctx := &delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Hour), hasDeadline: true}
	ticket, err := g.acquire(ctx)
	if ticket != nil || err != context.DeadlineExceeded || !g.invalid || !published || g.firstInvalidReason != invalidWaitCancelled {
		t.Fatal("sealed elapsed deadline bypassed original publication obligation")
	}
	if g.next != next || g.total != total || g.pending != pending || g.open != open || len(g.tickets) != retained || g.seal.target != target || target.registration != registration {
		t.Fatal("sealed rejection changed retained target or allocated ticket")
	}
	select {
	case <-g.seal.publishDone:
	default:
		t.Fatal("publication did not finish before return")
	}
}

func TestInactiveTaggedDeadlineContextStillDelegatesUnchanged(t *testing.T) {
	coordinator.Lock()
	absent := coordinator.gate == nil
	coordinator.Unlock()
	if !absent {
		t.Fatal("unexpected application coordinator")
	}
	ctx := &delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Hour), hasDeadline: true}
	want := errors.New("delegate result")
	calls := 0
	_, err := connectWith(ctx, nil, func(got context.Context, _ *pgx.ConnConfig) (*pgx.Conn, error) {
		calls++
		if got != ctx {
			t.Fatal("delegate context replaced")
		}
		return nil, want
	})
	if err != want || calls != 1 {
		t.Fatal("inactive coordinator changed delegate semantics")
	}
}
