//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

var testBackend atomic.Uint32

func attachTestTicket(ticket *lifecycleTicket) *Conn {
	conn := &Conn{permit: ticket}
	ticket.attach(conn, testBackend.Add(1))
	return conn
}

func testSealIdentity() ActivityIdentity {
	return ActivityIdentity{TaskID: "private-target", Kind: "user_assets.v3.export", Attempt: 1, LeaseOwner: "private-worker-0", FencingToken: 7, ResultVersion: 2, Phase: "first-fenced-progress-10"}
}

func newSealProtocolTestGate() (*lifecycleGate, uint64) {
	g := &lifecycleGate{}
	ticket, _ := g.acquire(context.Background())
	conn := attachTestTicket(ticket)
	_, _ = g.register(conn, testSealIdentity())
	return g, g.candidates()[0].generation
}

func TestSealRequiresWholeOtherLifetimes(t *testing.T) {
	for _, pending := range []bool{false, true} {
		g, target := newSealProtocolTestGate()
		other, _ := g.acquire(context.Background())
		if !pending {
			attachTestTicket(other)
		}
		if g.pin(1, target, nil) != nil || g.frame(1).Status != "draining" {
			t.Fatal("live non-target omitted")
		}
		if pending {
			attachTestTicket(other)
		}
		if g.frame(1).Status != "draining" {
			t.Fatal("connection establishment counted as close")
		}
		other.closed(true)
		if g.frame(1).Status != "sealed" {
			t.Fatal("clean close did not seal")
		}
	}
}

func TestSealWaitCancellationPublishedBeforeReturn(t *testing.T) {
	for _, extra := range []bool{false, true} {
		g, target := newSealProtocolTestGate()
		if extra {
			other, _ := g.acquire(context.Background())
			attachTestTicket(other)
		}
		published, allowReturn := make(chan struct{}), make(chan struct{})
		if g.pin(1, target, func() { close(published); <-allowReturn }) != nil {
			t.Fatal("pin failed")
		}
		ctx, cancel := context.WithCancel(context.Background())
		returned := make(chan error, 1)
		go func() { _, err := g.acquire(ctx); returned <- err }()
		cancel()
		select {
		case <-published:
		case <-time.After(time.Second):
			t.Fatal("invalidation not published")
		}
		select {
		case <-returned:
			t.Fatal("context error escaped before publication")
		default:
		}
		if g.frame(1).Status != "invalid" {
			t.Fatal("canceled waiter did not invalidate")
		}
		close(allowReturn)
		if <-returned != context.Canceled {
			t.Fatal("original context error changed")
		}
		if g.release(1) != nil {
			t.Fatal("release failed")
		}
	}
}

func TestSealWaitReleaseAndExpiredContext(t *testing.T) {
	g, target := newSealProtocolTestGate()
	if g.pin(1, target, nil) != nil {
		t.Fatal("pin failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan *lifecycleTicket, 1)
	go func() { ticket, _ := g.acquire(ctx); result <- ticket }()
	select {
	case <-result:
		t.Fatal("closed admission did not wait")
	case <-time.After(10 * time.Millisecond):
	}
	if g.release(1) != nil {
		t.Fatal("release failed")
	}
	if ticket := <-result; ticket == nil {
		t.Fatal("release did not admit")
	}
	cancel()
	if ticket, err := g.acquire(ctx); ticket != nil || err != context.Canceled {
		t.Fatal("expired context admitted")
	}
}

func TestSealTargetRevocationCloseAndWrongEpoch(t *testing.T) {
	for _, action := range []string{"revoke", "close", "dirty", "wrong", "repeat"} {
		g, target := newSealProtocolTestGate()
		if g.pin(1, target, nil) != nil {
			t.Fatal("pin failed")
		}
		switch action {
		case "revoke":
			g.mu.Lock()
			g.seal.target.registration = nil
			g.mu.Unlock()
		case "close":
			g.seal.target.closed(true)
		case "dirty":
			other := g.seal.target
			other.closed(false)
		case "wrong":
			if g.release(2) == nil {
				t.Fatal("wrong epoch released")
			}
			continue
		case "repeat":
			if g.pin(1, target, nil) == nil {
				t.Fatal("second pin accepted")
			}
			continue
		}
		if g.frame(1).Status != "invalid" {
			t.Fatal("target lifetime loss accepted")
		}
	}
}

func TestSealMissingAndDuplicateHandleRejected(t *testing.T) {
	g := &lifecycleGate{}
	ticket, _ := g.acquire(context.Background())
	ticket.attach(nil, 0)
	if !g.invalid || g.open != 0 {
		t.Fatal("missing physical identity accepted")
	}
	g = &lifecycleGate{}
	one, _ := g.acquire(context.Background())
	conn := attachTestTicket(one)
	two, _ := g.acquire(context.Background())
	two.attach(conn, one.backend)
	if !g.invalid {
		t.Fatal("duplicate handle accepted")
	}
}

func TestSealAllLifetimeInvalidationsPublish(t *testing.T) {
	for _, action := range []string{"revoke", "close_start", "close_end", "startup_failure", "explicit"} {
		t.Run(action, func(t *testing.T) {
			g := &lifecycleGate{}
			ticket, _ := g.acquire(context.Background())
			conn := attachTestTicket(ticket)
			revoke, err := g.register(conn, testSealIdentity())
			if err != nil {
				t.Fatal("register failed")
			}
			generation := g.candidates()[0].generation
			var pending *lifecycleTicket
			if action == "startup_failure" {
				pending, _ = g.acquire(context.Background())
			}
			var published atomic.Int32
			if g.pin(1, generation, func() { published.Add(1) }) != nil {
				t.Fatal("pin failed")
			}
			switch action {
			case "revoke":
				revoke()
			case "close_start":
				ticket.closingConnection()
			case "close_end":
				ticket.closed(false)
			case "startup_failure":
				pending.failed()
			case "explicit":
				g.invalidate()
			}
			if g.frame(1).Status != "invalid" || published.Load() != 1 {
				t.Fatal("lifetime invalidation not published")
			}
			g.invalidate()
			if published.Load() != 1 {
				t.Fatal("invalidation publication repeated")
			}
		})
	}
}

func TestSealConcurrentPinCloseAndRevocation(t *testing.T) {
	for i := 0; i < 100; i++ {
		g := &lifecycleGate{}
		ticket, _ := g.acquire(context.Background())
		conn := attachTestTicket(ticket)
		revoke, _ := g.register(conn, testSealIdentity())
		generation := g.candidates()[0].generation
		done := make(chan struct{}, 2)
		go func() { _ = g.pin(1, generation, nil); done <- struct{}{} }()
		go func() { revoke(); ticket.closingConnection(); ticket.closed(true); done <- struct{}{} }()
		<-done
		<-done
		if g.frame(1).Status == "sealed" {
			t.Fatal("dead target survived concurrent selection")
		}
	}
}

func TestSealSyntheticOneSecondHeartbeatInvalidates(t *testing.T) {
	g, target := newSealProtocolTestGate()
	published := make(chan struct{})
	if g.pin(1, target, func() { close(published) }) != nil {
		t.Fatal("pin failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if ticket, err := g.acquire(ctx); ticket != nil || err != context.DeadlineExceeded {
		t.Fatal("short heartbeat context changed")
	}
	select {
	case <-published:
	default:
		t.Fatal("deadline returned before invalidation")
	}
	if g.frame(1).Status != "invalid" || !g.closing {
		t.Fatal("short deadline silently released seal")
	}
	if g.release(1) != nil {
		t.Fatal("invalid attempt could not release")
	}
}
