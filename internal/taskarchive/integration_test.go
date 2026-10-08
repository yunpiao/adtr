//go:build integration

package taskarchive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type archiveFixture struct {
	t         *testing.T
	ctx       context.Context
	cfg       *pgx.ConnConfig
	db        *pgx.Conn
	archive   *Engine
	queue     *tasks.Engine
	principal tasks.Principal
}

// Each test owns a fresh random database; unavailable PostgreSQL is a failure.
func newArchiveFixture(t *testing.T, legacy bool) *archiveFixture {
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	var random [12]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "adtr_archive_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		_ = admin.Close(ctx)
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
	db, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	f := &archiveFixture{t: t, ctx: ctx, cfg: cfg, db: db, principal: tasks.Principal{TenantID: "one", ActorID: 1}}
	schema := tasks.Schema
	if legacy {
		schema = strings.ReplaceAll(schema, tasks.ArchiveCoreSchema, "")
	}
	f.exec(`CREATE SCHEMA adtr;CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,8);` + schema)
	if legacy {
		f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) VALUES('legacy-no-terminal-evidence','one','platform','infrastructure.health',1,'{}','legacy',1,'1','legacy','succeeded',5)`)
		f.exec(tasks.ArchiveCoreSchema)
	}
	f.exec(Schema + `CREATE TABLE adtr.synthetic_archive_actors(tenant_id text NOT NULL,actor_id bigint NOT NULL,can_read boolean NOT NULL DEFAULT true,can_write boolean NOT NULL DEFAULT true,PRIMARY KEY(tenant_id,actor_id));INSERT INTO adtr.synthetic_archive_actors(tenant_id,actor_id) VALUES('one',1),('one',2),('two',3);`)
	authorize := func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		if scope.TaskName != HealthKind || scope.DomainID != "platform" || !scope.Platform {
			return "", tasks.ErrAuthorization
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, p.TenantID); err != nil {
			return "", err
		}
		var read, write bool
		err := tx.QueryRow(ctx, `SELECT can_read,can_write FROM adtr.synthetic_archive_actors WHERE tenant_id=$1 AND actor_id=$2 FOR UPDATE`, p.TenantID, p.ActorID).Scan(&read, &write)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (!read || action != tasks.Read && !write) {
			return "", tasks.ErrAuthorization
		}
		if err != nil {
			return "", err
		}
		return "1", nil
	}
	f.archive, err = New(authorize)
	if err != nil {
		t.Fatal(err)
	}
	f.queue, err = tasks.New(cfg, tasks.ProductionRegistry(), authorize, SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *archiveFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *archiveFixture) transact(fn func(pgx.Tx) error) error {
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
	if err = f.archive.CheckSchemaTx(f.ctx, tx); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}
func (f *archiveFixture) submit(key string, p tasks.Principal) tasks.Task {
	f.t.Helper()
	var out tasks.Submission
	err := f.transact(func(tx pgx.Tx) error {
		var err error
		out, err = f.queue.SubmitTx(f.ctx, tx, p, tasks.SubmitInput{TaskName: HealthKind, DomainID: "platform", PayloadVersion: 1, Payload: json.RawMessage(`{}`), IdempotencyKey: key})
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out.Task
}
func (f *archiveFixture) cancelled(key string) tasks.Task {
	f.t.Helper()
	task := f.submit(key, f.principal)
	err := f.transact(func(tx pgx.Tx) error {
		var err error
		task, err = f.queue.CancelTx(f.ctx, tx, f.principal, task.ID)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}
func (f *archiveFixture) cutoff() string {
	f.t.Helper()
	var now time.Time
	if err := f.db.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		f.t.Fatal(err)
	}
	return now.UTC().Format(time.RFC3339Nano)
}
func (f *archiveFixture) mutation(in ArchiveInput) (Result, error) {
	var result Result
	err := f.transact(func(tx pgx.Tx) error {
		var err error
		result, err = f.archive.ArchiveTx(f.ctx, tx, f.principal, in)
		return err
	})
	return result, err
}
func status(err error, want int) bool {
	var e *tasks.Error
	return errors.As(err, &e) && e.Status == want
}
func (f *archiveFixture) count(table string) int {
	f.t.Helper()
	var count int
	if err := f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr."+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}
func archiveInput(id, before, key string, version int64) ArchiveInput {
	return ArchiveInput{Targets: []Target{{id, version}}, Before: before, Reason: "Synthetic archive verification", IdempotencyKey: key}
}

func TestArchiveRestoreReplayPreservesExecutionAndIdempotency(t *testing.T) {
	f := newArchiveFixture(t, false)
	task := f.cancelled("preserve")
	f.exec(`INSERT INTO adtr.task_schedule_occurrences(tenant_id,schedule_id,scheduled_at,task_id) VALUES('one','historical-schedule',clock_timestamp(),$1)`, task.ID)
	var oldTask, oldEvents, oldOutbox, oldOccurrences string
	snapshot := func() (string, string, string, string) {
		var a, b, c, d string
		if err := f.db.QueryRow(f.ctx, `SELECT (SELECT row_to_json(t)::text FROM adtr.tasks t WHERE task_id=$1),(SELECT jsonb_agg(e ORDER BY id)::text FROM adtr.task_events e WHERE task_id=$1),(SELECT jsonb_agg(o ORDER BY id)::text FROM adtr.task_outbox o WHERE task_id=$1),(SELECT jsonb_agg(s ORDER BY scheduled_at)::text FROM adtr.task_schedule_occurrences s WHERE task_id=$1)`, task.ID).Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		return a, b, c, d
	}
	oldTask, oldEvents, oldOutbox, oldOccurrences = snapshot()
	in := archiveInput(task.ID, f.cutoff(), "archive-once", 0)
	first, err := f.mutation(in)
	if err != nil || first.Replayed || len(first.Receipt.Targets) != 1 || first.Receipt.Targets[0].VisibilityVersion != 1 {
		t.Fatal(first, err)
	}
	replayedTask := f.submit("preserve", f.principal)
	if replayedTask.ID != task.ID || !replayedTask.Archived {
		t.Fatal("archival lost original idempotency", replayedTask)
	}
	var restored Result
	err = f.transact(func(tx pgx.Tx) error {
		var err error
		restored, err = f.archive.RestoreTx(f.ctx, tx, f.principal, RestoreInput{Targets: []Target{{task.ID, 1}}, Reason: "Restore record", IdempotencyKey: "restore-once"})
		return err
	})
	if err != nil || restored.Receipt.Targets[0].Archived || restored.Receipt.Targets[0].VisibilityVersion != 2 {
		t.Fatal(restored, err)
	}
	replay, err := f.mutation(in)
	if err != nil || !replay.Replayed || replay.Receipt.ID != first.Receipt.ID || !replay.Receipt.Targets[0].Archived {
		t.Fatal(replay, err)
	}
	var archived bool
	var version int64
	if err = f.db.QueryRow(f.ctx, `SELECT archived,visibility_version FROM adtr.task_visibility WHERE task_id=$1`, task.ID).Scan(&archived, &version); err != nil || archived || version != 2 {
		t.Fatal("old operation rearchived restored task", archived, version, err)
	}
	a, b, c, d := snapshot()
	if a != oldTask || b != oldEvents || c != oldOutbox || d != oldOccurrences {
		t.Fatal("archive changed protected task evidence")
	}
	if f.count("task_archive_operations") != 2 || f.count("task_archive_events") != 2 {
		t.Fatal("replay appended evidence")
	}
	in.Reason = "different request"
	if _, err = f.mutation(in); !status(err, 409) {
		t.Fatal("conflicting key accepted", err)
	}
}

func TestArchiveBatchHasNoPartialEffects(t *testing.T) {
	for _, mode := range []string{"foreign", "active", "stale", "audit_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newArchiveFixture(t, false)
			a := f.cancelled("a")
			b := f.cancelled("b")
			before := f.cutoff()
			targets := []Target{{a.ID, 0}, {b.ID, 0}}
			want := 409
			switch mode {
			case "foreign":
				foreign := f.submit("foreign", tasks.Principal{TenantID: "two", ActorID: 3})
				targets[1].TaskID = foreign.ID
				want = 404
			case "active":
				targets[1].TaskID = f.submit("active", f.principal).ID
			case "stale":
				targets[1].VisibilityVersion = 1
			case "audit_failure":
				f.exec(`CREATE FUNCTION adtr.synthetic_archive_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic control audit unavailable'; END; $$;CREATE TRIGGER synthetic_archive_failure BEFORE INSERT ON adtr.task_archive_events FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_archive_failure();`)
			}
			_, err := f.mutation(ArchiveInput{Targets: targets, Before: before, Reason: "Batch verification", IdempotencyKey: "batch"})
			if err == nil || mode != "audit_failure" && !status(err, want) {
				t.Fatal(mode, err)
			}
			if f.count("task_visibility") != 0 || f.count("task_archive_operations") != 0 || f.count("task_archive_events") != 0 {
				t.Fatal("failed batch committed partial evidence")
			}
		})
	}
}

