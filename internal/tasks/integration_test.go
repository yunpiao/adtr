//go:build integration

package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Each test creates and owns a fresh database. Only that generated database is
// dropped; the supplied connection is never used as the test schema itself.
// Synthetic executors and grants live in this test file, never in production.
type taskFixture struct {
	t   *testing.T
	ctx context.Context
	cfg *pgx.ConnConfig
	db  *pgx.Conn
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()
	return newTaskFixtureWithSchema(t, Schema)
}

func newTaskFixtureWithSchema(t *testing.T, schema string) *taskFixture {
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
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	name := "adtr_tasks_" + hex.EncodeToString(random[:])
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
		t.Fatal("could not connect to fresh task-test database")
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	if _, err = db.Exec(ctx, "CREATE SCHEMA adtr;CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,5);"+schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `CREATE TABLE adtr.synthetic_task_grants (
 tenant_id text NOT NULL, actor_id bigint NOT NULL, domain_id text NOT NULL,
 kind text NOT NULL, readable boolean NOT NULL DEFAULT true,
 writeable boolean NOT NULL DEFAULT true, executable boolean NOT NULL DEFAULT true,
 version text NOT NULL DEFAULT 'synthetic-v1', PRIMARY KEY(tenant_id,actor_id,domain_id,kind)
)`); err != nil {
		t.Fatal(err)
	}
	return &taskFixture{t: t, ctx: ctx, cfg: cfg, db: db}
}

func (f *taskFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// Every invocation owns its connection, including concurrent submitters. A
// failed callback is always rolled back, just like the authenticated API path.
func (f *taskFixture) transact(fn func(pgx.Tx) error) error {
	conn, err := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := CheckSchemaTx(f.ctx, tx, 5); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

var taskTestPrincipal = Principal{TenantID: "synthetic-tenant", ActorID: 41}

// This authorizer deliberately reads current durable grants on every call. No
// cached principal or submit-time fingerprint can keep a revoked grant alive.
func syntheticTaskAuthorizer(ctx context.Context, tx pgx.Tx, principal Principal, scope Scope, action Action) (string, error) {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))", principal.TenantID); err != nil {
		return "", err
	}
	var version string
	err := tx.QueryRow(ctx, `SELECT version FROM adtr.synthetic_task_grants
 WHERE tenant_id=$1 AND actor_id=$2 AND domain_id=$3 AND kind=$4
 AND CASE $5 WHEN 'read' THEN readable WHEN 'write' THEN writeable
 WHEN 'execute' THEN executable ELSE false END`,
		principal.TenantID, principal.ActorID, scope.DomainID, scope.TaskName, string(action)).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAuthorization
	}
	return version, err
}

func (f *taskFixture) grant(principal Principal, domain, kind string) {
	f.t.Helper()
	f.exec(`INSERT INTO adtr.synthetic_task_grants(tenant_id,actor_id,domain_id,kind)
 VALUES($1,$2,$3,$4)`, principal.TenantID, principal.ActorID, domain, kind)
}

