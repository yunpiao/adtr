//go:build integration

package simulationdnsip

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/tasks"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureDB struct {
	t          *testing.T
	ctx        context.Context
	cfg        *pgx.ConnConfig
	db         *pgx.Conn
	s          Scope
	engine     *tasks.Engine
	epoch      atomic.Int64
	revokeGate atomic.Bool
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
	name := "adtr_dnsip_" + hex.EncodeToString(random[:])
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
	ddl := fmt.Sprintf(`CREATE SCHEMA adtr;CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true,version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,%d);`, schemaversion.Current) + tasks.Schema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL + SchemaSQL + ImmutableSchemaSQL + `CREATE TABLE adtr.dns_ip_test_audit(operation_id text PRIMARY KEY);`
	if _, e = db.Exec(ctx, ddl); e != nil {
		t.Fatal(e)
	}
	f := &fixtureDB{t: t, ctx: ctx, cfg: cfg, db: db, s: Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}}
	f.epoch.Store(1)
	auth := func(_ context.Context, _ pgx.Tx, p tasks.Principal, s tasks.Scope, _ tasks.Action) (string, error) {
		if p.TenantID != "tenant" || p.ActorID < 1 || s.DomainID != "domain" || s.TaskName != KindName || s.Platform {
			return "", tasks.ErrAuthorization
		}
		return fmt.Sprint("dns-ip-", f.epoch.Load()), nil
	}
	kind := NewKind(auth)
	gate := kind.ValidateSuccess
	kind.ValidateSuccess = func(ctx context.Context, tx pgx.Tx, t tasks.Task, b json.RawMessage) (string, error) {
		code, e := gate(ctx, tx, t, b)
		if code == "" && e == nil && f.revokeGate.Swap(false) {
			f.epoch.Add(1)
		}
		return code, e
	}
	registry, e := tasks.NewRegistry(kind)
	if e != nil {
		t.Fatal(e)
	}
	f.engine, e = tasks.New(cfg, registry, auth, schemaversion.Current)
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixtureDB) tx(fn func(pgx.Tx) error) error {
	c, e := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if e != nil {
		return e
	}
	defer c.Close(context.Background())
	tx, e := c.Begin(f.ctx)
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
	_, e := tx.Exec(ctx, `INSERT INTO adtr.dns_ip_test_audit VALUES($1)`, r.OperationID)
	return e
}
func (f *fixtureDB) seed() Observation {
	f.t.Helper()
	var o Observation
	if e := f.tx(func(tx pgx.Tx) error { var e error; o, e = SeedFixtureTx(f.ctx, tx, f.s, sample(3)); return e }); e != nil {
		f.t.Fatal(e)
	}
	return o
}
func (f *fixtureDB) submit(o Observation, op, keyword string) (Receipt, error) {
	var r Receipt
	e := f.tx(func(tx pgx.Tx) error {
		var e error
		r, e = SubmitExportTx(f.ctx, tx, f.s, f.engine, ExportInput{ObservationID: o.ID, ObservationHash: o.Hash, OperationID: op, Keyword: keyword}, f.audit)
		return e
	})
	return r, e
}
func (f *fixtureDB) worker(id string, want tasks.State) {
	f.t.Helper()
	w, e := tasks.NewWorker(f.engine, tasks.WorkerConfig{Owner: "dns-ip-worker", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if e != nil {
		f.t.Fatal(e)
	}
	ctx, stop := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { stop(); <-done }()
	for {
		var state string
		if e = f.db.QueryRow(f.ctx, `SELECT state FROM adtr.tasks WHERE task_id=$1`, id).Scan(&state); e != nil {
			f.t.Fatal(e)
		}
		if tasks.State(state).Terminal() {
			if tasks.State(state) != want {
				f.t.Fatal("unexpected worker state", state)
			}
			return
		}
		select {
		case <-f.ctx.Done():
			f.t.Fatal("worker timeout")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func TestPostgresDNSIPWorkerDownloadAndReplay(t *testing.T) {
	f := newDB(t)
	o := f.seed()
	r, e := f.submit(o, "export", " ")
	if e != nil {
		t.Fatal(e)
	} // space literal gives a genuine empty workbook
	f.worker(r.Submission.Task.ID, tasks.Succeeded)
	var empty Manifest
	if e = f.tx(func(tx pgx.Tx) error {
		var e error
		empty, _, e = DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID)
		return e
	}); e != nil || empty.RowCount != 0 {
		t.Fatal(empty, e)
	}
	r, e = f.submit(o, "all", "")
	if e != nil {
		t.Fatal(e)
	}
	f.worker(r.Submission.Task.ID, tasks.Succeeded)
	var m Manifest
	var b []byte
	if e = f.tx(func(tx pgx.Tx) error {
		var e error
		m, b, e = DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID)
		return e
	}); e != nil || m.RowCount != 3 || len(b) == 0 {
		t.Fatal(m, e)
	}
	replay, e := f.submit(o, "all", "")
	if e != nil || !replay.Submission.Replayed || replay.Submission.Task.ID != r.Submission.Task.ID {
		t.Fatal(replay, e)
	}
	var again []byte
	if e = f.tx(func(tx pgx.Tx) error {
		_, v, e := DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID)
		again = v
		return e
	}); e != nil || !bytes.Equal(b, again) {
		t.Fatal("download changed", e)
	}
	total := 0
	for offset := 0; offset < 3; offset++ {
		if e = f.tx(func(tx pgx.Tx) error {
			p, e := ListTx(f.ctx, tx, f.s, Query{ObservationID: o.ID, Offset: offset, Limit: 1})
			if e == nil {
				total += len(p.Rows)
				if p.Total != m.RowCount {
					return ErrInvalid
				}
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	if total != m.RowCount {
		t.Fatal(total)
	}
	if _, e = f.db.Exec(f.ctx, `DELETE FROM adtr.simulation_dns_ip_artifacts WHERE task_id=$1`, r.Submission.Task.ID); e == nil {
		t.Fatal("committed artifact deletion accepted")
	}
}
func TestPostgresDNSIPIsolationQuotaAndAudit(t *testing.T) {
	f := newDB(t)
	o := f.seed()
	if e := f.tx(func(tx pgx.Tx) error {
		_, e := SubmitExportTx(f.ctx, tx, f.s, f.engine, ExportInput{ObservationID: o.ID, ObservationHash: o.Hash, OperationID: "audit-fail"}, func(context.Context, pgx.Tx, Receipt) error { return errors.New("audit rejected") })
		return e
	}); e == nil {
		t.Fatal("audit failure accepted")
	}
	var n int
	if e := f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.tasks`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	r, e := f.submit(o, "one", "")
	if e != nil {
		t.Fatal(e)
	}
	other := f.s
	other.ActorID = 2
	if e = f.tx(func(tx pgx.Tx) error {
		_, e := ReadExportTx(f.ctx, tx, other, f.engine, r.Submission.Task.ID)
		return e
	}); e == nil {
		t.Fatal("other owner read")
	}
	other = f.s
	other.Domain = "other"
	if e = f.tx(func(tx pgx.Tx) error {
		_, e := ListTx(f.ctx, tx, other, Query{ObservationID: o.ID, Limit: 10})
		return e
	}); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	for i := 1; i < MaxExports; i++ {
		if _, e = f.submit(o, fmt.Sprint("quota-", i), ""); e != nil {
			t.Fatal(i, e)
		}
	}
	if _, e = f.submit(o, "over", ""); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	if e = f.tx(func(tx pgx.Tx) error {
		_, e := CancelExportTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID, "safe-cancel", f.audit)
		return e
	}); e != nil {
		t.Fatal("quota blocks safe cancel", e)
	}
}
func TestPostgresDNSIPRejectedFinalizationAndCancellation(t *testing.T) {
	f := newDB(t)
	o := f.seed()
	r, e := f.submit(o, "cancel", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.tx(func(tx pgx.Tx) error {
		_, e := CancelExportTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID, "cancel-op", f.audit)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = f.tx(func(tx pgx.Tx) error { _, _, e := DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID); return e }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var n int
	f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.simulation_dns_ip_artifacts`).Scan(&n)
	if n != 0 {
		t.Fatal("queued cancel artifact")
	}
	r, e = f.submit(o, "revoke-after-gate", "")
	if e != nil {
		t.Fatal(e)
	}
	f.revokeGate.Store(true)
	f.worker(r.Submission.Task.ID, tasks.Failed)
	if e = f.tx(func(tx pgx.Tx) error { _, _, e := DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID); return e }); e == nil {
		t.Fatal("revoked candidate downloadable")
	}
	if e = f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.simulation_dns_ip_artifacts WHERE task_id=$1`, r.Submission.Task.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal("expected bounded unpublished candidate", n, e)
	}
}
func TestPostgresDNSIPSourceTamperingAndIntegrity(t *testing.T) {
	f := newDB(t)
	o := f.seed()
	bad := sample(3)
	bad.Records[0].IP = "192.0.2.44"
	if e := f.tx(func(tx pgx.Tx) error { _, e := SeedFixtureTx(f.ctx, tx, f.s, bad); return e }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(f.ctx, `UPDATE adtr.simulation_dns_ip_observations SET hash=repeat('a',64)`); e == nil {
		t.Fatal("immutable observation update accepted")
	}
	r, e := f.submit(o, "mismatch", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.submit(o, "mismatch", "other"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = f.tx(func(tx pgx.Tx) error { _, _, e := DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID); return e }); !errors.Is(e, ErrConflict) {
		t.Fatal("queued download", e)
	}
	f.worker(r.Submission.Task.ID, tasks.Succeeded)
	f.epoch.Add(1)
	if e = f.tx(func(tx pgx.Tx) error { _, e := ReceiptTx(f.ctx, tx, f.s, f.engine, "mismatch"); return e }); e == nil {
		t.Fatal("old epoch receipt readable")
	}
	if e = f.tx(func(tx pgx.Tx) error { _, _, e := DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID); return e }); e == nil {
		t.Fatal("old epoch download readable")
	}
}

func TestPostgresDNSIPFenceAndForgedSuccess(t *testing.T) {
	f := newDB(t)
	o := f.seed()
	r, e := f.submit(o, "fenced", "")
	if e != nil {
		t.Fatal(e)
	}
	lease, e := f.engine.Claim(f.ctx, "actual-owner")
	if e != nil {
		t.Fatal(e)
	}
	lease, e = f.engine.Start(f.ctx, lease)
	if e != nil {
		t.Fatal(e)
	}
	for _, forged := range []tasks.Lease{{Task: lease.Task, Owner: "other-owner", Token: lease.Token}, {Task: lease.Task, Owner: lease.Owner, Token: lease.Token + 1}} {
		if _, e = f.engine.Finish(f.ctx, forged, tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}); !errors.Is(e, tasks.ErrLeaseLost) {
			t.Fatal("forged finish", e)
		}
	}
	// Even the current lease cannot forge a complete result descriptor.
	finished, e := f.engine.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)})
	if e != nil || finished.State != tasks.Failed || finished.Error != "invalid_result" {
		t.Fatal("forged success", finished.State, finished.Error, e)
	}
	if e = f.tx(func(tx pgx.Tx) error { _, _, e := DownloadTx(f.ctx, tx, f.s, f.engine, r.Submission.Task.ID); return e }); !errors.Is(e, ErrConflict) {
		t.Fatal("forged download", e)
	}
	r, e = f.submit(o, "expired", "")
	if e != nil {
		t.Fatal(e)
	}
	lease, e = f.engine.Claim(f.ctx, "expires")
	if e != nil {
		t.Fatal(e)
	}
	lease, e = f.engine.Start(f.ctx, lease)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(f.ctx, `UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, r.Submission.Task.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.engine.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}); !errors.Is(e, tasks.ErrLeaseLost) {
		t.Fatal("expired finish", e)
	}
	var n int
	if e = f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.simulation_dns_ip_artifacts`).Scan(&n); e != nil || n != 0 {
		t.Fatal("rejected completion created artifact", n, e)
	}
}
