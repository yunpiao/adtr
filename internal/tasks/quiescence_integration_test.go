//go:build integration

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// These tests use the real PostgreSQL fixture and an entirely synthetic module
// table. A committed row represents cleanup acknowledgement, never a task result.
func newQuiescenceFixture(t *testing.T) *taskFixture {
	t.Helper()
	f := newTaskFixture(t)
	f.exec(`CREATE TABLE adtr.synthetic_quiescence (
 task_id text PRIMARY KEY, tenant_id text NOT NULL, domain_id text NOT NULL,
 actor_id bigint NOT NULL, kind text NOT NULL, payload_version integer NOT NULL,
 payload_hash text NOT NULL, owner text NOT NULL, fence bigint NOT NULL,
 attempt integer NOT NULL
)`)
	return f
}

func syntheticQuiescenceKind(execute Executor, hook func(context.Context, pgx.Tx, QuiescedAttempt) error) Kind {
	k := syntheticTaskKind(1, execute)
	k.ReplaySafe = false
	k.SingleAttemptOnly = true
	k.RetryCodes = nil
	k.OnQuiesced = hook
	return k
}

func recordSyntheticQuiescence(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cleanup inherited a canceled executor context: %w", err)
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > operationTimeout {
		return errors.New("cleanup must have its own bounded context")
	}
	_, err := tx.Exec(ctx, `INSERT INTO adtr.synthetic_quiescence
 (task_id,tenant_id,domain_id,actor_id,kind,payload_version,payload_hash,owner,fence,attempt)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(task_id) DO NOTHING`,
		q.TaskID(), q.TenantID(), q.DomainID(), q.ActorID(), q.Kind(),
		q.PayloadVersion(), q.PayloadHash(), q.Owner(), q.Fence(), q.Attempt())
	return err
}

func requireQuiescenceCount(t *testing.T, f *taskFixture, want int) {
	t.Helper()
	var got int
	if err := f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr.synthetic_quiescence").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("committed cleanup rows=%d, want %d", got, want)
	}
}

func requireQuiescenceIdentity(t *testing.T, f *taskFixture, started Task) {
	t.Helper()
	var got Task
	if err := f.db.QueryRow(f.ctx, `SELECT task_id,tenant_id,domain_id,actor_id,kind,
 payload_version,payload_hash,owner,fence,attempt FROM adtr.synthetic_quiescence WHERE task_id=$1`, started.ID).
		Scan(&got.ID, &got.TenantID, &got.DomainID, &got.ActorID, &got.Kind,
			&got.PayloadVersion, &got.PayloadHash, &got.LeaseOwner, &got.FencingToken, &got.Attempt); err != nil {
		t.Fatal(err)
	}
	if got.ID != started.ID || got.TenantID != started.TenantID || got.DomainID != started.DomainID ||
		got.ActorID != started.ActorID || got.Kind != started.Kind || got.PayloadVersion != started.PayloadVersion ||
		got.PayloadHash != started.PayloadHash || got.LeaseOwner != started.LeaseOwner ||
		got.FencingToken != started.FencingToken || got.Attempt != started.Attempt {
		t.Fatalf("cleanup identity differs from started attempt: got=%+v started=%+v", got, started)
	}
}

func quiescenceGate() (<-chan struct{}, func()) {
	gate := make(chan struct{})
	var once sync.Once
	return gate, func() { once.Do(func() { close(gate) }) }
}

func requireNoQuiescence(t *testing.T, f *taskFixture, e *Engine, calls *atomic.Int32, finished <-chan struct{}) {
	t.Helper()
	if calls.Load() != 0 || e.PendingQuiescence() != 0 {
		t.Fatalf("live executor acquired a cleanup receipt: callbacks=%d pending=%d", calls.Load(), e.PendingQuiescence())
	}
	requireQuiescenceCount(t, f, 0)
	select {
	case <-finished:
		t.Fatal("worker returned while executor or its defers remained live")
	default:
	}
}

