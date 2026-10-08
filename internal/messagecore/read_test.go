package messagecore

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestMarkReadUsesCurrentIdentityBoundStateAndPreservesFirstTimes(t *testing.T) {
	in := testRequest()
	r := mustResult(t, in)
	zone := time.FixedZone("caller", 5*60*60+30*60)
	first := testInstant().Add(-10 * time.Minute).In(zone)
	outside := testInstant().Add(-10 * 24 * time.Hour).In(zone)
	current := ReadState{TenantID: "tenant-a", UserID: "user-a", FirstReadAt: map[string]time.Time{
		"a": first, "unrelated-current-only": outside,
	}}
	before := copyStateForTest(current)
	at := testInstant().Add(time.Hour).In(zone)
	marked, err := r.MarkRead(current, []string{"a", "b", "c"}, at)
	if err != nil {
		t.Fatal(err)
	}
	wantProgress := Progress{Target: 3, Processed: 3, NewlyRead: 2, AlreadyRead: 1}
	if marked.Progress != wantProgress {
		t.Fatalf("progress=%#v, want %#v", marked.Progress, wantProgress)
	}
	wantTimes := map[string]time.Time{"a": first.UTC(), "b": at.UTC(), "c": at.UTC(), "unrelated-current-only": outside.UTC()}
	if !reflect.DeepEqual(marked.State.FirstReadAt, wantTimes) {
		t.Fatalf("marked times=%#v, want %#v", marked.State.FirstReadAt, wantTimes)
	}
	if marked.State.TenantID != current.TenantID || marked.State.UserID != current.UserID {
		t.Fatal("mark changed state identity")
	}
	if !reflect.DeepEqual(current, before) {
		t.Fatal("mark mutated current state")
	}
	if _, exists := marked.State.FirstReadAt["d"]; exists {
		t.Fatal("mark resurrected state from the old snapshot instead of using current state")
	}
	if r.Counts() != (Counts{All: 4, Read: 2, Unread: 2}) {
		t.Fatal("mark changed snapshot counts")
	}
	for _, entry := range r.Members() {
		if entry.Message.ID == "a" && entry.Read {
			t.Fatal("mark changed query-time unread metadata")
		}
		if entry.Message.ID == "b" && !entry.FirstReadAt.Equal(in.ReadState.FirstReadAt["b"]) {
			t.Fatal("mark replaced query-time first-read timestamp")
		}
	}
	// The returned state is neither the current map nor the immutable snapshot map.
	marked.State.FirstReadAt["a"] = at.Add(time.Hour)
	delete(marked.State.FirstReadAt, "unrelated-current-only")
	if !reflect.DeepEqual(current, before) || !reflect.DeepEqual(r.ReadSnapshot(), in.ReadState) {
		t.Fatal("mutating a mark output altered its inputs or snapshot")
	}
	current.FirstReadAt["after-call"] = at
	if _, exists := marked.State.FirstReadAt["after-call"]; exists {
		t.Fatal("output retained the current input map")
	}
}

