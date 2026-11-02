package simulationlabels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yunpiao/adtr/internal/groupgraph"
	"github.com/yunpiao/adtr/internal/sensitivelabels"
	"strings"
	"testing"
)

func testScope() Scope {
	return Scope{Tenant: "synthetic-tenant", Domain: "synthetic-domain", Read: true, Manage: true}
}
func testFixture() Fixture {
	s := testScope()
	o := Observation{ID: "o1", Sequence: 1, Complete: true, Snapshot: groupgraph.Snapshot{Version: groupgraph.ContractVersion, Simulated: true, Scope: scope(s), ObservationID: "o1", Nodes: []groupgraph.Node{{ID: "g", Kind: groupgraph.Group, MembersKnown: true}, {ID: "nested", Kind: groupgraph.Group, MembersKnown: true}, {ID: "u", Kind: groupgraph.User}, {ID: "c", Kind: groupgraph.Computer}}, Edges: []groupgraph.Edge{{GroupID: "g", MemberID: "nested"}, {GroupID: "nested", MemberID: "u"}}}, Assets: []Asset{{ObjectID: "g", Name: "Synthetic sensitive group"}, {ObjectID: "nested", Name: "Nested group"}, {ObjectID: "u", Name: "Example user"}, {ObjectID: "c", Name: "Example computer"}}}
	o2 := o
	o2.ID = "o2"
	o2.Sequence = 2
	o2.Snapshot = o.Snapshot
	o2.Snapshot.ObservationID = "o2"
	o2.Snapshot.Edges = []groupgraph.Edge{{GroupID: "g", MemberID: "nested"}, {GroupID: "nested", MemberID: "c"}}
	o3 := o2
	o3.ID = "o3"
	o3.Sequence = 3
	o3.Complete = false
	o3.Snapshot = o2.Snapshot
	o3.Snapshot.ObservationID = "o3"
	return Fixture{ID: "fixture", Version: Contract, Producer: "isolated-unit-fixture-v1", Simulated: true, Observations: []Observation{o, o2, o3}}
}
func TestCanonicalObservation(t *testing.T) {
	s := testScope()
	f := testFixture()
	o, e := canonicalObservation(s, f.Observations[0])
	if e != nil {
		t.Fatal(e)
	}
	if o.Hash == "" || o.Assets[0].ObjectID != "c" {
		t.Fatal("missing canonical order/hash")
	}
	again, e := canonicalObservation(s, o)
	if e != nil || !equal(o, again) {
		t.Fatal("not deterministic")
	}
	o.Assets[0].Name = "changed"
	if f.Observations[0].Assets[0].Name == "changed" {
		t.Fatal("aliased input")
	}
	cases := map[string]func(*Observation){"missing metadata": func(o *Observation) { o.Assets = o.Assets[:1] }, "duplicate metadata": func(o *Observation) { o.Assets[1] = o.Assets[0] }, "bad scope": func(o *Observation) { o.Snapshot.Scope.DomainID = "other" }, "real marker": func(o *Observation) { o.Snapshot.Simulated = false }, "wrong observation": func(o *Observation) { o.ID = "other" }, "sequence overflow": func(o *Observation) { o.Sequence = 129 }, "blank name": func(o *Observation) { o.Assets[0].Name = " " }, "unknown kind": func(o *Observation) { o.Snapshot.Nodes[0].Kind = "other" }}
	for n, fn := range cases {
		t.Run(n, func(t *testing.T) {
			o := testFixture().Observations[0]
			fn(&o)
			if _, e := canonicalObservation(s, o); e == nil {
				t.Fatal("accepted invalid")
			}
		})
	}
}
func TestCanonicalSpec(t *testing.T) {
	p, e := canonicalSpec(PolicySpec{Name: "Policy", SensitiveGroupIDs: []string{"z", "a"}, ManualLabels: []sensitivelabels.ManualLabel{{ObjectID: "u", Label: "custom"}}})
	if e != nil || p.SensitiveGroupIDs[0] != "a" {
		t.Fatal(e)
	}
	bad := []PolicySpec{{Name: ""}, {Name: "bad\n"}, {Name: "P", SensitiveGroupIDs: []string{"g", "g"}}, {Name: "P", ManualLabels: []sensitivelabels.ManualLabel{{ObjectID: "u", Label: "x"}, {ObjectID: "u", Label: "x"}}}, {Name: "P", ManualLabels: []sensitivelabels.ManualLabel{{ObjectID: "u", Label: strings.Repeat("x", 257)}}}}
	for _, p := range bad {
		if _, e := canonicalSpec(p); e == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
func TestComparisonEvidenceAndRejections(t *testing.T) {
	s := testScope()
	f := testFixture()
	p := Policy{ID: "policy", DatasetID: f.ID, Revision: 1, Status: "active", Hash: "fixed-policy-hash", Spec: PolicySpec{Name: "Policy", SensitiveGroupIDs: []string{"g"}, ManualLabels: []sensitivelabels.ManualLabel{{ObjectID: "nested", Label: "manual-group-only"}}}}
	a := Evaluation{ID: "a", Dataset: Dataset{ID: f.ID, Hash: "fixturehash"}, Observation: f.Observations[0], Policy: p}
	b := a
	b.ID = "b"
	b.Observation = f.Observations[1]
	v, e := makeComparison(s, a, b)
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Alerts) != 2 || v.Alerts[0].ObjectID != "c" || v.Alerts[0].Changes[0].Action != "added" || v.Alerts[1].ObjectID != "u" || v.Alerts[1].Changes[0].Action != "removed" {
		t.Fatalf("wrong alerts: %+v", v.Alerts)
	}
	for _, alert := range v.Alerts {
		if !alert.Simulation || alert.Delivery != "in_app_only" || len(alert.Changes) != 1 || alert.Changes[0].Provenance.Distance != 2 {
			t.Fatal("incorrect provenance")
		}
	}
	same, e := makeComparison(s, a, b)
	if e != nil || !equal(v, same) {
		t.Fatal("not deterministic")
	}
	tests := map[string]struct {
		change func(*Evaluation)
		want   error
	}{"policy revision": {func(v *Evaluation) { v.Policy.Revision++ }, ErrIncompatible}, "policy hash": {func(v *Evaluation) { v.Policy.Hash = "other" }, ErrIncompatible}, "reverse sequence": {func(v *Evaluation) { v.Observation.Sequence = 1 }, ErrIncompatible}, "dataset": {func(v *Evaluation) { v.Dataset.Hash = "other" }, ErrIncompatible}, "incomplete": {func(v *Evaluation) { v.Observation.Complete = false }, ErrIncomplete}, "coverage loss": {func(v *Evaluation) { v.Observation.Snapshot.Nodes = v.Observation.Snapshot.Nodes[:2] }, ErrIncomplete}}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c := b
			tc.change(&c)
			_, e := makeComparison(s, a, c)
			if !errors.Is(e, tc.want) {
				t.Fatalf("got %v want %v", e, tc.want)
			}
		})
	}
	b.Observation.Snapshot.Edges = a.Observation.Snapshot.Edges
	v, e = makeComparison(s, a, b)
	if e != nil || len(v.Alerts) != 0 || len(v.Changes.Changes) != 0 {
		t.Fatal("zero changes must create no alert")
	}
}
func TestUnknownNotSafe(t *testing.T) {
	s := testScope()
	o := testFixture().Observations[2]
	p := Policy{ID: "p", Spec: PolicySpec{Name: "P", SensitiveGroupIDs: []string{"g"}}}
	r, e := sensitivelabels.Evaluate(coreInput(s, o, p))
	if e != nil {
		t.Fatal(e)
	}
	for _, obj := range r.Objects {
		if obj.ObjectID == "u" && obj.State != "unknown" {
			t.Fatal("incomplete absence inferred safe")
		}
	}
}
func TestAuthorizationAndPagination(t *testing.T) {
	ctx := context.Background()
	s := testScope()
	if _, e := DatasetsTx(ctx, nil, s, 0, 10); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := MutatePolicyTx(ctx, nil, s, PolicyInput{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := EvaluateTx(ctx, nil, s, EvaluateInput{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := CompareTx(ctx, nil, s, CompareInput{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	for _, x := range [][2]int{{-1, 10}, {10001, 10}, {0, 0}, {0, 101}} {
		if _, e := page([]int{1}, x[0], x[1]); e == nil {
			t.Fatal("bad page")
		}
	}
	p, e := page([]int{1, 2}, 10, 10)
	if e != nil || p.Count != 2 || p.Items == nil || len(p.Items) != 0 {
		t.Fatal("bad empty page")
	}
}

// longIdentifierFixture binds every graph/metadata reference to the same
// specified boundary. Dataset/observation IDs stay small for readable failures.
func longIdentifierFixture(n int) Fixture {
	raw, _ := json.Marshal(testFixture())
	var f Fixture
	_ = json.Unmarshal(raw, &f)
	ids := map[string]string{}
	for _, node := range f.Observations[0].Snapshot.Nodes {
		ids[node.ID] = node.ID[:1] + strings.Repeat("x", n-1)
	}
	f.ID = "long-identifiers"
	for i := range f.Observations {
		o := &f.Observations[i]
		for j := range o.Snapshot.Nodes {
			o.Snapshot.Nodes[j].ID = ids[o.Snapshot.Nodes[j].ID]
		}
		for j := range o.Snapshot.Edges {
			o.Snapshot.Edges[j].GroupID = ids[o.Snapshot.Edges[j].GroupID]
			o.Snapshot.Edges[j].MemberID = ids[o.Snapshot.Edges[j].MemberID]
		}
		for j := range o.Assets {
			o.Assets[j].ObjectID = ids[o.Assets[j].ObjectID]
		}
	}
	return f
}
func TestIdentifierBoundaryMatchesDetailAPI(t *testing.T) {
	s := testScope()
	for _, n := range []int{128, 129} {
		f := longIdentifierFixture(n)
		_, err := canonicalObservation(s, f.Observations[0])
		if (err == nil) != (n == 128) {
			t.Fatalf("fixture ID bytes %d: %v", n, err)
		}
		group := "g" + strings.Repeat("x", n-1)
		user := "u" + strings.Repeat("x", n-1)
		for _, spec := range []PolicySpec{{Name: "P", SensitiveGroupIDs: []string{group}}, {Name: "P", ManualLabels: []sensitivelabels.ManualLabel{{ObjectID: user, Label: "custom"}}}} {
			_, err := canonicalSpec(spec)
			if (err == nil) != (n == 128) {
				t.Fatalf("policy ref bytes %d: %v", n, err)
			}
		}
		_, err = AssetTx(context.Background(), nil, s, "fixture", "evaluation", user)
		want := ErrDenied
		if n == 129 {
			want = ErrInvalid
		}
		if !errors.Is(err, want) {
			t.Fatalf("detail ID bytes %d got %v want %v", n, err, want)
		}
	}
	for name, change := range map[string]func(*Observation){
		"node":        func(o *Observation) { o.Snapshot.Nodes[0].ID = strings.Repeat("n", 129) },
		"edge group":  func(o *Observation) { o.Snapshot.Edges[0].GroupID = strings.Repeat("g", 129) },
		"edge member": func(o *Observation) { o.Snapshot.Edges[0].MemberID = strings.Repeat("m", 129) },
		"metadata":    func(o *Observation) { o.Assets[0].ObjectID = strings.Repeat("a", 129) },
	} {
		t.Run(name, func(t *testing.T) {
			o := testFixture().Observations[0]
			change(&o)
			if _, err := canonicalObservation(s, o); !errors.Is(err, ErrInvalid) {
				t.Fatalf("overlong ID accepted: %v", err)
			}
		})
	}
}

// escapedComparisonFixture reproduces the independent review's legal graph:
// 126 nodes,124/125 edges and64 selected groups, all128-byte identifiers.
// A one-hop distance change produces7872 changes and a >8MiB comparison.
func escapedComparisonFixture() (Fixture, PolicySpec) {
	s := testScope()
	id := func(i int) string { return strings.Repeat("<", 124) + fmt.Sprintf("%04d", i) }
	p := PolicySpec{Name: "Escaped synthetic policy", SensitiveGroupIDs: []string{}, ManualLabels: []sensitivelabels.ManualLabel{}}
	nodes := []groupgraph.Node{}
	before, after := []groupgraph.Edge{}, []groupgraph.Edge{}
	for i := 0; i < 64; i++ {
		p.SensitiveGroupIDs = append(p.SensitiveGroupIDs, id(i))
		nodes = append(nodes, groupgraph.Node{ID: id(i), Kind: groupgraph.Group, MembersKnown: true})
		before = append(before, groupgraph.Edge{GroupID: id(i), MemberID: id(64)})
		after = append(after, groupgraph.Edge{GroupID: id(i), MemberID: id(65)})
	}
	nodes = append(nodes, groupgraph.Node{ID: id(64), Kind: groupgraph.Group, MembersKnown: true}, groupgraph.Node{ID: id(65), Kind: groupgraph.Group, MembersKnown: true})
	after = append(after, groupgraph.Edge{GroupID: id(65), MemberID: id(64)})
	for i := 66; i < 126; i++ {
		nodes = append(nodes, groupgraph.Node{ID: id(i), Kind: groupgraph.User})
		edge := groupgraph.Edge{GroupID: id(64), MemberID: id(i)}
		before = append(before, edge)
		after = append(after, edge)
	}
	assets := []Asset{}
	for _, n := range nodes {
		assets = append(assets, Asset{ObjectID: n.ID, Name: "Synthetic escaped asset"})
	}
	obs := func(name string, seq uint64, edges []groupgraph.Edge) Observation {
		return Observation{ID: name, Sequence: seq, Complete: true, Snapshot: groupgraph.Snapshot{Version: groupgraph.ContractVersion, Simulated: true, Scope: scope(s), ObservationID: name, Nodes: nodes, Edges: edges}, Assets: assets}
	}
	return Fixture{ID: "escaped-comparison", Version: Contract, Producer: "isolated-review-regression", Simulated: true, Observations: []Observation{obs("before", 1, before), obs("after", 2, after)}}, p
}
func oversizedObservationFixture() Fixture {
	s := testScope()
	id := func(i int) string { return strings.Repeat("<", 124) + fmt.Sprintf("%04d", i) }
	desc := strings.Repeat("<", 512)
	o := Observation{ID: "oversized", Sequence: 1, Complete: true, Snapshot: groupgraph.Snapshot{Version: groupgraph.ContractVersion, Simulated: true, Scope: scope(s), ObservationID: "oversized", Nodes: []groupgraph.Node{}, Edges: []groupgraph.Edge{}}, Assets: []Asset{}}
	for i := 0; i < 256; i++ {
		kind := groupgraph.User
		known := false
		if i < 128 {
			kind = groupgraph.Group
			known = true
		}
		o.Snapshot.Nodes = append(o.Snapshot.Nodes, groupgraph.Node{ID: id(i), Kind: kind, MembersKnown: known})
		o.Assets = append(o.Assets, Asset{ObjectID: id(i), Name: "Synthetic", Description: &desc})
	}
	for i := 0; i < 8; i++ {
		for j := 128; j < 256; j++ {
			o.Snapshot.Edges = append(o.Snapshot.Edges, groupgraph.Edge{GroupID: id(i), MemberID: id(j)})
		}
	}
	return Fixture{ID: "oversized-fixture", Version: Contract, Producer: "isolated-byte-budget-regression", Simulated: true, Observations: []Observation{o}}
}
func TestPersistedByteBounds(t *testing.T) {
	for _, limit := range []int{4096, 128 * 1024, 1024 * 1024, 8 * 1024 * 1024} {
		if b, e := marshalBounded(strings.Repeat("x", limit-2), limit); e != nil || len(b) != limit {
			t.Fatalf("exact boundary %d: %v", limit, e)
		}
		if _, e := marshalBounded(strings.Repeat("x", limit-1), limit); !errors.Is(e, ErrLimit) {
			t.Fatalf("over boundary %d: %v", limit, e)
		}
	}
	v := struct {
		A string `json:"a"`
		B string `json:"b"`
	}{A: "comma,: inside string", B: "\\\"<"}
	raw, _ := json.Marshal(v)
	if _, e := marshalBounded(v, len(raw)+3); e != nil {
		t.Fatal("jsonb whitespace upper bound wrong", e)
	}
	if _, e := marshalBounded(v, len(raw)+2); !errors.Is(e, ErrLimit) {
		t.Fatal("jsonb separator budget missing", e)
	}
	// Even a nested record fitting8MiB cannot be inserted if its receipt wrapper
	// exceeds8MiB. This is a serializer boundary specimen, not a valid business row.
	nested := Comparison{Hash: strings.Repeat("x", 8*1024*1024-512)}
	if _, e := marshalBounded(nested, 8*1024*1024); e != nil {
		t.Fatal("nested boundary setup", e)
	}
	if _, _, e := prepareOperation(Receipt{Comparison: &nested, Reason: strings.Repeat("<", 512)}, CompareInput{}); !errors.Is(e, ErrLimit) {
		t.Fatal("receipt envelope escaped budget not enforced", e)
	}
	f, p := escapedComparisonFixture()
	s := testScope()
	policy := Policy{ID: "p", Revision: 1, Hash: "fixed", Spec: p}
	a := Evaluation{ID: "a", Dataset: Dataset{ID: f.ID, Hash: "fixed"}, Observation: f.Observations[0], Policy: policy}
	b := a
	b.ID = "b"
	b.Observation = f.Observations[1]
	for _, o := range f.Observations {
		canonical, e := canonicalObservation(s, o)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = marshalBounded(canonical, 1024*1024); e != nil {
			t.Fatal("legal observations must fit", e)
		}
	}
	if _, e := canonicalSpec(p); e != nil {
		t.Fatal(e)
	}
	// Both source evaluations fit their record and receipt bounds: the regression
	// specifically targets comparison expansion, not an unreachable input pair.
	for _, v := range []Evaluation{a, b} {
		result, e := sensitivelabels.Evaluate(coreInput(s, v.Observation, v.Policy))
		if e != nil {
			t.Fatal(e)
		}
		v.Result = result
		if _, e = marshalBounded(v, 8*1024*1024); e != nil {
			t.Fatal("source evaluation too large", e)
		}
		if _, _, e = prepareOperation(Receipt{Evaluation: &v}, EvaluateInput{}); e != nil {
			t.Fatal("source receipt too large", e)
		}
	}
	c, e := makeComparison(s, a, b)
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Changes.Changes) != 7872 || len(c.Alerts) != 62 {
		t.Fatal("independent reproduction changed")
	}
	if _, e := marshalBounded(c, 8*1024*1024); !errors.Is(e, ErrLimit) {
		t.Fatal("oversized legal comparison accepted", e)
	}
	if _, _, e := prepareOperation(Receipt{Comparison: &c}, CompareInput{}); !errors.Is(e, ErrLimit) {
		t.Fatal("oversized receipt accepted", e)
	}
	huge := oversizedObservationFixture()
	o, e := canonicalObservation(s, huge.Observations[0])
	if e != nil {
		t.Fatal("legal graph expected", e)
	}
	if _, e = marshalBounded(o, 1024*1024); !errors.Is(e, ErrLimit) {
		t.Fatal("oversized legal observation accepted", e)
	}
}

func TestSerializedPageLimitIsDistinct(t *testing.T) {
	large := strings.Repeat("x", 8*1024*1024)
	if _, err := page([]string{large}, 0, 1); !errors.Is(err, ErrPageTooLarge) || errors.Is(err, ErrLimit) {
		t.Fatalf("serialized page must have distinct error: %v", err)
	}
	if _, err := marshalBounded(large, 8*1024*1024); !errors.Is(err, ErrLimit) || errors.Is(err, ErrPageTooLarge) {
		t.Fatalf("record bound must retain ErrLimit: %v", err)
	}
	if _, _, err := prepareOperation(Receipt{Reason: large}, CompareInput{}); !errors.Is(err, ErrLimit) || errors.Is(err, ErrPageTooLarge) {
		t.Fatalf("receipt bound must retain ErrLimit: %v", err)
	}
	if _, err := page([]string{"small"}, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid page was reclassified: %v", err)
	}
	if _, err := boundedPage(Page[func()]{Items: []func(){func() {}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("serialization error was reclassified: %v", err)
	}
	if err := mapCore(groupgraph.ErrLimit); !errors.Is(err, ErrLimit) || errors.Is(err, ErrPageTooLarge) {
		t.Fatalf("graph budget was reclassified: %v", err)
	}
	if _, err := page([]string{"small"}, 0, 1); err != nil {
		t.Fatal(err)
	}
}
