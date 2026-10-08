// Package blockexceptions validates pure blocking-exception data. A match is
// data equality, never authorization to exempt a subject from blocking.
package blockexceptions

import (
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Code string

const (
	Required       Code = "required"
	Invalid        Code = "invalid"
	Unsupported    Code = "unsupported"
	Duplicate      Code = "duplicate"
	BudgetExceeded Code = "budget_exceeded"
)

// FieldError contains only a fixed field path and code, never supplied values.
type FieldError struct {
	Field string
	Code  Code
}

func (e *FieldError) Error() string      { return e.Field + ": " + string(e.Code) }
func fail(field string, code Code) error { return &FieldError{field, code} }

// Policy is a caller-selected protection budget, not a product capacity claim.
// MaxListItems bounds the aggregate number of query collection entries,
// including nested value lists. All fields must be explicitly positive.
type Policy struct {
	MaxIDBytes        int
	MaxUserValueBytes int
	MaxListItems      int
	MaxDeleteTargets  int
	MaxPageSize       int
}

func (p Policy) Validate() error {
	for _, v := range []struct {
		field string
		n     int
	}{{"policy.maxIDBytes", p.MaxIDBytes}, {"policy.maxUserValueBytes", p.MaxUserValueBytes}, {"policy.maxListItems", p.MaxListItems}, {"policy.maxDeleteTargets", p.MaxDeleteTargets}} {
		if v.n <= 0 {
			return fail(v.field, Invalid)
		}
	}
	if p.MaxPageSize < 10 {
		return fail("policy.maxPageSize", Invalid)
	}
	return nil
}

type Scope struct {
	TenantID     string
	DataSourceID string
	AppType      int
}
type ValueType int

const (
	User ValueType = 1
	IP   ValueType = 2
)

type Rule struct {
	RuleID    string
	Scope     Scope
	RuleName  string
	ValueType ValueType
	RuleValue string
	Remark    string
}

// Candidate intentionally has no rule name or remark.
type Candidate struct {
	Scope     Scope
	ValueType ValueType
	RuleValue string
}

// Edit cannot change name or scope. All mutable fields replace their old values.
type Edit struct {
	RuleID    string
	ValueType ValueType
	RuleValue string
	Remark    string
}

// Key is comparable and has no delimiter-encoding ambiguity.
type Key struct {
	Scope     Scope
	ValueType ValueType
	RuleValue string
}

func opaque(s, field string, maxBytes int) error {
	if !utf8.ValidString(s) {
		return fail(field, Invalid)
	}
	if s == "" {
		return fail(field, Required)
	}
	if len(s) > maxBytes {
		return fail(field, BudgetExceeded)
	}
	if strings.TrimSpace(s) != s {
		return fail(field, Invalid)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fail(field, Invalid)
		}
	}
	return nil
}
func textLength(s, field string, min, max int) error {
	if !utf8.ValidString(s) {
		return fail(field, Invalid)
	}
	n := utf8.RuneCountInString(s)
	if n < min {
		return fail(field, Required)
	}
	if n > max {
		return fail(field, Invalid)
	}
	return nil
}
func validScope(p Policy, s Scope) error {
	if err := opaque(s.TenantID, "scope.tenantID", p.MaxIDBytes); err != nil {
		return err
	}
	if err := opaque(s.DataSourceID, "scope.dataSourceID", p.MaxIDBytes); err != nil {
		return err
	}
	if s.AppType < 1 || s.AppType > 3 {
		return fail("scope.appType", Invalid)
	}
	return nil
}
func validValueType(t ValueType, field string) error {
	if t != User && t != IP {
		return fail(field, Invalid)
	}
	return nil
}
func normalizeValue(p Policy, t ValueType, s string) (string, error) {
	if err := validValueType(t, "valueType"); err != nil {
		return "", err
	}
	if t == User {
		if err := opaque(s, "ruleValue", p.MaxUserValueBytes); err != nil {
			return "", err
		}
		return s, nil
	}
	if !utf8.ValidString(s) {
		return "", fail("ruleValue", Invalid)
	}
	if s == "" {
		return "", fail("ruleValue", Required)
	}
	// Reject unsupported address forms before parsing. No DNS or network access.
	if strings.ContainsAny(s, "/%-") {
		return "", fail("ruleValue", Unsupported)
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", fail("ruleValue", Invalid)
	}
	if addr.Zone() != "" || addr.Is4In6() {
		return "", fail("ruleValue", Unsupported)
	}
	return addr.String(), nil
}
func validRemark(s string) error {
	if err := textLength(s, "remark", 0, 150); err != nil {
		return err
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fail("remark", Invalid)
		}
	}
	return nil
}

