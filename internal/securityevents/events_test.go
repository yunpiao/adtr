package securityevents

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testScope = Scope{Tenant: "synthetic-tenant", Domain: "synthetic-domain", Read: true, Manage: true}
var testProvenance = Provenance{Contract: ContractVersion, Fixture: "synthetic-events-001", Synthetic: true}

func raw(id string, seconds int) RawEvent {
	return RawEvent{ID: id, Tenant: testScope.Tenant, Domain: testScope.Domain, Kind: "login_failure", Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", SourceIP: "::ffff:192.0.2.1", OccurredAt: time.Date(2026, 1, 1, 0, 0, seconds, 0, time.UTC).Format(time.RFC3339Nano), Provenance: testProvenance}
}
func dataset(t *testing.T, rs ...RawEvent) *Dataset {
	t.Helper()
	d, err := Normalize(testScope, testProvenance, rs)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestNormalizeQueryAndIsolation(t *testing.T) {
	r := raw("b", 1)
	r.OccurredAt = "2026-01-01T01:00:01+01:00"
	input := []RawEvent{r, raw("a", 0)}
	d := dataset(t, input...)
	input[1].Actor = "changed"
	p, err := d.Query(testScope, Filter{Limit: 1})
	if err != nil || p.Total != 2 || p.Events[0].ID != "a" || p.Events[0].SourceIP != "192.0.2.1" || p.Provenance != testProvenance {
		t.Fatalf("%+v %v", p, err)
	}
	p.Events[0].Actor = "changed"
	p, err = d.Query(testScope, Filter{Limit: 10, From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), SourceIP: "::ffff:192.0.2.1", Actor: "synthetic-user", Device: "synthetic-device", Session: "synthetic-session", Kind: "login_failure"})
	if err != nil || p.Total != 1 || p.Events[0].ID != "a" {
		t.Fatalf("%+v %v", p, err)
	}
	p, _ = d.Query(testScope, Filter{Limit: 1, Offset: 1})
	if p.Events[0].ID != "b" || p.Events[0].OccurredAt.Location() != time.UTC {
		t.Fatal(p)
	}
	p, _ = d.Query(testScope, Filter{Limit: 1, Actor: "absent"})
	if p.Total != 0 || len(p.Events) != 0 {
		t.Fatal(p)
	}
}
func TestNormalizeRejectsAtomically(t *testing.T) {
	mutations := []func(*RawEvent){func(r *RawEvent) { r.ID = "" }, func(r *RawEvent) { r.ID = strings.Repeat("x", 129) }, func(r *RawEvent) { r.Actor = "bad\nactor" }, func(r *RawEvent) { r.Session = " " }, func(r *RawEvent) { r.Device = string([]byte{255}) }, func(r *RawEvent) { r.Kind = "unknown" }, func(r *RawEvent) { r.SourceIP = "bad" }, func(r *RawEvent) { r.SourceIP = "fe80::1%eth0" }, func(r *RawEvent) { r.OccurredAt = "1999-01-01T00:00:00Z" }, func(r *RawEvent) { r.OccurredAt = "bad" }, func(r *RawEvent) { r.Provenance.Synthetic = false }, func(r *RawEvent) { r.Provenance.Fixture = "other" }}
	for i, mutate := range mutations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := raw("bad", 0)
			mutate(&r)
			d, err := Normalize(testScope, testProvenance, []RawEvent{raw("good", 0), r})
			if !errors.Is(err, ErrInvalid) || d != nil {
				t.Fatalf("%v %v", d, err)
			}
		})
	}
	for _, p := range []Provenance{{}, {Contract: "v2", Fixture: "fixture", Synthetic: true}} {
		if _, err := Normalize(testScope, p, nil); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := Normalize(testScope, testProvenance, []RawEvent{raw("a", 0), raw("a", 1)}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Normalize(testScope, testProvenance, make([]RawEvent, MaxEvents+1)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	rs := make([]RawEvent, MaxEvents)
	for i := range rs {
		rs[i] = raw(fmt.Sprint(i), i)
	}
	d := dataset(t, rs...)
	p, err := d.Query(testScope, Filter{Limit: MaxEvents})
	if err != nil || p.Total != MaxEvents {
		t.Fatal(err, p.Total)
	}
	r := raw(strings.Repeat("x", MaxFieldBytes), 0)
	dataset(t, r)
}
func TestPermissionBoundaries(t *testing.T) {
	d := dataset(t, raw("a", 0))
	c, _ := NewCase(testScope, testProvenance, "case")
	for _, s := range []Scope{{}, {Tenant: testScope.Tenant, Domain: testScope.Domain, Manage: true}, {Tenant: "other", Domain: testScope.Domain, Read: true, Manage: true}, {Tenant: testScope.Tenant, Domain: "other", Read: true, Manage: true}} {
		if _, err := d.Query(s, Filter{Limit: 1}); !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
		if _, err := d.CorrelateFailures(s, time.Minute, 2); !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
		if _, err := c.View(s); !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
		if _, _, err := c.Apply(s, Change{}); !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
	}
	r := raw("a", 0)
	r.Domain = "other"
	if _, err := Normalize(testScope, testProvenance, []RawEvent{r}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	s := testScope
	s.Manage = false
	if _, err := NewCase(s, testProvenance, "case"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, _, err := c.Apply(s, Change{}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := Normalize(Scope{}, testProvenance, nil); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	var nilDataset *Dataset
	if _, err := nilDataset.Query(testScope, Filter{Limit: 1}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
func TestInvalidQueryAndCorrelation(t *testing.T) {
	d := dataset(t)
	at := time.Now()
	for _, f := range []Filter{{}, {Limit: MaxEvents + 1}, {Limit: 1, Offset: -1}, {Limit: 1, Offset: MaxEvents + 1}, {Limit: 1, From: at, Until: at}, {Limit: 1, Kind: "unknown"}, {Limit: 1, Actor: " bad"}, {Limit: 1, SourceIP: "invalid"}, {Limit: 1, SourceIP: "fe80::1%eth0"}} {
		if _, err := d.Query(testScope, f); !errors.Is(err, ErrInvalid) {
			t.Fatal(f, err)
		}
	}
	for _, window := range []time.Duration{0, -1, time.Second - 1, MaxWindow + 1} {
		if _, err := d.CorrelateFailures(testScope, window, 2); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, threshold := range []int{0, 1, MaxEvents + 1} {
		if _, err := d.CorrelateFailures(testScope, time.Minute, threshold); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	got, err := d.CorrelateFailures(testScope, MaxWindow, MaxEvents)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
func TestCorrelationWindowAndDeterminism(t *testing.T) {
	rs := []RawEvent{raw("e", 121), raw("d", 120), raw("c", 60), raw("b", 59), raw("a", 0)}
	other := raw("other", 1)
	other.Session = "other"
	rs = append(rs, other)
	success := raw("success", 30)
	success.Kind = "login_success"
	rs = append(rs, success)
	d := dataset(t, rs...)
	got, err := d.CorrelateFailures(testScope, time.Minute, 2)
	if err != nil || len(got) != 2 || !reflect.DeepEqual(got[0].EvidenceIDs, []string{"a", "b"}) || !reflect.DeepEqual(got[1].EvidenceIDs, []string{"d", "e"}) || got[0].Provenance != testProvenance {
		t.Fatalf("%+v %v", got, err)
	}
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	otherGot, _ := dataset(t, rs...).CorrelateFailures(testScope, time.Minute, 2)
	if !reflect.DeepEqual(got, otherGot) {
		t.Fatal("not deterministic")
	}
	got[0].EvidenceIDs[0] = "changed"
	otherGot, _ = d.CorrelateFailures(testScope, time.Minute, 2)
	if otherGot[0].EvidenceIDs[0] != "a" {
		t.Fatal("aliased evidence")
	}
}
func TestCaseStateMachine(t *testing.T) {
	c, err := NewCase(testScope, testProvenance, "case")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, action := range []string{"mark_suspicious", "mark_benign", "unmark", "close"} {
		old := c
		next, audit, err := c.Apply(testScope, Change{Action: action, Actor: "synthetic-reviewer", Reason: "synthetic-test", At: at, ExpectedRevision: uint64(i + 1)})
		if err != nil || next.revision != uint64(i+2) || audit.Revision != next.revision || audit.Provenance != testProvenance || old.revision != uint64(i+1) {
			t.Fatalf("%+v %+v %v", next, audit, err)
		}
		c = next
	}
	view, _ := c.View(testScope)
	if view.State != "closed" || view.Revision != 5 || view.Mark != "" {
		t.Fatal(view)
	}
	for _, ch := range []Change{{Action: "close", ExpectedRevision: 4}, {Action: "close", ExpectedRevision: 5, Actor: "a", Reason: "reason", At: at}} {
		next, audit, err := c.Apply(testScope, ch)
		if err == nil || next != c || audit != (Audit{}) {
			t.Fatal(next, audit, err)
		}
	}
	c, _ = NewCase(testScope, testProvenance, "case")
	for _, ch := range []Change{{Action: "unknown", ExpectedRevision: 1, Actor: "a", Reason: "reason", At: at}, {Action: "close", ExpectedRevision: 1, Actor: "a", At: at}, {Action: "close", ExpectedRevision: 1, Actor: "a", Reason: "reason"}, {Action: "unmark", ExpectedRevision: 1, Actor: "a", Reason: "reason", At: at}} {
		next, _, err := c.Apply(testScope, ch)
		if err == nil || next != c {
			t.Fatal(next, err)
		}
	}
	if _, err := NewCase(testScope, Provenance{}, "case"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestAuditCannotMoveBackwards(t *testing.T) {
	c, _ := NewCase(testScope, testProvenance, "case")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c, _, _ = c.Apply(testScope, Change{Action: "mark_benign", Actor: "reviewer", Reason: "synthetic", At: at, ExpectedRevision: 1})
	next, _, err := c.Apply(testScope, Change{Action: "close", Actor: "reviewer", Reason: "synthetic", At: at.Add(-time.Second), ExpectedRevision: 2})
	if !errors.Is(err, ErrInvalid) || next != c {
		t.Fatal(next, err)
	}
	d := dataset(t)
	if _, err := d.Query(testScope, Filter{Limit: 1, SourceIP: strings.Repeat("1", 65)}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestEqualTimeOrderingAndIdentitySeparation(t *testing.T) {
	rs := []RawEvent{raw("z", 0), raw("a", 0)}
	actor := raw("actor", 0)
	actor.Actor = "another-user"
	device := raw("device", 0)
	device.Device = "another-device"
	rs = append(rs, actor, device)
	d := dataset(t, rs...)
	p, _ := d.Query(testScope, Filter{Limit: MaxEvents})
	if p.Events[0].ID != "a" || p.Events[3].ID != "z" {
		t.Fatal(p)
	}
	got, err := d.CorrelateFailures(testScope, time.Second, 2)
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].EvidenceIDs, []string{"a", "z"}) {
		t.Fatal(got, err)
	}
	var c Case
	if _, err := c.View(testScope); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
