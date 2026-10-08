package tasks

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// QuiescedAttempt is an engine-issued witness that the exact started executor
// has returned. Its identity cannot be edited or constructed by another package.
// It contains neither payload, result, outcome, nor authority to resolve secrets.
// The zero value is not an issued witness and has no valid task identity.
type QuiescedAttempt struct {
	taskID, tenantID, domainID, kind, payloadHash, owner string
	actorID, fence                                       int64
	payloadVersion, attempt                              int
}

func (q QuiescedAttempt) TaskID() string      { return q.taskID }
func (q QuiescedAttempt) TenantID() string    { return q.tenantID }
func (q QuiescedAttempt) DomainID() string    { return q.domainID }
func (q QuiescedAttempt) ActorID() int64      { return q.actorID }
func (q QuiescedAttempt) Kind() string        { return q.kind }
func (q QuiescedAttempt) PayloadVersion() int { return q.payloadVersion }
func (q QuiescedAttempt) PayloadHash() string { return q.payloadHash }
func (q QuiescedAttempt) Owner() string       { return q.owner }
func (q QuiescedAttempt) Fence() int64        { return q.fence }
func (q QuiescedAttempt) Attempt() int        { return q.attempt }

// The witness is an internal capability, not a public task DTO. In particular,
// generic logging and JSON must not reveal owner/fencing identifiers.
func (q QuiescedAttempt) String() string   { return "quiesced task attempt" }
func (q QuiescedAttempt) GoString() string { return "tasks.QuiescedAttempt{redacted}" }
func (q QuiescedAttempt) MarshalJSON() ([]byte, error) {
	return []byte(`{"type":"quiesced_attempt"}`), nil
}

var ErrOwnerBusy = errors.New("task owner already active")
var ErrQuiescenceCapacity = errors.New("task owner capacity exhausted")
var ErrQuiescencePending = errors.New("task quiescence acknowledgements pending")
var errQuiescencePanic = errors.New("task quiescence callback panicked")

const maxQuiescenceOwners = 32
const quiescenceDrainTimeout = 10 * time.Second

type quiescenceReceipt struct {
	attempt QuiescedAttempt
	hook    func(context.Context, pgx.Tx, QuiescedAttempt) error
}

// A slot is reserved before Claim, so executor completion can always retain its
// witness. Pending slots remain reserved across RunOne calls. Idle empty slots
// are removed; changing owner names cannot grow this map beyond the hard bound.
type quiescenceSlot struct {
	active  bool
	pending *quiescenceReceipt
}

func (e *Engine) acquireOwner(owner string) (*quiescenceSlot, error) {
	if !identifier.MatchString(owner) {
		return nil, problem(400, "invalid_worker_owner")
	}
	e.quiescenceMu.Lock()
	defer e.quiescenceMu.Unlock()
	if slot := e.quiescenceOwners[owner]; slot != nil {
		if slot.active {
			return nil, ErrOwnerBusy
		}
		slot.active = true
		return slot, nil
	}
	if len(e.quiescenceOwners) >= maxQuiescenceOwners {
		return nil, ErrQuiescenceCapacity
	}
	if e.quiescenceOwners == nil {
		e.quiescenceOwners = make(map[string]*quiescenceSlot)
	}
	slot := &quiescenceSlot{active: true}
	e.quiescenceOwners[owner] = slot
	return slot, nil
}

func (e *Engine) releaseOwner(owner string, slot *quiescenceSlot) {
	e.quiescenceMu.Lock()
	defer e.quiescenceMu.Unlock()
	slot.active = false
	if slot.pending == nil {
		delete(e.quiescenceOwners, owner)
	}
}

// Called only by the engine after receiving runExecutor's completed result.
// Keeping only scalar identity prevents executor mutations of its Task copy
// (including payload backing arrays) from changing the witness.
func (e *Engine) retainQuiescence(slot *quiescenceSlot, lease Lease, k Kind) {
	q := QuiescedAttempt{
		taskID: lease.Task.ID, tenantID: lease.Task.TenantID,
		domainID: lease.Task.DomainID, actorID: lease.Task.ActorID,
		kind: lease.Task.Kind, payloadVersion: lease.Task.PayloadVersion,
		payloadHash: lease.Task.PayloadHash, owner: lease.Owner,
		fence: lease.Token, attempt: lease.Task.Attempt,
	}
	e.quiescenceMu.Lock()
	defer e.quiescenceMu.Unlock()
	if slot.pending != nil {
		// Exact receipts deduplicate; a different receipt cannot occur because
		// this owner cannot claim until its previous acknowledgement commits.
		if slot.pending.attempt != q {
			panic("task owner claimed with unacknowledged quiescence")
		}
		return
	}
	slot.pending = &quiescenceReceipt{attempt: q, hook: k.OnQuiesced}
}

