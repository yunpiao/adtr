package messagecore

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// These independent calculations encode the published logical byte formula;
// they deliberately do not call the implementation's accounting helpers.
func requestBytesForTest(r Request) int64 {
	n := int64(256 + len(r.Scope.TenantID) + len(r.Scope.UserID) + len(r.Query.Keyword) + len(r.Query.Sort) + len(r.Query.ReadFilter))
	for _, id := range r.Scope.DomainIDs {
		n += int64(64 + len(id))
	}
	for _, pair := range r.Catalog {
		n += int64(64 + len(pair.Type) + len(pair.SubType))
	}
	for _, kind := range r.Query.Types {
		n += int64(64 + len(kind))
	}
	for _, pair := range r.Query.SubTypes {
		n += int64(64 + len(pair.Type) + len(pair.SubType))
	}
	for _, m := range r.Messages {
		n += int64(192 + len(m.ID) + len(m.TenantID) + len(m.ScopeKind) + len(m.DomainID) + len(m.Type) + len(m.SubType) + len(m.Title) + len(m.Description))
	}
	return n + stateBytesForTest(r.ReadState)
}

func stateBytesForTest(s ReadState) int64 {
	n := int64(len(s.TenantID) + len(s.UserID))
	for id := range s.FirstReadAt {
		n += int64(64 + len(id))
	}
	return n
}

func markBytesForTest(s ReadState, targets []string) int64 {
	n := int64(256) + stateBytesForTest(s)
	for _, id := range targets {
		n += int64(64 + len(id))
	}
	return n
}

func TestRequestByteBudgetExactBoundaryCountsAllInputComponents(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Request)
	}{
		{"base", func(*Request) {}},
		{"tenant_identity", func(r *Request) { r.Scope.TenantID += "-extra"; r.ReadState.TenantID = r.Scope.TenantID }},
		{"user_identity", func(r *Request) { r.Scope.UserID += "-extra"; r.ReadState.UserID = r.Scope.UserID }},
		{"scope_domain", func(r *Request) { r.Scope.DomainIDs = append(r.Scope.DomainIDs, "unmatched-domain") }},
		{"catalog_pair", func(r *Request) { r.Catalog = append(r.Catalog, TypeSubType{Type: "new-type", SubType: "new-subtype"}) }},
		{"query_types", func(r *Request) { r.Query.Types = []string{"security", "audit"} }},
		{"query_subtypes", func(r *Request) { r.Query.SubTypes = []TypeSubType{{Type: "security", SubType: "login"}} }},
		{"keyword_raw_utf8_bytes", func(r *Request) { r.Query.Keyword = "中文é" }},
		{"descending_sort", func(r *Request) { r.Query.Sort = SortDescending }},
		{"unread_filter", func(r *Request) { r.Query.ReadFilter = ReadUnread }},
		{"message_id", func(r *Request) { r.Messages[0].ID += "-extended-id" }},
		{"message_tenant", func(r *Request) { r.Messages[0].TenantID += "-outside-scope" }},
		{"message_domain", func(r *Request) { r.Messages[0].DomainID += "-outside-scope" }},
		{"message_platform_kind", func(r *Request) { r.Messages[0].ScopeKind, r.Messages[0].DomainID = ScopePlatform, "" }},
		{"message_type_and_subtype", func(r *Request) { r.Messages[0].Type, r.Messages[0].SubType = "audit", "export" }},
		{"message_title", func(r *Request) { r.Messages[0].Title += strings.Repeat("T", 257) }},
		{"message_description", func(r *Request) { r.Messages[0].Description += strings.Repeat("D", 257) }},
		{"message_multibyte_string", func(r *Request) { r.Messages[0].Description = "中文é🧪" }},
		{"read_record_outside_result", func(r *Request) { r.ReadState.FirstReadAt["unrelated-extra-record"] = testInstant() }},
		{"empty_message_batch", func(r *Request) { r.Messages = nil }},
		{"nil_read_map", func(r *Request) { r.ReadState.FirstReadAt = nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			in := testRequest()
			test.change(&in)
			unbounded := mustResult(t, in)
			in.Limits.MaxInputBytes = requestBytesForTest(in)
			atLimit := mustResult(t, in)
			if !reflect.DeepEqual(atLimit.Members(), unbounded.Members()) || atLimit.Counts() != unbounded.Counts() {
				t.Fatal("exact budget silently truncated or changed result")
			}
			in.Limits.MaxInputBytes--
			r, err := NewResult(in)
			requireCode(t, err, BudgetExceeded)
			if r != nil {
				t.Fatal("byte budget violation returned partial result")
			}
		})
	}
}

func TestInputMessageBudgetIncludesExcludedMessagesAndNeverTruncates(t *testing.T) {
	in := testRequest()
	in.Messages[3].TenantID = "outside-tenant"
	in.Limits.MaxInputMessages = len(in.Messages)
	r := mustResult(t, in)
	requireIDs(t, r.Members(), "a", "b", "c")
	in.Limits.MaxInputMessages--
	r, err := NewResult(in)
	requireCode(t, err, BudgetExceeded)
	if r != nil {
		t.Fatal("message limit returned truncated result")
	}
}

