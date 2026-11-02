//go:build integration

package simulationdetectionschedules

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
	"sync"
	"testing"
	"time"
)

type scheduleFixture struct {
	t   *testing.T
	ctx context.Context
	cfg *pgx.ConnConfig
	db  *pgx.Conn
	e   *Engine
	q   *tasks.Engine
	s   Scope
}

func newScheduleFixture(t *testing.T) *scheduleFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; missing PostgreSQL is failure, not skip")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	var random [12]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "adtr_synthetic_schedules_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(c, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error("test-owned database cleanup failed", e)
		}
		_ = admin.Close(c)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	db, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	ddl := fmt.Sprintf(`CREATE SCHEMA adtr;CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true,version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,%d);`, schemaversion.Current) + tasks.Schema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL + SchemaSQL + ImmutableSchemaSQL + `CREATE TABLE adtr.synthetic_schedule_authority(epoch text NOT NULL,allowed boolean NOT NULL);INSERT INTO adtr.synthetic_schedule_authority VALUES('1',true);CREATE TABLE adtr.synthetic_schedule_audit(operation_id text PRIMARY KEY);`
	if _, err = db.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	auth := func(c context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		if p.TenantID != "tenant" || p.ActorID != 1 || scope.DomainID != "domain" || scope.TaskName != KindName || scope.Platform {
			return "", tasks.ErrAuthorization
		}
		var epoch string
		var allowed bool
		if er := tx.QueryRow(c, `SELECT epoch,allowed FROM adtr.synthetic_schedule_authority FOR UPDATE`).Scan(&epoch, &allowed); er != nil {
			return "", er
		}
		if !allowed {
			return "", tasks.ErrAuthorization
		}
		return epoch, nil
	}
	registry, err := tasks.NewRegistry(NewKind(auth))
	if err != nil {
		t.Fatal(err)
	}
	q, err := tasks.New(cfg, registry, auth, schemaversion.Current)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(cfg, q, auth)
	if err != nil {
		t.Fatal(err)
	}
	f := &scheduleFixture{t: t, ctx: ctx, cfg: cfg, db: db, e: engine, q: q, s: Scope{"tenant", "domain", 1}}
	err = f.tx(func(tx pgx.Tx) error {
		p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "schedule-fixture-v1", Synthetic: true}
		_, er := simulationevents.SeedFixtureTx(ctx, tx, securityevents.Scope{Tenant: "tenant", Domain: "domain", Read: true, Manage: true}, simulationevents.Fixture{ID: "dataset", Provenance: p, Events: []securityevents.RawEvent{{ID: "event", Tenant: "tenant", Domain: "domain", Kind: "login_failure", Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: p}}})
		if er != nil {
			return er
		}
		rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: detectionrules.Scope{Tenant: "tenant", Domain: "domain"}, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
		_, er = simulationdetection.MutateTx(ctx, tx, simulationdetection.Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}, simulationdetection.Mutation{ID: "template", Name: "Synthetic", OperationID: "template-create", Rules: []detectionrules.Rule{rule}}, func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil })
		return er
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *scheduleFixture) tx(fn func(pgx.Tx) error) error {
	c, err := pgx.ConnectConfig(f.ctx, f.cfg.Copy())
	if err != nil {
		return err
	}
	defer c.Close(context.Background())
	tx, err := c.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}
