package messagecore

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testInstant() time.Time {
	return time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
}

func testMessage(id string, at time.Time) Message {
	return Message{
		ID: id, TenantID: "tenant-a", ScopeKind: ScopeDomain, DomainID: "domain-a",
		Type: "security", SubType: "login", Title: "Message " + id, Description: "Sanitized summary",
		OccurredAt: at, IngestedAt: at.Add(time.Minute),
	}
}

func testRequest() Request {
	now := testInstant()
	return Request{
		Scope: Scope{TenantID: "tenant-a", UserID: "user-a", DomainIDs: []string{"domain-a"}},
		Messages: []Message{
			testMessage("a", now.Add(-3*time.Hour)),
			testMessage("b", now.Add(-2*time.Hour)),
			testMessage("c", now.Add(-2*time.Hour)),
			testMessage("d", now.Add(-time.Hour)),
		},
		Catalog: Catalog{
			{Type: "security", SubType: "login"},
			{Type: "security", SubType: "shared"},
			{Type: "audit", SubType: "shared"},
			{Type: "audit", SubType: "export"},
		},
		ReadState: ReadState{TenantID: "tenant-a", UserID: "user-a", FirstReadAt: map[string]time.Time{
			"b": now.Add(-90 * time.Minute), "d": now.Add(-30 * time.Minute), "outside": now.Add(-24 * time.Hour),
		}},
		Query:       Query{ReadFilter: ReadAll, Sort: SortAscending, Limit: 2},
		ConfirmedAt: now,
		Limits:      Limits{MaxPageSize: 200, MaxInputMessages: 1000, MaxInputBytes: 1 << 20, MaxMarkTargets: 200},
	}
}

func mustResult(t *testing.T, in Request) *Result {
	t.Helper()
	r, err := NewResult(in)
	if err != nil {
		t.Fatalf("NewResult() error = %v", err)
	}
	if r == nil {
		t.Fatal("NewResult() succeeded with a nil result")
	}
	return r
}

func requireCode(t *testing.T, err error, code Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", code)
	}
	var detail *Error
	if !errors.As(err, &detail) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if detail.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
	if detail.Field == "" {
		t.Fatalf("error has empty field: %#v", detail)
	}
}

func entryIDs(entries []Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.Message.ID
	}
	return ids
}

func requireIDs(t *testing.T, entries []Entry, want ...string) {
	t.Helper()
	got := entryIDs(entries)
	if len(got) != len(want) {
		t.Fatalf("IDs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("IDs = %v, want %v", got, want)
		}
	}
}

func copyStateForTest(s ReadState) ReadState {
	out := ReadState{TenantID: s.TenantID, UserID: s.UserID}
	if s.FirstReadAt != nil {
		out.FirstReadAt = make(map[string]time.Time, len(s.FirstReadAt))
		for id, at := range s.FirstReadAt {
			out.FirstReadAt[id] = at
		}
	}
	return out
}

func TestNewResultRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Request)
		code   Code
	}{
		{"page_policy_zero", func(r *Request) { r.Limits.MaxPageSize = 0 }, InvalidPolicy},
		{"page_policy_negative", func(r *Request) { r.Limits.MaxPageSize = -1 }, InvalidPolicy},
		{"page_policy_over_200", func(r *Request) { r.Limits.MaxPageSize = 201 }, InvalidPolicy},
		{"message_budget_zero", func(r *Request) { r.Limits.MaxInputMessages = 0 }, InvalidPolicy},
		{"message_budget_negative", func(r *Request) { r.Limits.MaxInputMessages = -1 }, InvalidPolicy},
		{"byte_budget_zero", func(r *Request) { r.Limits.MaxInputBytes = 0 }, InvalidPolicy},
		{"byte_budget_negative", func(r *Request) { r.Limits.MaxInputBytes = -1 }, InvalidPolicy},
		{"mark_budget_zero", func(r *Request) { r.Limits.MaxMarkTargets = 0 }, InvalidPolicy},
		{"mark_budget_negative", func(r *Request) { r.Limits.MaxMarkTargets = -1 }, InvalidPolicy},
		{"catalog_nil", func(r *Request) { r.Catalog = nil }, InvalidCatalog},
		{"catalog_empty", func(r *Request) { r.Catalog = Catalog{} }, InvalidCatalog},
		{"catalog_empty_type", func(r *Request) { r.Catalog[0].Type = "" }, InvalidCatalog},
		{"catalog_empty_subtype", func(r *Request) { r.Catalog[0].SubType = "" }, InvalidCatalog},
		{"catalog_duplicate_pair", func(r *Request) { r.Catalog = append(r.Catalog, r.Catalog[0]) }, InvalidCatalog},
		{"scope_empty_tenant", func(r *Request) { r.Scope.TenantID = "" }, InvalidScope},
		{"scope_empty_user", func(r *Request) { r.Scope.UserID = "" }, InvalidScope},
		{"scope_empty_domain", func(r *Request) { r.Scope.DomainIDs = []string{""} }, InvalidScope},
		{"scope_duplicate_domain", func(r *Request) { r.Scope.DomainIDs = []string{"domain-a", "domain-a"} }, InvalidScope},
		{"read_state_empty_tenant", func(r *Request) { r.ReadState.TenantID = "" }, InvalidReadState},
		{"read_state_empty_user", func(r *Request) { r.ReadState.UserID = "" }, InvalidReadState},
		{"read_state_other_tenant", func(r *Request) { r.ReadState.TenantID = "tenant-b" }, InvalidReadState},
		{"read_state_other_user", func(r *Request) { r.ReadState.UserID = "user-b" }, InvalidReadState},
		{"read_state_empty_id", func(r *Request) { r.ReadState.FirstReadAt[""] = testInstant() }, InvalidReadState},
		{"read_state_zero_time", func(r *Request) { r.ReadState.FirstReadAt["unrelated"] = time.Time{} }, InvalidReadState},
		{"confirmed_at_zero", func(r *Request) { r.ConfirmedAt = time.Time{} }, InvalidFilter},
		{"sort_omitted", func(r *Request) { r.Query.Sort = "" }, InvalidFilter},
		{"sort_unknown", func(r *Request) { r.Query.Sort = "latest" }, InvalidFilter},
		{"sort_case_sensitive", func(r *Request) { r.Query.Sort = "ASC" }, InvalidFilter},
		{"read_filter_omitted", func(r *Request) { r.Query.ReadFilter = "" }, InvalidFilter},
		{"read_filter_unknown", func(r *Request) { r.Query.ReadFilter = "pending" }, InvalidFilter},
		{"read_filter_case_sensitive", func(r *Request) { r.Query.ReadFilter = "All" }, InvalidFilter},
		{"only_start", func(r *Request) { r.Query.Start = testInstant().Add(-time.Hour) }, InvalidFilter},
		{"only_end", func(r *Request) { r.Query.End = testInstant() }, InvalidFilter},
		{"equal_endpoints", func(r *Request) { r.Query.Start, r.Query.End = testInstant(), testInstant() }, InvalidFilter},
		{"reversed_endpoints", func(r *Request) { r.Query.Start, r.Query.End = testInstant(), testInstant().Add(-time.Hour) }, InvalidFilter},
		{"negative_offset", func(r *Request) { r.Query.Offset = -1 }, InvalidPagination},
		{"zero_limit", func(r *Request) { r.Query.Limit = 0 }, InvalidPagination},
		{"negative_limit", func(r *Request) { r.Query.Limit = -1 }, InvalidPagination},
		{"limit_over_200", func(r *Request) { r.Query.Limit = 201 }, InvalidPagination},
		{"limit_over_policy", func(r *Request) { r.Limits.MaxPageSize = 1 }, InvalidPagination},
		{"empty_type", func(r *Request) { r.Query.Types = []string{""} }, InvalidFilter},
		{"unknown_type", func(r *Request) { r.Query.Types = []string{"missing"} }, InvalidFilter},
		{"type_case_sensitive", func(r *Request) { r.Query.Types = []string{"Security"} }, InvalidFilter},
		{"duplicate_type", func(r *Request) { r.Query.Types = []string{"security", "security"} }, InvalidFilter},
		{"empty_subtype_type", func(r *Request) { r.Query.SubTypes = []TypeSubType{{SubType: "login"}} }, InvalidFilter},
		{"empty_subtype_name", func(r *Request) { r.Query.SubTypes = []TypeSubType{{Type: "security"}} }, InvalidFilter},
		{"unknown_subtype", func(r *Request) { r.Query.SubTypes = []TypeSubType{{Type: "security", SubType: "missing"}} }, InvalidFilter},
		{"subtype_wrong_pair", func(r *Request) { r.Query.SubTypes = []TypeSubType{{Type: "audit", SubType: "login"}} }, InvalidFilter},
		{"subtype_case_sensitive", func(r *Request) { r.Query.SubTypes = []TypeSubType{{Type: "security", SubType: "Login"}} }, InvalidFilter},
		{"incompatible_subtype", func(r *Request) {
			r.Query.Types = []string{"security"}
			r.Query.SubTypes = []TypeSubType{{Type: "audit", SubType: "shared"}}
		}, InvalidFilter},
		{"duplicate_subtype", func(r *Request) { r.Query.SubTypes = []TypeSubType{r.Catalog[0], r.Catalog[0]} }, InvalidFilter},
		{"message_empty_id", func(r *Request) { r.Messages[0].ID = "" }, InvalidMessage},
		{"message_empty_tenant", func(r *Request) { r.Messages[0].TenantID = "" }, InvalidMessage},
		{"message_zero_occurred", func(r *Request) { r.Messages[0].OccurredAt = time.Time{} }, InvalidMessage},
		{"message_zero_ingested", func(r *Request) { r.Messages[0].IngestedAt = time.Time{} }, InvalidMessage},
		{"message_scope_omitted", func(r *Request) { r.Messages[0].ScopeKind = "" }, InvalidMessage},
		{"message_scope_unknown", func(r *Request) { r.Messages[0].ScopeKind = "all" }, InvalidMessage},
		{"message_scope_case_sensitive", func(r *Request) { r.Messages[0].ScopeKind = "Domain" }, InvalidMessage},
		{"message_domain_empty", func(r *Request) { r.Messages[0].DomainID = "" }, InvalidMessage},
		{"message_platform_has_domain", func(r *Request) { r.Messages[0].ScopeKind = ScopePlatform }, InvalidMessage},
		{"message_empty_type", func(r *Request) { r.Messages[0].Type = "" }, InvalidMessage},
		{"message_empty_subtype", func(r *Request) { r.Messages[0].SubType = "" }, InvalidMessage},
		{"message_unknown_type", func(r *Request) { r.Messages[0].Type = "unknown" }, InvalidMessage},
		{"message_unknown_subtype", func(r *Request) { r.Messages[0].SubType = "unknown" }, InvalidMessage},
		{"message_wrong_pair", func(r *Request) { r.Messages[0].Type = "audit" }, InvalidMessage},
		{"message_duplicate_id", func(r *Request) { r.Messages[1].ID = r.Messages[0].ID }, InvalidMessage},
		{"invalid_other_tenant_message", func(r *Request) { r.Messages[0].TenantID = "tenant-b"; r.Messages[0].IngestedAt = time.Time{} }, InvalidMessage},
		{"invalid_unselected_domain_message", func(r *Request) { r.Messages[0].DomainID = "domain-b"; r.Messages[0].OccurredAt = time.Time{} }, InvalidMessage},
		{"invalid_outside_time_message", func(r *Request) {
			r.Messages[0].OccurredAt = testInstant().Add(-200 * time.Hour)
			r.Messages[0].ScopeKind = "bad"
		}, InvalidMessage},
		{"duplicate_across_tenants", func(r *Request) { r.Messages[1].ID = r.Messages[0].ID; r.Messages[1].TenantID = "tenant-b" }, InvalidMessage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := testRequest()
			test.change(&in)
			r, err := NewResult(in)
			requireCode(t, err, test.code)
			if r != nil {
				t.Fatalf("invalid input returned a partial result: %#v", r)
			}
		})
	}
}

