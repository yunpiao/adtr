// Package detectionrules implements an offline, synthetic-only declarative evaluator.
// It is not a production authorization boundary or an AD event collector.
package detectionrules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	ContractVersion = "adtr.detection.simulated.v1"
	Provenance      = "synthetic-business-contract-2026-11-01"
	MaxRules        = 128
	MaxEvents       = 1000
	MaxConditions   = 32
	MaxValues       = 32
	MaxValueBytes   = 512
	MaxMatches      = 10000
)

var (
	ErrInvalid = errors.New("invalid synthetic detection input")
	ErrScope   = errors.New("synthetic detection scope mismatch")
	ErrLimit   = errors.New("synthetic detection limit exceeded")
)

type Scope struct {
	Tenant string `json:"tenant"`
	Domain string `json:"domain"`
}
type Condition struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}
type Rule struct {
	Contract   string      `json:"contract"`
	Provenance string      `json:"provenance"`
	ID         string      `json:"id"`
	Version    uint64      `json:"version"`
	Scope      Scope       `json:"scope"`
	Enabled    bool        `json:"enabled"`
	Combine    string      `json:"combine"`
	Conditions []Condition `json:"conditions"`
}
type Event struct {
	Provenance string            `json:"provenance"`
	ID         string            `json:"id"`
	Scope      Scope             `json:"scope"`
	Fields     map[string]string `json:"fields"`
}
type Match struct {
	Contract    string `json:"contract"`
	Provenance  string `json:"provenance"`
	RuleID      string `json:"rule_id"`
	RuleVersion uint64 `json:"rule_version"`
	RuleDigest  string `json:"rule_digest"`
	EventID     string `json:"event_id"`
	Scope       Scope  `json:"scope"`
}

// Program owns a deep copy of validated rules; concurrent Evaluate calls are safe.
type Program struct {
	scope   Scope
	rules   []Rule
	digests []string
}

func validText(s string, max int) bool {
	if len(s) == 0 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func validScope(s Scope) bool { return validText(s.Tenant, 128) && validText(s.Domain, 128) }
func allowedField(s string) bool {
	switch s {
	case "event.code", "event.category", "event.outcome", "actor.name", "target.name", "source.ip", "object.kind":
		return true
	}
	return false
}

// Compile validates every rule, including disabled rules, before constructing an
// immutable program. Scope is caller supplied, not inferred from untrusted rules.
func Compile(scope Scope, rules []Rule) (*Program, error) {
	if !validScope(scope) {
		return nil, ErrInvalid
	}
	if len(rules) == 0 || len(rules) > MaxRules {
		return nil, ErrLimit
	}
	p := &Program{scope: scope}
	ids := make(map[string]bool, len(rules))
	for _, r := range rules {
		if r.Scope != scope {
			return nil, ErrScope
		}
		if r.Contract != ContractVersion || r.Provenance != Provenance || !validText(r.ID, 128) || r.Version == 0 || ids[r.ID] {
			return nil, ErrInvalid
		}
		ids[r.ID] = true
		if r.Combine != "all" && r.Combine != "any" {
			return nil, ErrInvalid
		}
		if len(r.Conditions) == 0 || len(r.Conditions) > MaxConditions {
			return nil, ErrLimit
		}
		copyRule := r
		copyRule.Conditions = make([]Condition, len(r.Conditions))
		for i, c := range r.Conditions {
			if !allowedField(c.Field) {
				return nil, ErrInvalid
			}
			switch c.Operator {
			case "exists":
				if len(c.Values) != 0 {
					return nil, ErrInvalid
				}
			case "eq", "neq", "contains", "prefix":
				if len(c.Values) != 1 {
					return nil, ErrInvalid
				}
			case "in":
				if len(c.Values) == 0 || len(c.Values) > MaxValues {
					return nil, ErrLimit
				}
			default:
				return nil, ErrInvalid
			}
			for _, v := range c.Values {
				if !validText(v, MaxValueBytes) {
					return nil, ErrInvalid
				}
			}
			copyRule.Conditions[i] = Condition{Field: c.Field, Operator: c.Operator, Values: append([]string(nil), c.Values...)}
		}
		data, err := json.Marshal(copyRule)
		if err != nil {
			return nil, ErrInvalid
		}
		digest := sha256.Sum256(data)
		p.rules = append(p.rules, copyRule)
		p.digests = append(p.digests, hex.EncodeToString(digest[:]))
	}
	return p, nil
}

func conditionMatches(c Condition, e Event) bool {
	value, present := e.Fields[c.Field]
	if c.Operator == "exists" {
		return present
	}
	// Missing evidence never satisfies a negative comparison.
	if !present {
		return false
	}
	switch c.Operator {
	case "eq":
		return value == c.Values[0]
	case "neq":
		return value != c.Values[0]
	case "contains":
		return strings.Contains(value, c.Values[0])
	case "prefix":
		return strings.HasPrefix(value, c.Values[0])
	case "in":
		for _, v := range c.Values {
			if value == v {
				return true
			}
		}
	}
	return false
}

// Evaluate returns results in input event order then rule order. Any invalid
// event, cancellation or result overflow returns nil, never partial success.
// Callers must not mutate events concurrently with this call.
func (p *Program) Evaluate(ctx context.Context, events []Event) ([]Match, error) {
	if p == nil || ctx == nil || !validScope(p.scope) || len(p.rules) == 0 {
		return nil, ErrInvalid
	}
	if len(events) > MaxEvents {
		return nil, ErrLimit
	}
	ids := make(map[string]bool, len(events))
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Scope != p.scope {
			return nil, ErrScope
		}
		if e.Provenance != Provenance || !validText(e.ID, 128) || ids[e.ID] {
			return nil, ErrInvalid
		}
		ids[e.ID] = true
		if len(e.Fields) > 7 {
			return nil, ErrLimit
		}
		for k, v := range e.Fields {
			if !allowedField(k) || !validText(v, MaxValueBytes) {
				return nil, ErrInvalid
			}
		}
	}
	results := make([]Match, 0)
	for _, e := range events {
		for i, r := range p.rules {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !r.Enabled {
				continue
			}
			matched := r.Combine == "all"
			for _, c := range r.Conditions {
				m := conditionMatches(c, e)
				if r.Combine == "all" && !m {
					matched = false
					break
				}
				if r.Combine == "any" && m {
					matched = true
					break
				}
			}
			if matched {
				if len(results) == MaxMatches {
					return nil, ErrLimit
				}
				results = append(results, Match{Contract: ContractVersion, Provenance: Provenance, RuleID: r.ID, RuleVersion: r.Version, RuleDigest: p.digests[i], EventID: e.ID, Scope: p.scope})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
