package simulationlabels

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/sensitivelabels"
)

func makeComparison(s Scope, a, b Evaluation) (Comparison, error) {
	if a.Dataset.ID != b.Dataset.ID || a.Dataset.Hash != b.Dataset.Hash || a.Policy.ID != b.Policy.ID || a.Policy.Revision != b.Policy.Revision || a.Policy.Hash != b.Policy.Hash || a.Observation.Sequence >= b.Observation.Sequence {
		return Comparison{}, ErrIncompatible
	}
	changes, e := sensitivelabels.Compare(coreInput(s, a.Observation, a.Policy), coreInput(s, b.Observation, b.Policy))
	if e != nil {
		return Comparison{}, mapCore(e)
	}
	v := Comparison{ID: "comparison:" + digest([]string{a.ID, b.ID}), DatasetID: a.Dataset.ID, BeforeID: a.ID, AfterID: b.ID, Changes: changes, Alerts: []Alert{}}
	for _, c := range changes.Changes {
		if len(v.Alerts) == 0 || v.Alerts[len(v.Alerts)-1].ObjectID != c.ObjectID {
			v.Alerts = append(v.Alerts, Alert{ID: "alert:" + digest([]string{v.ID, c.ObjectID}), ObjectID: c.ObjectID, Changes: []sensitivelabels.Change{}, Simulation: true, Delivery: "in_app_only"})
		}
		i := len(v.Alerts) - 1
		v.Alerts[i].Changes = append(v.Alerts[i].Changes, c)
	}
	return v, nil
}
func CompareTx(ctx context.Context, tx pgx.Tx, s Scope, in CompareInput, audit AuditFunc) (Receipt, error) {
	if e := authorize(tx, s, true); e != nil {
		return Receipt{}, e
	}
	if !valid(in.BeforeID, 128) || !valid(in.AfterID, 128) {
		return Receipt{}, ErrInvalid
	}
	r, replay, e := beginOperation(ctx, tx, s, in.DatasetID, in.Actor, in.OperationID, in.Reason, "compare", in, audit)
	if e != nil || replay {
		return r, e
	}
	a, e := EvaluationTx(ctx, tx, s, in.DatasetID, in.BeforeID)
	if e != nil {
		return Receipt{}, e
	}
	b, e := EvaluationTx(ctx, tx, s, in.DatasetID, in.AfterID)
	if e != nil {
		return Receipt{}, e
	}
	v, e := makeComparison(s, a, b)
	if e != nil {
		return Receipt{}, e
	}
	old, e := ComparisonTx(ctx, tx, s, in.DatasetID, v.ID)
	if e == nil {
		r.Comparison = &old
		return finishOperation(ctx, tx, s, r, in, audit)
	}
	if !errors.Is(e, ErrNotFound) {
		return Receipt{}, e
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_comparisons WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, in.DatasetID).Scan(&n); e != nil {
		return Receipt{}, e
	}
	if n >= MaxEvaluations {
		return Receipt{}, ErrLimit
	}
	v.Actor = in.Actor
	v.At = r.At
	v.Hash = digest(v)
	raw, e := marshalBounded(v, 8*1024*1024)
	if e != nil {
		return Receipt{}, e
	}
	r.Comparison = &v
	if _, _, e = prepareOperation(r, in); e != nil {
		return Receipt{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_label_comparisons VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, in.DatasetID, v.ID, in.BeforeID, in.AfterID, raw); e != nil {
		return Receipt{}, e
	}
	r.Comparison = &v
	return finishOperation(ctx, tx, s, r, in, audit)
}
func ComparisonTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, id string) (Comparison, error) {
	if e := authorize(tx, s, false); e != nil {
		return Comparison{}, e
	}
	if !valid(dataset, 128) || !valid(id, 128) {
		return Comparison{}, ErrInvalid
	}
	var v Comparison
	var before, after string
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT before_id,after_id,payload FROM adtr.simulation_label_comparisons WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND comparison_id=$4`, s.Tenant, s.Domain, dataset, id).Scan(&before, &after, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if len(raw) > 8*1024*1024 || json.Unmarshal(raw, &v) != nil {
		return Comparison{}, ErrInvalid
	}
	h := v.Hash
	v.Hash = ""
	if digest(v) != h || v.ID != id || v.DatasetID != dataset || v.BeforeID != before || v.AfterID != after || !valid(v.Actor, 128) || v.At.IsZero() {
		return Comparison{}, ErrInvalid
	}
	v.Hash = h
	a, e := EvaluationTx(ctx, tx, s, dataset, before)
	if e != nil {
		return Comparison{}, e
	}
	b, e := EvaluationTx(ctx, tx, s, dataset, after)
	if e != nil {
		return Comparison{}, e
	}
	expected, e := makeComparison(s, a, b)
	if e != nil {
		return Comparison{}, e
	}
	expected.Actor = v.Actor
	expected.At = v.At
	expected.Hash = digest(expected)
	if !equal(v, expected) {
		return Comparison{}, ErrInvalid
	}
	return v, nil
}
func ComparisonsTx(ctx context.Context, tx pgx.Tx, s Scope, dataset string, offset, limit int) (Page[Comparison], error) {
	if e := authorize(tx, s, false); e != nil {
		return Page[Comparison]{}, e
	}
	if _, _, e := loadDataset(ctx, tx, s, dataset); e != nil {
		return Page[Comparison]{}, e
	}
	all, e := ids(ctx, tx, `SELECT comparison_id FROM adtr.simulation_label_comparisons WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 ORDER BY comparison_id LIMIT 4097`, s.Tenant, s.Domain, dataset)
	if e != nil {
		return Page[Comparison]{}, e
	}
	p, e := page(all, offset, limit)
	if e != nil {
		return Page[Comparison]{}, e
	}
	out := Page[Comparison]{Items: []Comparison{}, Count: p.Count, Offset: offset, Limit: limit}
	for _, id := range p.Items {
		v, e := ComparisonTx(ctx, tx, s, dataset, id)
		if e != nil {
			return Page[Comparison]{}, e
		}
		out.Items = append(out.Items, v)
	}
	return boundedPage(out)
}
