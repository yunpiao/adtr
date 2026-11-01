package groupgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func fixture() Snapshot {
	return Snapshot{Version: ContractVersion, Simulated: true, Scope: Scope{"synthetic-tenant", "synthetic-domain"}, ObservationID: "synthetic-observation", Nodes: []Node{{"a", Group, true}, {"b", Group, true}, {"c", Group, true}, {"u", User, false}, {"pc", Computer, false}, {"unknown-members", Group, false}}, Edges: []Edge{{"a", "b"}, {"a", "u"}, {"b", "c"}, {"b", "u"}, {"c", "a"}, {"c", "pc"}, {"c", "missing"}}}
}
func build(t *testing.T, s Snapshot) *Graph {
	t.Helper()
	g, e := Build(s)
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func query(s Snapshot, id string, d Direction, transitive bool) Query {
	return Query{s.Scope, id, d, transitive, MaxDepth, MaxNodes}
}
func TestDirectAndTransitive(t *testing.T) {
	s := fixture()
	g := build(t, s)
	r, e := g.Query(query(s, "a", Members, false))
	if e != nil {
		t.Fatal(e)
	}
	want := []Relationship{{Node{"b", Group, true}, 1, true}, {Node{"u", User, false}, 1, true}}
	if !reflect.DeepEqual(r.Relationships, want) || !r.CompleteWithinSnapshot {
		t.Fatalf("direct: %+v", r)
	}
	r, e = g.Query(query(s, "a", Members, true))
	if e != nil {
		t.Fatal(e)
	}
	want = []Relationship{{Node{"b", Group, true}, 1, true}, {Node{"c", Group, true}, 2, false}, {Node{"pc", Computer, false}, 3, false}, {Node{"u", User, false}, 1, true}}
	if !reflect.DeepEqual(r.Relationships, want) || r.CompleteWithinSnapshot || !reflect.DeepEqual(r.UnknownReferences, []Edge{{"c", "missing"}}) {
		t.Fatalf("transitive: %+v", r)
	}
	if r.Version != ContractVersion || !r.Simulated || r.ObservationID != s.ObservationID || r.Scope != s.Scope {
		t.Fatal("lost provenance")
	}
	r, e = g.Query(query(s, "u", MemberOf, true))
	if e != nil {
		t.Fatal(e)
	}
	want = []Relationship{{Node{"a", Group, true}, 1, true}, {Node{"b", Group, true}, 1, true}, {Node{"c", Group, true}, 2, false}}
	if !reflect.DeepEqual(r.Relationships, want) || r.CompleteWithinSnapshot || !reflect.DeepEqual(r.UnknownMembershipGroups, []string{"unknown-members"}) {
		t.Fatalf("reverse: %+v", r)
	}
	r, e = g.Query(query(s, "u", MemberOf, false))
	if e != nil || len(r.Relationships) != 2 {
		t.Fatalf("reverse direct: %+v %v", r, e)
	}
}
func TestUnknownVersusKnownEmpty(t *testing.T) {
	s := fixture()
	s.Nodes = append(s.Nodes, Node{"empty", Group, true})
	g := build(t, s)
	for _, tc := range []struct {
		id       string
		complete bool
	}{{"empty", true}, {"unknown-members", false}} {
		r, e := g.Query(query(s, tc.id, Members, true))
		if e != nil || r.CompleteWithinSnapshot != tc.complete || len(r.Relationships) != 0 {
			t.Fatalf("%s: %+v %v", tc.id, r, e)
		}
	}
	s.Edges = append(s.Edges, Edge{"a", "unknown-members"})
	r, e := build(t, s).Query(query(s, "a", Members, true))
	if e != nil || !reflect.DeepEqual(r.UnknownMembershipGroups, []string{"unknown-members"}) {
		t.Fatal(r, e)
	}
}
func TestCyclesAndDeterminism(t *testing.T) {
	s := fixture()
	s.Nodes = append(s.Nodes, Node{"self", Group, true})
	s.Edges = append(s.Edges, Edge{"self", "self"})
	g := build(t, s)
	want := Diagnostics{UnknownReferences: []Edge{{"c", "missing"}}, Cycles: [][]string{{"a", "b", "c"}, {"self"}}}
	if !reflect.DeepEqual(g.Diagnostics(), want) {
		t.Fatal(g.Diagnostics())
	}
	r, e := g.Query(query(s, "self", Members, true))
	if e != nil || len(r.Relationships) != 0 || !r.CompleteWithinSnapshot {
		t.Fatal(r, e)
	}
	before, _ := g.Query(query(s, "a", Members, true))
	bytesBefore, _ := json.Marshal(before)
	for i, j := 0, len(s.Nodes)-1; i < j; i, j = i+1, j-1 {
		s.Nodes[i], s.Nodes[j] = s.Nodes[j], s.Nodes[i]
	}
	for i, j := 0, len(s.Edges)-1; i < j; i, j = i+1, j-1 {
		s.Edges[i], s.Edges[j] = s.Edges[j], s.Edges[i]
	}
	h := build(t, s)
	after, _ := h.Query(query(s, "a", Members, true))
	bytesAfter, _ := json.Marshal(after)
	if string(bytesBefore) != string(bytesAfter) || !reflect.DeepEqual(h.Diagnostics(), want) {
		t.Fatal("input order changes output")
	}
	// A diamond is not a cycle.
	s = fixture()
	s.Edges = []Edge{{"a", "b"}, {"a", "c"}, {"b", "u"}, {"c", "u"}}
	if len(build(t, s).Diagnostics().Cycles) != 0 {
		t.Fatal("diamond reported as cycle")
	}
}
func TestValidation(t *testing.T) {
	cases := map[string]func(*Snapshot){
		"version": func(s *Snapshot) { s.Version = "v1" }, "real": func(s *Snapshot) { s.Simulated = false }, "scope": func(s *Snapshot) { s.Scope.TenantID = "" }, "observation": func(s *Snapshot) { s.ObservationID = "\x00secret" },
		"duplicate node": func(s *Snapshot) { s.Nodes = append(s.Nodes, s.Nodes[0]) }, "duplicate edge": func(s *Snapshot) { s.Edges = append(s.Edges, s.Edges[0]) }, "kind": func(s *Snapshot) { s.Nodes[0].Kind = "other" }, "non-group known": func(s *Snapshot) { s.Nodes[3].MembersKnown = true }, "invalid member": func(s *Snapshot) { s.Edges[0].MemberID = "\xff" }, "unknown source": func(s *Snapshot) { s.Edges[0].GroupID = "absent" }, "user source": func(s *Snapshot) { s.Edges[0].GroupID = "u" }, "unknown list source": func(s *Snapshot) { s.Edges[0].GroupID = "unknown-members" }, "oversize ID": func(s *Snapshot) { s.Nodes[0].ID = strings.Repeat("x", 257) }, "space ID": func(s *Snapshot) { s.Nodes[0].ID = " a" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			s := fixture()
			edit(&s)
			g, e := Build(s)
			if g != nil || !errors.Is(e, ErrInvalid) || strings.Contains(e.Error(), "secret") {
				t.Fatal(g, e)
			}
		})
	}
	s := fixture()
	s.Nodes = make([]Node, MaxNodes+1)
	if _, e := Build(s); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	s = fixture()
	s.Edges = make([]Edge, MaxEdges+1)
	if _, e := Build(s); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}
