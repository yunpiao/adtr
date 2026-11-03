package simulationassetassessment

import (
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/assetassessment"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testScope() Scope {
	return Scope{Tenant: "tenant", Domain: "domain", ActorID: 7, Epoch: 1, Read: true, Manage: true}
}
func TestFixtureReportIndependentTruth(t *testing.T) {
	s := testScope()
	o := fixture(s.Tenant, s.Domain)
	r, e := assess(s, o, CreateInput{ID: "report", ScenarioID: "baseline"}, o.Scenarios[0].AsOf)
	if e != nil {
		t.Fatal(e)
	}
	truth := [][4]string{{"enabled", "recent", "not_applicable", "not_applicable"}, {"disabled", "never_observed", "not_applicable", "not_applicable"}, {"locked", "inactive", "not_applicable", "not_applicable"}, {"unknown", "unknown", "not_applicable", "not_applicable"}, {"enabled", "recent", "not_applicable", "not_applicable"}, {"enabled", "inactive", "not_applicable", "not_applicable"}, {"unknown", "unknown", "not_applicable", "not_applicable"}, {"not_applicable", "not_applicable", "security", "global"}, {"not_applicable", "not_applicable", "distribution", "domain_local"}, {"not_applicable", "not_applicable", "security", "universal"}, {"not_applicable", "not_applicable", "unknown", "unknown"}}
	for i, row := range r.Rows {
		got := [4]string{row.AccountStatus.Value, row.Activity.Value, row.GroupType.Value, row.GroupScope.Value}
		if got != truth[i] {
			t.Fatalf("row %d got %v want %v", i, got, truth[i])
		}
	}
	want := Summary{Total: 11, Complete: false, Kinds: map[string]int{"user": 4, "computer": 3, "group": 4}, AccountStatus: map[string]int{"enabled": 3, "disabled": 1, "locked": 1, "unknown": 2, "not_applicable": 4}, Activity: map[string]int{"recent": 2, "inactive": 2, "never_observed": 1, "unknown": 2, "not_applicable": 4}, GroupType: map[string]int{"security": 2, "distribution": 1, "unknown": 1, "not_applicable": 7}, GroupScope: map[string]int{"global": 1, "domain_local": 1, "universal": 1, "unknown": 1, "not_applicable": 7}, Reasons: map[string]int{"explicit_flags_clear": 3, "explicit_disabled_flag": 1, "explicit_locked_flag": 1, "missing_evidence": 6, "last_login_within_window": 2, "last_login_at_or_before_boundary": 2, "explicit_never_assertion": 1, "explicit_security_flag": 3, "explicit_scope": 3, "asset_kind": 22}}
	if !reflect.DeepEqual(r.Summary, want) {
		t.Fatalf("aggregate got %#v want %#v", r.Summary, want)
	}
	if e = validateReport(s, r); e != nil {
		t.Fatal(e)
	}
	r.Summary.AccountStatus["enabled"]++
	r.Hash = ""
	r.Hash = digest(r)
	if validateReport(s, r) == nil {
		t.Fatal("rehashed aggregate tamper accepted")
	}
}
func TestAssessmentBoundariesAndEvidence(t *testing.T) {
	s := testScope()
	o := fixture(s.Tenant, s.Domain)
	base := o.Objects[0]
	at := base.AsOf
	for _, delta := range []time.Duration{-1, 0, 1} {
		in := base
		in.Account = &assetassessment.AccountEvidence{Disabled: boolp(false), Locked: boolp(false), Login: assetassessment.LoginObserved, LastLogin: timep(at.Add(-assetassessment.InactivityWindow - delta))}
		r, e := assetassessment.Evaluate(in)
		want := "inactive"
		if delta < 0 {
			want = "recent"
		}
		if e != nil || r.Activity.Value != want {
			t.Fatalf("30d %d %v %v", delta, r, e)
		}
	}
	for _, delta := range []time.Duration{-1, 0, 1} {
		in := base
		in.AsOf = in.ObservedAt.Add(assetassessment.FreshnessWindow + delta)
		r, e := assetassessment.Evaluate(in)
		want := "enabled"
		if delta > 0 {
			want = "unknown"
		}
		if e != nil || r.AccountStatus.Value != want {
			t.Fatalf("24h %d %v %v", delta, r, e)
		}
	}
	for _, a := range []*assetassessment.AccountEvidence{{Login: assetassessment.LoginNever, LastLogin: timep(at)}, {Login: assetassessment.LoginObserved, LastLogin: timep(at.Add(1))}, {Login: assetassessment.LoginUnknown, LastLogin: timep(at)}} {
		in := base
		in.Account = a
		if _, e := assetassessment.Evaluate(in); e == nil {
			t.Fatal("contradictory evidence accepted")
		}
	}
	for _, d := range []*bool{nil, boolp(false), boolp(true)} {
		for _, l := range []*bool{nil, boolp(false), boolp(true)} {
			in := base
			in.Account = &assetassessment.AccountEvidence{Disabled: d, Locked: l}
			r, e := assetassessment.Evaluate(in)
			want := "unknown"
			if d != nil && *d {
				want = "disabled"
			} else if d != nil && l != nil {
				want = "enabled"
				if *l {
					want = "locked"
				}
			}
			if e != nil || r.AccountStatus.Value != want {
				t.Fatal("disabled priority truth mismatch")
			}
		}
	}
	for _, scenario := range []string{"freshness-boundary", "stale"} {
		r, e := assess(s, o, CreateInput{ID: "r", ScenarioID: scenario}, at)
		if e != nil {
			t.Fatal(e)
		}
		if scenario == "stale" && (r.Summary.AccountStatus["unknown"] != 7 || r.Summary.Reasons["stale_observation"] != 22) {
			t.Fatalf("stale aggregate %#v", r.Summary)
		}
	}
}
func TestTamperBoundsAndDefaultDeny(t *testing.T) {
	s := testScope()
	o := fixture(s.Tenant, s.Domain)
	if validateObservation(o, s.Tenant, s.Domain) != nil {
		t.Fatal("fixture invalid")
	}
	o.Complete = true
	o.Hash = ""
	o.Hash = digest(o)
	if validateObservation(o, s.Tenant, s.Domain) == nil {
		t.Fatal("rehashed fabricated completeness accepted")
	}
	if authorize(nil, s, false) != ErrDenied {
		t.Fatal("nil tx allowed")
	}
	if validFilter(Filter{Limit: 21}) || validFilter(Filter{Limit: 1, Kind: "device"}) || validFilter(Filter{Limit: 1, Offset: -1}) {
		t.Fatal("invalid filter accepted")
	}
	b, e := marshalBounded(map[string]string{"x": "y"}, 9)
	if !errors.Is(e, ErrLimit) || b != nil {
		t.Fatal("jsonb whitespace budget")
	}
	if _, e = marshalBounded(strings.Repeat("x", MaxRecordBytes), MaxRecordBytes); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	var target Observation
	if decode([]byte(`{"arbitrary":true}`), &target) == nil {
		t.Fatal("unknown field accepted")
	}
	if decode([]byte(`{} {}`), &target) == nil {
		t.Fatal("trailing value accepted")
	}
	o = fixture(s.Tenant, s.Domain)
	r, _ := assess(s, o, CreateInput{ID: "r", ScenarioID: "baseline"}, o.Scenarios[0].AsOf)
	s.Epoch++
	if validateReport(s, r) != ErrNotFound {
		t.Fatal("old epoch allowed")
	}
	b, _ = json.Marshal(o)
	var restored Observation
	if decode(b, &restored) != nil || validateObservation(restored, o.TenantID, o.DomainID) != nil {
		t.Fatal("json roundtrip")
	}
}
