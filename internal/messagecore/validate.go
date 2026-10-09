package messagecore

import "time"

func validateLimits(l Limits) error {
	if l.MaxPageSize < 1 || l.MaxPageSize > 200 {
		return invalid(InvalidPolicy, "limits.max_page_size")
	}
	if l.MaxInputMessages <= 0 || l.MaxInputBytes <= 0 || l.MaxMarkTargets <= 0 {
		return invalid(InvalidPolicy, "limits.budgets")
	}
	return nil
}

func validatePagination(offset, limit, maximum int) error {
	if offset < 0 || limit < 1 || limit > maximum {
		return invalid(InvalidPagination, "pagination")
	}
	return nil
}

// Accounting is deliberately deterministic and architecture-independent. It is
// a logical input guard, not a claim to measure Go heap usage. Subtraction avoids
// integer overflow even with caller-selected limits near MaxInt64.
type byteBudget struct{ remaining int64 }

func (b *byteBudget) add(n int64) error {
	if n > b.remaining {
		return invalid(BudgetExceeded, "input_bytes")
	}
	b.remaining -= n
	return nil
}

func (b *byteBudget) strings(values ...string) error {
	for _, value := range values {
		if err := b.add(int64(len(value))); err != nil {
			return err
		}
	}
	return nil
}

func (b *byteBudget) state(s ReadState) error {
	if err := b.strings(s.TenantID, s.UserID); err != nil {
		return err
	}
	for id := range s.FirstReadAt {
		if err := b.add(64); err != nil {
			return err
		}
		if err := b.strings(id); err != nil {
			return err
		}
	}
	return nil
}

func checkRequestBudget(in Request) error {
	if len(in.Messages) > in.Limits.MaxInputMessages {
		return invalid(BudgetExceeded, "input_messages")
	}
	b := byteBudget{remaining: in.Limits.MaxInputBytes}
	if err := b.add(256); err != nil {
		return err
	}
	if err := b.strings(in.Scope.TenantID, in.Scope.UserID, in.Query.Keyword, string(in.Query.Sort), string(in.Query.ReadFilter)); err != nil {
		return err
	}
	for _, id := range in.Scope.DomainIDs {
		if err := b.add(64); err != nil {
			return err
		}
		if err := b.strings(id); err != nil {
			return err
		}
	}
	for _, pair := range in.Catalog {
		if err := b.add(64); err != nil {
			return err
		}
		if err := b.strings(pair.Type, pair.SubType); err != nil {
			return err
		}
	}
	for _, kind := range in.Query.Types {
		if err := b.add(64); err != nil {
			return err
		}
		if err := b.strings(kind); err != nil {
			return err
		}
	}
	for _, pair := range in.Query.SubTypes {
		if err := b.add(64); err != nil {
			return err
		}
		if err := b.strings(pair.Type, pair.SubType); err != nil {
			return err
		}
	}
	for _, m := range in.Messages {
		if err := b.add(192); err != nil {
			return err
		}
		if err := b.strings(m.ID, m.TenantID, string(m.ScopeKind), m.DomainID, m.Type, m.SubType, m.Title, m.Description); err != nil {
			return err
		}
	}
	return b.state(in.ReadState)
}

type catalogIndex map[string]map[string]struct{}

func validateCatalog(c Catalog) (catalogIndex, error) {
	if len(c) == 0 {
		return nil, invalid(InvalidCatalog, "catalog")
	}
	index := make(catalogIndex)
	for _, pair := range c {
		if pair.Type == "" || pair.SubType == "" {
			return nil, invalid(InvalidCatalog, "catalog.pair")
		}
		subtypes, ok := index[pair.Type]
		if !ok {
			subtypes = make(map[string]struct{})
			index[pair.Type] = subtypes
		}
		if _, exists := subtypes[pair.SubType]; exists {
			return nil, invalid(InvalidCatalog, "catalog.duplicate_pair")
		}
		subtypes[pair.SubType] = struct{}{}
	}
	return index, nil
}

