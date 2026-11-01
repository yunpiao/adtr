package simulationrisk

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

func requestHash(in ResetInput) string { return digest(in) }

// readReceipt validates the receipt against its canonical immutable dataset and
// reset prefix, not the latest revision. Replays remain stable after later resets.
func readReceipt(ctx context.Context, tx pgx.Tx, s Scope, id, actor, op string) (Receipt, string, error) {
	var r Receipt
	var h, subject, fixture string
	var rev int64
	var b []byte
	e := tx.QueryRow(ctx, `SELECT request_hash,subject,fixture_hash,revision,receipt FROM adtr.simulation_risk_receipts WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND actor=$4 AND operation_id=$5`, s.Tenant, s.Domain, id, actor, op).Scan(&h, &subject, &fixture, &rev, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, "", ErrNotFound
	}
	if e != nil {
		return r, "", e
	}
	if len(b) > 4*1024*1024 {
		return r, "", ErrLimit
	}
	if json.Unmarshal(b, &r) != nil || !r.Simulation || r.Actor != actor || r.OperationID != op || !valid(subject, 128) || rev < 2 || rev > MaxResets+1 || r.Result.Revision != uint64(rev) || r.Result.Scope.UserID != subject || !valid(r.Reason, 512) {
		return Receipt{}, "", ErrInvalid
	}
	d, events, e := dataset(ctx, tx, s, id)
	if e != nil {
		return Receipt{}, "", e
	}
	if fixture != d.FixtureHash {
		return Receipt{}, "", ErrInvalid
	}
	resets, e := loadResets(ctx, tx, s, d, subject)
	if e != nil {
		return Receipt{}, "", e
	}
	if len(resets) < int(rev)-1 {
		return Receipt{}, "", ErrInvalid
	}
	last := resets[rev-2]
	if last.ID != "reset:"+digest([]string{actor, op}) || last.Reason != r.Reason || !last.At.Equal(r.Result.AsOf) {
		return Receipt{}, "", ErrInvalid
	}
	expected, e := evaluate(s, d, subject, events, resets[:rev-1], r.Result.AsOf)
	if e != nil {
		return Receipt{}, "", e
	}
	// JSON decoding normalizes zero-length arrays and times to their wire form.
	eb, _ := json.Marshal(expected)
	var normalized Result
	if json.Unmarshal(eb, &normalized) != nil || !reflect.DeepEqual(normalized, r.Result) {
		return Receipt{}, "", ErrInvalid
	}
	if h != requestHash(ResetInput{DatasetID: id, Subject: subject, OperationID: op, Actor: actor, Reason: r.Reason, ExpectedRevision: uint64(rev - 1)}) {
		return Receipt{}, "", ErrInvalid
	}
	return r, h, nil
}

// OperationTx only retrieves receipts owned by the trusted authenticated actor.
func OperationTx(ctx context.Context, tx pgx.Tx, s Scope, id, actor, operation string) (Receipt, error) {
	if e := authorize(tx, s, false); e != nil {
		return Receipt{}, e
	}
	if !valid(id, 128) || !valid(actor, 128) || !valid(operation, 128) {
		return Receipt{}, ErrInvalid
	}
	r, _, e := readReceipt(ctx, tx, s, id, actor, operation)
	return r, e
}

// ResetTx obtains receipt-key then scope locks, checks expected revision, chooses
// database time, and writes reset, application audit and receipt in one tx.
func ResetTx(ctx context.Context, tx pgx.Tx, s Scope, in ResetInput, audit AuditFunc) (Receipt, error) {
	if e := authorize(tx, s, true); e != nil {
		return Receipt{}, e
	}
	if audit == nil || !valid(in.DatasetID, 128) || !valid(in.Subject, 128) || !valid(in.OperationID, 128) || !valid(in.Actor, 128) || !valid(in.Reason, 512) || in.ExpectedRevision < 1 || in.ExpectedRevision > MaxResets+1 {
		return Receipt{}, ErrInvalid
	}
	if e := lock(ctx, tx, "operation", s.Tenant, s.Domain, in.DatasetID, in.Actor, in.OperationID); e != nil {
		return Receipt{}, e
	}
	if e := lock(ctx, tx, "scope", s.Tenant, s.Domain, in.DatasetID, in.Subject); e != nil {
		return Receipt{}, e
	}
	r, h, e := readReceipt(ctx, tx, s, in.DatasetID, in.Actor, in.OperationID)
	if e == nil {
		if h != requestHash(in) {
			return Receipt{}, ErrConflict
		}
		return r, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return Receipt{}, e
	}
	d, events, e := dataset(ctx, tx, s, in.DatasetID)
	if e != nil {
		return Receipt{}, e
	}
	if _, _, e = mapped(s, in.Subject, events); e != nil {
		return Receipt{}, e
	}
	resets, e := loadResets(ctx, tx, s, d, in.Subject)
	if e != nil {
		return Receipt{}, e
	}
	if uint64(len(resets)+1) != in.ExpectedRevision {
		return Receipt{}, ErrConflict
	}
	if len(resets) >= MaxResets {
		return Receipt{}, ErrLimit
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return Receipt{}, e
	}
	now = now.UTC()
	// Regressing server time cannot create a reset before an existing boundary.
	if e = liveBoundary(resets, now); e != nil {
		return Receipt{}, e
	}
	reset := riskscore.Reset{ID: "reset:" + digest([]string{in.Actor, in.OperationID}), Scope: riskscore.Scope{TenantID: s.Tenant, DomainID: s.Domain, UserID: in.Subject}, At: now, Reason: in.Reason}
	resets = append(resets, reset)
	result, e := evaluate(s, d, in.Subject, events, resets, now)
	if e != nil {
		return Receipt{}, e
	}
	r = Receipt{Simulation: true, OperationID: in.OperationID, Actor: in.Actor, Reason: in.Reason, Result: result}
	if e = insertReset(ctx, tx, s, d, in, reset, result.Revision); e != nil {
		return Receipt{}, e
	}
	if e = audit(ctx, tx, r); e != nil {
		return Receipt{}, e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return Receipt{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_risk_receipts(tenant_id,domain_id,dataset_id,fixture_hash,subject,revision,actor,operation_id,request_hash,receipt) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, s.Tenant, s.Domain, d.ID, d.FixtureHash, in.Subject, int64(result.Revision), in.Actor, in.OperationID, requestHash(in), b)
	if e != nil {
		return Receipt{}, e
	}
	return r, nil
}
func insertReset(ctx context.Context, tx pgx.Tx, s Scope, d simulationevents.Dataset, in ResetInput, r riskscore.Reset, revision uint64) error {
	_, e := tx.Exec(ctx, `INSERT INTO adtr.simulation_risk_resets(tenant_id,domain_id,dataset_id,fixture_hash,subject,revision,reset_id,actor,operation_id,reason,at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, s.Tenant, s.Domain, d.ID, d.FixtureHash, in.Subject, int64(revision), r.ID, in.Actor, in.OperationID, in.Reason, r.At)
	return e
}
