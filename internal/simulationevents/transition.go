package simulationevents

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
	"time"
)

func requestHash(in TransitionInput) (string, error) {
	// Database time is not request identity: retries may arrive in a later session.
	return hash(struct {
		Dataset, Case, Operation, Actor, Action, Reason string
		Revision                                        uint64
	}{in.DatasetID, in.CaseID, in.OperationID, in.Change.Actor, in.Change.Action, in.Change.Reason, in.Change.ExpectedRevision})
}
func readReceipt(ctx context.Context, tx pgx.Tx, s securityevents.Scope, d Dataset, actor, operation string) (Receipt, string, error) {
	var r Receipt
	var h string
	var b []byte
	err := tx.QueryRow(ctx, `SELECT request_hash,receipt FROM adtr.simulation_event_receipts WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 AND actor=$5 AND operation_id=$6`, s.Tenant, s.Domain, d.ID, d.FixtureHash, actor, operation).Scan(&h, &b)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, h, ErrNotFound
	}
	if err != nil {
		return r, h, err
	}
	if len(b) > 8192 || json.Unmarshal(b, &r) != nil || !r.Simulation || r.Dataset != d || r.DatasetID != d.ID || r.OperationID != operation || r.Audit.Actor != actor || r.Audit.CaseID != r.Case.ID || r.Audit.Revision != r.Case.Revision || r.Audit.State != r.Case.State || r.Audit.Mark != r.Case.Mark || r.Audit.Provenance != provenance(d) {
		return Receipt{}, "", ErrInvalid
	}
	if err = validateAudit(s, provenance(d), r.Audit); err != nil {
		return Receipt{}, "", err
	}
	expectedHash, e := requestHash(TransitionInput{DatasetID: d.ID, CaseID: r.Case.ID, OperationID: operation, Change: securityevents.Change{Actor: actor, Action: r.Audit.Action, Reason: r.Audit.Reason, ExpectedRevision: r.Case.Revision - 1}})
	if e != nil || h != expectedHash {
		return Receipt{}, "", ErrInvalid
	}
	_, err = securityevents.RestoreCase(s, provenance(d), securityevents.CaseSnapshot{ID: r.Case.ID, Tenant: s.Tenant, Domain: s.Domain, State: r.Case.State, Mark: r.Case.Mark, Revision: r.Case.Revision, UpdatedAt: r.Audit.At, Provenance: r.Case.Provenance})
	return r, h, err
}
func OperationTx(ctx context.Context, tx pgx.Tx, s securityevents.Scope, id, actor, operation string) (Receipt, error) {
	if !identifier(actor) || !identifier(operation) {
		return Receipt{}, ErrInvalid
	}
	d, err := getDataset(ctx, tx, s, id)
	if err != nil {
		return Receipt{}, err
	}
	r, _, err := readReceipt(ctx, tx, s, d, actor, operation)
	return r, err
}
func TransitionTx(ctx context.Context, tx pgx.Tx, s securityevents.Scope, in TransitionInput, writeAudit AuditFunc) (Receipt, error) {
	if err := authorize(tx, s, true); err != nil {
		return Receipt{}, err
	}
	if writeAudit == nil || !identifier(in.DatasetID) || !identifier(in.CaseID) || !identifier(in.OperationID) || !identifier(in.Change.Actor) || !identifier(in.Change.Reason) || in.Change.ExpectedRevision < 1 || in.Change.ExpectedRevision > MaxHistory+1 {
		return Receipt{}, ErrInvalid
	}
	d, err := getDataset(ctx, tx, s, in.DatasetID)
	if err != nil {
		return Receipt{}, err
	}
	h, err := requestHash(in)
	if err != nil {
		return Receipt{}, err
	}
	// Serialize a receipt key even when competing requests name different cases.
	key, err := hash([]string{s.Tenant, s.Domain, in.DatasetID, in.Change.Actor, in.OperationID})
	if err != nil {
		return Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-events:"+key); err != nil {
		return Receipt{}, err
	}
	r, oldHash, err := readReceipt(ctx, tx, s, d, in.Change.Actor, in.OperationID)
	if err == nil {
		if oldHash != h {
			return Receipt{}, ErrConflict
		}
		return r, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Receipt{}, err
	}
	detail, err := DetailTx(ctx, tx, s, d.ID, in.CaseID, 0, 1)
	if err != nil {
		return Receipt{}, err
	}
	c, err := loadCase(ctx, tx, s, d, in.CaseID, true)
	if err != nil {
		return Receipt{}, err
	}
	if detail.Case.Revision >= MaxHistory+1 {
		return Receipt{}, ErrLimit
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return Receipt{}, err
	}
	in.Change.At = now.UTC()
	next, a, err := c.Apply(s, in.Change)
	if err != nil {
		return Receipt{}, err
	}
	v, err := next.View(s)
	if err != nil {
		return Receipt{}, err
	}
	r = Receipt{Simulation: true, Dataset: d, DatasetID: d.ID, OperationID: in.OperationID, Case: v, Audit: a}
	tag, err := tx.Exec(ctx, `UPDATE adtr.simulation_event_cases SET state=$6,mark=$7,revision=$8,updated_at=$9 WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 AND case_id=$5 AND revision=$10`, s.Tenant, s.Domain, d.ID, d.FixtureHash, in.CaseID, v.State, v.Mark, int64(v.Revision), a.At, int64(in.Change.ExpectedRevision))
	if err != nil {
		return Receipt{}, err
	}
	if tag.RowsAffected() != 1 {
		return Receipt{}, ErrConflict
	}
	ab, err := json.Marshal(a)
	if err != nil {
		return Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.simulation_event_transitions(tenant_id,domain_id,dataset_id,fixture_hash,case_id,revision,audit) VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Tenant, s.Domain, d.ID, d.FixtureHash, in.CaseID, int64(v.Revision), ab); err != nil {
		return Receipt{}, err
	}
	if err = writeAudit(ctx, tx, r); err != nil {
		return Receipt{}, err
	}
	rb, err := json.Marshal(r)
	if err != nil {
		return Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.simulation_event_receipts(tenant_id,domain_id,dataset_id,fixture_hash,actor,operation_id,case_id,request_hash,receipt) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, s.Tenant, s.Domain, d.ID, d.FixtureHash, in.Change.Actor, in.OperationID, in.CaseID, h, rb); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

// validateAudit reconstructs a validated predecessor and proves that the stored
// audit is exactly one permitted core transition, without granting write access.
func validateAudit(s securityevents.Scope, p securityevents.Provenance, a securityevents.Audit) error {
	if a.Revision < 2 || a.Revision > MaxHistory+1 {
		return ErrInvalid
	}
	previous := securityevents.CaseSnapshot{ID: a.CaseID, Tenant: s.Tenant, Domain: s.Domain, State: a.PreviousState, Mark: a.PreviousMark, Revision: a.Revision - 1, Provenance: p}
	if previous.Revision > 1 {
		previous.UpdatedAt = a.At
	}
	c, err := securityevents.RestoreCase(s, p, previous)
	if err != nil {
		return ErrInvalid
	}
	validation := s
	validation.Manage = true
	_, expected, err := c.Apply(validation, securityevents.Change{Actor: a.Actor, Action: a.Action, Reason: a.Reason, At: a.At, ExpectedRevision: a.Revision - 1})
	if err != nil || expected != a {
		return ErrInvalid
	}
	return nil
}
