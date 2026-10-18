//go:build integration && adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The statement is byte-for-byte the production activity statement and guard
// ACTIVITY_SQL. The fixture checks both sources before replacing only the schema.
const activityAliasSQL = `WITH observed AS (SELECT clock_timestamp() AS at)
INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code,last_activity_at,last_success_at)
SELECT $1,$2,$3,$4,$5,at,CASE WHEN $4='success' THEN at ELSE NULL END FROM observed
ON CONFLICT(tenant_id,worker_id,cycle) DO UPDATE SET status=EXCLUDED.status,error_code=EXCLUDED.error_code,
 last_activity_at=EXCLUDED.last_activity_at,
 last_success_at=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.last_activity_at ELSE adtr.system_worker_activity.last_success_at END`

const activityAliasTable = `CREATE TABLE adtr.system_worker_activity (
 tenant_id text NOT NULL, worker_id text NOT NULL, cycle text NOT NULL CHECK(cycle IN ('queue','recovery')),
 status text NOT NULL CHECK(status IN ('success','failure','progress')),
 error_code text NOT NULL DEFAULT '',
 last_activity_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_success_at timestamptz,
 PRIMARY KEY(tenant_id,worker_id,cycle),
 CHECK(status<>'progress' OR cycle='queue'),
 CHECK((status='failure' AND error_code IN ('cycle_failed','database_unavailable','execution_failed','schema_incompatible'))
    OR (status IN ('success','progress') AND error_code=''))
);
CREATE INDEX system_worker_activity_recent ON adtr.system_worker_activity(tenant_id,last_activity_at DESC);`

const activityAliasMigration = `ALTER TABLE adtr.system_worker_activity DROP CONSTRAINT system_worker_activity_cycle_check;
ALTER TABLE adtr.system_worker_activity ADD CONSTRAINT system_worker_activity_cycle_check CHECK(cycle IN ('queue','recovery','scheduler'));`

type activityAliasSession struct {
	conn   *Conn
	tx     pgx.Tx
	done   chan error
	cancel context.CancelFunc
	joined bool
	err    error
	rows   int64
}

type activityAliasFixture struct {
	t           *testing.T
	ctx         context.Context
	cancel      context.CancelFunc
	gate        *lifecycleGate
	config      *pgx.ConnConfig
	probe       *pgx.Conn
	barrierConn *pgx.Conn
	barrier     pgx.Tx
	schema      string
	relation    string
	statement   string
	application string
	sessions    []*activityAliasSession
	releases    []func()
}

// These are real PostgreSQL row-lock and lifetime regressions on a synthetic,
// exclusively owned relation. They do not prove an exporter snapshot checkpoint,
// the production schema gate, or STOP eligibility. The original guard/E2E keeps
// those checks and its unchanged SQL, relation and lock allowlists.
func newActivityAliasFixture(t *testing.T, g *lifecycleGate) *activityAliasFixture {
	t.Helper()
	worker, err := os.ReadFile("../systemhealth/persistence_worker.go")
	if err != nil || !strings.Contains(string(worker), "`"+activityAliasSQL+"`") {
		t.Fatal("activity fixture statement differs from production")
	}
	guard, err := os.ReadFile("../../scripts/asset_export_v3_running_cancel_guard.py")
	if err != nil || !strings.Contains(string(guard), "ACTIVITY_SQL = \"\"\""+activityAliasSQL+"\"\"\"") {
		t.Fatal("activity fixture statement differs from guard")
	}
	source, err := os.ReadFile("../systemhealth/persistence_schema.go")
	if err != nil || !strings.Contains(string(source), activityAliasTable) || !strings.Contains(string(source), activityAliasMigration) {
		t.Fatal("activity fixture structure differs from production")
	}
	config := integrationConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	f := &activityAliasFixture{t: t, ctx: ctx, cancel: cancel, gate: g, config: config}
	t.Cleanup(f.cleanup)
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal("activity fixture identity failed")
	}
	suffix := hex.EncodeToString(nonce[:])
	f.schema = pgx.Identifier{"activity_alias_" + suffix}.Sanitize()
	f.relation = f.schema + ".system_worker_activity"
	f.statement = strings.ReplaceAll(activityAliasSQL, "adtr.", f.schema+".")
	f.application = "adtr-ci-activity-" + suffix
	f.probe, err = pgx.ConnectConfig(ctx, f.config)
	if err != nil {
		t.Fatal("activity probe connection failed")
	}
	if _, err = f.probe.Exec(ctx, "CREATE SCHEMA "+f.schema); err != nil {
		f.schema = ""
		t.Fatal("activity fixture schema failed")
	}
	if _, err = f.probe.Exec(ctx, strings.ReplaceAll(activityAliasTable+"\n"+activityAliasMigration, "adtr.", f.schema+".")); err != nil {
		t.Fatal("activity fixture table failed")
	}
	f.barrierConn, err = pgx.ConnectConfig(ctx, f.config)
	if err != nil {
		t.Fatal("activity barrier connection failed")
	}
	f.config = f.config.Copy()
	if f.config.RuntimeParams == nil {
		f.config.RuntimeParams = make(map[string]string)
	}
	f.config.RuntimeParams["application_name"] = f.application
	return f
}

