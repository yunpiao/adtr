//go:build integration

package operationallogs_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Every fixture runs the actual migrator in its own disposable database. These
// storage tests are not evidence of API/worker emitter or product acceptance.
type journalFixture struct {
	t         *testing.T
	ctx       context.Context
	cfg       *pgx.ConnConfig
	db        *pgx.Conn
	store     *operationallogs.Store
	principal operationallogs.Principal
}

func newJournalFixture(t *testing.T) *journalFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; missing PostgreSQL is a failure, not a skip")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("PostgreSQL unavailable")
	}
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "adtr_journal_" + hex.EncodeToString(nonce[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error("cannot remove owned test database", err)
		}
		_ = admin.Close(cleanup)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	if err = store.Migrate(ctx, cfg); err != nil {
		t.Fatal("actual migration failed", err)
	}
	if err = store.Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	s, err := operationallogs.NewStore(cfg, store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	f := &journalFixture{t: t, ctx: ctx, cfg: cfg, db: db, store: s, principal: operationallogs.Principal{TenantID: operationallogs.OwnerTenantID}}
	err = db.QueryRow(ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) VALUES('default','journal-owner','synthetic-unused','platform_admin',false) RETURNING id`).Scan(&f.principal.ActorID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *journalFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *journalFixture) tx(fn func(pgx.Tx) error) error {
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
	if err = tasks.CheckSchemaTx(f.ctx, tx, store.SchemaVersion); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}
func (f *journalFixture) list(p operationallogs.Principal, filter operationallogs.Filter) (operationallogs.List, error) {
	var out operationallogs.List
	err := f.tx(func(tx pgx.Tx) error {
		var err error
		out, err = operationallogs.ListTx(f.ctx, tx, p, filter)
		return err
	})
	return out, err
}

type capturingJournalStore struct {
	store       *operationallogs.Store
	event       operationallogs.Event
	observation operationallogs.RecorderObservation
	recorded    operationallogs.Event
}

func (s *capturingJournalStore) Append(ctx context.Context, e operationallogs.Event, o operationallogs.RecorderObservation) (operationallogs.Event, error) {
	s.event = e
	s.observation = o
	v, err := s.store.Append(ctx, e, o)
	s.recorded = v
	return v, err
}
func (f *journalFixture) emit() *capturingJournalStore {
	f.t.Helper()
	capture := &capturingJournalStore{store: f.store}
	r, err := operationallogs.NewRecorder(capture, operationallogs.API)
	if err != nil {
		f.t.Fatal(err)
	}
	if !r.Emit(operationallogs.ServiceStartRequested, operationallogs.Attempted, operationallogs.NoReason) {
		f.t.Fatal("producer admission failed")
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	if err = r.Close(ctx); err != nil {
		f.t.Fatal(err)
	}
	if r.Stats().Acknowledged != 1 {
		f.t.Fatal("event not acknowledged")
	}
	return capture
}

func TestJournalActualMigrationReplayWindowAndProvenance(t *testing.T) {
	f := newJournalFixture(t)
	empty, err := f.list(f.principal, operationallogs.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Page.Total != 0 || len(empty.List) != 0 || !empty.Exhausted || empty.Coverage.FirstRecordedAt != nil || empty.Coverage.JournalCreatedAt.IsZero() || empty.Coverage.Mode != "best_effort" || !empty.Coverage.GapsPossible || empty.Selection.StartTm == "" || empty.Selection.EndTm == "" {
		t.Fatalf("empty data fabricated coverage: %+v", empty)
	}
	capture := f.emit()
	original := capture.recorded
	replay, err := f.store.Append(f.ctx, capture.event, capture.observation)
	if err != nil || !reflect.DeepEqual(replay, original) {
		t.Fatalf("same identity replay changed committed record: %+v %v", replay, err)
	}
	conflict := capture.event
	conflict.Code = operationallogs.ServiceStopped
	conflict.Outcome = operationallogs.Completed
	_, err = f.store.Append(f.ctx, conflict, capture.observation)
	var public *operationallogs.Error
	if !errors.As(err, &public) || public.Code != "event_conflict" {
		t.Fatalf("conflicting content not rejected: %v", err)
	}
	filter := operationallogs.Filter{PageIdx: 1, PageSize: 20, Selection: operationallogs.Selection{StartTm: original.RecordedAt.Format(time.RFC3339Nano), EndTm: original.RecordedAt.Add(time.Second).Format(time.RFC3339Nano), SystemType: []string{"api"}}}
	page, err := f.list(f.principal, filter)
	if err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 1 || len(page.List) != 1 || page.List[0].Event.EventID != original.EventID || page.Coverage.FirstRecordedAt == nil || !page.Coverage.FirstRecordedAt.Equal(original.RecordedAt) {
		t.Fatalf("wrong exact window: %+v", page)
	}
	var decoded operationallogs.Event
	if json.Unmarshal([]byte(page.List[0].Log), &decoded) != nil || !reflect.DeepEqual(decoded, page.List[0].Event) {
		t.Fatal("legacy log field differs from typed event")
	}
	filter.StartTm = original.RecordedAt.Add(-time.Second).Format(time.RFC3339Nano)
	filter.EndTm = original.RecordedAt.Format(time.RFC3339Nano)
	page, err = f.list(f.principal, filter)
	if err != nil || page.Page.Total != 0 {
		t.Fatalf("end bound is not exclusive: %+v %v", page, err)
	}
	if err = f.tx(func(tx pgx.Tx) error {
		sources, err := operationallogs.SourcesTx(f.ctx, tx, f.principal)
		if err == nil && (len(sources.Modules) != 2 || sources.Modules[0].Module != operationallogs.API || sources.Modules[0].RecordedCount != 1 || sources.Modules[1].RecordedCount != 0 || len(sources.Reports) != 1 || sources.Reports[0].Evidence != "incomplete_process_observation" || sources.Reports[0].Acknowledged != 0 || sources.ReportsTruncated) {
			t.Fatalf("incorrect source/report evidence %+v", sources)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A fresh Store reconnects to persistent history; no process-local row cache.
	f.store, err = operationallogs.NewStore(f.cfg, store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	page, err = f.list(f.principal, operationallogs.Filter{})
	if err != nil || page.Page.Total != 1 {
		t.Fatal("persisted journal vanished after store restart", err)
	}
}

func TestJournalDedicatedReadAuthorityAndExactGate(t *testing.T) {
	f := newJournalFixture(t)
	f.emit()
	denied := []operationallogs.Principal{{TenantID: "another", ActorID: f.principal.ActorID}, {TenantID: "default", ActorID: 0}, {TenantID: "default", ActorID: f.principal.ActorID + 1000}}
	for _, p := range denied {
		if out, err := f.list(p, operationallogs.Filter{}); err == nil || out.Page.Total != 0 || len(out.List) != 0 {
			t.Fatalf("unauthorized journal/count disclosure %+v %v", out, err)
		}
	}
	f.exec(`INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default','journal-reader','Journal reader')`)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default','journal-reader','system',true,false),('default','journal-reader','system_logs',true,false)`)
	var reader int64
	if err := f.db.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change) VALUES('default','read-only-journal','synthetic-unused','viewer','journal-reader',false) RETURNING id`).Scan(&reader); err != nil {
		t.Fatal(err)
	}
	p := operationallogs.Principal{TenantID: "default", ActorID: reader}
	if out, err := f.list(p, operationallogs.Filter{}); err != nil || out.Page.Total != 1 {
		t.Fatal("dedicated read unexpectedly requires tasks grants", err)
	}
	f.exec(`UPDATE adtr.access_permissions SET readable=false WHERE tenant_id='default' AND role_id='journal-reader' AND mark='system_logs'`)
	if _, err := f.list(p, operationallogs.Filter{}); err == nil {
		t.Fatal("revoked module read still allowed")
	}
	f.exec(`UPDATE adtr.access_permissions SET readable=true WHERE tenant_id='default' AND role_id='journal-reader' AND mark='system_logs'`)
	for _, field := range []string{"disabled=true", "must_change=true", "password_updated_at=clock_timestamp()-interval '91 days'"} {
		f.exec(`UPDATE adtr.users SET `+field+` WHERE id=$1`, reader)
		if _, err := f.list(p, operationallogs.Filter{}); err == nil {
			t.Fatal("inactive identity allowed:", field)
		}
		f.exec(`UPDATE adtr.users SET disabled=false,must_change=false,password_updated_at=clock_timestamp() WHERE id=$1`, reader)
	}
	tx, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = operationallogs.RequireSchema(f.ctx, tx); !errors.Is(err, tasks.ErrSchemaGateRequired) {
		t.Fatalf("missing prior gate accepted %v", err)
	}
	_ = tx.Rollback(f.ctx)
	f.exec(`UPDATE adtr.schema_version SET version=13 WHERE singleton=true`)
	if _, err := f.list(f.principal, operationallogs.Filter{}); !errors.Is(err, tasks.ErrSchemaIncompatible) {
		t.Fatalf("old schema accepted %v", err)
	}
	if _, err := operationallogs.NewStore(f.cfg, 13); err == nil {
		t.Fatal("old recorder writer admitted")
	}
}

