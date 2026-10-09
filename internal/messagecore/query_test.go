package messagecore

import (
	"reflect"
	"testing"
	"time"
)

func TestScopeExplicitlySeparatesPlatformDomainsAndTenants(t *testing.T) {
	in := testRequest()
	in.Messages = []Message{
		testMessage("domain-a", testInstant().Add(-time.Hour)),
		testMessage("domain-b", testInstant().Add(-time.Hour)),
		testMessage("platform", testInstant().Add(-time.Hour)),
		testMessage("other-tenant-domain", testInstant().Add(-time.Hour)),
		testMessage("other-tenant-platform", testInstant().Add(-time.Hour)),
	}
	in.Messages[1].DomainID = "domain-b"
	in.Messages[2].ScopeKind, in.Messages[2].DomainID = ScopePlatform, ""
	in.Messages[3].TenantID = "tenant-b"
	in.Messages[4].TenantID, in.Messages[4].ScopeKind, in.Messages[4].DomainID = "tenant-b", ScopePlatform, ""
	cases := []struct {
		name     string
		domains  []string
		platform bool
		want     []string
	}{
		{"no_grants", nil, false, nil},
		{"empty_domain_slice", []string{}, false, nil},
		{"domain_a_only", []string{"domain-a"}, false, []string{"domain-a"}},
		{"domain_b_only", []string{"domain-b"}, false, []string{"domain-b"}},
		{"platform_only", nil, true, []string{"platform"}},
		{"platform_and_domain", []string{"domain-a"}, true, []string{"domain-a", "platform"}},
		{"all_explicit", []string{"domain-a", "domain-b"}, true, []string{"domain-a", "domain-b", "platform"}},
		{"unknown_domain_is_no_grant", []string{"missing"}, false, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := in
			request.Scope.DomainIDs, request.Scope.IncludePlatform = test.domains, test.platform
			r := mustResult(t, request)
			requireIDs(t, r.Members(), test.want...)
			if r.Total() != len(test.want) || r.Counts() != (Counts{All: len(test.want), Unread: len(test.want)}) {
				t.Fatalf("unexpected scoped totals: total=%d counts=%#v", r.Total(), r.Counts())
			}
		})
	}
}

func TestDefaultWindowIsConfirmedUTC168HoursAndHalfOpen(t *testing.T) {
	in := testRequest()
	in.ConfirmedAt = time.Date(2026, time.October, 8, 20, 0, 0, 123, time.FixedZone("caller", 8*60*60))
	end := in.ConfirmedAt.UTC()
	start := end.Add(-168 * time.Hour)
	in.Messages = []Message{
		testMessage("before-start", start.Add(-time.Nanosecond)),
		testMessage("at-start", start),
		testMessage("after-start", start.Add(time.Nanosecond)),
		testMessage("before-end", end.Add(-time.Nanosecond)),
		testMessage("at-end", end),
		testMessage("after-end", end.Add(time.Nanosecond)),
	}
	// Ingestion time is independent and must not affect membership or ordering.
	in.Messages[1].IngestedAt = end.Add(365 * 24 * time.Hour)
	in.Messages[2].IngestedAt = start.Add(-365 * 24 * time.Hour)
	r := mustResult(t, in)
	requireIDs(t, r.Members(), "at-start", "after-start", "before-end")
	q := r.Query()
	if !q.Start.Equal(start) || !q.End.Equal(end) || q.End.Sub(q.Start) != 168*time.Hour {
		t.Fatalf("default window = [%v, %v), want [%v, %v)", q.Start, q.End, start, end)
	}
	if q.Start.Location() != time.UTC || q.End.Location() != time.UTC || r.ConfirmedAt().Location() != time.UTC {
		t.Fatal("query confirmation and endpoints must be canonical UTC")
	}
	if !r.ConfirmedAt().Equal(in.ConfirmedAt) {
		t.Fatal("confirmation instant changed during UTC normalization")
	}
	if !in.Query.Start.IsZero() || !in.Query.End.IsZero() {
		t.Fatal("default range mutated the caller query")
	}
}

