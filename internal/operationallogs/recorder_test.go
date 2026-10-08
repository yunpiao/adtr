package operationallogs

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type appendFunc func(context.Context, Event, RecorderObservation) (Event, error)

func (f appendFunc) Append(ctx context.Context, e Event, o RecorderObservation) (Event, error) {
	return f(ctx, e, o)
}
func testEvent(t *testing.T) Event {
	t.Helper()
	id, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	return Event{SchemaVersion: 1, EventID: id, ProcessID: pid, Module: API, Code: ServiceStartRequested, Outcome: Attempted, Severity: Info, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}
}
func recorded(e Event) Event { e.RecordedAt = time.Now().UTC().Truncate(time.Microsecond); return e }
func closeRecorder(t *testing.T, r *Recorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func newTestRecorder(t *testing.T, s EventStore) *Recorder {
	t.Helper()
	r, err := NewRecorder(s, Worker)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("recorder did not reach expected operation")
	}
}

func TestRecorderBoundedQueueAndAdmissionCounters(t *testing.T) {
	var events []Event
	r := newTestRecorder(t, appendFunc(func(_ context.Context, e Event, o RecorderObservation) (Event, error) {
		if err := validateObservation(o, e); err != nil {
			t.Error(err)
		}
		events = append(events, e)
		return recorded(e), nil
	}))
	for i := 0; i < QueueCapacity; i++ {
		if !r.Emit(QueueCycle, Completed, NoReason) {
			t.Fatalf("queue rejected event %d", i)
		}
	}
	start := time.Now()
	for i := 0; i < 1000; i++ {
		if r.Emit(QueueCycle, Completed, NoReason) {
			t.Fatal("queue exceeded capacity")
		}
	}
	if time.Since(start) > time.Second {
		t.Fatal("queue overload blocked producer")
	}
	if r.Emit(QueueCycle, Failed, Reason("raw-sensitive-error")) {
		t.Fatal("arbitrary error emitted")
	}
	closeRecorder(t, r)
	stats := r.Stats()
	if len(events) != 128 || stats.Accepted != 128 || stats.Acknowledged != 128 || stats.QueueFull != 1000 || stats.Rejected != 1 || stats.Unacknowledged != 0 || stats.Abandoned != 0 || !stats.AcceptanceStopped {
		t.Fatalf("wrong admission/delivery totals: %+v records=%d", stats, len(events))
	}
	if r.Emit(QueueCycle, Completed, NoReason) || r.Stats().Rejected != 2 {
		t.Fatal("closed recorder accepted producer")
	}
	for _, e := range events {
		if e.ProcessID != stats.ProcessID || e.ObservedAt.Location() != time.UTC || e.ObservedAt.Nanosecond()%1000 != 0 || !validUUID(e.EventID) || !e.RecordedAt.IsZero() {
			t.Fatal("event identity/time contract broken")
		}
	}
}
func TestRecorderOnePendingInAdditionToQueue(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := newTestRecorder(t, appendFunc(func(ctx context.Context, e Event, _ RecorderObservation) (Event, error) {
		once.Do(func() { close(started); <-release })
		return recorded(e), nil
	}))
	r.Emit(QueueCycle, Completed, NoReason)
	go r.Run(context.Background())
	waitSignal(t, started)
	for i := 0; i < 128; i++ {
		if !r.Emit(QueueCycle, Completed, NoReason) {
			t.Fatal("pending event consumed queue capacity")
		}
	}
	if r.Emit(QueueCycle, Completed, NoReason) {
		t.Fatal("more than queue128+pending accepted")
	}
	close(release)
	closeRecorder(t, r)
	if s := r.Stats(); s.Accepted != 129 || s.Acknowledged != 129 || s.QueueFull != 1 {
		t.Fatalf("wrong totals %+v", s)
	}
}
func TestRecorderRetriesIdenticalEventAfterUncertainCommit(t *testing.T) {
	var attempts []Event
	var committed Event
	r := newTestRecorder(t, appendFunc(func(_ context.Context, e Event, _ RecorderObservation) (Event, error) {
		attempts = append(attempts, e)
		if len(attempts) == 1 {
			committed = recorded(e)
			return Event{}, &AppendError{Err: errors.New("response lost"), Uncertain: true}
		}
		return committed, nil
	}))
	r.Emit(QueueCycle, Failed, CycleFailed)
	closeRecorder(t, r)
	if len(attempts) != 2 || !reflect.DeepEqual(attempts[0], attempts[1]) {
		t.Fatal("retry changed identity or observed content")
	}
	s := r.Stats()
	if s.Accepted != 1 || s.Acknowledged != 1 || s.WriteFailures != 1 || s.Unacknowledged != 0 || s.Abandoned != 0 {
		t.Fatalf("uncertain commit was not reconciled by exact replay: %+v", s)
	}
}
func TestRecorderShutdownSeparatesUnacknowledgedAndDefiniteDrops(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "definite_rollback", true: "unknown_commit"}[uncertain], func(t *testing.T) {
			attempted := make(chan struct{})
			var once sync.Once
			r := newTestRecorder(t, appendFunc(func(_ context.Context, _ Event, _ RecorderObservation) (Event, error) {
				once.Do(func() { close(attempted) })
				return Event{}, &AppendError{Err: errors.New("unavailable"), Uncertain: uncertain}
			}))
			r.Emit(QueueCycle, Completed, NoReason)
			r.Emit(QueueCycle, Completed, NoReason)
			go r.Run(context.Background())
			waitSignal(t, attempted)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := r.Close(ctx); err == nil {
				t.Fatal("shutdown timeout claimed success")
			}
			waitSignal(t, r.done)
			s := r.Stats()
			if s.Acknowledged != 0 || s.Accepted != 2 || s.WriteFailures < 1 {
				t.Fatalf("wrong delivery counters %+v", s)
			}
			if uncertain && (s.Unacknowledged != 1 || s.Abandoned != 1) || !uncertain && (s.Unacknowledged != 0 || s.Abandoned != 2) {
				t.Fatalf("definite and uncertain outcomes conflated: %+v", s)
			}
		})
	}
}
func TestRecorderCloseCancelsActiveWriteAndJoins(t *testing.T) {
	started, returned := make(chan struct{}), make(chan struct{})
	r := newTestRecorder(t, appendFunc(func(ctx context.Context, _ Event, _ RecorderObservation) (Event, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > appendTimeout {
			t.Error("write lacks bounded context")
		}
		close(started)
		<-ctx.Done()
		close(returned)
		return Event{}, &AppendError{Err: ctx.Err()}
	}))
	r.Emit(QueueCycle, Completed, NoReason)
	go r.Run(context.Background())
	waitSignal(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := r.Close(ctx); err == nil {
		t.Fatal("cancelled write counted complete")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Close exceeded cooperative shutdown bound")
	}
	waitSignal(t, returned)
	waitSignal(t, r.done)
	if s := r.Stats(); s.Acknowledged != 0 || s.Abandoned != 1 || s.Unacknowledged != 0 {
		t.Fatalf("wrong cancelled-write totals %+v", s)
	}
}
func TestRecorderCloseCannotBeHeldByNoncooperativeCustomWriter(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := newTestRecorder(t, appendFunc(func(_ context.Context, e Event, _ RecorderObservation) (Event, error) {
		once.Do(func() { close(started) })
		<-release
		return Event{}, &AppendError{Err: errors.New("not committed")}
	}))
	r.Emit(QueueCycle, Completed, NoReason)
	go r.Run(context.Background())
	waitSignal(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.Close(ctx)
	elapsed := time.Since(start)
	close(release)
	waitSignal(t, r.done)
	if err == nil || elapsed > time.Second {
		t.Fatalf("unbounded or falsely successful Close: %v %s", err, elapsed)
	}
}
func TestRecorderAcknowledgementMustMatchImmutableContent(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	r := newTestRecorder(t, appendFunc(func(_ context.Context, e Event, _ RecorderObservation) (Event, error) {
		once.Do(func() { close(started) })
		e.Code = ServiceStopped
		return recorded(e), nil
	}))
	r.Emit(QueueCycle, Completed, NoReason)
	go r.Run(context.Background())
	waitSignal(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_ = r.Close(ctx)
	waitSignal(t, r.done)
	if s := r.Stats(); s.Acknowledged != 0 || s.Unacknowledged != 1 || s.WriteFailures < 1 {
		t.Fatalf("mismatched row counted successful: %+v", s)
	}
}
func TestRecorderConcurrentCloseAndEmitAreRaceSafe(t *testing.T) {
	r := newTestRecorder(t, appendFunc(func(_ context.Context, e Event, _ RecorderObservation) (Event, error) { return recorded(e), nil }))
	go r.Run(context.Background())
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r.Emit(QueueCycle, Completed, NoReason)
			}
		}()
	}
	closeRecorder(t, r)
	wg.Wait()
	s := r.Stats()
	if s.Accepted != s.Acknowledged || s.Accepted+s.Rejected+s.QueueFull != 800 || s.Abandoned != 0 || s.Unacknowledged != 0 {
		t.Fatalf("admission race lost observations: %+v", s)
	}
}
func TestRecorderProcessIdentityAndInvalidConfiguration(t *testing.T) {
	store := appendFunc(func(_ context.Context, e Event, _ RecorderObservation) (Event, error) { return recorded(e), nil })
	a := newTestRecorder(t, store)
	b := newTestRecorder(t, store)
	if a.Stats().ProcessID == b.Stats().ProcessID {
		t.Fatal("public process IDs reused")
	}
	closeRecorder(t, a)
	closeRecorder(t, b)
	if _, err := NewRecorder(store, Module("db_syncer")); err == nil {
		t.Fatal("unsupported producer registered")
	}
	if _, err := NewRecorder(nil, API); err == nil {
		t.Fatal("nil store accepted")
	}
}

