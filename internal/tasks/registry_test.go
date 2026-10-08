package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProductionRegistryIsSafeAndExplicit(t *testing.T) {
	r := ProductionRegistry()
	kinds := r.Kinds()
	if len(kinds) != 1 || kinds[0].TaskName != "infrastructure.health" || kinds[0].Scope != "platform" || kinds[0].MaxAttempts != 5 {
		t.Fatal(kinds)
	}
	for _, name := range []string{"engineRestart", "ad.disable_user", "infrastructure.health.v2", ""} {
		if _, err := r.kind(name); err == nil {
			t.Fatalf("accepted unknown %q", name)
		}
	}
	for _, payload := range []string{`null`, `[]`, `{"url":"https://example.com"}`, `{"delay":1}`, `{"x":1,"x":2}`, `{} {}`, `{"actorId":1}`} {
		if _, err := r.kinds["infrastructure.health"].Validate(json.RawMessage(payload)); err == nil {
			t.Fatalf("accepted unsafe payload %s", payload)
		}
	}
	if got, err := r.kinds["infrastructure.health"].Validate(json.RawMessage(" { } ")); err != nil || string(got) != "{}" {
		t.Fatal(string(got), err)
	}
}
func TestCanonicalJSONRejectsAmbiguityPreservesPrecision(t *testing.T) {
	a, err := canonicalObject(json.RawMessage(`{"z":9007199254740993,"a":{"z":1,"a":2}}`))
	if err != nil || string(a) != `{"a":{"a":2,"z":1},"z":9007199254740993}` {
		t.Fatal(string(a), err)
	}
	for _, raw := range []string{`{"x":1,"x":2}`, `{"a":{"x":1,"x":2}}`, `{"a":[{"x":1,"x":2}]}`, `null`, `[1]`, `{} true`, `{"x":`} {
		if _, err = canonicalObject(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestRegistryRejectsUnboundedAndUnsafePolicies(t *testing.T) {
	base := ProductionRegistry().kinds["infrastructure.health"]
	cases := []func(*Kind){func(k *Kind) { k.MaxAttempts = 0 }, func(k *Kind) { k.MaxAttempts = 6 }, func(k *Kind) { k.Lease = 0 }, func(k *Kind) { k.Heartbeat = k.Lease }, func(k *Kind) { k.Timeout = 2 * time.Hour }, func(k *Kind) { k.RetryCap = 2 * time.Minute }, func(k *Kind) { k.ReplaySafe = false }, func(k *Kind) { k.RetryCodes = []string{"authorization_revoked"} }, func(k *Kind) { k.RetryCodes = []string{"certificate_invalid"} }, func(k *Kind) { k.Execute = nil }, func(k *Kind) { k.Validate = nil }}
	for i, change := range cases {
		k := base
		change(&k)
		if _, err := NewRegistry(k); err == nil {
			t.Fatalf("policy case %d accepted", i)
		}
	}
	if _, err := NewRegistry(base, base); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestRetryCountsIncludeFirstAttempt(t *testing.T) {
	k := ProductionRegistry().kinds["infrastructure.health"]
	for i, want := range []time.Duration{5, 10, 20, 40, 60, 60} {
		if got := k.delay(i + 1); got != want*time.Second {
			t.Fatalf("attempt %d delay %v", i+1, got)
		}
	}
	if k.retry("authorization_revoked") || k.retry("invalid_input") {
		t.Fatal("permanent retry")
	}
	if !k.retry("database_unavailable") {
		t.Fatal("transient not registered")
	}
}
func TestStateMappingAndTerminals(t *testing.T) {
	for _, s := range []State{Queued, Running, RetryWait, CancelRequested, Succeeded, Failed, PartialFailed, DeadLetter, Cancelled} {
		if !s.Valid() || s.Source() == "" {
			t.Fatal(s)
		}
	}
	if State("completed-with-cancel-race").Valid() {
		t.Fatal("audit event accepted as state")
	}
	if Running.Terminal() || !PartialFailed.Terminal() {
		t.Fatal("terminal wrong")
	}
}
func TestWorkerAndEngineDefaultDeny(t *testing.T) {
	if _, err := New(nil, ProductionRegistry(), nil, 5); err == nil {
		t.Fatal("engine missing authorization accepted")
	}
	if _, err := NewWorker(nil, WorkerConfig{Owner: "worker"}); err == nil {
		t.Fatal("nil worker accepted")
	}
	if principalOK(Principal{"tenant", 0}) || principalOK(Principal{"", 1}) {
		t.Fatal("invalid principal")
	}
	if !denied(ErrAuthorization) || !denied(&Error{403, "forbidden"}) || denied(errors.New("database failed")) {
		t.Fatal("failure classification")
	}
}
func TestExecutorPanicIsIsolated(t *testing.T) {
	out := runExecutor(context.Background(), Kind{Execute: func(context.Context, Execution) Outcome { panic("must never escape or expose this") }}, Execution{})
	if out.State != Failed || out.Code != "executor_panic" || out.Retryable {
		t.Fatal(out)
	}
}

func TestPartialEvidenceIsExplicitAndBounded(t *testing.T) {
	good := json.RawMessage(`{"objects":[{"objectId":"a","state":"succeeded"},{"objectId":"b","state":"uncertain","error":"executor_lost"}]}`)
	if !validPartial(good) {
		t.Fatal("valid partial evidence rejected")
	}
	for _, raw := range []string{`{}`, `{"objects":[]}`, `{"objects":[{"objectId":"a","state":"succeeded"}]}`, `{"objects":[{"objectId":"a","state":"succeeded"},{"objectId":"a","state":"failed"}]}`, `{"objects":[{"objectId":"a","state":"succeeded"},{"objectId":"b","state":"invented"}]}`, `{"objects":[{"objectId":"a","state":"succeeded","error":"contradictory"},{"objectId":"b","state":"failed"}]}`} {
		if validPartial(json.RawMessage(raw)) {
			t.Fatalf("accepted invalid evidence %s", raw)
		}
	}
	if _, err := canonicalObject(json.RawMessage{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestCanonicalJSONBoundsEscapedRepresentation(t *testing.T) {
	for _, char := range []string{"&", "<", ">"} {
		raw := json.RawMessage(`{"value":"` + strings.Repeat(char, 20000) + `"}`)
		if len(raw) > 65536 {
			t.Fatal("invalid fixture")
		}
		if _, err := canonicalObject(raw); err == nil {
			t.Fatalf("escaped %q exceeded storage bound", char)
		}
	}
	raw := json.RawMessage(`{"value":"` + strings.Repeat("a", 65536-len(`{"value":""}`)) + `"}`)
	out, err := canonicalObject(raw)
	if err != nil || len(out) != 65536 {
		t.Fatalf("exact boundary length=%d err=%v", len(out), err)
	}
}

func TestOperationDeadlineIsBoundedAndPreservesTighterParent(t *testing.T) {
	start := time.Now()
	ctx, cancel := operationContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(start.Add(operationTimeout+time.Millisecond)) {
		t.Fatal("unbounded operation context")
	}
	parent, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	short, shortCancel := operationContext(parent)
	defer shortCancel()
	parentDeadline, _ := parent.Deadline()
	shortDeadline, ok := short.Deadline()
	if !ok || !shortDeadline.Equal(parentDeadline) {
		t.Fatal("tighter parent deadline lost")
	}
}

func TestSingleAttemptKindCannotRetryScheduleOrClaimReplaySafety(t *testing.T) {
	base := ProductionRegistry().kinds["infrastructure.health"]
	base.ReplaySafe = false
	base.SingleAttemptOnly = true
	base.Schedulable = false
	base.MaxAttempts = 1
	base.RetryCodes = nil
	if _, err := NewRegistry(base); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Kind){
		"replay":            func(k *Kind) { k.ReplaySafe = true },
		"multiple attempts": func(k *Kind) { k.MaxAttempts = 2 },
		"retry code":        func(k *Kind) { k.RetryCodes = []string{"network_failed"} },
		"scheduled":         func(k *Kind) { k.Schedulable = true },
		"implicit":          func(k *Kind) { k.SingleAttemptOnly = false },
	} {
		t.Run(name, func(t *testing.T) {
			k := base
			change(&k)
			if _, err := NewRegistry(k); err == nil {
				t.Fatal("unsafe policy accepted")
			}
		})
	}
}
