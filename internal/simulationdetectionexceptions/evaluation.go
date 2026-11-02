package simulationdetectionexceptions

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionexceptions"
	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

func EvaluateTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, in EvaluationInput, audit AuditFunc) (Receipt, error) {
	var out Receipt
	if e := authorize(tx, s, true); e != nil {
		return out, e
	}
	if audit == nil || !identifier.MatchString(in.ID) || !identifier.MatchString(in.OperationID) || in.ExpectedRevision < 1 || in.ExpectedRevision > MaxRevisions {
		return out, ErrInvalid
	}
	intent := Intent{DomainID: s.Domain, Action: "evaluate", OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, ID: in.ID, SourceTaskID: in.SourceTaskID}
	h := digest(intent)
	if e := lock(ctx, tx, s); e != nil {
		return out, e
	}
	r, e := receipt(ctx, tx, s, in.OperationID)
	if e == nil {
		if r.RequestHash != h {
			return out, ErrConflict
		}
		return ReceiptTx(ctx, tx, s, engine, in.OperationID)
	}
	if !errors.Is(e, ErrNotFound) {
		return out, e
	}
	record, e := SnapshotTx(ctx, tx, s)
	if e != nil {
		return out, e
	}
	if record.Snapshot.Revision != in.ExpectedRevision {
		return out, ErrConflict
	}
	src, events, e := source(ctx, tx, s, engine, in.SourceTaskID)
	if e != nil {
		return out, e
	}
	var count int
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_or(evaluation_id=$3),false) FROM adtr.simulation_exception_evaluations WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain, in.ID).Scan(&count, &exists); e != nil {
		return out, e
	}
	if exists {
		return out, ErrConflict
	}
	if count >= MaxEvaluations {
		return out, ErrLimit
	}
	now := time.Now().UTC()
	matches, e := detectionexceptions.Evaluate(ctx, access(s), record.Snapshot, events, src.Matches, now)
	if e != nil {
		return out, mapCore(e)
	}
	v := Evaluation{Contract: Contract, Simulation: true, ID: in.ID, ActorID: s.ActorID, SourceTaskID: in.SourceTaskID, SourceResultHash: digest(src), Source: src, Revision: record.Snapshot.Revision, SnapshotHash: record.Hash, EvaluatedAt: now, Matches: matches}
	v.Hash = digest(v)
	out = Receipt{Contract: Contract, Simulation: true, OperationID: in.OperationID, Action: "evaluate", ActorID: s.ActorID, At: now, Intent: intent, RequestHash: h, Revision: v.Revision, SnapshotHash: v.SnapshotHash, Evaluation: &v}
	b, e := marshalBounded(v, MaxRecordBytes)
	if e != nil {
		return Receipt{}, e
	}
	if _, e = marshalBounded(out, MaxResponseBytes); e != nil {
		return Receipt{}, e
	}
	if record.ActorID == 0 {
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_exception_heads WHERE tenant_id=$1 AND domain_id=$2)`, s.Tenant, s.Domain).Scan(&exists); e != nil {
			return Receipt{}, e
		}
		if !exists {
			if e = persist(ctx, tx, s, record); e != nil {
				return Receipt{}, e
			}
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_exception_evaluations VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, in.ID, s.ActorID, v.Revision, v.Hash, b); e != nil {
		return Receipt{}, e
	}
	if e = saveReceipt(ctx, tx, s, out, audit); e != nil {
		return Receipt{}, e
	}
	return out, nil
}
func EvaluationTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, id string) (Evaluation, error) {
	var out Evaluation
	if e := authorize(tx, s, false); e != nil {
		return out, e
	}
	if !s.DetectionRead || !s.EventsRead {
		return out, ErrDenied
	}
	if !identifier.MatchString(id) {
		return out, ErrInvalid
	}
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT body,hash FROM adtr.simulation_exception_evaluations WHERE tenant_id=$1 AND domain_id=$2 AND evaluation_id=$3 AND actor_id=$4`, s.Tenant, s.Domain, id, s.ActorID).Scan(&b, &h)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if e = decode(b, MaxRecordBytes, &out); e != nil {
		return out, e
	}
	got := out.Hash
	out.Hash = ""
	want := digest(out)
	out.Hash = got
	if got != want || got != h || out.ID != id || out.ActorID != s.ActorID || out.Contract != Contract || !out.Simulation {
		return Evaluation{}, ErrInvalid
	}
	src, events, e := source(ctx, tx, s, engine, out.SourceTaskID)
	if e != nil {
		return Evaluation{}, e
	}
	if digest(src) != out.SourceResultHash || digest(out.Source) != out.SourceResultHash {
		return Evaluation{}, ErrInvalid
	}
	record, e := HistoryTx(ctx, tx, s, out.Revision)
	if e != nil {
		return Evaluation{}, e
	}
	if record.Hash != out.SnapshotHash {
		return Evaluation{}, ErrInvalid
	}
	matches, e := detectionexceptions.Evaluate(ctx, access(s), record.Snapshot, events, src.Matches, out.EvaluatedAt)
	if e != nil {
		return Evaluation{}, mapCore(e)
	}
	if digest(matches) != digest(out.Matches) {
		return Evaluation{}, ErrInvalid
	}
	return out, nil
}
func EvaluationsTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine) ([]EvaluationSummary, error) {
	out := []EvaluationSummary{}
	if e := authorize(tx, s, false); e != nil {
		return out, e
	}
	if !s.DetectionRead || !s.EventsRead {
		return out, ErrDenied
	}
	rows, e := tx.Query(ctx, `SELECT evaluation_id FROM adtr.simulation_exception_evaluations WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 ORDER BY evaluation_id LIMIT 65`, s.Tenant, s.Domain, s.ActorID)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > MaxEvaluations {
		return out, ErrLimit
	}
	for _, id := range ids {
		v, e := EvaluationTx(ctx, tx, s, engine, id)
		if e != nil {
			return nil, e
		}
		out = append(out, EvaluationSummary{ID: id, SourceTaskID: v.SourceTaskID, Revision: v.Revision, EvaluatedAt: v.EvaluatedAt})
	}
	return out, nil
}
