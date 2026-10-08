package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func quiescenceTestLease(owner string) Lease {
	return Lease{Owner: owner, Token: 29, Task: Task{
		ID: "task-private", TenantID: "tenant-private", DomainID: "domain-private",
		ActorID: 41, Kind: "synthetic.quiescence", PayloadVersion: 3,
		PayloadHash: "payload-hash-private", Attempt: 1,
		Payload: json.RawMessage(`{"sensitive":"payload"}`),
		Result:  json.RawMessage(`{"sensitive":"result"}`),
	}}
}

func TestQuiescedAttemptIdentityIsImmutableAndRedacted(t *testing.T) {
	e := &Engine{}
	lease := quiescenceTestLease("owner-private")
	slot, err := e.acquireOwner(lease.Owner)
	if err != nil {
		t.Fatal(err)
	}
	e.retainQuiescence(slot, lease, Kind{})
	q := slot.pending.attempt
	lease.Task.ID, lease.Task.TenantID, lease.Task.Kind = "forged", "forged", "forged"
	lease.Task.Payload[0], lease.Task.Result[0] = 'x', 'x'
	lease.Owner, lease.Token, lease.Task.Attempt = "forged", 999, 999
	if q.TaskID() != "task-private" || q.TenantID() != "tenant-private" || q.DomainID() != "domain-private" || q.ActorID() != 41 || q.Kind() != "synthetic.quiescence" || q.PayloadVersion() != 3 || q.PayloadHash() != "payload-hash-private" || q.Owner() != "owner-private" || q.Fence() != 29 || q.Attempt() != 1 {
		t.Fatal("started identity was not frozen")
	}
	typ := reflect.TypeOf(q)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.IsExported() {
			t.Fatalf("mutable exported field %s", field.Name)
		}
		switch field.Type.Kind() {
		case reflect.String, reflect.Int, reflect.Int64:
		default:
			t.Fatalf("non-scalar receipt field %s", field.Name)
		}
	}
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, formatted := range []string{string(raw), fmt.Sprint(q), fmt.Sprintf("%+v", q), fmt.Sprintf("%#v", q)} {
		for _, private := range []string{"task-private", "tenant-private", "domain-private", "owner-private", "payload-hash-private", "sensitive", "29", "41"} {
			if strings.Contains(formatted, private) {
				t.Fatalf("generic formatting leaked witness metadata: %s", formatted)
			}
		}
	}
}

func TestQuiescenceHookRegistrationIsRestricted(t *testing.T) {
	k := ProductionRegistry().kinds["infrastructure.health"]
	k.OnQuiesced = func(context.Context, pgx.Tx, QuiescedAttempt) error { return nil }
	if _, err := NewRegistry(k); err == nil {
		t.Fatal("replayable hook registered")
	}
	k.ReplaySafe, k.SingleAttemptOnly, k.Schedulable, k.MaxAttempts, k.RetryCodes = false, true, false, 1, nil
	if _, err := NewRegistry(k); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Kind){
		func(k *Kind) { k.SingleAttemptOnly = false; k.ReplaySafe = true },
		func(k *Kind) { k.Schedulable = true },
		func(k *Kind) { k.MaxAttempts = 2 },
		func(k *Kind) { k.RetryCodes = []string{"retry"} },
	} {
		changed := k
		mutate(&changed)
		if _, err := NewRegistry(changed); err == nil {
			t.Fatal("unsafe quiescence registration allowed")
		}
	}
}

