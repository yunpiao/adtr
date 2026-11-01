package assetassessment

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func flag(v bool) *bool { return &v }
func base() Input {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	return Input{Version: Version, Source: SyntheticSource, TenantID: "synthetic-tenant", DomainID: "synthetic-domain", ObjectID: "synthetic-object", ObservationID: "synthetic-observation", Kind: User, AsOf: now, ObservedAt: now}
}
func evaluate(t *testing.T, in Input) Result {
	t.Helper()
	r, e := Evaluate(in)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestAccountStatusTruthTable(t *testing.T) {
	for _, kind := range []Kind{User, Computer} {
		for _, tc := range []struct {
			name             string
			disabled, locked *bool
			want             string
		}{
			{"both_missing", nil, nil, "unknown"}, {"disabled_missing_locked_clear", nil, flag(false), "unknown"},
			{"disabled_missing_locked_set", nil, flag(true), "unknown"}, {"locked_missing", flag(false), nil, "unknown"},
			{"disabled_locked_missing", flag(true), nil, "disabled"}, {"disabled_locked_set", flag(true), flag(true), "disabled"},
			{"disabled_locked_clear", flag(true), flag(false), "disabled"}, {"locked", flag(false), flag(true), "locked"},
			{"enabled", flag(false), flag(false), "enabled"},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				in := base()
				in.Kind = kind
				in.Account = &AccountEvidence{Disabled: tc.disabled, Locked: tc.locked}
				r := evaluate(t, in)
				if r.AccountStatus.Value != tc.want {
					t.Fatalf("%+v", r)
				}
				if r.Activity.Value != "unknown" {
					t.Fatalf("missing login inferred: %+v", r)
				}
			})
		}
	}
}

func TestActivityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
		want string
	}{
		{"now", 0, "recent"}, {"before_boundary", InactivityWindow - time.Nanosecond, "recent"},
		{"boundary", InactivityWindow, "inactive"}, {"after_boundary", InactivityWindow + time.Nanosecond, "inactive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			last := in.AsOf.Add(-tc.age)
			in.Account = &AccountEvidence{Login: LoginObserved, LastLogin: &last}
			r := evaluate(t, in)
			if r.Activity.Value != tc.want {
				t.Fatalf("%+v", r.Activity)
			}
		})
	}
	for _, state := range []LoginState{"", LoginUnknown, LoginNever} {
		in := base()
		in.Account = &AccountEvidence{Login: state}
		r := evaluate(t, in)
		want := "unknown"
		if state == LoginNever {
			want = "never_observed"
		}
		if r.Activity.Value != want {
			t.Fatalf("state %q: %+v", state, r)
		}
	}
}

func TestFreshnessAndApplicability(t *testing.T) {
	for _, kind := range []Kind{User, Computer, Group} {
		for _, age := range []time.Duration{FreshnessWindow, FreshnessWindow + time.Nanosecond} {
			in := base()
			in.Kind = kind
			in.ObservedAt = in.AsOf.Add(-age)
			if kind == Group {
				in.Group = &GroupEvidence{SecurityEnabled: flag(true), Scope: "global"}
			} else {
				in.Account = &AccountEvidence{Disabled: flag(false), Locked: flag(false), Login: LoginNever}
			}
			r := evaluate(t, in)
			relevant := []Finding{r.AccountStatus, r.Activity}
			irrelevant := []Finding{r.GroupType, r.GroupScope}
			if kind == Group {
				relevant, irrelevant = irrelevant, relevant
			}
			for _, f := range irrelevant {
				if f.Value != "not_applicable" {
					t.Fatal(f)
				}
			}
			for _, f := range relevant {
				if age > FreshnessWindow {
					if f != (Finding{"unknown", "stale_observation"}) {
						t.Fatal(f)
					}
				} else if f.Value == "unknown" {
					t.Fatal(f)
				}
			}
		}
	}
}

