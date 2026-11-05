// Package attackpaths explores explicitly supplied synthetic event relationships.
// A path is connectivity evidence, not proof of exploitability or authorization.
package attackpaths

import (
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Version = "adtr.simulated-attackpaths.v1"
const MaxNodes = 4096
const MaxEdges = 16384
const MaxRules = 300
const MaxDepth = 64

var (
	ErrInvalid  = errors.New("attackpaths: invalid synthetic contract")
	ErrScope    = errors.New("attackpaths: scope mismatch")
	ErrLimit    = errors.New("attackpaths: budget exceeded")
	ErrNotFound = errors.New("attackpaths: node unavailable")
)

type Scope struct {
	TenantID string `json:"tenantId"`
	DomainID string `json:"domainId"`
}
type Node struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Edge records one supplied observation. RuleID is an opaque label, not code.
// OccurredAt must be a nonzero UTC instant with a year in 1..9999.
type Edge struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	RuleID     string    `json:"ruleId"`
	OccurredAt time.Time `json:"occurredAt"`
}
type Snapshot struct {
	Version       string `json:"version"`
	Simulated     bool   `json:"simulated"`
	Scope         Scope  `json:"scope"`
	ObservationID string `json:"observationId"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

// Graph is immutable. No input or returned slices alias its storage.
type Graph struct {
	scope       Scope
	observation string
	nodes       map[string]Node
	edges       map[string][]Edge
}

type Query struct {
	Scope    Scope     `json:"scope"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	RuleIDs  []string  `json:"ruleIds"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	MaxDepth int       `json:"maxDepth"`
}
type Result struct {
	Version       string `json:"version"`
	Simulated     bool   `json:"simulated"`
	Scope         Scope  `json:"scope"`
	ObservationID string `json:"observationId"`
	// Found means a shortest directed path exists in the supplied filtered graph.
	Found bool   `json:"found"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

func label(s string, max int) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > max || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func scopeValid(s Scope) bool { return label(s.TenantID, 128) && label(s.DomainID, 128) }
func instant(t time.Time) bool {
	_, offset := t.Zone()
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && offset == 0
}

func Build(s Snapshot) (*Graph, error) {
	if s.Version != Version || !s.Simulated || !scopeValid(s.Scope) || !label(s.ObservationID, 128) {
		return nil, ErrInvalid
	}
	if len(s.Nodes) > MaxNodes || len(s.Edges) > MaxEdges {
		return nil, ErrLimit
	}
	g := &Graph{scope: s.Scope, observation: s.ObservationID, nodes: map[string]Node{}, edges: map[string][]Edge{}}
	for _, n := range s.Nodes {
		if !label(n.ID, 128) || !label(n.Name, 256) {
			return nil, ErrInvalid
		}
		if _, ok := g.nodes[n.ID]; ok {
			return nil, ErrInvalid
		}
		g.nodes[n.ID] = n
	}
	ids := map[string]bool{}
	for _, e := range s.Edges {
		if !label(e.ID, 128) || !label(e.RuleID, 128) || !instant(e.OccurredAt) || ids[e.ID] {
			return nil, ErrInvalid
		}
		if _, ok := g.nodes[e.From]; !ok {
			return nil, ErrInvalid
		}
		if _, ok := g.nodes[e.To]; !ok {
			return nil, ErrInvalid
		}
		ids[e.ID] = true
		e.OccurredAt = e.OccurredAt.UTC()
		g.edges[e.From] = append(g.edges[e.From], e)
	}
	// Sorted adjacency and FIFO traversal make equal-length choices independent of input order.
	for id := range g.edges {
		sort.Slice(g.edges[id], func(i, j int) bool {
			a, b := g.edges[id][i], g.edges[id][j]
			if a.To != b.To {
				return a.To < b.To
			}
			return a.ID < b.ID
		})
	}
	return g, nil
}

// Shortest returns one deterministic shortest directed path, with both endpoints.
// It matches rule labels exactly and uses the half-open time window [Start,End).
// ErrLimit means traversal would need to inspect an unseen node beyond MaxDepth;
// it must not be interpreted as evidence that no path exists. Times along a path
// are not required to be chronological, so connectivity does not imply causality.
func (g *Graph) Shortest(q Query) (Result, error) {
	empty := Result{}
	if g == nil || !scopeValid(q.Scope) {
		return empty, ErrInvalid
	}
	if q.Scope != g.scope {
		return empty, ErrScope
	}
	if !label(q.From, 128) || !label(q.To, 128) || !instant(q.Start) || !instant(q.End) || !q.Start.Before(q.End) {
		return empty, ErrInvalid
	}
	if q.MaxDepth < 1 || q.MaxDepth > MaxDepth || len(q.RuleIDs) > MaxRules {
		return empty, ErrLimit
	}
	if len(q.RuleIDs) == 0 {
		return empty, ErrInvalid
	}
	rules := map[string]bool{}
	for _, r := range q.RuleIDs {
		if !label(r, 128) || rules[r] {
			return empty, ErrInvalid
		}
		rules[r] = true
	}
	if _, ok := g.nodes[q.From]; !ok {
		return empty, ErrNotFound
	}
	if _, ok := g.nodes[q.To]; !ok {
		return empty, ErrNotFound
	}
	result := Result{Version: Version, Simulated: true, Scope: g.scope, ObservationID: g.observation, Nodes: []Node{}, Edges: []Edge{}}
	if q.From == q.To {
		result.Found = true
		result.Nodes = append(result.Nodes, g.nodes[q.From])
		return result, nil
	}
	depths := map[string]int{q.From: 0}
	parents := map[string]Edge{}
	queue := []string{q.From}
	limited := false
	for i := 0; i < len(queue); i++ {
		current := queue[i]
		for _, e := range g.edges[current] {
			if !rules[e.RuleID] || e.OccurredAt.Before(q.Start) || !e.OccurredAt.Before(q.End) {
				continue
			}
			if _, seen := depths[e.To]; seen {
				continue
			}
			if depths[current] == q.MaxDepth {
				limited = true
				continue
			}
			depths[e.To] = depths[current] + 1
			parents[e.To] = e
			queue = append(queue, e.To)
			if e.To == q.To {
				result.Found = true
				for id := q.To; id != q.From; {
					edge := parents[id]
					result.Edges = append(result.Edges, edge)
					result.Nodes = append(result.Nodes, g.nodes[id])
					id = edge.From
				}
				result.Nodes = append(result.Nodes, g.nodes[q.From])
				for l, r := 0, len(result.Nodes)-1; l < r; l, r = l+1, r-1 {
					result.Nodes[l], result.Nodes[r] = result.Nodes[r], result.Nodes[l]
				}
				for l, r := 0, len(result.Edges)-1; l < r; l, r = l+1, r-1 {
					result.Edges[l], result.Edges[r] = result.Edges[r], result.Edges[l]
				}
				return result, nil
			}
		}
	}
	if limited {
		return empty, ErrLimit
	}
	return result, nil
}
