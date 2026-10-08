//go:build integration

package systemhealth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type healthFixture struct {
	t     *testing.T
	ctx   context.Context
	cfg   *pgx.ConnConfig
	db    *pgx.Conn
	store *Store
}

func newHealthFixture(t *testing.T) *healthFixture {
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
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	name := "adtr_health_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error("could not remove test-owned database:", err)
		}
		_ = admin.Close(cleanup)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	db, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("could not connect to owned health-test database")
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	if _, err = db.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL); INSERT INTO adtr.schema_version VALUES(true,7);`+Schema); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(cfg, 7)
	if err != nil {
		t.Fatal(err)
	}
	return &healthFixture{t, ctx, cfg, db, s}
}
func (f *healthFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *healthFixture) transact(fn func(pgx.Tx) error) error {
	conn, err := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(f.ctx)
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(f.ctx)
	if err = tasks.CheckSchemaTx(f.ctx, tx, 7); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

func TestHealthPersistenceRealObservationsHistoryAndRecovery(t *testing.T) {
	f := newHealthFixture(t)
	if err := f.transact(func(tx pgx.Tx) error {
		current, err := f.store.CurrentTx(f.ctx, tx, OwnerTenantID, LocalInstanceID)
		if err == nil && (current.Snapshot != nil || current.Reason != "no_samples" || current.AvailableSince != nil) {
			t.Fatal("empty storage fabricated an observation")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := f.db.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	source, sampler := newFixture(t)
	source.now = now.Add(-2 * time.Minute)
	first := sampler.Sample()
	if err := f.store.Persist(f.ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := f.transact(func(tx pgx.Tx) error {
		current, err := f.store.CurrentTx(f.ctx, tx, OwnerTenantID, LocalInstanceID)
		if err == nil && (!current.Stale || current.Availability != Unavailable || current.Snapshot.CPU.Percent != nil) {
			t.Fatal("stale or warming CPU misrepresented")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A failed read persists an actual unavailable attempt, never the previous
	// successful value. Later history omits its null value and exposes the gap.
	source.now = now.Add(-105 * time.Second)
	source.errors[ProcMeminfo] = errors.New("synthetic unavailable kernel read")
	source.statErr = errors.New("synthetic unavailable filesystem read")
	failed := sampler.Sample()
	if err := f.store.Persist(f.ctx, failed); err != nil {
		t.Fatal(err)
	}
	var failedRAM, failedDisk *float64
	if err := f.db.QueryRow(f.ctx, `SELECT r.ram_percent,s.use_percent FROM adtr.system_resource_samples r JOIN adtr.system_storage_samples s USING(tenant_id,instance_id,observed_at) WHERE r.observed_at=$1`, failed.ObservedAt).Scan(&failedRAM, &failedDisk); err != nil || failedRAM != nil || failedDisk != nil {
		t.Fatalf("failed read retained an old value: %v %v %v", failedRAM, failedDisk, err)
	}
	delete(source.errors, ProcMeminfo)
	source.statErr = nil
	source.now = now.Add(-15 * time.Second)
	source.data[ProcStat] = "cpu  120 10 20 220 30 4 6 0 5 2\ncpu0 0 0 0 0\n"
	second := sampler.Sample()
	if err := f.store.Persist(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	requireCode(t, f.store.Persist(f.ctx, second), 409, "observation_conflict")
	if err := f.transact(func(tx pgx.Tx) error {
		current, err := f.store.CurrentTx(f.ctx, tx, OwnerTenantID, LocalInstanceID)
		if err != nil {
			return err
		}
		if current.Stale || current.Availability != Available {
			t.Fatal("fresh observation did not recover")
		}
		requirePercent(t, current.Snapshot.CPU.Percent, 50)
		in := HistoryInput{Instance: LocalInstanceID, GraphType: "cpu_basic", StartTime: now.Add(-3 * time.Minute).Unix(), EndTime: now.Unix()}
		history, err := f.store.HistoryTx(f.ctx, tx, OwnerTenantID, in)
		if err != nil {
			return err
		}
		if len(history.Info[0].Data.Value) != 1 || history.Info[0].Data.Value[0] != "50" || history.Info[0].Data.Timestamp[0] != second.ObservedAt.Unix() {
			t.Fatalf("CPU history contains fabricated samples: %+v", history)
		}
		in.GraphType = "ram_basic"
		history, err = f.store.HistoryTx(f.ctx, tx, OwnerTenantID, in)
		if err != nil {
			return err
		}
		if len(history.Info[0].Data.Value) != 2 || len(history.Gaps) < 1 {
			t.Fatal("RAM observations/gaps missing")
		}
		in.GraphType = "disk_usage"
		in.StorageID = RuntimeRootID
		history, err = f.store.HistoryTx(f.ctx, tx, OwnerTenantID, in)
		if err != nil {
			return err
		}
		if len(history.Info[0].Data.Value) != 2 {
			t.Fatal("storage observations not persisted")
		}
		storage, err := f.store.StorageTx(f.ctx, tx, OwnerTenantID, LocalInstanceID, 1, 10)
		if err != nil {
			return err
		}
		if storage.Total != 1 || storage.Storage[0].AlarmPercent != 85 || storage.Storage[0].Revision != 0 {
			t.Fatal("default alarm setting incorrect")
		}
		requireDecimal(t, storage.Storage[0].TotalBytes, "409600")
		_, err = f.store.HistoryTx(f.ctx, tx, "other-tenant", in)
		requireCode(t, err, 403, "forbidden")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A new store has no process cache and reads the same persisted history.
	restarted, err := NewStore(f.cfg, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.transact(func(tx pgx.Tx) error {
		current, err := restarted.CurrentTx(f.ctx, tx, OwnerTenantID, LocalInstanceID)
		if err == nil && current.Snapshot == nil {
			t.Fatal("restart lost persisted samples")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHealthAlarmCASAuditIsolationAndRollback(t *testing.T) {
	f := newHealthFixture(t)
	in := AlarmInput{Instance: LocalInstanceID, MountID: RuntimeRootID, Percent: 88, ExpectedRevision: 0}
	var winners, conflicts atomic.Int32
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			err := f.transact(func(tx pgx.Tx) error { _, err := f.store.SetAlarmTx(f.ctx, tx, OwnerTenantID, 42, in); return err })
			if err == nil {
				winners.Add(1)
				return
			}
			var public *Error
			if errors.As(err, &public) && public.Code == "revision_conflict" {
				conflicts.Add(1)
			} else {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if winners.Load() != 1 || conflicts.Load() != 7 {
		t.Fatalf("CAS winners=%d conflicts=%d", winners.Load(), conflicts.Load())
	}
	var count, actor int64
	if err := f.db.QueryRow(f.ctx, "SELECT count(*),min(actor_id) FROM adtr.system_alarm_audit").Scan(&count, &actor); err != nil || count != 1 || actor != 42 {
		t.Fatalf("audit count/actor wrong: %d %d %v", count, actor, err)
	}
	if err := f.transact(func(tx pgx.Tx) error { _, err := f.store.SetAlarmTx(f.ctx, tx, "other-tenant", 42, in); return err }); err == nil {
		t.Fatal("cross-tenant setting changed")
	}
	for _, statement := range []string{"UPDATE adtr.system_alarm_audit SET actor_id=99", "DELETE FROM adtr.system_alarm_audit", "TRUNCATE adtr.system_alarm_audit"} {
		if _, err := f.db.Exec(f.ctx, statement); err == nil {
			t.Fatal("immutable audit mutation succeeded")
		}
	}
	f.exec(`CREATE FUNCTION adtr.synthetic_reject_alarm_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END; $$; CREATE TRIGGER synthetic_reject_audit BEFORE INSERT ON adtr.system_alarm_audit FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_reject_alarm_audit();`)
	in.Percent = 90
	in.ExpectedRevision = 1
	err := f.transact(func(tx pgx.Tx) error { _, err := f.store.SetAlarmTx(f.ctx, tx, OwnerTenantID, 42, in); return err })
	requireCode(t, err, 503, "health_storage_unavailable")
	var percent int
	var revision int64
	if err = f.db.QueryRow(f.ctx, "SELECT alarm_percent,revision FROM adtr.system_alarm_settings").Scan(&percent, &revision); err != nil || percent != 88 || revision != 1 {
		t.Fatalf("audit failure did not roll back setting: %d %d %v", percent, revision, err)
	}
}

func TestHealthSchemaGatesAndBoundedHistory(t *testing.T) {
	f := newHealthFixture(t)
	tx, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.CurrentTx(f.ctx, tx, OwnerTenantID, LocalInstanceID)
	requireCode(t, err, 503, "schema_gate_required")
	_ = tx.Rollback(f.ctx)
	var now time.Time
	if err = f.db.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	start := now.Add(-2 * time.Hour).Truncate(time.Second)
	f.exec(`INSERT INTO adtr.system_resource_samples(tenant_id,instance_id,observed_at,cpu_percent,ram_percent,snapshot) SELECT 'default','local-api',$1::timestamptz + n*interval '1 second',25,50,'{}'::jsonb FROM generate_series(0,5760) n`, start)
	err = f.transact(func(tx pgx.Tx) error {
		_, err := f.store.HistoryTx(f.ctx, tx, OwnerTenantID, HistoryInput{Instance: LocalInstanceID, GraphType: "cpu_basic", StartTime: start.Unix(), EndTime: now.Unix()})
		return err
	})
	requireCode(t, err, 422, "history_point_limit")
	f.exec("UPDATE adtr.schema_version SET version=8")
	tx, err = f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, f.store.CheckSchemaTx(f.ctx, tx), 503, "schema_incompatible")
	_ = tx.Rollback(f.ctx)
	_, sampler := newFixture(t)
	requireCode(t, f.store.Persist(f.ctx, sampler.Sample()), 503, "schema_incompatible")
}

func TestHealthWorkerActivityUsesDatabaseClockAndFailedCycles(t *testing.T) {
	f := newHealthFixture(t)
	if err := f.store.RecordWorkerCycle(f.ctx, "worker-one", WorkerQueue, WorkerCycleSuccess, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordWorkerCycle(f.ctx, "worker-one", WorkerRecovery, WorkerCycleSuccess, ""); err != nil {
		t.Fatal(err)
	}
	read := func() WorkerActivity {
		t.Helper()
		var out WorkerActivity
		if err := f.transact(func(tx pgx.Tx) error {
			var err error
			out, err = f.store.WorkerActivityTx(f.ctx, tx, OwnerTenantID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	activity := read()
	if activity.Availability != Available || len(activity.Cycles) != 2 {
		t.Fatalf("actual cycles not healthy: %+v", activity)
	}
	for _, cycle := range activity.Cycles {
		if cycle.LastActivityAt == nil || cycle.LastSuccessAt == nil || activity.CheckedAt.Sub(*cycle.LastActivityAt) > 5*time.Second {
			t.Fatal("worker clock is not current database clock")
		}
	}
	if err := f.store.RecordWorkerCycle(f.ctx, "worker-one", WorkerQueue, WorkerCycleFailure, "execution_failed"); err != nil {
		t.Fatal(err)
	}
	activity = read()
	if activity.Availability != Unavailable || activity.Reason != "worker_cycle_failed" {
		t.Fatal("failed worker retained fresh healthy state")
	}
	if err := f.store.RecordWorkerCycle(f.ctx, "worker-one", WorkerQueue, WorkerCycleProgress, ""); err != nil {
		t.Fatal(err)
	}
	if activity = read(); activity.Availability != Available {
		t.Fatal("real renewed execution activity failed to recover health")
	}
	f.exec("UPDATE adtr.system_worker_activity SET last_activity_at=clock_timestamp()-interval '16 seconds'")
	if activity = read(); activity.Availability != Unavailable {
		t.Fatal("expired worker activity remained healthy")
	}
}
