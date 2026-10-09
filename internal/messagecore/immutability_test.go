package messagecore

import (
	"reflect"
	"testing"
	"time"
)

func copyRequestForTest(in Request) Request {
	out := in
	out.Scope.DomainIDs = append([]string(nil), in.Scope.DomainIDs...)
	out.Messages = append([]Message(nil), in.Messages...)
	out.Catalog = append(Catalog(nil), in.Catalog...)
	out.Query.Types = append([]string(nil), in.Query.Types...)
	out.Query.SubTypes = append([]TypeSubType(nil), in.Query.SubTypes...)
	out.ReadState = copyStateForTest(in.ReadState)
	return out
}

type resultExportsForTest struct {
	Scope       Scope
	Query       Query
	State       ReadState
	Base        []Entry
	Members     []Entry
	IDs         []string
	List        Page
	Page        Page
	Counts      Counts
	Limits      Limits
	ConfirmedAt time.Time
	Total       int
}

func resultExports(t *testing.T, r *Result) resultExportsForTest {
	t.Helper()
	list, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.Page(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	return resultExportsForTest{
		Scope: r.Scope(), Query: r.Query(), State: r.ReadSnapshot(), Base: r.Base(), Members: r.Members(),
		IDs: r.IDs(), List: list, Page: page, Counts: r.Counts(), Limits: r.Limits(), ConfirmedAt: r.ConfirmedAt(), Total: r.Total(),
	}
}

func TestConstructionNeverMutatesInputsAndRetainsNoMutableInputAliases(t *testing.T) {
	in := testRequest()
	in.Query.Types = []string{"security"}
	in.Query.SubTypes = []TypeSubType{{Type: "security", SubType: "login"}}
	in.Query.Offset = 1
	before := copyRequestForTest(in)
	r := mustResult(t, in)
	if !reflect.DeepEqual(in, before) {
		t.Fatal("constructor mutated input")
	}
	want := resultExports(t, r)
	in.Scope.TenantID, in.Scope.UserID, in.Scope.DomainIDs[0], in.Scope.IncludePlatform = "mutated", "mutated", "mutated", true
	in.Messages[0] = Message{ID: "mutated", Title: "mutated", Description: "mutated"}
	in.Catalog[0] = TypeSubType{Type: "mutated", SubType: "mutated"}
	in.Query.Types[0] = "mutated"
	in.Query.SubTypes[0] = TypeSubType{Type: "mutated", SubType: "mutated"}
	in.Query.Start, in.Query.End, in.Query.Keyword, in.Query.Sort = time.Time{}, time.Time{}, "mutated", SortDescending
	in.ReadState.TenantID, in.ReadState.UserID = "mutated", "mutated"
	in.ReadState.FirstReadAt["a"] = testInstant()
	delete(in.ReadState.FirstReadAt, "b")
	in.ConfirmedAt = time.Time{}
	in.Limits = Limits{}
	if got := resultExports(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller input mutation changed result:\ngot %#v\nwant %#v", got, want)
	}

	invalid := testRequest()
	invalid.Messages[3].ID = invalid.Messages[0].ID
	before = copyRequestForTest(invalid)
	if _, err := NewResult(invalid); err == nil {
		t.Fatal("invalid fixture unexpectedly accepted")
	}
	if !reflect.DeepEqual(invalid, before) {
		t.Fatal("failed constructor mutated input")
	}
}

func TestEveryExportedSliceAndMapIsAnIndependentCopy(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*testing.T, *Result)
	}{
		{"scope_domains", func(_ *testing.T, r *Result) { s := r.Scope(); s.DomainIDs[0] = "mutated"; s.TenantID = "mutated" }},
		{"query_types", func(_ *testing.T, r *Result) { q := r.Query(); q.Types[0] = "mutated"; q.Offset = 200 }},
		{"query_subtypes", func(_ *testing.T, r *Result) {
			q := r.Query()
			q.SubTypes[0].SubType = "mutated"
			q.Sort = SortDescending
		}},
		{"read_snapshot_map", func(_ *testing.T, r *Result) {
			s := r.ReadSnapshot()
			delete(s.FirstReadAt, "b")
			s.FirstReadAt["a"] = testInstant()
			s.UserID = "mutated"
		}},
		{"base_entries", func(_ *testing.T, r *Result) {
			entries := r.Base()
			entries[0].Read = true
			entries[0].Message.Title = "mutated"
			entries[0].FirstReadAt = testInstant()
		}},
		{"member_entries", func(_ *testing.T, r *Result) {
			entries := r.Members()
			entries[0].Message.ID = "mutated"
			entries[0].Message.OccurredAt = time.Time{}
		}},
		{"member_ids", func(_ *testing.T, r *Result) { ids := r.IDs(); ids[0] = "mutated" }},
		{"list_items", func(t *testing.T, r *Result) {
			p, err := r.List()
			if err != nil {
				t.Fatal(err)
			}
			p.Items[0].Message.Description = "mutated"
			p.Total = 0
			p.Counts.All = 0
		}},
		{"page_items", func(t *testing.T, r *Result) {
			p, err := r.Page(0, 1)
			if err != nil {
				t.Fatal(err)
			}
			p.Items[0].Message.DomainID = "mutated"
			p.Items[0].Read = true
			p.Offset = 100
		}},
		{"counts_value", func(_ *testing.T, r *Result) { counts := r.Counts(); counts.All = -1 }},
		{"limits_value", func(_ *testing.T, r *Result) { limits := r.Limits(); limits.MaxPageSize = 1; limits.MaxInputBytes = 1 }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			in := testRequest()
			in.Query.Types = []string{"security"}
			in.Query.SubTypes = []TypeSubType{{Type: "security", SubType: "login"}}
			in.Query.Offset = 1
			r := mustResult(t, in)
			want := resultExports(t, r)
			mutation.mutate(t, r)
			if got := resultExports(t, r); !reflect.DeepEqual(got, want) {
				t.Fatalf("export mutation changed snapshot: got %#v, want %#v", got, want)
			}
			// A sibling exported copy must not alias another exported copy either.
			first, second := r.ReadSnapshot(), r.ReadSnapshot()
			first.FirstReadAt["extra"] = testInstant()
			if _, exists := second.FirstReadAt["extra"]; exists {
				t.Fatal("independent exports alias each other")
			}
		})
	}
}