func TestQuiescenceWaitsForExecutorAndEveryDefer(t *testing.T) {
	for _, scenario := range []struct {
		name string
		want State
		code string
	}{
		{"success", Succeeded, ""},
		{"failure", Failed, "synthetic_failure"},
		{"panic", Failed, "executor_panic"},
		{"cancel", Cancelled, "cancelled"},
		{"timeout", Failed, "execution_timeout"},
		{"shutdown", Failed, "worker_interrupted"},
		{"revoked_actor", Failed, "authorization_revoked"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newQuiescenceFixture(t)
			body, releaseBody := quiescenceGate()
			cleanup, releaseCleanup := quiescenceGate()
			entered, stopped, inDefer, unwound, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls, executions atomic.Int32
			var started Task
			k := syntheticQuiescenceKind(func(ctx context.Context, ex Execution) Outcome {
				executions.Add(1)
				started = ex.Task
				defer close(unwound)
				defer func() { close(inDefer); <-cleanup }()
				close(entered)
				switch scenario.name {
				case "cancel", "timeout", "shutdown", "revoked_actor":
					<-ctx.Done()
					close(stopped)
				default:
					<-body
				}
				if scenario.name == "panic" {
					panic("synthetic executor panic")
				}
				if scenario.name == "success" {
					return Outcome{State: Succeeded, Result: json.RawMessage(`{"completed":true}`)}
				}
				return Outcome{State: Failed, Code: "synthetic_failure"}
			}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
				calls.Add(1)
				select {
				case <-unwound:
				default:
					return errors.New("cleanup callback ran before the executor's last defer")
				}
				return recordSyntheticQuiescence(ctx, tx, q)
			})
			if scenario.name == "timeout" {
				k.Timeout = 250 * time.Millisecond
			}
			e := f.engine(k)
			f.grant(taskTestPrincipal, "domain-a", k.Name)
			task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "quiescence-lifecycle"))
			ctx, cancel := context.WithCancel(f.ctx)
			var worked bool
			var runErr error
			go func() { defer close(finished); worked, runErr = e.RunOne(ctx, "quiescence-worker") }()
			t.Cleanup(func() {
				cancel()
				releaseBody()
				releaseCleanup()
				awaitTaskSignal(t, finished, "quiescence lifecycle cleanup")
			})
			awaitTaskSignal(t, entered, "executor entry")
			requireNoQuiescence(t, f, e, &calls, finished)
			if scenario.name == "success" {
				if worked, err := e.RunOne(f.ctx, "quiescence-worker"); worked || !errors.Is(err, ErrOwnerBusy) {
					t.Fatalf("concurrent owner entered its live slot: worked=%v err=%v", worked, err)
				}
			}
			switch scenario.name {
			case "cancel":
				if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				cancel()
			case "revoked_actor":
				// The actor disappears from the durable authorizer entirely. Cleanup
				// still has to work after both identity and all grants disappear.
				f.exec("DELETE FROM adtr.synthetic_task_grants WHERE tenant_id=$1 AND actor_id=$2", taskTestPrincipal.TenantID, taskTestPrincipal.ActorID)
			case "success", "failure", "panic":
				releaseBody()
			}
			switch scenario.name {
			case "cancel", "timeout", "shutdown", "revoked_actor":
				awaitTaskSignal(t, stopped, "executor cancellation")
			}
			awaitTaskSignal(t, inDefer, "held executor defer")
			requireNoQuiescence(t, f, e, &calls, finished)
			if f.snapshot(task.ID).State.Terminal() {
				t.Fatal("task became terminal before executor defers returned")
			}
			releaseCleanup()
			awaitTaskSignal(t, finished, "executor acknowledgement")
			if !worked || runErr != nil {
				t.Fatalf("worked=%v err=%v", worked, runErr)
			}
			saved := f.requireState(task.ID, scenario.want, 1)
			if saved.Error != scenario.code || saved.LeaseOwner != "" || saved.LeaseUntil != nil {
				t.Fatalf("wrong terminal result after cleanup: %+v", saved)
			}
			if calls.Load() != 1 || executions.Load() != 1 || e.PendingQuiescence() != 0 {
				t.Fatalf("callbacks=%d executions=%d pending=%d", calls.Load(), executions.Load(), e.PendingQuiescence())
			}
			requireQuiescenceCount(t, f, 1)
			requireQuiescenceIdentity(t, f, started)
			if err := e.RetryQuiescence(f.ctx); err != nil || calls.Load() != 1 {
				t.Fatalf("committed receipt was retried: callbacks=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestQuiescenceAfterLostLeasePreservesOriginalIdentityAndTerminalResult(t *testing.T) {
	f := newQuiescenceFixture(t)
	body, releaseBody := quiescenceGate()
	cleanup, releaseCleanup := quiescenceGate()
	entered, stopped, inDefer, unwound, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls, executions atomic.Int32
	var started Task
	k := syntheticQuiescenceKind(func(ctx context.Context, ex Execution) Outcome {
		executions.Add(1)
		started = ex.Task
		defer close(unwound)
		defer func() { close(inDefer); <-cleanup }()
		close(entered)
		<-ctx.Done()
		close(stopped)
		<-body
		// A module may edit its local Task copy, including the payload backing
		// array. No such mutation may forge the engine's original witness.
		ex.Task.ID, ex.Task.TenantID, ex.Task.DomainID = "forged-task", "forged-tenant", "forged-domain"
		ex.Task.ActorID, ex.Task.Kind, ex.Task.PayloadVersion = 99, "forged-kind", 99
		ex.Task.PayloadHash, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt = "forged-hash", "forged-owner", 99, 99
		for i := range ex.Task.Payload {
			ex.Task.Payload[i] = 'x'
		}
		return Outcome{State: Succeeded, Result: json.RawMessage(`{"lateResult":true}`)}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		calls.Add(1)
		select {
		case <-unwound:
		default:
			return errors.New("live executor reported quiescence")
		}
		var state State
		var owner string
		if err := tx.QueryRow(ctx, "SELECT state,lease_owner FROM adtr.tasks WHERE task_id=$1", q.TaskID()).Scan(&state, &owner); err != nil {
			return err
		}
		if state != Failed || owner != "" {
			return fmt.Errorf("cleanup must survive terminal lease recovery: state=%s owner=%q", state, owner)
		}
		return recordSyntheticQuiescence(ctx, tx, q)
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "lost-quiescence"))
	ctx, cancel := context.WithCancel(f.ctx)
	var worked bool
	var runErr error
	go func() { defer close(finished); worked, runErr = e.RunOne(ctx, "lost-quiescence-owner") }()
	t.Cleanup(func() {
		cancel()
		releaseBody()
		releaseCleanup()
		awaitTaskSignal(t, finished, "lost executor cleanup")
	})
	awaitTaskSignal(t, entered, "lost executor start")
	f.exec("UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1", task.ID)
	restarted := f.engine(k)
	if n, err := restarted.RecoverExpired(f.ctx); err != nil || n != 1 {
		t.Fatalf("recovery count=%d err=%v", n, err)
	}
	before := f.requireState(task.ID, Failed, 1)
	if before.Error != "executor_lost" || before.LeaseOwner != "" || before.LeaseUntil != nil {
		t.Fatalf("recovery did not clear the lost executor's authority: %+v", before)
	}
	_, eventsBefore, outboxBefore := f.counts()
	awaitTaskSignal(t, stopped, "lost lease cancellation")
	requireNoQuiescence(t, f, e, &calls, finished)
	if err := restarted.RetryQuiescence(f.ctx); err != nil || restarted.PendingQuiescence() != 0 || calls.Load() != 0 {
		t.Fatalf("another engine inferred executor return from terminal task: pending=%d callbacks=%d err=%v", restarted.PendingQuiescence(), calls.Load(), err)
	}
	releaseBody()
	awaitTaskSignal(t, inDefer, "lost executor held defer")
	requireNoQuiescence(t, f, e, &calls, finished)
	releaseCleanup()
	awaitTaskSignal(t, finished, "lost executor return")
	if !worked || !errors.Is(runErr, ErrLeaseLost) || calls.Load() != 1 || executions.Load() != 1 || e.PendingQuiescence() != 0 {
		t.Fatalf("lost lease acknowledgement: worked=%v err=%v callbacks=%d executions=%d pending=%d", worked, runErr, calls.Load(), executions.Load(), e.PendingQuiescence())
	}
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	after := f.requireState(task.ID, Failed, 1)
	_, eventsAfter, outboxAfter := f.counts()
	if after.Error != before.Error || after.ResultVersion != before.ResultVersion || string(after.Result) != string(before.Result) ||
		after.LeaseOwner != "" || eventsAfter != eventsBefore || outboxAfter != outboxBefore {
		t.Fatalf("late return changed recovered terminal result: before=%+v after=%+v events=%d/%d outbox=%d/%d", before, after, eventsBefore, eventsAfter, outboxBefore, outboxAfter)
	}
}

func TestQuiescenceFailureRollsBackAndRetriesWithoutReexecution(t *testing.T) {
	for _, failure := range []string{"callback_error", "callback_panic", "statement_error", "commit_error"} {
		t.Run(failure, func(t *testing.T) {
			f := newQuiescenceFixture(t)
			if failure == "commit_error" {
				f.exec(`CREATE TABLE adtr.synthetic_quiescence_failure(enabled boolean NOT NULL);
 INSERT INTO adtr.synthetic_quiescence_failure VALUES(true);
 CREATE FUNCTION adtr.synthetic_fail_cleanup_commit() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF (SELECT enabled FROM adtr.synthetic_quiescence_failure) THEN
   RAISE EXCEPTION 'synthetic cleanup commit failure';
  END IF;
  RETURN NEW;
 END; $$;
 CREATE CONSTRAINT TRIGGER synthetic_cleanup_commit AFTER INSERT ON adtr.synthetic_quiescence
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_fail_cleanup_commit()`)
			}
			var calls, executions atomic.Int32
			var permit atomic.Bool
			var started Task
			k := syntheticQuiescenceKind(func(_ context.Context, ex Execution) Outcome {
				if executions.Add(1) == 1 {
					started = ex.Task
				}
				return Outcome{State: Succeeded, Result: json.RawMessage(`{"completed":true}`)}
			}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
				calls.Add(1)
				if err := recordSyntheticQuiescence(ctx, tx, q); err != nil {
					return err
				}
				if !permit.Load() {
					switch failure {
					case "callback_error":
						return errors.New("synthetic transient cleanup failure")
					case "callback_panic":
						panic("synthetic cleanup panic after write")
					case "statement_error":
						_, err := tx.Exec(ctx, "SELECT 1/0")
						return err
					}
				}
				return nil
			})
			e := f.engine(k)
			f.grant(taskTestPrincipal, "domain-a", k.Name)
			first := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "failed-cleanup"))
			if worked, err := e.RunOne(f.ctx, "retry-quiescence-owner"); !worked || !errors.Is(err, ErrQuiescencePending) {
				t.Fatalf("failed acknowledgement not retained: worked=%v err=%v", worked, err)
			}
			// Callback failure is independent of the ordinary fenced Finish path.
			f.requireState(first.ID, Succeeded, 1)
			requireQuiescenceCount(t, f, 0)
			if calls.Load() != 1 || executions.Load() != 1 || e.PendingQuiescence() != 1 {
				t.Fatalf("callbacks=%d executions=%d pending=%d", calls.Load(), executions.Load(), e.PendingQuiescence())
			}
			second := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "blocked-behind-cleanup"))
			if worked, err := e.RunOne(f.ctx, "retry-quiescence-owner"); worked || !errors.Is(err, ErrQuiescencePending) {
				t.Fatalf("owner claimed over failed receipt: worked=%v err=%v", worked, err)
			}
			f.requireState(second.ID, Queued, 0)
			before := f.snapshot(first.ID)
			beforeTasks, beforeEvents, beforeOutbox := f.counts()
			if err := e.RetryQuiescence(f.ctx); !errors.Is(err, ErrQuiescencePending) {
				t.Fatalf("failed retry lost its receipt: %v", err)
			}
			requireQuiescenceCount(t, f, 0)
			if calls.Load() != 3 || executions.Load() != 1 || e.PendingQuiescence() != 1 {
				t.Fatalf("failed retry callbacks=%d executions=%d pending=%d", calls.Load(), executions.Load(), e.PendingQuiescence())
			}
			permit.Store(true)
			if failure == "commit_error" {
				f.exec("UPDATE adtr.synthetic_quiescence_failure SET enabled=false")
			}
			if err := e.RetryQuiescence(f.ctx); err != nil {
				t.Fatal(err)
			}
			requireQuiescenceCount(t, f, 1)
			requireQuiescenceIdentity(t, f, started)
			f.requireState(second.ID, Queued, 0)
			after := f.requireState(first.ID, Succeeded, 1)
			afterTasks, afterEvents, afterOutbox := f.counts()
			if calls.Load() != 4 || executions.Load() != 1 || e.PendingQuiescence() != 0 ||
				after.ResultVersion != before.ResultVersion || string(after.Result) != string(before.Result) ||
				afterTasks != beforeTasks || afterEvents != beforeEvents || afterOutbox != beforeOutbox {
				t.Fatalf("retry changed task execution/history: callbacks=%d executions=%d pending=%d before=%+v after=%+v", calls.Load(), executions.Load(), e.PendingQuiescence(), before, after)
			}
			if err := e.RetryQuiescence(f.ctx); err != nil || calls.Load() != 4 {
				t.Fatalf("successful acknowledgement repeated: calls=%d err=%v", calls.Load(), err)
			}
			if worked, err := e.RunOne(f.ctx, "retry-quiescence-owner"); !worked || err != nil {
				t.Fatalf("acknowledged owner did not resume claims: worked=%v err=%v", worked, err)
			}
			f.requireState(second.ID, Succeeded, 1)
			if executions.Load() != 2 || calls.Load() != 5 {
				t.Fatalf("unexpected task replay: executions=%d callbacks=%d", executions.Load(), calls.Load())
			}
			requireQuiescenceCount(t, f, 2)
		})
	}
}

