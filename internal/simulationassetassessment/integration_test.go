//go:build integration

package simulationassetassessment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"sync"
	"testing"
	"time"
)

type fixtureDB struct {
	ctx context.Context
	db  *pgx.Conn
	s   Scope
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, e := pgx.ConnectConfig(ctx, cfg.Copy())
	if e != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	var random [12]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	name := "adtr_asset_reports_" + hex.EncodeToString(random[:])
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
	db, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(context.Background()) })
	ddl := `CREATE SCHEMA adtr;CREATE FUNCTION adtr.reject_simulation_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'immutable';END$$;` + SchemaSQL + ImmutableSchemaSQL + `CREATE TABLE adtr.asset_audit(operation_id text PRIMARY KEY);`
	if _, e = db.Exec(ctx, ddl); e != nil {
		t.Fatal(e)
	}
	return &fixtureDB{ctx: ctx, db: db, s: testScope()}
}
func (f *fixtureDB) tx(fn func(pgx.Tx) error) error {
	tx, e := f.db.Begin(f.ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(f.ctx)
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(f.ctx)
}
func (f *fixtureDB) audit(ctx context.Context, tx pgx.Tx, r Receipt) error {
	_, e := tx.Exec(ctx, `INSERT INTO adtr.asset_audit VALUES($1)`, r.OperationID)
	return e
}
func (f *fixtureDB) seed(t *testing.T) Observation {
	t.Helper()
	var o Observation
	e := f.tx(func(tx pgx.Tx) error { var e error; o, e = SeedFixtureTx(f.ctx, tx, f.s.Tenant, f.s.Domain); return e })
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func request(o Observation, id string) CreateInput {
	return CreateInput{ID: id, OperationID: "op-" + id, ObservationID: o.ID, ExpectedSourceHash: o.Hash, ScenarioID: "baseline"}
}
func TestPostgresAssetAssessmentPersistenceReplayAndPaging(t *testing.T) {
	f := newDB(t)
	o := f.seed(t)
	in := request(o, "report")
	var first Receipt
	if e := f.tx(func(tx pgx.Tx) error { var e error; first, e = CreateTx(f.ctx, tx, f.s, in, f.audit); return e }); e != nil {
		t.Fatal(e)
	}
	if e := f.tx(func(tx pgx.Tx) error {
		again, e := CreateTx(f.ctx, tx, f.s, in, f.audit)
		if e != nil {
			return e
		}
		if digest(again) != digest(first) {
			t.Fatal("replay drift")
		}
		r, e := ReportTx(f.ctx, tx, f.s, in.ID)
		if e != nil {
			return e
		}
		if r.Summary.Total != 11 || r.Summary.AccountStatus["enabled"] != 3 {
			t.Fatal("persisted truth mismatch")
		}
		p, e := RowsTx(f.ctx, tx, f.s, in.ID, Filter{Kind: "user", Offset: 1, Limit: 2})
		if e != nil {
			return e
		}
		if p.Matched != 4 || len(p.Rows) != 2 || p.Summary.Total != 11 || p.Rows[0].ObjectID != "user-disabled-never" {
			t.Fatal("page/full aggregate mismatch")
		}
		h, e := HistoryTx(f.ctx, tx, f.s, 0, 20)
		if e != nil {
			return e
		}
		if h.Total != 1 || len(h.Reports) != 1 {
			t.Fatal("history")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.asset_audit`).Scan(&count); e != nil || count != 1 {
		t.Fatal("audit replay", count, e)
	}
	changed := in
	changed.ScenarioID = "stale"
	if e := f.tx(func(tx pgx.Tx) error { _, e := CreateTx(f.ctx, tx, f.s, changed, f.audit); return e }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestPostgresAssetAssessmentOwnerEpochAndSource(t *testing.T) {
	f := newDB(t)
	o := f.seed(t)
	in := request(o, "report")
	if e := f.tx(func(tx pgx.Tx) error { _, e := CreateTx(f.ctx, tx, f.s, in, f.audit); return e }); e != nil {
		t.Fatal(e)
	}
	for _, s := range []Scope{{Tenant: "tenant", Domain: "domain", ActorID: 8, Epoch: 1, Read: true}, {Tenant: "other", Domain: "domain", ActorID: 7, Epoch: 1, Read: true}, {Tenant: "tenant", Domain: "other", ActorID: 7, Epoch: 1, Read: true}} {
		if e := f.tx(func(tx pgx.Tx) error { _, e := ReportTx(f.ctx, tx, s, in.ID); return e }); !errors.Is(e, ErrNotFound) {
			t.Fatal("owner isolation", e)
		}
	}
	s := f.s
	s.Epoch++
	if e := f.tx(func(tx pgx.Tx) error { _, e := ReportTx(f.ctx, tx, s, in.ID); return e }); !errors.Is(e, ErrNotFound) {
		t.Fatal("epoch read", e)
	}
	if e := f.tx(func(tx pgx.Tx) error { _, e := ReceiptTx(f.ctx, tx, s, in.OperationID); return e }); !errors.Is(e, ErrNotFound) {
		t.Fatal("epoch receipt", e)
	}
	if e := f.tx(func(tx pgx.Tx) error {
		h, e := HistoryTx(f.ctx, tx, s, 0, 20)
		if e == nil && h.Total != 0 {
			t.Fatal("history old epoch")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	// Regrant hides old reads, but does not free either operation or report IDs.
	for _, id := range []string{in.ID, "new-report-old-operation"} {
		retry := in
		retry.ID = id
		if e := f.tx(func(tx pgx.Tx) error { _, e := CreateTx(f.ctx, tx, s, retry, f.audit); return e }); !errors.Is(e, ErrConflict) {
			t.Fatal("historical operation must conflict without insertion", e)
		}
	}
	newOp := in
	newOp.OperationID = "new-operation-old-report"
	if e := f.tx(func(tx pgx.Tx) error { _, e := CreateTx(f.ctx, tx, s, newOp, f.audit); return e }); !errors.Is(e, ErrConflict) {
		t.Fatal("historical report remains reserved", e)
	}
	var reports, receipts, audits int
	if e := f.db.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.simulation_asset_reports),(SELECT count(*) FROM adtr.simulation_asset_receipts),(SELECT count(*) FROM adtr.asset_audit)`).Scan(&reports, &receipts, &audits); e != nil || reports != 1 || receipts != 1 || audits != 1 {
		t.Fatal("regrant retries mutated history", reports, receipts, audits, e)
	}
	denied := s
	denied.Read = false
	if e := f.tx(func(tx pgx.Tx) error { _, e := ReportTx(f.ctx, tx, denied, in.ID); return e }); !errors.Is(e, ErrDenied) {
		t.Fatal("missing current grant remains denied", e)
	}
	in = request(o, "other")
	in.ExpectedSourceHash = digest("wrong")
	if e := f.tx(func(tx pgx.Tx) error { _, e := CreateTx(f.ctx, tx, f.s, in, f.audit); return e }); !errors.Is(e, ErrConflict) {
		t.Fatal("source CAS", e)
	}
}
func TestPostgresAssetAssessmentAtomicRollbackAndImmutable(t *testing.T) {
	f := newDB(t)
	o := f.seed(t)
	in := request(o, "rollback")
	sentinel := errors.New("audit failed")
	if e := f.tx(func(tx pgx.Tx) error {
		_, e := CreateTx(f.ctx, tx, f.s, in, func(context.Context, pgx.Tx, Receipt) error { return sentinel })
		if !errors.Is(e, sentinel) {
			t.Fatal(e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := f.db.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.simulation_asset_reports)+(SELECT count(*) FROM adtr.simulation_asset_receipts)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial records after outer commit", count, e)
	}
	if e := f.tx(func(tx pgx.Tx) error { _, e := CreateTx(f.ctx, tx, f.s, in, f.audit); return e }); e != nil {
		t.Fatal(e)
	}
	for _, sql := range []string{`UPDATE adtr.simulation_asset_reports SET epoch=2`, `DELETE FROM adtr.simulation_asset_reports`, `TRUNCATE adtr.simulation_asset_receipts`, `UPDATE adtr.simulation_asset_observations SET hash=repeat('0',64)`} {
		if _, e := f.db.Exec(f.ctx, sql); e == nil {
			t.Fatal("immutable mutation allowed", sql)
		}
	}
}
func TestPostgresAssetAssessmentTamperAndQuota(t *testing.T) {
	f := newDB(t)
	o := f.seed(t)
	if e := f.tx(func(tx pgx.Tx) error {
		for i := 0; i < MaxReports; i++ {
			id := time.Unix(int64(i), 0).UTC().Format("150405")
			if _, e := CreateTx(f.ctx, tx, f.s, request(o, id), f.audit); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := f.tx(func(tx pgx.Tx) error { _, e := CreateTx(f.ctx, tx, f.s, request(o, "overflow"), f.audit); return e }); !errors.Is(e, ErrLimit) {
		t.Fatal("quota", e)
	}
	// Controlled corruption in disposable database only, never application API.
	if _, e := f.db.Exec(f.ctx, `ALTER TABLE adtr.simulation_asset_reports DISABLE TRIGGER immutable_asset_rows; UPDATE adtr.simulation_asset_reports SET body=jsonb_set(body,'{summary,total}','999'); ALTER TABLE adtr.simulation_asset_reports ENABLE TRIGGER immutable_asset_rows`); e != nil {
		t.Fatal(e)
	}
	if e := f.tx(func(tx pgx.Tx) error { _, e := ReportTx(f.ctx, tx, f.s, "000000"); return e }); !errors.Is(e, ErrInvalid) {
		t.Fatal("tamper", e)
	}
}

func TestPostgresAssetAssessmentConcurrentIdempotence(t *testing.T) {
	f := newDB(t)
	o := f.seed(t)
	in := request(o, "concurrent")
	const workers = 6
	results := make(chan Receipt, workers)
	failures := make(chan error, workers)
	start := make(chan struct{})
	var ready, syncWorkers sync.WaitGroup
	ready.Add(workers)
	syncWorkers.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer syncWorkers.Done()
			db, e := pgx.ConnectConfig(f.ctx, f.db.Config().Copy())
			ready.Done()
			if e != nil {
				failures <- e
				return
			}
			defer db.Close(context.Background())
			<-start
			tx, e := db.Begin(f.ctx)
			if e != nil {
				failures <- e
				return
			}
			defer tx.Rollback(f.ctx)
			r, e := CreateTx(f.ctx, tx, f.s, in, f.audit)
			if e != nil {
				failures <- e
				return
			}
			if e = tx.Commit(f.ctx); e != nil {
				failures <- e
				return
			}
			results <- r
		}()
	}
	ready.Wait()
	close(start)
	syncWorkers.Wait()
	close(results)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	hash := ""
	n := 0
	for r := range results {
		n++
		if hash == "" {
			hash = digest(r)
		} else if digest(r) != hash {
			t.Fatal("concurrent replay differs")
		}
	}
	if n != workers {
		t.Fatal("missing results")
	}
	var records, audits int
	if e := f.db.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.simulation_asset_reports),(SELECT count(*) FROM adtr.asset_audit)`).Scan(&records, &audits); e != nil || records != 1 || audits != 1 {
		t.Fatal("duplicate concurrent write", records, audits, e)
	}
}
