//go:build integration

package tasks

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"testing"
)

func TestSingleAttemptLostWorkerCannotBeReplayedOrRecovered(t *testing.T) {
	f := newTaskFixture(t)
	k := syntheticTaskKind(1, syntheticTaskSuccess)
	k.ReplaySafe = false
	k.SingleAttemptOnly = true
	k.RetryCodes = nil
	e := f.engine(k)
	f.grant(taskTestPrincipal, "domain-a", k.Name)
	original := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "single-start"))
	lease := f.start(e, "single-worker")
	f.exec("UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1", original.ID)
	restarted := f.engine(k)
	if n, err := restarted.RecoverExpired(f.ctx); err != nil || n != 1 {
		t.Fatalf("recovery %d %v", n, err)
	}
	state := f.requireState(original.ID, Failed, 1)
	if state.Error != "executor_lost" {
		t.Fatal("lost external result misrepresented", state.Error)
	}
	if _, err := restarted.Claim(f.ctx, "replacement"); !errors.Is(err, ErrNoTask) {
		t.Fatal("lost operation replayed", err)
	}
	if _, err := e.Finish(f.ctx, lease, Outcome{State: Succeeded}); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old worker retained authority", err)
	}
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	err := f.transact(func(tx pgx.Tx) error {
		_, err := restarted.RecoverTx(f.ctx, tx, taskTestPrincipal, original.ID, "forbidden-recovery")
		return err
	})
	var problem *Error
	if !errors.As(err, &problem) || problem.Code != "task_not_recoverable" {
		t.Fatal("single attempt recovered", err)
	}
	afterTasks, afterEvents, afterOutbox := f.counts()
	if beforeTasks != afterTasks || beforeEvents != afterEvents || beforeOutbox != afterOutbox {
		t.Fatal("denied recovery mutated state")
	}
	replay, err := f.submit(restarted, taskTestPrincipal, syntheticTaskInput("domain-a", "single-start"))
	if err != nil || !replay.Replayed || replay.Task.ID != original.ID {
		t.Fatal("same key created another operation", err)
	}
	fresh := f.mustSubmit(restarted, taskTestPrincipal, syntheticTaskInput("domain-a", "explicit-fresh-key"))
	if fresh.ID == original.ID {
		t.Fatal("explicit fresh operation reused old task")
	}
}
