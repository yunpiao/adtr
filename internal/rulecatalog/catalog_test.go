package rulecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"math"
	"reflect"
	"testing"
)

var scope = detectionrules.Scope{Tenant: "synthetic", Domain: "example.invalid"}

func rule(version uint64) *detectionrules.Rule {
	return &detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "r1", Version: version, Scope: scope, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.code", Operator: "eq", Values: []string{"4625"}}}}
}
func cases() []TestCase {
	return []TestCase{
		{Event: detectionrules.Event{Provenance: detectionrules.Provenance, ID: "positive", Scope: scope, Fields: map[string]string{"event.code": "4625"}}, WantMatch: true},
		{Event: detectionrules.Event{Provenance: detectionrules.Provenance, ID: "negative", Scope: scope, Fields: map[string]string{"event.code": "4624"}}, WantMatch: false},
	}
}
func execute(t *testing.T, in Request) Response {
	t.Helper()
	out, err := Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func initial(t *testing.T) Snapshot {
	return execute(t, Request{Scope: scope, Action: "init"}).Snapshot
}
func change(t *testing.T, s Snapshot, action string, extra func(*Request)) Snapshot {
	t.Helper()
	in := Request{Scope: scope, Snapshot: &s, Action: action, ID: "r1", ExpectedRevision: s.Revision}
	if extra != nil {
		extra(&in)
	}
	return execute(t, in).Snapshot
}
func created(t *testing.T) Snapshot {
	return change(t, initial(t), "create", func(r *Request) { r.Rule = rule(1) })
}
func tested(t *testing.T) Snapshot {
	return change(t, created(t), "test", func(r *Request) { r.Cases = cases() })
}
func published(t *testing.T) Snapshot {
	return change(t, tested(t), "publish-simulation", func(r *Request) { r.Reviewer = "synthetic-reviewer" })
}
func TestLifecycle(t *testing.T) {
	s := created(t)
	if s.Revision != 2 || s.Entries[0].Draft == nil || s.Entries[0].ActiveVersion != 0 {
		t.Fatal(s)
	}
	s = change(t, s, "test", func(r *Request) { r.Cases = cases() })
	if !s.Entries[0].Test.Passed {
		t.Fatal("actual fixtures failed")
	}
	s = change(t, s, "publish-simulation", func(r *Request) { r.Reviewer = "synthetic-reviewer" })
	if s.Entries[0].Draft != nil || len(s.Entries[0].Published) != 1 || s.Entries[0].ActiveVersion != 0 {
		t.Fatal("publish implicitly activated")
	}
	s = change(t, s, "enable", nil)
	if s.Entries[0].ActiveVersion != 1 {
		t.Fatal("not enabled")
	}
	oldPublished := s.Entries[0].Published[0]
	s = change(t, s, "edit", func(r *Request) { r.Rule = rule(2) })
	if s.Entries[0].ActiveVersion != 1 || !reflect.DeepEqual(oldPublished, s.Entries[0].Published[0]) {
		t.Fatal("edited active version")
	}
	s = change(t, s, "test", func(r *Request) { r.Cases = cases() })
	s = change(t, s, "edit", func(r *Request) { r.Rule = rule(3) })
	if s.Entries[0].Test != nil {
		t.Fatal("test survived edit")
	}
	s = change(t, s, "test", func(r *Request) { r.Cases = cases() })
	s = change(t, s, "publish-simulation", func(r *Request) { r.Reviewer = "synthetic-reviewer" })
	if s.Entries[0].ActiveVersion != 1 {
		t.Fatal("implicit version switch")
	}
	s = change(t, s, "enable", nil)
	if s.Entries[0].ActiveVersion != 3 {
		t.Fatal("latest not enabled")
	}
	s = change(t, s, "disable", nil)
	if s.Entries[0].ActiveVersion != 0 {
		t.Fatal("not disabled")
	}
	s = change(t, s, "archive", nil)
	if !s.Entries[0].Archived || len(s.Entries[0].Published) != 2 {
		t.Fatal("history lost")
	}
	for _, action := range []string{"enable", "disable", "archive", "edit", "test", "publish-simulation"} {
		r := Request{Scope: scope, Snapshot: &s, Action: action, ID: "r1", ExpectedRevision: s.Revision}
		if action == "edit" {
			r.Rule = rule(4)
		}
		if action == "test" {
			r.Cases = cases()
		}
		if action == "publish-simulation" {
			r.Reviewer = "reviewer"
		}
		if _, err := Execute(context.Background(), r); err == nil {
			t.Fatalf("archived %s allowed", action)
		}
	}
}
func TestFailedTestCannotPublish(t *testing.T) {
	s := created(t)
	bad := cases()
	bad[0].Event.Fields["event.code"] = "4624"
	s = change(t, s, "test", func(r *Request) { r.Cases = bad })
	if s.Entries[0].Test.Passed {
		t.Fatal("false test passed")
	}
	_, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "publish-simulation", ID: "r1", ExpectedRevision: s.Revision, Reviewer: "reviewer"})
	if !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
}
func TestQueriesExportAndOwnership(t *testing.T) {
	s := published(t)
	before, _ := json.Marshal(s)
	for _, action := range []string{"list", "detail", "export"} {
		in := Request{Scope: scope, Snapshot: &s, Action: action}
		if action != "list" {
			in.ID = "r1"
		} else {
			in.Query = "r"
		}
		out := execute(t, in)
		if out.Snapshot.Revision != s.Revision {
			t.Fatal("read mutated revision")
		}
		if action == "list" && len(out.Entries) != 1 {
			t.Fatal("list")
		}
		if action == "detail" && out.Entry.ID != "r1" {
			t.Fatal("detail")
		}
		if action == "export" && len(out.Exported) != 1 {
			t.Fatal("export")
		}
		out.Snapshot.Entries[0].Published[0].Rule.Conditions[0].Values[0] = "tampered output"
		after, _ := json.Marshal(s)
		if string(before) != string(after) {
			t.Fatal("output alias")
		}
	}
	out := execute(t, Request{Scope: scope, Snapshot: &s, Action: "list", Query: "missing"})
	if len(out.Entries) != 0 {
		t.Fatal("filter")
	}
	for _, action := range []string{"detail", "export", "enable"} {
		_, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: action, ID: "absent", ExpectedRevision: func() uint64 {
			if action == "enable" {
				return s.Revision
			}
			return 0
		}()})
		if !errors.Is(err, ErrNotFound) {
			t.Fatal(action, err)
		}
	}
}
func TestTamperedSnapshotsRejected(t *testing.T) {
	mutations := map[string]func(*Snapshot){
		"scope":             func(s *Snapshot) { s.Scope.Domain = "other" },
		"contract":          func(s *Snapshot) { s.Contract = "production" },
		"provenance":        func(s *Snapshot) { s.Provenance = "real" },
		"revision":          func(s *Snapshot) { s.Revision = 0 },
		"digest":            func(s *Snapshot) { s.Entries[0].Published[0].Digest = "forged" },
		"test digest":       func(s *Snapshot) { s.Entries[0].Published[0].Test.Digest = "forged" },
		"test expected":     func(s *Snapshot) { s.Entries[0].Published[0].Test.Cases[0].Event.Fields["event.code"] = "4624" },
		"test result":       func(s *Snapshot) { s.Entries[0].Published[0].Test.Passed = false },
		"reviewer":          func(s *Snapshot) { s.Entries[0].Published[0].Reviewer = "" },
		"active version":    func(s *Snapshot) { s.Entries[0].ActiveVersion = 42 },
		"archived active":   func(s *Snapshot) { s.Entries[0].Archived = true; s.Entries[0].ActiveVersion = 1 },
		"enabled published": func(s *Snapshot) { s.Entries[0].Published[0].Rule.Enabled = true },
		"duplicate":         func(s *Snapshot) { s.Entries = append(s.Entries, s.Entries[0]) },
		"draft old":         func(s *Snapshot) { s.Entries[0].Draft = rule(1) },
		"orphan report":     func(s *Snapshot) { s.Entries[0].Test = &s.Entries[0].Published[0].Test },
		"missing versions":  func(s *Snapshot) { s.Entries[0].Published = nil },
		"too many entries":  func(s *Snapshot) { s.Entries = make([]Entry, MaxEntries+1) },
		"too many versions": func(s *Snapshot) { s.Entries[0].Published = make([]Published, MaxPublished+1) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s := published(t)
			mutate(&s)
			if _, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "list"}); err == nil {
				t.Fatal("tampered snapshot accepted")
			}
		})
	}
}
func TestRejectedMutationsAreAtomic(t *testing.T) {
	s := created(t)
	before, _ := json.Marshal(s)
	tests := []Request{
		{Action: "edit", Rule: rule(1)}, {Action: "enable"}, {Action: "publish-simulation", Reviewer: "reviewer"},
		{Action: "create", Rule: rule(1)}, {Action: "test", Cases: cases()[:1]},
		{Action: "test", Cases: []TestCase{cases()[0], cases()[0]}},
		{Action: "edit", Rule: rule(2), Reviewer: "unexpected"},
		{Action: "disable", Cases: cases()},
		{Action: "unknown"}, {Action: "disable", Query: "unexpected"},
	}
	for _, r := range tests {
		r.Scope = scope
		r.Snapshot = &s
		r.ID = "r1"
		r.ExpectedRevision = s.Revision
		if _, err := Execute(context.Background(), r); err == nil {
			t.Fatal("accepted", r.Action)
		}
		after, _ := json.Marshal(s)
		if string(before) != string(after) {
			t.Fatal("mutated failed input")
		}
	}
	_, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "disable", ID: "r1", ExpectedRevision: s.Revision - 1})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	s.Revision = math.MaxUint64
	_, err = Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "disable", ID: "r1", ExpectedRevision: s.Revision})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestBoundaryInputs(t *testing.T) {
	for _, in := range []Request{{Scope: scope, Action: "init", Snapshot: &Snapshot{}}, {Scope: scope, Action: "list"}, {Scope: scope, Action: "init", ID: "x"}, {Scope: detectionrules.Scope{}, Action: "init"}, {Scope: scope, Action: "init", Rule: rule(1)}} {
		if _, err := Execute(context.Background(), in); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if _, err := Execute(nil, Request{Scope: scope, Action: "init"}); err == nil {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Execute(ctx, Request{Scope: scope, Action: "init"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s := created(t)
	for _, mutate := range []func([]TestCase){func(c []TestCase) { c[1].WantMatch = true }, func(c []TestCase) { c[0].Event.Scope.Tenant = "other" }, func(c []TestCase) { c[1].Event.ID = c[0].Event.ID }} {
		c := cases()
		mutate(c)
		if _, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "test", ID: "r1", ExpectedRevision: s.Revision, Cases: c}); err == nil {
			t.Fatal("invalid test accepted")
		}
	}
	r := rule(2)
	r.Enabled = true
	if _, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "edit", ID: "r1", ExpectedRevision: s.Revision, Rule: r}); err == nil {
		t.Fatal("enabled draft accepted")
	}
}