func TestRecorderStartImmediatelyFollowedByClose(t *testing.T) {
	for i := 0; i < 100; i++ {
		r := newTestRecorder(t, appendFunc(func(_ context.Context, e Event, _ RecorderObservation) (Event, error) { return recorded(e), nil }))
		if err := r.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !r.Emit(QueueCycle, Completed, NoReason) {
			t.Fatal("fresh Start rejected emission")
		}
		closeRecorder(t, r)
		if r.Stats().Acknowledged != 1 {
			t.Fatal("startup/close race lost event")
		}
		if err := r.Start(context.Background()); err == nil {
			t.Fatal("second writer started")
		}
	}
}

func TestObservationRejectsImpossibleCountersAndStoreVersion(t *testing.T) {
	e := testEvent(t)
	base := RecorderObservation{ProcessID: e.ProcessID, Module: e.Module, StartedAt: e.ObservedAt, ObservedAt: e.ObservedAt, Accepted: 2, Acknowledged: 1}
	if err := validateObservation(base, e); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*RecorderObservation){
		func(o *RecorderObservation) { o.ProcessID = "not-a-process" },
		func(o *RecorderObservation) { o.Module = Worker },
		func(o *RecorderObservation) { o.Accepted = 0 },
		func(o *RecorderObservation) { o.Acknowledged = 3 },
		func(o *RecorderObservation) { o.Unacknowledged = 2 },
		func(o *RecorderObservation) { o.Abandoned = 2 },
		func(o *RecorderObservation) { o.QueueFull = -1 },
		func(o *RecorderObservation) { o.WriteFailures = -1 },
		func(o *RecorderObservation) { o.ObservedAt = o.ObservedAt.Add(time.Nanosecond) },
	} {
		o := base
		mutate(&o)
		if validateObservation(o, e) == nil {
			t.Fatalf("invalid observation accepted %+v", o)
		}
	}
	if _, err := NewStore(nil, SchemaVersion); err == nil {
		t.Fatal("nil configuration accepted")
	}
}
