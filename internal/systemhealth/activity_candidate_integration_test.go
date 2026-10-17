//go:build integration && adtr_ci_dbadmission

package systemhealth

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestActivityCandidateHealthOnlyFourSlots(t *testing.T) {
	for _, kindName := range []string{"computer_assets.v3.export", "group_assets.v3.export", "user_assets.v3.export"} {
		t.Run(kindName, func(t *testing.T) {
			f := newHealthFixture(t)
			f.exec(tasks.Schema)
			started := make(chan struct{}, 4)
			releaseWork := make(chan struct{})
			kind := tasks.Kind{Name: kindName, Version: 1, Platform: true, MaxAttempts: 1,
				Lease: 30 * time.Second, Heartbeat: time.Second, Timeout: 10 * time.Second,
				RetryBase: 10 * time.Millisecond, RetryCap: time.Second, ReplaySafe: true,
				Validate: func(json.RawMessage) (json.RawMessage, error) { return []byte(`{}`), nil },
				Execute: func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
					started <- struct{}{}
					select {
					case <-releaseWork:
					case <-ctx.Done():
						return tasks.Outcome{State: tasks.Failed, Code: "synthetic_failure"}
					}
					_, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
						return 10, []byte(`{}`), []byte(`{}`), nil
					})
					if err != nil {
						return tasks.Outcome{State: tasks.Failed, Code: "synthetic_failure"}
					}
					return tasks.Outcome{State: tasks.Succeeded, Result: []byte(`{}`)}
				},
			}
			registry, err := tasks.NewRegistry(kind)
			if err != nil {
				t.Fatal("candidate registry failed")
			}
			engine, err := tasks.New(f.cfg, registry, func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
				return "synthetic-epoch", nil
			}, 7)
			if err != nil {
				t.Fatal("candidate engine failed")
			}
			for _, key := range []string{"one", "two", "three", "four"} {
				if err := f.transact(func(tx pgx.Tx) error {
					_, err := engine.SubmitTx(f.ctx, tx, tasks.Principal{TenantID: OwnerTenantID, ActorID: 1}, tasks.SubmitInput{TaskName: kindName, DomainID: "platform", PayloadVersion: 1, Payload: []byte(`{}`), IdempotencyKey: key})
					return err
				}); err != nil {
					t.Fatal("candidate submission failed")
				}
			}
			results := make(chan bool, 4)
			ctx, cancel := context.WithCancel(f.ctx)
			worker, err := tasks.NewWorker(engine, tasks.WorkerConfig{Owner: "candidate-worker", Concurrency: 4, PollInterval: 50 * time.Millisecond,
				OnActivityCandidate: func(ctx context.Context, a tasks.WorkerActivity, candidate tasks.ActivityCandidate) {
					if candidate == nil {
						return
					}
					// Neither an ordinary recorder call nor an invalid activity status
					// can spend or re-label this dedicated candidate.
					ordinaryErr := f.store.RecordWorkerCycle(ctx, a.WorkerID, WorkerQueue, WorkerCycleProgress, "")
					invalidErr := f.store.RecordWorkerCycleCandidate(ctx, a.WorkerID, WorkerRecovery, WorkerCycleSuccess, "", candidate)
					boundErr := f.store.RecordWorkerCycleCandidate(ctx, a.WorkerID, WorkerQueue, WorkerCycleProgress, "", candidate)
					reusedErr := f.store.RecordWorkerCycleCandidate(ctx, a.WorkerID, WorkerQueue, WorkerCycleProgress, "", candidate)
					results <- ordinaryErr == nil && invalidErr != nil && boundErr == nil && reusedErr != nil
				},
			})
			if err != nil {
				cancel()
				t.Fatal("candidate worker failed")
			}
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() { cancel(); <-done }()
			for count := 0; count < 4; count++ {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("four concurrent slots did not start")
				}
			}
			close(releaseWork)
			for count := 0; count < 4; count++ {
				select {
				case ok := <-results:
					if !ok {
						t.Fatal("health candidate isolation failed")
					}
				case <-time.After(15 * time.Second):
					t.Fatal("four candidate callbacks were not observed")
				}
			}
		})
	}
}
