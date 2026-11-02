package simulationdetection

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

func SubmitRunTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, in RunInput, audit AuditFunc) (tasks.Submission, error) {
	if e := authorize(tx, s, true); e != nil {
		return tasks.Submission{}, e
	}
	if engine == nil || audit == nil || !identifier.MatchString(in.OperationID) || !identifier.MatchString(in.TemplateID) || !identifier.MatchString(in.DatasetID) || in.TemplateVersion < 1 {
		return tasks.Submission{}, ErrInvalid
	}
	if e := lock(ctx, tx, s); e != nil {
		return tasks.Submission{}, e
	}
	h := digest(in)
	r, old, e := receipt(ctx, tx, s, in.OperationID)
	if e == nil {
		if old != h || r.Submission == nil {
			return tasks.Submission{}, ErrConflict
		}
		d, e := ReadRunTx(ctx, tx, s, engine, r.Submission.Task.ID)
		if e != nil {
			return tasks.Submission{}, e
		}
		return tasks.Submission{Task: d.Task, Replayed: true}, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return tasks.Submission{}, e
	}
	t, e := DetailTx(ctx, tx, s, in.TemplateID)
	if e != nil {
		return tasks.Submission{}, e
	}
	if t.Archived || t.Version != in.TemplateVersion {
		return tasks.Submission{}, ErrConflict
	}
	d, _, e := dataset(ctx, tx, s, in.DatasetID)
	if e != nil {
		return tasks.Submission{}, e
	}
	p := Payload{TemplateID: t.ID, TemplateVersion: t.Version, TemplateHash: t.Hash, DatasetID: d.ID, FixtureHash: d.FixtureHash, MappingVersion: MappingVersion}
	b, _ := json.Marshal(p)
	sub, e := engine.SubmitTx(ctx, tx, tasks.Principal{TenantID: s.Tenant, ActorID: s.ActorID}, tasks.SubmitInput{TaskName: KindName, DomainID: s.Domain, PayloadVersion: 1, Payload: b, IdempotencyKey: in.OperationID})
	if e != nil {
		return sub, e
	}
	r = Receipt{OperationID: in.OperationID, Kind: "run", Submission: &sub}
	e = saveReceipt(ctx, tx, s, h, r, Audit{Action: "run", ObjectID: sub.Task.ID, OperationID: in.OperationID, Version: t.Version, Hash: t.Hash}, audit)
	return sub, e
}
func ReadRunTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, id string) (tasks.Detail, error) {
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
func CancelRunTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, id, op string, audit AuditFunc) (tasks.Task, error) {
	if e := authorize(tx, s, true); e != nil {
		return tasks.Task{}, e
	}
	if engine == nil || audit == nil || !identifier.MatchString(op) {
		return tasks.Task{}, ErrInvalid
	}
	if e := lock(ctx, tx, s); e != nil {
		return tasks.Task{}, e
	}
	if _, e := ReadRunTx(ctx, tx, s, engine, id); e != nil {
		return tasks.Task{}, e
	}
	h := digest([]string{"cancel", id})
	r, old, e := receipt(ctx, tx, s, op)
	if e == nil {
		if h != old || r.Task == nil {
			return tasks.Task{}, ErrConflict
		}
		return *r.Task, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return tasks.Task{}, e
	}
	t, e := engine.CancelTx(ctx, tx, tasks.Principal{TenantID: s.Tenant, ActorID: s.ActorID}, id)
	if e != nil {
		return t, e
	}
	r = Receipt{OperationID: op, Kind: "cancel", Task: &t}
	e = saveReceipt(ctx, tx, s, h, r, Audit{Action: "cancel", ObjectID: id, OperationID: op}, audit)
	return t, e
}