func (f *taskFixture) snapshot(id string) Task {
	f.t.Helper()
	task := Task{ID: id}
	err := f.db.QueryRow(f.ctx, `SELECT tenant_id,domain_id,kind,state,attempt,max_attempts,
 fencing_token,lease_owner,lease_until,result_version,progress,result,cursor,error_code,
 parent_task_id,authorization_version,next_attempt_at FROM adtr.tasks WHERE task_id=$1`, id).
		Scan(&task.TenantID, &task.DomainID, &task.Kind, &task.State, &task.Attempt, &task.MaxAttempts,
			&task.FencingToken, &task.LeaseOwner, &task.LeaseUntil, &task.ResultVersion, &task.Progress,
			&task.Result, &task.Cursor, &task.Error, &task.ParentID, &task.AuthorizationVersion, &task.NextAttemptAt)
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f *taskFixture) counts() (tasks, events, outbox int) {
	f.t.Helper()
	if err := f.db.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.tasks),
 (SELECT count(*) FROM adtr.task_events),(SELECT count(*) FROM adtr.task_outbox)`).
		Scan(&tasks, &events, &outbox); err != nil {
		f.t.Fatal(err)
	}
	return
}

func requireTaskProblem(t *testing.T, err error, status int) {
	t.Helper()
	var problem *Error
	if !errors.As(err, &problem) || problem.Status != status {
		t.Fatalf("got error %v, want task error status %d", err, status)
	}
}

func syntheticTaskKind(maxAttempts int, execute Executor) Kind {
	return Kind{
		Name: "synthetic", Version: 1, MaxAttempts: maxAttempts,
		Lease: 10 * time.Second, Heartbeat: 250 * time.Millisecond, Timeout: 15 * time.Second,
		RetryBase: 10 * time.Millisecond, RetryCap: 100 * time.Millisecond,
		RetryCodes: []string{"synthetic_transient"}, ReplaySafe: true,
		Validate: func(payload json.RawMessage) (json.RawMessage, error) {
			var value struct {
				Value string `json:"value"`
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fields); err != nil || len(fields) != 1 || fields["value"] == nil {
				return nil, errors.New("synthetic payload requires only value")
			}
			if err := json.Unmarshal(payload, &value); err != nil || value.Value == "" {
				return nil, errors.New("synthetic value must be nonempty")
			}
			return json.Marshal(value)
		},
		Execute: execute,
	}
}

func syntheticTaskSuccess(context.Context, Execution) Outcome {
	return Outcome{State: Succeeded, Result: json.RawMessage(`{"synthetic":true}`)}
}

func syntheticTaskInput(domain, key string) SubmitInput {
	return SubmitInput{TaskName: "synthetic", DomainID: domain, PayloadVersion: 1,
		Payload: json.RawMessage(`{"value":"first"}`), IdempotencyKey: key}
}

func TestTaskHistoryIsAppendOnly(t *testing.T) {
	f := newTaskFixture(t)
	// Even an empty audit/outbox must reject truncate: row triggers alone cannot
	// defend against TRUNCATE, and there must be no silent history reset path.
	for _, table := range []string{"adtr.task_events", "adtr.task_outbox", "adtr.task_schedule_occurrences"} {
		if _, err := f.db.Exec(f.ctx, "TRUNCATE "+table+" CASCADE"); err == nil {
			t.Fatalf("%s permitted history truncation", table)
		}
	}
}

func (f *taskFixture) engine(kind Kind) *Engine {
	f.t.Helper()
	registry, err := NewRegistry(kind)
	if err != nil {
		f.t.Fatal(err)
	}
	engine, err := New(f.cfg, registry, syntheticTaskAuthorizer, 5)
	if err != nil {
		f.t.Fatal(err)
	}
	return engine
}

func (f *taskFixture) submit(e *Engine, p Principal, in SubmitInput) (Submission, error) {
	var submission Submission
	err := f.transact(func(tx pgx.Tx) (err error) {
		submission, err = e.SubmitTx(f.ctx, tx, p, in)
		return err
	})
	return submission, err
}

func (f *taskFixture) mustSubmit(e *Engine, p Principal, in SubmitInput) Task {
	f.t.Helper()
	submission, err := f.submit(e, p, in)
	if err != nil {
		f.t.Fatal(err)
	}
	if submission.Replayed {
		f.t.Fatal("new submission unexpectedly replayed")
	}
	return submission.Task
}

func (f *taskFixture) cancel(e *Engine, p Principal, id string) (Task, error) {
	var task Task
	err := f.transact(func(tx pgx.Tx) (err error) {
		task, err = e.CancelTx(f.ctx, tx, p, id)
		return err
	})
	return task, err
}

func (f *taskFixture) detail(e *Engine, p Principal, id string) (Detail, error) {
	var detail Detail
	err := f.transact(func(tx pgx.Tx) (err error) {
		detail, err = e.DetailTx(f.ctx, tx, p, id)
		return err
	})
	return detail, err
}

func (f *taskFixture) start(e *Engine, owner string) Lease {
	f.t.Helper()
	lease, err := e.Claim(f.ctx, owner)
	if err != nil {
		f.t.Fatal(err)
	}
	started, err := e.Start(f.ctx, lease)
	if err != nil {
		f.t.Fatal(err)
	}
	return started
}

func (f *taskFixture) requireState(id string, state State, attempt int) Task {
	f.t.Helper()
	task := f.snapshot(id)
	if task.State != state || task.Attempt != attempt {
		f.t.Fatalf("task %s: got state=%s attempt=%d, want state=%s attempt=%d", id, task.State, task.Attempt, state, attempt)
	}
	return task
}

func TestTaskSubmitIsAtomicAndIdempotent(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	in := syntheticTaskInput("domain-a", "atomic")
	rollback := errors.New("synthetic caller rollback")
	err := f.transact(func(tx pgx.Tx) error {
		if _, err := e.SubmitTx(f.ctx, tx, taskTestPrincipal, in); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if tasks, events, outbox := f.counts(); tasks != 0 || events != 0 || outbox != 0 {
		t.Fatalf("rolled-back submission leaked task/audit/outbox: %d/%d/%d", tasks, events, outbox)
	}
	// An error in either durable side record rolls back every submission write.
	f.exec(`CREATE FUNCTION adtr.synthetic_reject_insert() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic persistence failure'; END; $$`)
	for _, table := range []string{"task_events", "task_outbox"} {
		f.exec("CREATE TRIGGER synthetic_fail BEFORE INSERT ON adtr." + table + " FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_reject_insert()")
		if _, err := f.submit(e, taskTestPrincipal, in); err == nil {
			t.Fatalf("%s failure did not reject submission", table)
		}
		if tasks, events, outbox := f.counts(); tasks != 0 || events != 0 || outbox != 0 {
			t.Fatalf("%s failure leaked task/audit/outbox: %d/%d/%d", table, tasks, events, outbox)
		}
		f.exec("DROP TRIGGER synthetic_fail ON adtr." + table)
	}
	created := f.mustSubmit(e, taskTestPrincipal, in)
	in.Payload = json.RawMessage(` { "value" : "first" } `)
	replay, err := f.submit(e, taskTestPrincipal, in)
	if err != nil || !replay.Replayed || replay.Task.ID != created.ID {
		t.Fatalf("canonical replay: %+v, %v", replay, err)
	}
	in.Payload = json.RawMessage(`{"value":"different"}`)
	_, err = f.submit(e, taskTestPrincipal, in)
	requireTaskProblem(t, err, 409)
	in.IdempotencyKey = "duplicate-fields"
	in.Payload = json.RawMessage(`{"value":"discarded","value":"first"}`)
	if _, err = f.submit(e, taskTestPrincipal, in); err == nil {
		t.Fatal("ambiguous duplicate payload keys accepted")
	}
	if tasks, events, outbox := f.counts(); tasks != 1 || events != 1 || outbox != 1 {
		t.Fatalf("idempotent replay/conflict appended records: %d/%d/%d", tasks, events, outbox)
	}
	var tenant, domain, authVersion, action string
	var actor int64
	if err = f.db.QueryRow(f.ctx, `SELECT ev.tenant_id,ev.domain_id,ev.actor_id,ev.authorization_version,ev.action
 FROM adtr.task_events ev JOIN adtr.task_outbox ob ON ob.event_id=ev.id
 WHERE ev.task_id=$1 AND ob.task_id=ev.task_id`, created.ID).Scan(&tenant, &domain, &actor, &authVersion, &action); err != nil {
		t.Fatal(err)
	}
	if tenant != taskTestPrincipal.TenantID || domain != "domain-a" || actor != taskTestPrincipal.ActorID || authVersion != "synthetic-v1" || action != "submitted" {
		t.Fatalf("wrong durable audit attribution: %s/%s/%d/%s/%s", tenant, domain, actor, authVersion, action)
	}
	for _, table := range []string{"adtr.task_events", "adtr.task_outbox"} {
		for _, statement := range []string{"UPDATE " + table + " SET task_id=task_id", "DELETE FROM " + table, "TRUNCATE " + table + " CASCADE"} {
			if _, err = f.db.Exec(f.ctx, statement); err == nil {
				t.Fatalf("history mutation accepted: %s", statement)
			}
		}
	}
}

func TestTaskConcurrentIdempotencyAndConflict(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	type response struct {
		submission Submission
		err        error
	}
	const count = 12
	answers := make(chan response, count)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			submission, err := f.submit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "same-key"))
			answers <- response{submission, err}
		}()
	}
	close(gate)
	wg.Wait()
	close(answers)
	id := ""
	created := 0
	for answer := range answers {
		if answer.err != nil {
			t.Fatal(answer.err)
		}
		if id == "" {
			id = answer.submission.Task.ID
		}
		if id != answer.submission.Task.ID {
			t.Fatal("same idempotency key created two tasks")
		}
		if !answer.submission.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d tasks for one logical submission", created)
	}
	if tasks, events, outbox := f.counts(); tasks != 1 || events != 1 || outbox != 1 {
		t.Fatalf("duplicate records: %d/%d/%d", tasks, events, outbox)
	}
	answers = make(chan response, 2)
	gate = make(chan struct{})
	for _, value := range []string{"left", "right"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			<-gate
			in := syntheticTaskInput("domain-a", "conflicting-key")
			in.Payload = json.RawMessage(fmt.Sprintf(`{"value":%q}`, value))
			submission, err := f.submit(e, taskTestPrincipal, in)
			answers <- response{submission, err}
		}(value)
	}
	close(gate)
	wg.Wait()
	close(answers)
	successes, conflicts := 0, 0
	for answer := range answers {
		if answer.err == nil {
			successes++
			continue
		}
		requireTaskProblem(t, answer.err, 409)
		conflicts++
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent conflict got success=%d conflict=%d", successes, conflicts)
	}
	if tasks, events, outbox := f.counts(); tasks != 2 || events != 2 || outbox != 2 {
		t.Fatalf("conflict records: %d/%d/%d", tasks, events, outbox)
	}
	// Identical domain IDs and keys in another tenant, and another domain in the
	// same tenant, are independent keys and must never alias each other.
	other := Principal{TenantID: "other-tenant", ActorID: 42}
	f.grant(other, "domain-a", "synthetic")
	f.grant(taskTestPrincipal, "domain-b", "synthetic")
	foreign := f.mustSubmit(e, other, syntheticTaskInput("domain-a", "same-key"))
	anotherDomain := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-b", "same-key"))
	if foreign.ID == id || anotherDomain.ID == id || foreign.ID == anotherDomain.ID {
		t.Fatal("idempotency escaped tenant/domain scope")
	}
}