func (f *activityAliasFixture) connect() *activityAliasSession {
	f.t.Helper()
	conn, err := ConnectConfig(f.ctx, f.config)
	if err != nil {
		f.t.Fatal("activity admitted connection failed")
	}
	s := &activityAliasSession{conn: conn}
	f.sessions = append(f.sessions, s)
	s.tx, err = conn.Begin(f.ctx)
	if err != nil {
		f.t.Fatal("activity transaction begin failed")
	}
	return s
}

func (f *activityAliasFixture) target(identity ActivityIdentity, workerID string) (*activityAliasSession, string, sealSnapshot) {
	f.t.Helper()
	if err := AwaitActivitySelection(f.ctx, identity); err != nil {
		f.t.Fatal("activity selection wait failed")
	}
	s := f.connect()
	release, key, err := RegisterActivityIdentity(s.conn, identity, workerID)
	if err != nil {
		f.t.Fatal("activity candidate registration failed")
	}
	f.releases = append(f.releases, release)
	candidates := f.gate.candidates()
	if len(candidates) != 1 || candidates[0].identity != identity || candidates[0].backend != s.conn.raw.PgConn().PID() {
		f.t.Fatal("activity candidate identity mismatch")
	}
	return s, key, candidates[0]
}

func (f *activityAliasFixture) seedAndLock(keys []string, lockKey string) {
	f.t.Helper()
	for _, key := range keys {
		if _, err := f.probe.Exec(f.ctx, f.statement, "fixture-tenant", key, "queue", "success", ""); err != nil {
			f.t.Fatal("activity fixture row failed")
		}
	}
	var err error
	f.barrier, err = f.barrierConn.Begin(f.ctx)
	if err != nil {
		f.t.Fatal("activity barrier begin failed")
	}
	var row string
	if err = f.barrier.QueryRow(f.ctx, "SELECT worker_id FROM "+f.relation+" WHERE tenant_id=$1 AND worker_id=$2 AND cycle='queue' FOR UPDATE", "fixture-tenant", lockKey).Scan(&row); err != nil || row != lockKey {
		f.t.Fatal("activity barrier row lock failed")
	}
}

func (s *activityAliasSession) start(ctx context.Context, statement string, args ...any) {
	queryCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan error, 1)
	go func() {
		tag, err := s.tx.Exec(queryCtx, statement, args...)
		s.rows = tag.RowsAffected()
		s.done <- err
	}()
}

func (s *activityAliasSession) join(ctx context.Context) bool {
	if s.done == nil || s.joined {
		return true
	}
	select {
	case s.err = <-s.done:
		s.joined = true
		return true
	case <-ctx.Done():
		return false
	}
}

func (f *activityAliasFixture) finishOrdinary(s *activityAliasSession) {
	f.t.Helper()
	if !s.join(f.ctx) || s.err != nil || s.rows != 1 {
		f.t.Fatal("ordinary activity statement failed to complete")
	}
	if err := s.tx.Commit(f.ctx); err != nil {
		f.t.Fatal("ordinary activity commit failed")
	}
	s.tx = nil
	if err := s.conn.Close(f.ctx); err != nil {
		f.t.Fatal("ordinary activity connection failed to close")
	}
}

