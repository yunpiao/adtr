//go:build integration

package schedules_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

type fixture struct {
	t         *testing.T
	ctx       context.Context
	cfg       *pgx.ConnConfig
	db        *pgx.Conn
	queue     *tasks.Engine
	engine    *schedules.Engine
	principal tasks.Principal
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; missing PostgreSQL is a failure, not a skip")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL configuration")
	}
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	var random [12]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "adtr_schedules_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close(cleanup)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	if err = store.Migrate(ctx, cfg); err != nil {
		t.Fatal("migrate schedule fixture", err)
	}
	if err = store.Ready(ctx, cfg); err != nil {
		t.Fatal("schedule fixture not ready", err)
	}
	db, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { schedules.IntegrationCloseConnection(db) })
	f := &fixture{t: t, ctx: ctx, cfg: cfg, db: db, principal: tasks.Principal{TenantID: "synthetic", ActorID: 41}}
	f.exec(`CREATE TABLE adtr.synthetic_schedule_grants(tenant_id text NOT NULL,actor_id bigint NOT NULL,epoch text NOT NULL,allowed boolean NOT NULL DEFAULT true,PRIMARY KEY(tenant_id,actor_id)); INSERT INTO adtr.synthetic_schedule_grants VALUES('synthetic',41,'epoch-1',true);`)
	f.queue, err = tasks.New(cfg, tasks.ProductionRegistry(), syntheticAuthorizer, schedules.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	f.engine, err = schedules.New(cfg, f.queue, syntheticAuthorizer)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func syntheticAuthorizer(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
	if scope.TaskName != schedules.HealthKind || scope.DomainID != "platform" || !scope.Platform || action != tasks.Read && action != tasks.Write && action != tasks.Execute {
		return "", tasks.ErrAuthorization
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, p.TenantID); err != nil {
		return "", err
	}
	var epoch string
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT epoch,allowed FROM adtr.synthetic_schedule_grants WHERE tenant_id=$1 AND actor_id=$2 FOR UPDATE`, p.TenantID, p.ActorID).Scan(&epoch, &allowed)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !allowed {
		return "", tasks.ErrAuthorization
	}
	return epoch, err
}
func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) now() time.Time {
	f.t.Helper()
	var now time.Time
	if err := f.db.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		f.t.Fatal(err)
	}
	return now.UTC()
}
func (f *fixture) transact(fn func(pgx.Tx) error) error {
	conn, err := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if err != nil {
		return err
	}
	defer schedules.IntegrationCloseConnection(conn)
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer schedules.IntegrationRollback(tx)
	if err = f.engine.CheckSchemaTx(f.ctx, tx); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

// Past anchors are synthetic persisted histories, not an API bypass in runtime.
func (f *fixture) seed(id, epoch string, anchor time.Time, nextIndex int64, state schedules.State) schedules.Schedule {
	f.t.Helper()
	in := schedules.IntegrationValidCreate()
	in.StartAt = anchor.Format(time.RFC3339)
	in.IdempotencyKey = id
	next, err := schedules.IntegrationGridAt(anchor, 60, nextIndex)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := schedules.IntegrationScan(f.db.QueryRow(f.ctx, `INSERT INTO adtr.task_schedules(schedule_id,tenant_id,actor_id,authorization_version,label,kind,domain_id,payload_version,payload,start_at,interval_seconds,definition_hash,creation_key,state,next_index,next_at) VALUES($1,$2,$3,$4,$5,$6,'platform',1,'{}',$7,60,$8,$1,$9,$10,$11) RETURNING `+schedules.IntegrationColumns, id, f.principal.TenantID, f.principal.ActorID, epoch, in.Label, schedules.HealthKind, anchor, schedules.IntegrationHash(in), state, nextIndex, next))
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *fixture) snapshot(id string) schedules.Schedule {
	f.t.Helper()
	s, err := schedules.IntegrationScan(f.db.QueryRow(f.ctx, "SELECT "+schedules.IntegrationColumns+" FROM adtr.task_schedules WHERE schedule_id=$1", id))
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *fixture) count(table string) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr."+pgx.Identifier{table}.Sanitize()).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestSchedulerCoalescesDowntimeAndConcurrentTicks(t *testing.T) {
	f := newFixture(t)
	anchor := f.now().Truncate(time.Second).Add(-10 * time.Minute)
	f.seed("downtime", "epoch-1", anchor, 0, schedules.Enabled)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() { _, err := f.engine.Tick(f.ctx); results <- err })
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.count("tasks") != 1 || f.count("task_schedule_occurrences") != 1 || f.count("task_events") != 1 || f.count("task_outbox") != 1 {
		t.Fatal("concurrent admission duplicated task/evidence")
	}
	s := f.snapshot("downtime")
	if s.NextIndex != 11 || s.ControlVersion != 1 || s.LastTaskID == "" || s.LastScheduledAt == nil || !s.LastScheduledAt.Equal(anchor.Add(10*time.Minute)) {
		t.Fatal("immutable grid drifted", s)
	}
	var first, last int64
	if err := f.db.QueryRow(f.ctx, `SELECT first_index,last_index FROM adtr.task_schedule_events WHERE action='skipped_misfire'`).Scan(&first, &last); err != nil || first != 0 || last != 9 {
		t.Fatal("downtime was not one bounded skip range", first, last, err)
	}
	if n, err := f.engine.Tick(f.ctx); err != nil || n != 0 || f.count("tasks") != 1 {
		t.Fatal("repeat wakeup duplicated occurrence", n, err)
	}
}

func TestSchedulerNoOverlapForEveryNonterminalState(t *testing.T) {
	for _, state := range []tasks.State{tasks.Queued, tasks.Running, tasks.RetryWait, tasks.CancelRequested} {
		t.Run(string(state), func(t *testing.T) {
			f := newFixture(t)
			anchor := f.now().Truncate(time.Second).Add(-10 * time.Minute)
			s := f.seed("overlap", "epoch-1", anchor, 1, schedules.Enabled)
			var prior tasks.Submission
			if err := f.transact(func(tx pgx.Tx) error {
				var err error
				prior, err = f.queue.SubmitScheduledTx(f.ctx, tx, f.principal, tasks.ScheduledInput{SubmitInput: tasks.SubmitInput{TaskName: schedules.HealthKind, DomainID: "platform", PayloadVersion: 1, Payload: s.Payload}, ScheduleID: s.ID, ScheduledAt: anchor, ExpectedAuthorizationVersion: "epoch-1"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			f.exec(`UPDATE adtr.tasks SET state=$1 WHERE task_id=$2`, state, prior.Task.ID)
			if n, err := f.engine.Tick(f.ctx); err != nil || n != 0 {
				t.Fatal("active prior task admitted overlap", n, err)
			}
			if f.count("tasks") != 1 || f.snapshot(s.ID).NextIndex != 11 {
				t.Fatal("overlap cursor/task mismatch")
			}
			var action string
			if err := f.db.QueryRow(f.ctx, `SELECT action FROM adtr.task_schedule_events ORDER BY id DESC LIMIT 1`).Scan(&action); err != nil || action != "skipped_overlap" {
				t.Fatal(action, err)
			}
		})
	}
}

func TestSchedulerAdmissionEvidenceFailureRollsBackEveryWrite(t *testing.T) {
	f := newFixture(t)
	s := f.seed("atomic", "epoch-1", f.now().Truncate(time.Second).Add(-time.Hour), 0, schedules.Enabled)
	f.exec(`CREATE FUNCTION adtr.synthetic_schedule_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='admitted' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF;RETURN NEW;END;$$; CREATE TRIGGER synthetic_schedule_failure BEFORE INSERT ON adtr.task_schedule_events FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_schedule_failure();`)
	if _, err := f.engine.Tick(f.ctx); err == nil {
		t.Fatal("injected evidence failure succeeded")
	}
	for _, table := range []string{"tasks", "task_events", "task_outbox", "task_schedule_occurrences", "task_schedule_events"} {
		if f.count(table) != 0 {
			t.Fatal("partial transaction persisted", table)
		}
	}
	if f.snapshot(s.ID).NextIndex != 0 {
		t.Fatal("failed admission advanced cursor")
	}
	f.exec(`DROP TRIGGER synthetic_schedule_failure ON adtr.task_schedule_events`)
	if n, err := f.engine.Tick(f.ctx); err != nil || n != 1 {
		t.Fatal("safe retry failed", n, err)
	}
}

func TestSchedulerRevokedEpochCannotResumeAfterGrantRestore(t *testing.T) {
	f := newFixture(t)
	anchor := f.now().Truncate(time.Second).Add(-time.Minute)
	s := f.seed("old", "epoch-1", anchor, 0, schedules.Enabled)
	f.exec(`UPDATE adtr.synthetic_schedule_grants SET epoch='epoch-2',allowed=false`)
	if n, err := f.engine.Tick(f.ctx); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if blocked := f.snapshot(s.ID); blocked.State != schedules.AuthorizationBlocked || blocked.Error != "authorization_revoked" || blocked.NextIndex != 0 {
		t.Fatal("not blocked", blocked)
	}
	f.exec(`UPDATE adtr.synthetic_schedule_grants SET epoch='epoch-3',allowed=true`)
	if n, err := f.engine.Tick(f.ctx); err != nil || n != 0 || f.count("tasks") != 0 {
		t.Fatal("restored grants revived old schedule", n, err)
	}
	f.seed("new", "epoch-3", anchor, 0, schedules.Enabled)
	if n, err := f.engine.Tick(f.ctx); err != nil || n != 1 {
		t.Fatal("new authorized schedule not admitted", n, err)
	}
	if err := f.transact(func(tx pgx.Tx) error {
		_, err := f.engine.ControlTx(f.ctx, tx, f.principal, "enable", schedules.ControlInput{ScheduleID: s.ID, ExpectedControlVersion: 2, IdempotencyKey: "revive"})
		return err
	}); err == nil {
		t.Fatal("old schedule reauthorized")
	}
}

func TestSchedulerDatabaseErrorsDoNotBecomeRevocation(t *testing.T) {
	f := newFixture(t)
	s := f.seed("db-error", "epoch-1", f.now().Truncate(time.Second).Add(-time.Minute), 0, schedules.Enabled)
	var err error
	f.engine, err = schedules.New(f.cfg, f.queue, func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		return "", errors.New("synthetic transport failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Tick(f.ctx); err == nil {
		t.Fatal("database error hidden")
	}
	if got := f.snapshot(s.ID); got.State != schedules.Enabled || got.Error != "" || got.NextIndex != 0 || f.count("task_schedule_events") != 0 {
		t.Fatal("database outage persisted auth loss", got)
	}
}

func TestSchedulePauseRaceAndDurableOperationReplay(t *testing.T) {
	f := newFixture(t)
	s := f.seed("pause-race", "epoch-1", f.now().Truncate(time.Second).Add(-time.Minute), 0, schedules.Enabled)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Go(func() { _, err := f.engine.Tick(f.ctx); errs <- err })
	wg.Go(func() {
		errs <- f.transact(func(tx pgx.Tx) error {
			_, err := f.engine.ControlTx(f.ctx, tx, f.principal, "pause", schedules.ControlInput{ScheduleID: s.ID, ExpectedControlVersion: 1, IdempotencyKey: "pause"})
			return err
		})
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current := f.snapshot(s.ID)
	if current.State != schedules.Paused || current.ControlVersion != 2 || f.count("tasks") > 1 {
		t.Fatal("pause/dispatch race corrupted state", current)
	}
	var enabled, paused, replay schedules.Control
	apply := func(action, key string, version int64, out *schedules.Control) {
		t.Helper()
		if err := f.transact(func(tx pgx.Tx) error {
			var err error
			*out, err = f.engine.ControlTx(f.ctx, tx, f.principal, action, schedules.ControlInput{ScheduleID: s.ID, ExpectedControlVersion: version, IdempotencyKey: key})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply("enable", "enable", 2, &enabled)
	apply("pause", "pause-later", 3, &paused)
	apply("enable", "enable", 2, &replay)
	if !replay.Replayed || replay.Receipt.OperationID != enabled.Receipt.OperationID || replay.Receipt.State != schedules.Enabled || replay.Schedule.State != schedules.Paused || replay.Schedule.ControlVersion != 4 || f.snapshot(s.ID).State != schedules.Paused {
		t.Fatal("old enable replay re-enabled schedule", replay)
	}
}

func TestScheduleSchemaGateAndImmutableEvidence(t *testing.T) {
	f := newFixture(t)
	s := f.seed("immutable", "epoch-1", f.now().Truncate(time.Second).Add(-time.Minute), 0, schedules.Enabled)
	f.exec(`UPDATE adtr.schema_version SET version=$1`, store.SchemaVersion-1)
	if _, err := f.engine.Tick(f.ctx); !errors.Is(err, tasks.ErrSchemaIncompatible) {
		t.Fatal("schema mismatch admitted", err)
	}
	if f.count("tasks") != 0 || f.count("task_schedule_events") != 0 {
		t.Fatal("incompatible schema mutated scheduler")
	}
	f.exec(`UPDATE adtr.schema_version SET version=$1`, store.SchemaVersion)
	if _, err := f.engine.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE adtr.task_schedules SET authorization_version='new'`, `UPDATE adtr.task_schedules SET interval_seconds=120`, `UPDATE adtr.task_schedules SET next_index=0`, `DELETE FROM adtr.task_schedules`, `TRUNCATE adtr.task_schedules CASCADE`,
		`UPDATE adtr.task_schedule_events SET action='paused'`, `DELETE FROM adtr.task_schedule_events`, `TRUNCATE adtr.task_schedule_events`,
	} {
		if _, err := f.db.Exec(f.ctx, sql); err == nil {
			t.Fatal("mutable scheduling identity/history", sql)
		}
	}
	if got := f.snapshot(s.ID); got.AuthorizationVersion != "epoch-1" || got.IntervalSeconds != 60 {
		t.Fatal(got)
	}
	conn, err := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.IntegrationCloseConnection(conn)
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.IntegrationRollback(tx)
	if _, err = f.engine.ListTx(f.ctx, tx, f.principal, schedules.Filter{}); !errors.Is(err, tasks.ErrSchemaGateRequired) {
		t.Fatal("missing pregate accepted", err)
	}
}

