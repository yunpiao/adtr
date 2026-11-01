package simulationevents

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
	"strings"
	"testing"
	"time"
)

// Any unexpected SQL call panics through the nil embedded interface.
type noSQL struct{ pgx.Tx }

var scope = securityevents.Scope{Tenant: "synthetic-tenant", Domain: "synthetic-domain", Read: true, Manage: true}

func TestInputValidationBeforeSQL(t *testing.T) {
	ctx := context.Background()
	tx := noSQL{}
	for _, s := range []securityevents.Scope{{}, {Tenant: scope.Tenant, Domain: scope.Domain}, {Tenant: "bad\n", Domain: scope.Domain, Read: true}} {
		if _, err := ListDatasetsTx(ctx, tx, s, 0, 10); !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
	}
	for _, p := range [][2]int{{-1, 1}, {0, 0}, {0, 101}, {4097, 1}} {
		if _, err := ListDatasetsTx(ctx, tx, scope, p[0], p[1]); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := QueryTx(ctx, tx, scope, "dataset", securityevents.Filter{Limit: 101}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := DetailTx(ctx, tx, scope, "dataset", "case", 0, 101); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := TransitionTx(ctx, tx, scope, TransitionInput{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	denied := scope
	denied.Manage = false
	if _, err := TransitionTx(ctx, tx, denied, TransitionInput{}, nil); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := OperationTx(ctx, tx, scope, "dataset", "actor", "bad\n"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestReceiptRequestHashIdentity(t *testing.T) {
	a := TransitionInput{DatasetID: "dataset", CaseID: "case", OperationID: "op", Change: securityevents.Change{Actor: "trusted-actor", Action: "close", Reason: "synthetic", ExpectedRevision: 1}}
	h, err := requestHash(a)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Change.At = time.Now()
	h2, _ := requestHash(b)
	if h != h2 {
		t.Fatal("server timestamp changed retry identity")
	}
	changes := []func(*TransitionInput){func(x *TransitionInput) { x.DatasetID = "other" }, func(x *TransitionInput) { x.CaseID = "other" }, func(x *TransitionInput) { x.OperationID = "other" }, func(x *TransitionInput) { x.Change.Actor = "other" }, func(x *TransitionInput) { x.Change.Action = "unmark" }, func(x *TransitionInput) { x.Change.Reason = "other" }, func(x *TransitionInput) { x.Change.ExpectedRevision = 2 }}
	for _, change := range changes {
		b := a
		change(&b)
		got, _ := requestHash(b)
		if got == h {
			t.Fatal("request field excluded")
		}
	}
}
func TestFixtureRejectsBeforeSQL(t *testing.T) {
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "synthetic", Synthetic: true}
	base := Fixture{ID: "dataset", Provenance: p, Events: []securityevents.RawEvent{{ID: "e", Tenant: scope.Tenant, Domain: scope.Domain, Kind: "login_failure", Actor: "a", Device: "d", Session: "s", SourceIP: "192.0.2.1", OccurredAt: "2026-01-01T00:00:00Z", Provenance: p}}}
	for _, cases := range [][]FixtureCase{{{ID: "c"}}, {{ID: "c", EvidenceIDs: []string{"missing"}}}, {{ID: "c", EvidenceIDs: []string{"e", "e"}}}, {{ID: "c", EvidenceIDs: []string{"e"}}, {ID: "c", EvidenceIDs: []string{"e"}}}} {
		f := base
		f.Cases = cases
		if _, err := SeedFixtureTx(context.Background(), noSQL{}, scope, f); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	f := base
	f.Provenance.Synthetic = false
	if _, err := SeedFixtureTx(context.Background(), noSQL{}, scope, f); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestSchemaProvenanceAndImmutabilityContract(t *testing.T) {
	for _, table := range []string{"simulation_event_datasets", "simulation_events", "simulation_event_cases", "simulation_event_case_evidence", "simulation_event_transitions", "simulation_event_receipts"} {
		if !strings.Contains(SchemaSQL, "CREATE TABLE adtr."+table+" (") {
			t.Fatal(table)
		}
	}
	if strings.Count(SchemaSQL, "FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash") != 6 {
		t.Fatal("missing provenance foreign key")
	}
	for _, text := range []string{"CHECK(simulation)", "adtr.securityevents.simulated.v1", "CHECK(revision BETWEEN 1 AND 1025)", "NEW.revision<>OLD.revision+1", "PRIMARY KEY(tenant_id,domain_id,dataset_id,actor,operation_id)"} {
		if !strings.Contains(SchemaSQL, text) {
			t.Fatal(text)
		}
	}
	if !strings.Contains(ImmutableSchemaSQL, "BEFORE UPDATE OR DELETE") || !strings.Contains(ImmutableSchemaSQL, "BEFORE TRUNCATE") {
		t.Fatal("mutable fixture/history")
	}
}

func TestStoredAuditValidation(t *testing.T) {
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "synthetic", Synthetic: true}
	c, err := securityevents.NewCase(scope, p, "case")
	if err != nil {
		t.Fatal(err)
	}
	_, a, err := c.Apply(scope, securityevents.Change{Action: "mark_suspicious", Actor: "actor", Reason: "synthetic", At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	readOnly := scope
	readOnly.Manage = false
	if err = validateAudit(readOnly, p, a); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*securityevents.Audit){func(a *securityevents.Audit) { a.PreviousState = "closed" }, func(a *securityevents.Audit) { a.Action = "real" }, func(a *securityevents.Audit) { a.Revision = 0 }, func(a *securityevents.Audit) { a.Revision = MaxHistory + 2 }, func(a *securityevents.Audit) { a.Provenance.Synthetic = false }, func(a *securityevents.Audit) { a.Mark = "benign" }, func(a *securityevents.Audit) { a.Actor = "" }, func(a *securityevents.Audit) { a.At = time.Time{} }} {
		bad := a
		change(&bad)
		if err = validateAudit(scope, p, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}
