package simulationdetection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"regexp"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func authorize(tx pgx.Tx, s Scope, write bool) error {
	if tx == nil || !identifier.MatchString(s.Tenant) || !identifier.MatchString(s.Domain) || s.ActorID <= 0 || !s.Read || (write && !s.Manage) {
		return ErrDenied
	}
	return nil
}
func lock(ctx context.Context, tx pgx.Tx, s Scope) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-detection:"+digest([]string{s.Tenant, s.Domain}))
	return e
}
func receipt(ctx context.Context, tx pgx.Tx, s Scope, op string) (Receipt, string, error) {
	var r Receipt
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT request_hash,receipt FROM adtr.simulation_detection_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4`, s.Tenant, s.Domain, s.ActorID, op).Scan(&h, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, "", ErrNotFound
	}
	if e != nil {
		return r, "", e
	}
	if json.Unmarshal(b, &r) != nil {
		return r, "", ErrInvalid
	}
	return r, h, nil
}
func ReceiptTx(ctx context.Context, tx pgx.Tx, s Scope, op string) (Receipt, error) {
	if e := authorize(tx, s, false); e != nil {
		return Receipt{}, e
	}
	if !identifier.MatchString(op) {
		return Receipt{}, ErrInvalid
	}
	r, _, e := receipt(ctx, tx, s, op)
	return r, e
}
func saveReceipt(ctx context.Context, tx pgx.Tx, s Scope, h string, r Receipt, a Audit, audit AuditFunc) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_receipts VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, s.ActorID, r.OperationID, h, b); e != nil {
		return e
	}
	return audit(ctx, tx, a)
}

// validateRules binds every declaration to the containing immutable version.
// It validates, rather than rewriting, server-owned scope/contract/provenance.
func validateRules(s Scope, v int64, rules []detectionrules.Rule) error {
	if v < 1 || v > MaxVersions {
		return ErrInvalid
	}
	for _, r := range rules {
		if r.Version != uint64(v) {
			return ErrInvalid
		}
	}
	if _, err := detectionrules.Compile(detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}, rules); err != nil {
		return ErrInvalid
	}
	return nil
}
func version(ctx context.Context, tx pgx.Tx, s Scope, id string, v int64) (Template, error) {
	var t Template
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT hash,body FROM adtr.simulation_detection_versions WHERE tenant_id=$1 AND domain_id=$2 AND template_id=$3 AND version=$4`, s.Tenant, s.Domain, id, v).Scan(&h, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if e != nil {
		return t, e
	}
	if json.Unmarshal(b, &t) != nil || t.Hash != h || t.ID != id || t.Version != v || !t.Simulation {
		return t, ErrInvalid
	}
	if err := validateRules(s, t.Version, t.Rules); err != nil {
		return Template{}, err
	}
	want := t.Hash
	t.Hash = ""
	if digest(t) != want {
		return Template{}, ErrInvalid
	}
	t.Hash = want
	return t, nil
}
func DetailTx(ctx context.Context, tx pgx.Tx, s Scope, id string) (Template, error) {
	if e := authorize(tx, s, false); e != nil {
		return Template{}, e
	}
	if !identifier.MatchString(id) {
		return Template{}, ErrInvalid
	}
	var v int64
	var archived bool
	e := tx.QueryRow(ctx, `SELECT version,archived FROM adtr.simulation_detection_templates WHERE tenant_id=$1 AND domain_id=$2 AND template_id=$3`, s.Tenant, s.Domain, id).Scan(&v, &archived)
	if errors.Is(e, pgx.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if e != nil {
		return Template{}, e
	}
	t, e := version(ctx, tx, s, id, v)
	t.Archived = archived
	return t, e
}
func ListTx(ctx context.Context, tx pgx.Tx, s Scope) ([]Template, error) {
	if e := authorize(tx, s, false); e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT template_id FROM adtr.simulation_detection_templates WHERE tenant_id=$1 AND domain_id=$2 ORDER BY template_id LIMIT 101`, s.Tenant, s.Domain)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if len(ids) > MaxTemplates {
		return nil, ErrLimit
	}
	out := []Template{}
	for _, id := range ids {
		t, e := DetailTx(ctx, tx, s, id)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, nil
}
func MutateTx(ctx context.Context, tx pgx.Tx, s Scope, in Mutation, audit AuditFunc) (Template, error) {
	if e := authorize(tx, s, true); e != nil {
		return Template{}, e
	}
	if audit == nil || !identifier.MatchString(in.ID) || !identifier.MatchString(in.OperationID) || in.ExpectedVersion < 0 || in.ExpectedVersion > MaxVersions {
		return Template{}, ErrInvalid
	}
	if !in.Archive {
		if len(in.Name) == 0 || len(in.Name) > 128 {
			return Template{}, ErrInvalid
		}
		if e := validateRules(s, in.ExpectedVersion+1, in.Rules); e != nil {
			return Template{}, ErrInvalid
		}
		if b, _ := json.Marshal(in); len(b) > 60000 {
			return Template{}, ErrLimit
		}
	} else if in.Name != "" || len(in.Rules) != 0 || in.ExpectedVersion == 0 {
		return Template{}, ErrInvalid
	}
	if e := lock(ctx, tx, s); e != nil {
		return Template{}, e
	}
	h := digest(in)
	r, old, e := receipt(ctx, tx, s, in.OperationID)
	if e == nil {
		if old != h || r.Template == nil {
			return Template{}, ErrConflict
		}
		return *r.Template, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return Template{}, e
	}
	current, e := DetailTx(ctx, tx, s, in.ID)
	exists := e == nil
	if e != nil && !errors.Is(e, ErrNotFound) {
		return Template{}, e
	}
	if exists && (current.Archived || current.Version != in.ExpectedVersion) || !exists && in.ExpectedVersion != 0 {
		return Template{}, ErrConflict
	}
	action := "create"
	t := Template{ID: in.ID, Name: in.Name, Version: in.ExpectedVersion + 1, Rules: in.Rules, Simulation: true}
	if in.Archive {
		action = "archive"
		t = current
		t.Archived = true
		_, e = tx.Exec(ctx, `UPDATE adtr.simulation_detection_templates SET archived=true WHERE tenant_id=$1 AND domain_id=$2 AND template_id=$3 AND version=$4 AND NOT archived`, s.Tenant, s.Domain, in.ID, in.ExpectedVersion)
	} else {
		if t.Version > MaxVersions {
			return Template{}, ErrLimit
		}
		t.Hash = digest(t)
		if exists {
			action = "update"
			_, e = tx.Exec(ctx, `UPDATE adtr.simulation_detection_templates SET version=$4 WHERE tenant_id=$1 AND domain_id=$2 AND template_id=$3 AND version=$5 AND NOT archived`, s.Tenant, s.Domain, in.ID, t.Version, in.ExpectedVersion)
		} else {
			var count int
			e = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_detection_templates WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain).Scan(&count)
			if e != nil {
				return Template{}, e
			}
			if count >= MaxTemplates {
				return Template{}, ErrLimit
			}
			_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_templates VALUES($1,$2,$3,1,false)`, s.Tenant, s.Domain, in.ID)
		}
		if e == nil {
			b, _ := json.Marshal(t)
			_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_versions VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, t.ID, t.Version, t.Hash, b)
		}
	}
	if e != nil {
		return Template{}, e
	}
	r = Receipt{OperationID: in.OperationID, Kind: action, Template: &t}
	e = saveReceipt(ctx, tx, s, h, r, Audit{Action: action, ObjectID: t.ID, OperationID: in.OperationID, Version: t.Version, Hash: t.Hash}, audit)
	return t, e
}