func TestQuiescenceSucceedsEvenWhenFinishRollsBack(t *testing.T) {
	f := newQuiescenceFixture(t)
	f.exec(`CREATE FUNCTION adtr.synthetic_reject_finish() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic Finish persistence failure'; END; $$;
 CREATE TRIGGER synthetic_reject_finish BEFORE UPDATE ON adtr.tasks
 FOR EACH ROW WHEN (NEW.state='succeeded') EXECUTE FUNCTION adtr.synthetic_reject_finish()`)
	var calls, executions atomic.Int32
	var started Task
	k := syntheticQuiescenceKind(func(_ context.Context, ex Execution) Outcome {
		executions.Add(1)
		started = ex.Task
		return Outcome{State: Succeeded, Result: json.RawMessage(`{"mustNotCommit":true}`)}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		calls.Add(1)
		return recordSyntheticQuiescence(ctx, tx, q)
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "finish-rollback"))
	if worked, err := e.RunOne(f.ctx, "finish-rollback-owner"); !worked || err == nil || errors.Is(err, ErrQuiescencePending) {
		t.Fatalf("Finish failure suppressed independent acknowledgement: worked=%v err=%v", worked, err)
	}
	saved := f.requireState(task.ID, Running, 1)
	if saved.Progress != 0 || string(saved.Result) != "{}" || saved.LeaseOwner != started.LeaseOwner {
		t.Fatalf("failed Finish partially committed: %+v", saved)
	}
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	if err := e.RetryQuiescence(f.ctx); err != nil || e.PendingQuiescence() != 0 || calls.Load() != 1 || executions.Load() != 1 {
		t.Fatalf("successful acknowledgement was retained or replayed: err=%v pending=%d callbacks=%d executions=%d", err, e.PendingQuiescence(), calls.Load(), executions.Load())
	}
	f.requireState(task.ID, Running, 1)
	afterTasks, afterEvents, afterOutbox := f.counts()
	if beforeTasks != afterTasks || beforeEvents != afterEvents || beforeOutbox != afterOutbox {
		t.Fatal("quiescence retry retried Finish or changed task history")
	}
}

