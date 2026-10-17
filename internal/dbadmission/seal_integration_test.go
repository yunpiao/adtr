//go:build integration && adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This helper proves actual backend/lifetime and row-barrier correspondence.
// It does not claim a synthetic identity proves an exporter snapshot checkpoint.
func TestSealIntegrationExistingTargetAndSecondBegin(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		name := "without_capture_end"
		if authorized {
			name = "with_capture_end"
		}
		t.Run(name, func(t *testing.T) { testSealIntegrationTargetLifecycle(t, authorized) })
	}
}

func testSealIntegrationTargetLifecycle(t *testing.T, authorized bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := integrationConfig(t)
	observer, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("observer failed")
	}
	defer observer.Close(context.Background())

	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal("fixture identity failed")
	}
	schema := pgx.Identifier{"seal_" + hex.EncodeToString(nonce[:])}.Sanitize()
	relation := schema + ".barrier"
	if _, err = observer.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("fixture schema failed")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := observer.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("fixture schema cleanup failed")
		}
	}()
	if _, err = observer.Exec(ctx, "CREATE TABLE "+relation+"(id integer PRIMARY KEY, value integer NOT NULL)"); err != nil {
		t.Fatal("fixture table failed")
	}
	if _, err = observer.Exec(ctx, "INSERT INTO "+relation+" VALUES(1,0)"); err != nil {
		t.Fatal("fixture row failed")
	}

	barrier, err := observer.Begin(ctx)
	if err != nil {
		t.Fatal("barrier begin failed")
	}
	defer barrier.Rollback(context.Background())
	if _, err = barrier.Exec(ctx, "SELECT id FROM "+relation+" WHERE id=1 FOR UPDATE"); err != nil {
		t.Fatal("barrier lock failed")
	}
	g := installTestGate(t)
	workerConfig := config.Copy()
	workerConfig.RuntimeParams["application_name"] = "adtr-ci-seal-helper"
	target, err := ConnectConfig(ctx, workerConfig)
	if err != nil {
		t.Fatal("target connect failed")
	}
	defer target.Close(context.Background())
	release, _, err := RegisterActivityIdentity(target, testSealIdentity(), "candidate-worker")
	if err != nil {
		t.Fatal("candidate failed")
	}
	defer release()
	other, err := ConnectConfig(ctx, workerConfig)
	if err != nil {
		t.Fatal("other connect failed")
	}
	defer other.Close(context.Background())
	first, err := other.Begin(ctx)
	if err != nil {
		t.Fatal("first begin failed")
	}
	if first.Commit(ctx) != nil {
		t.Fatal("first commit failed")
	}
	second, err := other.Begin(ctx)
	if err != nil {
		t.Fatal("second begin failed")
	}
	defer second.Rollback(context.Background())
	targetTx, err := target.Begin(ctx)
	if err != nil {
		t.Fatal("target begin failed")
	}
	defer targetTx.Rollback(context.Background())
	updateDone := make(chan error, 1)
	go func() {
		_, err := targetTx.Exec(ctx, "UPDATE "+relation+" SET value=value+1 WHERE id=1")
		updateDone <- err
	}()
	// On every failure unblock and join the target query before touching its Conn.
	joined := false
	defer func() {
		_ = barrier.Rollback(context.Background())
		if !joined {
			<-updateDone
		}
	}()
	snapshots := g.candidates()
	if len(snapshots) != 1 || snapshots[0].identity != testSealIdentity() {
		t.Fatal("candidate snapshot mismatch")
	}
	snapshot := snapshots[0]
	// A separate observer is needed while the barrier transaction stays open.
	probe, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("probe failed")
	}
	defer probe.Close(context.Background())
	deadline := time.Now().Add(1500 * time.Millisecond)
	blocked := false
	for time.Now().Before(deadline) {
		if probe.QueryRow(ctx, `SELECT $2::int=ANY(pg_blocking_pids($1::int))`, snapshot.backend, observer.PgConn().PID()).Scan(&blocked) != nil {
			t.Fatal("blocking observation failed")
		}
		if blocked {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !blocked {
		t.Fatal("target barrier not observed")
	}
	started, proceed := make(chan struct{}), make(chan struct{})
	var proceedOnce sync.Once
	unblock := func() { proceedOnce.Do(func() { close(proceed) }) }
	pendingResult := make(chan *Conn, 1)
	go func() {
		conn, _ := connectWith(ctx, workerConfig, func(c context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
			close(started)
			<-proceed
			return pgx.ConnectConfig(c, cfg)
		})
		pendingResult <- conn
	}()
	<-started
	pendingJoined := false
	defer func() {
		unblock()
		if !pendingJoined {
			if c := <-pendingResult; c != nil {
				_ = c.Close(context.Background())
			}
		}
	}()
	if g.pin(1, snapshot.generation, nil) != nil || g.frame(1).Status != "draining" {
		t.Fatal("second transaction omitted")
	}
	if second.Commit(ctx) != nil {
		t.Fatal("second commit failed")
	}
	if g.frame(1).Status != "draining" {
		t.Fatal("commit incorrectly drained physical connection")
	}
	if other.Close(ctx) != nil {
		t.Fatal("other close failed")
	}
	if g.frame(1).Status != "draining" {
		t.Fatal("pending startup omitted from seal")
	}
	unblock()
	pendingConn := <-pendingResult
	pendingJoined = true
	if pendingConn == nil {
		t.Fatal("pending connection failed")
	}
	defer pendingConn.Close(context.Background())
	if g.frame(1).Status != "draining" {
		t.Fatal("newly established lifetime omitted")
	}
	if pendingConn.Close(ctx) != nil {
		t.Fatal("pending connection cleanup failed")
	}
	application, appErr := g.applicationSnapshot(1, "reply")
	if appErr != nil || application.Status != "sealed" || len(application.Records) != 1 || application.Records[0].Backend != target.raw.PgConn().PID() || application.Records[0].Candidate == nil {
		t.Fatal("application registry PG target mismatch")
	}
	if g.frame(1).Status != "sealed" {
		t.Fatal("clean target-only lifetime did not seal")
	}
	var total, matching int
	for time.Now().Before(deadline) {
		if probe.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE pid=$1::int) FROM pg_stat_activity WHERE application_name='adtr-ci-seal-helper'`, snapshot.backend).Scan(&total, &matching) != nil {
			t.Fatal("backend observation failed")
		}
		if total == 1 && matching == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if total != 1 || matching != 1 || time.Now().After(deadline) {
		t.Fatal("independent backend set did not match target")
	}
	if authorized {
		endReader, endWriter, pipeErr := os.Pipe()
		if pipeErr != nil {
			t.Fatal("capture end pipe")
		}
		defer endReader.Close()
		defer endWriter.Close()
		fd, prepareErr := prepareCaptureEnd(endReader)
		if prepareErr != nil {
			t.Fatal("capture end descriptor")
		}
		g.mu.Lock()
		g.captureEndFD = fd
		frame := make([]byte, 48)
		copy(frame[:8], "ADTREND1")
		copy(frame[8:24], g.nonce[:])
		binary.BigEndian.PutUint64(frame[24:32], g.seal.epoch)
		binary.BigEndian.PutUint64(frame[32:40], g.seal.target.generation)
		binary.BigEndian.PutUint64(frame[40:48], g.seal.registration.generation)
		g.mu.Unlock()
		if n, writeErr := endWriter.Write(frame); writeErr != nil || n != 48 {
			t.Fatal("capture end publication")
		}
	}
	if barrier.Rollback(ctx) != nil {
		t.Fatal("barrier release failed")
	}
	updateErr := <-updateDone
	joined = true
	if updateErr != nil {
		t.Fatal("target update failed")
	}
	if targetTx.Rollback(ctx) != nil {
		t.Fatal("target rollback failed")
	}
	release()
	if authorized {
		if target.Close(ctx) != nil || g.invalid || !g.seal.captureEnded || !g.closing || g.seal.released {
			t.Fatal("clean target completion invalidated committed capture")
		}
	} else if g.frame(1).Status != "invalid" {
		t.Fatal("target revoke did not invalidate seal")
	}
	if g.release(1) != nil {
		t.Fatal("admission release failed")
	}
}

// pgx may create a CancelRequest auxiliary socket during asynchronous close.
// It remains part of the original lifetime and can never establish a new seal.
func TestSealIntegrationCanceledQueryCleanupPoisonsAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := integrationConfig(t)
	observer, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("observer failed")
	}
	defer observer.Close(context.Background())
	g := installTestGate(t)
	target, err := ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("target failed")
	}
	defer target.Close(context.Background())
	revoke, _, err := RegisterActivityIdentity(target, testSealIdentity(), "candidate-worker")
	if err != nil {
		t.Fatal("registration failed")
	}
	defer revoke()
	other, err := ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("other failed")
	}
	defer other.Close(context.Background())
	otherPID := other.raw.PgConn().PID()
	if g.pin(1, g.candidates()[0].generation, nil) != nil {
		t.Fatal("pin failed")
	}
	queryCtx, stopQuery := context.WithCancel(ctx)
	queryDone := make(chan error, 1)
	go func() {
		_, e := other.Exec(queryCtx, "SELECT pg_sleep(60)")
		queryDone <- e
	}()
	joined := false
	defer func() {
		stopQuery()
		if !joined {
			select {
			case <-queryDone:
			case <-ctx.Done():
				t.Error("cancelled query cleanup deadline")
			}
		}
	}()
	// Observe a genuinely dispatched server query before cancelling. An already
	// expired context can be rejected without native close and is a different case.
	dispatched := false
	for !dispatched && ctx.Err() == nil {
		if observer.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE pid=$1::int AND wait_event='PgSleep')", otherPID).Scan(&dispatched) != nil {
			t.Fatal("query dispatch observation failed")
		}
		if !dispatched {
			time.Sleep(time.Millisecond)
		}
	}
	if !dispatched {
		t.Fatal("query did not dispatch")
	}
	stopQuery()
	var queryErr error
	select {
	case queryErr = <-queryDone:
		joined = true
	case <-ctx.Done():
		t.Fatal("cancelled query join deadline")
	}
	if queryErr == nil {
		t.Fatal("canceled query unexpectedly completed")
	}
	if g.frame(1).Status == "sealed" {
		t.Fatal("asynchronous cleanup counted as drained")
	}
	// Regardless of whether pgx has finished native close, wrapper Close must
	// account for its exact CleanupDone result and retain failure information.
	_ = other.Close(ctx)
	dirty := g.uncleanCloseDiagnostic()
	if dirty == nil || !dirty.AlreadyClosed || dirty.CloseFailed || dirty.Role != "other" {
		t.Fatal("native cancellation close facts unavailable")
	}
	select {
	case <-other.raw.PgConn().CleanupDone():
	case <-ctx.Done():
		t.Fatal("native cleanup did not finish")
	}
	if g.frame(1).Status != "invalid" {
		t.Fatal("canceled native lifetime yielded seal")
	}
	deadline := time.Now().Add(time.Second)
	exists := true
	for time.Now().Before(deadline) {
		if observer.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE pid=$1::int)", otherPID).Scan(&exists) != nil {
			t.Fatal("cleanup observation failed")
		}
		if !exists {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if exists {
		t.Fatal("canceled backend remained")
	}
	if g.release(1) != nil {
		t.Fatal("failed attempt release failed")
	}
}
