package simulationlabels

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/groupgraph"
	"github.com/yunpiao/adtr/internal/sensitivelabels"
	"strings"
)

func EvaluateTx(ctx context.Context, tx pgx.Tx, s Scope, in EvaluateInput, audit AuditFunc) (Receipt, error) {
	if e := authorize(tx, s, true); e != nil {
		return Receipt{}, e
	}
	if !valid(in.PolicyID, 128) || !valid(in.ObservationID, 128) || in.ExpectedRevision < 1 || in.ExpectedRevision > MaxRevisions {
		return Receipt{}, ErrInvalid
	}
	r, replay, e := beginOperation(ctx, tx, s, in.DatasetID, in.Actor, in.OperationID, in.Reason, "evaluate", in, audit)
	if e != nil || replay {
		return r, e
	}
	d, obs, e := loadDataset(ctx, tx, s, in.DatasetID)
	if e != nil {
		return Receipt{}, e
	}
	p, e := PolicyTx(ctx, tx, s, in.DatasetID, in.PolicyID)
	if e != nil {
		return Receipt{}, e
	}
	if p.Revision != in.ExpectedRevision || p.Status != "active" {
		return Receipt{}, ErrConflict
	}
	o, e := findObservation(obs, in.ObservationID)
	if e != nil {
		return Receipt{}, e
	}
	res, e := sensitivelabels.Evaluate(coreInput(s, o, p))
	if e != nil {
		return Receipt{}, mapCore(e)
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_evaluations WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, in.DatasetID).Scan(&n); e != nil {
		return Receipt{}, e
	}
	if n >= MaxEvaluations {
		return Receipt{}, ErrLimit
	}
	v := Evaluation{ID: "evaluation:" + digest([]string{in.Actor, in.OperationID}), Dataset: d, Observation: o, Policy: p, Result: res, Actor: in.Actor, At: r.At}
	v.Hash = digest(v)
	b, e := marshalBounded(v, 8*1024*1024)
	if e != nil {
		return Receipt{}, e
	}
	r.Evaluation = &v
	if _, _, e = prepareOperation(r, in); e != nil {
		return Receipt{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_label_evaluations VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, s.Tenant, s.Domain, in.DatasetID, v.ID, p.ID, int64(p.Revision), p.Hash, o.ID, o.Hash, b); e != nil {
		return Receipt{}, e
	}
	r.Evaluation = &v
	return finishOperation(ctx, tx, s, r, in, audit)
}
func EvaluationTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, id string) (Evaluation, error) {
	if e := authorize(tx, s, false); e != nil {
		return Evaluation{}, e
	}
	if !valid(dataset, 128) || !valid(id, 128) {
		return Evaluation{}, ErrInvalid
	}
	var v Evaluation
	var pID, pHash, oID, oHash string
	var rev int64
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT policy_id,revision,policy_hash,observation_id,observation_hash,payload FROM adtr.simulation_label_evaluations WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND evaluation_id=$4`, s.Tenant, s.Domain, dataset, id).Scan(&pID, &rev, &pHash, &oID, &oHash, &raw)
	if e == pgx.ErrNoRows {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if len(raw) > 8*1024*1024 || json.Unmarshal(raw, &v) != nil {
		return Evaluation{}, ErrInvalid
	}
	h := v.Hash
	v.Hash = ""
	if digest(v) != h || v.ID != id || !valid(v.Actor, 128) || v.At.IsZero() {
		return Evaluation{}, ErrInvalid
	}
	v.Hash = h
	d, obs, e := loadDataset(ctx, tx, s, dataset)
	if e != nil {
		return Evaluation{}, e
	}
	o, e := findObservation(obs, oID)
	if e != nil {
		return Evaluation{}, e
	}
	p, e := policyVersion(ctx, tx, s, dataset, pID, uint64(rev))
	if e != nil {
		return Evaluation{}, e
	}
	res, e := sensitivelabels.Evaluate(coreInput(s, o, p))
	if e != nil {
		return Evaluation{}, mapCore(e)
	}
	if p.Status != "active" || p.Hash != pHash || o.Hash != oHash || !equal(d, v.Dataset) || !equal(o, v.Observation) || !equal(p, v.Policy) || !equal(res, v.Result) {
		return Evaluation{}, ErrInvalid
	}
	return v, nil
}
func EvaluationsTx(ctx context.Context, tx pgx.Tx, s Scope, dataset string, offset, limit int) (Page[Evaluation], error) {
	if e := authorize(tx, s, false); e != nil {
		return Page[Evaluation]{}, e
	}
	if _, _, e := loadDataset(ctx, tx, s, dataset); e != nil {
		return Page[Evaluation]{}, e
	}
	all, e := ids(ctx, tx, `SELECT evaluation_id FROM adtr.simulation_label_evaluations WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 ORDER BY evaluation_id LIMIT 4097`, s.Tenant, s.Domain, dataset)
	if e != nil {
		return Page[Evaluation]{}, e
	}
	p, e := page(all, offset, limit)
	if e != nil {
		return Page[Evaluation]{}, e
	}
	out := Page[Evaluation]{Items: []Evaluation{}, Count: p.Count, Offset: offset, Limit: limit}
	for _, id := range p.Items {
		v, e := EvaluationTx(ctx, tx, s, dataset, id)
		if e != nil {
			return Page[Evaluation]{}, e
		}
		out.Items = append(out.Items, v)
	}
	return boundedPage(out)
}
func AssetsTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, evaluation string, f AssetFilter) (Page[AssetLabels], error) {
	if f.Kind != "" && f.Kind != string(groupgraph.User) && f.Kind != string(groupgraph.Group) && f.Kind != string(groupgraph.Computer) {
		return Page[AssetLabels]{}, ErrInvalid
	}
	if f.Name != "" && !valid(f.Name, 256) {
		return Page[AssetLabels]{}, ErrInvalid
	}
	v, e := EvaluationTx(ctx, tx, s, dataset, evaluation)
	if e != nil {
		return Page[AssetLabels]{}, e
	}
	meta := map[string]Asset{}
	for _, a := range v.Observation.Assets {
		meta[a.ObjectID] = a
	}
	out := []AssetLabels{}
	for _, o := range v.Result.Objects {
		m := meta[o.ObjectID]
		if f.Kind != "" && string(o.Kind) != f.Kind || f.Name != "" && !strings.Contains(m.Name, f.Name) {
			continue
		}
		reasons := []string{}
		for _, p := range o.Labels {
			if p.Source == "manual" {
				reasons = append(reasons, "manual: "+p.Label)
			} else {
				reasons = append(reasons, fmt.Sprintf("%s: %s (group %s, distance %d)", p.Source, p.Label, meta[p.GroupID].Name, p.Distance))
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, o.State)
		}
		out = append(out, AssetLabels{ObjectLabels: o, Name: m.Name, Description: m.Description, IsClick: true, IsHoneypot: nil, HoneypotAvailability: "unavailable", Reason: strings.Join(reasons, "; ")})
	}
	return page(out, f.Offset, f.Limit)
}
func AssetTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, evaluation, objectID string) (AssetLabels, error) {
	if !valid(objectID, 128) {
		return AssetLabels{}, ErrInvalid
	}
	for offset := 0; offset < MaxNodes; offset += MaxPage {
		p, e := AssetsTx(ctx, tx, s, dataset, evaluation, AssetFilter{Offset: offset, Limit: MaxPage})
		if e != nil {
			return AssetLabels{}, e
		}
		for _, a := range p.Items {
			if a.ObjectID == objectID {
				return a, nil
			}
		}
		if offset+MaxPage >= p.Count {
			break
		}
	}
	return AssetLabels{}, ErrNotFound
}
func ObservationTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, id string) (Observation, error) {
	if e := authorize(tx, s, false); e != nil {
		return Observation{}, e
	}
	if !valid(id, 128) {
		return Observation{}, ErrInvalid
	}
	_, obs, e := loadDataset(ctx, tx, s, dataset)
	if e != nil {
		return Observation{}, e
	}
	return findObservation(obs, id)
}
func HistoryTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, id string, offset, limit int) (Page[Policy], error) {
	return PolicyHistoryTx(ctx, tx, s, dataset, id, offset, limit)
}