func TestByteBudgetCheckedBeforeStructureAndDoesNotIgnoreEmptyElements(t *testing.T) {
	in := testRequest()
	in.Limits.MaxInputBytes = 255
	in.Catalog = nil
	_, err := NewResult(in)
	requireCode(t, err, BudgetExceeded)
	in = testRequest()
	in.Scope.DomainIDs = make([]string, 64)
	in.Limits.MaxInputBytes = requestBytesForTest(in) - 1
	_, err = NewResult(in)
	requireCode(t, err, BudgetExceeded)
	// Budget success is not semantic success: empty IDs remain invalid.
	in.Limits.MaxInputBytes++
	_, err = NewResult(in)
	requireCode(t, err, InvalidScope)
}

func TestMarkTargetBudgetIsIndependentOfPageAndInputLimits(t *testing.T) {
	in := testRequest()
	in.Query.Limit, in.Limits.MaxPageSize, in.Limits.MaxMarkTargets = 1, 1, 3
	r := mustResult(t, in)
	if r.Total() != 4 || len(r.IDs()) != 4 {
		t.Fatal("target budget truncated result membership")
	}
	marked, err := r.MarkRead(in.ReadState, []string{"a", "b", "c"}, testInstant())
	if err != nil || marked.Progress != (Progress{Target: 3, Processed: 3, NewlyRead: 2, AlreadyRead: 1}) {
		t.Fatalf("off-page multi-target mark=%#v,%v", marked, err)
	}
	before := copyStateForTest(in.ReadState)
	marked, err = r.MarkRead(in.ReadState, r.IDs(), testInstant())
	requireCode(t, err, BudgetExceeded)
	if !reflect.DeepEqual(marked, MarkResult{}) || !reflect.DeepEqual(in.ReadState, before) {
		t.Fatal("target budget failure partially marked input")
	}
	in.Limits.MaxMarkTargets = 4
	marked, err = mustResult(t, in).MarkRead(in.ReadState, []string{"a", "b", "c", "d"}, testInstant())
	if err != nil || marked.Progress.Target != 4 || marked.Progress.Processed != 4 {
		t.Fatalf("exact target count limit failed: %#v, %v", marked, err)
	}
}

func TestMarkByteBudgetRechecksCurrentStateAndTargets(t *testing.T) {
	in := testRequest()
	in.Limits.MaxInputBytes = requestBytesForTest(in)
	r := mustResult(t, in)
	for _, targets := range [][]string{nil, {"a"}, {"a", "b", "c", "d"}} {
		current := ReadState{TenantID: in.Scope.TenantID, UserID: in.Scope.UserID, FirstReadAt: map[string]time.Time{}}
		idBytes := int(in.Limits.MaxInputBytes - markBytesForTest(current, targets) - 64)
		if idBytes < 1 {
			t.Fatal("fixture lacks room for a current-state record")
		}
		id := strings.Repeat("x", idBytes)
		current.FirstReadAt[id] = testInstant().Add(-time.Hour)
		if markBytesForTest(current, targets) != in.Limits.MaxInputBytes {
			t.Fatal("incorrect test budget fixture")
		}
		before := copyStateForTest(current)
		marked, err := r.MarkRead(current, targets, testInstant())
		if err != nil || marked.Progress.Processed != len(targets) {
			t.Fatalf("exact mark byte boundary=%#v,%v", marked, err)
		}
		if _, ok := marked.State.FirstReadAt[id]; !ok {
			t.Fatal("mark dropped unrelated current state to satisfy budget")
		}
		delete(current.FirstReadAt, id)
		current.FirstReadAt[id+"x"] = before.FirstReadAt[id]
		before = copyStateForTest(current)
		marked, err = r.MarkRead(current, targets, testInstant())
		requireCode(t, err, BudgetExceeded)
		if !reflect.DeepEqual(marked, MarkResult{}) || !reflect.DeepEqual(current, before) {
			t.Fatal("mark byte rejection returned partial output or mutated input")
		}
	}
	// Unknown and empty targets consume bytes before target validation as well.
	marked, err := r.MarkRead(in.ReadState, []string{strings.Repeat("target", int(in.Limits.MaxInputBytes))}, testInstant())
	requireCode(t, err, BudgetExceeded)
	if !reflect.DeepEqual(marked, MarkResult{}) {
		t.Fatal("oversized target returned partial mark")
	}
}

func TestByteBudgetNearMaxInt64CannotOverflow(t *testing.T) {
	const maxInt64 = int64(^uint64(0) >> 1)
	in := testRequest()
	in.Limits.MaxInputBytes = maxInt64
	r := mustResult(t, in)
	requireIDs(t, r.Members(), "a", "b", "c", "d")
	if _, err := r.MarkRead(in.ReadState, r.IDs(), testInstant()); err != nil {
		t.Fatal(err)
	}
	budget := byteBudget{remaining: maxInt64}
	if err := budget.add(maxInt64 - 1); err != nil {
		t.Fatal(err)
	}
	if err := budget.add(1); err != nil || budget.remaining != 0 {
		t.Fatalf("exact max accounting = %d, %v", budget.remaining, err)
	}
	requireCode(t, budget.add(1), BudgetExceeded)
	if budget.remaining != 0 {
		t.Fatal("rejected addition mutated remaining budget")
	}
}
