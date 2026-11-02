package simulationlabels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/groupgraph"
	"github.com/yunpiao/adtr/internal/sensitivelabels"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func valid(s string, n int) bool {
	if s == "" || len(s) > n || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func equal(a, b any) bool { return digest(a) == digest(b) }
func authorize(tx pgx.Tx, s Scope, write bool) error {
	if tx == nil || !valid(s.Tenant, 128) || !valid(s.Domain, 128) || !s.Read || (write && !s.Manage) {
		return ErrDenied
	}
	return nil
}
func scope(s Scope) groupgraph.Scope { return groupgraph.Scope{TenantID: s.Tenant, DomainID: s.Domain} }
func lock(ctx context.Context, tx pgx.Tx, s Scope) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-labels:"+digest([]string{s.Tenant, s.Domain}))
	return e
}
func page[T any](items []T, offset, limit int) (Page[T], error) {
	if offset < 0 || offset > 10000 || limit < 1 || limit > MaxPage {
		return Page[T]{}, ErrInvalid
	}
	p := Page[T]{Items: []T{}, Count: len(items), Offset: offset, Limit: limit}
	if offset < len(items) {
		end := offset + limit
		if end > len(items) {
			end = len(items)
		}
		p.Items = items[offset:end]
	}
	return boundedPage(p)
}

// marshalBounded bounds both compact JSON and PostgreSQL jsonb's spaced wire
// representation. jsonb adds spaces after structural separators; counting those
// outside strings is a conservative upper bound without raising read caps.
func marshalBounded(v any, limit int) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, ErrInvalid
	}
	size := len(b)
	quoted, escaped := false, false
	for _, c := range b {
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		} else if c == ':' || c == ',' {
			size++
		}
	}
	if size > limit {
		return nil, ErrLimit
	}
	return b, nil
}
func boundedPage[T any](p Page[T]) (Page[T], error) {
	if _, e := marshalBounded(p, 8*1024*1024); e != nil {
		if errors.Is(e, ErrLimit) {
			return Page[T]{}, ErrPageTooLarge
		}
		return Page[T]{}, e
	}
	return p, nil
}
func readJSON(ctx context.Context, tx pgx.Tx, q string, args []any, out any) error {
	var b []byte
	e := tx.QueryRow(ctx, q, args...).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	if len(b) > 8*1024*1024 {
		return ErrLimit
	}
	if json.Unmarshal(b, out) != nil {
		return ErrInvalid
	}
	return nil
}
func ids(ctx context.Context, tx pgx.Tx, q string, args ...any) ([]string, error) {
	rows, e := tx.Query(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
		if len(out) > MaxEvaluations {
			return nil, ErrLimit
		}
	}
	return out, rows.Err()
}
func mapCore(e error) error {
	switch {
	case errors.Is(e, sensitivelabels.ErrIncomplete):
		return ErrIncomplete
	case errors.Is(e, sensitivelabels.ErrIncompatible):
		return ErrIncompatible
	case errors.Is(e, sensitivelabels.ErrLimit), errors.Is(e, groupgraph.ErrLimit):
		return ErrLimit
	case e != nil:
		return ErrInvalid
	}
	return nil
}
func canonicalObservation(s Scope, o Observation) (Observation, error) {
	if !valid(o.ID, 128) || o.Sequence < 1 || o.Sequence > MaxObservations || o.Snapshot.ObservationID != o.ID || o.Snapshot.Scope != scope(s) || len(o.Snapshot.Nodes) > MaxNodes || len(o.Snapshot.Edges) > MaxEdges || len(o.Assets) != len(o.Snapshot.Nodes) {
		return Observation{}, ErrInvalid
	}
	// The HTTP-facing synthetic module freezes all identifiers at 128 bytes.
	// Core groupgraph permits larger IDs; accepting those here would persist
	// objects that the detail API could never address. Reject, never truncate.
	for _, n := range o.Snapshot.Nodes {
		if !valid(n.ID, 128) {
			return Observation{}, ErrInvalid
		}
	}
	for _, edge := range o.Snapshot.Edges {
		if !valid(edge.GroupID, 128) || !valid(edge.MemberID, 128) {
			return Observation{}, ErrInvalid
		}
	}
	if _, e := groupgraph.Build(o.Snapshot); e != nil {
		return Observation{}, mapCore(e)
	}
	// JSON copying prevents mutation of caller-owned graph/metadata slices.
	b, _ := json.Marshal(o)
	o = Observation{}
	_ = json.Unmarshal(b, &o)
	seen := map[string]bool{}
	for _, n := range o.Snapshot.Nodes {
		seen[n.ID] = true
	}
	for _, a := range o.Assets {
		if !valid(a.ObjectID, 128) || !seen[a.ObjectID] || !valid(a.Name, 256) || (a.Description != nil && (*a.Description != "" && !valid(*a.Description, 512))) {
			return Observation{}, ErrInvalid
		}
		delete(seen, a.ObjectID)
	}
	if len(seen) != 0 {
		return Observation{}, ErrInvalid
	}
	sort.Slice(o.Assets, func(i, j int) bool { return o.Assets[i].ObjectID < o.Assets[j].ObjectID })
	sort.Slice(o.Snapshot.Nodes, func(i, j int) bool { return o.Snapshot.Nodes[i].ID < o.Snapshot.Nodes[j].ID })
	sort.Slice(o.Snapshot.Edges, func(i, j int) bool {
		a, b := o.Snapshot.Edges[i], o.Snapshot.Edges[j]
		if a.GroupID != b.GroupID {
			return a.GroupID < b.GroupID
		}
		return a.MemberID < b.MemberID
	})
	o.Hash = ""
	o.Hash = digest(o)
	return o, nil
}
func canonicalSpec(p PolicySpec) (PolicySpec, error) {
	if !valid(p.Name, 256) || len(p.SensitiveGroupIDs) > MaxGroups || len(p.ManualLabels) > MaxManualLabels {
		return PolicySpec{}, ErrInvalid
	}
	p.SensitiveGroupIDs = append([]string{}, p.SensitiveGroupIDs...)
	p.ManualLabels = append([]sensitivelabels.ManualLabel{}, p.ManualLabels...)
	sort.Strings(p.SensitiveGroupIDs)
	for i, id := range p.SensitiveGroupIDs {
		if !valid(id, 128) || (i > 0 && p.SensitiveGroupIDs[i-1] == id) {
			return PolicySpec{}, ErrInvalid
		}
	}
	sort.Slice(p.ManualLabels, func(i, j int) bool {
		a, b := p.ManualLabels[i], p.ManualLabels[j]
		if a.ObjectID != b.ObjectID {
			return a.ObjectID < b.ObjectID
		}
		return a.Label < b.Label
	})
	for i, m := range p.ManualLabels {
		if !valid(m.ObjectID, 128) || !valid(m.Label, 256) || (i > 0 && p.ManualLabels[i-1] == m) {
			return PolicySpec{}, ErrInvalid
		}
	}
	return p, nil
}
func coreInput(s Scope, o Observation, p Policy) sensitivelabels.Input {
	return sensitivelabels.Input{Version: sensitivelabels.ContractVersion, Simulated: true, Scope: scope(s), Snapshot: o.Snapshot, Complete: o.Complete, PolicyID: p.ID, SensitiveGroupIDs: p.Spec.SensitiveGroupIDs, ManualLabels: p.Spec.ManualLabels, MaxDepth: MaxDepth, MaxLabels: MaxLabels}
}
