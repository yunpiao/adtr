package simulationdetectionschedules

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"strings"
)

func datasetIdentifier(s string) bool { return len(s) <= 128 && payloadIdentifier.MatchString(s) }
func dataset(ctx context.Context, tx pgx.Tx, s Scope, id string) (simulationevents.Dataset, []securityevents.Event, error) {
	var d simulationevents.Dataset
	if !datasetIdentifier(id) {
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

// loadPinned serializes against template edits while verifying the exact approved
// declaration. The caller has acquired its actor/schedule locks first.
func loadPinned(ctx context.Context, tx pgx.Tx, s Scope, p simulationdetection.Payload) (simulationdetection.Template, []securityevents.Event, error) {
	var locked string
	err := tx.QueryRow(ctx, `SELECT template_id FROM adtr.simulation_detection_templates WHERE tenant_id=$1 AND domain_id=$2 AND template_id=$3 FOR SHARE`, s.Tenant, s.Domain, p.TemplateID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return simulationdetection.Template{}, nil, ErrConfiguration
	}
	if err != nil {
		return simulationdetection.Template{}, nil, err
	}
	t, err := simulationdetection.DetailTx(ctx, tx, simulationdetection.Scope{Tenant: s.Tenant, Domain: s.Domain, ActorID: s.ActorID, Read: true}, p.TemplateID)
	if errors.Is(err, simulationdetection.ErrNotFound) {
		return t, nil, ErrConfiguration
	}
	if err != nil {
		return t, nil, err
	}
	if t.Archived || t.Version != p.TemplateVersion || t.Hash != p.TemplateHash || p.MappingVersion != simulationdetection.MappingVersion {
		return t, nil, ErrConfiguration
	}
	d, events, err := dataset(ctx, tx, s, p.DatasetID)
	if errors.Is(err, ErrNotFound) {
		return t, nil, ErrConfiguration
	}
	if err != nil {
		return t, nil, err
	}
	if d.FixtureHash != p.FixtureHash {
		return t, nil, ErrConfiguration
	}
	return t, events, nil
}