func TestExplicitRangeUsesOccurredAtAndPreservesCallerEndpoints(t *testing.T) {
	in := testRequest()
	zone := time.FixedZone("caller", -7*60*60)
	in.Query.Start = testInstant().Add(-2 * time.Hour).In(zone)
	in.Query.End = testInstant().Add(-time.Hour).In(zone)
	in.Messages[0].IngestedAt = testInstant().Add(-90 * time.Minute)
	in.Messages[1].IngestedAt = testInstant().Add(-400 * time.Hour)
	in.Messages[2].IngestedAt = testInstant().Add(400 * time.Hour)
	r := mustResult(t, in)
	requireIDs(t, r.Members(), "b", "c")
	if r.Query().Start.Location() != time.UTC || r.Query().End.Location() != time.UTC {
		t.Fatal("explicit range is not normalized to UTC")
	}
	if in.Query.Start.Location() != zone || in.Query.End.Location() != zone {
		t.Fatal("explicit range mutated the caller's time values")
	}
}

func TestCatalogFiltersUseFullyQualifiedSubtypePairs(t *testing.T) {
	in := testRequest()
	in.Messages[0].Type, in.Messages[0].SubType = "security", "shared"
	in.Messages[1].Type, in.Messages[1].SubType = "audit", "shared"
	in.Messages[2].Type, in.Messages[2].SubType = "audit", "export"
	cases := []struct {
		name  string
		types []string
		pairs []TypeSubType
		want  []string
	}{
		{"empty_means_all", nil, nil, []string{"a", "b", "c", "d"}},
		{"one_type", []string{"audit"}, nil, []string{"b", "c"}},
		{"type_union", []string{"audit", "security"}, nil, []string{"a", "b", "c", "d"}},
		{"exact_security_pair", nil, []TypeSubType{{Type: "security", SubType: "shared"}}, []string{"a"}},
		{"exact_audit_pair", nil, []TypeSubType{{Type: "audit", SubType: "shared"}}, []string{"b"}},
		{"pair_union", nil, []TypeSubType{{Type: "audit", SubType: "shared"}, {Type: "security", SubType: "shared"}}, []string{"a", "b"}},
		{"type_and_pair_intersection", []string{"audit"}, []TypeSubType{{Type: "audit", SubType: "export"}}, []string{"c"}},
		{"pair_restricts_multiple_selected_types", []string{"audit", "security"}, []TypeSubType{{Type: "audit", SubType: "shared"}}, []string{"b"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := in
			request.Query.Types, request.Query.SubTypes = test.types, test.pairs
			r := mustResult(t, request)
			requireIDs(t, r.Members(), test.want...)
			if r.Counts().All != len(test.want) {
				t.Fatalf("filtered counts = %#v", r.Counts())
			}
		})
	}
}

func TestKeywordIsLiteralCaseSensitiveTitleSubstring(t *testing.T) {
	in := testRequest()
	in.Messages[0].Title = "ALERT [.*] 100% _ exact"
	in.Messages[1].Title = "alert [abc] 100x z exact"
	in.Messages[2].Title, in.Messages[2].Description = "ordinary", "ALERT [.*] 100% _ exact"
	in.Messages[3].Title = "message 中文标题"
	cases := []struct {
		keyword string
		want    []string
	}{
		{"", []string{"a", "b", "c", "d"}},
		{"ALERT", []string{"a"}},
		{"alert", []string{"b"}},
		{"Alert", nil},
		{"[.*]", []string{"a"}},
		{"%", []string{"a"}},
		{"_", []string{"a"}},
		{"100% _", []string{"a"}},
		{"中文", []string{"d"}},
		{" ALERT", nil},
		{"description-only", nil},
	}
	in.Messages[2].Description += " description-only"
	for _, test := range cases {
		t.Run(test.keyword, func(t *testing.T) {
			request := in
			request.Query.Keyword = test.keyword
			requireIDs(t, mustResult(t, request).Members(), test.want...)
		})
	}
}

