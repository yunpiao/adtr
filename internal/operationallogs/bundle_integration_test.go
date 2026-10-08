//go:build integration

package operationallogs_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

type bundleDBFixture struct {
	t      *testing.T
	ctx    context.Context
	cfg    *pgx.ConnConfig
	conn   *pgx.Conn
	engine *tasks.Engine
	actor  int64
}

func newBundleDBFixture(t *testing.T) *bundleDBFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: missing actual PostgreSQL environment is not a pass")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	name := fmt.Sprintf("adtr_bundle_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close(cleanup)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	if err = store.Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	f := &bundleDBFixture{t: t, ctx: ctx, cfg: cfg, conn: conn}
	if err = conn.QueryRow(ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) VALUES('default','synthetic-bundle-owner','synthetic-not-a-password','platform_admin',false) RETURNING id`).Scan(&f.actor); err != nil {
		t.Fatal(err)
	}
	f.engine, err = tasks.New(cfg, tasks.ProductionRegistry(operationallogs.Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *bundleDBFixture) tx(fn func(pgx.Tx) error) error {
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock_shared(734192801)`); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}
func (f *bundleDBFixture) p() operationallogs.Principal {
	return operationallogs.Principal{TenantID: "default", ActorID: f.actor}
}
func (f *bundleDBFixture) tp() tasks.Principal {
	return tasks.Principal{TenantID: "default", ActorID: f.actor}
}
func (f *bundleDBFixture) submit(key string, start, end time.Time) tasks.Submission {
	f.t.Helper()
	raw, _ := json.Marshal(operationallogs.BundlePayload{StartTm: start.UTC().Format(time.RFC3339Nano), EndTm: end.UTC().Format(time.RFC3339Nano), SystemType: []string{"api", "worker"}})
	var out tasks.Submission
	err := f.tx(func(tx pgx.Tx) error {
		var err error
		out, err = f.engine.SubmitTx(f.ctx, tx, f.tp(), tasks.SubmitInput{TaskName: operationallogs.BundleKindName, DomainID: "platform", PayloadVersion: 1, Payload: raw, IdempotencyKey: key})
		if err != nil {
			return err
		}
		if !out.Replayed {
			return operationallogs.RecordBundleTx(f.ctx, tx, f.p(), out.Task.ID, "bundle_submit")
		}
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *bundleDBFixture) record() {
	f.t.Helper()
	s, err := operationallogs.NewStore(f.cfg, store.SchemaVersion)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, module := range []operationallogs.Module{operationallogs.API, operationallogs.Worker} {
		r, err := operationallogs.NewRecorder(s, module)
		if err != nil {
			f.t.Fatal(err)
		}
		if !r.Emit(operationallogs.ServiceStartRequested, operationallogs.Attempted, operationallogs.NoReason) || !r.Emit(operationallogs.ServiceStopped, operationallogs.Completed, operationallogs.NoReason) {
			f.t.Fatal("producer rejected typed event")
		}
		closeCtx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
		err = r.Close(closeCtx)
		cancel()
		if err != nil {
			f.t.Fatal(err)
		}
		if stats := r.Stats(); stats.Acknowledged != 2 || stats.Unacknowledged != 0 || stats.Abandoned != 0 {
			f.t.Fatalf("recorder did not acknowledge actual storage: %+v", stats)
		}
	}
}
func (f *bundleDBFixture) detail(id string) operationallogs.BundleDetail {
	f.t.Helper()
	var out operationallogs.BundleDetail
	err := f.tx(func(tx pgx.Tx) error {
		var err error
		out, err = operationallogs.DetailTx(f.ctx, tx, f.p(), id)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *bundleDBFixture) download(id string) ([]byte, error) {
	var data []byte
	err := f.tx(func(tx pgx.Tx) error {
		var err error
		data, err = operationallogs.ReadArtifactTx(f.ctx, tx, f.p(), id)
		return err
	})
	return data, err
}
func (f *bundleDBFixture) stage() (tasks.Lease, tasks.Outcome) {
	f.t.Helper()
	lease, err := f.engine.Claim(f.ctx, "synthetic-bundle-worker")
	if err != nil {
		f.t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil {
		f.t.Fatal(err)
	}
	out := operationallogs.Kind().Execute(f.ctx, tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, version, work)
	}})
	if out.State != tasks.Succeeded {
		f.t.Fatalf("bundle staging failed: %s", out.Code)
	}
	return lease, out
}

func TestBundleActualMigrationRecorderSnapshotAndBytes(t *testing.T) {
	f := newBundleDBFixture(t)
	f.record()
	start := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	end := start.Add(2 * time.Hour)
	sub := f.submit("bundle-real-events", start, end)
	replay := f.submit("bundle-real-events", start, end)
	if !replay.Replayed || replay.Task.ID != sub.Task.ID {
		t.Fatal("idempotency replay created a replacement")
	}
	var audits int
	if err := f.conn.QueryRow(f.ctx, `SELECT count(*) FROM adtr.operational_log_audit WHERE task_id=$1 AND action='bundle_submit'`, sub.Task.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("control audit duplicated", err)
	}
	if worked, err := f.engine.RunOne(f.ctx, "actual-bundle-worker"); err != nil || !worked {
		t.Fatal("real worker failed", err)
	}
	detail := f.detail(sub.Task.ID)
	if detail.Task.State != tasks.Succeeded || !detail.DownloadReady || detail.RowCount == nil || *detail.RowCount != 4 || string(detail.Task.Result) != "{}" || string(detail.Task.Cursor) != "{}" {
		t.Fatalf("bad detail: %+v", detail)
	}
	data, err := f.download(sub.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) != 2 {
		t.Fatal("download is not exact ZIP", err)
	}
	if z.File[0].Name != "manifest.json" || z.File[1].Name != "events.jsonl" {
		t.Fatal("unsafe ZIP entries")
	}
	reader, err := z.File[1].Open()
	if err != nil {
		t.Fatal(err)
	}
	jsonl, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var journal operationallogs.List
	err = f.tx(func(tx pgx.Tx) error {
		var e error
		journal, e = operationallogs.ListTx(f.ctx, tx, f.p(), operationallogs.Filter{PageIdx: 1, PageSize: 100, Selection: operationallogs.Selection{StartTm: start.Format(time.RFC3339Nano), EndTm: end.Format(time.RFC3339Nano), SystemType: []string{"api", "worker"}}})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(jsonl, []byte{'\n'}), []byte{'\n'})
	if len(lines) != 4 || len(journal.List) != 4 {
		t.Fatal("real journal rows absent")
	}
	for i, line := range lines {
		if string(line) != journal.List[i].Log {
			t.Fatal("snapshot changed real event identity/order")
		}
	}
	empty := f.submit("empty-real-window", start.Add(-2*time.Hour), start.Add(-time.Hour))
	if _, err = f.engine.RunOne(f.ctx, "empty-bundle-worker"); err != nil {
		t.Fatal(err)
	}
	if d := f.detail(empty.Task.ID); !d.DownloadReady || d.RowCount == nil || *d.RowCount != 0 || !d.Coverage.GapsPossible {
		t.Fatal("empty window implies invented coverage")
	}
	for _, table := range []string{"snapshots", "rows", "chunks", "manifests"} {
		if _, err = f.conn.Exec(f.ctx, `DELETE FROM adtr.operational_log_bundle_`+table+` WHERE task_id=$1`, sub.Task.ID); err == nil {
			t.Fatalf("mutable %s", table)
		}
	}
	// Deliberate corruption is confined to this freshly migrated disposable DB.
	if _, err = f.conn.Exec(f.ctx, `ALTER TABLE adtr.operational_log_bundle_chunks DISABLE TRIGGER operational_log_bundle_chunks_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(f.ctx, `UPDATE adtr.operational_log_bundle_chunks SET data=set_byte(data,0,0) WHERE task_id=$1`, sub.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(f.ctx, `ALTER TABLE adtr.operational_log_bundle_chunks ENABLE TRIGGER operational_log_bundle_chunks_immutable`); err != nil {
		t.Fatal(err)
	}
	if !f.detail(sub.Task.ID).DownloadReady {
		t.Fatal("metadata eligibility must stay separate from bytes")
	}
	if _, err = f.download(sub.Task.ID); err == nil {
		t.Fatal("corrupt protected bytes downloaded")
	}
	if _, err = f.conn.Exec(f.ctx, `INSERT INTO adtr.task_visibility(task_id,tenant_id,archived,visibility_version,archived_at,archived_by,reason) VALUES($1,'default',true,1,clock_timestamp(),$2,'synthetic archive boundary')`, sub.Task.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	if err = f.tx(func(tx pgx.Tx) error { _, e := operationallogs.DetailTx(f.ctx, tx, f.p(), sub.Task.ID); return e }); err == nil {
		t.Fatal("archived artifact detail exposed")
	}
	if _, err = f.download(sub.Task.ID); err == nil {
		t.Fatal("archived artifact bytes exposed")
	}
	var active operationallogs.BundleHistory
	if err = f.tx(func(tx pgx.Tx) error {
		var e error
		active, e = operationallogs.HistoryTx(f.ctx, tx, f.p(), operationallogs.HistoryFilter{})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if active.Page.Total != 1 || len(active.List) != 1 || active.List[0].Task.ID != empty.Task.ID {
		t.Fatal("archived artifacts affected active counts")
	}
}

func TestBundleStagedCancellationAndStaleFenceNeverPublish(t *testing.T) {
	for _, mode := range []string{"cancel", "expired"} {
		t.Run(mode, func(t *testing.T) {
			f := newBundleDBFixture(t)
			f.record()
			start := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
			sub := f.submit("staged-"+mode, start, start.Add(2*time.Hour))
			lease, out := f.stage()
			if d := f.detail(sub.Task.ID); d.DownloadReady || d.ArtifactStatus != "not_ready" {
				t.Fatal("staged archive published before Finish")
			}
			if _, err := f.download(sub.Task.ID); err == nil {
				t.Fatal("running artifact downloaded")
			}
			if mode == "cancel" {
				err := f.tx(func(tx pgx.Tx) error {
					if _, err := f.engine.CancelTx(f.ctx, tx, f.tp(), sub.Task.ID); err != nil {
						return err
					}
					return operationallogs.RecordBundleTx(f.ctx, tx, f.p(), sub.Task.ID, "bundle_cancel")
				})
				if err != nil {
					t.Fatal(err)
				}
				terminal, err := f.engine.Finish(f.ctx, lease, out)
				if err != nil || terminal.State != tasks.Cancelled || string(terminal.Result) != "{}" {
					t.Fatal("cancel raced into publication", err)
				}
			} else {
				if _, err := f.conn.Exec(f.ctx, `UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, sub.Task.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.engine.RecoverExpired(f.ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := f.engine.Finish(f.ctx, lease, out); !errors.Is(err, tasks.ErrLeaseLost) {
					t.Fatalf("stale worker published: %v", err)
				}
				stale := operationallogs.Kind().Execute(f.ctx, tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
					return f.engine.WithTx(ctx, lease, version, work)
				}})
				if stale.State != tasks.Failed || stale.Code != "lease_lost" || len(stale.Result) != 0 {
					t.Fatal("stale executor reused staged artifact")
				}
			}
			if d := f.detail(sub.Task.ID); d.DownloadReady || d.ArtifactStatus != "not_ready" || d.Task.State == tasks.Succeeded {
				t.Fatal("ineligible artifact became ready")
			}
			if _, err := f.download(sub.Task.ID); err == nil {
				t.Fatal("terminal stale staging downloaded")
			}
		})
	}
}

