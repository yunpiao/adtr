package simulationdashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationrisk"
)

func valid(s string, n int) bool {
	if s == "" || len(s) > n || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func windowSpec(p string) (time.Duration, time.Duration, error) {
	switch p {
	case "", "24h":
		return 24 * time.Hour, time.Hour, nil
	case "1h":
		return time.Hour, time.Minute, nil
	case "7d":
		return 7 * 24 * time.Hour, 24 * time.Hour, nil
	}
	return 0, 0, ErrInvalid
}
func ranked(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for n, c := range m {
		out = append(out, Count{n, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
func unavailable() []Unavailable {
	return []Unavailable{
		{"assetInventory", "no_inventory_producer"}, {"connectionHealth", "no_connection_health_producer"}, {"collectorCount", "no_collector_producer"},
		{"requestRate", "no_request_counter"}, {"handlingDuration", "no_case_creation_clock"}, {"baselineDetection", "no_baseline_producer"},
		{"vulnerabilityDetection", "no_vulnerability_producer"}, {"weakPasswordDetection", "no_weak_password_producer"},
		{"compromisedTargets", "no_compromise_classification"}, {"blockedUsersAndIPs", "no_response_producer"}, {"attackHeatmap", "no_attack_classification"},
	}
}
func aggregate(ctx context.Context, s Scope, in Input, src source) (Summary, error) {
	out := Summary{}
	duration, bucket, e := windowSpec(in.Window)
	if e != nil {
		return out, e
	}
	d := src.Dataset
	h, he := hex.DecodeString(d.FixtureHash)
	if src.AsOf.IsZero() || src.AsOf.Year() < 2000 || src.AsOf.Year() > 2100 || d.ID != in.DatasetID || he != nil || len(h) != 32 || strings.ToLower(d.FixtureHash) != d.FixtureHash || d.EventCount < 0 {
		return out, ErrInvalid
	}
	if src.Oversized || len(src.Events) > securityevents.MaxEvents || d.EventCount > securityevents.MaxEvents || len(src.Cases) > 100 || len(src.Resets) > simulationrisk.MaxResets {
		return out, ErrLimit
	}
	if len(src.Events) != d.EventCount {
		return out, ErrInvalid
	}
	raw := make([]securityevents.RawEvent, 0, len(src.Events))
	for _, v := range src.Events {
		if v.Bytes > 4096 || len(v.Payload) > 4096 {
			return out, ErrLimit
		}
		var r securityevents.RawEvent
		if json.Unmarshal(v.Payload, &r) != nil || r.ID != v.ID {
			return out, ErrInvalid
		}
		raw = append(raw, r)
	}
	scope := securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true}
	provenance := securityevents.Provenance{Contract: d.Contract, Fixture: d.Fixture, Synthetic: d.Simulation}
	core, e := securityevents.Normalize(scope, provenance, raw)
	if e != nil {
		return out, ErrInvalid
	}
	page, e := core.Query(scope, securityevents.Filter{Limit: securityevents.MaxEvents})
	if e != nil {
		return out, ErrInvalid
	}
	at := src.AsOf.UTC()
	start := at.Add(-duration)
	preset := in.Window
	if preset == "" {
		preset = "24h"
	}
	out = Summary{Contract: Contract, Simulation: true, AsOf: at, Window: Window{preset, start, at, int(bucket / time.Second)}, Dataset: d,
		Coverage: Coverage{Complete: true, EventCount: len(page.Events), CaseCount: len(src.Cases)}, Cases: Cases{Scope: "dataset_current", Total: len(src.Cases)},
		EventActivity:       Activity{Scope: "selected_window", TopLimit: 5, ByKind: []KindCount{}, Buckets: []ActivityBucket{}},
		CaseCloseThroughput: Throughput{Status: "available", Unit: "case_closes_per_minute", Buckets: []CloseBucket{}},
		Risk:                Risk{Status: "not_selected", Trend: []riskscore.Point{}}, LatestManualDetection: Detection{Status: "no_result"}, Unavailable: unavailable()}
	// Buckets align to UTC boundaries; first/last buckets are clipped by window.
	first := start.Truncate(bucket)
	for t := first; t.Before(at); t = t.Add(bucket) {
		out.EventActivity.Buckets = append(out.EventActivity.Buckets, ActivityBucket{t, map[string]int{"login_failure": 0, "login_success": 0, "service_access": 0}})
	}
	closeFirst := start.Truncate(time.Minute)
	for t := closeFirst; t.Before(at); t = t.Add(time.Minute) {
		out.CaseCloseThroughput.Buckets = append(out.CaseCloseThroughput.Buckets, CloseBucket{Start: t})
	}
	kinds := map[string]int{"login_failure": 0, "login_success": 0, "service_access": 0}
	actors := map[string]int{}
	devices := map[string]int{}
	ips := map[string]int{}
	for _, ev := range page.Events {
		t := ev.OccurredAt
		if out.Coverage.SourceFirstAt == nil {
			v := t
			out.Coverage.SourceFirstAt = &v
		}
		v := t
		out.Coverage.SourceLastAt = &v
		if !t.Before(at) {
			out.Coverage.ExcludedFutureEvents++
			continue
		}
		if t.Before(start) {
			continue
		}
		out.EventActivity.Total++
		kinds[ev.Kind]++
		actors[ev.Actor]++
		devices[ev.Device]++
		ips[ev.SourceIP]++
		i := int(t.Sub(first) / bucket)
		out.EventActivity.Buckets[i].CountsByKind[ev.Kind]++
	}
	for _, k := range []string{"login_failure", "login_success", "service_access"} {
		out.EventActivity.ByKind = append(out.EventActivity.ByKind, KindCount{k, kinds[k]})
	}
	out.EventActivity.TopActors = ranked(actors)
	out.EventActivity.TopDevices = ranked(devices)
	out.EventActivity.TopSourceIPs = ranked(ips)
	out.EventActivity.DistinctActors = len(actors)
	out.EventActivity.DistinctDevices = len(devices)
	seen := map[string]bool{}
	for _, c := range src.Cases {
		if seen[c.ID] || c.Revision > 1025 || c.EvidenceCount < 1 {
			return Summary{}, ErrInvalid
		}
		seen[c.ID] = true
		if c.EvidenceCount > securityevents.MaxEvents || c.AuditBytes > 4096 {
			return Summary{}, ErrLimit
		}
		updated := time.Time{}
		if c.UpdatedAt != nil {
			updated = c.UpdatedAt.UTC()
		}
		if _, e := securityevents.RestoreCase(scope, provenance, securityevents.CaseSnapshot{ID: c.ID, Tenant: s.Tenant, Domain: s.Domain, State: c.State, Mark: c.Mark, Revision: c.Revision, UpdatedAt: updated, Provenance: provenance}); e != nil {
			return Summary{}, ErrInvalid
		}
		if updated.After(at) {
			return Summary{}, ErrConflict
		}
		if c.Revision > 1 {
			var a securityevents.Audit
			if json.Unmarshal(c.Audit, &a) != nil || a.CaseID != c.ID || a.Revision != c.Revision || a.State != c.State || a.Mark != c.Mark || !a.At.Equal(updated) || a.Provenance != provenance || !valid(a.Actor, 128) || !valid(a.Reason, 512) {
				return Summary{}, ErrInvalid
			}
			// Replay final transition from its previous state; catches forged close actions.
			prev, er := securityevents.RestoreCase(scope, provenance, securityevents.CaseSnapshot{ID: c.ID, Tenant: s.Tenant, Domain: s.Domain, State: a.PreviousState, Mark: a.PreviousMark, Revision: c.Revision - 1, UpdatedAt: func() time.Time {
				if c.Revision == 2 {
					return time.Time{}
				}
				return updated
			}(), Provenance: provenance})
			if er != nil {
				return Summary{}, ErrInvalid
			}
			_, want, er := prev.Apply(securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true, Manage: true}, securityevents.Change{Action: a.Action, Actor: a.Actor, Reason: a.Reason, At: a.At, ExpectedRevision: c.Revision - 1})
			if er != nil || !reflect.DeepEqual(want, a) {
				return Summary{}, ErrInvalid
			}
		} else if c.AuditBytes != 0 {
			return Summary{}, ErrInvalid
		}
		switch c.Mark {
		case "":
			out.Cases.Marks.Unmarked++
		case "benign":
			out.Cases.Marks.Benign++
		case "suspicious":
			out.Cases.Marks.Suspicious++
		}
		if c.State == "open" {
			out.Cases.Open++
			continue
		}
		out.Cases.Closed++
		if !updated.Before(start) && updated.Before(at) {
			out.CaseCloseThroughput.WindowTotal++
			out.CaseCloseThroughput.Buckets[int(updated.Sub(closeFirst)/time.Minute)].Count++
		}
		if !updated.Before(at.Add(-time.Minute)) && updated.Before(at) {
			out.CaseCloseThroughput.Last60Seconds++
		}
	}
	if in.Subject != "" {
		r, er := simulationrisk.EvaluateSnapshot(simulationrisk.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true}, d, in.Subject, page.Events, src.Resets, at)
		if errors.Is(er, simulationrisk.ErrNotFound) {
			out.Risk.Status = "subject_not_found"
			out.Risk.Subject = in.Subject
		} else if er != nil {
			return Summary{}, er
		} else {
			score := r.Score
			out.Risk = Risk{Status: "available", Subject: in.Subject, MappingVersion: r.MappingVersion, Revision: r.Revision, Score: &score, Trend: r.Trend, ResetBoundary: r.ResetBoundary}
		}
	} else if len(src.Resets) != 0 {
		return Summary{}, ErrInvalid
	}
	if src.Detection != nil {
		out.LatestManualDetection, e = validateDetection(ctx, s, d, page.Events, *src.Detection, at)
		if e != nil {
			return Summary{}, e
		}
	}
	if e = ctx.Err(); e != nil {
		return Summary{}, e
	}
	out.SnapshotID = digest(out)
	b, e := json.Marshal(out)
	if e != nil {
		return Summary{}, e
	}
	if len(b) > 2<<20 {
		return Summary{}, ErrLimit
	}
	return out, nil
}