func TestTaskScopeIsolationAndLiveReadRevocation(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	f.grant(taskTestPrincipal, "domain-b", "synthetic")
	other := Principal{TenantID: "other-tenant", ActorID: 42}
	f.grant(other, "domain-a", "synthetic")
	a := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "one"))
	b := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-b", "two"))
	foreign := f.mustSubmit(e, other, syntheticTaskInput("domain-a", "one"))
	for _, id := range []string{a.ID, b.ID} {
		_, err := f.detail(e, other, id)
		requireTaskProblem(t, err, 404)
		_, err = f.cancel(e, other, id)
		requireTaskProblem(t, err, 404)
	}
	if _, err := f.detail(e, taskTestPrincipal, foreign.ID); err == nil {
		t.Fatal("foreign task readable")
	}
	f.exec("UPDATE adtr.synthetic_task_grants SET readable=false,writeable=false,version='synthetic-v2' WHERE tenant_id=$1 AND domain_id='domain-b'", taskTestPrincipal.TenantID)
	_, deniedDetail := f.detail(e, taskTestPrincipal, b.ID)
	requireTaskProblem(t, deniedDetail, 404)
	_, deniedCancel := f.cancel(e, taskTestPrincipal, b.ID)
	requireTaskProblem(t, deniedCancel, 404)
	if _, err := f.submit(e, taskTestPrincipal, syntheticTaskInput("domain-b", "denied")); !errors.Is(err, ErrAuthorization) {
		t.Fatalf("revoked domain submit: %v", err)
	}
	var list List
	if err := f.transact(func(tx pgx.Tx) (err error) {
		list, err = e.ListTx(f.ctx, tx, taskTestPrincipal, Filter{PageIdx: 1, PageSize: 1})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if list.Page.Total != 1 || len(list.Tasks) != 1 || list.Tasks[0].ID != a.ID || !list.Exhausted {
		t.Fatalf("unauthorized rows leaked into list/count/pagination: %+v", list)
	}
	f.requireState(b.ID, Queued, 0)
}

func TestTaskClaimRaceFencingAndAtomicCheckpoint(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "claim-race"))
	type result struct {
		lease Lease
		err   error
	}
	const contenders = 10
	results := make(chan result, contenders)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			lease, err := e.Claim(f.ctx, fmt.Sprintf("worker-%d", i))
			results <- result{lease, err}
		}(i)
	}
	close(gate)
	wg.Wait()
	close(results)
	var winner Lease
	won := 0
	for result := range results {
		if errors.Is(result.err, ErrNoTask) {
			continue
		}
		if result.err != nil {
			t.Fatal(result.err)
		}
		winner = result.lease
		won++
	}
	if won != 1 || winner.Task.ID != task.ID {
		t.Fatalf("claim race winners=%d lease=%+v", won, winner)
	}
	f.requireState(task.ID, Queued, 0)
	for range 3 {
		if _, err := e.Claim(f.ctx, "duplicate-wakeup"); !errors.Is(err, ErrNoTask) {
			t.Fatalf("duplicate wakeup: %v", err)
		}
	}
	started, err := e.Start(f.ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	f.requireState(task.ID, Running, 1)
	if _, err = e.Start(f.ctx, winner); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("duplicate start: %v", err)
	}
	cursor := json.RawMessage(`{"sourceEvent":"synthetic-event-17"}`)
	resultData := json.RawMessage(`{"objects":["synthetic-object-17"]}`)
	version, err := e.Progress(f.ctx, started, 25, cursor, resultData, started.Task.ResultVersion)
	if err != nil || version != started.Task.ResultVersion+1 {
		t.Fatalf("checkpoint version=%d error=%v", version, err)
	}
	if _, err = e.Progress(f.ctx, started, 50, json.RawMessage(`{"sourceEvent":"stale"}`), json.RawMessage(`{"objects":[]}`), started.Task.ResultVersion); err == nil {
		t.Fatal("stale checkpoint overwrote newer result")
	}
	saved := f.requireState(task.ID, Running, 1)
	var savedCursor map[string]string
	var savedResult map[string][]string
	if err = json.Unmarshal(saved.Cursor, &savedCursor); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(saved.Result, &savedResult); err != nil {
		t.Fatal(err)
	}
	if saved.Progress != 25 || saved.ResultVersion != version || savedCursor["sourceEvent"] != "synthetic-event-17" || len(savedResult["objects"]) != 1 || savedResult["objects"][0] != "synthetic-object-17" {
		t.Fatalf("cursor/result were not atomic: %+v", saved)
	}
	for _, forged := range []Lease{{Task: started.Task, Owner: "wrong-owner", Token: started.Token}, {Task: started.Task, Owner: started.Owner, Token: started.Token + 1}} {
		if _, err = e.Heartbeat(f.ctx, forged); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("forged heartbeat: %v", err)
		}
		if _, err = e.Progress(f.ctx, forged, 90, cursor, resultData, version); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("forged checkpoint: %v", err)
		}
		if _, err = e.Finish(f.ctx, forged, Outcome{State: Succeeded, Result: resultData}); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("forged finish: %v", err)
		}
	}
	f.exec("UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1", task.ID)
	if _, err = e.Heartbeat(f.ctx, started); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired heartbeat: %v", err)
	}
	if _, err = e.Progress(f.ctx, started, 90, cursor, resultData, version); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired checkpoint: %v", err)
	}
	if _, err = e.Finish(f.ctx, started, Outcome{State: Succeeded, Result: resultData}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired finish: %v", err)
	}
	// Reconstruct the engine with no in-memory execution history, as after a crash.
	restarted := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	recovered, err := restarted.RecoverExpired(f.ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("crash recovery count=%d error=%v", recovered, err)
	}
	after := f.requireState(task.ID, RetryWait, 1)
	if after.FencingToken <= started.Token || after.LeaseOwner != "" || after.LeaseUntil != nil {
		t.Fatalf("lost worker not fenced: %+v", after)
	}
	if _, err = e.Finish(f.ctx, started, Outcome{State: Succeeded, Result: resultData}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old worker finish after recovery: %v", err)
	}
	f.exec("UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", task.ID)
	next := f.start(restarted, "replacement-worker")
	if next.Token <= started.Token || next.Task.Attempt != 2 {
		t.Fatalf("replacement token/attempt: %+v", next)
	}
	if _, err = restarted.Finish(f.ctx, next, Outcome{State: Succeeded, Result: resultData}); err != nil {
		t.Fatal(err)
	}
	f.requireState(task.ID, Succeeded, 2)
	if _, err = e.Finish(f.ctx, started, Outcome{State: Failed, Code: "stale_worker"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old worker overwrote new terminal result: %v", err)
	}
	if recovered, err = restarted.RecoverExpired(f.ctx); err != nil || recovered != 0 {
		t.Fatalf("terminal task recovered again: %d, %v", recovered, err)
	}
}

