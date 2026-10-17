//go:build integration && adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestQueuedParkIntegrationPhysicalDrainPrecedesReadyAndNewAdmission(t *testing.T) {
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
	ObserveCIWorkerContext(root)
	// Only application/controller installation is synthetic. The actual queued
	// APIs, blocked PostgreSQL query, COMMIT, native Close and independent PG
	// observation are exercised. No process STOP or controller wire is simulated.
	workerConfig := config.Copy()
	applicationName := "adtr-queued-park-" + hex.EncodeToString(nonce[:])
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
		releaseCommit()
		releaseClose()
		_ = barrier.Rollback(context.Background())
		g.abortQueuedSelection()
		cancelRoot()
		select {
		case <-workerDone:
			_ = worker.Close(context.Background())
		case <-time.After(5 * time.Second):
			t.Error("worker fixture did not join; connection not used concurrently")
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
	prepared := startQueuedParkTestPrepare(t, g, 2000)
	waitQueuedParkTestState(t, g, "parking")
	assertQueuedParkTestWaiting(t, g, prepared)
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
	assertQueuedParkTestWaiting(t, g, prepared)
	releaseCommit()
	select {
	case err := <-commitReturned:
		if err != nil {
			t.Fatal("admitted COMMIT failed during queued parking")
		}
	case <-ctx.Done():
		t.Fatal("worker COMMIT did not return")
	}
	assertQueuedParkTestWaiting(t, g, prepared)
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
			t.Fatal("clean physical drain rejected queued readiness")
		}
	case <-ctx.Done():
		t.Fatal("clean physical drain did not unblock queued readiness")
	}
	if g.invalid || g.selection != 1 || !g.queuedParked || g.queuedState != "drained" || g.open != 0 || g.pending != 0 || len(g.tickets) != 0 || g.total != 1 || root.Err() != nil || g.seal != nil {
		t.Fatal("queued readiness did not establish a root-active physical-drain boundary")
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
			t.Fatalf("owned backend remained %s", stage)
		}
	}
	// This independent zero proof establishes the physical precondition for a
	// later STOP. The bounded fixture poll is not a production sampling claim.
	assertOwnedBackendsGone("before queued configuration")
	if g.configureActivitySelection(testSealIdentity().TaskID, "private-worker") != nil || g.selection != 2 || !g.queuedParked || g.queuedState != "configured" || g.open != 0 || g.pending != 0 || root.Err() != nil {
		t.Fatal("selection binding reopened physical work before acknowledgment")
	}
	// The controller writes its ACK before this completion; socket/failed-ACK
	// behavior is covered separately rather than being claimed by this fixture.
	if g.completeQueuedSelection() != nil || g.queuedParked || g.queuedState != "complete" || g.invalid {
		t.Fatal("acknowledged queued configuration did not open admission")
	}
	fresh, err := ConnectConfig(root, workerConfig)
	if err != nil {
		t.Fatal("new caller could not connect after queued completion")
	}
	defer fresh.Close(context.Background())
	var one int
	if fresh.QueryRow(root, "SELECT 1").Scan(&one) != nil || one != 1 {
		t.Fatal("newly admitted connection did not preserve original active context")
	}
	if fresh.Close(ctx) != nil || g.invalid || g.total != 2 || g.pending != 0 || g.open != 0 || len(g.tickets) != 0 || root.Err() != nil || g.firstConnectFailure != nil || g.firstUncleanClose != nil || g.witness.(*diagnosticWitness).stores != 0 {
		t.Fatal("queued resume changed ordinary connection cleanup or root context")
	}
	assertOwnedBackendsGone("after fresh caller cleanup")
}
