//go:build integration && adtr_ci_dbadmission

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const shutdownSharedGateSQL = "SELECT pg_advisory_xact_lock_shared($1::bigint)"

type shutdownTraceKey struct{}
type shutdownQueryStart struct{ pid uint32 }
type shutdownQueryEnd struct {
	pid                     uint32
	cancelled, nativeClosed bool
	cleanup                 <-chan struct{}
}
type shutdownQueryTracer struct {
	starts chan shutdownQueryStart
	ends   chan shutdownQueryEnd
}

func (t *shutdownQueryTracer) TraceQueryStart(ctx context.Context, c *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if data.SQL != shutdownSharedGateSQL {
		return ctx
	}
	t.starts <- shutdownQueryStart{c.PgConn().PID()}
	return context.WithValue(ctx, shutdownTraceKey{}, true)
}
func (t *shutdownQueryTracer) TraceQueryEnd(ctx context.Context, c *pgx.Conn, data pgx.TraceQueryEndData) {
	if ctx.Value(shutdownTraceKey{}) != true {
		return
	}
	// This callback runs before Engine.transaction's rollback/managed Close.
	// Reading IsClosed only after Run returned would also include normal Close.
	t.ends <- shutdownQueryEnd{c.PgConn().PID(), errors.Is(data.Err, context.Canceled), c.IsClosed(), c.PgConn().CleanupDone()}
}

// This is a normal Worker.Run cancellation counterexample, not a capture test.
// It installs no controller and does not change or excuse the current CI gate's
// unclean-close verdict. It establishes whether normal worker cancellation can
// already have closed native connections before the managed Close is reached.
func TestActivityCandidateWorkerShutdownNativeCleanup(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("PostgreSQL configuration required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("PostgreSQL configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("PostgreSQL unavailable")
	}
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		admin.Close(ctx)
		t.Fatal("fixture identity")
	}
	name := "adtr_ci_shutdown_" + hex.EncodeToString(nonce[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		admin.Close(ctx)
		t.Fatal("fixture database")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(cleanup, "DROP DATABASE "+quoted); e != nil {
			t.Error("owned database cleanup")
		}
		admin.Close(cleanup)
	}()
	config = config.Copy()
	config.Database = name
	if store.Migrate(ctx, config) != nil {
		t.Fatal("actual migration")
	}
	probe, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("observer connection")
	}
	defer probe.Close(context.Background())
	locker, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("barrier connection")
	}
	defer locker.Close(context.Background())
	barrier, err := locker.Begin(ctx)
	if err != nil {
		t.Fatal("barrier transaction")
	}
	defer barrier.Rollback(context.Background())
	if _, err = barrier.Exec(ctx, "SELECT pg_advisory_xact_lock($1::bigint)", int64(734192801)); err != nil {
		t.Fatal("barrier acquisition")
	}
	trace := &shutdownQueryTracer{make(chan shutdownQueryStart, 16), make(chan shutdownQueryEnd, 16)}
	workerConfig := config.Copy()
	workerConfig.RuntimeParams["application_name"] = "adtr-ci-normal-shutdown"
	workerConfig.Tracer = trace
	var authorizations, cycleErrors atomic.Int32
	engine, err := tasks.New(workerConfig, tasks.ProductionRegistry(), func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		authorizations.Add(1)
		return "", tasks.ErrAuthorization
	}, store.SchemaVersion)
	if err != nil {
		t.Fatal("worker engine")
	}
	worker, err := tasks.NewWorker(engine, tasks.WorkerConfig{Owner: "shutdown-proof", Concurrency: 4, PollInterval: 250 * time.Millisecond, OnError: func(error) { cycleErrors.Add(1) }})
	if err != nil {
		t.Fatal("worker configuration")
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()
	joined := false
	defer func() {
		stopWorker()
		// Release the fixture lock even when an assertion fails before cancellation.
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = barrier.Rollback(cleanup)
		if !joined {
			select {
			case <-done:
			case <-cleanup.Done():
				t.Error("worker cleanup join deadline")
			}
		}
	}()
	// Four Claim calls and the recovery loop must all be in actual server-side
	// shared-lock waits; trace start alone is not evidence of dispatch.
	observationDeadline := time.Now().Add(3 * time.Second)
	var total, blocked int
	for time.Now().Before(observationDeadline) {
		err = probe.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='active' AND wait_event_type='Lock' AND wait_event='advisory' AND $1::int=ANY(pg_blocking_pids(pid))) FROM pg_stat_activity WHERE datname=current_database() AND application_name='adtr-ci-normal-shutdown'`, locker.PgConn().PID()).Scan(&total, &blocked)
		if err != nil {
			t.Fatal("worker query observation")
		}
		if total == 5 && blocked == 5 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if total != 5 || blocked != 5 {
		t.Fatal("five real worker queries did not block")
	}
	pids := make(map[uint32]bool, 5)
	for i := 0; i < 5; i++ {
		select {
		case start := <-trace.starts:
			if start.pid == 0 || pids[start.pid] {
				t.Fatal("worker connection identity not unique")
			}
			pids[start.pid] = true
		default:
			t.Fatal("dispatch trace incomplete")
		}
	}
	stopWorker()
	select {
	case err = <-done:
		joined = true
	case <-ctx.Done():
		t.Fatal("worker cancellation join deadline")
	}
	if err != nil || cycleErrors.Load() != 0 || authorizations.Load() != 0 || engine.PendingQuiescence() != 0 {
		t.Fatal("ordinary worker shutdown was not successful")
	}
	observed := make(map[uint32]bool, 5)
	for i := 0; i < 5; i++ {
		select {
		case end := <-trace.ends:
			if !pids[end.pid] || observed[end.pid] || !end.cancelled || !end.nativeClosed {
				t.Fatal("cancellation did not precede native close observation")
			}
			observed[end.pid] = true
			select {
			case <-end.cleanup:
			case <-ctx.Done():
				t.Fatal("native resource cleanup deadline")
			}
		default:
			t.Fatal("query completion trace incomplete")
		}
	}
	if len(trace.starts) != 0 || len(trace.ends) != 0 {
		t.Fatal("unexpected extra worker query")
	}
	// Full backend disappearance is separate from IsClosed and CleanupDone.
	remaining := 1
	for ctx.Err() == nil {
		if err = probe.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name='adtr-ci-normal-shutdown'`).Scan(&remaining); err != nil {
			t.Fatal("final backend observation")
		}
		if remaining == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if remaining != 0 {
		t.Fatal("worker backend cleanup incomplete")
	}
	var taskCount int
	if probe.QueryRow(ctx, "SELECT count(*) FROM adtr.tasks").Scan(&taskCount) != nil || taskCount != 0 {
		t.Fatal("empty queue changed")
	}
}
