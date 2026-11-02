package detectionexceptions

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (Access, Snapshot, Exception, Event, Match, time.Time) {
	t.Helper()
	scope := Scope{Tenant: "synthetic-tenant", Domain: "synthetic.invalid"}
	s, err := New(scope)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	event := Event{Provenance: Provenance, ID: "event-1", Scope: scope, Fields: map[string]string{"actor.name": "synthetic-user", "event.code": "4624"}}
	p, err := detectionrules.Compile(scope, []detectionrules.Rule{{Contract: detectionrules.ContractVersion, Provenance: Provenance, ID: "rule-1", Version: 1, Scope: scope, Enabled: true, Combine: "all", Conditions: []Condition{{Field: "event.code", Operator: "eq", Values: []string{"4624"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	matches, err := p.Evaluate(context.Background(), []Event{event})
	if err != nil {
		t.Fatal(err)
	}
	e := Exception{ID: "exception-1", Scope: scope, RuleID: matches[0].RuleID, RuleDigest: matches[0].RuleDigest, Reason: "synthetic approved fixture", NotBefore: now, ExpiresAt: now.Add(time.Hour), Conditions: []Condition{{Field: "actor.name", Operator: "eq", Values: []string{"synthetic-user"}}}}
	return Access{Scope: scope, Read: true, Write: true}, s, e, event, matches[0], now
}
func TestLifecycleAndIsolation(t *testing.T) {
	a, s, e, event, m, now := fixture(t)
	original, _ := json.Marshal(s)
	added, err := Add(a, s, s.Revision, e)
	if err != nil {
		t.Fatal(err)
	}
	if added.Revision != 2 || added.Entries[0].Enabled {
		t.Fatal(added)
	}
	e.Conditions[0].Values[0] = "mutated"
	if added.Entries[0].Conditions[0].Values[0] != "synthetic-user" {
		t.Fatal("add aliased input")
	}
	rows, err := List(a, added)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Conditions[0].Values[0] = "mutated"
	if added.Entries[0].Conditions[0].Values[0] != "synthetic-user" {
		t.Fatal("list aliased state")
	}
	run := func(s Snapshot, when time.Time, n int) {
		t.Helper()
		out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{m}, when)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || !reflect.DeepEqual(out[0].Match, m) || len(out[0].Exceptions) != n {
			t.Fatalf("unexpected result: %+v", out)
		}
	}
	run(added, now, 0)
	edit := added.Entries[0]
	edit.Enabled = true
	enabled, err := Edit(a, added, 2, edit)
	if err != nil {
		t.Fatal(err)
	}
	run(enabled, now, 1)
	run(enabled, now.Add(-time.Nanosecond), 0)
	run(enabled, now.Add(time.Hour), 0)
	archived, err := Archive(a, enabled, 3, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	run(archived, now, 0)
	if !archived.Entries[0].Archived || archived.Entries[0].Enabled {
		t.Fatal(archived)
	}
	after, _ := json.Marshal(s)
	if string(original) != string(after) {
		t.Fatal("input mutated")
	}
	one, _ := Export(a, enabled)
	two, _ := Export(a, enabled)
	if string(one) != string(two) {
		t.Fatal("nondeterministic export")
	}
}
func TestPreviewDisabledAndDeterministic(t *testing.T) {
	a, s, e, event, _, _ := fixture(t)
	draft, err := PreviewFromEvent(a, s, event, e.ID, e.Reason, e.RuleID, e.RuleDigest, e.NotBefore, e.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Enabled || draft.Global || draft.Archived || len(s.Entries) != 0 || draft.Conditions[0].Field != "actor.name" {
		t.Fatal(draft)
	}
	again, err := PreviewFromEvent(a, s, event, e.ID, e.Reason, e.RuleID, e.RuleDigest, e.NotBefore, e.ExpiresAt)
	if err != nil || !reflect.DeepEqual(draft, again) {
		t.Fatal("unstable preview")
	}
}
func TestAtomicImportConflictAndPermissions(t *testing.T) {
	a, s, e, _, _, _ := fixture(t)
	bad := e
	bad.ID = "bad"
	bad.Conditions = []Condition{{Field: "secret", Operator: "eq", Values: []string{"value"}}}
	if out, err := Import(a, s, 1, []Exception{e, bad}); err == nil || out.Revision != 0 || len(s.Entries) != 0 {
		t.Fatal("partial import")
	}
	if _, err := Import(a, s, 1, []Exception{e, e}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	added, err := Import(a, s, 1, []Exception{e})
	if err != nil || added.Revision != 2 {
		t.Fatal(err)
	}
	if _, err := Add(a, added, 1, e); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	denied := a
	denied.Write = false
	if _, err := Add(denied, s, 1, e); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	denied.Read = false
	if _, err := List(denied, s); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	if _, err := Export(denied, s); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	other := a
	other.Scope.Domain = "other.invalid"
	if _, err := List(other, s); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	if _, err := Edit(a, s, 1, e); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := Archive(a, s, 1, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func TestStrictValidation(t *testing.T) {
	a, s, e, _, _, _ := fixture(t)
	cases := []func(*Exception){func(e *Exception) { e.Enabled = true }, func(e *Exception) { e.Archived = true }, func(e *Exception) { e.Global = true }, func(e *Exception) { e.RuleDigest = "*" }, func(e *Exception) { e.Reason = "" }, func(e *Exception) { e.ExpiresAt = e.NotBefore }, func(e *Exception) { e.NotBefore = time.Time{} }, func(e *Exception) { e.Scope.Domain = "other.invalid" }, func(e *Exception) { e.Conditions = nil }, func(e *Exception) {
		e.Conditions = []Condition{{Field: "actor.name", Operator: "contains", Values: []string{"user"}}}
	}, func(e *Exception) {
		e.Conditions = []Condition{{Field: "actor.name", Operator: "eq", Values: []string{"*", "more"}}}
	}, func(e *Exception) {
		e.Conditions = []Condition{{Field: "actor.name", Operator: "eq", Values: []string{"\n"}}}
	}}
	for i, change := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			bad := e
			change(&bad)
			if _, err := Add(a, s, 1, bad); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
func TestEvaluationBindingScopeAndAtomicity(t *testing.T) {
	a, s, e, event, m, now := fixture(t)
	s, _ = Add(a, s, 1, e)
	e.Enabled = true
	s, _ = Edit(a, s, 2, e)
	other := m
	other.RuleDigest = strings.Repeat("a", 64)
	out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{other, m}, now)
	if err != nil || len(out[0].Exceptions) != 0 || len(out[1].Exceptions) != 1 {
		t.Fatal(out, err)
	}
	e.Global = true
	e.RuleID = ""
	e.RuleDigest = ""
	s, err = Edit(a, s, 3, e)
	if err != nil {
		t.Fatal(err)
	}
	out, err = Evaluate(context.Background(), a, s, []Event{event}, []Match{other, m}, now)
	if err != nil || len(out) != 2 || len(out[0].Exceptions) != 1 {
		t.Fatal(out, err)
	}
	bad := m
	bad.EventID = "missing"
	if out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{m, bad}, now); err == nil || out != nil {
		t.Fatal("partial results", out, err)
	}
	bad = m
	bad.Scope.Domain = "other.invalid"
	if out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{bad}, now); !errors.Is(err, ErrScope) || out != nil {
		t.Fatal(out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := Evaluate(ctx, a, s, []Event{event}, []Match{m}, now); !errors.Is(err, context.Canceled) || out != nil {
		t.Fatal(out, err)
	}
	event.Fields = map[string]string{}
	out, err = Evaluate(context.Background(), a, s, []Event{event}, []Match{m}, now)
	if err != nil || len(out[0].Exceptions) != 0 {
		t.Fatal("missing evidence matched", out, err)
	}
	if _, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{m}, time.Time{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestBoundsAndIn(t *testing.T) {
	a, s, e, event, m, now := fixture(t)
	e.Conditions[0].Operator = "in"
	e.Conditions[0].Values = []string{"another", "synthetic-user"}
	s, _ = Add(a, s, 1, e)
	e.Enabled = true
	s, _ = Edit(a, s, 2, e)
	out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{m}, now)
	if err != nil || len(out[0].Exceptions) != 1 {
		t.Fatal(out, err)
	}
	matches := make([]Match, detectionrules.MaxMatches+1)
	if out, err := Evaluate(context.Background(), a, s, nil, matches, now); !errors.Is(err, ErrLimit) || out != nil {
		t.Fatal(err)
	}
	entries := make([]Exception, MaxEntries+1)
	if _, err := Import(a, s, s.Revision, entries); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	second := e
	second.ID = "second"
	s.Entries = append(s.Entries, second)
	matches = make([]Match, MaxAnnotations/2+1)
	for i := range matches {
		matches[i] = m
	}
	if out, err := Evaluate(context.Background(), a, s, []Event{event}, matches, now); !errors.Is(err, ErrLimit) || out != nil {
		t.Fatal("annotation bound", err)
	}
}

func TestSnapshotValidationAndTerminalErrors(t *testing.T) {
	a, s, e, event, m, now := fixture(t)
	bad := s
	bad.Contract = "unknown"
	if _, err := List(a, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	bad = s
	bad.Revision = 0
	if _, err := List(a, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	bad = s
	bad.Entries = make([]Exception, MaxEntries+1)
	if _, err := List(a, bad); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	bad = s
	bad.Entries = []Exception{e, e}
	if _, err := List(a, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	bad = s
	bad.Revision = ^uint64(0)
	if _, err := Add(a, bad, bad.Revision, e); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if _, err := New(Scope{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Evaluate(nil, a, s, []Event{event}, []Match{m}, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	s, _ = Add(a, s, 1, e)
	s, _ = Archive(a, s, 2, e.ID)
	if _, err := Archive(a, s, 3, e.ID); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := Edit(a, s, 3, e); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	denied := a
	denied.Read = false
	if _, err := PreviewFromEvent(denied, s, event, e.ID, e.Reason, e.RuleID, e.RuleDigest, e.NotBefore, e.ExpiresAt); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	event.Fields["unknown"] = "bad"
	if out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{m}, now); err == nil || out != nil {
		t.Fatal("invalid event accepted")
	}
	if _, err := PreviewFromEvent(a, s, event, e.ID, e.Reason, e.RuleID, e.RuleDigest, e.NotBefore, e.ExpiresAt); err == nil {
		t.Fatal("invalid preview accepted")
	}
}

func TestArchivedEnabledSnapshotRejected(t *testing.T) {
	a, s, e, event, m, now := fixture(t)
	s, _ = Add(a, s, s.Revision, e)
	s, _ = Archive(a, s, s.Revision, e.ID)
	// Valid archived and disabled state remains readable/exportable/evaluable.
	if _, err := List(a, s); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(a, s); err != nil {
		t.Fatal(err)
	}
	out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{m}, now)
	if err != nil || len(out) != 1 || len(out[0].Exceptions) != 0 {
		t.Fatal(out, err)
	}
	s.Entries[0].Enabled = true
	if out, err := List(a, s); !errors.Is(err, ErrInvalid) || out != nil {
		t.Fatal(out, err)
	}
	if out, err := Export(a, s); !errors.Is(err, ErrInvalid) || out != nil {
		t.Fatal(out, err)
	}
	if out, err := Evaluate(context.Background(), a, s, []Event{event}, []Match{m}, now); !errors.Is(err, ErrInvalid) || out != nil {
		t.Fatal(out, err)
	}
}
