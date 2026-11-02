package simulationdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"time"

	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func strict(b []byte, v any) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v) == nil && d.Decode(&struct{}{}) == io.EOF
}
func validateDetection(ctx context.Context, s Scope, d simulationevents.Dataset, events []securityevents.Event, v detectionSource, at time.Time) (Detection, error) {
	fail := func() (Detection, error) { return Detection{}, ErrInvalid }
	if v.PayloadBytes > 4096 || v.ResultBytes > 120000 || v.TemplateBytes > 120000 {
		return Detection{}, ErrLimit
	}
	if !identifier.MatchString(v.ID) || v.CompletedAt == nil || v.CompletedAt.IsZero() || v.CompletedAt.After(at) || v.PayloadVersion != 1 || v.ResultVersion < 1 || !v.Intended {
		return fail()
	}
	var p simulationdetection.Payload
	var r simulationdetection.Result
	var t simulationdetection.Template
	if !strict(v.Payload, &p) || !strict(v.Result, &r) || !strict(v.Template, &t) {
		return fail()
	}
	if !identifier.MatchString(p.TemplateID) || p.TemplateVersion < 1 || p.TemplateVersion > simulationdetection.MaxVersions || p.DatasetID != d.ID || p.FixtureHash != d.FixtureHash || p.MappingVersion != simulationdetection.MappingVersion || r.Payload != p || !r.Simulation || r.Contract != detectionrules.ContractVersion || r.EventCount != d.EventCount || !t.Simulation || t.ID != p.TemplateID || t.Version != p.TemplateVersion || t.Hash != p.TemplateHash || t.Hash != v.TemplateHash {
		return fail()
	}
	hash := t.Hash
	t.Hash = ""
	if digest(t) != hash {
		return fail()
	}
	t.Hash = hash
	for _, rule := range t.Rules {
		if rule.Version != uint64(t.Version) {
			return fail()
		}
	}
	ds := detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}
	program, e := detectionrules.Compile(ds, t.Rules)
	if e != nil {
		return fail()
	}
	mapped := make([]detectionrules.Event, 0, len(events))
	for _, ev := range events {
		outcome := "success"
		if ev.Kind == "login_failure" {
			outcome = "failure"
		}
		mapped = append(mapped, detectionrules.Event{ID: ev.ID, Scope: ds, Provenance: detectionrules.Provenance, Fields: map[string]string{"event.category": ev.Kind, "event.outcome": outcome, "actor.name": ev.Actor, "target.name": ev.Device, "source.ip": ev.SourceIP}})
	}
	matches, e := program.Evaluate(ctx, mapped)
	if e != nil {
		if ctx.Err() != nil {
			return Detection{}, ctx.Err()
		}
		return fail()
	}
	// Re-evaluation binds every returned match (and absence) to immutable template
	// and complete fixture, not just a plausible untrusted persisted match count.
	if !reflect.DeepEqual(matches, r.Matches) {
		return fail()
	}
	b, _ := json.Marshal(r)
	if len(b) > 60000 {
		return Detection{}, ErrLimit
	}
	count := len(matches)
	eventCount := r.EventCount
	completed := v.CompletedAt.UTC()
	return Detection{Status: "available", TaskUUID: v.ID, CompletedAt: &completed, TemplateID: t.ID, TemplateVersion: t.Version, TemplateHash: t.Hash, EventCount: &eventCount, MatchCount: &count}, nil
}