func TestErrorsExposeOnlyStaticCodeAndField(t *testing.T) {
	secret := "SENTINEL-private-identifier-message-content"
	cases := []struct {
		name   string
		change func(*Request)
	}{
		{"scope", func(r *Request) { r.Scope.DomainIDs = []string{secret, secret} }},
		{"message", func(r *Request) { r.Messages[0].Type = secret; r.Messages[0].Title = secret }},
		{"catalog", func(r *Request) {
			r.Catalog = Catalog{{Type: secret, SubType: secret}, {Type: secret, SubType: secret}}
		}},
		{"query", func(r *Request) { r.Query.Types = []string{secret} }},
		{"read_state", func(r *Request) { r.ReadState.UserID = secret }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			in := testRequest()
			test.change(&in)
			_, err := NewResult(in)
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks submitted data: %v", err)
			}
			var detail *Error
			if !errors.As(err, &detail) || strings.Contains(detail.Field, secret) {
				t.Fatalf("error detail leaks values or has unexpected type: %#v", err)
			}
			want := "messagecore: " + string(detail.Code) + ": " + detail.Field
			if err.Error() != want {
				t.Fatalf("error string = %q, want %q", err.Error(), want)
			}
		})
	}
	r := mustResult(t, testRequest())
	_, err := r.MarkRead(r.ReadSnapshot(), []string{secret}, testInstant())
	requireCode(t, err, TargetNotInResult)
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("mark error leaks target: %v", err)
	}
}

func TestNilAndZeroResultsRejectOperations(t *testing.T) {
	for _, r := range []*Result{nil, {}} {
		p, err := r.List()
		requireCode(t, err, InvalidResult)
		if !reflect.DeepEqual(p, Page{}) {
			t.Errorf("invalid List returned %#v", p)
		}
		p, err = r.Page(0, 1)
		requireCode(t, err, InvalidResult)
		if !reflect.DeepEqual(p, Page{}) {
			t.Errorf("invalid Page returned %#v", p)
		}
		location, err := r.Locate("a", 1)
		requireCode(t, err, InvalidResult)
		if location != (Location{Index: -1}) {
			t.Errorf("invalid Locate returned %#v", location)
		}
		marked, err := r.MarkRead(testRequest().ReadState, []string{"a"}, testInstant())
		requireCode(t, err, InvalidResult)
		if !reflect.DeepEqual(marked, MarkResult{}) {
			t.Errorf("invalid MarkRead returned %#v", marked)
		}
	}
}
