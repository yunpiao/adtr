package riskscore

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

var scope = Scope{"synthetic-tenant", "synthetic-domain", "synthetic-user"}
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func event(id, kind string, seconds int) Event {
	return Event{id, scope, kind, epoch.Add(time.Duration(seconds) * time.Second)}
}
func request(events ...Event) Request {
	return Request{Scope: scope, AsOf: epoch.Add(time.Hour), Events: events}
}
func evaluate(t *testing.T, r Request) Result {
	t.Helper()
	v, e := Evaluate(r)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestDistributionTrendAndRecords(t *testing.T) {
	r := request(event("c", "critical", 3), event("a", "low", 1), event("b", "high", 2))
	got := evaluate(t, r)
	if !got.Simulated || got.Version != Version || got.Score != 85 || got.EvidenceCount != 3 {
		t.Fatalf("%+v", got)
	}
	if got.Distribution[0].Count != 1 || got.Distribution[2].RawPoints != 30 || got.Distribution[3].Weight != 50 {
		t.Fatal(got.Distribution)
	}
	if len(got.Records) != 3 || got.Records[0].ID != "a" || got.Records[2].Delta != 50 {
		t.Fatal(got.Records)
	}
	if len(got.Trend) != 3 || got.Trend[1].Score != 35 {
		t.Fatal(got.Trend)
	}
	r.Events[0], r.Events[2] = r.Events[2], r.Events[0]
	if other := evaluate(t, r); !reflect.DeepEqual(got, other) {
		t.Fatal("input order changed result")
	}
}
func TestResetBoundaryAndReplay(t *testing.T) {
	r := request(event("old", "critical", 1), event("same", "high", 2), event("new", "low", 3))
	r.Resets = []Reset{{"reset", scope, epoch.Add(2 * time.Second), "synthetic analyst reset"}}
	got := evaluate(t, r)
	if got.Score != 5 || got.EvidenceCount != 1 || got.Distribution[3].Count != 0 || got.ResetBoundary == nil {
		t.Fatal(got)
	}
	if len(got.Trend) != 3 || got.Trend[1].Score != 0 || got.Records[2].Delta != -80 {
		t.Fatal(got)
	}
	// Delayed pre-boundary evidence affects historical records, not current score.
	r.Events = append(r.Events, event("delayed", "critical", 1))
	if evaluate(t, r).Score != 5 {
		t.Fatal("old event crossed reset boundary")
	}
	r.Events = append(r.Events, r.Events[0])
	r.Resets = append(r.Resets, r.Resets[0])
	got = evaluate(t, r)
	if got.Score != 5 || got.Duplicates != 2 {
		t.Fatal(got)
	}
}
func TestBoundedAndEmpty(t *testing.T) {
	empty := evaluate(t, request())
	if empty.Score != 0 || empty.EvidenceCount != 0 || len(empty.Trend) != 0 || len(empty.Distribution) != 4 {
		t.Fatal(empty)
	}
	r := request(event("a", "critical", 0), event("b", "critical", 0), event("c", "critical", 0))
	got := evaluate(t, r)
	if got.Score != 100 || got.Records[2].Delta != 0 || len(got.Trend) != 1 || got.Distribution[3].RawPoints != 150 {
		t.Fatal(got)
	}
	r.Resets = []Reset{{"r", scope, epoch, "reset to zero"}}
	if evaluate(t, r).Score != 0 {
		t.Fatal("inclusive reset should win ties")
	}
}
func TestFutureAndTimeNormalization(t *testing.T) {
	r := request(event("a", "low", 1), event("future", "critical", 3601))
	r.Events = append(r.Events, r.Events[0])
	r.Events[2].OccurredAt = r.Events[0].OccurredAt.In(time.FixedZone("offset", 3600))
	got := evaluate(t, r)
	if got.Score != 5 || got.Duplicates != 1 || got.IgnoredFuture != 1 {
		t.Fatal(got)
	}
	r.AsOf = epoch.Add(time.Second)
	if evaluate(t, r).Score != 5 {
		t.Fatal("as_of must be inclusive")
	}
}
func TestInvalidAndConflict(t *testing.T) {
	cases := map[string]func(*Request){
		"scope":        func(r *Request) { r.Scope.UserID = "" },
		"as_of":        func(r *Request) { r.AsOf = time.Time{} },
		"version":      func(r *Request) { r.Version = "production" },
		"missing_id":   func(r *Request) { r.Events[0].ID = "" },
		"missing_time": func(r *Request) { r.Events[0].OccurredAt = time.Time{} },
		"unknown_kind": func(r *Request) { r.Events[0].Kind = "unknown" },
		"cross_tenant": func(r *Request) { r.Events[0].Scope.TenantID = "other" },
		"cross_domain": func(r *Request) { r.Events[0].Scope.DomainID = "other" },
		"cross_user":   func(r *Request) { r.Events[0].Scope.UserID = "other" },
		"reset_scope":  func(r *Request) { r.Resets = []Reset{{"r", Scope{"other", "d", "u"}, epoch, "reason"}} },
		"reset_reason": func(r *Request) { r.Resets = []Reset{{"r", scope, epoch, " "}} },
		"reset_time":   func(r *Request) { r.Resets = []Reset{{"r", scope, time.Time{}, "reason"}} },
		"reset_id":     func(r *Request) { r.Resets = []Reset{{"", scope, epoch, "reason"}} },
		"limit":        func(r *Request) { r.Events = make([]Event, MaxEntries+1) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := request(event("a", "low", 1))
			change(&r)
			got, err := Evaluate(r)
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(got, Result{}) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	for _, other := range []Event{event("a", "high", 1), event("a", "low", 2)} {
		_, err := Evaluate(request(event("a", "low", 1), other))
		if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	r := request(event("a", "low", 1))
	r.Resets = []Reset{{"a", scope, epoch, "reason"}}
	if _, err := Evaluate(r); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	r = request()
	r.Resets = []Reset{{"a", scope, epoch, "reason"}, {"a", scope, epoch, "changed"}}
	if _, err := Evaluate(r); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestWeightsAreImmutable(t *testing.T) {
	w := Weights()
	w["critical"] = 0
	if evaluate(t, request(event("a", "critical", 1))).Score != 50 {
		t.Fatal("mutable weights")
	}
}
func TestEntryLimitInclusive(t *testing.T) {
	r := request()
	for i := 0; i < MaxEntries; i++ {
		r.Events = append(r.Events, event("duplicate", "low", 1))
	}
	if got := evaluate(t, r); got.Duplicates != MaxEntries-1 || got.Score != 5 {
		t.Fatal(got)
	}
}