func TestTaskMaxAttemptsIncludesFirstExecution(t *testing.T) {
	for _, maxAttempts := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("max_%d", maxAttempts), func(t *testing.T) {
			f := newTaskFixture(t)
			e := f.engine(syntheticTaskKind(maxAttempts, syntheticTaskSuccess))
			f.grant(taskTestPrincipal, "domain-a", "synthetic")
			task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "retry-budget"))
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				lease := f.start(e, fmt.Sprintf("attempt-%d", attempt))
				if lease.Task.Attempt != attempt {
					t.Fatalf("attempt=%d want=%d", lease.Task.Attempt, attempt)
				}
				out, err := e.Finish(f.ctx, lease, Outcome{State: Failed, Code: "synthetic_transient", Retryable: true, Result: json.RawMessage(`{"synthetic":true}`)})
				if err != nil {
					t.Fatal(err)
				}
				if attempt == maxAttempts {
					if out.State != DeadLetter || out.Attempt != maxAttempts {
						t.Fatalf("exhausted task: %+v", out)
					}
					if _, err = e.Claim(f.ctx, "over-budget"); !errors.Is(err, ErrNoTask) {
						t.Fatalf("dead letter was automatically claimed: %v", err)
					}
				} else {
					if out.State != RetryWait || out.NextAttemptAt == nil {
						t.Fatalf("missing durable retry wait: %+v", out)
					}
					f.exec("UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()+interval '1 hour' WHERE task_id=$1", task.ID)
					if _, err = e.Claim(f.ctx, "early-retry"); !errors.Is(err, ErrNoTask) {
						t.Fatalf("backoff ignored: %v", err)
					}
					f.requireState(task.ID, RetryWait, attempt)
					f.exec("UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", task.ID)
				}
			}
			before, err := f.detail(e, taskTestPrincipal, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			var recovered Submission
			err = f.transact(func(tx pgx.Tx) (err error) {
				recovered, err = e.RecoverTx(f.ctx, tx, taskTestPrincipal, task.ID, "explicit-recovery")
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Task.ID == task.ID || recovered.Task.ParentID != task.ID || recovered.Task.Attempt != 0 || recovered.Task.State != Queued {
				t.Fatalf("recovery reset history instead of creating a child: %+v", recovered)
			}
			after, err := f.detail(e, taskTestPrincipal, task.ID)
			if err != nil || after.Task.State != DeadLetter || after.Task.Attempt != maxAttempts || len(after.Events) != len(before.Events) {
				t.Fatalf("recovery mutated parent: %+v, %v", after, err)
			}
			f.exec("UPDATE adtr.synthetic_task_grants SET writeable=false,version='synthetic-v2'")
			err = f.transact(func(tx pgx.Tx) error {
				_, err := e.RecoverTx(f.ctx, tx, taskTestPrincipal, task.ID, "revoked-recovery")
				return err
			})
			requireTaskProblem(t, err, 404)
		})
	}
}

func TestTaskUnapprovedFailuresNeverRetry(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(5, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	for _, code := range []string{"credentials_invalid", "certificate_invalid", "invalid_input", "not_in_retry_policy"} {
		task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", code))
		lease := f.start(e, "nonretry-worker")
		outcome, err := e.Finish(f.ctx, lease, Outcome{State: Failed, Code: code, Retryable: true})
		if err != nil || outcome.State != Failed || outcome.Attempt != 1 {
			t.Fatalf("forbidden retry code=%s outcome=%+v err=%v", code, outcome, err)
		}
		f.requireState(task.ID, Failed, 1)
	}
	if _, err := e.Claim(f.ctx, "no-unsafe-retries"); !errors.Is(err, ErrNoTask) {
		t.Fatalf("nonretryable failure requeued: %v", err)
	}
}

func TestTaskCancelQueuedRetryAndCompletionRace(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	queued := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "before-start"))
	reserved, err := e.Claim(f.ctx, "reserved")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.cancel(e, taskTestPrincipal, queued.ID); err != nil {
		t.Fatal(err)
	}
	f.requireState(queued.ID, Cancelled, 0)
	if _, err = e.Start(f.ctx, reserved); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("cancelled queued task started: %v", err)
	}
	retry := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "cancel-backoff"))
	lease := f.start(e, "retry-worker")
	if _, err = e.Finish(f.ctx, lease, Outcome{State: Failed, Code: "synthetic_transient", Retryable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.cancel(e, taskTestPrincipal, retry.ID); err != nil {
		t.Fatal(err)
	}
	f.requireState(retry.ID, Cancelled, 1)
	race := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "cancel-completion-race"))
	lease = f.start(e, "race-worker")
	if _, err = f.cancel(e, taskTestPrincipal, race.ID); err != nil {
		t.Fatal(err)
	}
	f.requireState(race.ID, CancelRequested, 1)
	actual := json.RawMessage(`{"alreadyCompleted":true}`)
	if _, err = e.Finish(f.ctx, lease, Outcome{State: Succeeded, Result: actual}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.detail(e, taskTestPrincipal, race.ID)
	if err != nil || detail.Task.State != Succeeded {
		t.Fatalf("real completion erased by cancellation: %+v %v", detail, err)
	}
	matches := 0
	for _, event := range detail.Events {
		if event.Action == "completed-with-cancel-race" {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("completion race audit count=%d", matches)
	}
}

func awaitTaskSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func (f *taskFixture) awaitState(id string, want State) Task {
	f.t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		task := f.snapshot(id)
		if task.State == want {
			return task
		}
		select {
		case <-deadline.C:
			f.t.Fatalf("task %s state=%s, want %s", id, task.State, want)
		case <-tick.C:
		}
	}
}

func TestTaskCancellationWaitsForExecutorToReturn(t *testing.T) {
	f := newTaskFixture(t)
	entered, cancelObserved, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	allowReturn := func() { releaseOnce.Do(func() { close(release) }) }
	execute := func(ctx context.Context, ex Execution) Outcome {
		close(entered)
		<-ctx.Done()
		close(cancelObserved)
		// Context cancellation is only a stop request. The executor deliberately
		// remains alive until cleanup is acknowledged through this separate gate.
		<-release
		return Outcome{State: Failed, Code: "synthetic_executor_stopped", Result: json.RawMessage(`{"stopped":true}`)}
	}
	e := f.engine(syntheticTaskKind(2, execute))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "running-cancel"))
	var worked bool
	var workerErr error
	go func() { defer close(finished); worked, workerErr = e.RunOne(f.ctx, "cancellation-worker") }()
	t.Cleanup(func() { allowReturn(); awaitTaskSignal(t, finished, "executor cleanup") })
	awaitTaskSignal(t, entered, "executor start")
	if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
		t.Fatal(err)
	}
	awaitTaskSignal(t, cancelObserved, "persisted cancellation reaching executor context")
	f.requireState(task.ID, CancelRequested, 1)
	select {
	case <-finished:
		t.Fatal("worker acknowledged cancellation before executor returned")
	default:
	}
	detail, err := f.detail(e, taskTestPrincipal, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range detail.Events {
		if event.State == Cancelled {
			t.Fatal("cancelled audit written before executor return")
		}
	}
	allowReturn()
	awaitTaskSignal(t, finished, "cancellation acknowledgement")
	if !worked || workerErr != nil {
		t.Fatalf("worked=%v error=%v", worked, workerErr)
	}
	f.requireState(task.ID, Cancelled, 1)
}

