//go:build integration

package simulationdetectionexceptions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/tasks"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureDB struct {
	t      *testing.T
	ctx    context.Context
	cfg    *pgx.ConnConfig
	db     *pgx.Conn
	s      Scope
	engine *tasks.Engine
	epoch  *atomic.Int64
}

func newDB(t *testing.T) *fixtureDB {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: missing PostgreSQL is failure, not skip")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal("invalid test database config")
	}
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, e := pgx.ConnectConfig(ctx, cfg.Copy())
	if e != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	var random [12]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	name := "adtr_exceptions_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+quoted); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(c, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error("test-owned cleanup failed", e)
		}
		admin.Close(c)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	db, e := pgx.ConnectConfig(ctx, cfg.Copy())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(context.Background()) })
	ddl := fmt.Sprintf(`CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true,version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,%d);`, schemaversion.Current) + tasks.Schema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL + SchemaSQL + ImmutableSchemaSQL + `CREATE TABLE adtr.exception_test_audit(operation_id text PRIMARY KEY,actor_id bigint);`
	if _, e = db.Exec(ctx, ddl); e != nil {
		t.Fatal(e)
	}
	epoch := &atomic.Int64{}
	epoch.Store(1)
	auth := func(_ context.Context, _ pgx.Tx, p tasks.Principal, s tasks.Scope, _ tasks.Action) (string, error) {
		if p.TenantID != "tenant" || p.ActorID != 1 || s.DomainID != "domain" || s.TaskName != simulationdetection.KindName || s.Platform {
			return "", tasks.ErrAuthorization
		}
		return fmt.Sprint("synthetic-test-", epoch.Load()), nil
	}
	registry, e := tasks.NewRegistry(simulationdetection.NewKind(auth))
	if e != nil {
		t.Fatal(e)
	}
	engine, e := tasks.New(cfg, registry, auth, schemaversion.Current)
	if e != nil {
		t.Fatal(e)
	}
	return &fixtureDB{t: t, ctx: ctx, cfg: cfg, db: db, s: Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true, DetectionRead: true, EventsRead: true}, engine: engine, epoch: epoch}
}
func (f *fixtureDB) tx(fn func(pgx.Tx) error) error {
	conn, e := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if e != nil {
		return e
	}
	defer conn.Close(context.Background())
	tx, e := conn.Begin(f.ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = tasks.CheckSchemaTx(f.ctx, tx, schemaversion.Current); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(f.ctx)
}
func (f *fixtureDB) audit(ctx context.Context, tx pgx.Tx, r Receipt) error {
	_, e := tx.Exec(ctx, `INSERT INTO adtr.exception_test_audit VALUES($1,$2)`, r.OperationID, r.ActorID)
	return e
}
func (f *fixtureDB) mutate(in Mutation, audit AuditFunc) (Receipt, error) {
	var r Receipt
	e := f.tx(func(tx pgx.Tx) error { var e error; r, e = MutateTx(f.ctx, tx, f.s, f.engine, in, audit); return e })
	return r, e
}
func (f *fixtureDB) record() Record {
	f.t.Helper()
	var r Record
	if e := f.tx(func(tx pgx.Tx) error { var e error; r, e = SnapshotTx(f.ctx, tx, f.s); return e }); e != nil {
		f.t.Fatal(e)
	}
	return r
}
func globalDecl(id string) Declaration {
	e := sampleEntry(id)
	return Declaration{ID: id, Global: true, Reason: e.Reason, NotBefore: e.NotBefore, ExpiresAt: e.ExpiresAt, Conditions: e.Conditions}
}
func (f *fixtureDB) evaluate(id, task string, rev uint64) (Receipt, error) {
	var r Receipt
	e := f.tx(func(tx pgx.Tx) error {
		var e error
		r, e = EvaluateTx(f.ctx, tx, f.s, f.engine, EvaluationInput{ID: id, OperationID: id, SourceTaskID: task, ExpectedRevision: rev}, f.audit)
		return e
	})
	return r, e
}
func (f *fixtureDB) run() string {
	f.t.Helper()
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "exception-worker-fixture", Synthetic: true}
	raw := []securityevents.RawEvent{}
	for _, v := range []struct{ id, kind string }{{"positive", "login_failure"}, {"negative", "login_success"}} {
		raw = append(raw, securityevents.RawEvent{ID: v.id, Tenant: "tenant", Domain: "domain", Kind: v.kind, Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: p})
	}
	ds := simulationdetection.Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}
	audit := func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil }
	var sub tasks.Submission
	if e := f.tx(func(tx pgx.Tx) error {
		if _, e := simulationevents.SeedFixtureTx(f.ctx, tx, securityevents.Scope{Tenant: "tenant", Domain: "domain", Read: true, Manage: true}, simulationevents.Fixture{ID: "dataset", Provenance: p, Events: raw}); e != nil {
			return e
		}
		rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Enabled: true, Scope: coreScope(f.s), Combine: "all", Conditions: sampleEntry("x").Conditions}
		if _, e := simulationdetection.MutateTx(f.ctx, tx, ds, simulationdetection.Mutation{ID: "template", Name: "SIMULATED", OperationID: "template", Rules: []detectionrules.Rule{rule}}, audit); e != nil {
			return e
		}
		var e error
		sub, e = simulationdetection.SubmitRunTx(f.ctx, tx, ds, f.engine, simulationdetection.RunInput{TemplateID: "template", TemplateVersion: 1, DatasetID: "dataset", OperationID: "run"}, audit)
		return e
	}); e != nil {
		f.t.Fatal(e)
	}
	worker, e := tasks.NewWorker(f.engine, tasks.WorkerConfig{Owner: "exception-worker", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if e != nil {
		f.t.Fatal(e)
	}
	ctx, stop := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { stop(); <-done }()
	for {
		var d tasks.Detail
		if e = f.tx(func(tx pgx.Tx) error {
			var e error
			d, e = simulationdetection.ReadRunTx(f.ctx, tx, ds, f.engine, sub.Task.ID)
			return e
		}); e != nil {
			f.t.Fatal(e)
		}
		if d.Task.State == tasks.Succeeded {
			var r simulationdetection.Result
			if json.Unmarshal(d.Task.Result, &r) != nil || len(r.Matches) != 1 || r.Matches[0].EventID != "positive" || r.EventCount != 2 {
				f.t.Fatal("nonempty exact Worker proof", r)
			}
			return sub.Task.ID
		}
		if d.Task.State.Terminal() {
			f.t.Fatal("worker failed", d.Task.State, d.Task.Error)
		}
		select {
		case <-f.ctx.Done():
			f.t.Fatal("worker timeout")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func TestPostgresExceptionsWorkerAnnotationsHistoryAndRevocation(t *testing.T) {
	f := newDB(t)
	task := f.run()
	first, e := f.evaluate("before", task, 1)
	if e != nil || len(first.Evaluation.Matches) != 1 || len(first.Evaluation.Matches[0].Exceptions) != 0 {
		t.Fatal(first, e)
	}
	d := globalDecl("global")
	r, e := f.mutate(Mutation{Action: "create", OperationID: "create", ExpectedRevision: 1, Declaration: &d}, f.audit)
	if e != nil || r.Revision != 2 {
		t.Fatal(r, e)
	}
	if _, e = f.mutate(Mutation{Action: "enable", OperationID: "enable", ID: "global", ExpectedRevision: 2}, f.audit); e != nil {
		t.Fatal(e)
	}
	after, e := f.evaluate("after", task, 3)
	if e != nil || len(after.Evaluation.Matches[0].Exceptions) != 1 || digest(after.Evaluation.Source.Matches) != digest(first.Evaluation.Source.Matches) {
		t.Fatal(after, e)
	}
	var preview Declaration
	if e = f.tx(func(tx pgx.Tx) error {
		var e error
		preview, e = PreviewTx(f.ctx, tx, f.s, f.engine, PreviewInput{SourceTaskID: task, EventID: "positive", RuleID: "rule", ID: "bound", Reason: "explicit shared configuration", NotBefore: d.NotBefore, ExpiresAt: d.ExpiresAt})
		return e
	}); e != nil || preview.RuleID != "rule" || len(preview.Conditions) != 5 {
		t.Fatal(preview, e)
	}
	if _, e = f.mutate(Mutation{Action: "create", OperationID: "bound", ExpectedRevision: 3, Declaration: &preview}, f.audit); e != nil {
		t.Fatal(e)
	}
	if _, e = f.mutate(Mutation{Action: "disable", OperationID: "disable", ID: "global", ExpectedRevision: 4}, f.audit); e != nil {
		t.Fatal(e)
	}
	later, e := f.evaluate("later", task, 5)
	if e != nil || len(later.Evaluation.Matches[0].Exceptions) != 0 {
		t.Fatal(later, e)
	}
	var historical Evaluation
	read := func(s Scope) error {
		return f.tx(func(tx pgx.Tx) error {
			var e error
			historical, e = EvaluationTx(f.ctx, tx, s, f.engine, "after")
			return e
		})
	}
	if e = read(f.s); e != nil || digest(historical) != digest(*after.Evaluation) {
		t.Fatal(historical, e)
	}
	denied := f.s
	denied.EventsRead = false
	if e = read(denied); !errors.Is(e, ErrDenied) {
		t.Fatal("events revoke", e)
	}
	denied = f.s
	denied.ActorID = 2
	if e = read(denied); !errors.Is(e, ErrNotFound) {
		t.Fatal("owner", e)
	}
	f.epoch.Add(1)
	if e = read(f.s); e == nil {
		t.Fatal("old epoch result exposed")
	}
	if e = f.tx(func(tx pgx.Tx) error { _, e := ReceiptTx(f.ctx, tx, f.s, f.engine, "after"); return e }); e == nil {
		t.Fatal("receipt epoch bypass")
	}
	// Explicit config is shared; original source result is not embedded or required for it.
	if f.record().Snapshot.Revision != 5 {
		t.Fatal("configuration corrupted")
	}
}
func TestPostgresExceptionsAtomicImportCASQuotaAndAudit(t *testing.T) {
	f := newDB(t)
	d := globalDecl("x")
	in := Mutation{Action: "create", OperationID: "create", ExpectedRevision: 1, Declaration: &d}
	if _, e := f.mutate(in, func(context.Context, pgx.Tx, Receipt) error { return errors.New("audit unavailable") }); e == nil {
		t.Fatal("audit accepted")
	}
	if f.record().Snapshot.Revision != 1 {
		t.Fatal("partial write")
	}
	r, e := f.mutate(in, f.audit)
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.mutate(in, f.audit)
	if e != nil || digest(r) != digest(again) {
		t.Fatal("replay", e)
	}
	changed := in
	changed.Declaration = &Declaration{ID: "other"}
	if _, e = f.mutate(changed, f.audit); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	entries := []Declaration{globalDecl("good"), globalDecl("x")}
	if _, e = f.mutate(Mutation{Action: "import", OperationID: "bad-import", ExpectedRevision: 2, Entries: entries}, f.audit); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if len(f.record().Snapshot.Entries) != 1 {
		t.Fatal("partial import")
	}
	entries = nil
	for i := 0; i < MaxEntries-2; i++ {
		entries = append(entries, globalDecl(fmt.Sprintf("fill-%d", i)))
	}
	if _, e = f.mutate(Mutation{Action: "import", OperationID: "fill", ExpectedRevision: 2, Entries: entries}, f.audit); e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			decl := globalDecl(fmt.Sprintf("last-%d", i))
			_, e := f.mutate(Mutation{Action: "create", OperationID: decl.ID, ExpectedRevision: 3, Declaration: &decl}, f.audit)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 || len(f.record().Snapshot.Entries) != MaxEntries {
		t.Fatal(ok, conflict)
	}
	over := globalDecl("over")
	if _, e = f.mutate(Mutation{Action: "create", OperationID: "over", ExpectedRevision: 4, Declaration: &over}, f.audit); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	if e = f.tx(func(tx pgx.Tx) error {
		_, e := tx.Exec(f.ctx, `UPDATE adtr.simulation_exception_revisions SET hash=repeat('a',64)`)
		return e
	}); e == nil {
		t.Fatal("immutable revision changed")
	}
	if e = f.tx(func(tx pgx.Tx) error {
		_, e := tx.Exec(f.ctx, `INSERT INTO adtr.simulation_exception_evaluations VALUES('other','domain','bad',1,1,repeat('a',64),'{}')`)
		return e
	}); e == nil {
		t.Fatal("cross scope FK accepted")
	}
}
func TestPostgresExceptionsRevisionExhaustionSafetyControls(t *testing.T) {
	f := newDB(t)
	d := globalDecl("x")
	if _, e := f.mutate(Mutation{Action: "create", OperationID: "create", ExpectedRevision: 1, Declaration: &d}, f.audit); e != nil {
		t.Fatal(e)
	}
	for rev := uint64(2); rev < 254; rev++ {
		action := "enable"
		if rev%2 == 1 {
			action = "disable"
		}
		if _, e := f.mutate(Mutation{Action: action, ID: "x", OperationID: fmt.Sprint("revision-", rev), ExpectedRevision: rev}, f.audit); e != nil {
			t.Fatal(rev, e)
		}
	}
	// Revision254 is disabled; enable would consume the two final safety slots.
	if _, e := f.mutate(Mutation{Action: "enable", ID: "x", OperationID: "over", ExpectedRevision: 254}, f.audit); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	if _, e := f.mutate(Mutation{Action: "archive", ID: "x", OperationID: "safe", ExpectedRevision: 254}, f.audit); e != nil {
		t.Fatal(e)
	}
	if !f.record().Snapshot.Entries[0].Archived {
		t.Fatal("not archived")
	}
}
func TestPostgresExceptionsSourceForgeryAndOversizeAtomicity(t *testing.T) {
	f := newDB(t)
	task := f.run()
	// Administrative corruption is contained in a rolled-back test transaction.
	// The API accepts only task IDs; it cannot supply any of these forged fields.
	for _, expr := range []string{`jsonb_set(result,'{matches}','[]'::jsonb)`, `jsonb_set(result,'{templateHash}',to_jsonb(repeat('a',64)))`, `jsonb_set(result,'{eventCount}','999'::jsonb)`} {
		e := f.tx(func(tx pgx.Tx) error {
			if _, e := tx.Exec(f.ctx, `UPDATE adtr.tasks SET result=`+expr+` WHERE tenant_id='tenant' AND task_id=$1`, task); e != nil {
				return e
			}
			_, e := EvaluateTx(f.ctx, tx, f.s, f.engine, EvaluationInput{ID: "forged", OperationID: "forged", SourceTaskID: task, ExpectedRevision: 1}, f.audit)
			return e
		})
		if !errors.Is(e, ErrInvalid) {
			t.Fatal("forged result accepted or wrong error", expr, e)
		}
	}
	entries := []Declaration{}
	for i := 0; i < MaxEntries; i++ {
		d := globalDecl(fmt.Sprint("large-", i))
		d.Conditions = nil
		for _, field := range []string{"event.category", "event.outcome", "actor.name", "target.name", "source.ip"} {
			values := []string{}
			for j := 0; j < 32; j++ {
				values = append(values, strings.Repeat("x", 510)+fmt.Sprintf("%02d", j))
			}
			d.Conditions = append(d.Conditions, detectionrules.Condition{Field: field, Operator: "in", Values: values})
		}
		entries = append(entries, d)
	}
	if _, e := f.mutate(Mutation{Action: "import", OperationID: "oversize", ExpectedRevision: 1, Entries: entries}, f.audit); !errors.Is(e, ErrLimit) {
		t.Fatal("oversize not rejected", e)
	}
	for _, table := range []string{"simulation_exception_heads", "simulation_exception_revisions", "simulation_exception_evaluations", "simulation_exception_receipts", "exception_test_audit"} {
		var count int
		if e := f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr."+table).Scan(&count); e != nil || count != 0 {
			t.Fatal("partial write", table, count, e)
		}
	}
	good, e := f.evaluate("valid", task, 1)
	if e != nil {
		t.Fatal("valid source damaged", e)
	}
	again, e := f.evaluate("valid", task, 1)
	if e != nil || digest(good) != digest(again) {
		t.Fatal("evaluation replay", e)
	}
	wrong := f.s
	wrong.Domain = "other"
	if e = f.tx(func(tx pgx.Tx) error { _, _, e := source(f.ctx, tx, wrong, f.engine, task); return e }); !errors.Is(e, ErrNotFound) {
		t.Fatal("domain scope", e)
	}
}