func TestQuiescenceSameOwnerCommitsReceiptBeforeNextExecution(t *testing.T) {
	f := newQuiescenceFixture(t)
	var executions, calls atomic.Int32
	var permit atomic.Bool
	var firstStarted Task
	k := syntheticQuiescenceKind(func(ctx context.Context, ex Execution) Outcome {
		if executions.Add(1) == 1 {
			firstStarted = ex.Task
			return Outcome{State: Succeeded}
		}
		// Reading from a separate connection requires that acknowledgement of
		// the prior attempt committed before this owner could start new work.
		conn, err := pgx.ConnectConfig(ctx, f.cfg.Copy())
		if err != nil {
			return Outcome{State: Failed, Code: "synthetic_verification_failed"}
		}
		defer conn.Close(context.Background())
		var acknowledged bool
		err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.synthetic_quiescence WHERE task_id=$1)", firstStarted.ID).Scan(&acknowledged)
		if err != nil || !acknowledged {
			return Outcome{State: Failed, Code: "synthetic_unacknowledged_predecessor"}
		}
		return Outcome{State: Succeeded}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		calls.Add(1)
		if err := recordSyntheticQuiescence(ctx, tx, q); err != nil {
			return err
		}
		if !permit.Load() {
			return errors.New("synthetic transient cleanup failure")
		}
		return nil
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	first := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "same-owner-first"))
	if worked, err := e.RunOne(f.ctx, "same-owner-retry"); !worked || !errors.Is(err, ErrQuiescencePending) {
		t.Fatalf("first acknowledgement did not remain pending: worked=%v err=%v", worked, err)
	}
	f.requireState(first.ID, Succeeded, 1)
	second := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "same-owner-second"))
	permit.Store(true)
	if worked, err := e.RunOne(f.ctx, "same-owner-retry"); !worked || err != nil {
		t.Fatalf("same-owner retry failed: worked=%v err=%v", worked, err)
	}
	f.requireState(second.ID, Succeeded, 1)
	if executions.Load() != 2 || calls.Load() != 3 || e.PendingQuiescence() != 0 {
		t.Fatalf("same-owner retry replayed or lost work: executions=%d callbacks=%d pending=%d", executions.Load(), calls.Load(), e.PendingQuiescence())
	}
	requireQuiescenceCount(t, f, 2)
	requireQuiescenceIdentity(t, f, firstStarted)
}

