//go:build integration

package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
	"github.com/yunpiao/adtr/internal/tasks"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	t      *testing.T
	ctx    context.Context
	cfg    *pgx.ConnConfig
	db     *pgx.Conn
	store  *Store
	engine *tasks.Engine
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	random, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	name := "adtr_messages_" + random
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
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	if _, err = db.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,17);`+tasks.Schema+Schema); err != nil {
		t.Fatal(err)
	}
	store, err := New(cfg, 17)
	if err != nil {
		t.Fatal(err)
	}
	// Production health execution with explicit synthetic task authorization;
	// full persisted session/grant boundaries are covered by the auth HTTP suite.
	authorize := func(_ context.Context, _ pgx.Tx, p tasks.Principal, scope tasks.Scope, _ tasks.Action) (string, error) {
		if p.ActorID <= 0 || p.TenantID == "" || !scope.Platform || scope.TaskName != "infrastructure.health" {
			return "", tasks.ErrAuthorization
		}
		return "1", nil
	}
	engine, err := tasks.New(cfg, tasks.ProductionRegistry(), authorize, 17)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, ctx: ctx, cfg: cfg, db: db, store: store, engine: engine}
}
func testIdentity() Identity {
	return Identity{TenantID: "synthetic-tenant", UserID: 41, SessionHash: strings.Repeat("a", 64), AuthorizationVersion: 1}
}
func (f *fixture) tx(fn func(pgx.Tx) error) error {
	conn, err := pgx.ConnectConfig(f.ctx, f.cfg)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = f.store.CheckSchemaTx(f.ctx, tx); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}
func (f *fixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) submit(id Identity, key string) tasks.Task {
	f.t.Helper()
	var out tasks.Submission
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		out, err = f.engine.SubmitTx(f.ctx, tx, tasks.Principal{TenantID: id.TenantID, ActorID: id.UserID}, tasks.SubmitInput{TaskName: "infrastructure.health", DomainID: "platform", PayloadVersion: 1, Payload: json.RawMessage(`{}`), IdempotencyKey: key})
		return err
	}))
	return out.Task
}
func (f *fixture) runHealth(id Identity, key string) tasks.Task {
	task := f.submit(id, key)
	worked, err := f.engine.RunOne(f.ctx, "synthetic-worker")
	f.must(err)
	if !worked {
		f.t.Fatal("health worker did not execute")
	}
	var state string
	f.must(f.db.QueryRow(f.ctx, `SELECT state FROM adtr.tasks WHERE task_id=$1`, task.ID).Scan(&state))
	if state != "succeeded" {
		f.t.Fatalf("real health outcome=%s", state)
	}
	return task
}
func (f *fixture) project() {
	f.t.Helper()
	for range 3 {
		f.must(f.store.ProjectOnce(f.ctx))
	}
}
func (f *fixture) query(id Identity, now time.Time, read messagecore.ReadFilter) Snapshot {
	f.t.Helper()
	var out Snapshot
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		out, err = f.store.QueryTx(f.ctx, tx, id, messagecore.Query{Sort: messagecore.SortDescending, ReadFilter: read, Limit: 20}, now)
		return err
	}))
	return out
}
func requireError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var p *Error
	if !errors.As(err, &p) || p.Status != status || p.Code != code {
		t.Fatalf("error=%v want %d %s", err, status, code)
	}
}

func TestRealHealthSourceFixedResultAndDurableReceipt(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	task := f.runHealth(id, "health-first")
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadUnread)
	if snap.Total != 1 || snap.Counts != (Counts{All: 1, Unread: 1}) || snap.Items[0].TaskID != task.ID || snap.Items[0].SubType != "succeeded" {
		t.Fatalf("unexpected real source: %+v", snap)
	}
	var receipt ReadReceipt
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		receipt, err = f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "operation-one", now)
		return err
	}))
	if receipt.Progress != (Progress{Target: 1, Processed: 1, NewlyRead: 1}) {
		t.Fatalf("receipt %+v", receipt)
	}
	var replay ReadReceipt
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		replay, err = f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "operation-one", now.Add(time.Minute))
		return err
	}))
	if replay != receipt {
		t.Fatalf("receipt changed on replay: %+v %+v", receipt, replay)
	}
	var original Snapshot
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		original, err = f.store.PageTx(f.ctx, tx, id, snap.ResultID, 0, 20, now)
		return err
	}))
	if original.Items[0].Read || original.Counts.Unread != 1 {
		t.Fatal("fixed read snapshot mutated")
	}
	fresh := f.query(id, now.Add(time.Second), messagecore.ReadUnread)
	if fresh.Total != 0 || fresh.Counts != (Counts{All: 1, Read: 1}) {
		t.Fatalf("refresh=%+v", fresh)
	}
	// Cleanup cannot destroy proof of an unknown committed operation.
	_, err := f.db.Exec(f.ctx, `DELETE FROM adtr.message_results WHERE result_id=$1`, snap.ResultID)
	f.must(err)
	newSession := id
	newSession.SessionHash = strings.Repeat("b", 64)
	newSession.AuthorizationVersion = 2
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		replay, err = f.store.ReadOperationTx(f.ctx, tx, newSession, "operation-one")
		return err
	}))
	if replay != receipt {
		t.Fatal("receipt unavailable to renewed authorized identity")
	}
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		replay, err = f.store.MarkReadTx(f.ctx, tx, newSession, snap.ResultID, snap.Items[0].ID, "operation-one", now.Add(time.Hour))
		return err
	}))
	if replay != receipt {
		t.Fatal("replay depends on deleted old snapshot")
	}
	err = f.tx(func(tx pgx.Tx) error {
		_, err := f.store.MarkReadTx(f.ctx, tx, id, "another-result", snap.Items[0].ID, "operation-one", now)
		return err
	})
	requireError(t, err, 409, "operation_conflict")
	for _, other := range []Identity{{TenantID: id.TenantID, UserID: 42, SessionHash: id.SessionHash, AuthorizationVersion: 1}, {TenantID: "other-tenant", UserID: id.UserID, SessionHash: id.SessionHash, AuthorizationVersion: 1}} {
		err = f.tx(func(tx pgx.Tx) error {
			_, err := f.store.ReadOperationTx(f.ctx, tx, other, "operation-one")
			return err
		})
		requireError(t, err, 404, "not_found")
		otherSnap := f.query(other, now, messagecore.ReadAll)
		if otherSnap.Total != 0 {
			t.Fatal("cross identity message leak")
		}
	}
}

func TestConcurrentReadFirstTimestampAndReceiptIsolation(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	f.runHealth(id, "health-concurrent")
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	receipts := make(chan ReadReceipt, 16)
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var r ReadReceipt
			err := f.tx(func(tx pgx.Tx) (err error) {
				r, err = f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, fmt.Sprintf("operation-%d", i), now.Add(time.Duration(i)*time.Millisecond))
				return err
			})
			if err != nil {
				errs <- err
			} else {
				receipts <- r
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(receipts)
	for err := range errs {
		f.must(err)
	}
	newly := 0
	var first time.Time
	for r := range receipts {
		newly += r.Progress.NewlyRead
		if first.IsZero() {
			first = r.FirstReadAt
		} else if !first.Equal(r.FirstReadAt) {
			t.Fatal("first timestamp overwritten")
		}
	}
	if newly != 1 {
		t.Fatalf("new reads=%d", newly)
	}
}

func TestExpiryEpochSessionAndQuotaReject(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	now := time.Now()
	snap := f.query(id, now, messagecore.ReadAll)
	for _, change := range []func(*Identity){func(i *Identity) { i.SessionHash = strings.Repeat("b", 64) }, func(i *Identity) { i.AuthorizationVersion++ }} {
		changed := id
		change(&changed)
		err := f.tx(func(tx pgx.Tx) error {
			_, err := f.store.PageTx(f.ctx, tx, changed, snap.ResultID, 0, 20, now)
			return err
		})
		requireError(t, err, 410, "result_invalidated")
	}
	err := f.tx(func(tx pgx.Tx) error {
		_, err := f.store.PageTx(f.ctx, tx, id, snap.ResultID, 0, 20, now.Add(SnapshotTTL))
		return err
	})
	requireError(t, err, 410, "result_expired")
	for i := 1; i < MaxUserSnapshots; i++ {
		f.query(id, now, messagecore.ReadAll)
	}
	err = f.tx(func(tx pgx.Tx) error {
		_, err := f.store.QueryTx(f.ctx, tx, id, messagecore.Query{Sort: messagecore.SortDescending, ReadFilter: messagecore.ReadAll, Limit: 20}, now)
		return err
	})
	requireError(t, err, 429, "snapshot_quota_exceeded")
}

func TestCanonicalCancelRaceAndSourceRedaction(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	task := f.submit(id, "cancel-race")
	lease, err := f.engine.Claim(f.ctx, "synthetic-owner")
	f.must(err)
	lease, err = f.engine.Start(f.ctx, lease)
	f.must(err)
	// The canceller is intentionally not the task's owner; recipient must use
	// tasks.actor_id rather than the event's operation actor.
	f.must(f.tx(func(tx pgx.Tx) error {
		_, err := f.engine.CancelTx(f.ctx, tx, tasks.Principal{TenantID: id.TenantID, ActorID: 99}, task.ID)
		return err
	}))
	_, err = f.engine.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{"sensitive":"private-source-sentinel"}`)})
	f.must(err)
	f.project()
	var terminalEvents int
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND state='succeeded'`, task.ID).Scan(&terminalEvents))
	if terminalEvents != 2 {
		t.Fatalf("cancel-race evidence=%d", terminalEvents)
	}
	snap := f.query(id, time.Now().Add(time.Second), messagecore.ReadAll)
	if snap.Total != 1 {
		t.Fatalf("duplicate terminal message: %+v", snap)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), "private-source-sentinel") {
		t.Fatal("task result leaked")
	}
	canceller := id
	canceller.UserID = 99
	if other := f.query(canceller, time.Now().Add(time.Second), messagecore.ReadAll); other.Total != 0 {
		t.Fatal("canceller received creator message")
	}
}

func TestProjectionWraparoundFindsLateCommittedLowID(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	low := f.submit(id, "late-low")
	conn, err := pgx.ConnectConfig(f.ctx, f.cfg)
	f.must(err)
	defer conn.Close(context.Background())
	tx, err := conn.Begin(f.ctx)
	f.must(err)
	defer tx.Rollback(context.Background())
	f.must(f.store.CheckSchemaTx(f.ctx, tx))
	_, err = f.engine.CancelTx(f.ctx, tx, tasks.Principal{TenantID: id.TenantID, ActorID: id.UserID}, low.ID)
	f.must(err)
	highIdentity := id
	highIdentity.TenantID = "synthetic-other-tenant"
	high := f.runHealth(highIdentity, "early-high")
	f.must(f.store.ProjectOnce(f.ctx))
	f.must(tx.Commit(f.ctx))
	f.project()
	snap := f.query(id, time.Now().Add(time.Second), messagecore.ReadAll)
	seen := map[string]bool{}
	for _, item := range snap.Items {
		seen[item.TaskID] = true
	}
	if snap.Total != 1 || !seen[low.ID] {
		t.Fatalf("late-commit source lost: %+v", snap)
	}
	highSnapshot := f.query(highIdentity, time.Now().Add(time.Second), messagecore.ReadAll)
	if highSnapshot.Total != 1 || highSnapshot.Items[0].TaskID != high.ID {
		t.Fatalf("higher committed source lost: %+v", highSnapshot)
	}
}

// These synthetic materialized rows test SQL candidate boundaries and the pure
// oracle, not source authenticity. The separate real-health tests above require
// actual ProductionRegistry execution and outbox projection end to end.
func TestSyntheticCandidateSQLMatchesPureOracle(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := now.Add(-time.Hour)
	end := now
	rows := []struct {
		id, title string
		at        time.Time
		owner     Identity
		read      bool
	}{
		{"message-2", "Case %_.*", start, id, false},
		{"message-10", "Case %_.*", start, id, true},
		{"message-3", "case %_.*", end.Add(-time.Microsecond), id, false},
		{"message-4", "Case at excluded end", end, id, false},
		{"message-5", "Case before excluded start", start.Add(-time.Microsecond), id, false},
		{"message-6", "Case other recipient", start, Identity{TenantID: id.TenantID, UserID: 42, SessionHash: id.SessionHash, AuthorizationVersion: 1}, false},
		{"message-7", "Case other tenant", start, Identity{TenantID: "foreign-tenant", UserID: id.UserID, SessionHash: id.SessionHash, AuthorizationVersion: 1}, false},
	}
	req := messagecore.Request{Scope: messagecore.Scope{TenantID: id.TenantID, UserID: "41", IncludePlatform: true}, Catalog: catalog(), Limits: limits(), ConfirmedAt: now, ReadState: messagecore.ReadState{TenantID: id.TenantID, UserID: "41", FirstReadAt: map[string]time.Time{}}}
	for i, row := range rows {
		task := f.runHealth(row.owner, fmt.Sprintf("oracle-source-%d", i))
		var event, version int64
		f.must(f.db.QueryRow(f.ctx, `SELECT id,result_version FROM adtr.task_events WHERE task_id=$1 AND action='finished'`, task.ID).Scan(&event, &version))
		_, err := f.db.Exec(f.ctx, `INSERT INTO adtr.messages(message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version,message_type,sub_type,title,description,occurred_at,ingested_at) VALUES($1,$2,$3,$4,$5,$6,'task_result','succeeded',$7,'synthetic oracle fixture',$8,$9)`, row.id, row.owner.TenantID, row.owner.UserID, task.ID, event, version, row.title, row.at, now)
		f.must(err)
		if row.read {
			_, err = f.db.Exec(f.ctx, `INSERT INTO adtr.message_reads VALUES($1,$2,$3,$4)`, row.owner.TenantID, row.owner.UserID, row.id, now.Add(-time.Minute))
			f.must(err)
		}
		if row.owner.TenantID == id.TenantID && row.owner.UserID == id.UserID {
			req.Messages = append(req.Messages, messagecore.Message{ID: row.id, TenantID: id.TenantID, ScopeKind: messagecore.ScopePlatform, Type: "task_result", SubType: "succeeded", Title: row.title, Description: "synthetic oracle fixture", OccurredAt: row.at, IngestedAt: now})
			if row.read {
				req.ReadState.FirstReadAt[row.id] = now.Add(-time.Minute)
			}
		}
	}
	for _, sort := range []messagecore.Sort{messagecore.SortAscending, messagecore.SortDescending} {
		for _, read := range []messagecore.ReadFilter{messagecore.ReadAll, messagecore.ReadUnread, messagecore.ReadRead} {
			for _, keyword := range []string{"", "Case", "case", "%_.*", ".*", "CASE"} {
				q := messagecore.Query{Start: start, End: end, Sort: sort, ReadFilter: read, Keyword: keyword, Types: []string{"task_result"}, SubTypes: []messagecore.TypeSubType{{Type: "task_result", SubType: "succeeded"}}, Offset: 0, Limit: 2}
				req.Query = q
				expected, err := messagecore.NewResult(req)
				f.must(err)
				want, err := expected.List()
				f.must(err)
				var actual Snapshot
				f.must(f.tx(func(tx pgx.Tx) (err error) { actual, err = f.store.QueryTx(f.ctx, tx, id, q, now); return err }))
				if actual.Total != want.Total || actual.Counts != (Counts{want.Counts.All, want.Counts.Unread, want.Counts.Read}) || len(actual.Items) != len(want.Items) {
					t.Fatalf("query %+v: actual %+v want %+v", q, actual, want)
				}
				for i, item := range actual.Items {
					if item.ID != want.Items[i].Message.ID || item.Read != want.Items[i].Read {
						t.Fatalf("query %+v position %d mismatch", q, i)
					}
				}
				var loc Location
				f.must(f.tx(func(tx pgx.Tx) (err error) {
					loc, err = f.store.LocateTx(f.ctx, tx, id, actual.ResultID, "message-3", 2, now)
					return err
				}))
				wantLoc, err := expected.Locate("message-3", 2)
				f.must(err)
				if loc.Found != wantLoc.Found || loc.Index != wantLoc.Index || loc.Page != wantLoc.Page {
					t.Fatalf("location mismatch %+v %+v", loc, wantLoc)
				}
				// Dispose only the synthetic test's derived snapshot, never history.
				_, err = f.db.Exec(f.ctx, `DELETE FROM adtr.message_results WHERE result_id=$1`, actual.ResultID)
				f.must(err)
			}
		}
	}
}

func TestProjectionFailureRollsBackCursorAndRecovers(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	f.runHealth(id, "projection-rollback")
	_, err := f.db.Exec(f.ctx, `CREATE FUNCTION adtr.synthetic_fail_message_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END; $$; CREATE TRIGGER synthetic_fail_message_insert BEFORE INSERT ON adtr.messages FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_fail_message_insert()`)
	f.must(err)
	if err = f.store.ProjectOnce(f.ctx); err == nil {
		t.Fatal("projection failure not reported")
	}
	var cursor int64
	var count int
	f.must(f.db.QueryRow(f.ctx, `SELECT after_id FROM adtr.message_projection_cursor WHERE singleton`).Scan(&cursor))
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.messages`).Scan(&count))
	if cursor != 0 || count != 0 {
		t.Fatalf("partial projection committed cursor=%d count=%d", cursor, count)
	}
	_, err = f.db.Exec(f.ctx, `DROP TRIGGER synthetic_fail_message_insert ON adtr.messages`)
	f.must(err)
	f.project()
	snap := f.query(id, time.Now().Add(time.Second), messagecore.ReadAll)
	if snap.Total != 1 {
		t.Fatal("projection did not recover")
	}
}