func TestVersionCapacityAndDraftReportTampering(t *testing.T) {
	s := published(t)
	for version := uint64(2); version <= MaxPublished; version++ {
		v := version
		s = change(t, s, "edit", func(r *Request) { r.Rule = rule(v) })
		s = change(t, s, "test", func(r *Request) { r.Cases = cases() })
		s = change(t, s, "publish-simulation", func(r *Request) { r.Reviewer = "simulated-reviewer" })
	}
	s = change(t, s, "edit", func(r *Request) { r.Rule = rule(MaxPublished + 1) })
	s = change(t, s, "test", func(r *Request) { r.Cases = cases() })
	if _, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "publish-simulation", ID: "r1", Reviewer: "simulated-reviewer", ExpectedRevision: s.Revision}); !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
	s.Entries[0].Test.Digest = "old"
	if _, err := Execute(context.Background(), Request{Scope: scope, Snapshot: &s, Action: "list"}); err == nil {
		t.Fatal("invalid draft report")
	}
}
func TestInputRuleAndFixtureOwnership(t *testing.T) {
	s := initial(t)
	r := rule(1)
	out := execute(t, Request{Scope: scope, Snapshot: &s, Action: "create", ID: "r1", Rule: r, ExpectedRevision: s.Revision})
	r.Conditions[0].Values[0] = "modified"
	if out.Snapshot.Entries[0].Draft.Conditions[0].Values[0] != "4625" {
		t.Fatal("rule input alias")
	}
	s = out.Snapshot
	c := cases()
	out = execute(t, Request{Scope: scope, Snapshot: &s, Action: "test", ID: "r1", Cases: c, ExpectedRevision: s.Revision})
	c[0].Event.Fields["event.code"] = "modified"
	if out.Snapshot.Entries[0].Test.Cases[0].Event.Fields["event.code"] != "4625" {
		t.Fatal("fixture input alias")
	}
}
