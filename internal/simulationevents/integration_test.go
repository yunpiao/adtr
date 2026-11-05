//go:build integration

package simulationevents_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

func TestPostgresAtomicSyntheticState(t *testing.T) {
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
	if _, err = db.Exec(ctx, "CREATE SCHEMA adtr;"+simulationevents.SchemaSQL+simulationevents.ImmutableSchemaSQL+`CREATE TABLE adtr.synthetic_audit(operation_id text PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	scope := securityevents.Scope{Tenant: "synthetic-tenant", Domain: "synthetic-domain", Read: true, Manage: true}
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "synthetic-pg", Synthetic: true}
	fixture := simulationevents.Fixture{ID: "dataset", Provenance: p, Events: []securityevents.RawEvent{{ID: "event", Tenant: scope.Tenant, Domain: scope.Domain, Kind: "login_failure", Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: p}}, Cases: []simulationevents.FixtureCase{{ID: "case", EvidenceIDs: []string{"event"}}}}
	transact := func(conn *pgx.Conn, fn func(pgx.Tx) error) error {
		tx, e := conn.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		if e = fn(tx); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if err = transact(db, func(tx pgx.Tx) error { _, e := simulationevents.SeedFixtureTx(ctx, tx, scope, fixture); return e }); err != nil {
		t.Fatal(err)
	}
	audit := func(ctx context.Context, tx pgx.Tx, r simulationevents.Receipt) error {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.synthetic_audit VALUES($1)`, r.OperationID)
		return e
	}
	in := simulationevents.TransitionInput{DatasetID: "dataset", CaseID: "case", OperationID: "op", Change: securityevents.Change{Actor: "trusted-actor", Action: "mark_suspicious", Reason: "synthetic check", ExpectedRevision: 1}}
	failure := errors.New("synthetic audit failure")
	if err = transact(db, func(tx pgx.Tx) error {
		_, e := simulationevents.TransitionTx(ctx, tx, scope, in, func(ctx context.Context, tx pgx.Tx, r simulationevents.Receipt) error {
			if e := audit(ctx, tx, r); e != nil {
				return e
			}
			return failure
		})
		return e
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err = transact(db, func(tx pgx.Tx) error {
		d, e := simulationevents.DetailTx(ctx, tx, scope, "dataset", "case", 0, 10)
		if e == nil && (d.Case.Revision != 1 || d.HistoryTotal != 0) {
			t.Fatal("partial failed mutation", d)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM adtr.synthetic_audit`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial audit", count, err)
	}
	// Two real connections race from revision one; exactly one may commit.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, op := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			conn, e := pgx.ConnectConfig(ctx, cfg)
			if e != nil {
				results <- e
				return
			}
			defer conn.Close(context.Background())
			input := in
			input.OperationID = op
			results <- transact(conn, func(tx pgx.Tx) error { _, e := simulationevents.TransitionTx(ctx, tx, scope, input, audit); return e })
		}(op)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, simulationevents.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	var winner string
	if err = db.QueryRow(ctx, `SELECT operation_id FROM adtr.synthetic_audit`).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	in.OperationID = winner
	// Reopen a connection to verify durable recovery, not in-memory state.
	reopened, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if err = transact(reopened, func(tx pgx.Tx) error {
		r, e := simulationevents.OperationTx(ctx, tx, scope, "dataset", in.Change.Actor, winner)
		if e != nil {
			return e
		}
		retry, e := simulationevents.TransitionTx(ctx, tx, scope, in, audit)
		if e != nil {
			return e
		}
		if r != retry || r.Case.Revision != 2 || !r.Simulation {
			t.Fatal("receipt replay mismatch")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.Change.Reason = "changed"
	if err = transact(db, func(tx pgx.Tx) error { _, e := simulationevents.TransitionTx(ctx, tx, scope, changed, audit); return e }); !errors.Is(err, simulationevents.ErrConflict) {
		t.Fatal(err)
	}
	if err = transact(db, func(tx pgx.Tx) error {
		d, e := simulationevents.DetailTx(ctx, tx, scope, "dataset", "case", 0, 1)
		if e != nil {
			return e
		}
		if d.HistoryTotal != 1 || len(d.History) != 1 || len(d.Evidence) != 1 {
			t.Fatal(d)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, other := range []securityevents.Scope{{Tenant: "other", Domain: scope.Domain, Read: true}, {Tenant: scope.Tenant, Domain: "other", Read: true}} {
		if err = transact(db, func(tx pgx.Tx) error {
			_, e := simulationevents.DetailTx(ctx, tx, other, "dataset", "case", 0, 1)
			return e
		}); !errors.Is(err, simulationevents.ErrNotFound) {
			t.Fatal("scope leak", err)
		}
	}
	for _, sql := range []string{`UPDATE adtr.simulation_event_datasets SET fixture='changed'`, `DELETE FROM adtr.simulation_events`, `TRUNCATE adtr.simulation_event_transitions`, `UPDATE adtr.simulation_event_receipts SET receipt='{}'`, `UPDATE adtr.simulation_event_cases SET revision=20`} {
		if err = transact(db, func(tx pgx.Tx) error { _, e := tx.Exec(ctx, sql); return e }); err == nil {
			t.Fatal("immutable write accepted", sql)
		}
	}
}