func TestBoundedCleanupRetainsLiveSnapshots(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	now := time.Now().UTC()
	live := f.query(id, now, messagecore.ReadAll)
	// Derived-result fixtures exercise only the deletion batch boundary.
	_, err := f.db.Exec(f.ctx, `INSERT INTO adtr.message_results(result_id,tenant_id,user_id,session_hash,authorization_version,projection_version,confirmed_at,expires_at,payload)
 SELECT 'expired-'||i::text,tenant_id,user_id,session_hash,authorization_version,projection_version,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour',payload
 FROM adtr.message_results CROSS JOIN generate_series(1,205) i WHERE result_id=$1`, live.ResultID)
	f.must(err)
	f.must(f.store.CleanupOnce(f.ctx))
	var count int
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_results`).Scan(&count))
	if count != 6 {
		t.Fatalf("cleanup exceeded/failed batch: %d", count)
	}
	f.must(f.store.CleanupOnce(f.ctx))
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_results`).Scan(&count))
	if count != 1 {
		t.Fatalf("live snapshot removed: %d", count)
	}
}

func TestSyntheticCandidateBudgetRejectsBeforeSelectiveKeyword(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	task := f.runHealth(id, "budget-source")
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Deliberately synthetic bulk data for a protection-limit test. This is not
	// the source-acceptance test and never claims 1001 real executions occurred.
	_, err := f.db.Exec(f.ctx, `WITH generated AS (
 INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version,occurred_at)
 SELECT task_id,tenant_id,domain_id,actor_id,'synthetic-budget-fixture','succeeded',attempt,result_version+i,fencing_token,authorization_version,$2::timestamptz-interval '1 second' FROM adtr.tasks CROSS JOIN generate_series(1,1001) i WHERE task_id=$1
 RETURNING id,task_id,tenant_id,actor_id,result_version,occurred_at)
 INSERT INTO adtr.messages(message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version,message_type,sub_type,title,description,occurred_at)
 SELECT 'budget-'||id::text,tenant_id,actor_id,task_id,id,result_version,'task_result','succeeded','synthetic budget record','synthetic fixture only',occurred_at FROM generated`, task.ID, now)
	f.must(err)
	err = f.tx(func(tx pgx.Tx) error {
		_, err := f.store.QueryTx(f.ctx, tx, id, messagecore.Query{Sort: messagecore.SortAscending, ReadFilter: messagecore.ReadRead, Keyword: "no match", Limit: 20}, now)
		return err
	})
	requireError(t, err, 422, "query_scan_budget_exceeded")
	var count int
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_results`).Scan(&count))
	if count != 0 {
		t.Fatal("over-budget query persisted a partial result")
	}
}

func TestTenantAndGlobalSnapshotQuotaAreBounded(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	now := time.Now().UTC()
	base := f.query(id, now, messagecore.ReadAll)
	_, err := f.db.Exec(f.ctx, `INSERT INTO adtr.message_results(result_id,tenant_id,user_id,session_hash,authorization_version,projection_version,confirmed_at,expires_at,payload)
 SELECT 'quota-tenant-'||i::text,tenant_id,user_id+i,session_hash,authorization_version,projection_version,confirmed_at,expires_at,payload FROM adtr.message_results CROSS JOIN generate_series(1,127) i WHERE result_id=$1`, base.ResultID)
	f.must(err)
	err = f.tx(func(tx pgx.Tx) error {
		_, err := f.store.QueryTx(f.ctx, tx, id, messagecore.Query{Sort: messagecore.SortAscending, ReadFilter: messagecore.ReadAll, Limit: 20}, now)
		return err
	})
	requireError(t, err, 429, "snapshot_quota_exceeded")
	_, err = f.db.Exec(f.ctx, `DELETE FROM adtr.message_results WHERE result_id<>$1`, base.ResultID)
	f.must(err)
	_, err = f.db.Exec(f.ctx, `INSERT INTO adtr.message_results(result_id,tenant_id,user_id,session_hash,authorization_version,projection_version,confirmed_at,expires_at,payload)
 SELECT 'quota-global-'||i::text,'synthetic-global-'||i::text,user_id,session_hash,authorization_version,projection_version,confirmed_at,expires_at,payload FROM adtr.message_results CROSS JOIN generate_series(1,1023) i WHERE result_id=$1`, base.ResultID)
	f.must(err)
	err = f.tx(func(tx pgx.Tx) error {
		_, err := f.store.QueryTx(f.ctx, tx, id, messagecore.Query{Sort: messagecore.SortAscending, ReadFilter: messagecore.ReadAll, Limit: 20}, now)
		return err
	})
	requireError(t, err, 429, "snapshot_quota_exceeded")
}

func TestTxMethodsRequirePriorSchemaGate(t *testing.T) {
	f := newFixture(t)
	tx, err := f.db.Begin(f.ctx)
	f.must(err)
	defer tx.Rollback(context.Background())
	_, err = f.store.QueryTx(f.ctx, tx, testIdentity(), messagecore.Query{Sort: messagecore.SortAscending, ReadFilter: messagecore.ReadAll, Limit: 20}, time.Now())
	if !errors.Is(err, tasks.ErrSchemaGateRequired) {
		t.Fatalf("missing schema gate=%v", err)
	}
}

func TestConcurrentSameOperationReplaysOriginalReceipt(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	f.runHealth(id, "same-operation-source")
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	var wg sync.WaitGroup
	receipts := make(chan ReadReceipt, 16)
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var receipt ReadReceipt
			err := f.tx(func(tx pgx.Tx) (err error) {
				receipt, err = f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "same-operation", now.Add(time.Duration(i)*time.Millisecond))
				return err
			})
			if err != nil {
				errs <- err
			} else {
				receipts <- receipt
			}
		}(i)
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for err := range errs {
		f.must(err)
	}
	var original ReadReceipt
	count := 0
	for receipt := range receipts {
		if count == 0 {
			original = receipt
		} else if receipt != original {
			t.Fatalf("same operation changed original receipt: %+v %+v", original, receipt)
		}
		count++
	}
	if count != 16 || original.Progress != (Progress{Target: 1, Processed: 1, NewlyRead: 1}) {
		t.Fatalf("same operation count=%d receipt=%+v", count, original)
	}
	var persisted int
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_read_operations`).Scan(&persisted))
	if persisted != 1 {
		t.Fatalf("duplicate receipts=%d", persisted)
	}
}