// NormalizeRule accepts an absent storage ID for creation; a supplied ID is validated.
func NormalizeRule(p Policy, r Rule) (Rule, error) {
	if err := p.Validate(); err != nil {
		return Rule{}, err
	}
	if r.RuleID != "" {
		if err := opaque(r.RuleID, "ruleID", p.MaxIDBytes); err != nil {
			return Rule{}, err
		}
	}
	if err := validScope(p, r.Scope); err != nil {
		return Rule{}, err
	}
	if err := textLength(r.RuleName, "ruleName", 1, 50); err != nil {
		return Rule{}, err
	}
	if err := opaque(r.RuleName, "ruleName", 200); err != nil {
		return Rule{}, err
	}
	if err := validRemark(r.Remark); err != nil {
		return Rule{}, err
	}
	value, err := normalizeValue(p, r.ValueType, r.RuleValue)
	if err != nil {
		return Rule{}, err
	}
	r.RuleValue = value
	return r, nil
}
func ValidateRule(p Policy, r Rule) error { _, err := NormalizeRule(p, r); return err }
func NormalizeCandidate(p Policy, c Candidate) (Candidate, error) {
	if err := p.Validate(); err != nil {
		return Candidate{}, err
	}
	if err := validScope(p, c.Scope); err != nil {
		return Candidate{}, err
	}
	value, err := normalizeValue(p, c.ValueType, c.RuleValue)
	if err != nil {
		return Candidate{}, err
	}
	c.RuleValue = value
	return c, nil
}

// Match validates both operands, even when their scopes already differ.
func Match(p Policy, r Rule, c Candidate) (bool, error) {
	nr, err := NormalizeRule(p, r)
	if err != nil {
		return false, err
	}
	nc, err := NormalizeCandidate(p, c)
	if err != nil {
		return false, err
	}
	return nr.Scope == nc.Scope && nr.ValueType == nc.ValueType && nr.RuleValue == nc.RuleValue, nil
}
func DuplicateKey(p Policy, r Rule) (Key, error) {
	n, err := NormalizeRule(p, r)
	if err != nil {
		return Key{}, err
	}
	return Key{n.Scope, n.ValueType, n.RuleValue}, nil
}
func ValidateDetailID(p Policy, id string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return opaque(id, "ruleID", p.MaxIDBytes)
}
func ApplyEdit(p Policy, old Rule, e Edit) (Rule, error) {
	if err := ValidateDetailID(p, old.RuleID); err != nil {
		return Rule{}, err
	}
	if err := ValidateRule(p, old); err != nil {
		return Rule{}, err
	}
	if err := ValidateDetailID(p, e.RuleID); err != nil {
		return Rule{}, err
	}
	if old.RuleID != e.RuleID {
		return Rule{}, fail("ruleID", Invalid)
	}
	next := old
	next.ValueType = e.ValueType
	next.RuleValue = e.RuleValue
	next.Remark = e.Remark
	return NormalizeRule(p, next)
}

// ValidateDeleteIDs returns an independent copy; it does not resolve ownership or delete.
func ValidateDeleteIDs(p Policy, ids []string) ([]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fail("ruleIDs", Required)
	}
	if len(ids) > p.MaxDeleteTargets {
		return nil, fail("ruleIDs", BudgetExceeded)
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if err := opaque(id, "ruleIDs", p.MaxIDBytes); err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			return nil, fail("ruleIDs", Duplicate)
		}
		seen[id] = struct{}{}
	}
	return append([]string(nil), ids...), nil
}
