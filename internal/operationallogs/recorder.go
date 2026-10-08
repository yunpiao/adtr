package operationallogs

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"
)

const retryInitial = 50 * time.Millisecond
const retryCap = time.Second
const closeJoinBudget = 100 * time.Millisecond
const closeTimeout = 5 * time.Second

// Recorder has 128 queued events and at most one pending event. Emit never
// waits for storage. EventStore must cooperate with cancellation; Store does.
// Retrying a write always uses the same event ID and observed content.
type Recorder struct {
	store       EventStore
	mu          sync.Mutex
	observation RecorderObservation
	queue       chan Event
	stop        chan struct{}
	done        chan struct{}
	started     bool
	cancel      context.CancelFunc
	runErr      error
}

func NewRecorder(store EventStore, module Module) (*Recorder, error) {
	if store == nil || (module != API && module != Worker) {
		return nil, problem(500, "invalid_configuration")
	}
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	return &Recorder{store: store, observation: RecorderObservation{ProcessID: id, Module: module, StartedAt: now, ObservedAt: now}, queue: make(chan Event, QueueCapacity), stop: make(chan struct{}), done: make(chan struct{})}, nil
}
func increment(n *int64) {
	if *n < math.MaxInt64 {
		*n++
	}
}
func (r *Recorder) Emit(code EventCode, outcome Outcome, reason Reason) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	severity, valid := eventSeverity(r.observation.Module, code, outcome, reason)
	if r.observation.AcceptanceStopped || !valid {
		increment(&r.observation.Rejected)
		return false
	}
	id, err := newUUID()
	if err != nil {
		increment(&r.observation.Rejected)
		return false
	}
	e := Event{SchemaVersion: 1, EventID: id, ProcessID: r.observation.ProcessID, Module: r.observation.Module, Code: code, Outcome: outcome, Severity: severity, Reason: reason, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}
	select {
	case r.queue <- e:
		increment(&r.observation.Accepted)
		return true
	default:
		increment(&r.observation.QueueFull)
		return false
	}
}
func (r *Recorder) Stats() RecorderObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.observation
	out.ObservedAt = time.Now().UTC().Truncate(time.Microsecond)
	return out
}
func (r *Recorder) stopLocked() {
	if !r.observation.AcceptanceStopped {
		r.observation.AcceptanceStopped = true
		close(r.stop)
	}
}
func (r *Recorder) startLocked(ctx context.Context) context.Context {
	r.started = true
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	return runCtx
}
func (r *Recorder) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return problem(409, "recorder_already_started")
	}
	runCtx := r.startLocked(ctx)
	r.mu.Unlock()
	return r.run(runCtx)
}

// Start atomically claims the single writer before returning. Runtime startup
// may immediately fail and call Close without racing an unscheduled Run call.
func (r *Recorder) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return problem(409, "recorder_already_started")
	}
	runCtx := r.startLocked(ctx)
	go r.run(runCtx)
	return nil
}
func (r *Recorder) finish(err error, pending bool, uncertain bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	if pending {
		if uncertain {
			increment(&r.observation.Unacknowledged)
		} else {
			increment(&r.observation.Abandoned)
		}
	}
	for {
		select {
		case <-r.queue:
			increment(&r.observation.Abandoned)
		default:
			r.runErr = err
			r.cancel()
			close(r.done)
			return err
		}
	}
}
func (r *Recorder) run(ctx context.Context) error {
	var pending Event
	hasPending, uncertain := false, false
	backoff := retryInitial
	for {
		if err := ctx.Err(); err != nil {
			return r.finish(problem(503, "journal_timeout"), hasPending, uncertain)
		}
		if !hasPending {
			select {
			case pending = <-r.queue:
				hasPending = true
			default:
				select {
				case pending = <-r.queue:
					hasPending = true
				case <-r.stop:
					// Close and Emit serialize on mu, so once stopped no new
					// event can appear after this final nonblocking drain.
					select {
					case pending = <-r.queue:
						hasPending = true
					default:
						return r.finish(nil, false, false)
					}
				case <-ctx.Done():
					return r.finish(problem(503, "journal_timeout"), false, false)
				}
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, appendTimeout)
		stored, err := r.store.Append(attemptCtx, pending, r.Stats())
		cancel()
		if err == nil && (!sameEvent(pending, stored) || validateEvent(stored, true) != nil) {
			err = &AppendError{Err: problem(503, "invalid_append_acknowledgement"), Uncertain: true}
		}
		if err == nil {
			r.mu.Lock()
			increment(&r.observation.Acknowledged)
			r.mu.Unlock()
			hasPending = false
			uncertain = false
			backoff = retryInitial
			continue
		}
		r.mu.Lock()
		increment(&r.observation.WriteFailures)
		r.mu.Unlock()
		var failure *AppendError
		if !errors.As(err, &failure) || failure.Uncertain {
			uncertain = true
		}
		if terminalAppendError(err) {
			r.mu.Lock()
			if uncertain {
				increment(&r.observation.Unacknowledged)
			} else {
				increment(&r.observation.Abandoned)
			}
			r.mu.Unlock()
			hasPending = false
			uncertain = false
			backoff = retryInitial
			continue
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return r.finish(problem(503, "journal_timeout"), hasPending, uncertain)
		}
		backoff *= 2
		if backoff > retryCap {
			backoff = retryCap
		}
	}
}

// Close stops admission and drains within the caller's budget. On expiry it
// cancels an active write and joins a cooperative writer. A noncooperative
// custom EventStore cannot hold Close indefinitely; it produces a timeout.
func (r *Recorder) Close(ctx context.Context) error {
	ctx, stopClose := context.WithTimeout(ctx, closeTimeout)
	defer stopClose()
	r.mu.Lock()
	r.stopLocked()
	if !r.started {
		runCtx := r.startLocked(context.Background())
		go r.run(runCtx)
	}
	cancel := r.cancel
	r.mu.Unlock()
	select {
	case <-r.done:
		r.mu.Lock()
		err := r.runErr
		r.mu.Unlock()
		return err
	case <-ctx.Done():
		cancel()
		timer := time.NewTimer(closeJoinBudget)
		defer timer.Stop()
		select {
		case <-r.done:
		case <-timer.C:
		}
		return problem(503, "journal_close_timeout")
	}
}