func (f *activityAliasFixture) requireWait(s *activityAliasSession, blocker uint32) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	for {
		var matches bool
		err := f.probe.QueryRow(ctx, `SELECT COALESCE(state='active' AND wait_event_type='Lock'
AND xact_start IS NOT NULL AND query=$2
AND cardinality(pg_blocking_pids(pid))=1 AND $3::int=ANY(pg_blocking_pids(pid)),false)
FROM pg_stat_activity WHERE pid=$1::int`, s.conn.raw.PgConn().PID(), f.statement, blocker).Scan(&matches)
		if err != nil {
			f.t.Fatal("activity blocking observation failed")
		}
		if matches {
			return
		}
		select {
		case <-ctx.Done():
			f.t.Fatal("activity expected blocking edge not observed")
		case <-time.After(time.Millisecond):
		}
	}
}

func (f *activityAliasFixture) requireOnlyTarget(target *activityAliasSession, key string, candidate sealSnapshot) {
	f.t.Helper()
	local := f.gate.frame(1)
	if local.Status != "sealed" || local.Pending != 0 || local.Open != 1 {
		f.t.Fatal("activity local lifetime count did not seal")
	}
	frame, err := f.gate.applicationSnapshot(1, "reply")
	if err != nil || frame.Status != "sealed" || frame.Pending != 0 || frame.Open != 1 || frame.Target != candidate.generation || len(frame.Records) != 1 {
		f.t.Fatal("activity target-only lifetime did not seal")
	}
	record := frame.Records[0]
	if record.Backend != candidate.backend || record.Candidate == nil || record.Candidate.Generation != candidate.generation || record.Candidate.ActivityKey != key || record.Candidate.TaskID != candidate.identity.TaskID {
		f.t.Fatal("activity registry target or actual row key mismatch")
	}
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	for {
		var onlyTarget bool
		if err := f.probe.QueryRow(ctx, `SELECT count(*)=1 AND COALESCE(bool_and(pid=$1::int),false)
FROM pg_stat_activity WHERE application_name=$2`, target.conn.raw.PgConn().PID(), f.application).Scan(&onlyTarget); err != nil {
			f.t.Fatal("activity backend set observation failed")
		}
		if onlyTarget {
			return
		}
		select {
		case <-ctx.Done():
			f.t.Fatal("activity backend set differs from target-only registry")
		case <-time.After(time.Millisecond):
		}
	}
}

// Every failure first releases the barrier, then joins each in-flight query
// before Rollback or Close touches its connection. Ordinary sessions precede
// target in this list, so their transactions cannot retain the indirect barrier.
// All cleanup calls and cancellation joins have independent bounded contexts.
func (f *activityAliasFixture) cleanup() {
	defer f.cancel()
	f.gate.mu.Lock()
	pinned := f.gate.seal != nil
	f.gate.mu.Unlock()
	if pinned && f.gate.release(1) != nil {
		f.t.Error("activity admission cleanup failed")
	}
	rollback := func(tx pgx.Tx) {
		if tx == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			f.t.Error("activity transaction cleanup failed")
		}
	}
	rollback(f.barrier)
	for _, s := range f.sessions {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		joined := s.join(ctx)
		cancel()
		if s.cancel != nil {
			s.cancel()
		}
		if !joined {
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			joined = s.join(ctx)
			cancel()
		}
		if !joined {
			f.t.Error("activity query cleanup failed to join")
			continue
		}
		rollback(s.tx)
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		if err := s.conn.Close(ctx); err != nil {
			f.t.Error("activity connection cleanup failed")
		}
		cancel()
	}
	for _, release := range f.releases {
		release()
	}
	if f.probe != nil && f.schema != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if _, err := f.probe.Exec(ctx, "DROP SCHEMA "+f.schema+" CASCADE"); err != nil {
			f.t.Error("activity fixture schema cleanup failed")
		}
		cancel()
	}
	for _, conn := range []*pgx.Conn{f.barrierConn, f.probe} {
		if conn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := conn.Close(ctx); err != nil {
				f.t.Error("activity observer cleanup failed")
			}
			cancel()
		}
	}
}

