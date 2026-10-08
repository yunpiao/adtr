package schedules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validCreate() CreateInput {
	return CreateInput{Label: "Synthetic health", TaskName: HealthKind, DomainID: "platform", PayloadVersion: 1, Payload: json.RawMessage(`{}`), StartAt: "2030-01-02T03:04:05Z", IntervalSeconds: 60, IdempotencyKey: "create-1"}
}
func TestDefinitionValidationAndUTCNormalization(t *testing.T) {
	in := validCreate()
	in.StartAt = "2030-01-02T04:04:05+01:00"
	in.Payload = json.RawMessage(`{ }`)
	got, anchor, err := validateCreate(in)
	if err != nil || anchor.Format(time.RFC3339) != validCreate().StartAt || hash(got) != hash(validCreate()) {
		t.Fatal("equivalent UTC definitions differ", got, anchor, err)
	}
	for _, change := range []func(*CreateInput){
		func(v *CreateInput) { v.TaskName = "audit.export" }, func(v *CreateInput) { v.TaskName = "detection" }, func(v *CreateInput) { v.DomainID = "domain-a" }, func(v *CreateInput) { v.PayloadVersion = 2 },
		func(v *CreateInput) { v.Payload = json.RawMessage(`null`) }, func(v *CreateInput) { v.Payload = json.RawMessage(`[]`) }, func(v *CreateInput) { v.Payload = json.RawMessage(`{"override":true}`) }, func(v *CreateInput) { v.Payload = json.RawMessage(`{} {}`) },
		func(v *CreateInput) { v.Label = " " }, func(v *CreateInput) { v.Label = " padded" }, func(v *CreateInput) { v.Label = "bad\nlabel" }, func(v *CreateInput) { v.Label = strings.Repeat("界", 81) },
		func(v *CreateInput) { v.IntervalSeconds = 59 }, func(v *CreateInput) { v.IntervalSeconds = 86401 }, func(v *CreateInput) { v.StartAt = "2030-01-02T03:04:05" }, func(v *CreateInput) { v.StartAt = "2030-01-02T03:04:05.1Z" }, func(v *CreateInput) { v.StartAt = "2030-01-02T03:04:05.000Z" }, func(v *CreateInput) { v.StartAt = "2030-02-30T03:04:05Z" }, func(v *CreateInput) { v.IdempotencyKey = "contains space" },
		func(v *CreateInput) { v.StartAt = "2030-01-02T03:04:05+24:00" }, func(v *CreateInput) { v.StartAt = "2030-01-02T03:04:05+00:60" },
	} {
		v := validCreate()
		change(&v)
		if _, _, err := validateCreate(v); err == nil {
			t.Fatalf("accepted invalid definition: %+v", v)
		}
	}
}

func TestImmutableGridBoundariesAndLongDowntime(t *testing.T) {
	anchor := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		now  time.Time
		want int64
	}{{anchor.Add(-time.Nanosecond), -1}, {anchor, 0}, {anchor.Add(59*time.Second + 999*time.Millisecond), 0}, {anchor.Add(time.Minute), 1}, {anchor.Add(11 * time.Minute), 11}} {
		if got := latestDue(anchor, tc.now, 60); got != tc.want {
			t.Fatalf("due %v got %d want %d", tc.now, got, tc.want)
		}
	}
	distant := time.Date(9000, 1, 1, 0, 0, 0, 0, time.UTC)
	index := latestDue(anchor, distant, 60)
	if got, err := gridAt(anchor, 60, index); err != nil || !got.Equal(distant) {
		t.Fatal("long downtime overflowed duration", got, err)
	}
	if _, err := gridAt(time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), 60, 1); err == nil {
		t.Fatal("accepted unrepresentable next date")
	}
	if _, err := gridAt(anchor, 60, -1); err == nil {
		t.Fatal("accepted negative index")
	}
	if _, err := gridAt(anchor, 60, 1<<62); err == nil {
		t.Fatal("accepted overflowing index")
	}
}

func TestSchedulePublicValuesHideIdentityAndProof(t *testing.T) {
	s := Schedule{ID: "visible", TenantID: "secret-tenant", ActorID: 41, AuthorizationVersion: "secret-epoch", Payload: json.RawMessage(`{"secret":"payload"}`), DefinitionHash: "secret-hash", CreationKey: "secret-key", NextIndex: 123}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-tenant", "secret-epoch", "secret-hash", "secret-key", "actorId", "nextIndex", `"payload":`} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private value %s disclosed: %s", secret, raw)
		}
	}
}

func TestPaginationAndStateBounds(t *testing.T) {
	if f, err := validateFilter(Filter{}); err != nil || f.PageIdx != 1 || f.PageSize != 20 {
		t.Fatal(f, err)
	}
	for _, f := range []Filter{{PageIdx: -1}, {PageIdx: 1000001}, {PageSize: 101}, {State: "running"}} {
		if _, err := validateFilter(f); err == nil {
			t.Fatal("accepted filter", f)
		}
	}
}