func TestScheduleCreateReplayAfterAnchorAndOwnerBoundary(t *testing.T) {
	f := newFixture(t)
	anchor := f.now().Truncate(time.Second).Add(-time.Hour)
	s := f.seed("replay", "epoch-1", anchor, 0, schedules.Paused)
	in := schedules.IntegrationValidCreate()
	in.StartAt = anchor.Format(time.RFC3339)
	in.IdempotencyKey = s.ID
	var got schedules.Creation
	if err := f.transact(func(tx pgx.Tx) error {
		var err error
		got, err = f.engine.CreateTx(f.ctx, tx, f.principal, in)
		return err
	}); err != nil || !got.Replayed || got.Schedule.ID != s.ID {
		t.Fatal("late retry not replayed", got, err)
	}
	f.exec(`INSERT INTO adtr.synthetic_schedule_grants VALUES('synthetic',42,'other-epoch',true)`)
	if err := f.transact(func(tx pgx.Tx) error {
		_, err := f.engine.DetailTx(f.ctx, tx, tasks.Principal{TenantID: "synthetic", ActorID: 42}, s.ID, schedules.Filter{})
		return err
	}); err == nil {
		t.Fatal("same-tenant different owner read schedule")
	}
	for _, sql := range []string{`UPDATE adtr.task_schedule_operations SET operation_key='changed'`, `DELETE FROM adtr.task_schedule_operations`, `TRUNCATE adtr.task_schedule_operations`} {
		// Seed one receipt first so row-level mutation checks are actually tested.
		if f.count("task_schedule_operations") == 0 {
			if err := f.transact(func(tx pgx.Tx) error {
				_, err := f.engine.ControlTx(f.ctx, tx, f.principal, "pause", schedules.ControlInput{ScheduleID: s.ID, ExpectedControlVersion: 1, IdempotencyKey: "receipt"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.db.Exec(f.ctx, sql); err == nil {
			t.Fatal("mutable receipt", sql)
		}
	}
}

func TestSchedulerQueueLeaseExpiryStillPreventsOverlap(t *testing.T) {
	f := newFixture(t)
	anchor := f.now().Truncate(time.Second).Add(-10 * time.Minute)
	s := f.seed("leased", "epoch-1", anchor, 1, schedules.Enabled)
	if err := f.transact(func(tx pgx.Tx) error {
		_, err := f.queue.SubmitScheduledTx(f.ctx, tx, f.principal, tasks.ScheduledInput{SubmitInput: tasks.SubmitInput{TaskName: schedules.HealthKind, DomainID: "platform", PayloadVersion: 1, Payload: s.Payload}, ScheduleID: s.ID, ScheduledAt: anchor, ExpectedAuthorizationVersion: "epoch-1"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := f.queue.Claim(f.ctx, "synthetic-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.queue.Start(f.ctx, lease); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, lease.Task.ID)
	if n, err := f.engine.Tick(f.ctx); err != nil || n != 0 || f.count("tasks") != 1 {
		t.Fatal("expired running lease created overlap", n, err)
	}
	if _, err = f.queue.Heartbeat(f.ctx, lease); !errors.Is(err, tasks.ErrLeaseLost) {
		t.Fatal(fmt.Sprintf("stale lease not fenced: %v", err))
	}
}

func TestSchedulerNeverHoldsScheduleWhileWaitingForTenant(t *testing.T) {
	f := newFixture(t)
	s := f.seed("lock-order", "epoch-1", f.now().Truncate(time.Second).Add(-time.Minute), 0, schedules.Enabled)
	hold, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.IntegrationRollback(hold)
	if _, err = hold.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, f.principal.TenantID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := f.engine.Tick(f.ctx); result <- err }()
	probe, err := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.IntegrationCloseConnection(probe)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err = probe.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND wait_event='advisory' AND query LIKE '%adtr-access:%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("scheduler completed before held tenant lock was released: %v", err)
		case <-deadline.C:
			t.Fatal("scheduler did not reach its tenant authorization lock")
		case <-ticker.C:
		}
	}
	rowProbe, err := probe.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.IntegrationRollback(rowProbe)
	var id string
	if err = rowProbe.QueryRow(f.ctx, `SELECT schedule_id FROM adtr.task_schedules WHERE schedule_id=$1 FOR UPDATE NOWAIT`, s.ID).Scan(&id); err != nil {
		t.Fatal("scheduler locked schedule before obtaining tenant/actor authorization", err)
	}
	if err = rowProbe.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = hold.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler failed to resume after tenant unlock")
	}
}
