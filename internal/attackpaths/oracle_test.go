package attackpaths_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"
	"time"

	ap "github.com/yunpiao/adtr/internal/attackpaths"
)

var oracleEpoch = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

// This oracle uses all simple paths and an independent Floyd-Warshall matrix,
// not the production adjacency ordering, FIFO traversal or visited-node rules.
// Every shortest unweighted path is simple; cycles and self-loops cannot shorten it.
type graphOracle struct {
	snapshot ap.Snapshot
	index    map[string]int
	distance [][]int
	paths    [][][]ap.Edge
	infinity int
}

func pathLess(a, b []ap.Edge) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i].To != b[i].To {
			return a[i].To < b[i].To
		}
		if a[i].ID != b[i].ID {
			return a[i].ID < b[i].ID
		}
	}
	return false
}

func newGraphOracle(t testing.TB, s ap.Snapshot, rules []string, start, end time.Time) graphOracle {
	t.Helper()
	n := len(s.Nodes)
	o := graphOracle{s, make(map[string]int), make([][]int, n), make([][][]ap.Edge, n), n + 1}
	for i, node := range s.Nodes {
		o.index[node.ID] = i
		o.distance[i] = make([]int, n)
		o.paths[i] = make([][]ap.Edge, n)
		for j := range n {
			o.distance[i][j] = o.infinity
		}
		o.distance[i][i] = 0
	}
	filtered := make([]ap.Edge, 0, len(s.Edges))
	for _, edge := range s.Edges {
		if slices.Contains(rules, edge.RuleID) && edge.OccurredAt.Compare(start) >= 0 && edge.OccurredAt.Compare(end) < 0 {
			filtered = append(filtered, edge)
			from, to := o.index[edge.From], o.index[edge.To]
			if from != to {
				o.distance[from][to] = 1
			}
		}
	}
	for k := range n {
		for i := range n {
			for j := range n {
				o.distance[i][j] = min(o.distance[i][j], o.distance[i][k]+o.distance[k][j])
			}
		}
	}
	for source := range n {
		visited := make([]bool, n)
		var visit func(int, []ap.Edge)
		visit = func(at int, path []ap.Edge) {
			if o.paths[source][at] == nil || pathLess(path, o.paths[source][at]) {
				o.paths[source][at] = append([]ap.Edge{}, path...)
			}
			visited[at] = true
			for _, edge := range filtered {
				next := o.index[edge.To]
				if edge.From == s.Nodes[at].ID && !visited[next] {
					visit(next, append(path, edge))
				}
			}
			visited[at] = false
		}
		visit(source, nil)
		for target := range n {
			path := o.paths[source][target]
			if (path == nil) != (o.distance[source][target] == o.infinity) || (path != nil && len(path) != o.distance[source][target]) {
				t.Fatalf("independent oracles disagree: from=%d to=%d distance=%d path=%v", source, target, o.distance[source][target], path)
			}
		}
	}
	return o
}

func (o graphOracle) expected(q ap.Query) (ap.Result, error) {
	from, to := o.index[q.From], o.index[q.To]
	out := ap.Result{Version: ap.Version, Simulated: true, Scope: o.snapshot.Scope,
		ObservationID: o.snapshot.ObservationID, Nodes: []ap.Node{}, Edges: []ap.Edge{}}
	if o.distance[from][to] < o.infinity && o.distance[from][to] <= q.MaxDepth {
		out.Found = true
		out.Nodes = append(out.Nodes, o.snapshot.Nodes[from])
		for _, edge := range o.paths[from][to] {
			edge.OccurredAt = edge.OccurredAt.UTC()
			out.Edges = append(out.Edges, edge)
			out.Nodes = append(out.Nodes, o.snapshot.Nodes[o.index[edge.To]])
		}
		return out, nil
	}
	// A missing target is not proof of no path if any reachable node lies beyond
	// the depth bound. A found in-budget target takes precedence over that branch.
	for _, d := range o.distance[from] {
		if d < o.infinity && d > q.MaxDepth {
			return ap.Result{}, ap.ErrLimit
		}
	}
	return out, nil
}

func requireOracleResult(t testing.TB, label any, q ap.Query, got ap.Result, err error, o graphOracle) {
	t.Helper()
	want, wantErr := o.expected(q)
	if !errors.Is(err, wantErr) || !reflect.DeepEqual(got, want) {
		t.Fatalf("case=%v from=%q to=%q depth=%d rules=%v window=[%s,%s):\ngot=%+v err=%v\nwant=%+v err=%v",
			label, q.From, q.To, q.MaxDepth, q.RuleIDs, q.Start, q.End, got, err, want, wantErr)
	}
}

func oracleSnapshot(ids []string) ap.Snapshot {
	s := ap.Snapshot{Version: ap.Version, Simulated: true,
		Scope:         ap.Scope{TenantID: "synthetic-oracle-tenant", DomainID: "synthetic-oracle-domain"},
		ObservationID: "synthetic-oracle-observation", Nodes: []ap.Node{}, Edges: []ap.Edge{}}
	for _, id := range ids {
		s.Nodes = append(s.Nodes, ap.Node{ID: id, Name: "Synthetic " + id})
	}
	return s
}

