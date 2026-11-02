// Package detectionexceptions provides immutable synthetic exception snapshots.
// It does not suppress detections or authorize access to production data.
package detectionexceptions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/detectionrules"
)

const (
	ContractVersion = "adtr.detection-exceptions.simulated.v1"
	Provenance      = detectionrules.Provenance
	MaxEntries      = 128
	MaxConditions   = 7
	MaxAnnotations  = 10000
)

var (
	ErrInvalid    = errors.New("invalid synthetic exception input")
	ErrPermission = errors.New("synthetic exception permission denied")
	ErrScope      = errors.New("synthetic exception scope mismatch")
	ErrConflict   = errors.New("synthetic exception revision conflict")
	ErrLimit      = errors.New("synthetic exception limit exceeded")
	ErrNotFound   = errors.New("synthetic exception not found")
)

type Scope = detectionrules.Scope
type Condition = detectionrules.Condition
type Event = detectionrules.Event
type Match = detectionrules.Match

// Access is a synthetic caller assertion, not a substitute for runtime RBAC.
type Access struct {
	Scope Scope `json:"scope"`
	Read  bool  `json:"read"`
	Write bool  `json:"write"`
}
type Exception struct {
	ID         string      `json:"id"`
	Scope      Scope       `json:"scope"`
	RuleID     string      `json:"rule_id,omitempty"`
	RuleDigest string      `json:"rule_digest,omitempty"`
	Global     bool        `json:"global"`
	Enabled    bool        `json:"enabled"`
	Archived   bool        `json:"archived"`
	Reason     string      `json:"reason"`
	NotBefore  time.Time   `json:"not_before"`
	ExpiresAt  time.Time   `json:"expires_at"`
	Conditions []Condition `json:"conditions"`
}
type Snapshot struct {
	Contract   string      `json:"contract"`
	Provenance string      `json:"provenance"`
	Scope      Scope       `json:"scope"`
	Revision   uint64      `json:"revision"`
	Entries    []Exception `json:"entries"`
}
type Annotation struct {
	ExceptionID string `json:"exception_id"`
	Reason      string `json:"reason"`
	Simulated   bool   `json:"simulated"`
}
type AnnotatedMatch struct {
	Match      Match        `json:"match"`
	Exceptions []Annotation `json:"exceptions"`
}

