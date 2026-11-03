package simulationassetassessment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/assetassessment"
	"io"
	"regexp"
	"time"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// PostgreSQL JSONB adds spaces after structural separators. Account for those
// before writing so its strict storage check cannot be accidentally exceeded.
func marshalBounded(v any, limit int) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, ErrInvalid
	}
	size := len(b)
	quoted, escaped := false, false
	for _, c := range b {
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		} else if c == ':' || c == ',' {
			size++
		}
	}
	if size > limit {
		return nil, ErrLimit
	}
	return b, nil
}
func decode(b []byte, v any) error {
	if len(b) > MaxRecordBytes {
		return ErrLimit
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func boolp(v bool) *bool           { return &v }
func timep(v time.Time) *time.Time { return &v }

// fixture is the sole producer. All inputs are authored here; neither HTTP nor
// directory observations can enter this dataset through an exported API.
func fixture(tenant, domain string) Observation {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	o := Observation{Contract: Contract, Simulation: true, ID: "synthetic-assets-v1", TenantID: tenant, DomainID: domain, Complete: false, CompletenessReason: "fixed_synthetic_subset_not_directory_inventory", Objects: []assetassessment.Input{}, Scenarios: []Scenario{{"baseline", at}, {"freshness-boundary", at.Add(24 * time.Hour)}, {"stale", at.Add(24*time.Hour + time.Nanosecond)}}}
	add := func(id string, k assetassessment.Kind, a *assetassessment.AccountEvidence, g *assetassessment.GroupEvidence) {
		o.Objects = append(o.Objects, assetassessment.Input{Version: assetassessment.Version, Source: assetassessment.SyntheticSource, TenantID: tenant, DomainID: domain, ObjectID: id, ObservationID: o.ID, Kind: k, ObservedAt: at, AsOf: at, Account: a, Group: g})
	}
	add("user-enabled-recent", assetassessment.User, &assetassessment.AccountEvidence{Disabled: boolp(false), Locked: boolp(false), Login: assetassessment.LoginObserved, LastLogin: timep(at.Add(-time.Hour))}, nil)
	add("user-disabled-never", assetassessment.User, &assetassessment.AccountEvidence{Disabled: boolp(true), Locked: boolp(true), Login: assetassessment.LoginNever}, nil)
	add("user-locked-boundary", assetassessment.User, &assetassessment.AccountEvidence{Disabled: boolp(false), Locked: boolp(true), Login: assetassessment.LoginObserved, LastLogin: timep(at.Add(-30 * 24 * time.Hour))}, nil)
	add("user-unknown-disabled", assetassessment.User, &assetassessment.AccountEvidence{Locked: boolp(true), Login: assetassessment.LoginUnknown}, nil)
	add("computer-recent-boundary", assetassessment.Computer, &assetassessment.AccountEvidence{Disabled: boolp(false), Locked: boolp(false), Login: assetassessment.LoginObserved, LastLogin: timep(at.Add(-30*24*time.Hour + time.Nanosecond))}, nil)
	add("computer-inactive-boundary", assetassessment.Computer, &assetassessment.AccountEvidence{Disabled: boolp(false), Locked: boolp(false), Login: assetassessment.LoginObserved, LastLogin: timep(at.Add(-30*24*time.Hour - time.Nanosecond))}, nil)
	add("computer-missing", assetassessment.Computer, nil, nil)
	add("group-security-global", assetassessment.Group, nil, &assetassessment.GroupEvidence{SecurityEnabled: boolp(true), Scope: "global"})
	add("group-distribution-local", assetassessment.Group, nil, &assetassessment.GroupEvidence{SecurityEnabled: boolp(false), Scope: "domain_local"})
	add("group-security-universal", assetassessment.Group, nil, &assetassessment.GroupEvidence{SecurityEnabled: boolp(true), Scope: "universal"})
	add("group-unknown", assetassessment.Group, nil, nil)
	o.Hash = digest(o)
	return o
}
func validateObservation(o Observation, tenant, domain string) error {
	expected := fixture(tenant, domain)
	if digest(o) != digest(expected) {
		return ErrInvalid
	}
	_, e := marshalBounded(o, MaxRecordBytes)
	return e
}
func assess(s Scope, o Observation, in CreateInput, at time.Time) (Report, error) {
	r := Report{Contract: Contract, Simulation: true, ID: in.ID, TenantID: s.Tenant, DomainID: s.Domain, ActorID: s.ActorID, Epoch: s.Epoch, ObservationID: o.ID, SourceHash: o.Hash, ScenarioID: in.ScenarioID, Version: assetassessment.Version, CreatedAt: at.UTC(), Rows: []assetassessment.Result{}}
	for _, scenario := range o.Scenarios {
		if scenario.ID == in.ScenarioID {
			r.AsOf = scenario.AsOf
		}
	}
	if r.AsOf.IsZero() || len(o.Objects) > MaxObjects {
		return Report{}, ErrInvalid
	}
	r.Summary = Summary{Complete: o.Complete, Kinds: map[string]int{}, AccountStatus: map[string]int{}, Activity: map[string]int{}, GroupType: map[string]int{}, GroupScope: map[string]int{}, Reasons: map[string]int{}}
	for _, input := range o.Objects {
		input.AsOf = r.AsOf
		row, e := assetassessment.Evaluate(input)
		if e != nil {
			return Report{}, ErrInvalid
		}
		r.Rows = append(r.Rows, row)
		r.Summary.Total++
		r.Summary.Kinds[string(row.Kind)]++
		r.Summary.AccountStatus[row.AccountStatus.Value]++
		r.Summary.Activity[row.Activity.Value]++
		r.Summary.GroupType[row.GroupType.Value]++
		r.Summary.GroupScope[row.GroupScope.Value]++
		for _, f := range []assetassessment.Finding{row.AccountStatus, row.Activity, row.GroupType, row.GroupScope} {
			r.Summary.Reasons[f.Reason]++
		}
	}
	r.Hash = digest(r)
	_, e := marshalBounded(r, MaxRecordBytes)
	return r, e
}
func validateReport(s Scope, r Report) error {
	if r.Epoch != s.Epoch {
		return ErrNotFound
	}
	if r.ActorID != s.ActorID || r.TenantID != s.Tenant || r.DomainID != s.Domain || !identifier.MatchString(r.ID) || r.CreatedAt.IsZero() {
		return ErrInvalid
	}
	o := fixture(s.Tenant, s.Domain)
	expected, e := assess(s, o, CreateInput{ID: r.ID, ScenarioID: r.ScenarioID}, r.CreatedAt)
	if e != nil || digest(expected) != digest(r) {
		return ErrInvalid
	}
	return nil
}
func validInput(in CreateInput) bool {
	return identifier.MatchString(in.ID) && identifier.MatchString(in.OperationID) && identifier.MatchString(in.ObservationID) && identifier.MatchString(in.ScenarioID) && hashPattern.MatchString(in.ExpectedSourceHash)
}
func validFilter(f Filter) bool {
	has := func(v string, allowed ...string) bool {
		if v == "" {
			return true
		}
		for _, a := range allowed {
			if v == a {
				return true
			}
		}
		return false
	}
	return f.Offset >= 0 && f.Offset <= MaxObjects && f.Limit >= 1 && f.Limit <= MaxPageSize && has(f.Kind, "user", "computer", "group") && has(f.AccountStatus, "enabled", "disabled", "locked", "unknown", "not_applicable") && has(f.Activity, "recent", "inactive", "never_observed", "unknown", "not_applicable") && has(f.GroupType, "security", "distribution", "unknown", "not_applicable") && has(f.GroupScope, "global", "domain_local", "universal", "unknown", "not_applicable")
}
