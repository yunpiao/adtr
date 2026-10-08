package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type WorkerActivity struct{ WorkerID, Cycle, Status, Code string }

type WorkerConfig struct {
	Owner        string
	Concurrency  int
	PollInterval time.Duration
	OnError      func(error)
	OnActivity   func(context.Context, WorkerActivity)
}
type Worker struct {
	engine *Engine
	config WorkerConfig
}

func NewWorker(e *Engine, c WorkerConfig) (*Worker, error) {
	if e == nil || !identifier.MatchString(c.Owner) || len(c.Owner) > 110 {
		return nil, errors.New("worker needs valid engine and owner")
	}
	if c.Concurrency == 0 {
		c.Concurrency = 4
	}
	if c.PollInterval == 0 {
		c.PollInterval = 250 * time.Millisecond
	}
	if c.Concurrency < 1 || c.Concurrency > maxQuiescenceOwners || c.PollInterval < 10*time.Millisecond || c.PollInterval > 5*time.Second {
		return nil, errors.New("invalid worker limits")
	}
	return &Worker{e, c}, nil
}

// Run stops accepting work on cancellation and waits for every in-process
// executor to return. No task is acknowledged stopped before that return. The
// production registry contains only context-aware pgx reads, never subprocesses.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < w.config.Concurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			owner := fmt.Sprintf("%s-%d", w.config.Owner, slot)
			delay := w.config.PollInterval
			for ctx.Err() == nil {
				worked, err := w.engine.runOne(ctx, owner, func(c context.Context) { w.activity(c, "queue", "progress", "") })
				if err == nil {
					w.activity(ctx, "queue", "success", "")
				} else {
					w.activity(ctx, "queue", "failure", "cycle_failed")
				}
				if err != nil && ctx.Err() == nil && w.config.OnError != nil {
					w.config.OnError(errors.New("task worker cycle failed"))
				}
				if worked && err == nil {
					delay = w.config.PollInterval
					continue
				}
				if !pause(ctx, delay) {
					return
				}
				if err != nil && delay < 5*time.Second {
					delay *= 2
					if delay > 5*time.Second {
						delay = 5 * time.Second
					}
				} else if err == nil {
					delay = w.config.PollInterval
				}
			}
		}(i)
	}
	for ctx.Err() == nil {
		if w.engine.PendingQuiescence() != 0 {
			if err := w.engine.RetryQuiescence(ctx); err != nil && ctx.Err() == nil && w.config.OnError != nil {
				w.config.OnError(errors.New("task quiescence acknowledgement failed"))
			}
		}
		_, recoveryErr := w.engine.RecoverExpired(ctx)
		if recoveryErr == nil {
			w.activity(ctx, "recovery", "success", "")
		} else {
			w.activity(ctx, "recovery", "failure", "cycle_failed")
		}
		if recoveryErr != nil && ctx.Err() == nil && w.config.OnError != nil {
			w.config.OnError(errors.New("task lease recovery failed"))
		}
		if !pause(ctx, w.config.PollInterval) {
			break
		}
	}
	wg.Wait()
	// Cancellation stops claims first. Joining has no timeout: a live executor
	// can never be called stopped merely because the cleanup budget expired.
	cleanup, cancel := context.WithTimeout(context.Background(), quiescenceDrainTimeout)
	defer cancel()
	return w.engine.drainQuiescence(cleanup, w.config.PollInterval)
}
func (w *Worker) activity(ctx context.Context, cycle, status, code string) {
	if ctx.Err() != nil || w.config.OnActivity == nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	w.config.OnActivity(bounded, WorkerActivity{w.config.Owner, cycle, status, code})
}

func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func runExecutor(ctx context.Context, k Kind, ex Execution) (out Outcome) {
	defer func() {
		if recover() != nil {
			out = Outcome{State: Failed, Code: "executor_panic"}
		}
	}()
	return k.Execute(ctx, ex)
}

// RunOne is the standalone execution entrypoint, sharing Worker's bounded owner
// reservations and acknowledgement retention. A pending receipt is retried before
// this owner may claim again. A task failure remains a persisted outcome; an
// acknowledgement failure is returned separately and never rewrites that outcome.
func (e *Engine) RunOne(ctx context.Context, owner string) (bool, error) {
	return e.runOne(ctx, owner, nil)
}