func TestBundleActualRowCapRollsBackAndMetadataDamageIsPerRow(t *testing.T) {
	f := newBundleDBFixture(t)
	// Bulk synthetic rows test the cap only, not production instrumentation.
	_, err := f.conn.Exec(f.ctx, `INSERT INTO adtr.operational_log_events(event_id,process_id,schema_version,module,code,outcome,severity,reason,observed_at) SELECT '00000000-0000-4000-8000-'||lpad(i::text,12,'0'),'10000000-0000-4000-8000-000000000001',1,'worker','queue_cycle','completed','info','',clock_timestamp() FROM generate_series(1,10001) i`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	sub := f.submit("row-cap", start, start.Add(2*time.Hour))
	if _, err = f.engine.RunOne(f.ctx, "cap-bundle-worker"); err != nil {
		t.Fatal(err)
	}
	d := f.detail(sub.Task.ID)
	if d.Task.State != tasks.Failed || d.Task.Error != "result_too_large" {
		t.Fatalf("cap did not fail explicitly: %+v", d)
	}
	var count int
	if err = f.conn.QueryRow(f.ctx, `SELECT count(*) FROM adtr.operational_log_bundle_snapshots WHERE task_id=$1`, sub.Task.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("oversize snapshot partially committed", err)
	}
	empty := f.submit("metadata-damage", start.Add(-2*time.Hour), start.Add(-time.Hour))
	if _, err = f.engine.RunOne(f.ctx, "metadata-bundle-worker"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(f.ctx, `UPDATE adtr.tasks SET result='{"private":"must never escape"}'::jsonb WHERE task_id=$1`, empty.Task.ID); err != nil {
		t.Fatal(err)
	}
	var history operationallogs.BundleHistory
	err = f.tx(func(tx pgx.Tx) error {
		var e error
		history, e = operationallogs.HistoryTx(f.ctx, tx, f.p(), operationallogs.HistoryFilter{})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if history.Page.Total != 2 || len(history.List) != 2 {
		t.Fatal("damaged artifact suppressed unrelated history")
	}
	found := false
	for _, row := range history.List {
		if row.Task.ID == empty.Task.ID {
			found = true
			if row.DownloadReady || row.ArtifactStatus != "artifact_invalid" || row.Coverage != nil {
				t.Fatal("invalid artifact metadata exposed")
			}
		}
	}
	if !found {
		t.Fatal("damaged artifact vanished")
	}
	encoded, _ := json.Marshal(history)
	if strings.Contains(string(encoded), "must never escape") {
		t.Fatal("private result escaped history")
	}
}
