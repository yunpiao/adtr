//go:build integration

package simulationdashboard

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
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/tasks"
)

// This test owns a random throwaway database, never the supplied database.
// Missing PostgreSQL is a failure, not a skip or simulated pass.
func TestPostgresDashboardSnapshot(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: missing PostgreSQL is a failure, not a skip")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal("invalid test database config")
	}
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	defer admin.Close(context.Background())
	var random [12]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	name := "adtr_dashboard_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+quoted); e != nil {
		t.Fatal(e)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(c, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	connect := func() *pgx.Conn {
		c, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal("test connection unavailable")
		}
		return c
	}
	db := connect()
	defer db.Close(context.Background())
	schema := `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL); INSERT INTO adtr.schema_version VALUES(true,26);`
	if _, e = db.Exec(ctx, schema+simulationevents.SchemaSQL+simulationevents.ImmutableSchemaSQL+simulationrisk.SchemaSQL+simulationrisk.ImmutableSchemaSQL+simulationdetection.SchemaSQL+simulationdetection.ImmutableSchemaSQL+tasks.Schema+`CREATE TABLE adtr.dashboard_test_audit(kind text,operation text);`); e != nil {
		t.Fatal(e)
	}
	transact := func(c *pgx.Conn, fn func(pgx.Tx) error) {
		tx, err := c.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = fn(tx); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s := Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, AuthorizationVersion: "epoch1", Read: true}
	in := Input{DatasetID: "dataset", Subject: "actor", Window: "1h"}
	es := securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true, Manage: true}
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "synthetic-dashboard-pg", Synthetic: true}
	fixture := simulationevents.Fixture{ID: in.DatasetID, Provenance: p, Events: []securityevents.RawEvent{{ID: "event", Tenant: s.Tenant, Domain: s.Domain, Kind: "login_failure", Actor: "actor", Device: "device", Session: "session", SourceIP: "192.0.2.1", OccurredAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), Provenance: p}}, Cases: []simulationevents.FixtureCase{{ID: "case", EvidenceIDs: []string{"event"}}}}
	transact(db, func(tx pgx.Tx) error { _, err := simulationevents.SeedFixtureTx(ctx, tx, es, fixture); return err })
	read := func(sc Scope) Summary {
		var out Summary
		transact(db, func(tx pgx.Tx) error { var err error; out, err = SummaryTx(ctx, tx, sc, in); return err })
		return out
	}
	before := read(s)
	if before.Cases.Open != 1 || before.EventActivity.Total != 1 || before.Risk.Score == nil || *before.Risk.Score != 5 || before.LatestManualDetection.Status != "no_result" {
		t.Fatal("initial facts", before)
	}
	// Real declaration -> admission receipt -> actual production synthetic executor
	// -> engine success publication. No INSERT of a fabricated succeeded result.
	auth := func(_ context.Context, _ pgx.Tx, principal tasks.Principal, scope tasks.Scope, _ tasks.Action) (string, error) {
		if principal.TenantID != s.Tenant || principal.ActorID != s.ActorID || scope.DomainID != s.Domain || scope.TaskName != simulationdetection.KindName {
			return "", tasks.ErrAuthorization
		}
		return s.AuthorizationVersion, nil
	}
	registry, e := tasks.NewRegistry(simulationdetection.NewKind(auth))
	if e != nil {
		t.Fatal(e)
	}
	engine, e := tasks.New(cfg, registry, auth, 26)
	if e != nil {
		t.Fatal(e)
	}
	ds := simulationdetection.Scope{Tenant: s.Tenant, Domain: s.Domain, ActorID: 1, Read: true, Manage: true}
	da := func(ctx context.Context, tx pgx.Tx, a simulationdetection.Audit) error {
		_, err := tx.Exec(ctx, `INSERT INTO adtr.dashboard_test_audit VALUES('detection',$1)`, a.OperationID)
		return err
	}
	rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
	var taskID string
	transact(db, func(tx pgx.Tx) error {
		_, err := simulationdetection.MutateTx(ctx, tx, ds, simulationdetection.Mutation{ID: "template", Name: "Synthetic", OperationID: "create", Rules: []detectionrules.Rule{rule}}, da)
		return err
	})
	transact(db, func(tx pgx.Tx) error {
		sub, err := simulationdetection.SubmitRunTx(ctx, tx, ds, engine, simulationdetection.RunInput{TemplateID: "template", DatasetID: in.DatasetID, TemplateVersion: 1, OperationID: "run"}, da)
		taskID = sub.Task.ID
		return err
	})
	if worked, err := engine.RunOne(ctx, "dashboard-test-worker"); err != nil || !worked {
		t.Fatal("worker did not execute", worked, err)
	}
	out := read(s)
	if out.LatestManualDetection.Status != "available" || out.LatestManualDetection.TaskUUID != taskID || out.LatestManualDetection.MatchCount == nil || *out.LatestManualDetection.MatchCount != 1 {
		t.Fatal("actual task output unavailable", out.LatestManualDetection)
	}
	other := s
	other.ActorID = 2
	if read(other).LatestManualDetection.Status != "no_result" {
		t.Fatal("cross-owner leak")
	}
	other = s
	other.AuthorizationVersion = "epoch2"
	if read(other).LatestManualDetection.Status != "no_result" {
		t.Fatal("stale epoch leak")
	}
	if _, e = db.Exec(ctx, `INSERT INTO adtr.task_visibility(task_id,tenant_id,archived,visibility_version,archived_at,archived_by,reason) VALUES($1,$2,true,1,clock_timestamp(),1,'synthetic test')`, taskID, s.Tenant); e != nil {
		t.Fatal(e)
	}
	if read(s).LatestManualDetection.Status != "no_result" {
		t.Fatal("archived result leaked")
	}
	// Hold one statement after its MVCC snapshot while a separate transaction
	// atomically closes the case and resets risk. This proves no mixed cards.
	locker := connect()
	defer locker.Close(context.Background())
	writer := connect()
	defer writer.Close(context.Background())
	reader := connect()
	defer reader.Close(context.Background())
	const gate int64 = 845128432
	if _, e = locker.Exec(ctx, `SELECT pg_advisory_lock($1)`, gate); e != nil {
		t.Fatal(e)
	}
	defer locker.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, gate)
	tx, e := reader.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	gated := &gatedSnapshotTx{Tx: tx, key: gate}
	type answer struct {
		summary Summary
		err     error
	}
	done := make(chan answer, 1)
	go func() { r, err := SummaryTx(ctx, gated, s, in); done <- answer{r, err} }()
	for {
		var waiting bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='advisory')`, reader.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case a := <-done:
			t.Fatal("snapshot gate did not block", a.err)
		case <-ctx.Done():
			t.Fatal("snapshot gate timeout")
		case <-time.After(5 * time.Millisecond):
		}
	}
	transact(writer, func(tx pgx.Tx) error {
		_, err := simulationevents.TransitionTx(ctx, tx, es, simulationevents.TransitionInput{DatasetID: in.DatasetID, CaseID: "case", OperationID: "close", Change: securityevents.Change{Actor: "reviewer", Action: "close", Reason: "synthetic", ExpectedRevision: 1}}, func(ctx context.Context, tx pgx.Tx, r simulationevents.Receipt) error {
			_, e := tx.Exec(ctx, `INSERT INTO adtr.dashboard_test_audit VALUES('close',$1)`, r.OperationID)
			return e
		})
		if err != nil {
			return err
		}
		_, err = simulationrisk.ResetTx(ctx, tx, simulationrisk.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true, Manage: true}, simulationrisk.ResetInput{DatasetID: in.DatasetID, Subject: in.Subject, OperationID: "reset", Actor: "reviewer", Reason: "synthetic", ExpectedRevision: 1}, func(ctx context.Context, tx pgx.Tx, r simulationrisk.Receipt) error {
			_, e := tx.Exec(ctx, `INSERT INTO adtr.dashboard_test_audit VALUES('reset',$1)`, r.OperationID)
			return e
		})
		return err
	})
	if _, e = locker.Exec(ctx, `SELECT pg_advisory_unlock($1)`, gate); e != nil {
		t.Fatal(e)
	}
	a := <-done
	if a.err != nil || a.summary.Cases.Closed != 0 || a.summary.Risk.Score == nil || *a.summary.Risk.Score != 5 {
		t.Fatal("mixed snapshot", a.summary.Cases, a.summary.Risk, a.err)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	after := read(s)
	if after.Cases.Closed != 1 || after.CaseCloseThroughput.Last60Seconds != 1 || after.Risk.Score == nil || *after.Risk.Score != 0 || after.Risk.Revision != 2 {
		t.Fatal("postcommit facts", after.Cases, after.Risk)
	}
	bad := s
	bad.Domain = "other"
	transact(db, func(tx pgx.Tx) error {
		_, err := SummaryTx(ctx, tx, bad, in)
		if !errors.Is(err, ErrNotFound) {
			t.Fatal("crossdomain", err)
		}
		return nil
	})
}

// Test-only query gate preserves the exact source statement and snapshot while
// forcing a concurrent writer to commit between statement start and return.
type gatedSnapshotTx struct {
	pgx.Tx
	key int64
}

func (t *gatedSnapshotTx) QueryRow(ctx context.Context, q string, args ...any) pgx.Row {
	if q == sourceSQL {
		q = strings.Replace(q, "WITH d AS MATERIALIZED (", "WITH gate AS MATERIALIZED (SELECT pg_advisory_xact_lock(845128432)), d AS MATERIALIZED (", 1)
		q = strings.TrimSuffix(q, ") FROM d") + ") FROM d CROSS JOIN gate"
	}
	return t.Tx.QueryRow(ctx, q, args...)
}
