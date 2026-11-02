package simulationlabels

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

func beginOperation(ctx context.Context, tx pgx.Tx, s Scope, dataset, actor, op, reason, action string, in any, audit AuditFunc) (Receipt, bool, error) {
	r := Receipt{}
	if audit == nil || !valid(dataset, 128) || !valid(actor, 128) || !valid(op, 128) || !valid(reason, 512) {
		return r, false, ErrInvalid
	}
	if _, e := marshalBounded(in, 128*1024); e != nil {
		return r, false, e
	}
	if e := lock(ctx, tx, s); e != nil {
		return r, false, e
	}
	r, h, e := readReceipt(ctx, tx, s, dataset, actor, op)
	if e == nil {
		if h != digest(in) || r.Action != action {
			return Receipt{}, false, ErrConflict
		}
		return r, true, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return Receipt{}, false, e
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_label_receipts WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, dataset).Scan(&count); e != nil {
		return Receipt{}, false, e
	}
	if count >= 16384 {
		return Receipt{}, false, ErrLimit
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return Receipt{}, false, e
	}
	return Receipt{Simulation: true, OperationID: op, Actor: actor, Action: action, DatasetID: dataset, Reason: reason, At: now.UTC()}, false, nil
}

// prepareOperation is called BEFORE any business INSERT/pointer UPDATE. The
// receipt envelope can exceed the cap even when the nested result alone fits.
func prepareOperation(r Receipt, in any) ([]byte, []byte, error) {
	req, e := marshalBounded(in, 128*1024)
	if e != nil {
		return nil, nil, e
	}
	b, e := marshalBounded(r, 8*1024*1024)
	if e != nil {
		return nil, nil, e
	}
	return b, req, nil
}
func finishOperation(ctx context.Context, tx pgx.Tx, s Scope, r Receipt, in any, audit AuditFunc) (Receipt, error) {
	b, req, e := prepareOperation(r, in)
	if e != nil {
		return Receipt{}, e
	}
	if e = audit(ctx, tx, r); e != nil {
		return Receipt{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_label_receipts VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, s.Tenant, s.Domain, r.DatasetID, r.Actor, r.OperationID, digest(in), req, b)
	if e != nil {
		return Receipt{}, e
	}
	return r, nil
}
func readReceipt(ctx context.Context, tx pgx.Tx, s Scope, dataset, actor, op string) (Receipt, string, error) {
	var r Receipt
	var h string
	var b, request []byte
	e := tx.QueryRow(ctx, `SELECT request_hash,request,payload FROM adtr.simulation_label_receipts WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND actor=$4 AND operation_id=$5`, s.Tenant, s.Domain, dataset, actor, op).Scan(&h, &request, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, "", ErrNotFound
	}
	if e != nil {
		return r, "", e
	}
	if len(b) > 8*1024*1024 || len(request) > 128*1024 || json.Unmarshal(b, &r) != nil || !r.Simulation || r.Actor != actor || r.OperationID != op || r.DatasetID != dataset || !valid(r.Reason, 512) || r.At.IsZero() {
		return Receipt{}, "", ErrInvalid
	}
	// Reconstruct the typed request and bind receipt fields, avoiding reliance on
	// jsonb key ordering or a serialized client's assertion of successful work.
	switch r.Action {
	case "create", "update", "archive", "restore":
		var in PolicyInput
		if json.Unmarshal(request, &in) != nil || digest(in) != h || in.Actor != actor || in.OperationID != op || in.DatasetID != dataset || in.Action != r.Action || in.Reason != r.Reason || r.Policy == nil || r.Evaluation != nil || r.Comparison != nil || in.PolicyID != r.Policy.ID || in.ExpectedRevision+1 != r.Policy.Revision {
			return Receipt{}, "", ErrInvalid
		}
		p, e := policyVersion(ctx, tx, s, dataset, r.Policy.ID, r.Policy.Revision)
		if e != nil {
			return Receipt{}, "", e
		}
		if !equal(p, *r.Policy) || p.Actor != actor || p.Reason != r.Reason || !p.At.Equal(r.At) || ((in.Action == "create" || in.Action == "update") && !equal(in.Spec, p.Spec)) {
			return Receipt{}, "", ErrInvalid
		}
	case "evaluate":
		var in EvaluateInput
		if json.Unmarshal(request, &in) != nil || digest(in) != h || in.Actor != actor || in.OperationID != op || in.DatasetID != dataset || in.Reason != r.Reason || r.Policy != nil || r.Evaluation == nil || r.Comparison != nil {
			return Receipt{}, "", ErrInvalid
		}
		v, e := EvaluationTx(ctx, tx, s, dataset, r.Evaluation.ID)
		if e != nil {
			return Receipt{}, "", e
		}
		if v.ID != "evaluation:"+digest([]string{actor, op}) || !equal(v, *r.Evaluation) || v.Policy.ID != in.PolicyID || v.Policy.Revision != in.ExpectedRevision || v.Observation.ID != in.ObservationID || v.Actor != actor || !v.At.Equal(r.At) {
			return Receipt{}, "", ErrInvalid
		}
	case "compare":
		var in CompareInput
		if json.Unmarshal(request, &in) != nil || digest(in) != h || in.Actor != actor || in.OperationID != op || in.DatasetID != dataset || in.Reason != r.Reason || r.Policy != nil || r.Evaluation != nil || r.Comparison == nil {
			return Receipt{}, "", ErrInvalid
		}
		v, e := ComparisonTx(ctx, tx, s, dataset, r.Comparison.ID)
		if e != nil {
			return Receipt{}, "", e
		}
		if !equal(v, *r.Comparison) || v.BeforeID != in.BeforeID || v.AfterID != in.AfterID {
			return Receipt{}, "", ErrInvalid
		}
	default:
		return Receipt{}, "", ErrInvalid
	}
	return r, h, nil
}
func OperationTx(ctx context.Context, tx pgx.Tx, s Scope, dataset, actor, op string) (Receipt, error) {
	if e := authorize(tx, s, false); e != nil {
		return Receipt{}, e
	}
	if !valid(dataset, 128) || !valid(actor, 128) || !valid(op, 128) {
		return Receipt{}, ErrInvalid
	}
	r, _, e := readReceipt(ctx, tx, s, dataset, actor, op)
	return r, e
}