func TestCountsIgnoreOnlyReadFilterAndMembershipIsStable(t *testing.T) {
	for _, test := range []struct {
		filter ReadFilter
		want   []string
	}{
		{ReadAll, []string{"a", "b", "c", "d"}},
		{ReadUnread, []string{"a", "c"}},
		{ReadRead, []string{"b", "d"}},
	} {
		t.Run(string(test.filter), func(t *testing.T) {
			in := testRequest()
			in.Query.ReadFilter, in.Query.Offset, in.Query.Limit = test.filter, 1, 1
			r := mustResult(t, in)
			wantCounts := Counts{All: 4, Unread: 2, Read: 2}
			if r.Counts() != wantCounts || r.Total() != len(test.want) {
				t.Fatalf("counts=%#v total=%d", r.Counts(), r.Total())
			}
			requireIDs(t, r.Base(), "a", "b", "c", "d")
			requireIDs(t, r.Members(), test.want...)
			page, err := r.List()
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != len(test.want) || page.Counts != wantCounts || page.Offset != 1 || page.Limit != 1 {
				t.Fatalf("page metadata = %#v", page)
			}
			requireIDs(t, page.Items, test.want[1])
			for _, entry := range r.Base() {
				first, read := in.ReadState.FirstReadAt[entry.Message.ID]
				if entry.Read != read || !entry.FirstReadAt.Equal(first) {
					t.Errorf("entry read metadata = %#v", entry)
				}
			}
		})
	}
	in := testRequest()
	in.Query.Keyword = "Message b"
	in.Query.ReadFilter = ReadUnread
	r := mustResult(t, in)
	requireIDs(t, r.Base(), "b")
	requireIDs(t, r.Members())
	if r.Counts() != (Counts{All: 1, Read: 1}) || r.Total() != 0 {
		t.Fatalf("counts should retain other filters: %#v", r.Counts())
	}
}

func TestSortingIsTimeThenAscendingIDForBothDirections(t *testing.T) {
	for _, test := range []struct {
		sort Sort
		want []string
	}{
		{SortAscending, []string{"a", "b", "c", "d"}},
		{SortDescending, []string{"d", "b", "c", "a"}},
	} {
		t.Run(string(test.sort), func(t *testing.T) {
			in := testRequest()
			in.Query.Sort = test.sort
			in.Messages = []Message{in.Messages[2], in.Messages[3], in.Messages[1], in.Messages[0]}
			r := mustResult(t, in)
			requireIDs(t, r.Members(), test.want...)
			in.Messages[0], in.Messages[3] = in.Messages[3], in.Messages[0]
			requireIDs(t, mustResult(t, in).Members(), test.want...)
			for i, id := range test.want {
				p, err := r.Page(i, 1)
				if err != nil {
					t.Fatal(err)
				}
				requireIDs(t, p.Items, id)
			}
		})
	}
}

func TestPaginationBoundsLargeOffsetsAndOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, maximum := range []int{1, 2, 200} {
		in := testRequest()
		in.Limits.MaxPageSize, in.Query.Limit = maximum, maximum
		r := mustResult(t, in)
		for _, offset := range []int{0, 1, 3, 4, 5, maxInt - 1, maxInt} {
			p, err := r.Page(offset, maximum)
			if err != nil {
				t.Fatalf("Page(%d,%d): %v", offset, maximum, err)
			}
			if p.Items == nil {
				t.Fatalf("Page(%d,%d) returned nil items", offset, maximum)
			}
			if p.Total != 4 || p.Counts != (Counts{All: 4, Read: 2, Unread: 2}) || p.Offset != offset || p.Limit != maximum {
				t.Fatalf("incorrect page metadata: %#v", p)
			}
			want := []string{"a", "b", "c", "d"}
			if offset >= len(want) {
				want = nil
			} else {
				want = want[offset:]
				if len(want) > maximum {
					want = want[:maximum]
				}
			}
			requireIDs(t, p.Items, want...)
		}
		for _, pagination := range [][2]int{{-1, 1}, {0, 0}, {0, -1}, {0, maximum + 1}, {0, maxInt}} {
			p, err := r.Page(pagination[0], pagination[1])
			requireCode(t, err, InvalidPagination)
			if !reflect.DeepEqual(p, Page{}) {
				t.Fatalf("invalid page returned %#v", p)
			}
		}
		in.Query.Offset = maxInt
		p, err := mustResult(t, in).List()
		if err != nil || len(p.Items) != 0 {
			t.Fatalf("large initial offset: %#v, %v", p, err)
		}
	}
}

func TestLocateUsesWholeFixedReadFilteredOrder(t *testing.T) {
	in := testRequest()
	in.Query.Sort, in.Query.ReadFilter, in.Query.Offset, in.Query.Limit = SortDescending, ReadUnread, 1, 1
	r := mustResult(t, in)
	requireIDs(t, r.Members(), "c", "a")
	for _, test := range []struct {
		id   string
		size int
		want Location
	}{
		{"c", 1, Location{Found: true, Index: 0, Page: 1}},
		{"a", 1, Location{Found: true, Index: 1, Page: 2}},
		{"a", 2, Location{Found: true, Index: 1, Page: 1}},
		{"a", 200, Location{Found: true, Index: 1, Page: 1}},
		{"b", 1, Location{Index: -1}},
		{"outside", 1, Location{Index: -1}},
		{"missing", 1, Location{Index: -1}},
	} {
		location, err := r.Locate(test.id, test.size)
		if err != nil || location != test.want {
			t.Errorf("Locate(%q,%d)=%#v,%v, want %#v", test.id, test.size, location, err, test.want)
		}
	}
	for _, size := range []int{-1, 0, 201} {
		location, err := r.Locate("a", size)
		requireCode(t, err, InvalidPagination)
		if location != (Location{Index: -1}) {
			t.Fatalf("invalid Locate = %#v", location)
		}
	}
	location, err := r.Locate("", 1)
	requireCode(t, err, InvalidTarget)
	if location != (Location{Index: -1}) {
		t.Fatalf("empty Locate = %#v", location)
	}
	_, err = r.MarkRead(in.ReadState, []string{"c", "a"}, testInstant())
	if err != nil {
		t.Fatal(err)
	}
	location, err = r.Locate("a", 1)
	if err != nil || location != (Location{Found: true, Index: 1, Page: 2}) {
		t.Fatalf("mark changed fixed location: %#v, %v", location, err)
	}
}

func TestEmptyInputAndEmptyFieldsAreValidWhereContractAllows(t *testing.T) {
	in := testRequest()
	in.Messages = nil
	in.ReadState.FirstReadAt = nil
	r := mustResult(t, in)
	if r.Total() != 0 || r.Counts() != (Counts{}) {
		t.Fatalf("empty result counts = %#v", r.Counts())
	}
	if r.Base() == nil || r.Members() == nil || r.IDs() == nil || r.ReadSnapshot().FirstReadAt == nil {
		t.Fatal("empty exports should be usable independent empty containers")
	}
	p, err := r.List()
	if err != nil || len(p.Items) != 0 || p.Total != 0 {
		t.Fatalf("empty List=%#v,%v", p, err)
	}
	in = testRequest()
	in.Messages[0].Title, in.Messages[0].Description = "", ""
	in.Messages[0].IngestedAt = in.Messages[0].OccurredAt.Add(-time.Hour)
	r = mustResult(t, in)
	requireIDs(t, r.Members(), "a", "b", "c", "d")
}
