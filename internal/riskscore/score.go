// Package riskscore replays synthetic risk evidence. Its weights are simulated,
// not a validated AD detection model. Callers own authorization and persistence.
package riskscore

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const Version = "simulated-risk-v1"
const MaxEntries = 10000

var ErrInvalid = errors.New("invalid simulated risk input")
var ErrConflict = errors.New("conflicting risk evidence ID")

// Scope must be selected and authorized by the caller, not trusted from a client.
type Scope struct {
	TenantID string `json:"tenant_id"`
	DomainID string `json:"domain_id"`
	UserID   string `json:"user_id"`
}
type Event struct {
	ID         string    `json:"id"`
	Scope      Scope     `json:"scope"`
	Kind       string    `json:"kind"`
	OccurredAt time.Time `json:"occurred_at"`
}
type Reset struct {
	ID     string    `json:"id"`
	Scope  Scope     `json:"scope"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}
type Request struct {
	Version string    `json:"version"`
	Scope   Scope     `json:"scope"`
	AsOf    time.Time `json:"as_of"`
	Events  []Event   `json:"events"`
	Resets  []Reset   `json:"resets"`
}
type Bucket struct {
	Kind      string `json:"kind"`
	Count     int    `json:"count"`
	Weight    int    `json:"weight"`
	RawPoints int    `json:"raw_points"`
}
type Record struct {
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Reason string    `json:"reason,omitempty"`
	Before int       `json:"before"`
	After  int       `json:"after"`
	Delta  int       `json:"delta"`
}
type Point struct {
	At    time.Time `json:"at"`
	Score int       `json:"score"`
}
type Result struct {
	Version       string     `json:"version"`
	Simulated     bool       `json:"simulated"`
	Scope         Scope      `json:"scope"`
	AsOf          time.Time  `json:"as_of"`
	Score         int        `json:"score"`
	EvidenceCount int        `json:"evidence_count"`
	Duplicates    int        `json:"duplicates"`
	IgnoredFuture int        `json:"ignored_future"`
	ResetBoundary *time.Time `json:"reset_boundary,omitempty"`
	Distribution  []Bucket   `json:"distribution"`
	Records       []Record   `json:"records"`
	Trend         []Point    `json:"trend"`
}

// Weights returns a copy; callers cannot mutate the versioned scoring policy.
func Weights() map[string]int {
	return map[string]int{"low": 5, "medium": 15, "high": 30, "critical": 50}
}
func validText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && s == strings.TrimSpace(s)
}
func validScope(s Scope) bool {
	return validText(s.TenantID, 128) && validText(s.DomainID, 128) && validText(s.UserID, 128)
}

type entry struct {
	id     string
	at     time.Time
	kind   string
	reason string
	reset  bool
}

// Evaluate validates the entire batch before replaying it. Duplicate IDs with
// identical normalized evidence are idempotent; changed evidence is an error.
// Events at a reset timestamp precede the reset, establishing an inclusive
// reset boundary. Equal-time records sort by ID and trend keeps the final score.
func Evaluate(req Request) (Result, error) {
	fail := func(why string) (Result, error) { return Result{}, fmt.Errorf("%w: %s", ErrInvalid, why) }
	if req.Version != "" && req.Version != Version {
		return fail("unsupported version")
	}
	if !validScope(req.Scope) || req.AsOf.IsZero() {
		return fail("scope and as_of required")
	}
	if len(req.Events) > MaxEntries || len(req.Resets) > MaxEntries-len(req.Events) {
		return fail("entry limit exceeded")
	}
	out := Result{Version: Version, Simulated: true, Scope: req.Scope, AsOf: req.AsOf.UTC(), Distribution: []Bucket{}, Records: []Record{}, Trend: []Point{}}
	weights := Weights()
	entries := make([]entry, 0, len(req.Events)+len(req.Resets))
	seen := map[string]entry{}
	add := func(e entry) error {
		if old, ok := seen[e.id]; ok {
			if old.kind != e.kind || old.reason != e.reason || old.reset != e.reset || !old.at.Equal(e.at) {
				return ErrConflict
			}
			out.Duplicates++
			return nil
		}
		seen[e.id] = e
		if e.at.After(req.AsOf) {
			out.IgnoredFuture++
			return nil
		}
		entries = append(entries, e)
		return nil
	}
	for _, e := range req.Events {
		if !validText(e.ID, 128) || e.Scope != req.Scope || e.OccurredAt.IsZero() || weights[e.Kind] == 0 {
			return fail("event ID, scope, timestamp or kind")
		}
		if err := add(entry{id: e.ID, at: e.OccurredAt.UTC(), kind: e.Kind}); err != nil {
			return Result{}, err
		}
	}
	for _, r := range req.Resets {
		if !validText(r.ID, 128) || r.Scope != req.Scope || r.At.IsZero() || !validText(r.Reason, 512) {
			return fail("reset ID, scope, timestamp or reason")
		}
		if err := add(entry{id: r.ID, at: r.At.UTC(), kind: "reset", reason: r.Reason, reset: true}); err != nil {
			return Result{}, err
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		if a.reset != b.reset {
			return !a.reset
		}
		return a.id < b.id
	})
	counts := map[string]int{}
	for _, e := range entries {
		before := out.Score
		if e.reset {
			out.Score = 0
			counts = map[string]int{}
			out.EvidenceCount = 0
			at := e.at
			out.ResetBoundary = &at
		} else {
			out.Score += weights[e.kind]
			if out.Score > 100 {
				out.Score = 100
			}
			counts[e.kind]++
			out.EvidenceCount++
		}
		out.Records = append(out.Records, Record{ID: e.id, At: e.at, Kind: e.kind, Reason: e.reason, Before: before, After: out.Score, Delta: out.Score - before})
		point := Point{At: e.at, Score: out.Score}
		n := len(out.Trend)
		if n > 0 && out.Trend[n-1].At.Equal(e.at) {
			out.Trend[n-1] = point
		} else {
			out.Trend = append(out.Trend, point)
		}
	}
	for _, kind := range []string{"low", "medium", "high", "critical"} {
		out.Distribution = append(out.Distribution, Bucket{Kind: kind, Count: counts[kind], Weight: weights[kind], RawPoints: counts[kind] * weights[kind]})
	}
	return out, nil
}
