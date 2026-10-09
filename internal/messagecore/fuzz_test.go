package messagecore

import (
	"reflect"
	"testing"
)

func FuzzPageIntegerBounds(f *testing.F) {
	maxInt := int(^uint(0) >> 1)
	for _, seed := range [][2]int{{0, 1}, {0, 200}, {0, 201}, {-1, 1}, {0, 0}, {4, 2}, {maxInt, 200}, {maxInt - 1, maxInt}, {-maxInt - 1, 1}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, offset, limit int) {
		r := mustResult(t, testRequest())
		page, err := r.Page(offset, limit)
		if offset < 0 || limit < 1 || limit > 200 {
			requireCode(t, err, InvalidPagination)
			if !reflect.DeepEqual(page, Page{}) {
				t.Fatal("invalid bounds produced partial page")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"a", "b", "c", "d"}
		if offset >= len(want) {
			want = nil
		} else {
			want = want[offset:]
			if len(want) > limit {
				want = want[:limit]
			}
		}
		requireIDs(t, page.Items, want...)
		if page.Total != 4 || page.Counts != (Counts{All: 4, Read: 2, Unread: 2}) || page.Offset != offset || page.Limit != limit {
			t.Fatalf("page metadata=%#v", page)
		}
	})
}

func FuzzMarkReadTargetValidationIsAtomic(f *testing.F) {
	for _, seed := range [][]byte{nil, {0}, {0, 1}, {0, 0}, {3}, {2}, {4}, {0, 1, 2, 3}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8 {
			data = data[:8]
		}
		in := testRequest()
		in.Query.ReadFilter, in.Limits.MaxMarkTargets = ReadUnread, 3
		r := mustResult(t, in)
		ids := []string{"a", "c", "b", "", "outside", "a"}
		targets := make([]string, len(data))
		for i, b := range data {
			targets[i] = ids[int(b)%len(ids)]
		}
		var wantCode Code
		if len(targets) > 3 {
			wantCode = BudgetExceeded
		} else {
			seen := make(map[string]bool)
			for _, id := range targets {
				if id == "" || seen[id] {
					wantCode = InvalidTarget
					break
				}
				if id != "a" && id != "c" {
					wantCode = TargetNotInResult
					break
				}
				seen[id] = true
			}
		}
		before := copyStateForTest(in.ReadState)
		snapshot := r.ReadSnapshot()
		marked, err := r.MarkRead(in.ReadState, targets, testInstant())
		if wantCode != "" {
			requireCode(t, err, wantCode)
			if !reflect.DeepEqual(marked, MarkResult{}) {
				t.Fatal("invalid targets yielded partial output")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if marked.Progress != (Progress{Target: len(targets), Processed: len(targets), NewlyRead: len(targets)}) {
				t.Fatalf("progress=%#v", marked.Progress)
			}
			for _, id := range targets {
				if marked.State.FirstReadAt[id] != testInstant() {
					t.Fatal("valid target was not marked")
				}
			}
			marked.State.FirstReadAt["mutated-output"] = testInstant()
		}
		if !reflect.DeepEqual(in.ReadState, before) || !reflect.DeepEqual(r.ReadSnapshot(), snapshot) {
			t.Fatal("mark mutated input or snapshot")
		}
	})
}