func TestShortestExhaustiveOracle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ids   []string
		loops bool
	}{
		{"three_nodes_including_self_loops", []string{"a", "b", "c"}, true},
		{"four_nodes_without_self_loops", []string{"a", "b", "c", "d"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			all := []ap.Edge{}
			for _, from := range tc.ids {
				for _, to := range tc.ids {
					if tc.loops || from != to {
						all = append(all, ap.Edge{ID: from + "-" + to, From: from, To: to, RuleID: "r", OccurredAt: oracleEpoch})
					}
				}
			}
			checks := 0
			for mask := 0; mask < 1<<len(all); mask++ {
				s := oracleSnapshot(tc.ids)
				for i, edge := range all {
					if mask&(1<<i) != 0 {
						s.Edges = append(s.Edges, edge)
					}
				}
				g, err := ap.Build(s)
				if err != nil {
					t.Fatal(err)
				}
				o := newGraphOracle(t, s, []string{"r"}, oracleEpoch, oracleEpoch.Add(time.Second))
				for _, from := range tc.ids {
					for _, to := range tc.ids {
						for depth := 1; depth <= len(tc.ids); depth++ {
							q := ap.Query{Scope: s.Scope, From: from, To: to, RuleIDs: []string{"r"}, Start: oracleEpoch, End: oracleEpoch.Add(time.Second), MaxDepth: depth}
							got, err := g.Shortest(q)
							requireOracleResult(t, mask, q, got, err, o)
							checks++
						}
					}
				}
			}
			t.Logf("graphs=%d shortest-query comparisons=%d", 1<<len(all), checks)
		})
	}
}

func TestShortestFilteredOracleAndJSON(t *testing.T) {
	rng := rand.New(rand.NewSource(298))
	ids := []string{"z", "a-2", "猫", "a-10", "é", "a"}
	checks, jsonChecks := 0, 0
	for sample := range 32 {
		s := oracleSnapshot(ids)
		s.Edges = append(s.Edges,
			ap.Edge{ID: "parallel-z", From: ids[0], To: ids[1], RuleID: "r-a", OccurredAt: oracleEpoch},
			ap.Edge{ID: "parallel-a", From: ids[0], To: ids[1], RuleID: "r-a", OccurredAt: oracleEpoch})
		for i := range 24 {
			s.Edges = append(s.Edges, ap.Edge{ID: fmt.Sprintf("edge-%d", i), From: ids[rng.Intn(len(ids))], To: ids[rng.Intn(len(ids))],
				RuleID: []string{"r-a", "r-b"}[rng.Intn(2)], OccurredAt: oracleEpoch.Add(time.Duration(rng.Intn(5)-1) * time.Nanosecond)})
		}
		permuted := s
		permuted.Nodes = slices.Clone(s.Nodes)
		permuted.Edges = slices.Clone(s.Edges)
		rng.Shuffle(len(permuted.Nodes), func(i, j int) { permuted.Nodes[i], permuted.Nodes[j] = permuted.Nodes[j], permuted.Nodes[i] })
		rng.Shuffle(len(permuted.Edges), func(i, j int) { permuted.Edges[i], permuted.Edges[j] = permuted.Edges[j], permuted.Edges[i] })
		graphs := []*ap.Graph{}
		for _, snapshot := range []ap.Snapshot{s, permuted} {
			g, err := ap.Build(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			graphs = append(graphs, g)
		}
		for _, rules := range [][]string{{"r-a"}, {"r-b"}, {"r-b", "r-a"}, {"unknown"}} {
			for _, window := range [][2]int{{0, 2}, {-1, 0}, {2, 3}} {
				start, end := oracleEpoch.Add(time.Duration(window[0])*time.Nanosecond), oracleEpoch.Add(time.Duration(window[1])*time.Nanosecond)
				o := newGraphOracle(t, s, rules, start, end)
				for _, from := range ids {
					for _, to := range ids {
						for _, depth := range []int{1, 2, 5} {
							q := ap.Query{Scope: s.Scope, From: from, To: to, RuleIDs: rules, Start: start, End: end, MaxDepth: depth}
							for _, g := range graphs {
								got, err := g.Shortest(q)
								requireOracleResult(t, sample, q, got, err, o)
								checks++
							}
							if from == ids[0] && to == ids[len(ids)-1] && depth == 2 {
								for _, snapshot := range []ap.Snapshot{s, permuted} {
									data, err := json.Marshal(struct {
										Snapshot ap.Snapshot `json:"snapshot"`
										Query    ap.Query    `json:"query"`
									}{snapshot, q})
									if err != nil {
										t.Fatal(err)
									}
									got, err := ap.EvaluateJSON(data)
									requireOracleResult(t, sample, q, got, err, o)
									jsonChecks++
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("filtered/permuted comparisons=%d JSON comparisons=%d", checks, jsonChecks)
}