// Drop only the server response to one traced COMMIT, after Read actually
// receives it. The real server commits the write, while pgx sees a transport
// failure. This exercises uncertainty rather than a transaction that rolled back.
type syntheticCommitReplyLoss struct {
	armed, traced, dropped atomic.Bool
	previous               pgx.QueryTracer
}

func (loss *syntheticCommitReplyLoss) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if loss.previous != nil {
		ctx = loss.previous.TraceQueryStart(ctx, conn, data)
	}
	if data.SQL == "commit" && loss.traced.CompareAndSwap(false, true) {
		loss.armed.Store(true)
	}
	return ctx
}

func (loss *syntheticCommitReplyLoss) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if loss.previous != nil {
		loss.previous.TraceQueryEnd(ctx, conn, data)
	}
}

type syntheticCommitReplyConn struct {
	net.Conn
	loss *syntheticCommitReplyLoss
}

func (conn *syntheticCommitReplyConn) Read(buf []byte) (int, error) {
	n, err := conn.Conn.Read(buf)
	if n > 0 && conn.loss.armed.CompareAndSwap(true, false) {
		conn.loss.dropped.Store(true)
		return 0, io.EOF
	}
	return n, err
}

func TestQuiescenceRetainsReceiptAfterCommittedResponseIsLost(t *testing.T) {
	f := newQuiescenceFixture(t)
	var calls, executions atomic.Int32
	var started Task
	k := syntheticQuiescenceKind(func(_ context.Context, ex Execution) Outcome {
		executions.Add(1)
		started = ex.Task
		return Outcome{State: Succeeded, Result: json.RawMessage(`{"completed":true}`)}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		n := calls.Add(1)
		if err := recordSyntheticQuiescence(ctx, tx, q); err != nil {
			return err
		}
		if n == 1 {
			return errors.New("synthetic initial callback rollback")
		}
		return nil
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "uncertain-commit"))
	if worked, err := e.RunOne(f.ctx, "uncertain-commit-owner"); !worked || !errors.Is(err, ErrQuiescencePending) {
		t.Fatalf("initial rollback did not retain receipt: worked=%v err=%v", worked, err)
	}
	before := f.requireState(task.ID, Succeeded, 1)
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	requireQuiescenceCount(t, f, 0)
	original := e.database
	faulty := original.Copy()
	loss := &syntheticCommitReplyLoss{previous: faulty.Tracer}
	faulty.Tracer = loss
	dial := faulty.DialFunc
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	faulty.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &syntheticCommitReplyConn{Conn: conn, loss: loss}, nil
	}
	// No worker is active during these retries; only this engine's next
	// transaction uses the wrapper, and the fixture connection stays intact.
	e.database = faulty
	defer func() { e.database = original }()
	err := e.RetryQuiescence(f.ctx)
	e.database = original
	if !errors.Is(err, ErrQuiescencePending) || !loss.traced.Load() || !loss.dropped.Load() || e.PendingQuiescence() != 1 {
		t.Fatalf("commit response loss not retained: err=%v traced=%v dropped=%v pending=%d", err, loss.traced.Load(), loss.dropped.Load(), e.PendingQuiescence())
	}
	// A separate real connection proves the callback write committed even
	// though the engine did not receive its COMMIT acknowledgement.
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	if calls.Load() != 2 || executions.Load() != 1 {
		t.Fatalf("uncertain commit callbacks=%d executions=%d", calls.Load(), executions.Load())
	}
	if err := e.RetryQuiescence(f.ctx); err != nil || e.PendingQuiescence() != 0 {
		t.Fatalf("idempotent retry did not clear receipt: err=%v pending=%d", err, e.PendingQuiescence())
	}
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	after := f.requireState(task.ID, Succeeded, 1)
	afterTasks, afterEvents, afterOutbox := f.counts()
	if calls.Load() != 3 || executions.Load() != 1 || after.ResultVersion != before.ResultVersion ||
		string(after.Result) != string(before.Result) || beforeTasks != afterTasks || beforeEvents != afterEvents || beforeOutbox != afterOutbox {
		t.Fatalf("uncertain cleanup replayed executor or Finish: callbacks=%d executions=%d before=%+v after=%+v", calls.Load(), executions.Load(), before, after)
	}
}

