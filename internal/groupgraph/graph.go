// Package groupgraph computes relationships in explicitly synthetic snapshots.
// Relationships never establish effective permissions or authorize an action.
package groupgraph

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ContractVersion = "adtr.simulated-groupgraph.v1"
const MaxNodes = 4096
const MaxEdges = 16384
const MaxDepth = 64

var (
	ErrInvalid  = errors.New("groupgraph: invalid simulated contract")
	ErrScope    = errors.New("groupgraph: scope mismatch")
	ErrNotFound = errors.New("groupgraph: object unavailable")
	ErrLimit    = errors.New("groupgraph: budget exceeded")
)

type Scope struct {
	TenantID string `json:"tenantId"`
	DomainID string `json:"domainId"`
}
type Kind string

const (
	User     Kind = "user"
	Group    Kind = "group"
	Computer Kind = "computer"
)

// MembersKnown is only valid for groups. False means the supplied snapshot did
// not establish the group's complete explicit member list.
type Node struct {
	ID           string `json:"id"`
	Kind         Kind   `json:"kind"`
	MembersKnown bool   `json:"membersKnown"`
}
type Edge struct {
	GroupID  string `json:"groupId"`
	MemberID string `json:"memberId"`
}
type Snapshot struct {
	Version       string `json:"version"`
	Simulated     bool   `json:"simulated"`
	Scope         Scope  `json:"scope"`
	ObservationID string `json:"observationId"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}
type Diagnostics struct {
	UnknownReferences []Edge `json:"unknownReferences"`
	// Cycles are strongly connected group components, sorted by ID; singleton
	// components appear only when a self edge exists.
	Cycles [][]string `json:"cycles"`
}

// Graph is immutable after Build; concurrent readers are safe. No caller-owned
// slices are retained and returned slices never alias its storage.
type Graph struct {
	scope            Scope
	observation      string
	nodes            map[string]Node
	forward, reverse map[string][]string
	diagnostics      Diagnostics
}

func validID(v string) bool {
	if len(v) == 0 || len(v) > 256 || !utf8.ValidString(v) || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func Build(s Snapshot) (*Graph, error) {
	if s.Version != ContractVersion || !s.Simulated || !validID(s.Scope.TenantID) || !validID(s.Scope.DomainID) || !validID(s.ObservationID) {
		return nil, ErrInvalid
	}
	if len(s.Nodes) > MaxNodes || len(s.Edges) > MaxEdges {
		return nil, ErrLimit
	}
	g := &Graph{scope: s.Scope, observation: s.ObservationID, nodes: map[string]Node{}, forward: map[string][]string{}, reverse: map[string][]string{}, diagnostics: Diagnostics{UnknownReferences: []Edge{}, Cycles: [][]string{}}}
	for _, n := range s.Nodes {
		if !validID(n.ID) || (n.Kind != User && n.Kind != Group && n.Kind != Computer) || (n.Kind != Group && n.MembersKnown) {
			return nil, ErrInvalid
		}
		if _, ok := g.nodes[n.ID]; ok {
			return nil, ErrInvalid
		}
		g.nodes[n.ID] = n
	}
	seen := map[Edge]bool{}
	for _, e := range s.Edges {
		n, ok := g.nodes[e.GroupID]
		if !ok || n.Kind != Group || !n.MembersKnown || !validID(e.MemberID) || seen[e] {
			return nil, ErrInvalid
		}
		seen[e] = true
		g.forward[e.GroupID] = append(g.forward[e.GroupID], e.MemberID)
		if _, ok := g.nodes[e.MemberID]; !ok {
			g.diagnostics.UnknownReferences = append(g.diagnostics.UnknownReferences, e)
			continue
		}
		g.reverse[e.MemberID] = append(g.reverse[e.MemberID], e.GroupID)
	}
	for _, m := range []map[string][]string{g.forward, g.reverse} {
		for id := range m {
			sort.Strings(m[id])
		}
	}
	sort.Slice(g.diagnostics.UnknownReferences, func(i, j int) bool {
		a, b := g.diagnostics.UnknownReferences[i], g.diagnostics.UnknownReferences[j]
		if a.GroupID != b.GroupID {
			return a.GroupID < b.GroupID
		}
		return a.MemberID < b.MemberID
	})
	g.findCycles()
	return g, nil
}
func (g *Graph) Diagnostics() Diagnostics {
	d := Diagnostics{UnknownReferences: append([]Edge{}, g.diagnostics.UnknownReferences...), Cycles: make([][]string, len(g.diagnostics.Cycles))}
	for i, c := range g.diagnostics.Cycles {
		d.Cycles[i] = append([]string{}, c...)
	}
	return d
}

// findCycles uses bounded Tarjan SCC traversal, O(nodes+edges).
func (g *Graph) findCycles() {
	index := 0
	indices := map[string]int{}
	low := map[string]int{}
	active := map[string]bool{}
	stack := []string{}
	var visit func(string)
	visit = func(v string) {
		index++
		indices[v] = index
		low[v] = index
		stack = append(stack, v)
		active[v] = true
		for _, w := range g.forward[v] {
			n, ok := g.nodes[w]
			if !ok || n.Kind != Group {
				continue
			}
			if indices[w] == 0 {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if active[w] && indices[w] < low[v] {
				low[v] = indices[w]
			}
		}
		if low[v] != indices[v] {
			return
		}
		component := []string{}
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			active[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		self := false
		for _, w := range g.forward[v] {
			if w == v {
				self = true
			}
		}
		if len(component) > 1 || self {
			sort.Strings(component)
			g.diagnostics.Cycles = append(g.diagnostics.Cycles, component)
		}
	}
	ids := []string{}
	for id, n := range g.nodes {
		if n.Kind == Group {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if indices[id] == 0 {
			visit(id)
		}
	}
	sort.Slice(g.diagnostics.Cycles, func(i, j int) bool { return g.diagnostics.Cycles[i][0] < g.diagnostics.Cycles[j][0] })
}

type Direction string

const (
	Members  Direction = "members"
	MemberOf Direction = "member_of"
)

type Query struct {
	Scope      Scope     `json:"scope"`
	ObjectID   string    `json:"objectId"`
	Direction  Direction `json:"direction"`
	Transitive bool      `json:"transitive"`
	// Budgets are required, positive, and may only tighten package maxima.
	MaxDepth   int `json:"maxDepth"`
	MaxResults int `json:"maxResults"`
}
type Relationship struct {
	Object   Node `json:"object"`
	Distance int  `json:"distance"`
	Direct   bool `json:"direct"`
}
type Result struct {
	Version                 string         `json:"version"`
	Simulated               bool           `json:"simulated"`
	Scope                   Scope          `json:"scope"`
	ObservationID           string         `json:"observationId"`
	Relationships           []Relationship `json:"relationships"`
	UnknownReferences       []Edge         `json:"unknownReferences"`
	UnknownMembershipGroups []string       `json:"unknownMembershipGroups"`
	// This is only completeness over explicit edges of the supplied snapshot.
	CompleteWithinSnapshot bool `json:"completeWithinSnapshot"`
}

func (g *Graph) Query(q Query) (Result, error) {
	empty := Result{}
	if g == nil {
		return empty, ErrInvalid
	}
	if q.Scope != g.scope {
		return empty, ErrScope
	}
	if !validID(q.ObjectID) || (q.Direction != Members && q.Direction != MemberOf) || q.MaxDepth < 1 || q.MaxDepth > MaxDepth || q.MaxResults < 1 || q.MaxResults > MaxNodes {
		return empty, ErrInvalid
	}
	n, ok := g.nodes[q.ObjectID]
	if !ok {
		return empty, ErrNotFound
	}
	if q.Direction == Members && n.Kind != Group {
		return empty, ErrInvalid
	}
	r := Result{Version: ContractVersion, Simulated: true, Scope: g.scope, ObservationID: g.observation, Relationships: []Relationship{}, UnknownReferences: []Edge{}, UnknownMembershipGroups: []string{}, CompleteWithinSnapshot: true}
	adjacency := g.forward
	if q.Direction == MemberOf {
		adjacency = g.reverse
		// An unobserved group's outgoing list may contain any object: reverse
		// absence is uncertain even if that group isn't reachable by known edges.
		for id, n := range g.nodes {
			if n.Kind == Group && !n.MembersKnown {
				r.UnknownMembershipGroups = append(r.UnknownMembershipGroups, id)
			}
		}
	}
	type item struct {
		id    string
		depth int
	}
	queue := []item{{q.ObjectID, 0}}
	visited := map[string]bool{q.ObjectID: true}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if q.Direction == Members && !g.nodes[cur.id].MembersKnown && g.nodes[cur.id].Kind == Group {
			r.UnknownMembershipGroups = append(r.UnknownMembershipGroups, cur.id)
		}
		for _, id := range adjacency[cur.id] {
			target, known := g.nodes[id]
			if !known {
				r.UnknownReferences = append(r.UnknownReferences, Edge{cur.id, id})
				continue
			}
			if visited[id] {
				continue
			}
			if cur.depth+1 > q.MaxDepth {
				return empty, ErrLimit
			}
			if len(r.Relationships) >= q.MaxResults {
				return empty, ErrLimit
			}
			visited[id] = true
			r.Relationships = append(r.Relationships, Relationship{target, cur.depth + 1, cur.depth == 0})
			if q.Transitive {
				queue = append(queue, item{id, cur.depth + 1})
			}
		}
	}
	sort.Slice(r.Relationships, func(i, j int) bool { return r.Relationships[i].Object.ID < r.Relationships[j].Object.ID })
	sort.Strings(r.UnknownMembershipGroups)
	sort.Slice(r.UnknownReferences, func(i, j int) bool {
		a, b := r.UnknownReferences[i], r.UnknownReferences[j]
		if a.GroupID != b.GroupID {
			return a.GroupID < b.GroupID
		}
		return a.MemberID < b.MemberID
	})
	r.CompleteWithinSnapshot = len(r.UnknownMembershipGroups) == 0 && len(r.UnknownReferences) == 0
	return r, nil
}