func TestQueryBoundaries(t *testing.T) {
	s := fixture()
	g := build(t, s)
	for _, tc := range []struct {
		name string
		edit func(*Query)
		want error
	}{
		{"tenant", func(q *Query) { q.Scope.TenantID = "other" }, ErrScope}, {"domain", func(q *Query) { q.Scope.DomainID = "other" }, ErrScope}, {"unknown", func(q *Query) { q.ObjectID = "absent" }, ErrNotFound}, {"kind", func(q *Query) { q.ObjectID = "u" }, ErrInvalid}, {"direction", func(q *Query) { q.Direction = "other" }, ErrInvalid}, {"depth zero", func(q *Query) { q.MaxDepth = 0 }, ErrInvalid}, {"depth high", func(q *Query) { q.MaxDepth = MaxDepth + 1 }, ErrInvalid}, {"results zero", func(q *Query) { q.MaxResults = 0 }, ErrInvalid}, {"results high", func(q *Query) { q.MaxResults = MaxNodes + 1 }, ErrInvalid}, {"depth exceeded", func(q *Query) { q.MaxDepth = 2 }, ErrLimit}, {"results exceeded", func(q *Query) { q.MaxResults = 3 }, ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := query(s, "a", Members, true)
			tc.edit(&q)
			r, e := g.Query(q)
			if !errors.Is(e, tc.want) || !reflect.DeepEqual(r, Result{}) {
				t.Fatal(r, e)
			}
		})
	}
	q := query(s, "a", Members, true)
	q.MaxDepth = 3
	q.MaxResults = 4
	if _, e := g.Query(q); e != nil {
		t.Fatal(e)
	}
	var nilGraph *Graph
	if _, e := nilGraph.Query(q); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	// Literal case-sensitive IDs: no DN/case normalization is inferred.
	q.ObjectID = "A"
	if _, e := g.Query(q); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestOwnershipAndConcurrency(t *testing.T) {
	s := fixture()
	g := build(t, s)
	s.Nodes[0].ID = "mutated"
	s.Edges[0].MemberID = "mutated"
	d := g.Diagnostics()
	d.Cycles[0][0] = "mutated"
	d.UnknownReferences[0].MemberID = "mutated"
	q := query(s, "a", Members, true)
	r, _ := g.Query(q)
	r.Relationships[0].Object.ID = "mutated"
	r.UnknownReferences[0].MemberID = "mutated"
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := g.Query(q)
			if e != nil || r.Relationships[0].Object.ID != "b" || g.Diagnostics().Cycles[0][0] != "a" || r.UnknownReferences[0].MemberID != "missing" {
				t.Error("mutable graph", e)
			}
		}()
	}
	wg.Wait()
}
func TestMaximumGraphAndDepth(t *testing.T) {
	s := fixture()
	s.Nodes = nil
	s.Edges = nil
	for i := 0; i < MaxNodes; i++ {
		s.Nodes = append(s.Nodes, Node{fmt.Sprintf("g%04d", i), Group, true})
		if i > 0 {
			s.Edges = append(s.Edges, Edge{fmt.Sprintf("g%04d", i-1), fmt.Sprintf("g%04d", i)})
		}
	}
	g := build(t, s)
	r, e := g.Query(query(s, "g0000", Members, true))
	if !errors.Is(e, ErrLimit) || !reflect.DeepEqual(r, Result{}) {
		t.Fatal(r, e)
	}
	// Full bounded cycle does not overflow SCC traversal or enumerate paths.
	s.Edges = append(s.Edges, Edge{fmt.Sprintf("g%04d", MaxNodes-1), "g0000"})
	g = build(t, s)
	if len(g.Diagnostics().Cycles) != 1 || len(g.Diagnostics().Cycles[0]) != MaxNodes {
		t.Fatal("missing large cycle")
	}
}
