//go:build integration

package simulationrisk_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationrisk"
)

func TestPostgresRiskLedger(t *testing.T) {
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
	if _, err = db.Exec(ctx, "CREATE SCHEMA adtr;"+simulationevents.SchemaSQL+simulationevents.ImmutableSchemaSQL+simulationrisk.SchemaSQL+simulationrisk.ImmutableSchemaSQL+`CREATE TABLE adtr.synthetic_audit(operation_id text PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	scope := securityevents.Scope{Tenant: "synthetic-tenant", Domain: "synthetic-domain", Read: true, Manage: true}
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "synthetic-pg", Synthetic: true}
	fixture := simulationevents.Fixture{ID: "dataset", Provenance: p, Events: []securityevents.RawEvent{{ID: "event", Tenant: scope.Tenant, Domain: scope.Domain, Kind: "login_failure", Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: p}}, Cases: []simulationevents.FixtureCase{{ID: "case", EvidenceIDs: []string{"event"}}}}
	zero := fixture.Events[0]
	zero.ID = "zero-risk"
	zero.Actor = "zero-risk-subject"
	zero.Kind = "login_success"
	future := fixture.Events[0]
	future.ID = "future"
	future.OccurredAt = "2099-01-01T00:00:00Z"
	fixture.Events = append(fixture.Events, zero, future)
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
	riskScope := simulationrisk.Scope{Tenant: scope.Tenant, Domain: scope.Domain, Read: true, Manage: true}
	audit := func(ctx context.Context, tx pgx.Tx, r simulationrisk.Receipt) error {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.synthetic_audit VALUES($1)`, r.OperationID)
		return e
	}
	input := simulationrisk.ResetInput{DatasetID: "dataset", Subject: "synthetic-user", Actor: "authenticated-actor", OperationID: "reset", Reason: "illustrative reset", ExpectedRevision: 1}
	var initial simulationrisk.Result
	if err = transact(db, func(tx pgx.Tx) error {
		var e error
		initial, e = simulationrisk.EvaluateTx(ctx, tx, riskScope, "dataset", "synthetic-user")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if initial.Score != 5 || initial.IgnoredFuture != 1 || initial.Revision != 1 || initial.Dataset.FixtureHash == "" {
		t.Fatal(initial)
	}
	failure := errors.New("audit unavailable")
	if err = transact(db, func(tx pgx.Tx) error {
		_, e := simulationrisk.ResetTx(ctx, tx, riskScope, input, func(ctx context.Context, tx pgx.Tx, r simulationrisk.Receipt) error {
			if e := audit(ctx, tx, r); e != nil {
				return e
			}
			return failure
		})
		return e
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	for _, table := range []string{"simulation_risk_resets", "simulation_risk_receipts", "synthetic_audit"} {
		var n int
		if err = db.QueryRow(ctx, "SELECT count(*) FROM adtr."+table).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial reset", table, n, err)
		}
	}
	// Different operations from the same expected revision serialize by subject.
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
			in := input
			in.OperationID = op
			results <- transact(conn, func(tx pgx.Tx) error { _, e := simulationrisk.ResetTx(ctx, tx, riskScope, in, audit); return e })
		}(op)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, simulationrisk.ErrConflict) {
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
	input.OperationID = winner
	var saved simulationrisk.Receipt
	if err = transact(db, func(tx pgx.Tx) error {
		var e error
		saved, e = simulationrisk.OperationTx(ctx, tx, riskScope, "dataset", input.Actor, winner)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if saved.Result.Score != 0 || saved.Result.Revision != 2 || saved.Result.ResetBoundary == nil {
		t.Fatal(saved)
	}
	// Later revision cannot change the original receipt or original boundary.
	next := input
	next.OperationID = "next"
	next.ExpectedRevision = 2
	if err = transact(db, func(tx pgx.Tx) error { _, e := simulationrisk.ResetTx(ctx, tx, riskScope, next, audit); return e }); err != nil {
		t.Fatal(err)
	}
	reopened, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if err = transact(reopened, func(tx pgx.Tx) error {
		retry, e := simulationrisk.ResetTx(ctx, tx, riskScope, input, audit)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(saved, retry) {
			t.Fatal("receipt changed after reset/reconnect")
		}
		r, e := simulationrisk.EvaluateTx(ctx, tx, riskScope, "dataset", input.Subject)
		if e == nil && (r.Revision != 3 || r.Score != 0 || len(r.Records) != 3) {
			t.Fatal(r)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	// Concurrent exact retries share one receipt and write one additional audit.
	same := input
	same.OperationID = "same-id"
	same.ExpectedRevision = 3
	receipts := make(chan simulationrisk.Receipt, 2)
	sameErrors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, e := pgx.ConnectConfig(ctx, cfg)
			if e != nil {
				sameErrors <- e
				return
			}
			defer conn.Close(context.Background())
			var r simulationrisk.Receipt
			e = transact(conn, func(tx pgx.Tx) error {
				var err error
				r, err = simulationrisk.ResetTx(ctx, tx, riskScope, same, audit)
				return err
			})
			sameErrors <- e
			receipts <- r
		}()
	}
	wg.Wait()
	close(sameErrors)
	close(receipts)
	for e := range sameErrors {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first *simulationrisk.Receipt
	for r := range receipts {
		if first == nil {
			v := r
			first = &v
		} else if !reflect.DeepEqual(*first, r) {
			t.Fatal("concurrent replay differs")
		}
	}
	if err = transact(db, func(tx pgx.Tx) error {
		r, e := simulationrisk.EvaluateTx(ctx, tx, riskScope, "dataset", "zero-risk-subject")
		if e == nil && (r.Score != 0 || r.NoRiskEventCount != 1) {
			t.Fatal(r)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = transact(db, func(tx pgx.Tx) error {
		_, e := simulationrisk.EvaluateTx(ctx, tx, riskScope, "dataset", "unknown-subject")
		return e
	}); !errors.Is(err, simulationrisk.ErrNotFound) {
		t.Fatal(err)
	}
	denied := riskScope
	denied.Manage = false
	if err = transact(db, func(tx pgx.Tx) error { _, e := simulationrisk.ResetTx(ctx, tx, denied, same, audit); return e }); !errors.Is(err, simulationrisk.ErrDenied) {
		t.Fatal(err)
	}

	changed := input
	changed.Reason = "changed"
	if err = transact(db, func(tx pgx.Tx) error { _, e := simulationrisk.ResetTx(ctx, tx, riskScope, changed, audit); return e }); !errors.Is(err, simulationrisk.ErrConflict) {
		t.Fatal(err)
	}
	for _, other := range []simulationrisk.Scope{{Tenant: "other", Domain: scope.Domain, Read: true}, {Tenant: scope.Tenant, Domain: "other", Read: true}} {
		if err = transact(db, func(tx pgx.Tx) error {
			_, e := simulationrisk.EvaluateTx(ctx, tx, other, "dataset", input.Subject)
			return e
		}); !errors.Is(err, simulationrisk.ErrNotFound) {
			t.Fatal("scope leak", err)
		}
	}
	if err = transact(db, func(tx pgx.Tx) error {
		_, e := simulationrisk.OperationTx(ctx, tx, riskScope, "dataset", "other-actor", winner)
		return e
	}); !errors.Is(err, simulationrisk.ErrNotFound) {
		t.Fatal("receipt actor leak", err)
	}
	for _, sql := range []string{`UPDATE adtr.simulation_risk_resets SET reason='changed'`, `DELETE FROM adtr.simulation_risk_resets`, `TRUNCATE adtr.simulation_risk_receipts`, `UPDATE adtr.simulation_risk_receipts SET receipt='{}'`} {
		if err = transact(db, func(tx pgx.Tx) error { _, e := tx.Exec(ctx, sql); return e }); err == nil {
			t.Fatal("immutable write accepted", sql)
		}
	}
	var n int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM adtr.synthetic_audit`).Scan(&n); err != nil || n != 3 {
		t.Fatal("duplicate audit", n, err)
	}
}
