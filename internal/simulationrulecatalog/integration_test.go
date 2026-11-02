//go:build integration

package simulationrulecatalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/tasks"
	"os"
	"sync"
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
	name := "adtr_catalog_" + hex.EncodeToString(random[:])
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
	ddl := fmt.Sprintf(`CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true,version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,%d);`, schemaversion.Current) + tasks.Schema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL + SchemaSQL + ImmutableSchemaSQL + `CREATE TABLE adtr.catalog_test_audit(operation_id text PRIMARY KEY,actor_id bigint);`
	if _, e = db.Exec(ctx, ddl); e != nil {
		t.Fatal(e)
	}
	auth := func(_ context.Context, _ pgx.Tx, p tasks.Principal, s tasks.Scope, _ tasks.Action) (string, error) {
		if p.TenantID != "tenant" || p.ActorID != 1 || s.DomainID != "domain" || s.TaskName != simulationdetection.KindName || s.Platform {
			return "", tasks.ErrAuthorization
		}
		return "synthetic-test-1", nil
	}
	registry, e := tasks.NewRegistry(simulationdetection.NewKind(auth))
	if e != nil {
		t.Fatal(e)
	}
	engine, e := tasks.New(cfg, registry, auth, schemaversion.Current)
	if e != nil {
		t.Fatal(e)
	}
	return &fixtureDB{t: t, ctx: ctx, cfg: cfg, db: db, s: Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true, DetectionRead: true, DetectionManage: true}, engine: engine}
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
	_, e := tx.Exec(ctx, `INSERT INTO adtr.catalog_test_audit VALUES($1,$2)`, r.OperationID, r.ActorID)
	return e
}
func (f *fixtureDB) mutate(s Scope, in Mutation, audit AuditFunc) (Receipt, error) {
	var r Receipt
	e := f.tx(func(tx pgx.Tx) error { var e error; r, e = MutateTx(f.ctx, tx, s, in, audit); return e })
	return r, e
}
func (f *fixtureDB) detail() Record {
	f.t.Helper()
	var r Record
	e := f.tx(func(tx pgx.Tx) error { var e error; r, e = DetailTx(f.ctx, tx, f.s, "rule"); return e })
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}
func (f *fixtureDB) active() Record {
	f.t.Helper()
	for _, in := range []Mutation{{ID: "rule", Action: "create", OperationID: "create", Declaration: sampleDeclaration()}, {ID: "rule", Action: "test", OperationID: "test", ExpectedRevision: 2, Cases: fixtures()}} {
		if _, e := f.mutate(f.s, in, f.audit); e != nil {
			f.t.Fatal(e)
		}
	}
	reviewer := f.s
	reviewer.ActorID = 2
	reviewer.Manage = false
	reviewer.Publish = true
	if _, e := f.mutate(reviewer, Mutation{ID: "rule", Action: "publish", OperationID: "publish", ExpectedRevision: 3}, f.audit); e != nil {
		f.t.Fatal(e)
	}
	if _, e := f.mutate(f.s, Mutation{ID: "rule", Action: "enable", OperationID: "enable", ExpectedRevision: 4}, f.audit); e != nil {
		f.t.Fatal(e)
	}
	return f.detail()
}
func TestPostgresCatalogLifecycleCASAuditAndIsolation(t *testing.T) {
	f := newDB(t)
	r := f.active()
	if r.Revision != 5 || r.ActiveVersion != 1 || r.Snapshot.Entries[0].Published[0].Rule.Enabled {
		t.Fatal(r)
	}
	var exported Export
	if e := f.tx(func(tx pgx.Tx) error { var e error; exported, e = ExportTx(f.ctx, tx, f.s, "rule"); return e }); e != nil || len(exported.Publications) != 1 || exported.Publications[0].ReviewedBy != 2 || exported.Publications[0].TestedBy != 1 {
		t.Fatal(exported, e)
	}
	in := Mutation{ID: "rule", Action: "edit", OperationID: "edit", ExpectedRevision: 5, Declaration: sampleDeclaration()}
	if _, e := f.mutate(f.s, in, func(context.Context, pgx.Tx, Receipt) error { return errors.New("audit unavailable") }); e == nil {
		t.Fatal("audit accepted")
	}
	if f.detail().Revision != 5 {
		t.Fatal("partial write")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, op := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			m := in
			m.OperationID = op
			_, e := f.mutate(f.s, m, f.audit)
			errs <- e
		}(op)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	if e := f.tx(func(tx pgx.Tx) error {
		other := f.s
		other.Domain = "other"
		if _, e := DetailTx(f.ctx, tx, other, "rule"); !errors.Is(e, ErrNotFound) {
			t.Fatal(e)
		}
		denied := f.s
		denied.Read = false
		if _, e := DetailTx(f.ctx, tx, denied, "rule"); !errors.Is(e, ErrDenied) {
			t.Fatal(e)
		}
		p, e := ListTx(f.ctx, tx, f.s, "rule", 0, MaxPage)
		if e != nil || p.Count != 1 || p.Items[0].Revision != 6 {
			t.Fatal(p, e)
		}
		if _, e = ReceiptTx(f.ctx, tx, f.s, "edit"); !errors.Is(e, ErrNotFound) {
			t.Fatal("rolled back receipt", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, sql := range []string{`UPDATE adtr.simulation_rule_catalog_revisions SET hash=repeat('0',64)`, `DELETE FROM adtr.simulation_rule_catalog_publications`, `TRUNCATE adtr.simulation_rule_catalog_receipts`, `DELETE FROM adtr.simulation_rule_catalog_heads`} {
		if _, e := f.db.Exec(f.ctx, sql); e == nil {
			t.Fatal("immutable mutation accepted", sql)
		}
	}
}
func TestPostgresCatalogCopyReplayAndActualWorkerNonemptyMatches(t *testing.T) {
	f := newDB(t)
	r := f.active()
	in := CopyInput{ID: "rule", OperationID: "copy", TemplateID: "copied", Name: "Synthetic independent copy", Acknowledgement: CopyAcknowledgement, ExpectedRevision: r.Revision, PublishedVersion: 1, PublishedDigest: r.Snapshot.Entries[0].Published[0].Digest}
	var copied Receipt
	copyWith := func(i CopyInput, a AuditFunc) (Receipt, error) {
		var out Receipt
		e := f.tx(func(tx pgx.Tx) error { var e error; out, e = CopyTemplateTx(f.ctx, tx, f.s, i, a); return e })
		return out, e
	}
	if _, e := copyWith(in, func(context.Context, pgx.Tx, Receipt) error { return errors.New("audit failed") }); e == nil {
		t.Fatal("copy audit accepted")
	}
	var count int
	if e := f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.simulation_detection_templates`).Scan(&count); e != nil || count != 0 {
		t.Fatal("copy partially persisted", count, e)
	}
	var e error
	copied, e = copyWith(in, f.audit)
	if e != nil || copied.Template == nil || !copied.Template.Rules[0].Enabled || copied.Template.Rules[0].Version != 1 || copied.Origin.PublishedDigest != in.PublishedDigest || copied.Origin.TemplateHash != copied.Template.Hash {
		t.Fatal(copied, e)
	}
	// Copy receipts contain target declarations and therefore need CURRENT
	// detection read authority, even when catalog read remains granted.
	if e = f.tx(func(tx pgx.Tx) error {
		revoked := f.s
		revoked.DetectionRead = false
		if _, err := ReceiptTx(f.ctx, tx, revoked, "copy"); !errors.Is(err, ErrDenied) {
			t.Fatal("copy receipt leaked after revoke", err)
		}
		if _, err := ReceiptTx(f.ctx, tx, revoked, "create"); err != nil {
			t.Fatal("ordinary catalog receipt wrongly denied", err)
		}
		restored, err := ReceiptTx(f.ctx, tx, f.s, "copy")
		if err != nil {
			return err
		}
		if restored.Intent.Name != in.Name || restored.Intent.PublishedDigest != in.PublishedDigest || restored.Intent.Acknowledgement != CopyAcknowledgement || digest(restored.Intent) != restored.RequestHash {
			t.Fatal("unbound copy intent", restored.Intent)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	replay, e := copyWith(in, f.audit)
	if e != nil || digest(replay) != digest(copied) {
		t.Fatal("replay", e)
	}
	bad := in
	bad.Name = "changed"
	if _, e = copyWith(bad, f.audit); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	bad = in
	bad.OperationID = "second-copy"
	if _, e = copyWith(bad, f.audit); !errors.Is(e, ErrConflict) {
		t.Fatal("must be new template", e)
	}
	provenance := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "catalog-worker-fixture", Synthetic: true}
	events := []securityevents.RawEvent{}
	for _, c := range []struct{ id, kind string }{{"positive", "login_failure"}, {"negative", "login_success"}} {
		events = append(events, securityevents.RawEvent{ID: c.id, Tenant: "tenant", Domain: "domain", Kind: c.kind, Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: provenance})
	}
	ds := simulationdetection.Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}
	audit := func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil }
	var submission tasks.Submission
	if e = f.tx(func(tx pgx.Tx) error {
		if _, e := simulationevents.SeedFixtureTx(f.ctx, tx, securityevents.Scope{Tenant: "tenant", Domain: "domain", Read: true, Manage: true}, simulationevents.Fixture{ID: "dataset", Provenance: provenance, Events: events}); e != nil {
			return e
		}
		var e error
		submission, e = simulationdetection.SubmitRunTx(f.ctx, tx, ds, f.engine, simulationdetection.RunInput{TemplateID: "copied", TemplateVersion: 1, DatasetID: "dataset", OperationID: "run"}, audit)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.mutate(f.s, Mutation{ID: "rule", Action: "disable", OperationID: "disable", ExpectedRevision: 5}, f.audit); e != nil {
		t.Fatal(e)
	}
	bad = in
	bad.OperationID = "inactive-copy"
	bad.ExpectedRevision = 6
	bad.TemplateID = "inactive"
	if _, e = copyWith(bad, f.audit); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	worker, e := tasks.NewWorker(f.engine, tasks.WorkerConfig{Owner: "catalog-worker", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	workerCtx, stop := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()
	defer func() { stop(); <-done }()
	var final tasks.Task
	for {
		if e = f.tx(func(tx pgx.Tx) error {
			d, e := simulationdetection.ReadRunTx(f.ctx, tx, ds, f.engine, submission.Task.ID)
			final = d.Task
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if final.State == tasks.Succeeded {
			break
		}
		if final.State == tasks.Failed || final.State == tasks.Cancelled {
			t.Fatal("worker failed", final.State, final.Error)
		}
		select {
		case <-f.ctx.Done():
			t.Fatal("worker did not complete")
		case <-time.After(20 * time.Millisecond):
		}
	}
	var result simulationdetection.Result
	if e = json.Unmarshal(final.Result, &result); e != nil || len(result.Matches) != 1 || result.Matches[0].EventID != "positive" || result.Matches[0].RuleID != "rule" || result.TemplateHash != copied.Template.Hash || result.EventCount != 2 {
		t.Fatal("exact positive/negative nonempty proof", result, e)
	}
	if e = f.tx(func(tx pgx.Tx) error {
		again, e := simulationdetection.SubmitRunTx(f.ctx, tx, ds, f.engine, simulationdetection.RunInput{TemplateID: "copied", TemplateVersion: 1, DatasetID: "dataset", OperationID: "run"}, audit)
		if e != nil {
			return e
		}
		if again.Task.ID != submission.Task.ID || !again.Replayed || again.Task.State != tasks.Succeeded {
			t.Fatal("run replay", again)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if f.detail().Snapshot.Entries[0].Published[0].Rule.Enabled {
		t.Fatal("copy mutated publication")
	}
}
func TestPostgresCatalogUnsupportedMappedFieldCannotCreateTemplate(t *testing.T) {
	f := newDB(t)
	decl := sampleDeclaration()
	decl.Conditions[0].Field = "event.code"
	cases := fixtures()
	for i := range cases {
		cases[i].Fields = map[string]string{"event.code": cases[i].Fields["event.outcome"]}
	}
	for _, in := range []Mutation{{ID: "rule", Action: "create", OperationID: "create", Declaration: decl}, {ID: "rule", Action: "test", OperationID: "test", ExpectedRevision: 2, Cases: cases}} {
		if _, e := f.mutate(f.s, in, f.audit); e != nil {
			t.Fatal(e)
		}
	}
	reviewer := f.s
	reviewer.ActorID = 2
	reviewer.Manage = false
	reviewer.Publish = true
	if _, e := f.mutate(reviewer, Mutation{ID: "rule", Action: "publish", OperationID: "publish", ExpectedRevision: 3}, f.audit); e != nil {
		t.Fatal(e)
	}
	if _, e := f.mutate(f.s, Mutation{ID: "rule", Action: "enable", OperationID: "enable", ExpectedRevision: 4}, f.audit); e != nil {
		t.Fatal(e)
	}
	r := f.detail()
	if r.CopySupported || len(r.CopyUnsupportedFields) != 1 {
		t.Fatal(r.Summary)
	}
	e := f.tx(func(tx pgx.Tx) error {
		_, e := CopyTemplateTx(f.ctx, tx, f.s, CopyInput{ID: "rule", OperationID: "copy", TemplateID: "inert", Name: "Must not exist", Acknowledgement: CopyAcknowledgement, ExpectedRevision: 5, PublishedVersion: 1, PublishedDigest: r.Snapshot.Entries[0].Published[0].Digest}, f.audit)
		return e
	})
	if !errors.Is(e, ErrUnsupportedCopy) {
		t.Fatal(e)
	}
	var count int
	if e = f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.simulation_detection_templates`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}
func TestPostgresCatalogExhaustionRetainsSafetyControls(t *testing.T) {
	f := newDB(t)
	r := f.active()
	for r.Revision < MaxRevisions-2 {
		if _, e := f.mutate(f.s, Mutation{ID: "rule", Action: "enable", OperationID: fmt.Sprintf("fill-%d", r.Revision), ExpectedRevision: r.Revision}, f.audit); e != nil {
			t.Fatal(e)
		}
		r = f.detail()
	}
	if _, e := f.mutate(f.s, Mutation{ID: "rule", Action: "enable", OperationID: "exhausted-enable", ExpectedRevision: 62}, f.audit); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	if _, e := f.mutate(f.s, Mutation{ID: "rule", Action: "disable", OperationID: "safe-disable", ExpectedRevision: 62}, f.audit); e != nil {
		t.Fatal(e)
	}
	if _, e := f.mutate(f.s, Mutation{ID: "rule", Action: "disable", OperationID: "repeat-disable", ExpectedRevision: 63}, f.audit); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	if _, e := f.mutate(f.s, Mutation{ID: "rule", Action: "archive", OperationID: "safe-archive", ExpectedRevision: 63}, f.audit); e != nil {
		t.Fatal(e)
	}
	r = f.detail()
	if !r.Archived || r.ActiveVersion != 0 || r.Revision != 64 {
		t.Fatal(r.Summary)
	}
}