func textOK(s string, max int) bool {
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
func scopeOK(s Scope) bool { return textOK(s.Tenant, 128) && textOK(s.Domain, 128) }
func digestOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func New(scope Scope) (Snapshot, error) {
	if !scopeOK(scope) {
		return Snapshot{}, ErrInvalid
	}
	return Snapshot{Contract: ContractVersion, Provenance: Provenance, Scope: scope, Revision: 1, Entries: []Exception{}}, nil
}
func validateEntry(scope Scope, e Exception) error {
	if e.Archived && e.Enabled {
		return ErrInvalid
	}
	if e.Scope != scope {
		return ErrScope
	}
	if !textOK(e.ID, 128) || !textOK(e.Reason, 512) || e.NotBefore.IsZero() || e.ExpiresAt.IsZero() || !e.NotBefore.Before(e.ExpiresAt) || e.NotBefore.Year() < 1 || e.ExpiresAt.Year() > 9999 {
		return ErrInvalid
	}
	if e.Global {
		if e.RuleID != "" || e.RuleDigest != "" {
			return ErrInvalid
		}
	} else if !textOK(e.RuleID, 128) || !digestOK(e.RuleDigest) {
		return ErrInvalid
	}
	if len(e.Conditions) == 0 || len(e.Conditions) > MaxConditions {
		return ErrLimit
	}
	fields := map[string]bool{}
	for _, c := range e.Conditions {
		if fields[c.Field] {
			return ErrInvalid
		}
		fields[c.Field] = true
		if c.Operator != "eq" && c.Operator != "in" {
			return ErrInvalid
		}
	}
	_, err := detectionrules.Compile(scope, []detectionrules.Rule{{Contract: detectionrules.ContractVersion, Provenance: Provenance, ID: e.ID, Version: 1, Scope: scope, Combine: "all", Conditions: e.Conditions}})
	if err != nil {
		return ErrInvalid
	}
	return nil
}
func validate(s Snapshot) error {
	if s.Contract != ContractVersion || s.Provenance != Provenance || s.Revision == 0 || !scopeOK(s.Scope) {
		return ErrInvalid
	}
	if len(s.Entries) > MaxEntries {
		return ErrLimit
	}
	ids := map[string]bool{}
	for _, e := range s.Entries {
		if ids[e.ID] {
			return ErrInvalid
		}
		ids[e.ID] = true
		if err := validateEntry(s.Scope, e); err != nil {
			return err
		}
	}
	return nil
}
func guard(a Access, s Snapshot, write bool) error {
	if !a.Read || (write && !a.Write) {
		return ErrPermission
	}
	if a.Scope != s.Scope {
		return ErrScope
	}
	return validate(s)
}
func clone(s Snapshot) Snapshot {
	out := s
	out.Entries = make([]Exception, len(s.Entries))
	for i, e := range s.Entries {
		out.Entries[i] = e
		out.Entries[i].Conditions = make([]Condition, len(e.Conditions))
		for j, c := range e.Conditions {
			out.Entries[i].Conditions[j] = c
			out.Entries[i].Conditions[j].Values = append([]string(nil), c.Values...)
		}
	}
	return out
}
func List(a Access, s Snapshot) ([]Exception, error) {
	if err := guard(a, s, false); err != nil {
		return nil, err
	}
	return clone(s).Entries, nil
}
func writable(a Access, s Snapshot, revision uint64) error {
	if err := guard(a, s, true); err != nil {
		return err
	}
	if revision != s.Revision {
		return ErrConflict
	}
	if revision == math.MaxUint64 {
		return ErrLimit
	}
	return nil
}

// Add accepts disabled drafts only; enabling requires an explicit Edit.
func Add(a Access, s Snapshot, revision uint64, e Exception) (Snapshot, error) {
	if err := writable(a, s, revision); err != nil {
		return Snapshot{}, err
	}
	if e.Enabled || e.Archived {
		return Snapshot{}, ErrInvalid
	}
	if len(s.Entries) == MaxEntries {
		return Snapshot{}, ErrLimit
	}
	for _, old := range s.Entries {
		if old.ID == e.ID {
			return Snapshot{}, ErrConflict
		}
	}
	if err := validateEntry(s.Scope, e); err != nil {
		return Snapshot{}, err
	}
	out := clone(s)
	out.Entries = append(out.Entries, e)
	out.Revision++
	return clone(out), nil
}
func Edit(a Access, s Snapshot, revision uint64, e Exception) (Snapshot, error) {
	if err := writable(a, s, revision); err != nil {
		return Snapshot{}, err
	}
	if err := validateEntry(s.Scope, e); err != nil {
		return Snapshot{}, err
	}
	out := clone(s)
	for i, old := range out.Entries {
		if old.ID == e.ID {
			if old.Archived || e.Archived {
				return Snapshot{}, ErrInvalid
			}
			out.Entries[i] = e
			out.Revision++
			return clone(out), nil
		}
	}
	return Snapshot{}, ErrNotFound
}
func Archive(a Access, s Snapshot, revision uint64, id string) (Snapshot, error) {
	if err := writable(a, s, revision); err != nil {
		return Snapshot{}, err
	}
	out := clone(s)
	for i, e := range out.Entries {
		if e.ID == id {
			if e.Archived {
				return Snapshot{}, ErrConflict
			}
			out.Entries[i].Archived = true
			out.Entries[i].Enabled = false
			out.Revision++
			return out, nil
		}
	}
	return Snapshot{}, ErrNotFound
}

// Import atomically appends disabled, unarchived drafts. Existing IDs conflict.
func Import(a Access, s Snapshot, revision uint64, entries []Exception) (Snapshot, error) {
	if err := writable(a, s, revision); err != nil {
		return Snapshot{}, err
	}
	if len(entries) == 0 || len(entries) > MaxEntries-len(s.Entries) {
		return Snapshot{}, ErrLimit
	}
	out := clone(s)
	ids := map[string]bool{}
	for _, e := range s.Entries {
		ids[e.ID] = true
	}
	for _, e := range entries {
		if ids[e.ID] {
			return Snapshot{}, ErrConflict
		}
		ids[e.ID] = true
		if e.Enabled || e.Archived {
			return Snapshot{}, ErrInvalid
		}
		if err := validateEntry(s.Scope, e); err != nil {
			return Snapshot{}, err
		}
		out.Entries = append(out.Entries, e)
	}
	out.Revision++
	return clone(out), nil
}

// Export returns deterministic JSON in snapshot entry order, never changing state.
func Export(a Access, s Snapshot) ([]byte, error) {
	if err := guard(a, s, false); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}
func eventProgram(scope Scope) (*detectionrules.Program, error) {
	return detectionrules.Compile(scope, []detectionrules.Rule{{Contract: detectionrules.ContractVersion, Provenance: Provenance, ID: "event-validation", Version: 1, Scope: scope, Combine: "all", Conditions: []Condition{{Field: "event.code", Operator: "exists"}}}})
}

// PreviewFromEvent creates a disabled rule-bound draft using sorted exact fields.
// It neither writes the snapshot nor enables a global exception implicitly.
func PreviewFromEvent(a Access, s Snapshot, event Event, id, reason, ruleID, ruleDigest string, notBefore, expiresAt time.Time) (Exception, error) {
	if err := guard(a, s, false); err != nil {
		return Exception{}, err
	}
	p, _ := eventProgram(s.Scope)
	if _, err := p.Evaluate(context.Background(), []Event{event}); err != nil {
		return Exception{}, err
	}
	keys := make([]string, 0, len(event.Fields))
	for k := range event.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	e := Exception{ID: id, Scope: s.Scope, RuleID: ruleID, RuleDigest: ruleDigest, Reason: reason, NotBefore: notBefore, ExpiresAt: expiresAt, Conditions: []Condition{}}
	for _, k := range keys {
		e.Conditions = append(e.Conditions, Condition{Field: k, Operator: "eq", Values: []string{event.Fields[k]}})
	}
	if err := validateEntry(s.Scope, e); err != nil {
		return Exception{}, err
	}
	return e, nil
}

// Evaluate preserves every original match in input order. Time validity is
// [NotBefore,ExpiresAt); expired entries remain stored but cannot annotate.
// Any invalid input, cancellation or overflow yields no partial results.
func Evaluate(ctx context.Context, a Access, s Snapshot, events []Event, matches []Match, asOf time.Time) ([]AnnotatedMatch, error) {
	if err := guard(a, s, false); err != nil {
		return nil, err
	}
	if ctx == nil || asOf.IsZero() {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(matches) > detectionrules.MaxMatches {
		return nil, ErrLimit
	}
	p, _ := eventProgram(s.Scope)
	if _, err := p.Evaluate(ctx, events); err != nil {
		return nil, err
	}
	byID := map[string]Event{}
	for _, e := range events {
		byID[e.ID] = e
	}
	out := make([]AnnotatedMatch, 0, len(matches))
	count := 0
	for _, m := range matches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if m.Scope != s.Scope {
			return nil, ErrScope
		}
		event, ok := byID[m.EventID]
		if !ok || m.Contract != detectionrules.ContractVersion || m.Provenance != Provenance || !textOK(m.RuleID, 128) || m.RuleVersion == 0 || !digestOK(m.RuleDigest) {
			return nil, ErrInvalid
		}
		result := AnnotatedMatch{Match: m, Exceptions: []Annotation{}}
		for _, e := range s.Entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !e.Enabled || e.Archived || asOf.Before(e.NotBefore) || !asOf.Before(e.ExpiresAt) || (!e.Global && (e.RuleID != m.RuleID || e.RuleDigest != m.RuleDigest)) {
				continue
			}
			matched := true
			for _, c := range e.Conditions {
				v, present := event.Fields[c.Field]
				found := false
				if present {
					for _, want := range c.Values {
						if v == want {
							found = true
							break
						}
					}
				}
				if !found {
					matched = false
					break
				}
			}
			if matched {
				if count == MaxAnnotations {
					return nil, ErrLimit
				}
				count++
				result.Exceptions = append(result.Exceptions, Annotation{ExceptionID: e.ID, Reason: e.Reason, Simulated: true})
			}
		}
		out = append(out, result)
	}
	return out, nil
}