func TestQuiescenceNeverAcknowledgesAnExecutorThatDidNotStart(t *testing.T) {
	for _, scenario := range []string{"revoked_before_start", "cancelled_before_claim"} {
		t.Run(scenario, func(t *testing.T) {
			f := newQuiescenceFixture(t)
			var calls, executions atomic.Int32
			k := syntheticQuiescenceKind(func(context.Context, Execution) Outcome {
				executions.Add(1)
				return Outcome{State: Succeeded}
			}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
				calls.Add(1)
				return recordSyntheticQuiescence(ctx, tx, q)
			})
			e := f.engine(k)
			f.grant(taskTestPrincipal, "domain-a", k.Name)
			task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "never-started"))
			want := Failed
			if scenario == "revoked_before_start" {
				f.exec("DELETE FROM adtr.synthetic_task_grants")
			} else {
				want = Cancelled
				if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.RunOne(f.ctx, "never-started-owner"); err != nil {
				t.Fatal(err)
			}
			if err := e.RetryQuiescence(f.ctx); err != nil || e.PendingQuiescence() != 0 || calls.Load() != 0 || executions.Load() != 0 {
				t.Fatalf("unstarted task received witness: err=%v pending=%d callbacks=%d executions=%d", err, e.PendingQuiescence(), calls.Load(), executions.Load())
			}
			f.requireState(task.ID, want, 0)
			requireQuiescenceCount(t, f, 0)
		})
	}
}

func TestQuiescenceRequiresExactSchemaEvenAfterExecutorReturned(t *testing.T) {
	f := newQuiescenceFixture(t)
	body, release := quiescenceGate()
	entered, finished := make(chan struct{}), make(chan struct{})
	var calls, executions atomic.Int32
	var started Task
	k := syntheticQuiescenceKind(func(_ context.Context, ex Execution) Outcome {
		executions.Add(1)
		started = ex.Task
		close(entered)
		<-body
		return Outcome{State: Succeeded, Result: json.RawMessage(`{"uncommitted":true}`)}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		calls.Add(1)
		return recordSyntheticQuiescence(ctx, tx, q)
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "schema-gated-cleanup"))
	var runErr error
	go func() { defer close(finished); _, runErr = e.RunOne(f.ctx, "schema-quiescence-owner") }()
	t.Cleanup(func() { release(); awaitTaskSignal(t, finished, "schema-gated executor cleanup") })
	awaitTaskSignal(t, entered, "schema-gated executor start")
	before := f.requireState(task.ID, Running, 1)
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	f.exec("UPDATE adtr.schema_version SET version=4")
	release()
	awaitTaskSignal(t, finished, "schema-gated executor return")
	if !errors.Is(runErr, ErrSchemaIncompatible) || !errors.Is(runErr, ErrQuiescencePending) || e.PendingQuiescence() != 1 || calls.Load() != 0 {
		t.Fatalf("initial cleanup crossed incompatible schema: pending=%d callbacks=%d err=%v", e.PendingQuiescence(), calls.Load(), runErr)
	}
	for _, version := range []int{4, 6, 0} {
		if version == 0 {
			f.exec("DELETE FROM adtr.schema_version")
		} else {
			f.exec("UPDATE adtr.schema_version SET version=$1", version)
		}
		if err := e.RetryQuiescence(f.ctx); !errors.Is(err, ErrSchemaIncompatible) || !errors.Is(err, ErrQuiescencePending) {
			t.Fatalf("version=%d cleanup retry crossed schema gate: %v", version, err)
		}
		if worked, err := e.RunOne(f.ctx, "schema-quiescence-owner"); worked || !errors.Is(err, ErrSchemaIncompatible) {
			t.Fatalf("version=%d owner crossed schema gate: worked=%v err=%v", version, worked, err)
		}
		if calls.Load() != 0 || executions.Load() != 1 || e.PendingQuiescence() != 1 {
			t.Fatalf("version=%d callbacks=%d executions=%d pending=%d", version, calls.Load(), executions.Load(), e.PendingQuiescence())
		}
		requireQuiescenceCount(t, f, 0)
	}
	f.exec("INSERT INTO adtr.schema_version VALUES(true,5)")
	// Revoking access after the return must not obstruct a compatible retry.
	f.exec("DELETE FROM adtr.synthetic_task_grants")
	if err := e.RetryQuiescence(f.ctx); err != nil {
		t.Fatal(err)
	}
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	after := f.requireState(task.ID, Running, 1)
	afterTasks, afterEvents, afterOutbox := f.counts()
	if calls.Load() != 1 || executions.Load() != 1 || e.PendingQuiescence() != 0 ||
		after.ResultVersion != before.ResultVersion || string(after.Result) != string(before.Result) ||
		afterTasks != beforeTasks || afterEvents != beforeEvents || afterOutbox != beforeOutbox {
		t.Fatalf("retry replayed execution or Finish: callbacks=%d executions=%d pending=%d before=%+v after=%+v", calls.Load(), executions.Load(), e.PendingQuiescence(), before, after)
	}
}