func TestMarkReadReplayIsIdempotentAndOldSnapshotStaysFixed(t *testing.T) {
	in := testRequest()
	in.Query.ReadFilter, in.Query.Limit = ReadUnread, 1
	r := mustResult(t, in)
	beforeMembers, beforeBase, beforeCounts := r.Members(), r.Base(), r.Counts()
	targets := r.IDs()
	marked, err := r.MarkRead(in.ReadState, targets, testInstant())
	if err != nil {
		t.Fatal(err)
	}
	if marked.Progress != (Progress{Target: 2, Processed: 2, NewlyRead: 2}) {
		t.Fatalf("first mark progress=%#v", marked.Progress)
	}
	firstState := copyStateForTest(marked.State)
	replay, err := r.MarkRead(marked.State, targets, testInstant().Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if replay.Progress != (Progress{Target: 2, Processed: 2, AlreadyRead: 2}) {
		t.Fatalf("replay progress=%#v", replay.Progress)
	}
	if !reflect.DeepEqual(replay.State, firstState) || !reflect.DeepEqual(marked.State, firstState) {
		t.Fatal("replay changed first timestamps or input state")
	}
	if !reflect.DeepEqual(r.Members(), beforeMembers) || !reflect.DeepEqual(r.Base(), beforeBase) || r.Counts() != beforeCounts {
		t.Fatal("mark or replay mutated the old snapshot")
	}
	page, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	requireIDs(t, page.Items, "a")
	if page.Total != 2 || page.Items[0].Read {
		t.Fatalf("old page changed after mark: %#v", page)
	}
	in.ReadState = marked.State
	fresh := mustResult(t, in)
	if fresh.Total() != 0 || fresh.Counts() != (Counts{All: 4, Read: 4}) {
		t.Fatalf("fresh query did not observe returned state: total=%d counts=%#v", fresh.Total(), fresh.Counts())
	}
	delete(replay.State.FirstReadAt, "a")
	if !reflect.DeepEqual(marked.State, firstState) {
		t.Fatal("replay output aliases prior transition output")
	}
}

func TestMarkReadEmptyTargetsAreCopiedStateNoOp(t *testing.T) {
	r := mustResult(t, testRequest())
	for _, targets := range [][]string{nil, {}} {
		for _, emptyMap := range []bool{false, true} {
			current := testRequest().ReadState
			if emptyMap {
				current.FirstReadAt = nil
			}
			before := copyStateForTest(current)
			marked, err := r.MarkRead(current, targets, testInstant())
			if err != nil {
				t.Fatal(err)
			}
			if marked.Progress != (Progress{}) {
				t.Fatalf("empty mark progress=%#v", marked.Progress)
			}
			if marked.State.FirstReadAt == nil {
				t.Fatal("empty mark must return an independently usable map")
			}
			if len(marked.State.FirstReadAt) != len(current.FirstReadAt) {
				t.Fatal("empty mark lost state")
			}
			marked.State.FirstReadAt["independent"] = testInstant()
			if !reflect.DeepEqual(current, before) {
				t.Fatal("empty mark output aliases input")
			}
		}
	}
}

func TestMarkReadRejectsInvalidStateAndTargetsAtomically(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ReadState, *[]string, *time.Time)
		code   Code
	}{
		{"empty_tenant", func(s *ReadState, _ *[]string, _ *time.Time) { s.TenantID = "" }, InvalidReadState},
		{"empty_user", func(s *ReadState, _ *[]string, _ *time.Time) { s.UserID = "" }, InvalidReadState},
		{"other_tenant", func(s *ReadState, _ *[]string, _ *time.Time) { s.TenantID = "tenant-b" }, InvalidReadState},
		{"other_user", func(s *ReadState, _ *[]string, _ *time.Time) { s.UserID = "user-b" }, InvalidReadState},
		{"empty_state_id", func(s *ReadState, _ *[]string, _ *time.Time) { s.FirstReadAt[""] = testInstant() }, InvalidReadState},
		{"zero_state_timestamp", func(s *ReadState, _ *[]string, _ *time.Time) { s.FirstReadAt["outside"] = time.Time{} }, InvalidReadState},
		{"zero_mark_time", func(_ *ReadState, _ *[]string, at *time.Time) { *at = time.Time{} }, InvalidFilter},
		{"empty_target", func(_ *ReadState, ids *[]string, _ *time.Time) { *ids = []string{""} }, InvalidTarget},
		{"empty_after_valid", func(_ *ReadState, ids *[]string, _ *time.Time) { *ids = []string{"a", ""} }, InvalidTarget},
		{"duplicate", func(_ *ReadState, ids *[]string, _ *time.Time) { *ids = []string{"a", "a"} }, InvalidTarget},
		{"duplicate_after_two_valid", func(_ *ReadState, ids *[]string, _ *time.Time) { *ids = []string{"a", "c", "a"} }, InvalidTarget},
		{"not_in_result", func(_ *ReadState, ids *[]string, _ *time.Time) { *ids = []string{"unseen"} }, TargetNotInResult},
		{"outside_after_valid", func(_ *ReadState, ids *[]string, _ *time.Time) { *ids = []string{"a", "outside"} }, TargetNotInResult},
		{"read_filtered_out", func(_ *ReadState, ids *[]string, _ *time.Time) { *ids = []string{"a", "b"} }, TargetNotInResult},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := testRequest()
			in.Query.ReadFilter = ReadUnread
			r := mustResult(t, in)
			current, targets, at := copyStateForTest(in.ReadState), []string{"a", "c"}, testInstant()
			test.change(&current, &targets, &at)
			before, beforeIDs, beforeSnapshot := copyStateForTest(current), append([]string(nil), targets...), r.ReadSnapshot()
			marked, err := r.MarkRead(current, targets, at)
			requireCode(t, err, test.code)
			if !reflect.DeepEqual(marked, MarkResult{}) {
				t.Fatalf("invalid mark returned partial state/progress: %#v", marked)
			}
			if !reflect.DeepEqual(current, before) || !reflect.DeepEqual(targets, beforeIDs) || !reflect.DeepEqual(r.ReadSnapshot(), beforeSnapshot) {
				t.Fatal("rejected mark mutated current state, targets, or snapshot")
			}
		})
	}
}

