package messagecore_test

import (
	"fmt"
	"time"

	"github.com/yunpiao/adtr/internal/messagecore"
)

func ExampleNewResult() {
	confirmed := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	current := messagecore.ReadState{
		TenantID: "synthetic-tenant",
		UserID:   "synthetic-user",
		FirstReadAt: map[string]time.Time{
			"already-read": confirmed.Add(-time.Hour),
		},
	}
	result, err := messagecore.NewResult(messagecore.Request{
		Scope: messagecore.Scope{
			TenantID: "synthetic-tenant", UserID: "synthetic-user", IncludePlatform: true,
		},
		Catalog: messagecore.Catalog{{Type: "synthetic", SubType: "notice"}},
		Messages: []messagecore.Message{
			{
				ID: "already-read", TenantID: "synthetic-tenant", ScopeKind: messagecore.ScopePlatform,
				Type: "synthetic", SubType: "notice", Title: "Synthetic platform notice",
				OccurredAt: confirmed.Add(-2 * time.Hour), IngestedAt: confirmed.Add(-time.Hour),
			},
			{
				ID: "new-notice", TenantID: "synthetic-tenant", ScopeKind: messagecore.ScopePlatform,
				Type: "synthetic", SubType: "notice", Title: "Another synthetic notice",
				OccurredAt: confirmed.Add(-time.Minute), IngestedAt: confirmed.Add(-time.Minute),
			},
		},
		ReadState:   current,
		ConfirmedAt: confirmed,
		Query: messagecore.Query{
			ReadFilter: messagecore.ReadUnread, Sort: messagecore.SortDescending, Limit: 20,
		},
		Limits: messagecore.Limits{
			MaxPageSize: 200, MaxInputMessages: 100, MaxInputBytes: 100_000, MaxMarkTargets: 10,
		},
	})
	if err != nil {
		panic(err)
	}
	page, err := result.List()
	if err != nil {
		panic(err)
	}
	fmt.Println(page.Total, page.Counts.All, page.Counts.Unread, page.Counts.Read)
	location, err := result.Locate("new-notice", 20)
	if err != nil {
		panic(err)
	}
	fmt.Println(location.Found, location.Index, location.Page)
	marked, err := result.MarkRead(current, result.IDs(), confirmed)
	if err != nil {
		panic(err)
	}
	fmt.Println(marked.Progress.NewlyRead, len(marked.State.FirstReadAt), len(current.FirstReadAt))
	// The old unread result still reflects the confirmation-time snapshot.
	fmt.Println(result.Total(), result.Counts().Unread)
	// Output:
	// 1 2 1 1
	// true 0 1
	// 1 2 1
	// 1 1
}
