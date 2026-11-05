package attackpaths

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var epoch = time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

func fixture() Snapshot {
	return Snapshot{Version: Version, Simulated: true, Scope: Scope{"synthetic-tenant", "synthetic-domain"}, ObservationID: "snapshot-1", Nodes: []Node{{"a", "Alice"}, {"b", "Bob"}, {"c", "Carol"}, {"d", "Destination"}, {"z", "Isolated"}}, Edges: []Edge{
		{"ac", "a", "c", "rule-1", epoch}, {"cd", "c", "d", "rule-1", epoch},
		{"ab-z", "a", "b", "rule-1", epoch}, {"ab-a", "a", "b", "rule-1", epoch},
		{"bd", "b", "d", "rule-1", epoch}, {"ba", "b", "a", "rule-1", epoch},
		{"aa", "a", "a", "rule-1", epoch}, {"direct", "a", "d", "rule-2", epoch.Add(time.Hour)},
	}}
}
func query() Query {
	return Query{Scope: fixture().Scope, From: "a", To: "d", RuleIDs: []string{"rule-1"}, Start: epoch, End: epoch.Add(time.Hour), MaxDepth: 4}
}
func build(t *testing.T, s Snapshot) *Graph {
	t.Helper()
	g, e := Build(s)
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func TestDeterministicShortestAndCopiedEvidence(t *testing.T) {
	s := fixture()
	g := build(t, s)
	want := []string{"ab-a", "bd"}
	r, e := g.Shortest(query())
	if e != nil || !r.Found || !r.Simulated || r.Version != Version || r.Scope != s.Scope || r.ObservationID != s.ObservationID {
		t.Fatalf("%+v %v", r, e)
	}
	if !reflect.DeepEqual([]string{r.Edges[0].ID, r.Edges[1].ID}, want) || !reflect.DeepEqual(r.Nodes, []Node{s.Nodes[0], s.Nodes[1], s.Nodes[3]}) {
		t.Fatalf("%+v", r)
	}
	for i, j := 0, len(s.Edges)-1; i < j; i, j = i+1, j-1 {
		s.Edges[i], s.Edges[j] = s.Edges[j], s.Edges[i]
	}
	r2, e := build(t, s).Shortest(query())
	if e != nil || !reflect.DeepEqual(r, r2) {
		t.Fatal("input order changed path")
	}
	s.Nodes[0].Name = "mutated"
	for i := range s.Edges {
		if s.Edges[i].ID == "ab-a" {
			s.Edges[i].ID = "mutated"
		}
	}
	r.Nodes[0].Name = "mutated"
	r.Edges[0].ID = "mutated"
	r3, e := g.Shortest(query())
	if e != nil || r3.Nodes[0].Name != "Alice" || r3.Edges[0].ID != "ab-a" {
		t.Fatal("alias")
	}
}
func TestRuleAndHalfOpenTimeFilters(t *testing.T) {
	g := build(t, fixture())
	q := query()
	q.RuleIDs = []string{"rule-2"}
	r, e := g.Shortest(q)
	if e != nil || r.Found || r.Nodes == nil || r.Edges == nil {
		t.Fatalf("exclusive end: %+v %v", r, e)
	}
	q.Start = epoch.Add(time.Hour)
	q.End = q.Start.Add(time.Nanosecond)
	r, e = g.Shortest(q)
	if e != nil || !r.Found || len(r.Edges) != 1 || r.Edges[0].ID != "direct" {
		t.Fatalf("inclusive start: %+v %v", r, e)
	}
	q.Start = q.Start.Add(time.Nanosecond)
	q.End = q.Start.Add(time.Hour)
	r, e = g.Shortest(q)
	if e != nil || r.Found {
		t.Fatal("before start")
	}
	q = query()
	q.RuleIDs = []string{"unknown-rule"}
	r, e = g.Shortest(q)
	if e != nil || r.Found {
		t.Fatal("unknown rule is empty match")
	}
}
func TestDepthBudgetAndCycles(t *testing.T) {
	g := build(t, fixture())
	q := query()
	q.MaxDepth = 1
	r, e := g.Shortest(q)
	if !errors.Is(e, ErrLimit) || r.Found {
		t.Fatalf("%+v %v", r, e)
	}
	q.MaxDepth = 2
	r, e = g.Shortest(q)
	if e != nil || !r.Found {
		t.Fatal(e)
	}
	q.To = "z"
	r, e = g.Shortest(q)
	if e != nil || r.Found {
		t.Fatalf("fully explored disconnected cycle: %+v %v", r, e)
	}
	q.From = "z"
	q.To = "z"
	q.MaxDepth = 1
	r, e = g.Shortest(q)
	if e != nil || !r.Found || len(r.Nodes) != 1 || len(r.Edges) != 0 {
		t.Fatal("zero-hop identity")
	}
}
func TestInvalidSnapshots(t *testing.T) {
	cases := map[string]func(*Snapshot){
		"version": func(s *Snapshot) { s.Version = "other" }, "simulation": func(s *Snapshot) { s.Simulated = false }, "scope": func(s *Snapshot) { s.Scope.DomainID = "" }, "observation": func(s *Snapshot) { s.ObservationID = " " },
		"duplicate node": func(s *Snapshot) { s.Nodes = append(s.Nodes, s.Nodes[0]) }, "empty node": func(s *Snapshot) { s.Nodes[0].ID = "" }, "bad UTF8": func(s *Snapshot) { s.Nodes[0].Name = "\xff" }, "control": func(s *Snapshot) { s.Nodes[0].Name = "a\nb" }, "name limit": func(s *Snapshot) { s.Nodes[0].Name = strings.Repeat("界", 257) },
		"duplicate edge": func(s *Snapshot) { s.Edges = append(s.Edges, s.Edges[0]) }, "unknown from": func(s *Snapshot) { s.Edges[0].From = "missing" }, "unknown to": func(s *Snapshot) { s.Edges[0].To = "missing" }, "edge ID": func(s *Snapshot) { s.Edges[0].ID = " " }, "rule": func(s *Snapshot) { s.Edges[0].RuleID = "" }, "zero time": func(s *Snapshot) { s.Edges[0].OccurredAt = time.Time{} }, "offset": func(s *Snapshot) { s.Edges[0].OccurredAt = epoch.In(time.FixedZone("offset", 3600)) }, "year": func(s *Snapshot) { s.Edges[0].OccurredAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := fixture()
			mutate(&s)
			g, e := Build(s)
			if !errors.Is(e, ErrInvalid) || g != nil {
				t.Fatalf("%v %v", g, e)
			}
		})
	}
	for _, nodes := range []bool{true, false} {
		s := fixture()
		if nodes {
			s.Nodes = make([]Node, MaxNodes+1)
		} else {
			s.Edges = make([]Edge, MaxEdges+1)
		}
		if _, e := Build(s); !errors.Is(e, ErrLimit) {
			t.Fatal(e)
		}
	}
	s := fixture()
	s.Nodes = nil
	s.Edges = nil
	build(t, s)
}
func TestQueryValidation(t *testing.T) {
	g := build(t, fixture())
	cases := []struct {
		name   string
		mutate func(*Query)
		want   error
	}{
		{"tenant", func(q *Query) { q.Scope.TenantID = "other" }, ErrScope}, {"domain", func(q *Query) { q.Scope.DomainID = "other" }, ErrScope}, {"invalid scope", func(q *Query) { q.Scope.DomainID = "" }, ErrInvalid},
		{"missing endpoint", func(q *Query) { q.To = "missing" }, ErrNotFound}, {"empty endpoint", func(q *Query) { q.From = "" }, ErrInvalid}, {"no rules", func(q *Query) { q.RuleIDs = nil }, ErrInvalid}, {"duplicate rules", func(q *Query) { q.RuleIDs = []string{"r", "r"} }, ErrInvalid}, {"blank rule", func(q *Query) { q.RuleIDs = []string{" "} }, ErrInvalid}, {"many rules", func(q *Query) { q.RuleIDs = make([]string, 301) }, ErrLimit},
		{"zero depth", func(q *Query) { q.MaxDepth = 0 }, ErrLimit}, {"large depth", func(q *Query) { q.MaxDepth = 65 }, ErrLimit}, {"negative depth", func(q *Query) { q.MaxDepth = -1 }, ErrLimit}, {"empty time", func(q *Query) { q.Start = time.Time{} }, ErrInvalid}, {"equal window", func(q *Query) { q.End = q.Start }, ErrInvalid}, {"inverted", func(q *Query) { q.End = q.Start.Add(-time.Second) }, ErrInvalid}, {"offset", func(q *Query) { q.End = q.End.In(time.FixedZone("x", 60)) }, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := query()
			c.mutate(&q)
			r, e := g.Shortest(q)
			if !errors.Is(e, c.want) || !reflect.DeepEqual(r, Result{}) {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
	var nilGraph *Graph
	if _, e := nilGraph.Shortest(query()); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestLimitsAndConcurrentReaders(t *testing.T) {
	s := fixture()
	s.Nodes = nil
	s.Edges = nil
	for i := 0; i < MaxNodes; i++ {
		s.Nodes = append(s.Nodes, Node{fmt.Sprint(i), "node"})
	}
	for i := 0; i < MaxEdges; i++ {
		s.Edges = append(s.Edges, Edge{fmt.Sprint(i), "0", "1", "r", epoch})
	}
	g := build(t, s)
	q := query()
	q.From = "0"
	q.To = "1"
	q.RuleIDs = []string{"r"}
	q.MaxDepth = MaxDepth
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := g.Shortest(q)
			if e != nil || !r.Found || len(r.Edges) != 1 || r.Edges[0].ID != "0" {
				t.Errorf("%+v %v", r, e)
			}
		}()
	}
	wg.Wait()
}
func TestConnectivityDoesNotRequireChronology(t *testing.T) {
	s := fixture()
	s.Edges[4].OccurredAt = epoch.Add(-time.Minute)
	g := build(t, s)
	q := query()
	q.Start = epoch.Add(-time.Hour)
	r, e := g.Shortest(q)
	if e != nil || !r.Found || !r.Edges[1].OccurredAt.Before(r.Edges[0].OccurredAt) {
		t.Fatal("documented noncausal connectivity")
	}
}

func TestMaximumDepthChain(t *testing.T) {
	s := fixture()
	s.Nodes = nil
	s.Edges = nil
	for i := 0; i <= 65; i++ {
		s.Nodes = append(s.Nodes, Node{fmt.Sprint(i), "node"})
		if i > 0 {
			s.Edges = append(s.Edges, Edge{fmt.Sprint(i), fmt.Sprint(i - 1), fmt.Sprint(i), "r", epoch})
		}
	}
	g := build(t, s)
	q := query()
	q.From = "0"
	q.To = "64"
	q.RuleIDs = []string{"r"}
	q.MaxDepth = 64
	r, e := g.Shortest(q)
	if e != nil || !r.Found || len(r.Edges) != 64 || len(r.Nodes) != 65 {
		t.Fatalf("64-edge path: %+v %v", r, e)
	}
	q.To = "65"
	r, e = g.Shortest(q)
	if !errors.Is(e, ErrLimit) || !reflect.DeepEqual(r, Result{}) {
		t.Fatalf("65-edge path: %+v %v", r, e)
	}
}