func TestQuiescenceRetryHonorsCallerDeadlineAndMigrationLock(t *testing.T) {
	f := newQuiescenceFixture(t)
	var calls, executions, mode atomic.Int32
	var started Task
	k := syntheticQuiescenceKind(func(_ context.Context, ex Execution) Outcome {
		executions.Add(1)
		started = ex.Task
		return Outcome{State: Succeeded}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		calls.Add(1)
		if err := recordSyntheticQuiescence(ctx, tx, q); err != nil {
			return err
		}
		switch mode.Load() {
		case 0:
			return errors.New("synthetic initial cleanup failure")
		case 1:
			_, err := tx.Exec(ctx, "SELECT pg_sleep(10)")
			return err
		default:
			return nil
		}
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "bounded-cleanup"))
	if worked, err := e.RunOne(f.ctx, "bounded-cleanup-owner"); !worked || !errors.Is(err, ErrQuiescencePending) {
		t.Fatalf("initial cleanup failure not retained: worked=%v err=%v", worked, err)
	}
	before := f.requireState(task.ID, Succeeded, 1)
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	mode.Store(1)
	bounded, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
	begin := time.Now()
	err := e.RetryQuiescence(bounded)
	elapsed := time.Since(begin)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrQuiescencePending) || elapsed >= 2*time.Second || calls.Load() != 2 || e.PendingQuiescence() != 1 {
		t.Fatalf("slow cleanup exceeded caller budget or lost receipt: err=%v elapsed=%s callbacks=%d pending=%d", err, elapsed, calls.Load(), e.PendingQuiescence())
	}
	requireQuiescenceCount(t, f, 0)
	// A cleanup transaction shares the migration gate even though it bypasses
	// actor/grant/lease authorization. The exclusive migrator lock blocks the
	// callback itself, and its acquisition obeys the same total caller budget.
	migration, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer migration.Rollback(context.Background())
	if _, err := migration.Exec(f.ctx, "SELECT pg_advisory_xact_lock($1::bigint)", migrationLockID); err != nil {
		t.Fatal(err)
	}
	mode.Store(2)
	bounded, cancel = context.WithTimeout(f.ctx, 100*time.Millisecond)
	begin = time.Now()
	err = e.RetryQuiescence(bounded)
	elapsed = time.Since(begin)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrQuiescencePending) || elapsed >= 2*time.Second || calls.Load() != 2 || e.PendingQuiescence() != 1 {
		t.Fatalf("cleanup crossed migration gate or exceeded budget: err=%v elapsed=%s callbacks=%d pending=%d", err, elapsed, calls.Load(), e.PendingQuiescence())
	}
	if err := migration.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	requireQuiescenceCount(t, f, 0)
	if err := e.RetryQuiescence(f.ctx); err != nil || e.PendingQuiescence() != 0 {
		t.Fatalf("compatible bounded retry did not resume: err=%v pending=%d", err, e.PendingQuiescence())
	}
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	after := f.requireState(task.ID, Succeeded, 1)
	afterTasks, afterEvents, afterOutbox := f.counts()
	if calls.Load() != 3 || executions.Load() != 1 || before.ResultVersion != after.ResultVersion ||
		beforeTasks != afterTasks || beforeEvents != afterEvents || beforeOutbox != afterOutbox {
		t.Fatalf("bounded retry repeated execution or Finish: callbacks=%d executions=%d before=%+v after=%+v", calls.Load(), executions.Load(), before, after)
	}
}