func TestReceiptFailureRollsBackFirstReadAndSameOperationRetries(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	f.runHealth(id, "receipt-failure-source")
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	_, err := f.db.Exec(f.ctx, `CREATE FUNCTION adtr.synthetic_fail_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic receipt failure'; END; $$;CREATE TRIGGER synthetic_fail_receipt BEFORE INSERT ON adtr.message_read_operations FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_fail_receipt()`)
	f.must(err)
	err = f.tx(func(tx pgx.Tx) error {
		_, err := f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "retry-same-operation", now)
		return err
	})
	if err == nil {
		t.Fatal("receipt fault did not fail transaction")
	}
	var reads, receipts int
	f.must(f.db.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.message_reads),(SELECT count(*) FROM adtr.message_read_operations)`).Scan(&reads, &receipts))
	if reads != 0 || receipts != 0 {
		t.Fatalf("partial commit reads=%d receipts=%d", reads, receipts)
	}
	_, err = f.db.Exec(f.ctx, `DROP TRIGGER synthetic_fail_receipt ON adtr.message_read_operations`)
	f.must(err)
	var receipt ReadReceipt
	retryAt := now.Add(time.Second)
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		receipt, err = f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "retry-same-operation", retryAt)
		return err
	}))
	if receipt.Progress.NewlyRead != 1 || !receipt.FirstReadAt.Equal(retryAt.UTC().Truncate(time.Microsecond)) {
		t.Fatalf("failed attempt leaked first-read state: %+v", receipt)
	}
}

func TestSQLNanosecondEndAndStartCandidateBudget(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	task := f.runHealth(id, "nanosecond-source")
	now := time.Now().UTC().Truncate(time.Microsecond)
	base := now.Add(-time.Hour).Truncate(time.Second)
	var event, version int64
	f.must(f.db.QueryRow(f.ctx, `SELECT id,result_version FROM adtr.task_events WHERE task_id=$1 AND action='finished'`, task.ID).Scan(&event, &version))
	// Synthetic microsecond storage fixture, separate from real-source acceptance.
	_, err := f.db.Exec(f.ctx, `INSERT INTO adtr.messages(message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version,message_type,sub_type,title,description,occurred_at) VALUES('nano-original',$1,$2,$3,$4,$5,'task_result','succeeded','synthetic nanos','fixture only',$6)`, id.TenantID, id.UserID, task.ID, event, version, base.Add(time.Microsecond))
	f.must(err)
	var snap Snapshot
	q := messagecore.Query{Start: base, End: base.Add(1500 * time.Nanosecond), Sort: messagecore.SortAscending, ReadFilter: messagecore.ReadAll, Limit: 20}
	f.must(f.tx(func(tx pgx.Tx) (err error) { snap, err = f.store.QueryTx(f.ctx, tx, id, q, now); return err }))
	if snap.Total != 1 || snap.Items[0].ID != "nano-original" {
		t.Fatalf("1.5us end lost 1us event: %+v", snap)
	}
	_, err = f.db.Exec(f.ctx, `WITH generated AS (
 INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version,occurred_at)
 SELECT task_id,tenant_id,domain_id,actor_id,'synthetic-nanosecond-budget','succeeded',attempt,result_version+i,fencing_token,authorization_version,$2 FROM adtr.tasks CROSS JOIN generate_series(1,1000) i WHERE task_id=$1
 RETURNING id,task_id,tenant_id,actor_id,result_version,occurred_at)
 INSERT INTO adtr.messages(message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version,message_type,sub_type,title,description,occurred_at)
 SELECT 'nano-budget-'||id::text,tenant_id,actor_id,task_id,id,result_version,'task_result','succeeded','synthetic nanos','fixture only',occurred_at FROM generated`, task.ID, base.Add(time.Microsecond))
	f.must(err)
	q.Start = base.Add(1500 * time.Nanosecond)
	q.End = base.Add(3 * time.Microsecond)
	f.must(f.tx(func(tx pgx.Tx) (err error) { snap, err = f.store.QueryTx(f.ctx, tx, id, q, now); return err }))
	if snap.Total != 0 {
		t.Fatalf("1.5us start admitted 1us candidates: %+v", snap)
	}
}

// Synthetic noncanonical outbox noise creates controlled throughput without
// pretending that hundreds of health results were executed by the worker.
func (f *fixture) appendSweepNoise(taskID string, n int) {
	f.t.Helper()
	_, err := f.db.Exec(f.ctx, `WITH noise AS (
 INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version)
 SELECT task_id,tenant_id,domain_id,actor_id,'synthetic-sweep-noise','queued',attempt,result_version,fencing_token,authorization_version FROM adtr.tasks CROSS JOIN generate_series(1,$2::integer) i WHERE task_id=$1 RETURNING id,task_id)
 INSERT INTO adtr.task_outbox(task_id,event_id) SELECT task_id,id FROM noise`, taskID, n)
	f.must(err)
}
func TestFixedSweepUpperWrapsDespiteContinuousTail(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	low := f.submit(id, "continuous-low")
	conn, err := pgx.ConnectConfig(f.ctx, f.cfg)
	f.must(err)
	defer conn.Close(context.Background())
	tx, err := conn.Begin(f.ctx)
	f.must(err)
	defer tx.Rollback(context.Background())
	f.must(f.store.CheckSchemaTx(f.ctx, tx))
	_, err = f.engine.CancelTx(f.ctx, tx, tasks.Principal{TenantID: id.TenantID, ActorID: id.UserID}, low.ID)
	f.must(err)
	highIdentity := id
	highIdentity.TenantID = "synthetic-continuous-tenant"
	high := f.runHealth(highIdentity, "continuous-high")
	f.appendSweepNoise(high.ID, 401)
	f.must(f.store.ProjectOnce(f.ctx))
	var firstUpper, after int64
	f.must(f.db.QueryRow(f.ctx, `SELECT after_id,upper_id FROM adtr.message_projection_cursor WHERE singleton`).Scan(&after, &firstUpper))
	if after >= firstUpper {
		t.Fatal("fixture did not create a partial first sweep")
	}
	f.must(tx.Commit(f.ctx))
	found := false
	wrapped := false
	for range 8 {
		f.appendSweepNoise(high.ID, 200)
		f.must(f.store.ProjectOnce(f.ctx))
		var currentUpper int64
		f.must(f.db.QueryRow(f.ctx, `SELECT upper_id FROM adtr.message_projection_cursor WHERE singleton`).Scan(&currentUpper))
		if currentUpper != firstUpper {
			wrapped = true
		}
		f.must(f.db.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM adtr.messages WHERE source_task_id=$1)`, low.ID).Scan(&found))
		if found {
			break
		}
	}
	if !wrapped || !found {
		t.Fatalf("continuous append starved late event: wrapped=%v found=%v", wrapped, found)
	}
}