func TestTaskLiveExecutionRevocation(t *testing.T) {
	t.Run("before_start", func(t *testing.T) {
		f := newTaskFixture(t)
		var calls atomic.Int32
		e := f.engine(syntheticTaskKind(2, func(context.Context, Execution) Outcome { calls.Add(1); return Outcome{State: Succeeded} }))
		f.grant(taskTestPrincipal, "domain-a", "synthetic")
		task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "revoked-before-start"))
		f.exec("UPDATE adtr.synthetic_task_grants SET executable=false,version='synthetic-v2'")
		worked, err := e.RunOne(f.ctx, "revoked-worker")
		if err != nil || !worked || calls.Load() != 0 {
			t.Fatalf("revoked actor reached executor: worked=%v calls=%d err=%v", worked, calls.Load(), err)
		}
		saved := f.requireState(task.ID, Failed, 0)
		if saved.Error != "authorization_revoked" {
			t.Fatalf("revocation reason=%s", saved.Error)
		}
	})
	t.Run("while_running", func(t *testing.T) {
		f := newTaskFixture(t)
		entered, stopped, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		allowReturn := func() { once.Do(func() { close(release) }) }
		e := f.engine(syntheticTaskKind(2, func(ctx context.Context, ex Execution) Outcome {
			close(entered)
			<-ctx.Done()
			close(stopped)
			<-release
			// Even a late success cannot overrule a current permission revocation.
			return Outcome{State: Succeeded, Result: json.RawMessage(`{"lateSuccess":true}`)}
		}))
		f.grant(taskTestPrincipal, "domain-a", "synthetic")
		task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "revoked-while-running"))
		var workerErr error
		go func() { defer close(finished); _, workerErr = e.RunOne(f.ctx, "live-revocation-worker") }()
		t.Cleanup(func() { allowReturn(); awaitTaskSignal(t, finished, "revoked executor cleanup") })
		awaitTaskSignal(t, entered, "executor start")
		f.exec("UPDATE adtr.synthetic_task_grants SET executable=false,version='synthetic-v2'")
		awaitTaskSignal(t, stopped, "live revocation reaching executor")
		saved := f.requireState(task.ID, CancelRequested, 1)
		if saved.Error != "authorization_revoked" {
			t.Fatalf("missing revocation marker: %+v", saved)
		}
		allowReturn()
		awaitTaskSignal(t, finished, "revoked executor return")
		if workerErr != nil {
			t.Fatal(workerErr)
		}
		saved = f.requireState(task.ID, Failed, 1)
		if saved.Error != "authorization_revoked" || saved.NextAttemptAt != nil {
			t.Fatalf("revocation was retried or ignored: %+v", saved)
		}
	})
}

func TestTaskDomainsExecuteInParallelWithFailureIsolation(t *testing.T) {
	f := newTaskFixture(t)
	enteredA, releaseA := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	var callsA, callsB atomic.Int32
	allowReturn := func() { releaseOnce.Do(func() { close(releaseA) }) }
	e := f.engine(syntheticTaskKind(2, func(ctx context.Context, ex Execution) Outcome {
		if ex.Task.DomainID == "domain-a" {
			callsA.Add(1)
			startOnce.Do(func() { close(enteredA) })
			select {
			case <-releaseA:
				return Outcome{State: Succeeded, Result: json.RawMessage(`{"domain":"a"}`)}
			case <-ctx.Done():
				return Outcome{State: Failed, Code: "synthetic_stopped"}
			}
		}
		callsB.Add(1)
		return Outcome{State: Failed, Code: "credentials_invalid", Retryable: true}
	}))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	f.grant(taskTestPrincipal, "domain-b", "synthetic")
	a1 := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "a-first"))
	a2 := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "a-second"))
	b := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-b", "b-first"))
	worker, err := NewWorker(e, WorkerConfig{Owner: "parallel-worker", Concurrency: 3, PollInterval: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(f.ctx)
	finished := make(chan struct{})
	var workerErr error
	go func() { defer close(finished); workerErr = worker.Run(ctx) }()
	t.Cleanup(func() { allowReturn(); stop(); awaitTaskSignal(t, finished, "parallel worker shutdown") })
	awaitTaskSignal(t, enteredA, "domain-a executor start")
	f.awaitState(b.ID, Failed)
	f.requireState(a1.ID, Running, 1)
	f.requireState(a2.ID, Queued, 0)
	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("domain lease isolation failed: domain-a=%d domain-b=%d", callsA.Load(), callsB.Load())
	}
	allowReturn()
	f.awaitState(a1.ID, Succeeded)
	f.awaitState(a2.ID, Succeeded)
	f.requireState(b.ID, Failed, 1)
	if callsA.Load() != 2 || callsB.Load() != 1 {
		t.Fatalf("unexpected replay across domains: domain-a=%d domain-b=%d", callsA.Load(), callsB.Load())
	}
	stop()
	awaitTaskSignal(t, finished, "worker shutdown")
	if workerErr != nil {
		t.Fatal(workerErr)
	}
}

func TestTaskLostExecutorDoesNotConfirmCancellation(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "lost-during-cancel"))
	lease := f.start(e, "lost-worker")
	if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
		t.Fatal(err)
	}
	f.exec("UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1", task.ID)
	recovered, err := e.RecoverExpired(f.ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("recover=%d err=%v", recovered, err)
	}
	saved := f.requireState(task.ID, Failed, 1)
	if saved.Error != "cancellation_unconfirmed" || saved.NextAttemptAt != nil {
		t.Fatalf("lost executor falsely acknowledged: %+v", saved)
	}
	if _, err = e.Finish(f.ctx, lease, Outcome{State: Cancelled}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("lost executor supplied late cancellation: %v", err)
	}
	detail, err := f.detail(e, taskTestPrincipal, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range detail.Events {
		if event.State == Cancelled {
			t.Fatal("audit claimed unconfirmed cancellation succeeded")
		}
	}
}

func TestTaskPartialFailurePreservesObjectEvidenceAndRejectsWholeTaskReplay(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "partial-objects"))
	lease := f.start(e, "partial-worker")
	results := json.RawMessage(`{"objects":[{"objectId":"synthetic-a","state":"succeeded"},{"objectId":"synthetic-b","state":"failed"},{"objectId":"synthetic-c","state":"uncertain"}]}`)
	if _, err := e.Finish(f.ctx, lease, Outcome{State: PartialFailed, Result: results, Code: "partial_objects"}); err != nil {
		t.Fatal(err)
	}
	saved := f.requireState(task.ID, PartialFailed, 1)
	var got, want any
	if err := json.Unmarshal(saved.Result, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(results, &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("per-object evidence changed: %s", saved.Result)
	}
	err := f.transact(func(tx pgx.Tx) error {
		_, err := e.RecoverTx(f.ctx, tx, taskTestPrincipal, task.ID, "unsafe-whole-replay")
		return err
	})
	requireTaskProblem(t, err, 409)
	if _, err = e.Claim(f.ctx, "no-partial-replay"); !errors.Is(err, ErrNoTask) {
		t.Fatalf("partial failure automatically replayed: %v", err)
	}
}

func TestTaskAuthorizationLockPrecedesTaskLockDuringCancellation(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	e.database.RuntimeParams["application_name"] = "synthetic-lock-worker"
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "authorization-lock-order"))
	lease := f.start(e, "lock-order-worker")
	blocker, err := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close(context.Background())
	tx, err := blocker.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(f.ctx, "SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))", taskTestPrincipal.TenantID); err != nil {
		t.Fatal(err)
	}
	heartbeatDone := make(chan struct{})
	var heartbeatErr error
	go func() { defer close(heartbeatDone); _, heartbeatErr = e.Heartbeat(f.ctx, lease) }()
	t.Cleanup(func() { awaitTaskSignal(t, heartbeatDone, "lock-order heartbeat cleanup") })
	// Detect actual lock contention through PostgreSQL rather than assuming a
	// goroutine ran after a sleep. The worker must be waiting on authorization
	// without holding the task row needed by the API's cancellation transaction.
	waiting := false
	deadline := time.Now().Add(5 * time.Second)
	for !waiting && time.Now().Before(deadline) {
		if err = f.db.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM pg_stat_activity
   WHERE datname=current_database() AND application_name='synthetic-lock-worker'
   AND wait_event_type='Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			select {
			case <-heartbeatDone:
				t.Fatalf("heartbeat did not wait for authorization lock: %v", heartbeatErr)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if !waiting {
		t.Fatal("heartbeat never reached the held authorization lock")
	}
	probe, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, lockErr := probe.Exec(f.ctx, "SELECT task_id FROM adtr.tasks WHERE task_id=$1 FOR UPDATE NOWAIT", task.ID)
	_ = probe.Rollback(context.Background())
	if lockErr != nil {
		t.Fatalf("heartbeat acquired task row before authorization, creating a cancellation deadlock: %v", lockErr)
	}
	cancelDone := make(chan struct{})
	var cancelErr error
	go func() { defer close(cancelDone); _, cancelErr = f.cancel(e, taskTestPrincipal, task.ID) }()
	t.Cleanup(func() { awaitTaskSignal(t, cancelDone, "lock-order cancellation cleanup") })
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	awaitTaskSignal(t, heartbeatDone, "heartbeat after authorization lock release")
	awaitTaskSignal(t, cancelDone, "concurrent cancellation after authorization lock release")
	if heartbeatErr != nil && !errors.Is(heartbeatErr, ErrCancelRequested) {
		t.Fatal(heartbeatErr)
	}
	if cancelErr != nil {
		t.Fatal(cancelErr)
	}
	f.requireState(task.ID, CancelRequested, 1)
}