func TestJournalPaginationCapsAndHistoryImmutability(t *testing.T) {
	f := newJournalFixture(t)
	captured := f.emit()
	// Explicit synthetic fixtures exercise count/order/caps, not producer evidence.
	f.exec(`INSERT INTO adtr.operational_log_events(event_id,process_id,schema_version,module,code,outcome,severity,reason,observed_at,recorded_at)
 SELECT '00000000-0000-4000-8000-'||lpad(g::text,12,'0'),$1,1,'worker','queue_cycle','completed','info','','2026-01-01T00:00:00Z','2026-01-01T01:00:00Z' FROM generate_series(1,1001) g`, captured.event.ProcessID)
	selection := operationallogs.Selection{StartTm: "2026-01-01T00:00:00Z", EndTm: "2026-01-02T00:00:00Z", SystemType: []string{"worker"}}
	page, err := f.list(f.principal, operationallogs.Filter{PageIdx: 2, PageSize: 100, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 1001 || page.Page.TotalPage != 11 || len(page.List) != 100 || page.Exhausted || page.List[0].Event.EventID != "00000000-0000-4000-8000-000000000901" {
		t.Fatalf("incorrect count/tie-break pagination %+v", page.Page)
	}
	_, err = f.list(f.principal, operationallogs.Filter{PageIdx: 1, PageSize: -1, Selection: selection})
	var public *operationallogs.Error
	if !errors.As(err, &public) || public.Code != "result_too_large" {
		t.Fatal("unbounded all-rows request accepted", err)
	}
	page, err = f.list(f.principal, operationallogs.Filter{PageIdx: 12, PageSize: 100, Selection: selection})
	if err != nil || len(page.List) != 0 || !page.Exhausted || page.Page.Total != 1001 {
		t.Fatal("empty later page lost total", err)
	}
	for _, sql := range []string{`UPDATE adtr.operational_log_events SET reason=''`, `DELETE FROM adtr.operational_log_events`, `TRUNCATE adtr.operational_log_events CASCADE`, `UPDATE adtr.operational_log_state SET created_at=clock_timestamp()`, `DELETE FROM adtr.operational_log_state`, `TRUNCATE adtr.operational_log_state`, `UPDATE adtr.operational_log_reports SET accepted=0`, `DELETE FROM adtr.operational_log_reports`, `TRUNCATE adtr.operational_log_reports`, `TRUNCATE adtr.operational_log_audit`} {
		if _, err = f.db.Exec(f.ctx, sql); err == nil {
			t.Fatal("protected journal/history mutation accepted", sql)
		}
	}
}
