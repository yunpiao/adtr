package simulationrisk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

var testScope = Scope{Tenant: "synthetic-tenant", Domain: "synthetic-domain", Read: true, Manage: true}
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testEvents() []securityevents.Event {
	return []securityevents.Event{
		{ID: "failure", Actor: "subject", Kind: "login_failure", OccurredAt: epoch},
		{ID: "access", Actor: "subject", Kind: "service_access", OccurredAt: epoch.Add(time.Second)},
		{ID: "success", Actor: "subject", Kind: "login_success", OccurredAt: epoch.Add(2 * time.Second)},
		{ID: "other", Actor: "other", Kind: "service_access", OccurredAt: epoch},
	}
}
func TestIllustrativeMappingAndReset(t *testing.T) {
	d := simulationevents.Dataset{ID: "dataset"}
	out, e := evaluate(testScope, d, "subject", testEvents(), nil, epoch.Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if out.Score != 20 || out.EvidenceCount != 2 || out.SourceEventCount != 3 || out.NoRiskEventCount != 1 || out.Revision != 1 || !out.Simulated || out.MappingVersion != MappingVersion || out.Contract != Contract {
		t.Fatal(out)
	}
	if out.Distribution[0].RawPoints != 5 || out.Distribution[1].RawPoints != 15 || len(out.Records) != 2 || len(out.Trend) != 2 {
		t.Fatal(out)
	}
	resets := []riskscore.Reset{{ID: "reset:example", Scope: out.Scope, At: epoch.Add(time.Second), Reason: "illustrative"}}
	out, e = evaluate(testScope, d, "subject", testEvents(), resets, epoch.Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if out.Score != 0 || out.Revision != 2 || out.ResetBoundary == nil || len(out.Records) != 3 || out.Records[2].Delta != -20 {
		t.Fatal(out)
	}
	// No-risk evidence still establishes an existing subject.
	out, e = evaluate(testScope, d, "subject", testEvents()[2:3], nil, epoch.Add(time.Hour))
	if e != nil || out.Score != 0 || out.NoRiskEventCount != 1 {
		t.Fatal(out, e)
	}
	if _, e = evaluate(testScope, d, "absent", testEvents(), nil, epoch); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestFutureEvidenceAndBound(t *testing.T) {
	out, e := evaluate(testScope, simulationevents.Dataset{}, "subject", testEvents(), nil, epoch)
	if e != nil || out.Score != 5 || out.IgnoredFuture != 1 {
		t.Fatal(out, e)
	}
	events := testEvents()
	events[0].Kind = "arbitrary"
	if _, _, e = mapped(testScope, "subject", events); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	resets := make([]riskscore.Reset, riskscore.MaxEntries)
	if _, e = evaluate(testScope, simulationevents.Dataset{}, "subject", testEvents(), resets, epoch); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestValidationAndAuthorization(t *testing.T) {
	for _, v := range []string{"", " x", "x ", "x\n", strings.Repeat("x", 129), string([]byte{255})} {
		if valid(v, 128) {
			t.Fatalf("accepted %q", v)
		}
	}
	if !valid("synthetic subject", 128) || !valid(strings.Repeat("x", 512), 512) {
		t.Fatal("valid rejected")
	}
	ctx := context.Background()
	if _, e := EvaluateTx(ctx, nil, testScope, "d", "s"); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := ResetTx(ctx, nil, testScope, ResetInput{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := OperationTx(ctx, nil, testScope, "d", "a", "o"); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	tx := &unusedTx{}
	denied := testScope
	denied.Manage = false
	if _, e := ResetTx(ctx, tx, denied, ResetInput{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := ResetTx(ctx, tx, testScope, ResetInput{}, nil); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if _, e := EvaluateTx(ctx, tx, testScope, "d", " "); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if _, e := OperationTx(ctx, tx, testScope, "d", "a", " "); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

type unusedTx struct{ pgx.Tx }

func TestRequestIdentityAndNamespace(t *testing.T) {
	in := ResetInput{DatasetID: "d", Subject: "s", Actor: "a", OperationID: "o", Reason: "reason", ExpectedRevision: 1}
	base := requestHash(in)
	for _, change := range []func(*ResetInput){func(v *ResetInput) { v.DatasetID = "x" }, func(v *ResetInput) { v.Subject = "x" }, func(v *ResetInput) { v.Actor = "x" }, func(v *ResetInput) { v.OperationID = "x" }, func(v *ResetInput) { v.Reason = "x" }, func(v *ResetInput) { v.ExpectedRevision++ }} {
		v := in
		change(&v)
		if requestHash(v) == base {
			t.Fatal("identity omitted field")
		}
	}
	if digest([]string{"ab", "c"}) == digest([]string{"a", "bc"}) {
		t.Fatal("ambiguous lock key")
	}
}

func TestLiveClockRegressionFailsClosedWithoutChangingHistoricalReplay(t *testing.T) {
	scope := riskscore.Scope{TenantID: testScope.Tenant, DomainID: testScope.Domain, UserID: "subject"}
	first := riskscore.Reset{ID: "reset:first", Scope: scope, At: epoch.Add(time.Minute), Reason: "first"}
	second := riskscore.Reset{ID: "reset:second", Scope: scope, At: epoch.Add(2 * time.Minute), Reason: "second"}
	resets := []riskscore.Reset{first, second}
	for _, now := range []time.Time{epoch, first.At, second.At.Add(-time.Nanosecond)} {
		if err := liveBoundary(resets, now); !errors.Is(err, ErrConflict) {
			t.Fatalf("regression accepted at %s: %v", now, err)
		}
	}
	for _, now := range []time.Time{second.At, second.At.Add(time.Nanosecond)} {
		if err := liveBoundary(resets, now); err != nil {
			t.Fatalf("nonregressing boundary: %v", err)
		}
	}
	if err := liveBoundary(nil, epoch); err != nil {
		t.Fatal(err)
	}
	// An old durable receipt still reconstructs at its original boundary even
	// when newer resets exist and the live read guard would reject that time.
	historical, err := evaluate(testScope, simulationevents.Dataset{}, "subject", testEvents(), resets[:1], first.At)
	if err != nil || historical.Score != 0 || historical.Revision != 2 || historical.ResetBoundary == nil || !historical.ResetBoundary.Equal(first.At) {
		t.Fatal(historical, err)
	}
}
