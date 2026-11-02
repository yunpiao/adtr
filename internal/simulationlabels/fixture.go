package simulationlabels

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
)

// SeedFixtureTx is solely an explicit trusted isolated fixture setup operation.
// It is not a runtime import API. Callers must rollback on any error.
func SeedFixtureTx(ctx context.Context, tx pgx.Tx, s Scope, f Fixture) (Dataset, error) {
	if e := authorize(tx, s, true); e != nil {
		return Dataset{}, e
	}
	if !valid(f.ID, 128) || f.Version != Contract || !f.Simulated || !valid(f.Producer, 128) || len(f.Observations) < 1 || len(f.Observations) > MaxObservations {
		return Dataset{}, ErrInvalid
	}
	obs := make([]Observation, len(f.Observations))
	observationJSON := make([][]byte, len(f.Observations))
	seen := map[string]bool{}
	for i, o := range f.Observations {
		var e error
		obs[i], e = canonicalObservation(s, o)
		if e != nil {
			return Dataset{}, e
		}
		if o.Sequence != uint64(i+1) || seen[o.ID] || o.Hash != "" {
			return Dataset{}, ErrInvalid
		}
		observationJSON[i], e = marshalBounded(obs[i], 1024*1024)
		if e != nil {
			return Dataset{}, e
		}
		seen[o.ID] = true
	}
	f.Observations = obs
	d := Dataset{ID: f.ID, Contract: Contract, Producer: f.Producer, Simulation: true, Hash: digest(f), ObservationCount: len(obs)}
	b, e := marshalBounded(d, 4096)
	if e != nil {
		return Dataset{}, e
	}
	if e := lock(ctx, tx, s); e != nil {
		return Dataset{}, e
	}
	var n int
	if e := tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_datasets WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain).Scan(&n); e != nil {
		return Dataset{}, e
	}
	if n >= MaxDatasets {
		return Dataset{}, ErrLimit
	}
	var exists bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_label_datasets WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3)`, s.Tenant, s.Domain, d.ID).Scan(&exists); e != nil {
		return Dataset{}, e
	}
	if exists {
		return Dataset{}, ErrConflict
	}
	if _, e := tx.Exec(ctx, `INSERT INTO adtr.simulation_label_datasets VALUES($1,$2,$3,$4,$5)`, s.Tenant, s.Domain, d.ID, d.Hash, b); e != nil {
		return Dataset{}, e
	}
	for i, o := range obs {
		b = observationJSON[i]
		if _, e := tx.Exec(ctx, `INSERT INTO adtr.simulation_label_observations VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, s.Tenant, s.Domain, d.ID, d.Hash, o.ID, int64(o.Sequence), o.Hash, b); e != nil {
			return Dataset{}, e
		}
	}
	return d, nil
}
func loadDataset(ctx context.Context, tx pgx.Tx, s Scope, id string) (Dataset, []Observation, error) {
	var d Dataset
	if !valid(id, 128) {
		return d, nil, ErrInvalid
	}
	var hash string
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT fixture_hash,payload FROM adtr.simulation_label_datasets WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, id).Scan(&hash, &raw)
	if e != nil {
		if e == pgx.ErrNoRows {
			return d, nil, ErrNotFound
		}
		return d, nil, e
	}
	if len(raw) > 4096 || json.Unmarshal(raw, &d) != nil || d.ID != id || !d.Simulation || d.Contract != Contract || !valid(d.Producer, 128) || d.Hash != hash || d.ObservationCount < 1 || d.ObservationCount > MaxObservations {
		return Dataset{}, nil, ErrInvalid
	}
	rows, e := tx.Query(ctx, `SELECT observation_id,sequence,observation_hash,payload FROM adtr.simulation_label_observations WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 ORDER BY sequence LIMIT 129`, s.Tenant, s.Domain, id, hash)
	if e != nil {
		return Dataset{}, nil, e
	}
	defer rows.Close()
	obs := []Observation{}
	for rows.Next() {
		var o Observation
		var oid, oh string
		var seq int64
		var b []byte
		if e = rows.Scan(&oid, &seq, &oh, &b); e != nil {
			return Dataset{}, nil, e
		}
		if len(b) > 1024*1024 || json.Unmarshal(b, &o) != nil {
			return Dataset{}, nil, ErrInvalid
		}
		expected, err := canonicalObservation(s, o)
		if err != nil || o.ID != oid || o.Sequence != uint64(seq) || o.Sequence != uint64(len(obs)+1) || o.Hash != oh || !equal(o, expected) {
			return Dataset{}, nil, ErrInvalid
		}
		obs = append(obs, o)
	}
	if e = rows.Err(); e != nil {
		return Dataset{}, nil, e
	}
	if len(obs) != d.ObservationCount || digest(Fixture{ID: d.ID, Version: d.Contract, Producer: d.Producer, Simulated: true, Observations: obs}) != d.Hash {
		return Dataset{}, nil, ErrInvalid
	}
	return d, obs, nil
}
func DatasetsTx(ctx context.Context, tx pgx.Tx, s Scope, offset, limit int) (Page[Dataset], error) {
	if e := authorize(tx, s, false); e != nil {
		return Page[Dataset]{}, e
	}
	all, e := ids(ctx, tx, `SELECT dataset_id FROM adtr.simulation_label_datasets WHERE tenant_id=$1 AND domain_id=$2 ORDER BY dataset_id LIMIT 129`, s.Tenant, s.Domain)
	if e != nil {
		return Page[Dataset]{}, e
	}
	if len(all) > MaxDatasets {
		return Page[Dataset]{}, ErrLimit
	}
	p, e := page(all, offset, limit)
	if e != nil {
		return Page[Dataset]{}, e
	}
	out := Page[Dataset]{Items: []Dataset{}, Count: p.Count, Offset: offset, Limit: limit}
	for _, id := range p.Items {
		d, _, e := loadDataset(ctx, tx, s, id)
		if e != nil {
			return Page[Dataset]{}, e
		}
		out.Items = append(out.Items, d)
	}
	return out, nil
}
func ObservationsTx(ctx context.Context, tx pgx.Tx, s Scope, id string, offset, limit int) (Page[Observation], error) {
	if e := authorize(tx, s, false); e != nil {
		return Page[Observation]{}, e
	}
	_, obs, e := loadDataset(ctx, tx, s, id)
	if e != nil {
		return Page[Observation]{}, e
	}
	return page(obs, offset, limit)
}
func findObservation(obs []Observation, id string) (Observation, error) {
	for _, o := range obs {
		if o.ID == id {
			return o, nil
		}
	}
	return Observation{}, ErrNotFound
}