func TestArchiveConcurrentVersionWinnerAndImmutableReceipts(t *testing.T) {
	f := newArchiveFixture(t, false)
	task := f.cancelled("race")
	before := f.cutoff()
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := f.mutation(archiveInput(task.ID, before, key, 0))
			errs <- err
		}(key)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if status(err, 409) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || f.count("task_archive_operations") != 1 || f.count("task_archive_events") != 1 {
		t.Fatal(success, conflict)
	}
	for _, table := range []string{"task_archive_operations", "task_archive_events"} {
		for _, query := range []string{"UPDATE adtr." + table + " SET actor_id=actor_id", "DELETE FROM adtr." + table, "TRUNCATE adtr." + table + " CASCADE"} {
			if _, err := f.db.Exec(f.ctx, query); err == nil {
				t.Fatal("mutable archive evidence", query)
			}
		}
	}
}

func TestArchiveSuccessfulBatchReplayUsesCanonicalTargetsAndCutoff(t *testing.T) {
	f := newArchiveFixture(t, false)
	a, b := f.cancelled("batch-a"), f.cancelled("batch-b")
	in := ArchiveInput{Targets: []Target{{b.ID, 0}, {a.ID, 0}}, Before: f.cutoff(), Reason: "Review both records", IdempotencyKey: "canonical-batch"}
	first, err := f.mutation(in)
	if err != nil || first.Replayed || len(first.Receipt.Targets) != 2 {
		t.Fatal(first, err)
	}
	if first.Receipt.Targets[0].TaskID >= first.Receipt.Targets[1].TaskID || f.count("task_visibility") != 2 || f.count("task_archive_events") != 2 {
		t.Fatal("successful batch did not retain both ordered results", first)
	}
	in.Targets[0], in.Targets[1] = in.Targets[1], in.Targets[0]
	before, err := ParseBefore(in.Before)
	if err != nil {
		t.Fatal(err)
	}
	in.Before = before.Format("2006-01-02T15:04:05.000000Z")
	replay, err := f.mutation(in)
	if err != nil || !replay.Replayed || replay.Receipt.ID != first.Receipt.ID || f.count("task_archive_events") != 2 {
		t.Fatal("equivalent request was not a historical replay", replay, err)
	}
	// The operation key is scoped to the current actor, not readable across the
	// tenant just because health visibility itself permits tenant-wide control.
	f.principal.ActorID = 2
	if _, err = f.mutation(in); !status(err, 409) {
		t.Fatal("another actor received the first actor's receipt", err)
	}
}

