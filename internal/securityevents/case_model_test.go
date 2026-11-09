package securityevents_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	events "github.com/yunpiao/adtr/internal/securityevents"
)

// This oracle uses only the public value API and a handwritten transition
// table from the simulated contract. It does not inspect private Case fields,
// call production validation helpers or derive expected values from Apply.
var modelScope = events.Scope{Tenant: "model-tenant", Domain: "model-domain", Read: true, Manage: true}
var modelProvenance = events.Provenance{Contract: events.ContractVersion, Fixture: "case-transition-model-v1", Synthetic: true}
var modelEpoch = time.Date(2026, 11, 1, 12, 0, 0, 123456789, time.UTC)
var modelActions = [...]string{"mark_suspicious", "mark_benign", "unmark", "close"}

// Rows: open/unmarked, open/suspicious, open/benign, then their closed forms.
// Columns follow modelActions. -1 is a no-op conflict; -2 is terminal rejection.
var modelTransitions = [6][4]int{
	{1, 2, -1, 3},
	{-1, 2, 0, 4},
	{1, -1, 0, 5},
	{-2, -2, -2, -2},
	{-2, -2, -2, -2},
	{-2, -2, -2, -2},
}
var modelMarks = [...]string{"", "suspicious", "benign", "", "suspicious", "benign"}

type caseModel struct {
	node     int
	revision uint64
	last     time.Time
}

func modelView(m caseModel) events.CaseView {
	state := "open"
	if m.node >= 3 {
		state = "closed"
	}
	return events.CaseView{ID: "model-case", State: state, Mark: modelMarks[m.node], Revision: m.revision, Provenance: modelProvenance}
}

func newModelCase(t *testing.T) events.Case {
	t.Helper()
	c, err := events.NewCase(modelScope, modelProvenance, "model-case")
	if err != nil {
		t.Fatal(err)
	}
	assertModelView(t, c, caseModel{revision: 1}, "initial")
	return c
}

func assertModelView(t *testing.T, c events.Case, m caseModel, trace string) {
	t.Helper()
	got, err := c.View(modelScope)
	if err != nil || got != modelView(m) {
		t.Fatalf("%s: view=%+v err=%v want=%+v", trace, got, err, modelView(m))
	}
}

func modelStep(t *testing.T, c events.Case, m caseModel, action int, at time.Time, trace string) (events.Case, caseModel) {
	t.Helper()
	ch := events.Change{Action: modelActions[action], Actor: "analyst-" + fmt.Sprint(len(trace)), Reason: "synthetic-model: " + trace, At: at, ExpectedRevision: m.revision}
	// The reason budget is independent of history length, which is bounded at 8.
	if len(ch.Reason) > events.MaxFieldBytes {
		ch.Reason = "synthetic-model-transition"
	}
	next, audit, err := c.Apply(modelScope, ch)
	node := modelTransitions[m.node][action]
	if node < 0 {
		if err == nil || node == -1 && !errors.Is(err, events.ErrConflict) || next != c || audit != (events.Audit{}) {
			t.Fatalf("%s: failed transition changed case/audit or wrong result: err=%v audit=%+v", trace, err, audit)
		}
		assertModelView(t, next, m, trace)
		return next, m
	}
	want := caseModel{node: node, revision: m.revision + 1, last: at.UTC()}
	before, after := modelView(m), modelView(want)
	wantAudit := events.Audit{CaseID: "model-case", Action: ch.Action, Actor: ch.Actor, Reason: ch.Reason,
		PreviousState: before.State, State: after.State, PreviousMark: before.Mark, Mark: after.Mark,
		At: at.UTC(), Revision: want.revision, Provenance: modelProvenance}
	if err != nil || audit != wantAudit {
		t.Fatalf("%s: err=%v audit=%+v want=%+v", trace, err, audit, wantAudit)
	}
	assertModelView(t, next, want, trace)
	assertModelView(t, c, m, trace+" original value")
	return next, want
}

func TestCaseModelAllActionHistories(t *testing.T) {
	const depth = 8 // 4^8 complete histories; 87,380 checked prefix transitions.
	var transitions, histories int
	var walk func(events.Case, caseModel, int, string)
	walk = func(c events.Case, m caseModel, level int, trace string) {
		if level == depth {
			histories++
			return
		}
		for action := range modelActions {
			// Alternate equal and increasing instants, represented in a non-UTC
			// zone. Equal timestamps are allowed; every audit must be UTC.
			at := modelEpoch.Add(time.Duration(level/2) * time.Second).In(time.FixedZone("synthetic+0530", 19800))
			next, want := modelStep(t, c, m, action, at, trace+fmt.Sprint(action))
			transitions++
			walk(next, want, level+1, trace+fmt.Sprint(action))
		}
	}
	walk(newModelCase(t), caseModel{revision: 1}, 0, "")
	if transitions != 87380 || histories != 65536 {
		t.Fatalf("incomplete enumeration: transitions=%d histories=%d", transitions, histories)
	}
	t.Logf("checked %d transitions across %d length-%d histories", transitions, histories, depth)
}

func modelSeed(t *testing.T, actions []int) (events.Case, caseModel) {
	t.Helper()
	c, m := newModelCase(t), caseModel{revision: 1}
	for i, action := range actions {
		c, m = modelStep(t, c, m, action, modelEpoch.Add(time.Duration(i)*time.Second), fmt.Sprintf("seed-%d", i))
	}
	return c, m
}

