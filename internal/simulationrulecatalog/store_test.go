package simulationrulecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"strings"
	"testing"
	"time"
)

func sampleDeclaration() *Declaration {
	return &Declaration{Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
}
func fixtures() []Fixture {
	return []Fixture{{ID: "positive", Fields: map[string]string{"event.outcome": "failure"}, WantMatch: true}, {ID: "negative", Fields: map[string]string{"event.outcome": "success"}, WantMatch: false}}
}
func TestTransitionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Read: true, Manage: true}
	at := time.Unix(100, 0).UTC()
	mutate := func(r Record, action string, decl *Declaration, cases []Fixture) Record {
		t.Helper()
		in := Mutation{ID: "rule", Action: action, OperationID: action, ExpectedRevision: r.Revision, Declaration: decl, Cases: cases}
		n, _, e := transition(ctx, s, in, r, at)
		if e != nil {
			t.Fatal(action, e)
		}
		return n
	}
	r := mutate(Record{}, "create", sampleDeclaration(), nil)
	if r.Revision != 2 || r.LastEditorID != 1 || r.ActiveVersion != 0 {
		t.Fatal(r.Summary)
	}
	r = mutate(r, "test", nil, fixtures())
	if r.TestPassed == nil || !*r.TestPassed || r.TestedBy != 1 {
		t.Fatal(r)
	}
	s.Publish = true
	if _, _, e := transition(ctx, s, Mutation{ID: "rule", Action: "publish", ExpectedRevision: r.Revision}, r, at); !errors.Is(e, ErrDenied) {
		t.Fatal("self review", e)
	}
	s.ActorID = 2
	s.Manage = false
	published, pub, e := transition(ctx, s, Mutation{ID: "rule", Action: "publish", ExpectedRevision: r.Revision}, r, at)
	if e != nil || pub == nil || pub.ReviewedBy != 2 || pub.TestedBy != 1 || pub.Rule.Rule.Enabled {
		t.Fatal(pub, e)
	}
	r = published
	before, _ := json.Marshal(r.Snapshot.Entries[0].Published)
	s.ActorID = 1
	s.Manage = true
	r = mutate(r, "enable", nil, nil)
	if r.ActiveVersion != 1 {
		t.Fatal(r)
	}
	r = mutate(r, "edit", sampleDeclaration(), nil)
	if r.ActiveVersion != 1 || r.DraftVersion != 2 || r.TestPassed != nil || r.TestRevision != 0 {
		t.Fatal(r)
	}
	after, _ := json.Marshal(r.Snapshot.Entries[0].Published)
	if string(before) != string(after) {
		t.Fatal("published bytes changed")
	}
	r = mutate(r, "archive", nil, nil)
	if !r.Archived || r.ActiveVersion != 0 {
		t.Fatal(r)
	}
	if _, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "disable", ExpectedRevision: r.Revision}, r, at); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
}
func TestFailedTestAndCancellation(t *testing.T) {
	s := Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Publish: true}
	ctx := context.Background()
	r, _, e := transition(ctx, s, Mutation{ID: "rule", Action: "create", Declaration: sampleDeclaration()}, Record{}, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	cases := fixtures()
	cases[0].Fields["event.outcome"] = "success"
	r, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "test", Cases: cases, ExpectedRevision: r.Revision}, r, time.Time{})
	if e != nil || r.TestPassed == nil || *r.TestPassed {
		t.Fatal(e)
	}
	s.ActorID = 2
	if _, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "publish", ExpectedRevision: r.Revision}, r, time.Time{}); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, e = transition(cancelled, s, Mutation{ID: "rule", Action: "disable", ExpectedRevision: r.Revision}, r, time.Time{}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestBoundaryAndDefaultDeny(t *testing.T) {
	if _, e := MutateTx(context.Background(), nil, Scope{}, Mutation{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := CopyTemplateTx(context.Background(), nil, Scope{}, CopyInput{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if validMutation(Mutation{ID: "rule", Action: "create", OperationID: "op", Declaration: sampleDeclaration(), ExpectedRevision: 1}) {
		t.Fatal("create revision")
	}
	if validMutation(Mutation{ID: "rule", Action: "test", OperationID: "op", ExpectedRevision: 2, Cases: make([]Fixture, MaxCases+1)}) {
		t.Fatal("case budget")
	}
	if validName(" x") || validName("x\n") {
		t.Fatal("name")
	}
	v := map[string]any{"a": []string{"x:y,z", `escaped\"`}, "b": true}
	b, _ := json.Marshal(v)
	if _, e := marshalBounded(v, len(b)); !errors.Is(e, ErrLimit) {
		t.Fatal("jsonb separator budget", e)
	}
	if _, e := marshalBounded(v, len(b)+10); e != nil {
		t.Fatal(e)
	}
	if _, e := marshalBounded(strings.Repeat("x", MaxRecordBytes), MaxRecordBytes); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}
func TestCopyMappingCapabilities(t *testing.T) {
	r := detectionrules.Rule{Conditions: []detectionrules.Condition{{Field: "event.code"}, {Field: "object.kind"}, {Field: "event.code"}, {Field: "event.outcome"}}}
	got := unsupportedCopyFields(r)
	if len(got) != 2 || got[0] != "event.code" || got[1] != "object.kind" {
		t.Fatal(got)
	}
	r.Conditions = []detectionrules.Condition{{Field: "event.category"}, {Field: "event.outcome"}, {Field: "actor.name"}, {Field: "target.name"}, {Field: "source.ip"}}
	if len(unsupportedCopyFields(r)) != 0 {
		t.Fatal("available fields rejected")
	}
	s := Scope{Tenant: "tenant", Domain: "domain", ActorID: 1}
	ctx := context.Background()
	declaration := sampleDeclaration()
	declaration.Conditions[0].Field = "event.code"
	record, _, e := transition(ctx, s, Mutation{ID: "rule", Action: "create", Declaration: declaration}, Record{}, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	cases := fixtures()
	for i := range cases {
		cases[i].Fields = map[string]string{"event.code": cases[i].Fields["event.outcome"]}
	}
	record, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "test", ExpectedRevision: record.Revision, Cases: cases}, record, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	s.ActorID = 2
	s.Publish = true
	record, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "publish", ExpectedRevision: record.Revision}, record, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	record, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "enable", ExpectedRevision: record.Revision}, record, time.Time{})
	if e != nil || record.CopySupported || len(record.CopyUnsupportedFields) != 1 || record.CopyUnsupportedFields[0] != "event.code" {
		t.Fatal(record.Summary, e)
	}
}
func TestMissingFieldNeverSatisfiesNegativeComparison(t *testing.T) {
	s := Scope{Tenant: "tenant", Domain: "domain", ActorID: 1}
	ctx := context.Background()
	decl := sampleDeclaration()
	decl.Conditions[0].Operator = "neq"
	decl.Conditions[0].Values = []string{"success"}
	r, _, e := transition(ctx, s, Mutation{ID: "rule", Action: "create", Declaration: decl}, Record{}, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	cases := []Fixture{{ID: "positive", Fields: map[string]string{"event.outcome": "failure"}, WantMatch: true}, {ID: "missing", Fields: map[string]string{"actor.name": "synthetic"}, WantMatch: false}}
	r, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "test", ExpectedRevision: r.Revision, Cases: cases}, r, time.Time{})
	if e != nil || r.TestPassed == nil || !*r.TestPassed || len(r.CaseResults) != 2 || !r.CaseResults[0].ActualMatch || r.CaseResults[1].ActualMatch {
		t.Fatal(r.CaseResults, e)
	}
}
func TestRevisionExhaustionReservesDisableAndArchive(t *testing.T) {
	ctx := context.Background()
	s := Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, Publish: true}
	r, _, e := transition(ctx, s, Mutation{ID: "rule", Action: "create", Declaration: sampleDeclaration()}, Record{}, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	r, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "test", ExpectedRevision: r.Revision, Cases: fixtures()}, r, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	s.ActorID = 2
	r, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "publish", ExpectedRevision: r.Revision}, r, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	for r.Revision < MaxRevisions-2 {
		r, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "enable", ExpectedRevision: r.Revision}, r, time.Time{})
		if e != nil {
			t.Fatal(e)
		}
	}
	if r.ActiveVersion != 1 || r.Revision != 62 {
		t.Fatal(r.Summary)
	}
	for _, action := range []string{"enable", "edit", "test", "publish"} {
		if _, _, e = transition(ctx, s, Mutation{ID: "rule", Action: action, ExpectedRevision: r.Revision}, r, time.Time{}); !errors.Is(e, ErrLimit) {
			t.Fatal(action, e)
		}
	}
	r, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "disable", ExpectedRevision: r.Revision}, r, time.Time{})
	if e != nil || r.ActiveVersion != 0 || r.Revision != 63 || r.CopySupported {
		t.Fatal(r.Summary, e)
	}
	if _, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "disable", ExpectedRevision: r.Revision}, r, time.Time{}); !errors.Is(e, ErrTransition) {
		t.Fatal("repeat disable consumes reserve", e)
	}
	r, _, e = transition(ctx, s, Mutation{ID: "rule", Action: "archive", ExpectedRevision: r.Revision}, r, time.Time{})
	if e != nil || !r.Archived || r.Revision != 64 {
		t.Fatal(r.Summary, e)
	}
}
func TestSemanticIntentBindsAllInputFields(t *testing.T) {
	base := Intent{DomainID: "domain", ID: "rule", Action: "test", OperationID: "op", ExpectedRevision: 3, Cases: fixtures()}
	hash := digest(base)
	changed := base
	changed.ExpectedRevision++
	if digest(changed) == hash {
		t.Fatal("revision not bound")
	}
	changed = base
	changed.Cases = fixtures()
	changed.Cases[0].WantMatch = false
	if digest(changed) == hash {
		t.Fatal("case expectation not bound")
	}
	changed = base
	changed.Cases = fixtures()
	changed.Cases[0].Fields["event.outcome"] = "changed"
	if digest(changed) == hash {
		t.Fatal("fixture fields not bound")
	}
	copy := Intent{DomainID: "domain", ID: "rule", Action: "copy-template", OperationID: "copy", ExpectedRevision: 5, PublishedVersion: 1, PublishedDigest: strings.Repeat("a", 64), TemplateID: "target", Name: "name", Acknowledgement: CopyAcknowledgement}
	original := digest(copy)
	for _, change := range []func(*Intent){func(i *Intent) { i.PublishedVersion++ }, func(i *Intent) { i.PublishedDigest = strings.Repeat("b", 64) }, func(i *Intent) { i.TemplateID = "other" }, func(i *Intent) { i.Name = "changed" }, func(i *Intent) { i.Acknowledgement = "wrong" }} {
		v := copy
		change(&v)
		if digest(v) == original {
			t.Fatal("copy field not bound")
		}
	}
}