func TestMarkReadCanTargetBeyondInitialPageButNeverBeyondMembership(t *testing.T) {
	in := testRequest()
	in.Query.Limit = 1
	in.Scope.DomainIDs = []string{"domain-a"}
	in.Messages = append(in.Messages, testMessage("forbidden-domain", testInstant().Add(-time.Hour)))
	in.Messages[4].DomainID = "domain-b"
	r := mustResult(t, in)
	marked, err := r.MarkRead(in.ReadState, []string{"c"}, testInstant())
	if err != nil || marked.Progress.NewlyRead != 1 {
		t.Fatalf("valid off-page target rejected: %#v, %v", marked, err)
	}
	marked, err = r.MarkRead(in.ReadState, []string{"a", "forbidden-domain"}, testInstant())
	requireCode(t, err, TargetNotInResult)
	if !reflect.DeepEqual(marked, MarkResult{}) {
		t.Fatal("scope failure returned partial mark")
	}
}

func TestConcurrentReadAndMarkUseIndependentMaps(t *testing.T) {
	in := testRequest()
	r := mustResult(t, in)
	before := copyStateForTest(in.ReadState)
	wantIDs := []string{"a", "b", "c", "d"}
	const workers = 24
	const iterations = 25
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				page, err := r.Page(iteration%4, 2)
				if err != nil || page.Total != 4 {
					errors <- fmt.Errorf("worker %d Page: %#v, %v", worker, page, err)
					return
				}
				location, err := r.Locate("d", 2)
				if err != nil || location != (Location{Found: true, Index: 3, Page: 2}) {
					errors <- fmt.Errorf("worker %d Locate: %#v, %v", worker, location, err)
					return
				}
				if !reflect.DeepEqual(r.IDs(), wantIDs) || r.Counts() != (Counts{All: 4, Read: 2, Unread: 2}) {
					errors <- fmt.Errorf("worker %d snapshot changed", worker)
					return
				}
				current := r.ReadSnapshot()
				current.FirstReadAt[fmt.Sprintf("worker-%d", worker)] = testInstant()
				marked, err := r.MarkRead(current, []string{"a", "c"}, testInstant().Add(time.Duration(worker)*time.Second))
				if err != nil || marked.Progress != (Progress{Target: 2, Processed: 2, NewlyRead: 2}) {
					errors <- fmt.Errorf("worker %d MarkRead: %#v, %v", worker, marked.Progress, err)
					return
				}
				// All mutation is confined to this goroutine's independent outputs.
				marked.State.FirstReadAt["a"] = testInstant().Add(24 * time.Hour)
				current.FirstReadAt["c"] = testInstant().Add(48 * time.Hour)
				page.Items[0].Message.Title = "local mutation"
				members := r.Members()
				members[0].Read = true
				domains := r.Scope()
				domains.DomainIDs[0] = "local domain"
			}
		}(worker)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if !reflect.DeepEqual(in.ReadState, before) || !reflect.DeepEqual(r.ReadSnapshot(), before) {
		t.Fatal("concurrent calls mutated shared input or snapshot")
	}
	requireIDs(t, r.Members(), wantIDs...)
}
