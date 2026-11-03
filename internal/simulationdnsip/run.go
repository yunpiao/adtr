package simulationdnsip

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/tasks"
)

func receipt(ctx context.Context, tx pgx.Tx, s Scope, op string) (Receipt, error) {
	var r Receipt
	var b []byte
	var h, id, action string
	e := tx.QueryRow(ctx, `SELECT request_hash,task_id,action,body FROM adtr.simulation_dns_ip_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4`, s.Tenant, s.Domain, s.ActorID, op).Scan(&h, &id, &action, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if e = strict(b, MaxResponseBytes, &r); e != nil {
		return r, e
	}
	if r.Contract != Contract || !r.Simulation || r.ActorID != s.ActorID || r.OperationID != op || r.Action != action || r.Intent.DomainID != s.Domain || r.Intent.Action != action || r.Intent.OperationID != op || r.RequestHash != h || digest(r.Intent) != h {
		return Receipt{}, ErrInvalid
	}
	if action == "export" {
		if r.Submission == nil || r.Task != nil || r.Submission.Task.ID != id || r.Intent.Export == nil || r.Intent.TaskID != "" || r.Intent.Export.OperationID != op {
			return Receipt{}, ErrInvalid
		}
	} else if action == "cancel" {
		if r.Task == nil || r.Submission != nil || r.Task.ID != id || r.Intent.TaskID != id || r.Intent.Export != nil {
			return Receipt{}, ErrInvalid
		}
	} else {
		return Receipt{}, ErrInvalid
	}
	return r, nil
}
func ReadExportTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, id string) (tasks.Detail, error) {
	if e := authorize(tx, s, false); e != nil {
		return tasks.Detail{}, e
	}
	if engine == nil {
		return tasks.Detail{}, ErrInvalid
	}
	d, e := engine.DetailTx(ctx, tx, tasks.Principal{TenantID: s.Tenant, ActorID: s.ActorID}, id)
	if e != nil {
		return d, e
	}
	if d.Task.Kind != KindName || d.Task.DomainID != s.Domain || d.Task.ActorID != s.ActorID {
		return tasks.Detail{}, ErrNotFound
	}
	return d, nil
}
func ReceiptTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, op string) (Receipt, error) {
	if e := authorize(tx, s, false); e != nil {
		return Receipt{}, e
	}
	if !identifier.MatchString(op) {
		return Receipt{}, ErrInvalid
	}
	r, e := receipt(ctx, tx, s, op)
	if e != nil {
		return r, e
	}
	id := r.Intent.TaskID
	if r.Submission != nil {
		id = r.Submission.Task.ID
	}
	d, e := ReadExportTx(ctx, tx, s, engine, id)
	if e != nil {
		return Receipt{}, e
	}
	if r.Submission != nil {
		r.Submission.Task = d.Task
	} else {
		r.Task = &d.Task
	}
	return r, nil
}
func saveReceipt(ctx context.Context, tx pgx.Tx, s Scope, r Receipt, audit AuditFunc) error {
	b, e := bounded(r, MaxResponseBytes)
	if e != nil {
		return e
	}
	id := r.Intent.TaskID
	if r.Submission != nil {
		id = r.Submission.Task.ID
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_dns_ip_receipts VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, s.Tenant, s.Domain, s.ActorID, r.OperationID, r.RequestHash, id, r.Action, b)
	if e != nil {
		return e
	}
	return audit(ctx, tx, r)
}
func SubmitExportTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, in ExportInput, audit AuditFunc) (Receipt, error) {
	if e := authorize(tx, s, true); e != nil {
		return Receipt{}, e
	}
	if engine == nil || audit == nil || !identifier.MatchString(in.OperationID) || !identifier.MatchString(in.ObservationID) || !hashPattern.MatchString(in.ObservationHash) {
		return Receipt{}, ErrInvalid
	}
	if e := lock(ctx, tx, s); e != nil {
		return Receipt{}, e
	}
	intent := Intent{DomainID: s.Domain, Action: "export", OperationID: in.OperationID, Export: &in}
	h := digest(intent)
	old, e := receipt(ctx, tx, s, in.OperationID)
	if e == nil {
		if old.RequestHash != h {
			return Receipt{}, ErrConflict
		}
		r, e := ReceiptTx(ctx, tx, s, engine, in.OperationID)
		if r.Submission != nil {
			r.Submission.Replayed = true
		}
		return r, e
	}
	if !errors.Is(e, ErrNotFound) {
		return Receipt{}, e
	}
	v, e := observation(ctx, tx, s, in.ObservationID)
	if e != nil {
		return Receipt{}, e
	}
	if v.Observation.Hash != in.ObservationHash {
		return Receipt{}, ErrConflict
	}
	if _, e = dnsipassets.Select(v.Records, dnsipassets.Filter{Domain: v.Observation.DNSDomain, Observation: in.ObservationID, Keyword: in.Keyword, Family: in.Family}); e != nil {
		return Receipt{}, ErrInvalid
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_dns_ip_receipts WHERE tenant_id=$1 AND domain_id=$2 AND action='export'`, s.Tenant, s.Domain).Scan(&count); e != nil {
		return Receipt{}, e
	}
	if count >= MaxExports {
		return Receipt{}, ErrLimit
	}
	p := Payload{ObservationID: in.ObservationID, ObservationHash: in.ObservationHash, DNSDomain: v.Observation.DNSDomain, Keyword: in.Keyword, Family: in.Family, Contract: Contract}
	b, _ := json.Marshal(p)
	sub, e := engine.SubmitTx(ctx, tx, tasks.Principal{TenantID: s.Tenant, ActorID: s.ActorID}, tasks.SubmitInput{TaskName: KindName, DomainID: s.Domain, PayloadVersion: 1, Payload: b, IdempotencyKey: in.OperationID})
	if e != nil {
		return Receipt{}, e
	}
	r := Receipt{Contract: Contract, Simulation: true, OperationID: in.OperationID, Action: "export", ActorID: s.ActorID, Intent: intent, RequestHash: h, Submission: &sub}
	if e = saveReceipt(ctx, tx, s, r, audit); e != nil {
		return Receipt{}, e
	}
	return r, nil
}
func CancelExportTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, id, op string, audit AuditFunc) (Receipt, error) {
	if e := authorize(tx, s, true); e != nil {
		return Receipt{}, e
	}
	if engine == nil || audit == nil || !identifier.MatchString(op) {
		return Receipt{}, ErrInvalid
	}
	if e := lock(ctx, tx, s); e != nil {
		return Receipt{}, e
	}
	if _, e := ReadExportTx(ctx, tx, s, engine, id); e != nil {
		return Receipt{}, e
	}
	intent := Intent{DomainID: s.Domain, Action: "cancel", OperationID: op, TaskID: id}
	h := digest(intent)
	old, e := receipt(ctx, tx, s, op)
	if e == nil {
		if old.RequestHash != h {
			return Receipt{}, ErrConflict
		}
		return ReceiptTx(ctx, tx, s, engine, op)
	}
	if !errors.Is(e, ErrNotFound) {
		return Receipt{}, e
	}
	// At most one cancellation intent per export, without consuming another export reservation.
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_dns_ip_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND action='cancel' AND task_id=$4)`, s.Tenant, s.Domain, s.ActorID, id).Scan(&exists); e != nil {
		return Receipt{}, e
	}
	if exists {
		return Receipt{}, ErrConflict
	}
	t, e := engine.CancelTx(ctx, tx, tasks.Principal{TenantID: s.Tenant, ActorID: s.ActorID}, id)
	if e != nil {
		return Receipt{}, e
	}
	r := Receipt{Contract: Contract, Simulation: true, OperationID: op, Action: "cancel", ActorID: s.ActorID, Intent: intent, RequestHash: h, Task: &t}
	if e = saveReceipt(ctx, tx, s, r, audit); e != nil {
		return Receipt{}, e
	}
	return r, nil
}