func TestCaseModelFailureAtomicityAndRecovery(t *testing.T) {
	seeds := [][]int{nil, {0}, {1}, {3}, {0, 3}, {1, 3}, {0, 1, 2, 0, 1, 2}}
	for seed, actions := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			c, m := modelSeed(t, actions)
			valid := events.Change{Action: "close", Actor: "analyst", Reason: "synthetic-reason", At: modelEpoch.Add(time.Hour), ExpectedRevision: m.revision}
			type attempt struct {
				name  string
				scope events.Scope
				input events.Change
				err   error
			}
			var attempts []attempt
			for _, denied := range []struct {
				name  string
				scope events.Scope
			}{
				{"no-read", events.Scope{Tenant: modelScope.Tenant, Domain: modelScope.Domain, Manage: true}},
				{"no-manage", events.Scope{Tenant: modelScope.Tenant, Domain: modelScope.Domain, Read: true}},
				{"other-tenant", events.Scope{Tenant: "other", Domain: modelScope.Domain, Read: true, Manage: true}},
				{"other-domain", events.Scope{Tenant: modelScope.Tenant, Domain: "other", Read: true, Manage: true}},
			} {
				attempts = append(attempts, attempt{denied.name, denied.scope, valid, events.ErrDenied})
			}
			for _, revision := range []uint64{0, m.revision - 1, m.revision + 1, ^uint64(0)} {
				ch := valid
				ch.ExpectedRevision = revision
				attempts = append(attempts, attempt{fmt.Sprintf("revision-%d", revision), modelScope, ch, events.ErrConflict})
			}
			for _, field := range []string{"actor", "reason"} {
				for i, value := range []string{"", " leading", "trailing ", "line\nbreak", string([]byte{0xff}), strings.Repeat("x", 129)} {
					ch := valid
					if field == "actor" {
						ch.Actor = value
					} else {
						ch.Reason = value
					}
					attempts = append(attempts, attempt{fmt.Sprintf("%s-%d", field, i), modelScope, ch, events.ErrInvalid})
				}
			}
			for i, at := range []time.Time{{}, time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(2101, 1, 1, 0, 0, 0, 0, time.UTC)} {
				ch := valid
				ch.At = at
				attempts = append(attempts, attempt{fmt.Sprintf("time-%d", i), modelScope, ch, events.ErrInvalid})
			}
			if !m.last.IsZero() {
				ch := valid
				ch.At = m.last.Add(-time.Nanosecond)
				attempts = append(attempts, attempt{"backward-nanosecond", modelScope, ch, events.ErrInvalid})
			}
			unknown := valid
			unknown.Action = "reopen"
			attempts = append(attempts, attempt{"unknown-action", modelScope, unknown, events.ErrInvalid})
			for _, probe := range attempts {
				t.Run(probe.name, func(t *testing.T) {
					next, audit, err := c.Apply(probe.scope, probe.input)
					if !errors.Is(err, probe.err) || next != c || audit != (events.Audit{}) {
						t.Fatalf("non-atomic failure: err=%v want=%v audit=%+v", err, probe.err, audit)
					}
					assertModelView(t, next, m, "failure")
					// A failed future-dated request must not poison the private
					// timestamp. Apply every continuation to both returned and original
					// values at the last successful instant, not the failed timestamp.
					at := m.last
					if at.IsZero() {
						at = modelEpoch
					}
					for action := range modelActions {
						got, state := modelStep(t, next, m, action, at, "after-failure")
						control, controlState := modelStep(t, c, m, action, at, "after-failure")
						if got != control || state != controlState {
							t.Fatal("failure changed later behavior")
						}
					}
				})
			}
		})
	}
}

func TestCaseModelReadAndForkIsolation(t *testing.T) {
	c, m := modelSeed(t, []int{0})
	readOnly := modelScope
	readOnly.Manage = false
	got, err := c.View(readOnly)
	if err != nil || got != modelView(m) {
		t.Fatal("read grant must suffice for a view", got, err)
	}
	got.Mark, got.Provenance.Fixture = "changed", "changed"
	assertModelView(t, c, m, "detached view")
	for _, scope := range []events.Scope{{}, {Tenant: "other", Domain: modelScope.Domain, Read: true}, {Tenant: modelScope.Tenant, Domain: "other", Read: true}, {Tenant: modelScope.Tenant, Domain: modelScope.Domain, Manage: true}} {
		if view, err := c.View(scope); !errors.Is(err, events.ErrDenied) || view != (events.CaseView{}) {
			t.Fatal("denied view returned data", view, err)
		}
	}
	left, leftModel := modelStep(t, c, m, 1, modelEpoch, "left-fork")
	right, rightModel := modelStep(t, c, m, 3, modelEpoch, "right-fork")
	assertModelView(t, left, leftModel, "left remains independent")
	assertModelView(t, right, rightModel, "right remains independent")
	assertModelView(t, c, m, "source remains independent")
	// Both pure branches may succeed; this is deliberately NOT persistent CAS.
	if leftModel.revision != rightModel.revision || leftModel.node == rightModel.node {
		t.Fatal("unexpected fork results")
	}
}
