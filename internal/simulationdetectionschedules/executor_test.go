package simulationdetectionschedules

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/tasks"
	"testing"
	"time"
)

func testPayload() Payload {
	return Payload{ScheduleID: "schedule", Revision: 1, ScheduledAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Payload: simulationdetection.Payload{TemplateID: "template", TemplateVersion: 1, TemplateHash: digest("template"), DatasetID: "dataset", FixtureHash: digest("fixture"), MappingVersion: simulationdetection.MappingVersion}}
}
func TestScheduledKindPreservesRegistryGuard(t *testing.T) {
	k := NewKind(nil)
	if _, err := tasks.NewRegistry(k); err != nil {
		t.Fatal(err)
	}
	if k.Schedulable || !k.OwnerScoped || !k.ReplaySafe || !k.CancelDiscardsResult || k.MaxAttempts != 1 || k.ValidateSuccess == nil {
		t.Fatal("unsafe scheduled-origin policy")
	}
	k.Schedulable = true
	if _, err := tasks.NewRegistry(k); err == nil {
		t.Fatal("shared registry guard weakened")
	}
}
func TestScheduledPayloadValidation(t *testing.T) {
	p := testPayload()
	b, _ := json.Marshal(p)
	if _, err := validatePayload(b); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{}`, string(b) + `{}`, string(b[:len(b)-1]) + `,"credentials":"forbidden"}`} {
		if _, err := validatePayload([]byte(bad)); err == nil {
			t.Fatal("accepted malformed payload")
		}
	}
	for _, change := range []func(*Payload){func(p *Payload) { p.Revision = 0 }, func(p *Payload) { p.ScheduledAt = time.Time{} }, func(p *Payload) { p.FixtureHash = "bad" }, func(p *Payload) { p.ScheduledAt = p.ScheduledAt.Add(time.Nanosecond) }, func(p *Payload) { p.MappingVersion = "unknown" }} {
		p = testPayload()
		change(&p)
		b, _ = json.Marshal(p)
		if _, err := validatePayload(b); err == nil {
			t.Fatal("accepted invalid pin")
		}
	}
}
func TestScheduledFailureDoesNotPublish(t *testing.T) {
	b, _ := json.Marshal(testPayload())
	for _, want := range []error{tasks.ErrLeaseLost, tasks.ErrCancelRequested, tasks.ErrAuthorization, ErrLimit, ErrConfiguration} {
		k := NewKind(nil)
		o := k.Execute(context.Background(), tasks.Execution{Task: tasks.Task{Payload: b}, WithTx: func(context.Context, int64, tasks.FencedWork) (int64, error) { return 0, want }})
		if o.State == tasks.Succeeded || len(o.Result) > 0 {
			t.Fatal(o)
		}
		if errors.Is(want, tasks.ErrCancelRequested) && o.State != tasks.Cancelled {
			t.Fatal(o)
		}
		if errors.Is(want, ErrConfiguration) && o.Code != "configuration_changed" {
			t.Fatal(o)
		}
	}
}
func TestScheduledFinalGateChecksEpochBeforeDatabase(t *testing.T) {
	k := NewKind(func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		return "new", nil
	})
	code, err := k.ValidateSuccess(context.Background(), nil, tasks.Task{AuthorizationVersion: "old"}, nil)
	if err != nil || code != "authorization_revoked" {
		t.Fatal(code, err)
	}
	transient := errors.New("transient")
	k = NewKind(func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		return "", transient
	})
	code, err = k.ValidateSuccess(context.Background(), nil, tasks.Task{}, nil)
	if code != "" || !errors.Is(err, transient) {
		t.Fatal("transient converted into durable verdict", code, err)
	}
}

type absentIntentRow struct{}

func (absentIntentRow) Scan(dest ...any) error { *dest[0].(*bool) = false; return nil }

type absentIntentTx struct {
	pgx.Tx
	called bool
}

func (t *absentIntentTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	t.called = true
	return absentIntentRow{}
}
func TestForgedScheduledOriginWithoutIntentIsRejected(t *testing.T) {
	p := testPayload()
	b, _ := json.Marshal(p)
	task := tasks.Task{ID: "task", TenantID: "tenant", DomainID: "domain", ActorID: 1, AuthorizationVersion: "1", Payload: b}
	k := NewKind(func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		return "1", nil
	})
	tx := &absentIntentTx{}
	out := k.Execute(context.Background(), tasks.Execution{Task: task, WithTx: func(ctx context.Context, _ int64, work tasks.FencedWork) (int64, error) {
		_, _, _, err := work(ctx, tx)
		return 0, err
	}})
	if !tx.called || out.State != tasks.Failed || out.Code != "authorization_revoked" || len(out.Result) != 0 {
		t.Fatal(out, tx.called)
	}
	tx.called = false
	code, err := k.ValidateSuccess(context.Background(), tx, task, nil)
	if !tx.called || code != "authorization_revoked" || err != nil {
		t.Fatal(code, err, tx.called)
	}
}
