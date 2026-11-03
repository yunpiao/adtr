package simulationassetassessment

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

func authorize(tx pgx.Tx, s Scope, write bool) error {
	if tx == nil || !identifier.MatchString(s.Tenant) || !identifier.MatchString(s.Domain) || s.ActorID <= 0 || s.Epoch <= 0 || !s.Read || (write && !s.Manage) {
		return ErrDenied
	}
	return nil
}
func lock(ctx context.Context, tx pgx.Tx, s Scope) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-assets:"+digest([]any{s.Tenant, s.Domain, s.ActorID}))
	return e
}

// SeedFixtureTx is trusted installation/test code only. It accepts identifiers,
// not evidence, timestamps, completeness, hash or evaluation results.
func SeedFixtureTx(ctx context.Context, tx pgx.Tx, tenant, domain string) (Observation, error) {
	if tx == nil || !identifier.MatchString(tenant) || !identifier.MatchString(domain) {
		return Observation{}, ErrInvalid
	}
	o := fixture(tenant, domain)
	b, e := marshalBounded(o, MaxRecordBytes)
	if e != nil {
		return Observation{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_asset_observations VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, tenant, domain, o.ID, o.Hash, b)
	if e != nil {
		return Observation{}, e
	}
	return ObservationTx(ctx, tx, Scope{Tenant: tenant, Domain: domain, ActorID: 1, Read: true, Epoch: 1}, o.ID)
}
func ObservationTx(ctx context.Context, tx pgx.Tx, s Scope, id string) (Observation, error) {
	var o Observation
	if e := authorize(tx, s, false); e != nil {
		return o, e
	}
	if !identifier.MatchString(id) {
		return o, ErrInvalid
	}
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT body,hash FROM adtr.simulation_asset_observations WHERE tenant_id=$1 AND domain_id=$2 AND observation_id=$3`, s.Tenant, s.Domain, id).Scan(&b, &h)
	if errors.Is(e, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	if e != nil {
		return o, e
	}
	if e = decode(b, &o); e != nil {
		return Observation{}, e
	}
	if o.ID != id || o.Hash != h {
		return Observation{}, ErrInvalid
	}
	return o, validateObservation(o, s.Tenant, s.Domain)
}
func ReportTx(ctx context.Context, tx pgx.Tx, s Scope, id string) (Report, error) {
	var r Report
	if e := authorize(tx, s, false); e != nil {
		return r, e
	}
	if !identifier.MatchString(id) {
		return r, ErrInvalid
	}
	var b []byte
	var h string
	var epoch int64
	e := tx.QueryRow(ctx, `SELECT body,hash,epoch FROM adtr.simulation_asset_reports WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND report_id=$4`, s.Tenant, s.Domain, s.ActorID, id).Scan(&b, &h, &epoch)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if epoch != s.Epoch {
		return r, ErrNotFound
	}
	if e = decode(b, &r); e != nil {
		return Report{}, e
	}
	if r.ID != id || r.Hash != h || r.Epoch != epoch {
		return Report{}, ErrInvalid
	}
	if e = validateReport(s, r); e != nil {
		return Report{}, e
	}
	o, e := ObservationTx(ctx, tx, s, r.ObservationID)
	if e != nil {
		return Report{}, e
	}
	if o.Hash != r.SourceHash {
		return Report{}, ErrInvalid
	}
	return r, nil
}
func ReceiptTx(ctx context.Context, tx pgx.Tx, s Scope, op string) (Receipt, error) {
	var r Receipt
	if e := authorize(tx, s, false); e != nil {
		return r, e
	}
	if !identifier.MatchString(op) {
		return r, ErrInvalid
	}
	var b []byte
	var h string
	var epoch int64
	e := tx.QueryRow(ctx, `SELECT body,request_hash,epoch FROM adtr.simulation_asset_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4`, s.Tenant, s.Domain, s.ActorID, op).Scan(&b, &h, &epoch)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if epoch != s.Epoch {
		return r, ErrNotFound
	}
	if e = decode(b, &r); e != nil {
		return Receipt{}, e
	}
	if r.Contract != Contract || !r.Simulation || r.OperationID != op || r.Input.OperationID != op || !validInput(r.Input) || digest(r.Input) != h || r.RequestHash != h || r.Input.ID != r.Report.ID || r.Input.ObservationID != r.Report.ObservationID || r.Input.ExpectedSourceHash != r.Report.SourceHash || r.Input.ScenarioID != r.Report.ScenarioID {
		return Receipt{}, ErrInvalid
	}
	actual, e := ReportTx(ctx, tx, s, r.Report.ID)
	if e != nil {
		return Receipt{}, e
	}
	if digest(actual) != digest(r.Report) {
		return Receipt{}, ErrInvalid
	}
	return r, nil
}
func CreateTx(ctx context.Context, tx pgx.Tx, s Scope, in CreateInput, audit AuditFunc) (Receipt, error) {
	if e := authorize(tx, s, true); e != nil {
		return Receipt{}, e
	}
	if !validInput(in) || audit == nil {
		return Receipt{}, ErrInvalid
	}
	if e := lock(ctx, tx, s); e != nil {
		return Receipt{}, e
	}
	prior, e := ReceiptTx(ctx, tx, s, in.OperationID)
	if e == nil {
		if prior.RequestHash != digest(in) {
			return Receipt{}, ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return Receipt{}, e
	}
	// Hidden historical receipts still reserve their operation IDs across grant
	// epochs. Never turn a privacy-preserving read miss into a second insert.
	var reserved bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_asset_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4)`, s.Tenant, s.Domain, s.ActorID, in.OperationID).Scan(&reserved); e != nil {
		return Receipt{}, e
	}
	if reserved {
		return Receipt{}, ErrConflict
	}
	o, e := ObservationTx(ctx, tx, s, in.ObservationID)
	if e != nil {
		return Receipt{}, e
	}
	if o.Hash != in.ExpectedSourceHash {
		return Receipt{}, ErrConflict
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_asset_reports WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND report_id=$4)`, s.Tenant, s.Domain, s.ActorID, in.ID).Scan(&exists); e != nil {
		return Receipt{}, e
	}
	if exists {
		return Receipt{}, ErrConflict
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_asset_reports WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3`, s.Tenant, s.Domain, s.ActorID).Scan(&count); e != nil {
		return Receipt{}, e
	}
	if count >= MaxReports {
		return Receipt{}, ErrLimit
	}
	report, e := assess(s, o, in, time.Now().UTC())
	if e != nil {
		return Receipt{}, e
	}
	r := Receipt{Contract: Contract, Simulation: true, OperationID: in.OperationID, RequestHash: digest(in), Input: in, Report: report}
	rb, e := marshalBounded(report, MaxRecordBytes)
	if e != nil {
		return Receipt{}, e
	}
	b, e := marshalBounded(r, MaxRecordBytes)
	if e != nil {
		return Receipt{}, e
	}
	// Nested transaction gives atomic rollback even when a caller mishandles a
	// callback error and subsequently commits its outer transaction.
	save, e := tx.Begin(ctx)
	if e != nil {
		return Receipt{}, e
	}
	defer save.Rollback(ctx)
	if _, e = save.Exec(ctx, `INSERT INTO adtr.simulation_asset_reports VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, s.ActorID, in.ID, s.Epoch, report.Hash, rb); e != nil {
		return Receipt{}, e
	}
	if _, e = save.Exec(ctx, `INSERT INTO adtr.simulation_asset_receipts VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, s.ActorID, in.OperationID, s.Epoch, r.RequestHash, b); e != nil {
		return Receipt{}, e
	}
	if e = audit(ctx, save, r); e != nil {
		return Receipt{}, e
	}
	if e = save.Commit(ctx); e != nil {
		return Receipt{}, e
	}
	return r, nil
}
