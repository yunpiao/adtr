package simulationevents

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
)

// SeedFixtureTx is only for explicit isolated integration-test setup. It has no
// HTTP/production route. It validates the whole fixture before writing anything;
// callers must roll back on any database failure. IDs are immutable, not upserts.
func SeedFixtureTx(ctx context.Context, tx pgx.Tx, s securityevents.Scope, f Fixture) (Dataset, error) {
	if err := authorize(tx, s, true); err != nil {
		return Dataset{}, err
	}
	if !identifier(f.ID) || len(f.Cases) > MaxPage {
		return Dataset{}, ErrInvalid
	}
	core, err := securityevents.Normalize(s, f.Provenance, f.Events)
	if err != nil {
		return Dataset{}, err
	}
	p, err := core.Query(s, securityevents.Filter{Limit: securityevents.MaxEvents})
	if err != nil {
		return Dataset{}, err
	}
	events := map[string]bool{}
	for _, e := range p.Events {
		events[e.ID] = true
	}
	cases := map[string]bool{}
	for _, c := range f.Cases {
		if _, err = securityevents.NewCase(s, f.Provenance, c.ID); err != nil {
			return Dataset{}, err
		}
		if cases[c.ID] || len(c.EvidenceIDs) == 0 || len(c.EvidenceIDs) > securityevents.MaxEvents {
			return Dataset{}, ErrInvalid
		}
		cases[c.ID] = true
		seen := map[string]bool{}
		for _, id := range c.EvidenceIDs {
			if !events[id] || seen[id] {
				return Dataset{}, ErrInvalid
			}
			seen[id] = true
		}
	}
	h, err := hash(f)
	if err != nil {
		return Dataset{}, err
	}
	d := Dataset{ID: f.ID, Fixture: f.Provenance.Fixture, FixtureHash: h, Contract: f.Provenance.Contract, Simulation: true, EventCount: len(f.Events)}
	// Serialize setup writers for a domain; enforce a finite dataset budget.
	key, err := hash([]string{s.Tenant, s.Domain})
	if err != nil {
		return Dataset{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-fixture:"+key); err != nil {
		return Dataset{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_event_datasets WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain).Scan(&count); err != nil {
		return Dataset{}, err
	}
	if count >= securityevents.MaxEvents {
		return Dataset{}, ErrLimit
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.simulation_event_datasets(tenant_id,domain_id,dataset_id,fixture,fixture_hash,contract,simulation,event_count) VALUES($1,$2,$3,$4,$5,$6,true,$7)`, s.Tenant, s.Domain, d.ID, d.Fixture, d.FixtureHash, d.Contract, d.EventCount); err != nil {
		return Dataset{}, err
	}
	for _, e := range f.Events {
		b, e2 := json.Marshal(e)
		if e2 != nil {
			return Dataset{}, e2
		}
		if _, err = tx.Exec(ctx, `INSERT INTO adtr.simulation_events(tenant_id,domain_id,dataset_id,fixture_hash,event_id,payload) VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, d.ID, h, e.ID, b); err != nil {
			return Dataset{}, err
		}
	}
	for _, c := range f.Cases {
		if _, err = tx.Exec(ctx, `INSERT INTO adtr.simulation_event_cases(tenant_id,domain_id,dataset_id,fixture_hash,case_id,state,mark,revision) VALUES($1,$2,$3,$4,$5,'open','',1)`, s.Tenant, s.Domain, d.ID, h, c.ID); err != nil {
			return Dataset{}, err
		}
		for _, id := range c.EvidenceIDs {
			if _, err = tx.Exec(ctx, `INSERT INTO adtr.simulation_event_case_evidence(tenant_id,domain_id,dataset_id,fixture_hash,case_id,event_id) VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, d.ID, h, c.ID, id); err != nil {
				return Dataset{}, err
			}
		}
	}
	return d, nil
}
