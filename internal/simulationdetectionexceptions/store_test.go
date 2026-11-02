package simulationdetectionexceptions

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/detectionexceptions"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/tasks"
	"strings"
	"testing"
	"time"
)

func sampleScope() Scope {
	return Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true, DetectionRead: true, EventsRead: true}
}
func sampleEntry(id string) detectionexceptions.Exception {
	return detectionexceptions.Exception{ID: id, Scope: coreScope(sampleScope()), Global: true, Reason: "SIMULATED approved configuration", NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
}
func TestTransitionLifecycleAndSafetyReserve(t *testing.T) {
	s := sampleScope()
	r := empty(s)
	d := Declaration{ID: "x", Global: true}
	e := sampleEntry("x")
	r, err := transition(s, r, Mutation{Action: "create", ExpectedRevision: 1, Declaration: &d}, []detectionexceptions.Exception{e})
	if err != nil || r.Snapshot.Entries[0].Enabled {
		t.Fatal(r, err)
	}
	r, err = transition(s, r, Mutation{Action: "enable", ID: "x", ExpectedRevision: 2}, nil)
	if err != nil || !r.Snapshot.Entries[0].Enabled {
		t.Fatal(r, err)
	}
	r.Snapshot.Revision = MaxRevisions - 2
	if _, err = transition(s, r, Mutation{Action: "edit", ExpectedRevision: r.Snapshot.Revision, Declaration: &d}, []detectionexceptions.Exception{e}); err != nil {
		t.Fatal("edit disables, leaving archive slot", err)
	}
	if _, err = transition(s, r, Mutation{Action: "create", ExpectedRevision: r.Snapshot.Revision, Declaration: &Declaration{ID: "y", Global: true}}, []detectionexceptions.Exception{sampleEntry("y")}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	r, err = transition(s, r, Mutation{Action: "disable", ID: "x", ExpectedRevision: r.Snapshot.Revision}, nil)
	if err != nil || r.Snapshot.Revision != 255 {
		t.Fatal(r, err)
	}
	if _, err = transition(s, r, Mutation{Action: "disable", ID: "x", ExpectedRevision: r.Snapshot.Revision}, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("noop spent reserve", err)
	}
	r, err = transition(s, r, Mutation{Action: "archive", ID: "x", ExpectedRevision: r.Snapshot.Revision}, nil)
	if err != nil || r.Snapshot.Revision != 256 || !r.Snapshot.Entries[0].Archived {
		t.Fatal(r, err)
	}
}
func TestImportAtomicInputsAndUnsupportedConditions(t *testing.T) {
	s := sampleScope()
	old := empty(s)
	e := sampleEntry("x")
	decl := Declaration{ID: "x", Global: true}
	if _, err := transition(s, old, Mutation{Action: "import", ExpectedRevision: 1, Entries: []Declaration{decl, decl}}, []detectionexceptions.Exception{e, e}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if old.Snapshot.Revision != 1 || len(old.Snapshot.Entries) != 0 {
		t.Fatal("input mutated")
	}
	for _, field := range []string{"event.code", "object.kind", "unknown"} {
		if conditions([]detectionrules.Condition{{Field: field, Operator: "eq"}}) == nil {
			t.Fatal(field)
		}
	}
	if conditions([]detectionrules.Condition{{Field: "actor.name", Operator: "regex"}}) == nil {
		t.Fatal("regex accepted")
	}
	if _, err := transition(s, old, Mutation{Action: "create", ExpectedRevision: 2, Declaration: &decl}, []detectionexceptions.Exception{e}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestCoreAnnotationsPreserveOriginalAndTimeBoundaries(t *testing.T) {
	s := sampleScope()
	rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: coreScope(s), Enabled: true, Combine: "all", Conditions: sampleEntry("x").Conditions}
	p, err := detectionrules.Compile(coreScope(s), []detectionrules.Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	events := []detectionrules.Event{{ID: "positive", Scope: coreScope(s), Provenance: detectionrules.Provenance, Fields: map[string]string{"event.outcome": "failure"}}, {ID: "negative", Scope: coreScope(s), Provenance: detectionrules.Provenance, Fields: map[string]string{"event.outcome": "success"}}}
	matches, err := p.Evaluate(context.Background(), events)
	if err != nil || len(matches) != 1 {
		t.Fatal(matches, err)
	}
	snap, _ := detectionexceptions.New(coreScope(s))
	entry := sampleEntry("x")
	snap, err = detectionexceptions.Add(access(s), snap, 1, entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.Enabled = true
	snap, err = detectionexceptions.Edit(access(s), snap, 2, entry)
	if err != nil {
		t.Fatal(err)
	}
	original := digest(matches)
	for _, tt := range []struct {
		at time.Time
		n  int
	}{{entry.NotBefore.Add(-time.Nanosecond), 0}, {entry.NotBefore, 1}, {entry.ExpiresAt.Add(-time.Nanosecond), 1}, {entry.ExpiresAt, 0}} {
		out, e := detectionexceptions.Evaluate(context.Background(), access(s), snap, events, matches, tt.at)
		if e != nil || len(out) != 1 || len(out[0].Exceptions) != tt.n || digest(out[0].Match) != digest(matches[0]) {
			t.Fatal(out, e)
		}
	}
	if digest(matches) != original {
		t.Fatal("rewritten matches")
	}
	entry.Global = false
	entry.RuleID = "rule"
	entry.RuleDigest = strings.Repeat("a", 64)
	snap, _ = detectionexceptions.Edit(access(s), snap, 3, entry)
	out, e := detectionexceptions.Evaluate(context.Background(), access(s), snap, events, matches, entry.NotBefore)
	if e != nil || len(out[0].Exceptions) != 0 {
		t.Fatal(out, e)
	}
}
func TestStrictDecodeAndJSONBPreflight(t *testing.T) {
	var in EvaluationInput
	for _, raw := range []string{`{"id":"x","matches":[]}`, `{"id":"x"} {}`, `null trailing`} {
		if strict([]byte(raw), 1024, &in) == nil {
			t.Fatal(raw)
		}
	}
	v := map[string][]string{"x": strings.Fields(strings.Repeat("a ", 100))}
	b, _ := json.Marshal(v)
	if _, err := marshalBounded(v, len(b)); !errors.Is(err, ErrLimit) {
		t.Fatal("JSONB spacing not budgeted", err)
	}
	if _, err := marshalBounded(v, len(b)+110); err != nil {
		t.Fatal(err)
	}
	r := empty(sampleScope())
	if e := validateRecord(sampleScope(), r); e != nil {
		t.Fatal(e)
	}
	r.Contract = "tampered"
	if validateRecord(sampleScope(), r) == nil {
		t.Fatal("hash accepted")
	}
}
func TestNoSourceAndNilTxDenied(t *testing.T) {
	s := sampleScope()
	if _, _, e := source(context.Background(), nil, s, nil, "x"); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := SnapshotTx(context.Background(), nil, s); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
}
func TestSourceVerdictsPreserveInfrastructureErrors(t *testing.T) {
	if !errors.Is(sourceError(&tasks.Error{Status: 404, Code: "not_found"}), ErrNotFound) {
		t.Fatal("404 not mapped")
	}
	if !errors.Is(sourceError(&tasks.Error{Status: 403, Code: "denied"}), ErrDenied) {
		t.Fatal("403 not mapped")
	}
	if sourceError(context.DeadlineExceeded) != context.DeadlineExceeded {
		t.Fatal("deadline reclassified")
	}
	db := errors.New("database unavailable")
	if sourceError(db) != db {
		t.Fatal("database reclassified")
	}
}