type quiescenceTransaction func(context.Context, func(context.Context, pgx.Tx) error) error

// Keep connection/transaction teardown inside the same caller budget. Ordinary
// task operations give teardown an additional grace period; receipt draining
// instead must honor the total retry/shutdown deadline even on a broken socket.
// Closing a connection with an expired context still closes the underlying
// socket, so an uncommitted acknowledgement cannot be left in a reusable session.
func (e *Engine) quiescenceTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := operationContext(ctx)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, e.database.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := e.CheckSchemaTx(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func callQuiescence(ctx context.Context, tx pgx.Tx, receipt *quiescenceReceipt) (err error) {
	defer func() {
		if recover() != nil {
			err = errQuiescencePanic
		}
	}()
	return receipt.hook(ctx, tx, receipt.attempt)
}

// The caller owns the active slot. Only a successful commit removes a receipt;
// an uncertain commit is retained and the idempotent hook must tolerate replay.
func (e *Engine) acknowledgeQuiescence(ctx context.Context, slot *quiescenceSlot, transact quiescenceTransaction) error {
	e.quiescenceMu.Lock()
	receipt := slot.pending
	e.quiescenceMu.Unlock()
	if receipt == nil {
		return nil
	}
	if err := transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return callQuiescence(ctx, tx, receipt)
	}); err != nil {
		return errors.Join(ErrQuiescencePending, err)
	}
	e.quiescenceMu.Lock()
	slot.pending = nil
	e.quiescenceMu.Unlock()
	return nil
}

// PendingQuiescence counts retained witnesses, including acknowledgement in
// progress. It never infers quiescence from lease, task, or heartbeat state.
func (e *Engine) PendingQuiescence() int {
	e.quiescenceMu.Lock()
	defer e.quiescenceMu.Unlock()
	n := 0
	for _, slot := range e.quiescenceOwners {
		if slot.pending != nil {
			n++
		}
	}
	return n
}

// RetryQuiescence makes one bounded pass over retained, inactive owner slots.
// It performs no claims, executions, Finish calls, or authorization checks. The
// context is the cleanup budget, independent of any executor's canceled context.
// Active slots are left to their current owner; pending witnesses are never
// evicted. A process restart cannot reconstruct lost in-memory witnesses.
func (e *Engine) RetryQuiescence(ctx context.Context) error {
	return e.retryQuiescence(ctx, e.quiescenceTx)
}

func (e *Engine) retryQuiescence(ctx context.Context, transact quiescenceTransaction) error {
	ctx, cancel := operationContext(ctx)
	defer cancel()
	owners := e.quiescenceRetryOwners()
	var errs []error
	for _, owner := range owners {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		e.quiescenceMu.Lock()
		slot := e.quiescenceOwners[owner]
		if slot == nil || slot.active || slot.pending == nil {
			e.quiescenceMu.Unlock()
			continue
		}
		slot.active = true
		// Advance before I/O: an owner that consumes this pass's whole budget
		// cannot starve other retained receipts on every subsequent pass.
		e.quiescenceCursor = owner
		e.quiescenceMu.Unlock()
		if err := e.acknowledgeQuiescence(ctx, slot, transact); err != nil {
			errs = append(errs, err)
		}
		e.releaseOwner(owner, slot)
	}
	if e.PendingQuiescence() != 0 {
		errs = append(errs, ErrQuiescencePending)
	}
	return errors.Join(errs...)
}

func (e *Engine) quiescenceRetryOwners() []string {
	e.quiescenceMu.Lock()
	defer e.quiescenceMu.Unlock()
	owners := make([]string, 0, len(e.quiescenceOwners))
	for owner, slot := range e.quiescenceOwners {
		if !slot.active && slot.pending != nil {
			owners = append(owners, owner)
		}
	}
	sort.Strings(owners)
	start := sort.Search(len(owners), func(i int) bool { return owners[i] > e.quiescenceCursor })
	if start > 0 && start < len(owners) {
		rotated := append([]string(nil), owners[start:]...)
		return append(rotated, owners[:start]...)
	}
	return owners
}

func (e *Engine) drainQuiescence(ctx context.Context, interval time.Duration) error {
	for e.PendingQuiescence() != 0 {
		_ = e.RetryQuiescence(ctx)
		if e.PendingQuiescence() == 0 {
			return nil
		}
		if !pause(ctx, interval) {
			return errors.Join(ErrQuiescencePending, ctx.Err())
		}
	}
	return nil
}
