package simulationrisk

import (
	"errors"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"strings"
	"testing"
	"time"
)

func TestSnapshotResetValidationAndLiveBoundary(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	s := Scope{Tenant: "tenant", Domain: "domain", Read: true}
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "synthetic", Synthetic: true}
	d := simulationevents.Dataset{ID: "dataset", Fixture: p.Fixture, FixtureHash: strings.Repeat("a", 64), Contract: p.Contract, Simulation: true, EventCount: 1}
	ev := []securityevents.Event{{ID: "event", Tenant: s.Tenant, Domain: s.Domain, Actor: "actor", Kind: "login_failure", OccurredAt: at, Provenance: p}}
	reset := SnapshotReset{Revision: 2, ID: "reset:" + digest([]string{"reviewer", "op"}), Actor: "reviewer", OperationID: "op", Reason: "synthetic", At: at}
	r, e := EvaluateSnapshot(s, d, "actor", ev, []SnapshotReset{reset}, at)
	if e != nil || r.Score != 0 || r.Revision != 2 || r.ResetBoundary == nil {
		t.Fatal(r, e)
	}
	if _, e = EvaluateSnapshot(s, d, "actor", ev, []SnapshotReset{reset}, at.Add(-time.Second)); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	reset.Revision = 3
	if _, e = EvaluateSnapshot(s, d, "actor", ev, []SnapshotReset{reset}, at); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	reset.Revision = 2
	reset.ID = "fake"
	if _, e = EvaluateSnapshot(s, d, "actor", ev, []SnapshotReset{reset}, at); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
