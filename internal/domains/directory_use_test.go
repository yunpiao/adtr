package domains

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

func directoryUseTestPins() directoryPinnedPayload {
	return directoryPinnedPayload{"operation_account", "2", "2", "account-id", "1", "platform_admin", "9", strings.Repeat("a", 64)}
}

func directoryUseTestTask() tasks.Task {
	return tasks.Task{ID: "directory-task", TenantID: "tenant", DomainID: "domain", ActorID: 7,
		Kind: DirectoryKindName, PayloadVersion: 1, Payload: directoryUseTestPins().json(), PayloadHash: strings.Repeat("b", 64),
		MaxAttempts: 1, State: tasks.Queued}
}

func TestDirectoryUseTaskPinsStaySeparateFromDiagnosticAuthority(t *testing.T) {
	original := directoryUseTestTask()
	if p, err := directoryUseTaskPayload(original); err != nil || p != directoryUseTestPins() {
		t.Fatal("valid immutable directory task rejected", err)
	}
	for name, change := range map[string]func(*tasks.Task){
		"legacy-kind":        func(v *tasks.Task) { v.Kind = AccountKindName },
		"version":            func(v *tasks.Task) { v.PayloadVersion++ },
		"repeatable":         func(v *tasks.Task) { v.MaxAttempts++ },
		"child":              func(v *tasks.Task) { v.ParentID = "parent" },
		"no-id":              func(v *tasks.Task) { v.ID = "" },
		"no-tenant":          func(v *tasks.Task) { v.TenantID = "" },
		"no-domain":          func(v *tasks.Task) { v.DomainID = "" },
		"no-actor":           func(v *tasks.Task) { v.ActorID = 0 },
		"no-hash":            func(v *tasks.Task) { v.PayloadHash = "" },
		"diagnostic-payload": func(v *tasks.Task) { v.Payload = validAccountPins().json() },
		"invalid-payload":    func(v *tasks.Task) { v.Payload = []byte(`{}`) },
	} {
		t.Run(name, func(t *testing.T) {
			v := original
			change(&v)
			if _, err := directoryUseTaskPayload(v); !errors.Is(err, errDirectoryUseEvidence) {
				t.Fatal("invalid task pins accepted", err)
			}
		})
	}
	// Cleanup identity intentionally ignores current state, lease, authorization
	// epoch, result and visibility: none proves whether the executor returned.
	for _, state := range []tasks.State{tasks.Running, tasks.CancelRequested, tasks.Failed, tasks.Cancelled} {
		v := original
		v.State, v.LeaseOwner, v.FencingToken = state, "later-owner", 100
		v.Attempt, v.AuthorizationVersion, v.Archived = 8, "later-epoch", true
		if _, err := directoryUseTaskPayload(v); err != nil {
			t.Fatal("cleanup depended on current authority", err)
		}
	}
}

func TestDirectoryUseLifecycleRequiresExactNoOpenOrOpenerEvidence(t *testing.T) {
	stamp := time.Now()
	owner, fence, attempt := "original-owner", int64(4), 1
	returned, unopened := "executor_returned", "never_opened_terminal"
	rows := []directoryUseRow{
		{state: "reserved"},
		{state: "opened", openedAt: &stamp, owner: &owner, fence: &fence, attempt: &attempt},
		{state: "quiesced", openedAt: &stamp, quiescedAt: &stamp, owner: &owner, fence: &fence, attempt: &attempt, reason: &returned},
		{state: "quiesced", quiescedAt: &stamp, reason: &unopened},
	}
	for _, u := range rows {
		if !u.validLifecycle() {
			t.Fatal("valid evidence rejected", u.state)
		}
	}
	for name, u := range map[string]directoryUseRow{
		"unknown":                  {state: "unknown"},
		"reserved-with-opener":     {state: "reserved", owner: &owner},
		"reserved-with-open-time":  {state: "reserved", openedAt: &stamp},
		"reserved-with-completion": {state: "reserved", quiescedAt: &stamp},
		"opened-without-time":      {state: "opened", owner: &owner, fence: &fence, attempt: &attempt},
		"opened-without-owner":     {state: "opened", openedAt: &stamp, fence: &fence, attempt: &attempt},
		"opened-with-reason":       {state: "opened", openedAt: &stamp, owner: &owner, fence: &fence, attempt: &attempt, reason: &returned},
		"opened-with-completion":   {state: "opened", openedAt: &stamp, quiescedAt: &stamp, owner: &owner, fence: &fence, attempt: &attempt},
		"returned-without-open":    {state: "quiesced", quiescedAt: &stamp, reason: &returned},
		"returned-without-time":    {state: "quiesced", openedAt: &stamp, owner: &owner, fence: &fence, attempt: &attempt, reason: &returned},
		"never-opened-with-owner":  {state: "quiesced", quiescedAt: &stamp, owner: &owner, reason: &unopened},
		"quiesced-without-reason":  {state: "quiesced", quiescedAt: &stamp},
	} {
		t.Run(name, func(t *testing.T) {
			if u.validLifecycle() {
				t.Fatal("incomplete lifecycle evidence accepted")
			}
		})
	}
	for _, value := range []int64{0, -1} {
		u := rows[1]
		u.fence = &value
		if u.validLifecycle() {
			t.Fatal("invalid opener fence accepted")
		}
	}
	for _, value := range []int{0, 2, -1} {
		u := rows[1]
		u.attempt = &value
		if u.validLifecycle() {
			t.Fatal("invalid opener attempt accepted")
		}
	}
}

func TestDirectoryUseRejectsUnissuedWitnessAndInvalidBoundaryBeforeSQL(t *testing.T) {
	s := New(nil)
	if err := s.acknowledgeDirectoryUse(context.Background(), nil, tasks.QuiescedAttempt{}); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("zero witness was accepted", err)
	}
	v, p := directoryUseTestTask(), directoryUseTestPins()
	p.GrantRevision = "10"
	if err := reserveDirectoryUseTx(context.Background(), nil, v, p); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("reservation ignored task payload", err)
	}
	v.State = tasks.Running
	if err := reserveDirectoryUseTx(context.Background(), nil, v, directoryUseTestPins()); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("running reservation accepted", err)
	}
	if err := openDirectoryUseTx(context.Background(), nil, v, directoryUseTestPins()); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("opening without original opener accepted", err)
	}
	for _, limit := range []int{-1, 0, 101} {
		if n, err := s.ReconcileReservedDirectoryUses(context.Background(), &pgx.ConnConfig{}, limit); err == nil || n != 0 {
			t.Fatal("unbounded reconciliation accepted", n, err)
		}
	}
	if _, err := s.ReconcileReservedDirectoryUses(context.Background(), nil, 1); err == nil {
		t.Fatal("nil config accepted")
	}
}
