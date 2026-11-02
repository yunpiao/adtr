package simulationlabels

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/sensitivelabels"
)

func policyVersion(ctx context.Context, tx pgx.Tx, s Scope, dataset, id string, revision uint64) (Policy, error) {
	var p Policy
	if !valid(dataset, 128) || !valid(id, 128) || revision < 1 || revision > MaxRevisions {
		return p, ErrInvalid
	}
	var h string
	var b []byte
	e := tx.QueryRow(ctx, `SELECT policy_hash,payload FROM adtr.simulation_label_policy_versions WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND policy_id=$4 AND revision=$5`, s.Tenant, s.Domain, dataset, id, int64(revision)).Scan(&h, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if e != nil {
		return p, e
	}
	if len(b) > 128*1024 || json.Unmarshal(b, &p) != nil {
		return Policy{}, ErrInvalid
	}
	old := p.Hash
	p.Hash = ""
	if old != h || digest(p) != old || p.ID != id || p.DatasetID != dataset || p.Revision != revision || (p.Status != "active" && p.Status != "archived") || !valid(p.Actor, 128) || !valid(p.Reason, 512) || p.At.IsZero() {
		return Policy{}, ErrInvalid
	}
	p.Hash = old
	sp, e := canonicalSpec(p.Spec)
	if e != nil || !equal(sp, p.Spec) {
		return Policy{}, ErrInvalid
	}
	return p, nil
}
func PolicyTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, id string) (Policy, error) {
	if e := authorize(tx, s, false); e != nil {
		return Policy{}, e
	}
	if !valid(dataset, 128) || !valid(id, 128) {
		return Policy{}, ErrInvalid
	}
	var rev int64
	e := tx.QueryRow(ctx, `SELECT revision FROM adtr.simulation_label_policies WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND policy_id=$4`, s.Tenant, s.Domain, dataset, id).Scan(&rev)
	if errors.Is(e, pgx.ErrNoRows) {
		return Policy{}, ErrNotFound
	}
	if e != nil {
		return Policy{}, e
	}
	return policyVersion(ctx, tx, s, dataset, id, uint64(rev))
}
func PoliciesTx(ctx context.Context, tx pgx.Tx, s Scope, dataset string, offset, limit int) (Page[Policy], error) {
	if e := authorize(tx, s, false); e != nil {
		return Page[Policy]{}, e
	}
	if _, _, e := loadDataset(ctx, tx, s, dataset); e != nil {
		return Page[Policy]{}, e
	}
	all, e := ids(ctx, tx, `SELECT policy_id FROM adtr.simulation_label_policies WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 ORDER BY policy_id LIMIT 129`, s.Tenant, s.Domain, dataset)
	if e != nil {
		return Page[Policy]{}, e
	}
	if len(all) > MaxPolicies {
		return Page[Policy]{}, ErrLimit
	}
	p, e := page(all, offset, limit)
	if e != nil {
		return Page[Policy]{}, e
	}
	out := Page[Policy]{Items: []Policy{}, Count: p.Count, Offset: offset, Limit: limit}
	for _, id := range p.Items {
		v, e := PolicyTx(ctx, tx, s, dataset, id)
		if e != nil {
			return Page[Policy]{}, e
		}
		out.Items = append(out.Items, v)
	}
	return boundedPage(out)
}
func PolicyHistoryTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, id string, offset, limit int) (Page[Policy], error) {
	p, e := PolicyTx(ctx, tx, s, dataset, id)
	if e != nil {
		return Page[Policy]{}, e
	}
	revisions := make([]uint64, p.Revision)
	for i := range revisions {
		revisions[i] = uint64(i + 1)
	}
	selected, e := page(revisions, offset, limit)
	if e != nil {
		return Page[Policy]{}, e
	}
	out := Page[Policy]{Items: []Policy{}, Count: selected.Count, Offset: offset, Limit: limit}
	for _, r := range selected.Items {
		v, e := policyVersion(ctx, tx, s, dataset, id, r)
		if e != nil {
			return Page[Policy]{}, e
		}
		out.Items = append(out.Items, v)
	}
	return boundedPage(out)
}
func MutatePolicyTx(ctx context.Context, tx pgx.Tx, s Scope, in PolicyInput, audit AuditFunc) (Receipt, error) {
	if e := authorize(tx, s, true); e != nil {
		return Receipt{}, e
	}
	if !valid(in.PolicyID, 128) || in.ExpectedRevision > MaxRevisions {
		return Receipt{}, ErrInvalid
	}
	if in.Action != "create" && in.Action != "update" && in.Action != "archive" && in.Action != "restore" {
		return Receipt{}, ErrInvalid
	}
	if in.Action == "create" || in.Action == "update" {
		p, e := canonicalSpec(in.Spec)
		if e != nil {
			return Receipt{}, e
		}
		in.Spec = p
	} else if !equal(in.Spec, PolicySpec{}) {
		return Receipt{}, ErrInvalid
	}
	r, replayed, e := beginOperation(ctx, tx, s, in.DatasetID, in.Actor, in.OperationID, in.Reason, in.Action, in, audit)
	if e != nil || replayed {
		return r, e
	}
	_, obs, e := loadDataset(ctx, tx, s, in.DatasetID)
	if e != nil {
		return Receipt{}, e
	}
	old, e := PolicyTx(ctx, tx, s, in.DatasetID, in.PolicyID)
	if in.Action == "create" {
		if in.ExpectedRevision != 0 || e == nil {
			return Receipt{}, ErrConflict
		}
		if !errors.Is(e, ErrNotFound) {
			return Receipt{}, e
		}
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_policies WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, in.DatasetID).Scan(&count); e != nil {
			return Receipt{}, e
		}
		if count >= MaxPolicies {
			return Receipt{}, ErrLimit
		}
	} else {
		if e != nil {
			return Receipt{}, e
		}
		if old.Revision != in.ExpectedRevision {
			return Receipt{}, ErrConflict
		}
		if old.Revision >= MaxRevisions {
			return Receipt{}, ErrLimit
		}
		if (in.Action == "restore" && old.Status != "archived") || (in.Action != "restore" && old.Status != "active") {
			return Receipt{}, ErrConflict
		}
	}
	p := Policy{ID: in.PolicyID, DatasetID: in.DatasetID, Revision: old.Revision + 1, Status: "active", Spec: in.Spec, Actor: in.Actor, Reason: in.Reason, At: r.At}
	if in.Action == "archive" || in.Action == "restore" {
		p.Spec = old.Spec
	}
	if in.Action == "archive" {
		p.Status = "archived"
	}
	// Policy references must exist in the explicitly fixed first baseline. Later
	// observations may be incompatible; evaluation then rejects missing identities.
	if _, e = sensitivelabels.Evaluate(coreInput(s, obs[0], p)); e != nil {
		return Receipt{}, mapCore(e)
	}
	p.Hash = digest(p)
	b, e := marshalBounded(p, 128*1024)
	if e != nil {
		return Receipt{}, e
	}
	r.Policy = &p
	if _, _, e = prepareOperation(r, in); e != nil {
		return Receipt{}, e
	}
	if in.Action == "create" {
		_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_label_policies VALUES($1,$2,$3,$4,1)`, s.Tenant, s.Domain, in.DatasetID, in.PolicyID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, e = tx.Exec(ctx, `UPDATE adtr.simulation_label_policies SET revision=$5 WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND policy_id=$4 AND revision=$6`, s.Tenant, s.Domain, in.DatasetID, in.PolicyID, int64(p.Revision), int64(in.ExpectedRevision))
		if e == nil && tag.RowsAffected() != 1 {
			return Receipt{}, ErrConflict
		}
	}
	if e != nil {
		return Receipt{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_label_policy_versions VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, in.DatasetID, in.PolicyID, int64(p.Revision), p.Hash, b); e != nil {
		return Receipt{}, e
	}
	r.Policy = &p
	return finishOperation(ctx, tx, s, r, in, audit)
}
