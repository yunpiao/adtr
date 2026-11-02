//go:build integration

package simulationdetection_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"os"
	"testing"
	"time"
)

func TestPostgresSimulationDetection(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: missing PostgreSQL is a failure, not a skip")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database config")
	}
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	defer admin.Close(context.Background())
	var random [12]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "adtr_sim_events_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	if _, err = db.Exec(ctx, "CREATE SCHEMA adtr;"+simulationevents.SchemaSQL+simulationevents.ImmutableSchemaSQL+simulationdetection.SchemaSQL+simulationdetection.ImmutableSchemaSQL+`CREATE TABLE adtr.synthetic_audit(operation_id text PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	scope := simulationdetection.Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}
	audit := func(ctx context.Context, tx pgx.Tx, a simulationdetection.Audit) error {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.synthetic_audit VALUES($1)`, a.OperationID)
		return e
	}
	rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: detectionrules.Scope{Tenant: scope.Tenant, Domain: scope.Domain}, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
	input := simulationdetection.Mutation{ID: "template", Name: "Synthetic detection", OperationID: "create", Rules: []detectionrules.Rule{rule}}
	mutate := func(in simulationdetection.Mutation, fn simulationdetection.AuditFunc) (simulationdetection.Template, error) {
		tx, e := db.Begin(ctx)
		if e != nil {
			return simulationdetection.Template{}, e
		}
		defer tx.Rollback(ctx)
		out, e := simulationdetection.MutateTx(ctx, tx, scope, in, fn)
		if e != nil {
			return out, e
		}
		return out, tx.Commit(ctx)
	}
	first, e := mutate(input, audit)
	if e != nil || first.Version != 1 {
		t.Fatal(first, e)
	}
	replay, e := mutate(input, audit)
	if e != nil || first.Hash != replay.Hash {
		t.Fatal(replay, e)
	}
	bad := input
	bad.Name = "different"
	if _, e = mutate(bad, audit); !errors.Is(e, simulationdetection.ErrConflict) {
		t.Fatal(e)
	}
	clone := func(in simulationdetection.Mutation) simulationdetection.Mutation {
		b, e := json.Marshal(in)
		if e != nil {
			t.Fatal(e)
		}
		var out simulationdetection.Mutation
		if e = json.Unmarshal(b, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	update := clone(input)
	update.OperationID = "update"
	update.ExpectedVersion = 1
	update.Name = "Updated"
	mismatch := clone(update)
	mismatch.OperationID = "version-mismatch"
	if _, e = mutate(mismatch, audit); !errors.Is(e, simulationdetection.ErrInvalid) {
		t.Fatal("mismatched rule version accepted", e)
	}
	update.Rules[0].Version = 2
	second, e := mutate(update, audit)
	if e != nil || second.Version != 2 || second.Rules[0].Version != 2 || first.Rules[0].Version != 1 || second.Hash == first.Hash {
		t.Fatal(second, e)
	}
	stale := update
	stale.OperationID = "stale"
	if _, e = mutate(stale, audit); !errors.Is(e, simulationdetection.ErrConflict) {
		t.Fatal(e)
	}
	rollback := clone(update)
	rollback.ExpectedVersion = 2
	rollback.Rules[0].Version = 3
	rollback.OperationID = "rollback"
	rollback.Name = "Must not persist"
	if _, e = mutate(rollback, func(context.Context, pgx.Tx, simulationdetection.Audit) error { return errors.New("audit failed") }); e == nil {
		t.Fatal("audit failure accepted")
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	current, e := simulationdetection.DetailTx(ctx, tx, scope, "template")
	if e != nil || current.Version != 2 {
		t.Fatal(current, e)
	}
	other := scope
	other.Domain = "other"
	if _, e = simulationdetection.DetailTx(ctx, tx, other, "template"); !errors.Is(e, simulationdetection.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = simulationdetection.ReceiptTx(ctx, tx, scope, "rollback"); !errors.Is(e, simulationdetection.ErrNotFound) {
		t.Fatal(e)
	}
	tx.Rollback(ctx)
	archived, e := mutate(simulationdetection.Mutation{ID: "template", OperationID: "archive", ExpectedVersion: 2, Archive: true}, audit)
	if e != nil || !archived.Archived || archived.Hash != second.Hash {
		t.Fatal(archived, e)
	}
	var versions, audits int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_detection_versions`).Scan(&versions); e != nil || versions != 2 {
		t.Fatal(versions, e)
	}
	if e = db.QueryRow(ctx, `SELECT count(*) FROM adtr.synthetic_audit`).Scan(&audits); e != nil || audits != 3 {
		t.Fatal(audits, e)
	}
	if _, e = db.Exec(ctx, `UPDATE adtr.simulation_detection_versions SET hash=repeat('0',64)`); e == nil {
		t.Fatal("immutable version changed")
	}
	if _, e = db.Exec(ctx, `TRUNCATE adtr.simulation_detection_receipts`); e == nil {
		t.Fatal("immutable receipt truncated")
	}
}
