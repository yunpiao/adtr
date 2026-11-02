//go:build integration

package simulationlabels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/sensitivelabels"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresLabelsLedger(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: missing PostgreSQL is a failure, not a skip")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal("invalid PostgreSQL test config")
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
	name := "adtr_labels_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+quoted); e != nil {
		t.Fatal(e)
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
	db, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close(context.Background())
	if _, e = db.Exec(ctx, "CREATE SCHEMA adtr;"+SchemaSQL+ImmutableSchemaSQL+`CREATE TABLE adtr.synthetic_audit(operation_id text PRIMARY KEY);`); e != nil {
		t.Fatal(e)
	}
	transact := func(c *pgx.Conn, fn func(pgx.Tx) error) error {
		tx, e := c.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		if e = fn(tx); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	s := testScope()
	fixture := testFixture()
	audit := func(ctx context.Context, tx pgx.Tx, r Receipt) error {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.synthetic_audit VALUES($1)`, r.OperationID)
		return e
	}
	must := func(fn func(pgx.Tx) error) {
		t.Helper()
		if e := transact(db, fn); e != nil {
			t.Fatal(e)
		}
	}
	must(func(tx pgx.Tx) error { _, e := SeedFixtureTx(ctx, tx, s, fixture); return e })
	if e = transact(db, func(tx pgx.Tx) error { _, e := SeedFixtureTx(ctx, tx, s, fixture); return e }); !errors.Is(e, ErrConflict) {
		t.Fatalf("duplicate fixture: %v", e)
	}
	t.Run("Identifier128SeedPolicyDetailAnd129Rejection", func(t *testing.T) {
		must := func(fn func(pgx.Tx) error) {
			t.Helper()
			if err := transact(db, fn); err != nil {
				t.Fatal(err)
			}
		}
		// The HTTP module freezes identifiers at128 bytes. Prove a maximal ID can
		// be seeded, used by a rule, evaluated and read back via the detail service.
		longFixture := longIdentifierFixture(128)
		must(func(tx pgx.Tx) error { _, e := SeedFixtureTx(ctx, tx, s, longFixture); return e })
		overlong := longIdentifierFixture(129)
		overlong.ID = "overlong-rejected"
		if e = transact(db, func(tx pgx.Tx) error { _, e := SeedFixtureTx(ctx, tx, s, overlong); return e }); !errors.Is(e, ErrInvalid) {
			t.Fatalf("129-byte fixture accepted: %v", e)
		}
		longGroup := "g" + strings.Repeat("x", 127)
		longUser := "u" + strings.Repeat("x", 127)
		longPolicy := PolicyInput{DatasetID: longFixture.ID, PolicyID: "long-policy", Action: "create", Actor: "actor", OperationID: "long-create", Reason: "128-byte synthetic references", Spec: PolicySpec{Name: "Boundary", SensitiveGroupIDs: []string{longGroup}, ManualLabels: []sensitivelabels.ManualLabel{{ObjectID: longUser, Label: "custom"}}}}
		must(func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, longPolicy, audit); return e })
		badPolicy := longPolicy
		badPolicy.PolicyID = "bad-policy"
		badPolicy.OperationID = "bad-create"
		badPolicy.Spec.SensitiveGroupIDs = []string{longGroup + "x"}
		if e = transact(db, func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, badPolicy, audit); return e }); !errors.Is(e, ErrInvalid) {
			t.Fatalf("129-byte policy accepted: %v", e)
		}
		var longEvaluationID string
		must(func(tx pgx.Tx) error {
			r, e := EvaluateTx(ctx, tx, s, EvaluateInput{DatasetID: longFixture.ID, PolicyID: longPolicy.PolicyID, ObservationID: "o1", ExpectedRevision: 1, Actor: "actor", OperationID: "long-evaluate", Reason: "detail compatibility"}, audit)
			if e == nil {
				longEvaluationID = r.Evaluation.ID
			}
			return e
		})
		must(func(tx pgx.Tx) error {
			a, e := AssetTx(ctx, tx, s, longFixture.ID, longEvaluationID, longUser)
			if e == nil && (a.ObjectID != longUser || len(a.Labels) != 2) {
				return errors.New("128-byte detail mismatch")
			}
			return e
		})
		if e = transact(db, func(tx pgx.Tx) error {
			_, e := AssetTx(ctx, tx, s, longFixture.ID, longEvaluationID, longUser+"x")
			return e
		}); !errors.Is(e, ErrInvalid) {
			t.Fatalf("129-byte detail accepted: %v", e)
		}
	})
	create := PolicyInput{DatasetID: fixture.ID, PolicyID: "policy", Action: "create", Actor: "actor", OperationID: "create", Reason: "synthetic test", Spec: PolicySpec{Name: "Policy", SensitiveGroupIDs: []string{"g"}}}
	var original Receipt
	must(func(tx pgx.Tx) error { var e error; original, e = MutatePolicyTx(ctx, tx, s, create, audit); return e })
	must(func(tx pgx.Tx) error {
		r, e := MutatePolicyTx(ctx, tx, s, create, audit)
		if e == nil && !equal(original, r) {
			return errors.New("replay changed")
		}
		return e
	})
	badCreate := create
	badCreate.Spec.Name = "Changed"
	if e = transact(db, func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, badCreate, audit); return e }); !errors.Is(e, ErrConflict) {
		t.Fatalf("op conflict: %v", e)
	}
	evaluations := []Evaluation{}
	for _, o := range fixture.Observations {
		in := EvaluateInput{DatasetID: fixture.ID, PolicyID: "policy", ObservationID: o.ID, ExpectedRevision: 1, Actor: "actor", OperationID: "eval-" + o.ID, Reason: "synthetic evaluate"}
		must(func(tx pgx.Tx) error {
			r, e := EvaluateTx(ctx, tx, s, in, audit)
			if e == nil {
				evaluations = append(evaluations, *r.Evaluation)
			}
			return e
		})
	}
	cmp := CompareInput{DatasetID: fixture.ID, BeforeID: evaluations[0].ID, AfterID: evaluations[1].ID, Actor: "actor", OperationID: "compare", Reason: "synthetic comparison"}
	var comparison Comparison
	must(func(tx pgx.Tx) error {
		r, e := CompareTx(ctx, tx, s, cmp, audit)
		if e == nil {
			comparison = *r.Comparison
			if len(comparison.Alerts) != 2 {
				return errors.New("expected two persisted alerts")
			}
		}
		return e
	})
	cmp.OperationID = "compare-again"
	must(func(tx pgx.Tx) error {
		r, e := CompareTx(ctx, tx, s, cmp, audit)
		if e == nil && !equal(*r.Comparison, comparison) {
			return errors.New("pair dedup changed comparison")
		}
		return e
	})
	cmp.OperationID = "compare-incomplete"
	cmp.AfterID = evaluations[2].ID
	if e = transact(db, func(tx pgx.Tx) error { _, e := CompareTx(ctx, tx, s, cmp, audit); return e }); !errors.Is(e, ErrIncomplete) {
		t.Fatalf("incomplete: %v", e)
	}
	must(func(tx pgx.Tx) error {
		a, e := AssetTx(ctx, tx, s, fixture.ID, evaluations[0].ID, "u")
		if e != nil {
			return e
		}
		if a.IsHoneypot != nil || a.HoneypotAvailability != "unavailable" || len(a.Labels) != 1 || a.Labels[0].Distance != 2 {
			return errors.New("asset evidence incorrect")
		}
		p, e := AssetsTx(ctx, tx, s, fixture.ID, evaluations[0].ID, AssetFilter{Kind: "computer", Name: "Example", Limit: 10})
		if e == nil && (p.Count != 1 || len(p.Items) != 1 || p.Items[0].State != "unlabelled_within_snapshot") {
			return errors.New("filter wrong")
		}
		return e
	})
	t.Run("OversizedPayloadsLeaveNoWrites", func(t *testing.T) {
		must := func(fn func(pgx.Tx) error) {
			t.Helper()
			if err := transact(db, fn); err != nil {
				t.Fatal(err)
			}
		}
		// Legal escaped identifiers can multiply serialized provenance size. Reject
		// the oversized comparison BEFORE any business/audit/receipt write.
		escaped, escapedSpec := escapedComparisonFixture()
		must(func(tx pgx.Tx) error { _, e := SeedFixtureTx(ctx, tx, s, escaped); return e })
		escapedPolicy := PolicyInput{DatasetID: escaped.ID, PolicyID: "escaped-policy", Action: "create", Actor: "actor", OperationID: "escaped-create", Reason: "legal escaped input", Spec: escapedSpec}
		must(func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, escapedPolicy, audit); return e })
		escapedIDs := []string{}
		for _, o := range escaped.Observations {
			must(func(tx pgx.Tx) error {
				r, e := EvaluateTx(ctx, tx, s, EvaluateInput{DatasetID: escaped.ID, PolicyID: escapedPolicy.PolicyID, ObservationID: o.ID, ExpectedRevision: 1, Actor: "actor", OperationID: "escaped-eval-" + o.ID, Reason: "legal escaped evaluation"}, audit)
				if e == nil {
					escapedIDs = append(escapedIDs, r.Evaluation.ID)
				}
				return e
			})
		}
		oversizedCompare := CompareInput{DatasetID: escaped.ID, BeforeID: escapedIDs[0], AfterID: escapedIDs[1], Actor: "actor", OperationID: "oversized-compare", Reason: "bounded comparison"}
		if e = transact(db, func(tx pgx.Tx) error { _, e := CompareTx(ctx, tx, s, oversizedCompare, audit); return e }); !errors.Is(e, ErrLimit) {
			t.Fatalf("oversized comparison: %v", e)
		}
		var forbiddenRows int
		if e = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM adtr.simulation_label_comparisons WHERE dataset_id=$1)+(SELECT count(*) FROM adtr.simulation_label_receipts WHERE operation_id=$2)+(SELECT count(*) FROM adtr.synthetic_audit WHERE operation_id=$2)`, escaped.ID, oversizedCompare.OperationID).Scan(&forbiddenRows); e != nil || forbiddenRows != 0 {
			t.Fatalf("oversized comparison wrote rows=%d: %v", forbiddenRows, e)
		}
		oversizedFixture := oversizedObservationFixture()
		if e = transact(db, func(tx pgx.Tx) error { _, e := SeedFixtureTx(ctx, tx, s, oversizedFixture); return e }); !errors.Is(e, ErrLimit) {
			t.Fatalf("oversized observation: %v", e)
		}
		if e = db.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_datasets WHERE dataset_id=$1`, oversizedFixture.ID).Scan(&forbiddenRows); e != nil || forbiddenRows != 0 {
			t.Fatal("oversized seed left dataset")
		}
		oversizedPolicy := escapedPolicy
		oversizedPolicy.PolicyID = "oversized-policy"
		oversizedPolicy.OperationID = "oversized-policy-op"
		oversizedPolicy.Spec = PolicySpec{Name: "Oversized", ManualLabels: []sensitivelabels.ManualLabel{}}
		for i := 0; i < 256; i++ {
			oversizedPolicy.Spec.ManualLabels = append(oversizedPolicy.Spec.ManualLabels, sensitivelabels.ManualLabel{ObjectID: escaped.Observations[0].Snapshot.Nodes[0].ID, Label: strings.Repeat("<", 252) + fmt.Sprintf("%04d", i)})
		}
		if e = transact(db, func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, oversizedPolicy, audit); return e }); !errors.Is(e, ErrLimit) {
			t.Fatalf("oversized request: %v", e)
		}
		if e = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM adtr.simulation_label_policies WHERE policy_id=$1)+(SELECT count(*) FROM adtr.simulation_label_receipts WHERE operation_id=$2)+(SELECT count(*) FROM adtr.synthetic_audit WHERE operation_id=$2)`, oversizedPolicy.PolicyID, oversizedPolicy.OperationID).Scan(&forbiddenRows); e != nil || forbiddenRows != 0 {
			t.Fatal("oversized policy wrote rows")
		}
	})
	t.Run("ScopedCompositeFKRejectsTampering", func(t *testing.T) {
		for _, stmt := range []string{
			`INSERT INTO adtr.simulation_label_evaluations SELECT tenant_id,'foreign-domain',dataset_id,'tampered-scope',policy_id,revision,policy_hash,observation_id,observation_hash,payload FROM adtr.simulation_label_evaluations WHERE evaluation_id=$1`,
			`INSERT INTO adtr.simulation_label_evaluations SELECT tenant_id,domain_id,dataset_id,'tampered-policy-hash',policy_id,revision,repeat('0',64),observation_id,observation_hash,payload FROM adtr.simulation_label_evaluations WHERE evaluation_id=$1`,
			`INSERT INTO adtr.simulation_label_evaluations SELECT tenant_id,domain_id,dataset_id,'tampered-observation-hash',policy_id,revision,policy_hash,observation_id,repeat('0',64),payload FROM adtr.simulation_label_evaluations WHERE evaluation_id=$1`,
		} {
			err := transact(db, func(tx pgx.Tx) error { _, e := tx.Exec(ctx, stmt, evaluations[0].ID); return e })
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
				t.Fatalf("expected scoped FK violation23503, got %v", err)
			}
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_evaluations WHERE evaluation_id LIKE 'tampered-%'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("FK tamper persisted rows=%d error=%v", n, err)
		}
	})
	t.Run("ConcurrentCreateAtFixedPolicyQuota", func(t *testing.T) {
		must := func(fn func(pgx.Tx) error) {
			t.Helper()
			if err := transact(db, fn); err != nil {
				t.Fatal(err)
			}
		}
		quotaFixture := testFixture()
		quotaFixture.ID = "quota-fixture"
		must(func(tx pgx.Tx) error { _, e := SeedFixtureTx(ctx, tx, s, quotaFixture); return e })
		for i := 0; i < MaxPolicies-1; i++ {
			id := fmt.Sprintf("quota-%03d", i)
			in := PolicyInput{DatasetID: quotaFixture.ID, PolicyID: id, Action: "create", Actor: "actor", OperationID: id, Reason: "fixed quota setup", Spec: PolicySpec{Name: id, SensitiveGroupIDs: []string{"g"}}}
			must(func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, in, audit); return e })
		}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				conn, err := pgx.ConnectConfig(ctx, cfg)
				if err != nil {
					results <- err
					return
				}
				defer conn.Close(context.Background())
				id := fmt.Sprintf("quota-race-%d", i)
				in := PolicyInput{DatasetID: quotaFixture.ID, PolicyID: id, Action: "create", Actor: "actor", OperationID: id, Reason: "quota race", Spec: PolicySpec{Name: id, SensitiveGroupIDs: []string{"g"}}}
				results <- transact(conn, func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, in, audit); return e })
			}(i)
		}
		wg.Wait()
		close(results)
		wins, limits := 0, 0
		for err := range results {
			if err == nil {
				wins++
			} else if errors.Is(err, ErrLimit) {
				limits++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || limits != 1 {
			t.Fatalf("fixed quota winners=%d rejections=%d", wins, limits)
		}
		var policies, versions, receipts, audits int
		if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM adtr.simulation_label_policies WHERE dataset_id=$1),(SELECT count(*) FROM adtr.simulation_label_policy_versions WHERE dataset_id=$1),(SELECT count(*) FROM adtr.simulation_label_receipts WHERE dataset_id=$1),(SELECT count(*) FROM adtr.synthetic_audit WHERE operation_id LIKE 'quota-%')`, quotaFixture.ID).Scan(&policies, &versions, &receipts, &audits); err != nil {
			t.Fatal(err)
		}
		if policies != MaxPolicies || versions != MaxPolicies || receipts != MaxPolicies || audits != MaxPolicies {
			t.Fatalf("quota atomicity policies=%d versions=%d receipts=%d audits=%d", policies, versions, receipts, audits)
		}
	})
	// Two independent connections contend for one CAS revision; exactly one wins.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, e := pgx.ConnectConfig(ctx, cfg)
			if e != nil {
				results <- e
				return
			}
			defer conn.Close(context.Background())
			in := create
			in.Action = "update"
			in.ExpectedRevision = 1
			in.OperationID = []string{"update-a", "update-b"}[i]
			in.Spec.Name = in.OperationID
			results <- transact(conn, func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, in, audit); return e })
		}(i)
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for e := range results {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("CAS wins=%d conflicts=%d", wins, conflicts)
	}
	// Audit failure must roll back the policy pointer, version and receipt.
	fail := create
	fail.Action = "update"
	fail.ExpectedRevision = 2
	fail.OperationID = "audit-failure"
	if e = transact(db, func(tx pgx.Tx) error {
		_, e := MutatePolicyTx(ctx, tx, s, fail, func(context.Context, pgx.Tx, Receipt) error { return errors.New("expected audit failure") })
		return e
	}); e == nil {
		t.Fatal("audit error accepted")
	}
	must(func(tx pgx.Tx) error {
		p, e := PolicyTx(ctx, tx, s, fixture.ID, "policy")
		if e == nil && p.Revision != 2 {
			return errors.New("audit failure committed")
		}
		return e
	})
	for i, action := range []string{"archive", "restore"} {
		in := PolicyInput{DatasetID: fixture.ID, PolicyID: "policy", Action: action, ExpectedRevision: uint64(2 + i), Actor: "actor", OperationID: action, Reason: "synthetic lifecycle"}
		must(func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, s, in, audit); return e })
	}
	must(func(tx pgx.Tx) error {
		r, e := OperationTx(ctx, tx, s, fixture.ID, "actor", "create")
		if e == nil && !equal(r, original) {
			return errors.New("old receipt not historical")
		}
		if e != nil {
			return e
		}
		h, e := HistoryTx(ctx, tx, s, fixture.ID, "policy", 0, 10)
		if e == nil && (h.Count != 4 || h.Items[2].Status != "archived" || h.Items[3].Status != "active") {
			return errors.New("wrong version history")
		}
		return e
	})
	for _, mutate := range []func(*Scope){func(s *Scope) { s.Domain = "other" }, func(s *Scope) { s.Tenant = "other" }} {
		other := s
		mutate(&other)
		if e = transact(db, func(tx pgx.Tx) error { _, e := EvaluationTx(ctx, tx, other, fixture.ID, evaluations[0].ID); return e }); !errors.Is(e, ErrNotFound) {
			t.Fatalf("scope isolation: %v", e)
		}
	}
	denied := s
	denied.Manage = false
	if e = transact(db, func(tx pgx.Tx) error { _, e := MutatePolicyTx(ctx, tx, denied, create, audit); return e }); !errors.Is(e, ErrDenied) {
		t.Fatalf("denial: %v", e)
	}
	if e = transact(db, func(tx pgx.Tx) error { _, e := OperationTx(ctx, tx, s, fixture.ID, "other-actor", "create"); return e }); !errors.Is(e, ErrNotFound) {
		t.Fatalf("actor isolation: %v", e)
	}
	// Every immutable relation rejects direct UPDATE/DELETE/TRUNCATE even when no
	// application path is involved. Each failed statement owns a fresh transaction.
	for _, table := range []string{"simulation_label_datasets", "simulation_label_observations", "simulation_label_policy_versions", "simulation_label_evaluations", "simulation_label_comparisons", "simulation_label_receipts"} {
		for _, sql := range []string{"UPDATE adtr." + table + " SET payload=payload", "DELETE FROM adtr." + table, "TRUNCATE adtr." + table + " CASCADE"} {
			if e = transact(db, func(tx pgx.Tx) error { _, e := tx.Exec(ctx, sql); return e }); e == nil {
				t.Fatalf("immutable bypass: %s", table)
			}
		}
	}
	// Close/reopen demonstrates durable reads rather than Go object memory.
	if e = db.Close(ctx); e != nil {
		t.Fatal(e)
	}
	db, e = pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close(context.Background())
	must(func(tx pgx.Tx) error {
		c, e := ComparisonTx(ctx, tx, s, fixture.ID, comparison.ID)
		if e == nil && !equal(c, comparison) {
			return errors.New("restart lost comparison")
		}
		return e
	})
	var count int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_comparisons`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("dedup count %d, error %v", count, e)
	}
}
