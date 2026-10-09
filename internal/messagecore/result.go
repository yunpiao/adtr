package messagecore

import (
	"sort"
	"strings"
	"time"
)

// Result is an immutable query-time snapshot. Its zero value is invalid; use
// NewResult. Concurrent reads and transitions are safe provided callers do not
// concurrently mutate their own input maps/slices while a call is reading them.
type Result struct {
	valid       bool
	scope       Scope
	query       Query
	confirmedAt time.Time
	limits      Limits
	readState   ReadState
	base        []Entry
	members     []Entry
	positions   map[string]int
	counts      Counts
}

// NewResult validates the complete input, including messages outside the
// requested scope, before returning any result. Over-budget inputs are rejected,
// never truncated. No caller-owned slice or map is retained.
func NewResult(in Request) (*Result, error) {
	if err := validateLimits(in.Limits); err != nil {
		return nil, err
	}
	if err := checkRequestBudget(in); err != nil {
		return nil, err
	}
	catalog, err := validateCatalog(in.Catalog)
	if err != nil {
		return nil, err
	}
	domains, err := validateScope(in.Scope)
	if err != nil {
		return nil, err
	}
	if err := validateReadState(in.ReadState, in.Scope); err != nil {
		return nil, err
	}
	query, err := validateQuery(in.Query, in.ConfirmedAt, in.Limits, catalog)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(in.Messages))
	for _, m := range in.Messages {
		if err := validateMessage(m, catalog); err != nil {
			return nil, err
		}
		if _, exists := ids[m.ID]; exists {
			return nil, invalid(InvalidMessage, "message.duplicate_id")
		}
		ids[m.ID] = struct{}{}
	}
	r := &Result{
		valid:       true,
		scope:       cloneScope(in.Scope),
		query:       query,
		confirmedAt: utc(in.ConfirmedAt),
		limits:      in.Limits,
		readState:   cloneReadState(in.ReadState),
		base:        make([]Entry, 0),
		members:     make([]Entry, 0),
		positions:   make(map[string]int),
	}
	types := make(map[string]struct{}, len(query.Types))
	for _, kind := range query.Types {
		types[kind] = struct{}{}
	}
	pairs := make(map[TypeSubType]struct{}, len(query.SubTypes))
	for _, pair := range query.SubTypes {
		pairs[pair] = struct{}{}
	}
	for _, m := range in.Messages {
		if m.TenantID != r.scope.TenantID {
			continue
		}
		if m.ScopeKind == ScopePlatform {
			if !r.scope.IncludePlatform {
				continue
			}
		} else if _, allowed := domains[m.DomainID]; !allowed {
			continue
		}
		if len(types) > 0 {
			if _, selected := types[m.Type]; !selected {
				continue
			}
		}
		if len(pairs) > 0 {
			if _, selected := pairs[TypeSubType{Type: m.Type, SubType: m.SubType}]; !selected {
				continue
			}
		}
		m.OccurredAt, m.IngestedAt = utc(m.OccurredAt), utc(m.IngestedAt)
		if m.OccurredAt.Before(query.Start) || !m.OccurredAt.Before(query.End) || !strings.Contains(m.Title, query.Keyword) {
			continue
		}
		first, read := r.readState.FirstReadAt[m.ID]
		r.base = append(r.base, Entry{Message: m, Read: read, FirstReadAt: first})
	}
	sort.Slice(r.base, func(i, j int) bool {
		a, b := r.base[i].Message, r.base[j].Message
		if a.OccurredAt.Equal(b.OccurredAt) {
			return a.ID < b.ID
		}
		if query.Sort == SortAscending {
			return a.OccurredAt.Before(b.OccurredAt)
		}
		return a.OccurredAt.After(b.OccurredAt)
	})
	for _, entry := range r.base {
		r.counts.All++
		if entry.Read {
			r.counts.Read++
		} else {
			r.counts.Unread++
		}
		if query.ReadFilter == ReadUnread && entry.Read || query.ReadFilter == ReadRead && !entry.Read {
			continue
		}
		r.positions[entry.Message.ID] = len(r.members)
		r.members = append(r.members, entry)
	}
	return r, nil
}

func (r *Result) check() error {
	if r == nil || !r.valid {
		return invalid(InvalidResult, "result")
	}
	return nil
}

// Scope, Query and ReadSnapshot return independent copies. ConfirmedAt, Limits
// and Counts return values. They reflect the original query, not later marks.
func (r *Result) Scope() Scope            { return cloneScope(r.scope) }
func (r *Result) Query() Query            { return cloneQuery(r.query) }
func (r *Result) ReadSnapshot() ReadState { return cloneReadState(r.readState) }
func (r *Result) ConfirmedAt() time.Time  { return r.confirmedAt }
func (r *Result) Limits() Limits          { return r.limits }
func (r *Result) Counts() Counts          { return r.counts }
func (r *Result) Total() int              { return len(r.members) }

// Base returns the complete ordered, non-read-filtered basis for Counts.
func (r *Result) Base() []Entry {
	return append(make([]Entry, 0, len(r.base)), r.base...)
}

// Members returns the full ordered read-filtered membership before pagination.
func (r *Result) Members() []Entry {
	return append(make([]Entry, 0, len(r.members)), r.members...)
}

// IDs returns the fixed members for explicit selection. MarkRead may reject the
// whole selection if it exceeds MaxMarkTargets; callers must not truncate it.
func (r *Result) IDs() []string {
	ids := make([]string, len(r.members))
	for i, entry := range r.members {
		ids[i] = entry.Message.ID
	}
	return ids
}

// List returns the initially requested page of this same fixed result.
func (r *Result) List() (Page, error) {
	if err := r.check(); err != nil {
		return Page{}, err
	}
	return r.Page(r.query.Offset, r.query.Limit)
}

// Page changes only slicing. Large offsets are valid empty pages, and all
// arithmetic avoids offset+limit overflow.
func (r *Result) Page(offset, limit int) (Page, error) {
	if err := r.check(); err != nil {
		return Page{}, err
	}
	if err := validatePagination(offset, limit, r.limits.MaxPageSize); err != nil {
		return Page{}, err
	}
	p := Page{Items: make([]Entry, 0), Total: len(r.members), Counts: r.counts, Offset: offset, Limit: limit}
	if offset >= len(r.members) {
		return p, nil
	}
	n := len(r.members) - offset
	if n > limit {
		n = limit
	}
	p.Items = append(p.Items, r.members[offset:offset+n]...)
	return p, nil
}

// Locate searches only fixed read-filtered members, regardless of initial page.
// Page size must obey the same explicit pagination policy as Page.
func (r *Result) Locate(id string, pageSize int) (Location, error) {
	miss := Location{Index: -1}
	if err := r.check(); err != nil {
		return miss, err
	}
	if err := validatePagination(0, pageSize, r.limits.MaxPageSize); err != nil {
		return miss, err
	}
	if id == "" {
		return miss, invalid(InvalidTarget, "locate.id")
	}
	index, found := r.positions[id]
	if !found {
		return miss, nil
	}
	return Location{Found: true, Index: index, Page: index/pageSize + 1}, nil
}