func TestTaskScheduledOccurrenceIsUniqueAndAtomic(t *testing.T) {
	f := newTaskFixture(t)
	kind := syntheticTaskKind(2, syntheticTaskSuccess)
	kind.Schedulable = true
	e := f.engine(kind)
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	f.grant(taskTestPrincipal, "domain-b", "synthetic")
	when := time.Date(2026, 10, 7, 9, 30, 0, 123456000, time.UTC)
	input := ScheduledInput{SubmitInput: syntheticTaskInput("domain-a", "ignored-scheduler-key"), ScheduleID: "synthetic-schedule", ScheduledAt: when, ExpectedAuthorizationVersion: "synthetic-v1"}
	submit := func(principal Principal, in ScheduledInput) (Submission, error) {
		var out Submission
		err := f.transact(func(tx pgx.Tx) (err error) { out, err = e.SubmitScheduledTx(f.ctx, tx, principal, in); return err })
		return out, err
	}
	type answer struct {
		submission Submission
		err        error
	}
	const competitors = 8
	results := make(chan answer, competitors)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range competitors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			in := input
			// The same instant in different UTC offsets is one occurrence.
			if i%2 == 0 {
				in.ScheduledAt = when.In(time.FixedZone("synthetic-offset", 8*60*60))
			}
			out, err := submit(taskTestPrincipal, in)
			results <- answer{out, err}
		}(i)
	}
	close(gate)
	wg.Wait()
	close(results)
	id := ""
	created := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if id == "" {
			id = result.submission.Task.ID
		}
		if result.submission.Task.ID != id {
			t.Fatal("one schedule occurrence created multiple tasks")
		}
		if !result.submission.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("schedule occurrence creation count=%d", created)
	}
	if tasks, events, outbox := f.counts(); tasks != 1 || events != 1 || outbox != 1 {
		t.Fatalf("scheduled duplicates: %d/%d/%d", tasks, events, outbox)
	}
	changed := input
	changed.Payload = json.RawMessage(`{"value":"changed"}`)
	_, err := submit(taskTestPrincipal, changed)
	requireTaskProblem(t, err, 409)
	changed = input
	changed.DomainID = "domain-b"
	_, err = submit(taskTestPrincipal, changed)
	requireTaskProblem(t, err, 409)
	if tasks, events, outbox := f.counts(); tasks != 1 || events != 1 || outbox != 1 {
		t.Fatalf("conflicting occurrence leaked task/audit/outbox: %d/%d/%d", tasks, events, outbox)
	}
	// The occurrence mapping participates in the same transaction as the task,
	// audit event and outbox, including a failure after those records are written.
	f.exec(`CREATE FUNCTION adtr.synthetic_reject_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic occurrence failure'; END; $$;
 CREATE TRIGGER synthetic_fail BEFORE INSERT ON adtr.task_schedule_occurrences
 FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_reject_occurrence()`)
	next := input
	next.ScheduledAt = when.Add(time.Minute)
	if _, err = submit(taskTestPrincipal, next); err == nil {
		t.Fatal("failed occurrence mapping did not fail submission")
	}
	if tasks, events, outbox := f.counts(); tasks != 1 || events != 1 || outbox != 1 {
		t.Fatalf("failed occurrence leaked task/audit/outbox: %d/%d/%d", tasks, events, outbox)
	}
	f.exec("DROP TRIGGER synthetic_fail ON adtr.task_schedule_occurrences")
	nextOut, err := submit(taskTestPrincipal, next)
	if err != nil || nextOut.Replayed || nextOut.Task.ID == id {
		t.Fatalf("next occurrence: %+v %v", nextOut, err)
	}
	other := Principal{TenantID: "other-tenant", ActorID: 42}
	f.grant(other, "domain-a", "synthetic")
	foreign, err := submit(other, input)
	if err != nil || foreign.Replayed || foreign.Task.ID == id {
		t.Fatalf("tenant-scoped schedule: %+v %v", foreign, err)
	}
	var count int
	if err = f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr.task_schedule_occurrences").Scan(&count); err != nil || count != 3 {
		t.Fatalf("occurrence count=%d err=%v", count, err)
	}
	for _, query := range []string{"UPDATE adtr.task_schedule_occurrences SET task_id=task_id", "DELETE FROM adtr.task_schedule_occurrences", "TRUNCATE adtr.task_schedule_occurrences"} {
		if _, err = f.db.Exec(f.ctx, query); err == nil {
			t.Fatalf("schedule occurrence history mutable: %s", query)
		}
	}
}

func TestTaskAuthorizationEpochChangeInvalidatesDurableWork(t *testing.T) {
	t.Run("queued_recovery_requires_new_epoch", func(t *testing.T) {
		f := newTaskFixture(t)
		var executed atomic.Int32
		e := f.engine(syntheticTaskKind(2, func(context.Context, Execution) Outcome { executed.Add(1); return Outcome{State: Succeeded} }))
		f.grant(taskTestPrincipal, "domain-a", "synthetic")
		original := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "old-epoch"))
		// A restored/current true grant does not resurrect previously queued work.
		f.exec("UPDATE adtr.synthetic_task_grants SET version='synthetic-v2'")
		worked, err := e.RunOne(f.ctx, "old-epoch-worker")
		if err != nil || !worked || executed.Load() != 0 {
			t.Fatalf("old-epoch work executed: worked=%v executed=%d error=%v", worked, executed.Load(), err)
		}
		old := f.requireState(original.ID, Failed, 0)
		if old.Error != "authorization_revoked" || old.AuthorizationVersion != "synthetic-v1" {
			t.Fatalf("original authorization evidence changed: %+v", old)
		}
		var recovered Submission
		err = f.transact(func(tx pgx.Tx) (err error) {
			recovered, err = e.RecoverTx(f.ctx, tx, taskTestPrincipal, original.ID, "new-epoch-recovery")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if recovered.Task.AuthorizationVersion != "synthetic-v2" || recovered.Task.ParentID != original.ID {
			t.Fatalf("explicit recovery did not capture current epoch: %+v", recovered)
		}
		worked, err = e.RunOne(f.ctx, "new-epoch-worker")
		if err != nil || !worked || executed.Load() != 1 {
			t.Fatalf("new-epoch execution: worked=%v executed=%d error=%v", worked, executed.Load(), err)
		}
		f.requireState(recovered.Task.ID, Succeeded, 1)
		f.requireState(original.ID, Failed, 0)
	})
	t.Run("running_epoch_change_cancels_executor", func(t *testing.T) {
		f := newTaskFixture(t)
		entered, stopped, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
		e := f.engine(syntheticTaskKind(2, func(ctx context.Context, ex Execution) Outcome {
			close(entered)
			<-ctx.Done()
			close(stopped)
			return Outcome{State: Succeeded, Result: json.RawMessage(`{"lateSuccess":true}`)}
		}))
		f.grant(taskTestPrincipal, "domain-a", "synthetic")
		task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "running-old-epoch"))
		ctx, stop := context.WithCancel(f.ctx)
		var workerErr error
		go func() { defer close(finished); _, workerErr = e.RunOne(ctx, "running-old-epoch-worker") }()
		t.Cleanup(func() { stop(); awaitTaskSignal(t, finished, "epoch-revoked executor cleanup") })
		awaitTaskSignal(t, entered, "old-epoch executor start")
		f.exec("UPDATE adtr.synthetic_task_grants SET version='synthetic-v2'")
		awaitTaskSignal(t, stopped, "epoch change reaching executor context")
		awaitTaskSignal(t, finished, "epoch-revoked executor return")
		if workerErr != nil {
			t.Fatal(workerErr)
		}
		saved := f.requireState(task.ID, Failed, 1)
		if saved.Error != "authorization_revoked" || saved.AuthorizationVersion != "synthetic-v1" || saved.NextAttemptAt != nil {
			t.Fatalf("epoch change ignored, rewritten or retried: %+v", saved)
		}
	})
}

