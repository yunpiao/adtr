package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type WorkerConfig struct {
	Owner        string
	Concurrency  int
	PollInterval time.Duration
	OnError      func(error)
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
	if c.Concurrency < 1 || c.Concurrency > 32 || c.PollInterval < 10*time.Millisecond || c.PollInterval > 5*time.Second {
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
				worked, err := w.engine.RunOne(ctx, owner)
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
		if _, err := w.engine.RecoverExpired(ctx); err != nil && ctx.Err() == nil && w.config.OnError != nil {
			w.config.OnError(errors.New("task lease recovery failed"))
		}
		if !pause(ctx, w.config.PollInterval) {
			break
		}
	}
	wg.Wait()
	return nil
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

// RunOne is also the process-independent execution entrypoint used by contract
// tests. A task failure is a persisted outcome, not a worker-process failure.
func (e *Engine) RunOne(ctx context.Context, owner string) (bool, error) {
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
	ex := Execution{Task: lease.Task, Probe: e.probe, Checkpoint: func(c context.Context, p int, cursor, result json.RawMessage, v int64) (int64, error) {
		return e.Progress(c, stable, p, cursor, result, v)
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
			if stopReason == "" && executionCtx.Err() != nil {
				if ctx.Err() != nil {
					stopReason = "shutdown"
				} else {
					stopReason = "timeout"
				}
			}
			if lost {
				return true, ErrLeaseLost
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
			// Shutdown cancellation must not prevent a stopped executor's durable ack.
			finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err = e.Finish(finishCtx, stable, outcome)
			finishCancel()
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