func TestEmptyExportedSelectionSlicesAreNonNil(t *testing.T) {
	in := testRequest()
	in.Scope.DomainIDs, in.Query.Types, in.Query.SubTypes = nil, nil, nil
	r := mustResult(t, in)
	if r.Scope().DomainIDs == nil || r.Query().Types == nil || r.Query().SubTypes == nil {
		t.Fatal("empty Scope and Query slices must be nonnil")
	}
}

func TestAllRetainedInstantsAreCanonicalUTCWithoutMonotonicClock(t *testing.T) {
	in := testRequest()
	// Only this test reads a clock; the production result must use these supplied
	// instants, and its exported values must discard monotonic process metadata.
	now := time.Now()
	in.ConfirmedAt = now
	for i := range in.Messages {
		in.Messages[i].OccurredAt = now.Add(-time.Duration(i+1) * time.Hour)
		in.Messages[i].IngestedAt = now.Add(time.Duration(i+1) * time.Hour)
	}
	for id := range in.ReadState.FirstReadAt {
		in.ReadState.FirstReadAt[id] = now.Add(-time.Minute)
	}
	r := mustResult(t, in)
	if r.ConfirmedAt() != now.Round(0).UTC() {
		t.Fatal("confirmation retained location or monotonic metadata")
	}
	for _, e := range r.Members() {
		if e.Message.OccurredAt != e.Message.OccurredAt.Round(0).UTC() || e.Message.IngestedAt != e.Message.IngestedAt.Round(0).UTC() {
			t.Fatal("message timestamps are not canonical")
		}
		if e.Read && e.FirstReadAt != e.FirstReadAt.Round(0).UTC() {
			t.Fatal("entry first-read timestamp is not canonical")
		}
	}
	for _, at := range r.ReadSnapshot().FirstReadAt {
		if at != at.Round(0).UTC() {
			t.Fatal("read snapshot timestamp is not canonical")
		}
	}
	marked, err := r.MarkRead(in.ReadState, []string{"a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range marked.State.FirstReadAt {
		if at != at.Round(0).UTC() {
			t.Fatal("mark timestamp is not canonical")
		}
	}
	if marked.State.FirstReadAt["a"] != now.Round(0).UTC() {
		t.Fatal("mark instant changed")
	}
}