func TestArchiveLegacyUnknownTimeAndBoundaryAreIneligible(t *testing.T) {
	f := newArchiveFixture(t, true)
	var terminal *time.Time
	if err := f.db.QueryRow(f.ctx, `SELECT terminal_at FROM adtr.tasks WHERE task_id='legacy-no-terminal-evidence'`).Scan(&terminal); err != nil || terminal != nil {
		t.Fatal("legacy timestamp guessed", terminal, err)
	}
	in := archiveInput("legacy-no-terminal-evidence", f.cutoff(), "legacy", 0)
	if _, err := f.mutation(in); !status(err, 409) {
		t.Fatal("unknown terminal time accepted", err)
	}
	task := f.cancelled("boundary")
	if err := f.db.QueryRow(f.ctx, `SELECT terminal_at FROM adtr.tasks WHERE task_id=$1`, task.ID).Scan(&terminal); err != nil || terminal == nil {
		t.Fatal(terminal, err)
	}
	in = archiveInput(task.ID, terminal.UTC().Format(time.RFC3339Nano), "boundary", 0)
	if _, err := f.mutation(in); !status(err, 409) {
		t.Fatal("equal cutoff was not strict", err)
	}
	var candidates Candidates
	err := f.transact(func(tx pgx.Tx) error {
		var err error
		candidates, err = f.archive.CandidatesTx(f.ctx, tx, f.principal, Filter{Before: f.cutoff()})
		return err
	})
	if err != nil || candidates.Page.Total != 1 || len(candidates.Tasks) != 1 || candidates.Tasks[0].TaskID != task.ID {
		t.Fatal(candidates, err)
	}
}

func TestArchivedRecoveryRequiresRestorationAndPreservesAncestry(t *testing.T) {
	f := newArchiveFixture(t, false)
	parent := f.submit("failed-parent", f.principal)
	lease, err := f.queue.Claim(f.ctx, "synthetic-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.queue.Start(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.queue.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Failed, Code: "synthetic_failure"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.mutation(archiveInput(parent.ID, f.cutoff(), "archive-parent", 0)); err != nil {
		t.Fatal(err)
	}
	err = f.transact(func(tx pgx.Tx) error {
		_, err := f.queue.RecoverTx(f.ctx, tx, f.principal, parent.ID, "child")
		return err
	})
	if !status(err, 409) {
		t.Fatal("archived recovery accepted", err)
	}
	err = f.transact(func(tx pgx.Tx) error {
		_, err := f.archive.RestoreTx(f.ctx, tx, f.principal, RestoreInput{Targets: []Target{{parent.ID, 1}}, Reason: "Restore parent", IdempotencyKey: "restore-parent"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var child tasks.Submission
	err = f.transact(func(tx pgx.Tx) error {
		var err error
		child, err = f.queue.RecoverTx(f.ctx, tx, f.principal, parent.ID, "child")
		return err
	})
	if err != nil || child.Task.ID == parent.ID || child.Task.ParentID != parent.ID {
		t.Fatal("ancestry lost", child, err)
	}
	var state string
	if err = f.db.QueryRow(f.ctx, `SELECT state FROM adtr.tasks WHERE task_id=$1`, parent.ID).Scan(&state); err != nil || state != "failed" {
		t.Fatal(state, err)
	}
}