func TestTaskCheckpointPersistenceFailureRollsBackEntireBatch(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "atomic-batch"))
	lease := f.start(e, "batch-worker")
	before := f.snapshot(task.ID)
	_, eventsBefore, outboxBefore := f.counts()
	f.exec(`CREATE FUNCTION adtr.synthetic_reject_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic checkpoint persistence failure'; END; $$;
 CREATE TRIGGER synthetic_fail BEFORE INSERT ON adtr.task_outbox
 FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_reject_checkpoint()`)
	if _, err := e.Progress(f.ctx, lease, 75, json.RawMessage(`{"sourceEvent":"synthetic-batch-4"}`), json.RawMessage(`{"count":4}`), before.ResultVersion); err == nil {
		t.Fatal("checkpoint accepted without durable outbox")
	}
	after := f.snapshot(task.ID)
	_, eventsAfter, outboxAfter := f.counts()
	if after.ResultVersion != before.ResultVersion || after.Progress != before.Progress || string(after.Cursor) != string(before.Cursor) || string(after.Result) != string(before.Result) || eventsAfter != eventsBefore || outboxAfter != outboxBefore {
		t.Fatalf("failed batch partially committed: before=%+v after=%+v events=%d/%d outbox=%d/%d", before, after, eventsBefore, eventsAfter, outboxBefore, outboxAfter)
	}
	f.exec("DROP TRIGGER synthetic_fail ON adtr.task_outbox")
	if _, err := e.Progress(f.ctx, lease, 75, json.RawMessage(`{"sourceEvent":"synthetic-batch-4"}`), json.RawMessage(`{"count":4}`), before.ResultVersion); err != nil {
		t.Fatal(err)
	}
}

func TestTaskFailurePreservesCommittedCheckpointResult(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(1, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "preserve-checkpoint"))
	lease := f.start(e, "checkpoint-worker")
	if _, err := e.Progress(f.ctx, lease, 50, json.RawMessage(`{"lastSource":"synthetic-9"}`), json.RawMessage(`{"confirmedObjects":["synthetic-9"]}`), lease.Task.ResultVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Finish(f.ctx, lease, Outcome{State: Failed, Code: "credentials_invalid"}); err != nil {
		t.Fatal(err)
	}
	saved := f.requireState(task.ID, Failed, 1)
	var result map[string][]string
	if err := json.Unmarshal(saved.Result, &result); err != nil {
		t.Fatal(err)
	}
	if saved.Progress != 50 || len(result["confirmedObjects"]) != 1 || result["confirmedObjects"][0] != "synthetic-9" {
		t.Fatalf("failure erased checkpoint evidence: %+v", saved)
	}
}