func TestQuiescenceRetainsFailurePanicAndCommitUncertainty(t *testing.T) {
	for _, mode := range []string{"rollback", "panic", "commit_uncertainty"} {
		t.Run(mode, func(t *testing.T) {
			e := &Engine{}
			lease := quiescenceTestLease("owner")
			slot, err := e.acquireOwner(lease.Owner)
			if err != nil {
				t.Fatal(err)
			}
			calls, durableWrites := 0, 0
			seen := map[QuiescedAttempt]bool{}
			k := Kind{OnQuiesced: func(_ context.Context, _ pgx.Tx, q QuiescedAttempt) error {
				calls++
				if mode == "panic" && calls == 1 {
					panic("callback sensitive detail")
				}
				if mode == "rollback" && calls == 1 {
					return errors.New("synthetic rollback")
				}
				if !seen[q] {
					seen[q] = true
					durableWrites++
				}
				return nil
			}}
			e.retainQuiescence(slot, lease, k)
			receipt := slot.pending
			e.retainQuiescence(slot, lease, k)
			if slot.pending != receipt || e.PendingQuiescence() != 1 {
				t.Fatal("exact duplicate created extra witness")
			}
			transactions := 0
			transact := func(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
				transactions++
				if err := fn(ctx, nil); err != nil {
					return err
				}
				if mode == "commit_uncertainty" && transactions == 1 {
					return errors.New("synthetic lost commit response")
				}
				return nil
			}
			err = e.acknowledgeQuiescence(context.Background(), slot, transact)
			if !errors.Is(err, ErrQuiescencePending) || e.PendingQuiescence() != 1 {
				t.Fatal("uncertain acknowledgement was discarded", err)
			}
			if mode == "panic" && (strings.Contains(err.Error(), "sensitive") || !errors.Is(err, errQuiescencePanic)) {
				t.Fatal("panic detail exposed or not safely classified", err)
			}
			e.releaseOwner(lease.Owner, slot)
			if len(e.quiescenceOwners) != 1 {
				t.Fatal("failed receipt did not retain its owner slot")
			}
			slot, err = e.acquireOwner(lease.Owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.acknowledgeQuiescence(context.Background(), slot, transact); err != nil {
				t.Fatal(err)
			}
			if calls != 2 || durableWrites != 1 || e.PendingQuiescence() != 0 {
				t.Fatalf("retry changed receipt semantics: calls=%d writes=%d pending=%d", calls, durableWrites, e.PendingQuiescence())
			}
			e.releaseOwner(lease.Owner, slot)
			if len(e.quiescenceOwners) != 0 {
				t.Fatal("acknowledged idle owner retained memory")
			}
		})
	}
}

func TestQuiescenceAcknowledgementDoesNotHoldQueueMutex(t *testing.T) {
	e := &Engine{}
	slot, _ := e.acquireOwner("first")
	e.retainQuiescence(slot, quiescenceTestLease("first"), Kind{OnQuiesced: func(context.Context, pgx.Tx, QuiescedAttempt) error { return nil }})
	err := e.acknowledgeQuiescence(context.Background(), slot, func(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
		if e.PendingQuiescence() != 1 {
			t.Fatal("in-flight receipt not retained")
		}
		other, err := e.acquireOwner("other")
		if err != nil {
			t.Fatal(err)
		}
		e.releaseOwner("other", other)
		return fn(ctx, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	e.releaseOwner("first", slot)
}

func TestQuiescenceOwnerConcurrencyAndSaturationNeverLoseReceipts(t *testing.T) {
	e := &Engine{}
	var admitted, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 128; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			owner := fmt.Sprintf("owner-%d", i)
			slot, err := e.acquireOwner(owner)
			if errors.Is(err, ErrQuiescenceCapacity) {
				rejected.Add(1)
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			admitted.Add(1)
			if worked, err := e.RunOne(context.Background(), owner); worked || !errors.Is(err, ErrOwnerBusy) {
				t.Error("same owner crossed the claim guard", worked, err)
			}
			e.retainQuiescence(slot, quiescenceTestLease(owner), Kind{})
			e.releaseOwner(owner, slot)
		}(i)
	}
	wg.Wait()
	if admitted.Load() != maxQuiescenceOwners || rejected.Load() != 128-maxQuiescenceOwners || e.PendingQuiescence() != maxQuiescenceOwners || len(e.quiescenceOwners) != maxQuiescenceOwners {
		t.Fatalf("unbounded or discarded slots: admitted=%d rejected=%d pending=%d owners=%d", admitted.Load(), rejected.Load(), e.PendingQuiescence(), len(e.quiescenceOwners))
	}
	if worked, err := e.RunOne(context.Background(), "overflow"); worked || !errors.Is(err, ErrQuiescenceCapacity) {
		t.Fatal("capacity guard admitted work", worked, err)
	}
	for owner, slot := range e.quiescenceOwners {
		if slot.active || slot.pending.attempt.Owner() != owner {
			t.Fatal("pending owner corrupted")
		}
	}
}

func TestQuiescenceActiveAndPendingShareCapacity(t *testing.T) {
	e := &Engine{}
	for i := 0; i < maxQuiescenceOwners; i++ {
		owner := fmt.Sprintf("owner-%d", i)
		slot, err := e.acquireOwner(owner)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			e.retainQuiescence(slot, quiescenceTestLease(owner), Kind{})
			e.releaseOwner(owner, slot)
		}
	}
	if _, err := e.acquireOwner("overflow"); !errors.Is(err, ErrQuiescenceCapacity) || e.PendingQuiescence() != maxQuiescenceOwners/2 {
		t.Fatal("active and pending slots used separate bounds", err)
	}
	// Finishing a no-hook slot frees capacity; its replacement still cannot
	// evict any of the pending witnesses.
	e.releaseOwner("owner-1", e.quiescenceOwners["owner-1"])
	if _, err := e.acquireOwner("replacement"); err != nil || e.PendingQuiescence() != maxQuiescenceOwners/2 {
		t.Fatal("idle slot not reusable or witness evicted", err)
	}
}

func TestQuiescenceNilHookAndEmptyRetryAreNoOps(t *testing.T) {
	e := &Engine{}
	slot, err := e.acquireOwner("nil-hook")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.acknowledgeQuiescence(context.Background(), slot, func(context.Context, func(context.Context, pgx.Tx) error) error {
		t.Fatal("empty slot opened a database transaction")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.releaseOwner("nil-hook", slot)
	if err := e.RetryQuiescence(context.Background()); err != nil || e.PendingQuiescence() != 0 {
		t.Fatal("empty retry failed", err)
	}
	if err := e.drainQuiescence(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(e.quiescenceOwners) != 0 {
		t.Fatal("idle no-hook owner retained")
	}
}

func TestQuiescenceDrainBudgetPreservesUnacknowledgedWitness(t *testing.T) {
	e := &Engine{}
	slot, _ := e.acquireOwner("still-active")
	e.retainQuiescence(slot, quiescenceTestLease("still-active"), Kind{})
	// An in-flight acknowledgement cannot be retried concurrently or forgotten.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := e.drainQuiescence(ctx, time.Millisecond); !errors.Is(err, ErrQuiescencePending) || !errors.Is(err, context.DeadlineExceeded) || e.PendingQuiescence() != 1 || !slot.active {
		t.Fatal("drain pretended active acknowledgement completed", err)
	}
}

func TestQuiescenceCancelledRetryDoesNotTouchPendingWitness(t *testing.T) {
	e := &Engine{}
	slot, _ := e.acquireOwner("pending")
	e.retainQuiescence(slot, quiescenceTestLease("pending"), Kind{})
	e.releaseOwner("pending", slot)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.RetryQuiescence(ctx); !errors.Is(err, ErrQuiescencePending) || !errors.Is(err, context.Canceled) || e.PendingQuiescence() != 1 || slot.active {
		t.Fatal("canceled retry changed receipt or ignored total budget", err)
	}
	if worked, err := e.RunOne(context.Background(), "bad owner"); worked || err == nil {
		t.Fatal("invalid owner crossed claim boundary", err)
	} else if p, ok := err.(*Error); !ok || p.Code != "invalid_worker_owner" {
		t.Fatal("invalid-owner compatibility changed", err)
	}
}

func TestQuiescenceDeadlineCannotStarveLaterOwners(t *testing.T) {
	e := &Engine{}
	var slowCalls, readyCalls int
	for _, owner := range []string{"a", "b"} {
		slot, _ := e.acquireOwner(owner)
		e.retainQuiescence(slot, quiescenceTestLease(owner), Kind{OnQuiesced: func(ctx context.Context, _ pgx.Tx, q QuiescedAttempt) error {
			if q.Owner() == "a" {
				slowCalls++
				<-ctx.Done()
				return ctx.Err()
			}
			readyCalls++
			return nil
		}})
		e.releaseOwner(owner, slot)
	}
	transact := func(ctx context.Context, fn func(context.Context, pgx.Tx) error) error { return fn(ctx, nil) }
	for pass := 1; pass <= 2; pass++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		err := e.retryQuiescence(ctx, transact)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrQuiescencePending) {
			t.Fatal("slow callback did not retain receipt on deadline", err)
		}
		if slowCalls != pass || readyCalls != pass-1 || e.PendingQuiescence() != 3-pass {
			t.Fatalf("deadline exhausted pass starved a ready owner: pass=%d slow=%d ready=%d pending=%d", pass, slowCalls, readyCalls, e.PendingQuiescence())
		}
	}
	if e.quiescenceOwners["a"].active || e.quiescenceOwners["b"] != nil {
		t.Fatal("retry retained completed owner or left timed-out slot active")
	}
}
