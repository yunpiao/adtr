package messagecore

import (
	"reflect"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestDefaultWindowAcrossDSTIsElapsed168HoursNotCalendarWeek(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, confirmed := range []time.Time{
		time.Date(2026, time.March, 11, 12, 0, 0, 0, location),
		time.Date(2026, time.November, 4, 12, 0, 0, 0, location),
	} {
		t.Run(confirmed.Format("2006-01-02"), func(t *testing.T) {
			in := testRequest()
			in.ConfirmedAt = confirmed
			start := confirmed.UTC().Add(-168 * time.Hour)
			calendarStart := confirmed.AddDate(0, 0, -7)
			if start.Equal(calendarStart) {
				t.Fatal("fixture does not cross a daylight-saving offset change")
			}
			in.Messages = []Message{testMessage("start", start), testMessage("before", start.Add(-time.Nanosecond)), testMessage("end", confirmed)}
			r := mustResult(t, in)
			q := r.Query()
			if q.Start != start || q.End != confirmed.UTC() || q.End.Sub(q.Start) != 168*time.Hour {
				t.Fatalf("DST window is not exact UTC168h: %#v", q)
			}
			requireIDs(t, r.Members(), "start")
		})
	}
}

func TestDomainNamedPlatformDoesNotConferPlatformScope(t *testing.T) {
	in := testRequest()
	in.Scope.DomainIDs = []string{"platform"}
	in.Messages = []Message{testMessage("domain", testInstant().Add(-time.Hour)), testMessage("platform", testInstant().Add(-time.Hour))}
	in.Messages[0].DomainID = "platform"
	in.Messages[1].ScopeKind, in.Messages[1].DomainID = ScopePlatform, ""
	requireIDs(t, mustResult(t, in).Members(), "domain")
	in.Scope.DomainIDs, in.Scope.IncludePlatform = nil, true
	requireIDs(t, mustResult(t, in).Members(), "platform")
}

func TestOpaqueNamesAndKeywordsAreNotTrimmedOrUnicodeNormalized(t *testing.T) {
	in := testRequest()
	in.Catalog = append(in.Catalog, TypeSubType{Type: " security ", SubType: " login "})
	in.Messages[0].Type, in.Messages[0].SubType = " security ", " login "
	in.Messages[0].Title, in.Messages[1].Title = "caf\u00e9", "cafe\u0301"
	in.Query.Types = []string{" security "}
	requireIDs(t, mustResult(t, in).Members(), "a")
	in.Query.Types, in.Query.Keyword = nil, "caf\u00e9"
	requireIDs(t, mustResult(t, in).Members(), "a")
	in.Query.Keyword = "cafe\u0301"
	requireIDs(t, mustResult(t, in).Members(), "b")
	in.Query.Keyword = ""
	in.Scope.DomainIDs = []string{" domain-a "}
	requireIDs(t, mustResult(t, in).Members())
}

func permutationsForTest(values []Message, visit func([]Message)) {
	var walk func(int)
	walk = func(i int) {
		if i == len(values) {
			visit(values)
			return
		}
		for j := i; j < len(values); j++ {
			values[i], values[j] = values[j], values[i]
			walk(i + 1)
			values[i], values[j] = values[j], values[i]
		}
	}
	walk(0)
}

func TestEveryInputPermutationAndReadAssignmentPreservesOrderAndCountConservation(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		in := testRequest()
		in.ReadState.FirstReadAt = make(map[string]time.Time)
		readCount := 0
		for i, m := range in.Messages {
			if mask&(1<<i) != 0 {
				in.ReadState.FirstReadAt[m.ID] = testInstant().Add(-time.Minute)
				readCount++
			}
		}
		wantCounts := Counts{All: 4, Read: readCount, Unread: 4 - readCount}
		for _, order := range []struct {
			sort Sort
			ids  []string
		}{
			{SortAscending, []string{"a", "b", "c", "d"}},
			{SortDescending, []string{"d", "b", "c", "a"}},
		} {
			in.Query.Sort = order.sort
			for _, filter := range []ReadFilter{ReadAll, ReadRead, ReadUnread} {
				in.Query.ReadFilter = filter
				want := make([]string, 0, 4)
				for _, id := range order.ids {
					_, read := in.ReadState.FirstReadAt[id]
					if filter == ReadAll || filter == ReadRead && read || filter == ReadUnread && !read {
						want = append(want, id)
					}
				}
				permutationsForTest(in.Messages, func(messages []Message) {
					request := in
					request.Messages = messages
					r := mustResult(t, request)
					if r.Counts() != wantCounts || r.Counts().All != r.Counts().Read+r.Counts().Unread {
						t.Fatalf("mask=%d sort=%s filter=%s counts=%#v", mask, order.sort, filter, r.Counts())
					}
					requireIDs(t, r.Base(), order.ids...)
					if !reflect.DeepEqual(r.IDs(), want) || r.Total() != len(want) {
						t.Fatalf("mask=%d sort=%s filter=%s IDs=%v want=%v", mask, order.sort, filter, r.IDs(), want)
					}
					joined := make([]string, 0, len(want))
					for offset := 0; offset < r.Total(); offset += 2 {
						page, err := r.Page(offset, 2)
						if err != nil {
							t.Fatal(err)
						}
						if page.Counts != wantCounts || page.Total != len(want) {
							t.Fatal("page changed count conservation")
						}
						joined = append(joined, entryIDs(page.Items)...)
					}
					if !reflect.DeepEqual(joined, want) {
						t.Fatalf("page concatenation=%v want=%v", joined, want)
					}
					for index, id := range want {
						location, err := r.Locate(id, 2)
						if err != nil || location != (Location{Found: true, Index: index, Page: index/2 + 1}) {
							t.Fatalf("fixed location mismatch: %#v %v", location, err)
						}
					}
				})
			}
		}
	}
}
