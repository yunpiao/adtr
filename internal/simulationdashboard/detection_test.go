package simulationdashboard

import (
	"context"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"testing"
	"time"
)

func detectionFixture(t *testing.T) (Scope, source, []securityevents.Event, detectionSource) {
	t.Helper()
	s, _, src := fixture(t)
	src.Events = src.Events[:1]
	src.Dataset.EventCount = 1
	var raw securityevents.RawEvent
	_ = json.Unmarshal(src.Events[0].Payload, &raw)
	core, e := securityevents.Normalize(securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true}, raw.Provenance, []securityevents.RawEvent{raw})
	if e != nil {
		t.Fatal(e)
	}
	page, e := core.Query(securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true}, securityevents.Filter{Limit: 4096})
	if e != nil {
		t.Fatal(e)
	}
	scope := detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}
	rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: scope, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
	template := simulationdetection.Template{ID: "template", Name: "Synthetic", Version: 1, Rules: []detectionrules.Rule{rule}, Simulation: true}
	template.Hash = digest(template)
	p := simulationdetection.Payload{TemplateID: template.ID, TemplateVersion: 1, TemplateHash: template.Hash, DatasetID: src.Dataset.ID, FixtureHash: src.Dataset.FixtureHash, MappingVersion: simulationdetection.MappingVersion}
	program, e := detectionrules.Compile(scope, template.Rules)
	if e != nil {
		t.Fatal(e)
	}
	ev := page.Events[0]
	matches, e := program.Evaluate(context.Background(), []detectionrules.Event{{ID: ev.ID, Scope: scope, Provenance: detectionrules.Provenance, Fields: map[string]string{"event.category": ev.Kind, "event.outcome": "failure", "actor.name": ev.Actor, "target.name": ev.Device, "source.ip": ev.SourceIP}}})
	if e != nil {
		t.Fatal(e)
	}
	r := simulationdetection.Result{Simulation: true, Contract: detectionrules.ContractVersion, Payload: p, EventCount: 1, Matches: matches}
	pb, _ := json.Marshal(p)
	rb, _ := json.Marshal(r)
	tb, _ := json.Marshal(template)
	at := src.AsOf.Add(-time.Second)
	return s, src, page.Events, detectionSource{ID: "task", CompletedAt: &at, PayloadVersion: 1, ResultVersion: 3, Payload: pb, Result: rb, Template: tb, TemplateHash: template.Hash, PayloadBytes: len(pb), ResultBytes: len(rb), TemplateBytes: len(tb), Intended: true}
}
func TestDetectionPinnedResultReevaluation(t *testing.T) {
	s, src, events, v := detectionFixture(t)
	r, e := validateDetection(context.Background(), s, src.Dataset, events, v, src.AsOf)
	if e != nil || r.MatchCount == nil || *r.MatchCount != 1 || r.EventCount == nil || *r.EventCount != 1 {
		t.Fatal(r, e)
	}
	tests := map[string]func(*detectionSource){"unintended": func(v *detectionSource) { v.Intended = false }, "future": func(v *detectionSource) { a := src.AsOf.Add(time.Second); v.CompletedAt = &a }, "missing template": func(v *detectionSource) { v.Template = nil }, "hash": func(v *detectionSource) { v.TemplateHash = "wrong" }, "empty matches": func(v *detectionSource) {
		var r simulationdetection.Result
		_ = json.Unmarshal(v.Result, &r)
		r.Matches = []detectionrules.Match{}
		v.Result, _ = json.Marshal(r)
	}, "wrong match": func(v *detectionSource) {
		var r simulationdetection.Result
		_ = json.Unmarshal(v.Result, &r)
		r.Matches[0].EventID = "other"
		v.Result, _ = json.Marshal(r)
	}, "overflow": func(v *detectionSource) { v.ResultBytes = 120001 }, "unknown payload": func(v *detectionSource) {
		v.Payload = append(v.Payload[:len(v.Payload)-1], []byte(`,"unknown":true}`)...)
	}}
	for n, f := range tests {
		t.Run(n, func(t *testing.T) {
			s, src, events, v := detectionFixture(t)
			f(&v)
			if _, e := validateDetection(context.Background(), s, src.Dataset, events, v, src.AsOf); e == nil {
				t.Fatal("corruption accepted")
			}
		})
	}
}