// Reproduce a stale candidate crossing an independent committed state change.
// Claim uses this exact locked-row admission helper after its candidate query;
// the stale argument models a READ COMMITTED EvalPlanQual selection race.
func TestTaskClaimRevalidatesLatestCandidateEligibility(t *testing.T) {
	for _, scenario := range []string{"live_lease", "running", "cancelled", "terminal", "retry_postponed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newTaskFixture(t)
			e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
			f.grant(taskTestPrincipal, "domain-a", "synthetic")
			candidate := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "stale-candidate"))
			var winner Lease
			var err error
			switch scenario {
			case "live_lease":
				winner, err = e.Claim(f.ctx, "winner")
			case "running":
				winner = f.start(e, "winner")
			case "cancelled":
				_, err = f.cancel(e, taskTestPrincipal, candidate.ID)
			case "terminal":
				winner = f.start(e, "winner")
				_, err = e.Finish(f.ctx, winner, Outcome{State: Succeeded})
			case "retry_postponed":
				winner = f.start(e, "winner")
				_, err = e.Finish(f.ctx, winner, Outcome{State: Failed, Code: "synthetic_transient", Retryable: true})
				f.exec("UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()+interval '1 hour' WHERE task_id=$1", candidate.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.detail(e, taskTestPrincipal, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			err = f.transact(func(tx pgx.Tx) error {
				lease, claimed, err := e.claimCandidate(f.ctx, tx, candidate, "stale-contender")
				if err != nil {
					return err
				}
				if claimed || lease.Task.ID != "" {
					t.Fatalf("claimed changed row: %+v", lease)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			after, err := f.detail(e, taskTestPrincipal, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Task.State != before.Task.State || after.Task.FencingToken != before.Task.FencingToken || after.Task.LeaseOwner != before.Task.LeaseOwner || after.Task.ResultVersion != before.Task.ResultVersion || len(after.Events) != len(before.Events) {
				t.Fatalf("stale candidate mutated current task: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestTaskAdmissionIdentityIsImmutable(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "immutable-admission"))
	updates := []string{"task_id='forged-id'", "tenant_id='forged-tenant'", "domain_id='forged-domain'", "kind='forged_kind'", "payload_version=2", "payload='{\"value\":\"forged\"}'::jsonb", "payload_hash='forged-hash'", "actor_id=actor_id+1", "authorization_version='forged-epoch'", "idempotency_key='forged-key'", "max_attempts=3", "parent_task_id='forged-parent'", "created_at=created_at+interval '1 second'"}
	for _, update := range updates {
		if _, err := f.db.Exec(f.ctx, "UPDATE adtr.tasks SET "+update+" WHERE task_id=$1", task.ID); err == nil {
			t.Fatalf("admission mutation allowed: %s", update)
		}
	}
	got, err := f.detail(e, taskTestPrincipal, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameAdmission(got.Task, task) {
		t.Fatal("admission changed despite rejected writes")
	}
	lease := f.start(e, "unchanged-admission-worker")
	forged := lease
	forged.Task.ActorID++
	if _, err := e.Heartbeat(f.ctx, forged); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("forged immutable snapshot accepted: %v", err)
	}
	if _, err := e.Finish(f.ctx, lease, Outcome{State: Succeeded}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskWorkerPreservesGenuineSuccessAfterCancellationRequest(t *testing.T) {
	f := newTaskFixture(t)
	entered, finished := make(chan struct{}), make(chan struct{})
	e := f.engine(syntheticTaskKind(2, func(ctx context.Context, ex Execution) Outcome {
		close(entered)
		<-ctx.Done()
		// The operation finished before stop could take effect. Report actual work,
		// never turn a genuine completed result into a false cancellation success.
		return Outcome{State: Succeeded, Result: json.RawMessage(`{"actuallyCompleted":true}`)}
	}))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "worker-cancel-success-race"))
	var workerErr error
	workerCtx, cancel := context.WithCancel(f.ctx)
	go func() { defer close(finished); _, workerErr = e.RunOne(workerCtx, "real-race-worker") }()
	t.Cleanup(func() { cancel(); awaitTaskSignal(t, finished, "race executor cleanup") })
	awaitTaskSignal(t, entered, "race executor start")
	if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
		t.Fatal(err)
	}
	awaitTaskSignal(t, finished, "real completion racing cancellation")
	if workerErr != nil {
		t.Fatal(workerErr)
	}
	detail, err := f.detail(e, taskTestPrincipal, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.State != Succeeded {
		t.Fatalf("genuine success overwritten: %+v", detail.Task)
	}
	races := 0
	for _, event := range detail.Events {
		if event.Action == "completed-with-cancel-race" {
			races++
		}
		if event.State == Cancelled {
			t.Fatal("worker falsely acknowledged cancellation")
		}
	}
	if races != 1 {
		t.Fatalf("cancellation race events=%d", races)
	}
	var result map[string]bool
	if json.Unmarshal(detail.Task.Result, &result) != nil || !result["actuallyCompleted"] {
		t.Fatalf("real result lost: %s", detail.Task.Result)
	}
}

func TestTaskSchemaCompatibilityFailsClosedAtEveryBoundary(t *testing.T) {
	f := newTaskFixture(t)
	var executed atomic.Int32
	e := f.engine(syntheticTaskKind(2, func(context.Context, Execution) Outcome { executed.Add(1); return Outcome{State: Succeeded} }))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "schema-blocked"))
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	for _, version := range []int{4, 6, 0} {
		if version == 0 {
			f.exec("DELETE FROM adtr.schema_version")
		} else {
			f.exec("UPDATE adtr.schema_version SET version=$1", version)
		}
		if _, err := e.Claim(f.ctx, "incompatible-worker"); !errors.Is(err, ErrSchemaIncompatible) {
			t.Fatalf("version%d claim: %v", version, err)
		}
		if n, err := e.RecoverExpired(f.ctx); n != 0 || !errors.Is(err, ErrSchemaIncompatible) {
			t.Fatalf("version%d recovery: n=%d err=%v", version, n, err)
		}
		if err := e.probe(f.ctx); !errors.Is(err, ErrSchemaIncompatible) {
			t.Fatalf("version%d probe: %v", version, err)
		}
		if worked, err := e.RunOne(f.ctx, "incompatible-runner"); worked || !errors.Is(err, ErrSchemaIncompatible) {
			t.Fatalf("version%d runner: worked=%v err=%v", version, worked, err)
		}
		if _, err := f.submit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "rejected-admission")); !errors.Is(err, ErrSchemaIncompatible) {
			t.Fatalf("version%d submit: %v", version, err)
		}
		state := f.requireState(task.ID, Queued, 0)
		if state.LeaseOwner != "" || state.FencingToken != 0 || executed.Load() != 0 {
			t.Fatalf("incompatible schema executed or leased work: %+v calls=%d", state, executed.Load())
		}
		tasks, events, outbox := f.counts()
		if tasks != beforeTasks || events != beforeEvents || outbox != beforeOutbox {
			t.Fatal("incompatible schema mutated task history")
		}
	}
	f.exec("INSERT INTO adtr.schema_version VALUES(true,5)")
	if worked, err := e.RunOne(f.ctx, "compatible-runner"); !worked || err != nil {
		t.Fatalf("compatible schema did not resume: worked=%v err=%v", worked, err)
	}
	f.requireState(task.ID, Succeeded, 1)
	if executed.Load() != 1 {
		t.Fatalf("execution calls=%d", executed.Load())
	}
}

func TestTaskSchemaChangeAfterClaimCannotStartExecution(t *testing.T) {
	f := newTaskFixture(t)
	var executed atomic.Int32
	e := f.engine(syntheticTaskKind(2, func(context.Context, Execution) Outcome { executed.Add(1); return Outcome{State: Succeeded} }))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "schema-after-claim"))
	lease, err := e.Claim(f.ctx, "old-schema-worker")
	if err != nil {
		t.Fatal(err)
	}
	beforeTasks, beforeEvents, beforeOutbox := f.counts()
	f.exec("UPDATE adtr.schema_version SET version=6")
	if _, err = e.Start(f.ctx, lease); !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("start on changed schema: %v", err)
	}
	if _, err = e.Heartbeat(f.ctx, lease); !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("heartbeat on changed schema: %v", err)
	}
	if _, err = e.Progress(f.ctx, lease, 25, json.RawMessage(`{}`), json.RawMessage(`{}`), lease.Task.ResultVersion); !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("checkpoint on changed schema: %v", err)
	}
	if _, err = e.Finish(f.ctx, lease, Outcome{State: Succeeded}); !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("finish on changed schema: %v", err)
	}
	state := f.requireState(task.ID, Queued, 0)
	if state.LeaseOwner != lease.Owner || state.FencingToken != lease.Token || executed.Load() != 0 {
		t.Fatalf("post-claim mismatch mutated work: %+v calls=%d", state, executed.Load())
	}
	tasks, events, outbox := f.counts()
	if tasks != beforeTasks || events != beforeEvents || outbox != beforeOutbox {
		t.Fatal("post-claim mismatch mutated history")
	}
	f.exec("UPDATE adtr.schema_version SET version=5")
	f.exec("UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1", task.ID)
	if worked, err := e.RunOne(f.ctx, "restored-compatible-worker"); !worked || err != nil {
		t.Fatalf("compatible retry did not resume: worked=%v err=%v", worked, err)
	}
	f.requireState(task.ID, Succeeded, 1)
	if executed.Load() != 1 {
		t.Fatalf("execution calls=%d", executed.Load())
	}
}

func TestTaskAPITxRequiresEarlierSchemaGate(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	tx, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	// Simulate an adapter that already took its tenant lock but forgot the earlier
	// schema gate. Tx methods reject; they never acquire it in reverse order.
	if _, err = tx.Exec(f.ctx, "SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))", taskTestPrincipal.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.SubmitTx(f.ctx, tx, taskTestPrincipal, syntheticTaskInput("domain-a", "missing-gate")); !errors.Is(err, ErrSchemaGateRequired) {
		t.Fatalf("missing pre-auth gate accepted: %v", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if tasks, events, outbox := f.counts(); tasks != 0 || events != 0 || outbox != 0 {
		t.Fatal("ungated admission mutated state")
	}
}

func TestTaskSchemaGateSharesMigratorLock(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "migration-lock"))
	migration, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer migration.Rollback(context.Background())
	if _, err = migration.Exec(f.ctx, "SELECT pg_advisory_xact_lock($1::bigint)", migrationLockID); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(f.ctx, 200*time.Millisecond)
	_, err = e.Claim(blocked, "blocked-by-migration")
	cancel()
	if err == nil {
		t.Fatal("worker crossed exclusive migration boundary")
	}
	if err = migration.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.requireState(task.ID, Queued, 0)
	if worked, err := e.RunOne(f.ctx, "after-migration"); !worked || err != nil {
		t.Fatalf("did not resume after migrator released lock: %v", err)
	}
	f.requireState(task.ID, Succeeded, 1)
}