func (f *scheduleFixture) audit(ctx context.Context, tx pgx.Tx, a Audit) error {
	_, err := tx.Exec(ctx, `INSERT INTO adtr.synthetic_schedule_audit VALUES($1)`, a.OperationID)
	return err
}
func (f *scheduleFixture) create(op string) Change {
	f.t.Helper()
	var out Change
	err := f.tx(func(tx pgx.Tx) error {
		var e error
		out, e = f.e.CreateTx(f.ctx, tx, f.s, Mutation{OperationID: op, Definition: Definition{Label: "Synthetic", Cycle: 0, TemplateID: "template", TemplateVersion: 1, DatasetID: "dataset"}}, f.audit)
		return e
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// seedDue installs an isolated historical schedule directly at creation. It
// never disables triggers, rewinds existing cursors or adds production clocks.
func (f *scheduleFixture) seedDue(id string, cycle int, late time.Duration, back int64) Schedule {
	f.t.Helper()
	var out Schedule
	err := f.tx(func(tx pgx.Tx) error {
		if _, e := f.e.auth(f.ctx, tx, f.s, tasks.Write); e != nil {
			return e
		}
		var now time.Time
		if e := tx.QueryRow(f.ctx, `SELECT date_trunc('second',clock_timestamp())`).Scan(&now); e != nil {
			return e
		}
		when := now.Add(-late)
		anchor := when
		if cycle == 0 {
			anchor = when.AddDate(0, 0, -int(back))
		} else if cycle == 1 {
			anchor = when.AddDate(0, 0, -7*int(back))
		} else {
			anchor = when.AddDate(0, -int(back), 0)
		}
		d := Definition{Label: "Synthetic", Cycle: cycle, TemplateID: "template", TemplateVersion: 1, DatasetID: "dataset"}
		p, e := pin(f.ctx, tx, f.s, d)
		if e != nil {
			return e
		}
		out = Schedule{ID: id, DomainID: "domain", Revision: 1, ControlVersion: 1, State: "enabled", Definition: d, Payload: p, Anchor: anchor, NextIndex: 0, NextAt: anchor}
		_, e = tx.Exec(f.ctx, `INSERT INTO adtr.simulation_detection_schedules(tenant_id,domain_id,actor_id,schedule_id,revision,control_version,authorization_version,state,next_index,next_at) VALUES('tenant','domain',1,$1,1,1,'1','enabled',0,$2)`, id, anchor)
		if e != nil {
			return e
		}
		return insertRevision(f.ctx, tx, f.s, out)
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *scheduleFixture) count(table string) int {
	f.t.Helper()
	var n int
	if e := f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr."+pgx.Identifier{table}.Sanitize()).Scan(&n); e != nil {
		f.t.Fatal(e)
	}
	return n
}
func TestPostgresScheduleCRUDCASReceiptAndAuditRollback(t *testing.T) {
	f := newScheduleFixture(t)
	v := f.create("create")
	if v.Schedule.State != "paused" || v.Schedule.Revision != 1 {
		t.Fatal(v)
	}
	again := f.create("create")
	if !again.Replayed || again.Schedule.ID != v.Schedule.ID || f.count("synthetic_schedule_audit") != 1 {
		t.Fatal(again)
	}
	fail := errors.New("audit unavailable")
	err := f.tx(func(tx pgx.Tx) error {
		_, e := f.e.ControlTx(f.ctx, tx, f.s, ControlInput{ID: v.Schedule.ID, OperationID: "enable-fail", ExpectedControlVersion: 1, Action: "enable"}, func(context.Context, pgx.Tx, Audit) error { return fail })
		return e
	})
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if f.count("simulation_detection_schedule_operations") != 1 {
		t.Fatal("failed audit committed operation")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, op := range []string{"enable-a", "enable-b"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			results <- f.tx(func(tx pgx.Tx) error {
				_, e := f.e.ControlTx(f.ctx, tx, f.s, ControlInput{ID: v.Schedule.ID, OperationID: op, ExpectedControlVersion: 1, Action: "enable"}, f.audit)
				return e
			})
		}(op)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	err = f.tx(func(tx pgx.Tx) error {
		_, e := f.e.ControlTx(f.ctx, tx, f.s, ControlInput{ID: v.Schedule.ID, OperationID: "pause", ExpectedControlVersion: 2, Action: "pause"}, f.audit)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.tx(func(tx pgx.Tx) error {
		r, e := f.e.ReceiptTx(f.ctx, tx, f.s, "create")
		if e == nil && (r.Schedule.State != "paused" || r.Schedule.ControlVersion != 3 || r.Receipt.ControlVersion != 1) {
			t.Fatal(r)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.tx(func(tx pgx.Tx) error {
		_, e := f.e.DetailTx(f.ctx, tx, Scope{"tenant", "other", 1}, v.Schedule.ID, 1, 10)
		return e
	})
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(f.ctx, `UPDATE adtr.simulation_detection_schedule_revisions SET anchor=anchor`); err == nil {
		t.Fatal("mutable revision")
	}
}
func TestPostgresScheduleConcurrentTickActualExecutionAndIntent(t *testing.T) {
	f := newScheduleFixture(t)
	v := f.seedDue("due", 0, 0, 0)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.e.Tick(f.ctx); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal(e)
		}
	}
	if f.count("tasks") != 1 || f.count("simulation_detection_schedule_intents") != 1 {
		t.Fatal("duplicate occurrence")
	}
	if ok, e := f.q.RunOne(f.ctx, "schedule-worker"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	var taskID string
	var state string
	var raw []byte
	if e := f.db.QueryRow(f.ctx, `SELECT task_id,state,result FROM adtr.tasks`).Scan(&taskID, &state, &raw); e != nil {
		t.Fatal(e)
	}
	var result executionResult
	if e := json.Unmarshal(raw, &result); e != nil || state != "succeeded" || !result.Simulation || len(result.Matches) != 1 || result.EventCount != 1 || result.ScheduleID != v.ID {
		t.Fatal(state, string(raw), e)
	}
	err := f.tx(func(tx pgx.Tx) error {
		task, e := f.e.ReadRunTx(f.ctx, tx, f.s, taskID)
		if e == nil && task.State != tasks.Succeeded {
			t.Fatal(task)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.count("task_schedule_occurrences") != 0 {
		t.Fatal("pretended generic scheduler admission")
	}
	for _, cycle := range []int{1, 2} {
		t.Run(fmt.Sprintf("cycle-%d", cycle), func(t *testing.T) {
			other := newScheduleFixture(t)
			other.seedDue("calendar-due", cycle, 0, 0)
			if n, e := other.e.Tick(other.ctx); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			if ok, e := other.q.RunOne(other.ctx, "calendar-worker"); e != nil || !ok {
				t.Fatal(ok, e)
			}
			var state string
			if e := other.db.QueryRow(other.ctx, `SELECT state FROM adtr.tasks`).Scan(&state); e != nil || state != "succeeded" {
				t.Fatal(state, e)
			}
		})
	}
}
func TestPostgresScheduleMissedOverlapRevocationAndStalePin(t *testing.T) {
	f := newScheduleFixture(t)
	f.seedDue("late", 0, 2*time.Minute, 10)
	if n, e := f.e.Tick(f.ctx); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if f.count("tasks") != 0 {
		t.Fatal("backfilled old occurrence")
	}
	var first, last int64
	if e := f.db.QueryRow(f.ctx, `SELECT first_index,last_index FROM adtr.simulation_detection_schedule_events WHERE schedule_id='late'`).Scan(&first, &last); e != nil || first != 0 || last != 10 {
		t.Fatal(first, last, e)
	}
	f.seedDue("revoke", 0, 0, 0)
	if _, e := f.db.Exec(f.ctx, `UPDATE adtr.synthetic_schedule_authority SET epoch='2'`); e != nil {
		t.Fatal(e)
	}
	if n, e := f.e.Tick(f.ctx); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	var state string
	if e := f.db.QueryRow(f.ctx, `SELECT state FROM adtr.simulation_detection_schedules WHERE schedule_id='revoke'`).Scan(&state); e != nil || state != "authorization_blocked" {
		t.Fatal(state, e)
	}
	if _, e := f.db.Exec(f.ctx, `UPDATE adtr.synthetic_schedule_authority SET epoch='1'`); e != nil {
		t.Fatal(e)
	}
	f.seedDue("stale", 0, 0, 0)
	err := f.tx(func(tx pgx.Tx) error {
		_, e := simulationdetection.MutateTx(f.ctx, tx, simulationdetection.Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}, simulationdetection.Mutation{ID: "template", OperationID: "archive-template", ExpectedVersion: 1, Archive: true}, func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil })
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, e := f.e.Tick(f.ctx); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e := f.db.QueryRow(f.ctx, `SELECT state FROM adtr.simulation_detection_schedules WHERE schedule_id='stale'`).Scan(&state); e != nil || state != "configuration_blocked" {
		t.Fatal(state, e)
	}
}

func TestPostgresScheduledKindForgedOriginAndTemplateArchive(t *testing.T) {
	f := newScheduleFixture(t)
	v := f.seedDue("forged-source", 0, 0, 0)
	raw, _ := json.Marshal(Payload{ScheduleID: v.ID, Revision: 1, ScheduledAt: v.Anchor, Payload: v.Payload})
	err := f.tx(func(tx pgx.Tx) error {
		if er := f.e.CheckSchemaTx(f.ctx, tx); er != nil {
			return er
		}
		_, er := f.q.SubmitTx(f.ctx, tx, tasks.Principal{TenantID: "tenant", ActorID: 1}, tasks.SubmitInput{TaskName: KindName, DomainID: "domain", PayloadVersion: 1, Payload: raw, IdempotencyKey: "forged-manual"})
		return er
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, er := f.q.RunOne(f.ctx, "forged-worker"); er != nil || !ok {
		t.Fatal(ok, er)
	}
	var state, code string
	if er := f.db.QueryRow(f.ctx, `SELECT state,error_code FROM adtr.tasks WHERE idempotency_key='forged-manual'`).Scan(&state, &code); er != nil || state != "failed" || code != "authorization_revoked" {
		t.Fatal(state, code, er)
	}
	if n, er := f.e.Tick(f.ctx); er != nil || n != 1 {
		t.Fatal(n, er)
	}
	err = f.tx(func(tx pgx.Tx) error {
		_, er := simulationdetection.MutateTx(f.ctx, tx, simulationdetection.Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}, simulationdetection.Mutation{ID: "template", OperationID: "archive-after-admission", ExpectedVersion: 1, Archive: true}, func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil })
		return er
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, er := f.q.RunOne(f.ctx, "stale-worker"); er != nil || !ok {
		t.Fatal(ok, er)
	}
	if er := f.db.QueryRow(f.ctx, `SELECT state,error_code FROM adtr.tasks WHERE idempotency_key<>'forged-manual'`).Scan(&state, &code); er != nil || state != "failed" || code != "configuration_changed" {
		t.Fatal(state, code, er)
	}
}

func TestPostgresScheduleOverlapAcrossRevisions(t *testing.T) {
	f := newScheduleFixture(t)
	v := f.seedDue("overlap", 0, 0, 1)
	if n, e := f.e.Tick(f.ctx); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	// Model a new immutable revision due now while old revision task is queued.
	// Neither old intent nor cursor history is rewritten.
	err := f.tx(func(tx pgx.Tx) error {
		if _, e := f.e.auth(f.ctx, tx, f.s, tasks.Write); e != nil {
			return e
		}
		current, e := get(f.ctx, tx, f.s, v.ID, true)
		if e != nil {
			return e
		}
		var now time.Time
		if e = tx.QueryRow(f.ctx, `SELECT date_trunc('second',clock_timestamp())`).Scan(&now); e != nil {
			return e
		}
		current.Revision++
		current.Anchor = now
		if e = insertRevision(f.ctx, tx, f.s, current); e != nil {
			return e
		}
		_, e = tx.Exec(f.ctx, `UPDATE adtr.simulation_detection_schedules SET revision=2,control_version=control_version+1,next_index=0,next_at=$1 WHERE schedule_id=$2`, now, v.ID)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, e := f.e.Tick(f.ctx); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if f.count("tasks") != 1 || f.count("simulation_detection_schedule_intents") != 1 {
		t.Fatal("overlap task admitted")
	}
	var n int
	if e := f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.simulation_detection_schedule_events WHERE schedule_id=$1 AND revision=2 AND action='skipped_overlap'`, v.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}

func TestPostgresEnableAlreadyEnabledDoesNotSkipDue(t *testing.T) {
	f := newScheduleFixture(t)
	v := f.seedDue("already-enabled", 0, 0, 0)
	err := f.tx(func(tx pgx.Tx) error {
		_, e := f.e.ControlTx(f.ctx, tx, f.s, ControlInput{ID: v.ID, OperationID: "enable-again", ExpectedControlVersion: 1, Action: "enable"}, f.audit)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, e := f.e.Tick(f.ctx); e != nil || n != 1 {
		t.Fatal("redundant enable must not discard a due occurrence", n, e)
	}
}

func TestPostgresFinalPublicationRejectsPostExecutionConfigurationOrEpochChange(t *testing.T) {
	for _, scenario := range []string{"template", "epoch-regrant"} {
		t.Run(scenario, func(t *testing.T) {
			f := newScheduleFixture(t)
			f.seedDue("final-fence", 0, 0, 0)
			if n, e := f.e.Tick(f.ctx); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			lease, err := f.q.Claim(f.ctx, "final-fence-worker")
			if err != nil {
				t.Fatal(err)
			}
			lease, err = f.q.Start(f.ctx, lease)
			if err != nil {
				t.Fatal(err)
			}
			k := NewKind(f.e.authorize)
			outcome := k.Execute(f.ctx, tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
				return f.q.WithTx(ctx, lease, v, work)
			}})
			if outcome.State != tasks.Succeeded {
				t.Fatal("evaluation must succeed before publication race", outcome.State, outcome.Code)
			}
			want := "configuration_changed"
			if scenario == "template" {
				err = f.tx(func(tx pgx.Tx) error {
					_, e := simulationdetection.MutateTx(f.ctx, tx, simulationdetection.Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}, simulationdetection.Mutation{ID: "template", OperationID: "archive-between-execute-and-finish", ExpectedVersion: 1, Archive: true}, func(context.Context, pgx.Tx, simulationdetection.Audit) error { return nil })
					return e
				})
			} else {
				want = "authorization_revoked"
				_, err = f.db.Exec(f.ctx, `UPDATE adtr.synthetic_schedule_authority SET allowed=false,epoch='2';UPDATE adtr.synthetic_schedule_authority SET allowed=true`)
			}
			if err != nil {
				t.Fatal(err)
			}
			final, err := f.q.Finish(f.ctx, lease, outcome)
			if err != nil {
				t.Fatal(err)
			}
			if final.State != tasks.Failed || final.Error != want || (scenario == "template" && string(final.Result) != "{}") {
				t.Fatal("stale success published", final.State, final.Error, string(final.Result))
			}
			if scenario == "epoch-regrant" {
				er := f.tx(func(tx pgx.Tx) error { _, e := f.e.ReadRunTx(f.ctx, tx, f.s, final.ID); return e })
				var taskError *tasks.Error
				if !errors.As(er, &taskError) || taskError.Status != 404 || taskError.Code != "not_found" {
					t.Fatal("old epoch must be hidden after regrant", er)
				}
				er = f.tx(func(tx pgx.Tx) error {
					d, e := f.e.DetailTx(f.ctx, tx, f.s, "final-fence", 1, 50)
					if e == nil {
						for _, ev := range d.Events {
							if ev.Action == "succeeded" {
								t.Fatal("history relabelled revoked task success")
							}
						}
					}
					return e
				})
				if er != nil {
					t.Fatal(er)
				}
			}
		})
	}
}
