package simulationdetectionexceptions

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionexceptions"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"strings"
)

func dataset(ctx context.Context, tx pgx.Tx, s Scope, id string) (simulationevents.Dataset, []securityevents.Event, error) {
	var d simulationevents.Dataset
	if !identifier.MatchString(id) {
		return d, nil, ErrInvalid
	}
	e := tx.QueryRow(ctx, `SELECT dataset_id,fixture,fixture_hash,contract,simulation,event_count FROM adtr.simulation_event_datasets WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, id).Scan(&d.ID, &d.Fixture, &d.FixtureHash, &d.Contract, &d.Simulation, &d.EventCount)
	if errors.Is(e, pgx.ErrNoRows) {
		return d, nil, ErrNotFound
	}
	if e != nil {
		return d, nil, e
	}
	hash, he := hex.DecodeString(d.FixtureHash)
	if d.ID != id || he != nil || len(hash) != 32 || strings.ToLower(d.FixtureHash) != d.FixtureHash || d.EventCount < 0 {
		return d, nil, ErrInvalid
	}
	if d.EventCount > detectionrules.MaxEvents {
		return d, nil, ErrLimit
	}
	rows, e := tx.Query(ctx, `SELECT event_id,payload FROM adtr.simulation_events WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 ORDER BY event_id LIMIT 1001`, s.Tenant, s.Domain, id, d.FixtureHash)
	if e != nil {
		return d, nil, e
	}
	defer rows.Close()
	raw := []securityevents.RawEvent{}
	for rows.Next() {
		var id string
		var b []byte
		var r securityevents.RawEvent
		if e = rows.Scan(&id, &b); e != nil {
			return d, nil, e
		}
		if len(b) > 4096 {
			return d, nil, ErrLimit
		}
		if json.Unmarshal(b, &r) != nil || r.ID != id {
			return d, nil, ErrInvalid
		}
		raw = append(raw, r)
		if len(raw) > detectionrules.MaxEvents {
			return d, nil, ErrLimit
		}
	}
	if e = rows.Err(); e != nil {
		return d, nil, e
	}
	if len(raw) != d.EventCount {
		return d, nil, ErrInvalid
	}
	scope := securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true}
	core, e := securityevents.Normalize(scope, securityevents.Provenance{Contract: d.Contract, Fixture: d.Fixture, Synthetic: d.Simulation}, raw)
	if e != nil {
		return d, nil, e
	}
	p, e := core.Query(scope, securityevents.Filter{Limit: detectionrules.MaxEvents})
	return d, p.Events, e
}

func mapped(s Scope, events []securityevents.Event) []detectionrules.Event {
	out := make([]detectionrules.Event, 0, len(events))
	for _, e := range events {
		outcome := "success"
		if e.Kind == "login_failure" {
			outcome = "failure"
		}
		out = append(out, detectionrules.Event{ID: e.ID, Scope: detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}, Provenance: detectionrules.Provenance, Fields: map[string]string{"event.category": e.Kind, "event.outcome": outcome, "actor.name": e.Actor, "target.name": e.Device, "source.ip": e.SourceIP}})
	}
	return out
}
func strict(b []byte, limit int, v any) error {
	if len(b) > limit {
		return ErrLimit
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		return ErrInvalid
	}
	return nil
}

// source replays the existing frozen mapping against immutable database inputs.
// No caller-supplied result or event is accepted. ReadRunTx checks owner and epoch.
func source(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, id string) (simulationdetection.Result, []detectionrules.Event, error) {
	var r simulationdetection.Result
	if e := authorize(tx, s, false); e != nil {
		return r, nil, e
	}
	if !s.DetectionRead || !s.EventsRead {
		return r, nil, ErrDenied
	}
	if !identifier.MatchString(id) || engine == nil {
		return r, nil, ErrInvalid
	}
	d, e := simulationdetection.ReadRunTx(ctx, tx, simulationdetection.Scope{Tenant: s.Tenant, Domain: s.Domain, ActorID: s.ActorID, Read: true}, engine, id)
	if e != nil {
		return r, nil, sourceError(e)
	}
	if d.Task.State != tasks.Succeeded || d.Task.PayloadVersion != 1 {
		return r, nil, ErrConflict
	}
	var p simulationdetection.Payload
	if e = strict(d.Task.Payload, 2048, &p); e != nil {
		return r, nil, e
	}
	if e = strict(d.Task.Result, MaxRecordBytes, &r); e != nil {
		return r, nil, e
	}
	if !r.Simulation || r.Contract != detectionrules.ContractVersion || r.Payload != p || p.MappingVersion != simulationdetection.MappingVersion || p.TemplateVersion < 1 || !identifier.MatchString(p.TemplateID) || !identifier.MatchString(p.DatasetID) {
		return r, nil, ErrInvalid
	}
	var b []byte
	var h string
	var t simulationdetection.Template
	e = tx.QueryRow(ctx, `SELECT hash,body FROM adtr.simulation_detection_versions WHERE tenant_id=$1 AND domain_id=$2 AND template_id=$3 AND version=$4`, s.Tenant, s.Domain, p.TemplateID, p.TemplateVersion).Scan(&h, &b)
	if e != nil {
		return r, nil, e
	}
	if e = strict(b, MaxRecordBytes, &t); e != nil {
		return r, nil, e
	}
	got := t.Hash
	t.Hash = ""
	want := digest(t)
	t.Hash = got
	if got != h || h != want || h != p.TemplateHash || t.ID != p.TemplateID || t.Version != p.TemplateVersion || !t.Simulation {
		return r, nil, ErrInvalid
	}
	for _, rule := range t.Rules {
		if rule.Version != uint64(t.Version) {
			return r, nil, ErrInvalid
		}
	}
	ds, events, e := dataset(ctx, tx, s, p.DatasetID)
	if e != nil {
		return r, nil, e
	}
	if ds.FixtureHash != p.FixtureHash || len(events) != r.EventCount {
		return r, nil, ErrInvalid
	}
	program, e := detectionrules.Compile(coreScope(s), t.Rules)
	if e != nil {
		return r, nil, mapCore(e)
	}
	ev := mapped(s, events)
	matches, e := program.Evaluate(ctx, ev)
	if e != nil {
		return r, nil, mapCore(e)
	}
	if digest(matches) != digest(r.Matches) {
		return r, nil, ErrInvalid
	}
	return r, ev, nil
}
func conditions(cs []detectionrules.Condition) error {
	for _, c := range cs {
		switch c.Field {
		case "event.category", "event.outcome", "actor.name", "target.name", "source.ip":
		default:
			return ErrInvalid
		}
		if c.Operator != "eq" && c.Operator != "in" {
			return ErrInvalid
		}
	}
	return nil
}
func declaration(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, d Declaration) (detectionexceptions.Exception, error) {
	e := detectionexceptions.Exception{ID: d.ID, Scope: coreScope(s), Global: d.Global, Reason: d.Reason, NotBefore: d.NotBefore, ExpiresAt: d.ExpiresAt, Conditions: d.Conditions}
	if !identifier.MatchString(d.ID) || conditions(d.Conditions) != nil {
		return e, ErrInvalid
	}
	if d.Global {
		if d.SourceTaskID != "" || d.RuleID != "" {
			return e, ErrInvalid
		}
	} else {
		r, _, err := source(ctx, tx, s, engine, d.SourceTaskID)
		if err != nil {
			return e, err
		}
		found := false
		for _, m := range r.Matches {
			if m.RuleID == d.RuleID {
				if found && e.RuleDigest != m.RuleDigest {
					return e, ErrInvalid
				}
				e.RuleID = m.RuleID
				e.RuleDigest = m.RuleDigest
				found = true
			}
		}
		if !found {
			return e, ErrNotFound
		}
	}
	return e, nil
}
func PreviewTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, in PreviewInput) (Declaration, error) {
	var out Declaration
	r, events, e := source(ctx, tx, s, engine, in.SourceTaskID)
	if e != nil {
		return out, e
	}
	snap, e := SnapshotTx(ctx, tx, s)
	if e != nil {
		return out, e
	}
	for _, m := range r.Matches {
		if m.EventID != in.EventID || m.RuleID != in.RuleID {
			continue
		}
		for _, v := range events {
			if v.ID != m.EventID {
				continue
			}
			draft, e := detectionexceptions.PreviewFromEvent(access(s), snap.Snapshot, v, in.ID, in.Reason, m.RuleID, m.RuleDigest, in.NotBefore, in.ExpiresAt)
			if e != nil {
				return out, mapCore(e)
			}
			return Declaration{ID: draft.ID, SourceTaskID: in.SourceTaskID, RuleID: draft.RuleID, Reason: draft.Reason, NotBefore: draft.NotBefore, ExpiresAt: draft.ExpiresAt, Conditions: draft.Conditions}, nil
		}
	}
	return out, ErrNotFound
}

// Map only explicit authorization/not-found verdicts; retain context/DB failures.
func sourceError(e error) error {
	var te *tasks.Error
	if errors.Is(e, simulationdetection.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(e, simulationdetection.ErrDenied) || errors.Is(e, tasks.ErrAuthorization) {
		return ErrDenied
	}
	if errors.As(e, &te) {
		switch te.Status {
		case 404:
			return ErrNotFound
		case 403:
			return ErrDenied
		case 400:
			return ErrInvalid
		}
	}
	return e
}