func (c catalogIndex) contains(pair TypeSubType) bool {
	_, ok := c[pair.Type][pair.SubType]
	return ok
}

func validateScope(s Scope) (map[string]struct{}, error) {
	if s.TenantID == "" || s.UserID == "" {
		return nil, invalid(InvalidScope, "scope.identity")
	}
	domains := make(map[string]struct{}, len(s.DomainIDs))
	for _, id := range s.DomainIDs {
		if id == "" {
			return nil, invalid(InvalidScope, "scope.domain_ids")
		}
		if _, exists := domains[id]; exists {
			return nil, invalid(InvalidScope, "scope.duplicate_domain")
		}
		domains[id] = struct{}{}
	}
	return domains, nil
}

func validateReadState(s ReadState, scope Scope) error {
	if s.TenantID == "" || s.UserID == "" || s.TenantID != scope.TenantID || s.UserID != scope.UserID {
		return invalid(InvalidReadState, "read_state.identity")
	}
	for id, at := range s.FirstReadAt {
		if id == "" || at.IsZero() {
			return invalid(InvalidReadState, "read_state.entry")
		}
	}
	return nil
}

func validateQuery(q Query, confirmedAt time.Time, l Limits, c catalogIndex) (Query, error) {
	if confirmedAt.IsZero() {
		return Query{}, invalid(InvalidFilter, "confirmed_at")
	}
	if q.Sort != SortAscending && q.Sort != SortDescending {
		return Query{}, invalid(InvalidFilter, "query.sort")
	}
	if q.ReadFilter != ReadAll && q.ReadFilter != ReadUnread && q.ReadFilter != ReadRead {
		return Query{}, invalid(InvalidFilter, "query.read_filter")
	}
	if q.Start.IsZero() != q.End.IsZero() {
		return Query{}, invalid(InvalidFilter, "query.time_range")
	}
	if q.Start.IsZero() {
		q.End = utc(confirmedAt)
		q.Start = q.End.Add(-168 * time.Hour)
	} else {
		q.Start, q.End = utc(q.Start), utc(q.End)
	}
	if !q.Start.Before(q.End) {
		return Query{}, invalid(InvalidFilter, "query.time_range")
	}
	if err := validatePagination(q.Offset, q.Limit, l.MaxPageSize); err != nil {
		return Query{}, err
	}
	types := make(map[string]struct{}, len(q.Types))
	for _, kind := range q.Types {
		if _, ok := c[kind]; !ok {
			return Query{}, invalid(InvalidFilter, "query.types")
		}
		if _, exists := types[kind]; exists {
			return Query{}, invalid(InvalidFilter, "query.duplicate_type")
		}
		types[kind] = struct{}{}
	}
	pairs := make(map[TypeSubType]struct{}, len(q.SubTypes))
	for _, pair := range q.SubTypes {
		if !c.contains(pair) {
			return Query{}, invalid(InvalidFilter, "query.subtypes")
		}
		if len(types) != 0 {
			if _, selected := types[pair.Type]; !selected {
				return Query{}, invalid(InvalidFilter, "query.incompatible_subtype")
			}
		}
		if _, exists := pairs[pair]; exists {
			return Query{}, invalid(InvalidFilter, "query.duplicate_subtype")
		}
		pairs[pair] = struct{}{}
	}
	return cloneQuery(q), nil
}

func validateMessage(m Message, c catalogIndex) error {
	if m.ID == "" || m.TenantID == "" || m.OccurredAt.IsZero() || m.IngestedAt.IsZero() {
		return invalid(InvalidMessage, "message.required_fields")
	}
	switch m.ScopeKind {
	case ScopePlatform:
		if m.DomainID != "" {
			return invalid(InvalidMessage, "message.platform_domain")
		}
	case ScopeDomain:
		if m.DomainID == "" {
			return invalid(InvalidMessage, "message.domain_id")
		}
	default:
		return invalid(InvalidMessage, "message.scope_kind")
	}
	if !c.contains(TypeSubType{Type: m.Type, SubType: m.SubType}) {
		return invalid(InvalidMessage, "message.type_subtype")
	}
	return nil
}
