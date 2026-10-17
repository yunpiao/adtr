//go:build integration && adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestFinalQuiesceIntegrationBlockedStatementCommitAndClosePrecedeCancellation(t *testing.T) {
	ctx, stopFixture := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopFixture()
	root, cancelRoot := context.WithCancel(ctx)
	defer cancelRoot()
	config := integrationConfig(t)
	observer, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("owned PostgreSQL observer failed")
	}
	defer observer.Close(context.Background())
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("fixture identity failed")
	}
	key := int64(binary.BigEndian.Uint64(nonce[:]) & 0x7fffffffffffffff)
	barrier, err := observer.Begin(ctx)
	if err != nil {
		t.Fatal("independent lock transaction failed")
	}
	defer barrier.Rollback(context.Background())
	if _, err := barrier.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", key); err != nil {
		t.Fatal("independent fixture lock failed")
	}

	g := installTestGate(t)
	g.application, g.witness = true, &diagnosticWitness{}
	g.changed = make(chan struct{})
	// Synthetic authorization is only the registry precondition for this test.
	// The blocked query, transaction commit, physical close and PG observation
	// below are real; controller ACK transport has separate socket tests.
	g.seal = &sealAttempt{epoch: 1, released: true, captureEnded: true, recoveryAuthorized: true}
	ObserveCIWorkerContext(root)
	var calls atomic.Int32
	ConfigureCIShutdown(func() { calls.Add(1); cancelRoot() })
	workerConfig := config.Copy()
	applicationName := "adtr-final-quiesce-" + hex.EncodeToString(nonce[:])
	workerConfig.RuntimeParams["application_name"] = applicationName
	worker, err := ConnectConfig(root, workerConfig)
	if err != nil {
		t.Fatal("owned worker connection failed")
	}
	tx, err := worker.Begin(root)
	if err != nil {
		_ = worker.Close(context.Background())
		t.Fatal("worker transaction failed")
	}
	workerPID := worker.raw.PgConn().PID()
	queryReturned, commitReturned, closeReturned := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	allowCommit, allowClose, workerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var commitOnce, closeOnce sync.Once
	releaseCommit := func() { commitOnce.Do(func() { close(allowCommit) }) }
	releaseClose := func() { closeOnce.Do(func() { close(allowClose) }) }
	defer func() {
		// Release every fixture latch and lock before joining or touching Conn.
		releaseCommit()
		releaseClose()
		_ = barrier.Rollback(context.Background())
		g.abortFinalQuiesce()
		cancelRoot()
		select {
		case <-workerDone:
			_ = worker.Close(context.Background())
		case <-time.After(5 * time.Second):
			t.Error("worker fixture failed to join; connection not used concurrently")
		}
	}()
	go func() {
		defer close(workerDone)
		_, queryErr := tx.Exec(root, "SELECT pg_advisory_xact_lock($1)", key)
		queryReturned <- queryErr
		<-allowCommit
		commitErr := queryErr
		if queryErr == nil {
			commitErr = tx.Commit(root)
		} else {
			_ = tx.Rollback(context.Background())
		}
		commitReturned <- commitErr
		<-allowClose
		closeReturned <- worker.Close(ctx)
	}()

	blocked := false
	for !blocked && ctx.Err() == nil {
		if barrier.QueryRow(ctx, "SELECT $2::int=ANY(pg_blocking_pids($1::int))", workerPID, observer.PgConn().PID()).Scan(&blocked) != nil {
			t.Fatal("independent worker blocking observation failed")
		}
		if !blocked {
			time.Sleep(time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("worker statement was not independently observed blocked")
	}
	select {
	case <-queryReturned:
		t.Fatal("worker statement returned while independent lock was held")
	default:
	}
	prepared := startFinalQuiesceTestPrepare(t, g, 5000)
	waitFinalQuiesceTestState(t, g, "parking")
	assertFinalQuiesceTestWaiting(t, g, prepared, &calls)
	if barrier.Rollback(ctx) != nil {
		t.Fatal("independent lock release failed")
	}
	select {
	case err := <-queryReturned:
		if err != nil {
			t.Fatal("admitted statement was cancelled instead of finishing")
		}
	case <-ctx.Done():
		t.Fatal("released worker statement did not return")
	}
	assertFinalQuiesceTestWaiting(t, g, prepared, &calls)
	releaseCommit()
	select {
	case err := <-commitReturned:
		if err != nil {
			t.Fatal("admitted COMMIT failed before root cancellation")
		}
	case <-ctx.Done():
		t.Fatal("worker COMMIT did not return")
	}
	assertFinalQuiesceTestWaiting(t, g, prepared, &calls)
	releaseClose()
	select {
	case err := <-closeReturned:
		if err != nil {
			t.Fatal("committed worker connection failed clean managed Close")
		}
	case <-ctx.Done():
		t.Fatal("worker managed Close did not return")
	}
	select {
	case err := <-prepared:
		if err != nil {
			t.Fatal("cleanly drained worker did not permit prepare acknowledgment")
		}
	case <-ctx.Done():
		t.Fatal("cleanly closed worker did not unblock final prepare")
	}
	if g.invalid || g.open != 0 || g.pending != 0 || !g.finalParked || g.finalQuiesce.state != "drained" || root.Err() != nil || calls.Load() != 0 {
		t.Fatal("prepare acknowledgment cancelled or reopened the drained worker")
	}
	assertOwnedBackendsGone := func(stage string) {
		t.Helper()
		remaining := -1
		for ctx.Err() == nil {
			if observer.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND datname=current_database()", applicationName).Scan(&remaining) != nil {
				t.Fatalf("independent owned backend observation failed %s", stage)
			}
			if remaining == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if remaining != 0 {
			t.Fatalf("owned worker backend remained %s", stage)
		}
	}
	// Independently establish owned PG zero after prepare and before cancel.
	// This bounded fixture poll does not model the production guard's sampling
	// policy; it proves the evidence can precede cancellation of the live root.
	assertOwnedBackendsGone("before final cancellation")
	if root.Err() != nil || calls.Load() != 0 || !g.finalParked {
		t.Fatal("independent pre-cancel backend proof occurred after root cancellation")
	}
	if g.cancelFinalQuiesce() != nil || root.Err() != context.Canceled || !g.finalParked || g.finalQuiesce.state != "cancelled" || calls.Load() != 1 || g.invalid {
		t.Fatal("final cancellation did not preserve the acknowledgment admission barrier")
	}
	// Model successful ACK completion here; actual bounded transport and its
	// cleanup lock are exercised by the controller-specific tests.
	if g.completeFinalQuiesce() != nil || g.finalParked || g.finalQuiesce.state != "complete" || g.invalid {
		t.Fatal("acknowledged final cancellation did not complete admission cleanup")
	}
	g.finishRecoveryCleanup()
	if g.invalid || !g.recoveryFinalized || g.recoveryQualified != 0 || g.firstConnectFailure != nil || g.firstUncleanClose != nil || g.witness.(*diagnosticWitness).stores != 0 {
		t.Fatal("clean final shutdown produced sticky or cancellation-close failure")
	}

	assertOwnedBackendsGone("after clean final shutdown")
}