func TestGroupClassifications(t *testing.T) {
	for _, security := range []*bool{nil, flag(false), flag(true)} {
		for _, scope := range []string{"", "unknown", "global", "domain_local", "universal"} {
			in := base()
			in.Kind = Group
			in.Group = &GroupEvidence{SecurityEnabled: security, Scope: scope}
			r := evaluate(t, in)
			want := "unknown"
			if security != nil {
				want = "distribution"
				if *security {
					want = "security"
				}
			}
			if r.GroupType.Value != want {
				t.Fatal(r)
			}
			want = scope
			if scope == "" {
				want = "unknown"
			}
			if r.GroupScope.Value != want {
				t.Fatal(r)
			}
		}
	}
	in := base()
	in.Kind = Group
	r := evaluate(t, in)
	if r.GroupType.Value != "unknown" || r.GroupScope.Value != "unknown" {
		t.Fatal(r)
	}
}

func TestRejectInvalidInputAtomically(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Input)
	}{
		{"version", func(i *Input) { i.Version = "production" }}, {"source", func(i *Input) { i.Source = "ad" }},
		{"missing_tenant", func(i *Input) { i.TenantID = "" }}, {"missing_domain", func(i *Input) { i.DomainID = "" }},
		{"missing_object", func(i *Input) { i.ObjectID = "" }}, {"missing_observation", func(i *Input) { i.ObservationID = "" }},
		{"whitespace", func(i *Input) { i.ObjectID = " object " }}, {"control", func(i *Input) { i.ObjectID = "x\ny" }},
		{"oversize_id", func(i *Input) { i.ObjectID = string(make([]byte, 257)) }},
		{"kind", func(i *Input) { i.Kind = "device" }}, {"zero_asof", func(i *Input) { i.AsOf = time.Time{} }},
		{"zero_observed", func(i *Input) { i.ObservedAt = time.Time{} }}, {"future_observed", func(i *Input) { i.ObservedAt = i.AsOf.Add(time.Nanosecond) }},
		{"user_group", func(i *Input) { i.Group = &GroupEvidence{} }}, {"group_account", func(i *Input) { i.Kind = Group; i.Account = &AccountEvidence{} }},
		{"invalid_scope", func(i *Input) { i.Kind = Group; i.Group = &GroupEvidence{Scope: "forest"} }},
		{"invalid_login", func(i *Input) { i.Account = &AccountEvidence{Login: "active"} }},
		{"observed_missing_time", func(i *Input) { i.Account = &AccountEvidence{Login: LoginObserved} }},
		{"observed_zero_time", func(i *Input) { i.Account = &AccountEvidence{Login: LoginObserved, LastLogin: &time.Time{}} }},
		{"future_login", func(i *Input) {
			last := i.ObservedAt.Add(time.Nanosecond)
			i.Account = &AccountEvidence{Login: LoginObserved, LastLogin: &last}
		}},
		{"never_with_time", func(i *Input) { i.Account = &AccountEvidence{Login: LoginNever, LastLogin: &i.AsOf} }},
		{"unknown_with_time", func(i *Input) { i.Account = &AccountEvidence{Login: LoginUnknown, LastLogin: &i.AsOf} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.change(&in)
			r, err := Evaluate(in)
			if !errors.Is(err, ErrInvalidInput) || !reflect.DeepEqual(r, Result{}) {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestDeterminismProvenanceAndNoMutation(t *testing.T) {
	in := base()
	in.AsOf = in.AsOf.In(time.FixedZone("synthetic-offset", 8*3600))
	in.Account = &AccountEvidence{Disabled: flag(true), Login: LoginNever}
	before, _ := json.Marshal(in)
	first := evaluate(t, in)
	for n := 0; n < 20; n++ {
		if got := evaluate(t, in); !reflect.DeepEqual(first, got) {
			t.Fatal("nondeterministic")
		}
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("mutated input")
	}
	if first.AsOf.Location() != time.UTC || first.ObservedAt.Location() != time.UTC || first.Version != Version || first.Source != SyntheticSource || first.TenantID != in.TenantID || first.DomainID != in.DomainID || first.ObjectID != in.ObjectID || first.ObservationID != in.ObservationID {
		t.Fatal(first)
	}
	// Results own their values and do not alias caller evidence.
	*in.Account.Disabled = false
	if first.AccountStatus.Value != "disabled" {
		t.Fatal("result aliases input")
	}
}