func (e *Engine) runOne(ctx context.Context, owner string, activity func(context.Context)) (bool, error) {
	slot, err := e.acquireOwner(owner)
	if err != nil {
		return false, err
	}
	defer e.releaseOwner(owner, slot)
	// A failed receipt applies backpressure to its owner before any new claim.
	ackCtx, ackCancel := operationContext(context.Background())
	err = e.acknowledgeQuiescence(ackCtx, slot, e.quiescenceTx)
	ackCancel()
	if err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	lease, err := e.Claim(ctx, owner)
	if errors.Is(err, ErrNoTask) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lease, err = e.Start(ctx, lease)
	if errors.Is(err, ErrAuthorization) || errors.Is(err, ErrNoTask) || errors.Is(err, ErrLeaseLost) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	k := e.registry.kinds[lease.Task.Kind]
	// Execution uses the caller cancellation and a frozen per-kind time budget.
	executionCtx, cancel := context.WithTimeout(ctx, k.Timeout)
	defer cancel()
	done := make(chan Outcome, 1)
	// Capture a stable immutable lease; checkpoint calls may run concurrently
	// with the heartbeat without sharing mutable lease state.
	stable := lease
	ex := Execution{Task: lease.Task, Probe: e.probe, WithTx: func(c context.Context, v int64, work FencedWork) (int64, error) {
		version, err := e.WithTx(c, stable, v, work)
		if err == nil && activity != nil {
			activity(c)
		}
		return version, err
	}, Checkpoint: func(c context.Context, p int, cursor, result json.RawMessage, v int64) (int64, error) {
		version, err := e.Progress(c, stable, p, cursor, result, v)
		if err == nil && activity != nil {
			activity(c)
		}
		return version, err
	}}
	go func() { done <- runExecutor(executionCtx, k, ex) }()
	ticker := time.NewTicker(k.Heartbeat)
	defer ticker.Stop()
	stopReason := ""
	lost := false
	executionDone := executionCtx.Done()
	for {
		select {
		case outcome := <-done:
			if k.OnQuiesced != nil {
				// runExecutor and all executor defers have returned. Reserve the
				// exact witness before attempting either independent transaction.
				e.retainQuiescence(slot, stable, k)
			}
			if stopReason == "" && executionCtx.Err() != nil {
				if ctx.Err() != nil {
					stopReason = "shutdown"
				} else {
					stopReason = "timeout"
				}
			}
			if outcome.State == "" {
				outcome = Outcome{State: Failed, Code: "invalid_executor_outcome"}
			}
			if outcome.State != Succeeded {
				switch stopReason {
				case "cancel":
					outcome.State = Cancelled
					outcome.Code = "cancelled"
					outcome.Retryable = false
				case "authorization":
					outcome.State = Failed
					outcome.Code = "authorization_revoked"
					outcome.Retryable = false
				case "timeout":
					outcome.State = Failed
					outcome.Code = "execution_timeout"
					outcome.Retryable = false
				case "shutdown":
					outcome.State = Failed
					outcome.Code = "worker_interrupted"
					outcome.Retryable = true
				}
			}
			// Persist a legitimate result while its lease is still valid. A slow
			// cleanup transaction must not itself expire the lease before Finish.
			// Both transactions outlive executor/shutdown cancellation, and neither
			// transaction's failure suppresses the other.
			err = ErrLeaseLost
			if !lost {
				finishCtx, finishCancel := operationContext(context.Background())
				_, err = e.Finish(finishCtx, stable, outcome)
				finishCancel()
			}
			var ackErr error
			if k.OnQuiesced != nil {
				ackCtx, ackCancel := operationContext(context.Background())
				ackErr = e.acknowledgeQuiescence(ackCtx, slot, e.quiescenceTx)
				ackCancel()
			}
			// Even a known lost lease reaches OnQuiesced before this return;
			// cleanup uses recorded attempt identity, never current lease state.
			if ackErr != nil {
				return true, errors.Join(err, ackErr)
			}
			return true, err
		case <-executionDone:
			executionDone = nil
			if stopReason == "" {
				if ctx.Err() != nil {
					stopReason = "shutdown"
				} else {
					stopReason = "timeout"
				}
			}
			cancel()
		case <-ticker.C:
			heartbeatCtx, heartbeatCancel := context.WithTimeout(context.Background(), min(k.Heartbeat, 5*time.Second))
			_, herr := e.Heartbeat(heartbeatCtx, stable)
			heartbeatCancel()
			switch {
			case herr == nil:
				if activity != nil {
					activity(executionCtx)
				}
			case errors.Is(herr, ErrCancelRequested):
				stopReason = "cancel"
				cancel()
			case errors.Is(herr, ErrAuthorization):
				stopReason = "authorization"
				cancel()
			default: // Fail closed on any lease uncertainty; never report cancellation.
				lost = true
				cancel()
			}
		}
	}
}
