//go:build integration && adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Hold only the actual primary connection's last native Close. The auxiliary
// CancelRequest dial remains unwrapped. The fixture deadline always releases it.
// This blocks real pgx CleanupDone; no native state or cleanup channel is forged.
type recoveryCleanupHoldConn struct {
	net.Conn
	fixture context.Context
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (c *recoveryCleanupHoldConn) Close() error {
	c.once.Do(func() { close(c.entered) })
	select {
	case <-c.release:
	case <-c.fixture.Done():
	}
	return c.Conn.Close()
}

func publishRecoveryCaptureEnd(t *testing.T, g *lifecycleGate) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal("capture end pipe failed")
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	fd, err := prepareCaptureEnd(reader)
	if err != nil {
		t.Fatal("capture end descriptor failed")
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
	if n, err := writer.Write(frame); err != nil || n != len(frame) {
		t.Fatal("capture end authorization publication failed")
	}
}

func TestRecoveryIntegrationAuthorizedCancellationAwaitsPhysicalCleanup(t *testing.T) {
	for _, scenario := range []string{
		"complete", "acquire_during_cleanup", "sticky_during_cleanup", "late_trace_after_complete",
		"contamination_during_cleanup", "generation_mismatch_during_cleanup",
		"state_changed_during_cleanup", "active_operation_during_cleanup",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx, stopFixture := context.WithTimeout(context.Background(), 15*time.Second)
			defer stopFixture()
			root, cancelRoot := context.WithCancel(ctx)
			defer cancelRoot()
			config := integrationConfig(t)
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				t.Fatal("fixture nonce failed")
			}
			applicationName := "adtr-recovery-" + hex.EncodeToString(nonce[:])
			observerConfig := config.Copy()
			observerConfig.RuntimeParams["application_name"] = "adtr-recovery-independent-observer"
			observer, err := pgx.ConnectConfig(ctx, observerConfig)
			if err != nil {
				t.Fatal("independent observer connection failed")
			}
			defer observer.Close(context.Background())
			g := installTestGate(t)
			g.nonce, g.application, g.witness = nonce, true, &diagnosticWitness{}
			ObserveCIWorkerContext(root)
			workerConfig := config.Copy()
			workerConfig.RuntimeParams["application_name"] = applicationName
			target, err := ConnectConfig(root, workerConfig)
			if err != nil {
				t.Fatal("target connection failed")
			}
			defer target.Close(context.Background())
			revoke, _, err := RegisterActivityIdentity(target, testSealIdentity(), "candidate-worker")
			if err != nil {
				t.Fatal("target registration failed")
			}
			defer revoke()
			candidates := g.candidates()
			if len(candidates) != 1 || g.pin(1, candidates[0].generation, nil) != nil || g.frame(1).Status != "sealed" {
				t.Fatal("target-only original registry did not seal")
			}
			publishRecoveryCaptureEnd(t, g)
			if g.release(1) != nil || !g.seal.recoveryAuthorized || !g.seal.captureEnded || !g.seal.released || g.invalid {
				t.Fatal("exact capture end did not authorize release")
			}

			entered, releaseNative := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(releaseNative) }) }
			defer unblock()
			ordinaryConfig := workerConfig.Copy()
			dial := ordinaryConfig.DialFunc
			var primaryWrapped atomic.Bool
			ordinaryConfig.DialFunc = func(c context.Context, network, address string) (net.Conn, error) {
				conn, err := dial(c, network, address)
				if err != nil {
					return nil, err
				}
				if primaryWrapped.CompareAndSwap(false, true) {
					return &recoveryCleanupHoldConn{Conn: conn, fixture: ctx, entered: entered, release: releaseNative}, nil
				}
				return conn, nil
			}
			ordinary, err := ConnectConfig(root, ordinaryConfig)
			if err != nil {
				t.Fatal("post-release ordinary connection failed")
			}
			ticket, ok := ordinary.permit.(*lifecycleTicket)
			if !ok || !ticket.recoveryAuthorized || ticket.recoveryEpoch != g.seal.epoch || ticket.birthPhase != "released" || ticket.registration != nil {
				unblock()
				_ = ordinary.Close(ctx)
				t.Fatal("new ordinary connection did not retain exact birth qualification")
			}
			var tx pgx.Tx
			var queryDone chan error
			var cleanupFinished chan struct{}
			joined := true
			defer func() {
				// Always release physical Close before cancelling/joining the query.
				unblock()
				cancelRoot()
				if !joined {
					select {
					case <-queryDone:
					case <-time.After(5 * time.Second):
						t.Error("cancelled query failed to join during cleanup")
						return // Never use a connection concurrently with its query.
					}
				}
				if tx != nil {
					_ = tx.Rollback(context.Background())
				}
				_ = ordinary.Close(context.Background())
				if cleanupFinished != nil {
					select {
					case <-cleanupFinished:
					case <-time.After(3 * time.Second):
						t.Error("registry cleanup did not join during fixture cleanup")
					}
				}
			}()
			ordinaryPID := ordinary.raw.PgConn().PID()
			tx, err = ordinary.Begin(root)
			if err != nil {
				t.Fatal("ordinary transaction failed")
			}
			queryDone = make(chan error, 1)
			joined = false
			go func() {
				_, err := tx.Exec(root, "SELECT pg_sleep(60)")
				queryDone <- err
			}()
			dispatched := false
			for !dispatched && ctx.Err() == nil {
				if observer.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE pid=$1::int AND application_name=$2 AND datname=current_database() AND wait_event='PgSleep')", ordinaryPID, applicationName).Scan(&dispatched) != nil {
					t.Fatal("independent dispatch observation failed")
				}
				if !dispatched {
					time.Sleep(time.Millisecond)
				}
			}
			if !dispatched {
				t.Fatal("ordinary transaction statement never dispatched")
			}
			cancelRoot()
			select {
			case err = <-queryDone:
				joined = true
			case <-ctx.Done():
				t.Fatal("cancelled transaction did not join")
			}
			if !errors.Is(err, context.Canceled) || !ordinary.raw.IsClosed() {
				t.Fatal("dispatched root cancellation did not close native connection")
			}
			first := ticket.firstOperationError
			if first == nil || first != ticket.firstNativeClosedError || first.Operation != "statement" || first.ContextState != "canceled" || first.ErrorCategory != "canceled" || !first.ErrorMatchesContext || !first.NativeClosedAtEnd || !first.ConnectionMatched || first.RootContextState != "canceled" || first.StartPhase != "released" || first.EndPhase != "released" {
				t.Fatal("exact first dispatched cancellation observation unavailable")
			}
			if err = tx.Rollback(root); !errors.Is(err, pgconn.ErrConnClosed) {
				t.Fatal("closed-connection cleanup rollback returned unexpected result")
			}
			if ticket.firstOperationError != first || ticket.firstNativeClosedError != first || ticket.recoveryContaminated || ticket.operationInFlight != 0 {
				t.Fatal("closed cleanup rollback replaced or contaminated original cancellation")
			}
			if ordinary.Close(ctx) != nil {
				t.Fatal("managed Close changed native closed result")
			}
			if g.invalid || ticket.state != 'q' || ticket.conn != ordinary || ticket.backend != ordinaryPID || g.tickets[ticket.generation] != ticket || g.recoveryQualified != 1 || g.recoveryCompleted != 0 || g.open != 1 || ticket.cleanupDone != ordinary.raw.PgConn().CleanupDone() {
				t.Fatal("eligible native closure did not remain physically pending")
			}
			if ordinary.Close(ctx) != nil || ticket.state != 'q' || g.open != 1 || g.recoveryQualified != 1 || g.invalid {
				t.Fatal("repeated managed Close changed pending accounting")
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("native cleanup did not reach controlled physical Close")
			}
			select {
			case <-ordinary.raw.PgConn().CleanupDone():
				t.Fatal("physical cleanup completed while native Close was held")
			default:
			}
			revoke()
			if target.Close(ctx) != nil || g.invalid || g.open != 0 || g.seal.target.state != 'c' {
				t.Fatal("target was not cleanly closed before final cleanup")
			}

			finishing, finished := make(chan struct{}), make(chan struct{})
			cleanupFinished = finished
			go func() {
				close(finishing)
				g.finishRecoveryCleanup()
				close(finished)
			}()
			<-finishing
			select {
			case <-finished:
				t.Fatal("final cleanup passed before physical Close completed")
			default:
			}
			if scenario == "acquire_during_cleanup" {
				pending, err := g.acquire(context.Background())
				if err != nil || pending == nil {
					t.Fatal("concurrent acquisition fixture did not create pending work")
				}
			} else if scenario == "sticky_during_cleanup" {
				g.invalidateTransport()
			} else {
				// Each mutation starts from the same qualified q state and actual
				// closed native connection as "complete". In particular, a missing
				// native handle cannot mask these independently rejected invariants.
				g.mu.Lock()
				switch scenario {
				case "contamination_during_cleanup":
					ticket.recoveryContaminated = true
				case "generation_mismatch_during_cleanup":
					// Preserve the retained map slot while changing the ticket's
					// generation, so lookup by the claimed generation must fail.
					ticket.generation++
				case "state_changed_during_cleanup":
					ticket.state = 'c'
				case "active_operation_during_cleanup":
					ticket.operationInFlight = 1
				}
				g.mu.Unlock()
			}
			unblock()
			select {
			case <-ordinary.raw.PgConn().CleanupDone():
			case <-ctx.Done():
				t.Fatal("real native cleanup failed to complete")
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("registry cleanup failed to finish")
			}
			if scenario == "acquire_during_cleanup" {
				if !g.invalid || !g.recoveryFinalized || g.pending != 1 || g.witness.(*diagnosticWitness).stores == 0 {
					t.Fatal("final proof omitted an acquisition during native cleanup")
				}
			} else if scenario == "sticky_during_cleanup" {
				if !g.invalid || !g.recoveryFinalized || g.firstInvalidReason != invalidTransport || ticket.state != 'q' || g.recoveryCompleted != 0 || g.recoveryQualified != 1 || g.witness.(*diagnosticWitness).stores == 0 {
					t.Fatal("real native cleanup erased preexisting sticky failure")
				}
			} else if scenario == "contamination_during_cleanup" || scenario == "generation_mismatch_during_cleanup" || scenario == "state_changed_during_cleanup" || scenario == "active_operation_during_cleanup" {
				wantState := byte('q')
				if scenario == "state_changed_during_cleanup" {
					wantState = 'c'
				}
				if !g.invalid || !g.recoveryFinalized || ticket.state != wantState || ticket.conn != ordinary || !ordinary.raw.IsClosed() || g.recoveryCompleted != 0 || g.recoveryQualified != 1 || g.witness.(*diagnosticWitness).stores == 0 {
					t.Fatal("mutated retained ticket completed recovery despite real native cleanup")
				}
			} else if g.invalid || !g.recoveryFinalized || ticket.state != 'r' || g.recoveryQualified != 1 || g.recoveryCompleted != 1 || g.open != 0 || g.pending != 0 || g.witness.(*diagnosticWitness).stores != 0 {
				t.Fatal("complete authorized cancellation did not settle without invalidation")
			}

			// Independent full owned scope, including the original target. Query or
			// transport failure is fatal rather than being interpreted as zero.
			remaining := -1
			for ctx.Err() == nil {
				if observer.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND datname=current_database()", applicationName).Scan(&remaining) != nil {
					t.Fatal("independent owned backend observation failed")
				}
				if remaining == 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if remaining != 0 {
				t.Fatal("owned PostgreSQL backend remained after real native cleanup")
			}
			if scenario == "late_trace_after_complete" {
				o := &operationObserver{ticket: ticket}
				traced := o.TraceQueryStart(root, ordinary.raw, pgx.TraceQueryStartData{SQL: "rollback"})
				o.TraceQueryEnd(traced, ordinary.raw, pgx.TraceQueryEndData{Err: pgconn.ErrConnClosed})
				if !g.invalid || !ticket.recoveryContaminated || g.witness.(*diagnosticWitness).stores == 0 || ticket.state != 'r' {
					t.Fatal("late callback after successful final proof escaped sticky history")
				}
				if g.release(1) != nil || !g.invalid || g.witness.(*diagnosticWitness).stores == 0 {
					t.Fatal("repeated release erased late callback failure")
				}
			}
		})
	}
}