func TestActivityAliasIntegrationSharedRowCreatesIndirectBarrier(t *testing.T) {
	g := installTestGate(t)
	g.application = true
	f := newActivityAliasFixture(t, g)
	ordinary := f.connect()
	identity := testSealIdentity()
	workerID := "private-worker"
	identity.LeaseOwner = workerID + "-0"
	target, key, candidate := f.target(identity, workerID)
	if key != workerID {
		t.Fatal("unconfigured activity changed original worker key")
	}
	f.seedAndLock([]string{key}, key)
	ordinary.start(f.ctx, f.statement, "fixture-tenant", key, "queue", "success", "")
	f.requireWait(ordinary, f.barrierConn.PgConn().PID())
	target.start(f.ctx, f.statement, "fixture-tenant", key, "queue", "progress", "")
	f.requireWait(target, ordinary.conn.raw.PgConn().PID())
	if g.pin(1, candidate.generation, nil) != nil || g.frame(1).Status != "draining" {
		t.Fatal("shared activity barrier did not retain ordinary lifetime")
	}
	// The same real statement waits on the ordinary transaction, which itself
	// waits on the barrier. Therefore a direct target barrier cannot be claimed.
	f.requireWait(ordinary, f.barrierConn.PgConn().PID())
	f.requireWait(target, ordinary.conn.raw.PgConn().PID())
	if err := f.barrier.Rollback(f.ctx); err != nil {
		t.Fatal("shared activity barrier release failed")
	}
	f.finishOrdinary(ordinary)
	if !target.join(f.ctx) || target.err != nil || target.rows != 1 {
		t.Fatal("shared activity target did not resume")
	}
}

func TestActivityAliasIntegrationSelectedRowDrainsOtherLifetimes(t *testing.T) {
	for _, kind := range []string{"user_assets.v3.export", "group_assets.v3.export", "computer_assets.v3.export"} {
		t.Run(kind, func(t *testing.T) {
			g := installTestGate(t)
			g.application = true
			g.nonce = [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
			identity := testSealIdentity()
			workerID := "private-worker"
			identity.Kind = kind
			identity.LeaseOwner = workerID + "-0"
			if g.prepareActivitySelection(kind) != nil || g.configureActivitySelection(identity.TaskID, workerID) != nil {
				t.Fatal("activity selection configuration failed")
			}
			f := newActivityAliasFixture(t, g)
			ordinaryUpdate, ordinaryUpsert := f.connect(), f.connect()
			target, key, candidate := f.target(identity, workerID)
			if key == workerID || key != activityRowKey(g.nonce, identity.TaskID) {
				t.Fatal("selected activity alias mismatch")
			}
			f.seedAndLock([]string{workerID, key}, key)
			target.start(f.ctx, f.statement, "fixture-tenant", key, "queue", "progress", "")
			f.requireWait(target, f.barrierConn.PgConn().PID())
			if g.pin(1, candidate.generation, nil) != nil || g.frame(1).Status != "draining" {
				t.Fatal("activity pin omitted other physical lifetimes")
			}
			ordinaryUpdate.start(f.ctx, "UPDATE "+f.relation+" SET last_activity_at=clock_timestamp() WHERE tenant_id=$1 AND worker_id=$2 AND cycle='queue'", "fixture-tenant", workerID)
			f.finishOrdinary(ordinaryUpdate)
			if g.frame(1).Status != "draining" {
				t.Fatal("activity pin omitted remaining physical lifetime")
			}
			ordinaryUpsert.start(f.ctx, f.statement, "fixture-tenant", workerID, "queue", "success", "")
			f.finishOrdinary(ordinaryUpsert)
			f.requireOnlyTarget(target, key, candidate)
			f.requireWait(target, f.barrierConn.PgConn().PID())
			if err := f.barrier.Rollback(f.ctx); err != nil {
				t.Fatal("selected activity barrier release failed")
			}
			if !target.join(f.ctx) || target.err != nil || target.rows != 1 {
				t.Fatal("selected activity target did not resume")
			}
		})
	}
}