func TestWorkerShutdownJoinsExecutorThenDrainsPendingQuiescence(t *testing.T) {
	f := newQuiescenceFixture(t)
	cleanup, releaseCleanup := quiescenceGate()
	acknowledge, releaseAcknowledgement := quiescenceGate()
	entered, inDefer, unwound, retryEntered, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls, executions atomic.Int32
	var started Task
	k := syntheticQuiescenceKind(func(ctx context.Context, ex Execution) Outcome {
		executions.Add(1)
		started = ex.Task
		defer close(unwound)
		defer func() { close(inDefer); <-cleanup }()
		close(entered)
		<-ctx.Done()
		return Outcome{State: Failed, Code: "synthetic_stopped"}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		n := calls.Add(1)
		select {
		case <-unwound:
		default:
			return errors.New("worker cleanup preceded executor defers")
		}
		if err := recordSyntheticQuiescence(ctx, tx, q); err != nil {
			return err
		}
		if n == 1 {
			return errors.New("synthetic first acknowledgement failure")
		}
		if n == 2 {
			close(retryEntered)
		}
		select {
		case <-acknowledge:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	first := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "shutdown-in-flight"))
	w, err := NewWorker(e, WorkerConfig{Owner: "quiescence-shutdown", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	var runErr error
	go func() { defer close(finished); runErr = w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		releaseCleanup()
		releaseAcknowledgement()
		awaitTaskSignal(t, finished, "worker drain cleanup")
	})
	awaitTaskSignal(t, entered, "worker executor start")
	second := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "shutdown-remains-queued"))
	cancel()
	awaitTaskSignal(t, inDefer, "shutdown held executor defer")
	requireNoQuiescence(t, f, e, &calls, finished)
	f.requireState(first.ID, Running, 1)
	f.requireState(second.ID, Queued, 0)
	releaseCleanup()
	awaitTaskSignal(t, retryEntered, "shutdown retry after callback rollback")
	if e.PendingQuiescence() != 1 || calls.Load() != 2 || executions.Load() != 1 {
		t.Fatalf("shutdown did not retain and retry receipt: pending=%d callbacks=%d executions=%d", e.PendingQuiescence(), calls.Load(), executions.Load())
	}
	requireQuiescenceCount(t, f, 0)
	select {
	case <-finished:
		t.Fatal("worker returned before pending acknowledgement committed")
	default:
	}
	firstSaved := f.requireState(first.ID, Failed, 1)
	if firstSaved.Error != "worker_interrupted" {
		t.Fatalf("shutdown outcome=%s", firstSaved.Error)
	}
	releaseAcknowledgement()
	awaitTaskSignal(t, finished, "worker shutdown drain")
	if runErr != nil || e.PendingQuiescence() != 0 || executions.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("shutdown returned before cleanup: err=%v pending=%d executions=%d callbacks=%d", runErr, e.PendingQuiescence(), executions.Load(), calls.Load())
	}
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	f.requireState(second.ID, Queued, 0)
}

func TestWorkerShutdownRetainsQuiescenceWhenDrainBudgetExpires(t *testing.T) {
	f := newQuiescenceFixture(t)
	var calls, executions atomic.Int32
	var permit atomic.Bool
	var started Task
	entered, finished := make(chan struct{}), make(chan struct{})
	k := syntheticQuiescenceKind(func(ctx context.Context, ex Execution) Outcome {
		executions.Add(1)
		started = ex.Task
		close(entered)
		<-ctx.Done()
		return Outcome{State: Failed, Code: "synthetic_stopped"}
	}, func(ctx context.Context, tx pgx.Tx, q QuiescedAttempt) error {
		calls.Add(1)
		if err := recordSyntheticQuiescence(ctx, tx, q); err != nil {
			return err
		}
		if !permit.Load() {
			return errors.New("synthetic cleanup remains unavailable")
		}
		return nil
	})
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "shutdown-pending-cleanup"))
	w, err := NewWorker(e, WorkerConfig{Owner: "pending-shutdown", Concurrency: 1, PollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	var runErr error
	go func() { defer close(finished); runErr = w.Run(ctx) }()
	awaitDrain := func() {
		t.Helper()
		timer := time.NewTimer(quiescenceDrainTimeout + 5*time.Second)
		defer timer.Stop()
		select {
		case <-finished:
		case <-timer.C:
			t.Fatal("worker did not return within its quiescence drain budget")
		}
	}
	t.Cleanup(func() { permit.Store(true); cancel(); awaitDrain() })
	awaitTaskSignal(t, entered, "persistent-failure worker start")
	stopStarted := time.Now()
	cancel()
	awaitDrain()
	if !errors.Is(runErr, ErrQuiescencePending) || e.PendingQuiescence() != 1 || executions.Load() != 1 || calls.Load() < 2 {
		t.Fatalf("shutdown lost pending acknowledgement: err=%v pending=%d executions=%d callbacks=%d", runErr, e.PendingQuiescence(), executions.Load(), calls.Load())
	}
	if time.Since(stopStarted) < quiescenceDrainTimeout {
		t.Fatal("worker abandoned pending cleanup before its retry budget expired")
	}
	requireQuiescenceCount(t, f, 0)
	before := f.requireState(task.ID, Failed, 1)
	if before.Error != "worker_interrupted" {
		t.Fatalf("shutdown result=%s", before.Error)
	}
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	beforeCalls := calls.Load()
	permit.Store(true)
	if err := e.RetryQuiescence(f.ctx); err != nil || e.PendingQuiescence() != 0 {
		t.Fatalf("receipt could not resume after drain timeout: err=%v pending=%d", err, e.PendingQuiescence())
	}
	requireQuiescenceCount(t, f, 1)
	requireQuiescenceIdentity(t, f, started)
	after := f.requireState(task.ID, Failed, 1)
	afterTasks, afterEvents, afterOutbox := f.counts()
	if executions.Load() != 1 || calls.Load() != beforeCalls+1 || before.ResultVersion != after.ResultVersion ||
		beforeTasks != afterTasks || beforeEvents != afterEvents || beforeOutbox != afterOutbox {
		t.Fatalf("post-shutdown retry repeated execution or Finish: executions=%d callbacks=%d/%d before=%+v after=%+v", executions.Load(), beforeCalls, calls.Load(), before, after)
	}
}
