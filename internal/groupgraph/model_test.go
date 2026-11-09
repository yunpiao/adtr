package groupgraph_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	gg "github.com/yunpiao/adtr/internal/groupgraph"
)

// This oracle uses a fixed adjacency matrix and all-pairs dynamic programming,
// not the production BFS or Tarjan algorithm. Only public APIs are exercised.
const infinity = 99

var modelIDs = [4]string{"g0", "g1", "g2", "u"}
var modelScope = gg.Scope{TenantID: "synthetic-tenant", DomainID: "synthetic-domain"}

type model struct {
	snapshot gg.Snapshot
	adjacent [4][4]bool
	known    [3]bool
	absent   [3]bool
	distance [4][4]int
}

func newModel(code int, leaf gg.Kind) model {
	m := model{snapshot: gg.Snapshot{Version: gg.ContractVersion, Simulated: true, Scope: modelScope, ObservationID: "synthetic-observation", Nodes: []gg.Node{}, Edges: []gg.Edge{}}}
	for i := 0; i < 3; i++ {
		choice := code % 33
		code /= 33
		m.known[i] = choice != 0
		m.snapshot.Nodes = append(m.snapshot.Nodes, gg.Node{ID: modelIDs[i], Kind: gg.Group, MembersKnown: m.known[i]})
		if choice == 0 {
			continue
		}
		bits := choice - 1
		for j := 0; j < 5; j++ {
			if bits&(1<<j) == 0 {
				continue
			}
			target := "missing"
			if j < 4 {
				target = modelIDs[j]
				m.adjacent[i][j] = true
			} else {
				m.absent[i] = true
			}
			m.snapshot.Edges = append(m.snapshot.Edges, gg.Edge{GroupID: modelIDs[i], MemberID: target})
		}
	}
	m.snapshot.Nodes = append(m.snapshot.Nodes, gg.Node{ID: modelIDs[3], Kind: leaf})
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			m.distance[i][j] = infinity
			if m.adjacent[i][j] {
				m.distance[i][j] = 1
			}
			if i == j {
				m.distance[i][j] = 0
			}
		}
	}
	for via := 0; via < 4; via++ {
		for from := 0; from < 4; from++ {
			for to := 0; to < 4; to++ {
				if d := m.distance[from][via] + m.distance[via][to]; d < m.distance[from][to] {
					m.distance[from][to] = d
				}
			}
		}
	}
	return m
}
func (m model) diagnostics() gg.Diagnostics {
	d := gg.Diagnostics{UnknownReferences: []gg.Edge{}, Cycles: [][]string{}}
	var assigned [3]bool
	for i := 0; i < 3; i++ {
		if m.absent[i] {
			d.UnknownReferences = append(d.UnknownReferences, gg.Edge{GroupID: modelIDs[i], MemberID: "missing"})
		}
		if assigned[i] {
			continue
		}
		component := []string{}
		for j := i; j < 3; j++ {
			// Mutual reachability is an equivalence relation, independently defining SCC.
			if m.distance[i][j] < infinity && m.distance[j][i] < infinity {
				component = append(component, modelIDs[j])
				assigned[j] = true
			}
		}
		if len(component) > 1 || m.adjacent[i][i] {
			d.Cycles = append(d.Cycles, component)
		}
	}
	return d
}
func (m model) query(root int, direction gg.Direction, transitive bool, depth, results int) (gg.Result, error) {
	r := gg.Result{Version: gg.ContractVersion, Simulated: true, Scope: m.snapshot.Scope, ObservationID: m.snapshot.ObservationID, Relationships: []gg.Relationship{}, UnknownReferences: []gg.Edge{}, UnknownMembershipGroups: []string{}, CompleteWithinSnapshot: true}
	var reached [4]bool
	reached[root] = true
	for target := 0; target < 4; target++ {
		if target == root {
			continue
		}
		from, to := root, target
		if direction == gg.MemberOf {
			from, to = target, root
		}
		d := m.distance[from][to]
		if !transitive {
			d = infinity
			if m.adjacent[from][to] {
				d = 1
			}
		}
		if d == infinity {
			continue
		}
		if d > depth {
			return gg.Result{}, gg.ErrLimit
		}
		reached[target] = true
		r.Relationships = append(r.Relationships, gg.Relationship{Object: m.snapshot.Nodes[target], Distance: d, Direct: d == 1})
	}
	if len(r.Relationships) > results {
		return gg.Result{}, gg.ErrLimit
	}
	for group := 0; group < 3; group++ {
		if direction == gg.MemberOf {
			if !m.known[group] {
				r.UnknownMembershipGroups = append(r.UnknownMembershipGroups, modelIDs[group])
			}
		} else if group == root || (transitive && reached[group]) {
			if !m.known[group] {
				r.UnknownMembershipGroups = append(r.UnknownMembershipGroups, modelIDs[group])
			}
			if m.absent[group] {
				r.UnknownReferences = append(r.UnknownReferences, gg.Edge{GroupID: modelIDs[group], MemberID: "missing"})
			}
		}
	}
	r.CompleteWithinSnapshot = len(r.UnknownReferences) == 0 && len(r.UnknownMembershipGroups) == 0
	return r, nil
}
func compareQuery(t *testing.T, graph *gg.Graph, m model, code, root int, direction gg.Direction, transitive bool, depth, results int) {
	t.Helper()
	want, expected := m.query(root, direction, transitive, depth, results)
	got, actual := graph.Query(gg.Query{Scope: m.snapshot.Scope, ObjectID: modelIDs[root], Direction: direction, Transitive: transitive, MaxDepth: depth, MaxResults: results})
	if !errors.Is(actual, expected) || !reflect.DeepEqual(got, want) {
		t.Fatalf("code=%d root=%d direction=%s transitive=%t budget=%d/%d: got=%+v err=%v want=%+v err=%v", code, root, direction, transitive, depth, results, got, actual, want, expected)
	}
}
func TestGroupModelExhaustive(t *testing.T) {
	snapshots, comparisons := 0, 0
	// Per group: one unknown-list state, or each of 32 known lists over the
	// three groups, one user and one absent target. 33^3 legal snapshots.
	for code := 0; code < 33*33*33; code++ {
		m := newModel(code, gg.User)
		graph, err := gg.Build(m.snapshot)
		if err != nil {
			t.Fatalf("build code=%d: %v", code, err)
		}
		if got, want := graph.Diagnostics(), m.diagnostics(); !reflect.DeepEqual(got, want) {
			t.Fatalf("diagnostics code=%d: got=%+v want=%+v", code, got, want)
		}
		snapshots++
		for root := 0; root < 4; root++ {
			for _, direction := range []gg.Direction{gg.Members, gg.MemberOf} {
				if root == 3 && direction == gg.Members {
					continue
				}
				for _, transitive := range []bool{false, true} {
					for _, budget := range [][2]int{{3, 3}, {1, 3}, {3, 1}} {
						compareQuery(t, graph, m, code, root, direction, transitive, budget[0], budget[1])
						comparisons++
					}
				}
			}
		}
	}
	if snapshots != 35937 || comparisons != 1509354 {
		t.Fatalf("coverage changed: %d snapshots, %d comparisons", snapshots, comparisons)
	}
	t.Logf("independent oracle: %d snapshots, %d query comparisons, %d whole-graph diagnostics", snapshots, comparisons, snapshots)
}
func TestGroupModelComputerPermutationAndOwnership(t *testing.T) {
	comparisons := 0
	for code := 0; code < 33*33*33; code += 97 {
		m := newModel(code, gg.Computer)
		source, err := json.Marshal(m.snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var reordered gg.Snapshot
		if err = json.Unmarshal(source, &reordered); err != nil {
			t.Fatal(err)
		}
		sort.Slice(reordered.Nodes, func(i, j int) bool { return reordered.Nodes[i].ID > reordered.Nodes[j].ID })
		sort.Slice(reordered.Edges, func(i, j int) bool {
			a, b := reordered.Edges[i], reordered.Edges[j]
			return a.GroupID > b.GroupID || a.GroupID == b.GroupID && a.MemberID > b.MemberID
		})
		graph, err := gg.Build(reordered)
		if err != nil {
			t.Fatal(err)
		}
		// Neither original input nor returned diagnostics may mutate the graph.
		for i := range reordered.Nodes {
			reordered.Nodes[i].ID = "changed"
		}
		for i := range reordered.Edges {
			reordered.Edges[i].MemberID = "changed"
		}
		diagnostics := graph.Diagnostics()
		for i := range diagnostics.UnknownReferences {
			diagnostics.UnknownReferences[i].MemberID = "changed"
		}
		for i := range diagnostics.Cycles {
			for j := range diagnostics.Cycles[i] {
				diagnostics.Cycles[i][j] = "changed"
			}
		}
		if !reflect.DeepEqual(graph.Diagnostics(), m.diagnostics()) {
			t.Fatalf("diagnostic/input alias code=%d", code)
		}
		for root := 0; root < 4; root++ {
			for _, direction := range []gg.Direction{gg.Members, gg.MemberOf} {
				if root == 3 && direction == gg.Members {
					continue
				}
				for _, transitive := range []bool{false, true} {
					q := gg.Query{Scope: m.snapshot.Scope, ObjectID: modelIDs[root], Direction: direction, Transitive: transitive, MaxDepth: 3, MaxResults: 3}
					r, e := graph.Query(q)
					if e != nil {
						t.Fatal(e)
					}
					for i := range r.Relationships {
						r.Relationships[i].Object.ID = "changed"
					}
					for i := range r.UnknownMembershipGroups {
						r.UnknownMembershipGroups[i] = "changed"
					}
					for i := range r.UnknownReferences {
						r.UnknownReferences[i].MemberID = "changed"
					}
					compareQuery(t, graph, m, code, root, direction, transitive, 3, 3)
					comparisons++
				}
			}
		}
	}
	if comparisons != 5194 {
		t.Fatalf("computer coverage changed: %d", comparisons)
	}
	t.Logf("computer/permuted/ownership comparisons: %d", comparisons)
}
func TestGroupModelScopeAndFailureAtomicity(t *testing.T) {
	m := newModel(35936, gg.User)
	graph, err := gg.Build(m.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	base := gg.Query{Scope: m.snapshot.Scope, ObjectID: "g0", Direction: gg.Members, Transitive: true, MaxDepth: 3, MaxResults: 3}
	cases := []struct {
		name   string
		change func(*gg.Query)
		err    error
	}{
		{"tenant", func(q *gg.Query) { q.Scope.TenantID = "other" }, gg.ErrScope},
		{"domain", func(q *gg.Query) { q.Scope.DomainID = "other" }, gg.ErrScope},
		{"absent", func(q *gg.Query) { q.ObjectID = "absent" }, gg.ErrNotFound},
		{"user-members", func(q *gg.Query) { q.ObjectID = "u" }, gg.ErrInvalid},
		{"direction", func(q *gg.Query) { q.Direction = "unexpected" }, gg.ErrInvalid},
		{"zero-depth", func(q *gg.Query) { q.MaxDepth = 0 }, gg.ErrInvalid},
		{"zero-results", func(q *gg.Query) { q.MaxResults = 0 }, gg.ErrInvalid},
		{"result-budget", func(q *gg.Query) { q.MaxResults = 1 }, gg.ErrLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.change(&q)
			r, e := graph.Query(q)
			if !errors.Is(e, tc.err) || !reflect.DeepEqual(r, gg.Result{}) {
				t.Fatalf("%s: %v %+v", tc.name, e, r)
			}
			compareQuery(t, graph, m, 35936, 0, gg.Members, true, 3, 3)
		})
	}
	// Literal mixed-case/Unicode IDs remain opaque; no case/normalization fold.
	s := gg.Snapshot{Version: gg.ContractVersion, Simulated: true, Scope: modelScope, ObservationID: "literal", Nodes: []gg.Node{{ID: "G", Kind: gg.Group, MembersKnown: true}, {ID: "g", Kind: gg.Group, MembersKnown: true}, {ID: "é", Kind: gg.User}, {ID: "e\u0301", Kind: gg.Computer}}, Edges: []gg.Edge{{GroupID: "G", MemberID: "é"}, {GroupID: "g", MemberID: "e\u0301"}}}
	g, e := gg.Build(s)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ root, target string }{{"G", "é"}, {"g", "e\u0301"}} {
		r, e := g.Query(gg.Query{Scope: modelScope, ObjectID: tc.root, Direction: gg.Members, MaxDepth: 1, MaxResults: 1})
		if e != nil || len(r.Relationships) != 1 || r.Relationships[0].Object.ID != tc.target {
			t.Fatal(fmt.Sprint(r), e)
		}
	}
}
