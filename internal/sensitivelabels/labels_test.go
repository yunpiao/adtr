package sensitivelabels

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/groupgraph"
)

func fixture() Input {
	scope := groupgraph.Scope{TenantID: "synthetic-tenant", DomainID: "synthetic-domain"}
	return Input{Version: ContractVersion, Simulated: true, Scope: scope, Complete: true, PolicyID: "synthetic-policy", MaxDepth: groupgraph.MaxDepth, MaxLabels: MaxLabels, SensitiveGroupIDs: []string{"a"}, ManualLabels: []ManualLabel{{"u", "review"}}, Snapshot: groupgraph.Snapshot{Version: groupgraph.ContractVersion, Simulated: true, Scope: scope, ObservationID: "synthetic-before", Nodes: []groupgraph.Node{{ID: "a", Kind: groupgraph.Group, MembersKnown: true}, {ID: "b", Kind: groupgraph.Group, MembersKnown: true}, {ID: "u", Kind: groupgraph.User}, {ID: "pc", Kind: groupgraph.Computer}, {ID: "other", Kind: groupgraph.User}}, Edges: []groupgraph.Edge{{GroupID: "a", MemberID: "b"}, {GroupID: "b", MemberID: "u"}, {GroupID: "a", MemberID: "pc"}, {GroupID: "b", MemberID: "a"}}}}
}
func evaluate(t *testing.T, in Input) Result {
	t.Helper()
	r, e := Evaluate(in)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func object(t *testing.T, r Result, id string) ObjectLabels {
	t.Helper()
	for _, o := range r.Objects {
		if o.ObjectID == id {
			return o
		}
	}
	t.Fatal("missing", id)
	return ObjectLabels{}
}
func TestLabelsAndProvenance(t *testing.T) {
	in := fixture()
	r := evaluate(t, in)
	if !r.CompleteWithinSnapshot || !r.Simulated || r.Scope != in.Scope || r.ObservationID != in.Snapshot.ObservationID {
		t.Fatal(r)
	}
	want := map[string][]Provenance{"a": {{SensitiveLabel, "sensitive_group", "a", 0}}, "b": {{SensitiveLabel, "direct_membership", "a", 1}}, "pc": {{SensitiveLabel, "direct_membership", "a", 1}}, "u": {{"review", "manual", "", 0}, {SensitiveLabel, "transitive_membership", "a", 2}}, "other": {}}
	for id, labels := range want {
		if !reflect.DeepEqual(object(t, r, id).Labels, labels) {
			t.Fatal(id, object(t, r, id))
		}
	}
	if object(t, r, "other").State != "unlabelled_within_snapshot" {
		t.Fatal(r)
	}
	// Manual provenance remains explicit; no group inheritance for manual labels.
	in.ManualLabels = []ManualLabel{{"a", "owner"}}
	r = evaluate(t, in)
	if len(object(t, r, "u").Labels) != 1 {
		t.Fatal(r)
	}
}
func TestUnknownNeverSafe(t *testing.T) {
	for _, edit := range []func(*Input){func(in *Input) { in.Complete = false }, func(in *Input) {
		in.Snapshot.Nodes = append(in.Snapshot.Nodes, groupgraph.Node{ID: "unknown", Kind: groupgraph.Group})
	}, func(in *Input) {
		in.Snapshot.Edges = append(in.Snapshot.Edges, groupgraph.Edge{GroupID: "a", MemberID: "missing"})
	}} {
		in := fixture()
		edit(&in)
		r := evaluate(t, in)
		if r.CompleteWithinSnapshot || object(t, r, "other").State != "unknown" || object(t, r, "u").State != "labelled" {
			t.Fatal(r)
		}
		after := fixture()
		after.Snapshot.ObservationID = "synthetic-after"
		if got, e := Compare(in, after); !errors.Is(e, ErrIncomplete) || !reflect.DeepEqual(got, ChangeSet{}) {
			t.Fatal(got, e)
		}
		if got, e := Compare(after, in); !errors.Is(e, ErrIncomplete) || !reflect.DeepEqual(got, ChangeSet{}) {
			t.Fatal(got, e)
		}
	}
}
func TestCompare(t *testing.T) {
	before, after := fixture(), fixture()
	after.Snapshot.ObservationID = "synthetic-after"
	after.Snapshot.Edges = []groupgraph.Edge{{GroupID: "a", MemberID: "b"}, {GroupID: "a", MemberID: "pc"}}
	after.ManualLabels = []ManualLabel{{"other", "watch"}}
	r, e := Compare(before, after)
	if e != nil {
		t.Fatal(e)
	}
	want := []Change{{"other", "added", Provenance{"watch", "manual", "", 0}}, {"u", "removed", Provenance{"review", "manual", "", 0}}, {"u", "removed", Provenance{SensitiveLabel, "transitive_membership", "a", 2}}}
	if !reflect.DeepEqual(r.Changes, want) || r.BeforeObservationID != before.Snapshot.ObservationID || r.AfterObservationID != after.Snapshot.ObservationID || !r.Simulated {
		t.Fatal(r)
	}
	after = fixture()
	after.Snapshot.ObservationID = "synthetic-after"
	r, e = Compare(before, after)
	if e != nil || len(r.Changes) != 0 {
		t.Fatal(r, e)
	}
	for name, edit := range map[string]func(*Input){"policy": func(in *Input) { in.PolicyID = "other" }, "groups": func(in *Input) { in.SensitiveGroupIDs = []string{"b"} }, "same observation": func(in *Input) { in.Snapshot.ObservationID = before.Snapshot.ObservationID }, "coverage": func(in *Input) { in.Snapshot.Nodes = in.Snapshot.Nodes[:4] }, "kind": func(in *Input) { in.Snapshot.Nodes[4].Kind = groupgraph.Computer }} {
		t.Run(name, func(t *testing.T) {
			in := fixture()
			in.Snapshot.ObservationID = "synthetic-after"
			edit(&in)
			r, e := Compare(before, in)
			if !errors.Is(e, ErrIncompatible) || !reflect.DeepEqual(r, ChangeSet{}) {
				t.Fatal(r, e)
			}
		})
	}
	after = fixture()
	after.Scope.DomainID = "other"
	after.Snapshot.Scope = after.Scope
	if _, e = Compare(before, after); !errors.Is(e, ErrScope) {
		t.Fatal(e)
	}
}
func TestValidationAndLimits(t *testing.T) {
	for name, edit := range map[string]func(*Input){"version": func(in *Input) { in.Version = "other" }, "real": func(in *Input) { in.Simulated = false }, "snapshot real": func(in *Input) { in.Snapshot.Simulated = false }, "policy": func(in *Input) { in.PolicyID = "" }, "unknown group": func(in *Input) { in.SensitiveGroupIDs = []string{"missing"} }, "user group": func(in *Input) { in.SensitiveGroupIDs = []string{"u"} }, "duplicate group": func(in *Input) { in.SensitiveGroupIDs = []string{"a", "a"} }, "unknown manual": func(in *Input) { in.ManualLabels = []ManualLabel{{"missing", "label"}} }, "duplicate manual": func(in *Input) { in.ManualLabels = append(in.ManualLabels, in.ManualLabels[0]) }, "blank label": func(in *Input) { in.ManualLabels[0].Label = " " }, "oversize label": func(in *Input) { in.ManualLabels[0].Label = strings.Repeat("x", 257) }, "control label": func(in *Input) { in.ManualLabels[0].Label = "secret\x00" }, "depth zero": func(in *Input) { in.MaxDepth = 0 }, "depth too high": func(in *Input) { in.MaxDepth = groupgraph.MaxDepth + 1 }, "labels zero": func(in *Input) { in.MaxLabels = 0 }, "labels too high": func(in *Input) { in.MaxLabels = MaxLabels + 1 }} {
		t.Run(name, func(t *testing.T) {
			in := fixture()
			edit(&in)
			r, e := Evaluate(in)
			if e == nil || !reflect.DeepEqual(r, Result{}) || strings.Contains(e.Error(), "secret") {
				t.Fatal(r, e)
			}
		})
	}
	for _, edit := range []func(*Input){func(in *Input) { in.MaxLabels = 1 }, func(in *Input) { in.ManualLabels = make([]ManualLabel, MaxLabels+1) }, func(in *Input) { in.SensitiveGroupIDs = make([]string, groupgraph.MaxNodes+1) }} {
		in := fixture()
		edit(&in)
		if _, e := Evaluate(in); !errors.Is(e, ErrLimit) {
			t.Fatal(e)
		}
	}
	in := fixture()
	in.MaxDepth = 1
	if _, e := Evaluate(in); !errors.Is(e, groupgraph.ErrLimit) {
		t.Fatal(e)
	}
	in = fixture()
	in.Scope.TenantID = "other"
	if _, e := Evaluate(in); !errors.Is(e, ErrScope) {
		t.Fatal(e)
	}
}
func TestDeterminismAndOwnership(t *testing.T) {
	in := fixture()
	in.SensitiveGroupIDs = []string{"a", "b"}
	in.ManualLabels = append(in.ManualLabels, ManualLabel{"u", "alpha"})
	before := evaluate(t, in)
	a, _ := json.Marshal(before)
	for i, j := 0, len(in.Snapshot.Nodes)-1; i < j; i, j = i+1, j-1 {
		in.Snapshot.Nodes[i], in.Snapshot.Nodes[j] = in.Snapshot.Nodes[j], in.Snapshot.Nodes[i]
	}
	for i, j := 0, len(in.Snapshot.Edges)-1; i < j; i, j = i+1, j-1 {
		in.Snapshot.Edges[i], in.Snapshot.Edges[j] = in.Snapshot.Edges[j], in.Snapshot.Edges[i]
	}
	in.SensitiveGroupIDs[0], in.SensitiveGroupIDs[1] = in.SensitiveGroupIDs[1], in.SensitiveGroupIDs[0]
	in.ManualLabels[0], in.ManualLabels[1] = in.ManualLabels[1], in.ManualLabels[0]
	b, _ := json.Marshal(evaluate(t, in))
	if string(a) != string(b) {
		t.Fatal("input order affects output")
	}
	before.Objects[0].Labels[0].Label = "mutated"
	b, _ = json.Marshal(evaluate(t, in))
	if string(a) != string(b) {
		t.Fatal("output aliases input")
	}
}
