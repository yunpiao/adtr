//go:build integration

package systemhealth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestActualWorkerActivityPersistsProgressFailureAndRestart(t *testing.T) {
	f := newHealthFixture(t)
	f.exec(tasks.Schema)
	engine, err := tasks.New(f.cfg, tasks.ProductionRegistry(), func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		return "synthetic-epoch", nil
	}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.transact(func(tx pgx.Tx) error {
		_, err := engine.SubmitTx(f.ctx, tx, tasks.Principal{TenantID: OwnerTenantID, ActorID: 1}, tasks.SubmitInput{TaskName: "infrastructure.health", DomainID: "platform", PayloadVersion: 1, Payload: []byte(`{}`), IdempotencyKey: "worker-evidence"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	events := make(chan tasks.WorkerActivity, 256)
	failures := make(chan error, 256)
	start := func(id string) (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(f.ctx)
		w, err := tasks.NewWorker(engine, tasks.WorkerConfig{Owner: id, Concurrency: 1, PollInterval: 50 * time.Millisecond, OnActivity: func(ctx context.Context, a tasks.WorkerActivity) {
			err := f.store.RecordWorkerCycle(ctx, a.WorkerID, WorkerCycle(a.Cycle), WorkerCycleStatus(a.Status), a.Code)
			if err != nil && ctx.Err() == nil {
				select {
				case failures <- err:
				default:
				}
			}
			select {
			case events <- a:
			default:
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		return cancel, done
	}
	wait := func(match func(tasks.WorkerActivity) bool) {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case err := <-failures:
				t.Fatal("actual emitter rejected by store", err)
			case e := <-events:
				if match(e) {
					return
				}
			case <-timer.C:
				t.Fatal("worker observation missing")
			}
		}
	}
	read := func() WorkerActivity {
		t.Helper()
		var v WorkerActivity
		if err := f.transact(func(tx pgx.Tx) (err error) { v, err = f.store.WorkerActivityTx(f.ctx, tx, OwnerTenantID); return err }); err != nil {
			t.Fatal(err)
		}
		return v
	}
	cancel, done := start("worker-contract")
	defer cancel()
	wait(func(a tasks.WorkerActivity) bool { return a.Cycle == "queue" && a.Status == "progress" })
	wait(func(a tasks.WorkerActivity) bool { return a.Cycle == "queue" && a.Status == "success" })
	wait(func(a tasks.WorkerActivity) bool { return a.Cycle == "recovery" && a.Status == "success" })
	if read().Availability != Available {
		t.Fatal("actual worker not healthy")
	}
	// Break only this test-owned queue relation, keeping monitoring storage live.
	f.exec("ALTER TABLE adtr.tasks RENAME TO synthetic_missing_queue")
	restored := false
	defer func() {
		if !restored {
			f.exec("ALTER TABLE adtr.synthetic_missing_queue RENAME TO tasks")
		}
	}()
	wait(func(a tasks.WorkerActivity) bool { return a.Cycle == "queue" && a.Status == "failure" })
	state := read()
	if state.Availability == Available || !strings.Contains(state.Reason, "failed") {
		t.Fatal("failed queue retained healthy state", state)
	}
	f.exec("ALTER TABLE adtr.synthetic_missing_queue RENAME TO tasks")
	restored = true
	wait(func(a tasks.WorkerActivity) bool { return a.Cycle == "queue" && a.Status == "success" })
	wait(func(a tasks.WorkerActivity) bool { return a.Cycle == "recovery" && a.Status == "success" })
	if read().Availability != Available {
		t.Fatal("restored queue did not recover")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(WorkerStaleAfter + time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	if read().Availability == Available {
		t.Fatal("stopped worker remained healthy")
	}
	cancel2, done2 := start("worker-restarted")
	defer cancel2()
	wait(func(a tasks.WorkerActivity) bool {
		return a.WorkerID == "worker-restarted" && a.Cycle == "queue" && a.Status == "success"
	})
	wait(func(a tasks.WorkerActivity) bool {
		return a.WorkerID == "worker-restarted" && a.Cycle == "recovery" && a.Status == "success"
	})
	if read().Availability != Available {
		t.Fatal("restarted worker did not recover")
	}
	cancel2()
	if err = <-done2; err != nil {
		t.Fatal(err)
	}
}
