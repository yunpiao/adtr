package simulationrulecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/rulecatalog"
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
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-rule-catalog:"+digest([]string{s.Tenant, s.Domain}))
	return e
}
func coreScope(s Scope) detectionrules.Scope {
	return detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}
}
func marshalBounded(v any, limit int) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, ErrInvalid
	}
	size := len(b)
	quoted, escaped := false, false
	for _, c := range b {
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		} else if c == ':' || c == ',' {
			size++
		}
	}
	if size > limit {
		return nil, ErrLimit
	}
	return b, nil
}
func decode(b []byte, limit int, v any) error {
	if len(b) > limit {
		return ErrLimit
	}
	if json.Unmarshal(b, v) != nil {
		return ErrInvalid
	}
	return nil
}
func mapCore(e error) error {
	switch {
	case errors.Is(e, rulecatalog.ErrConflict):
		return ErrConflict
	case errors.Is(e, rulecatalog.ErrTransition):
		return ErrTransition
	case errors.Is(e, rulecatalog.ErrNotFound):
		return ErrNotFound
	case errors.Is(e, rulecatalog.ErrInvalid), errors.Is(e, detectionrules.ErrInvalid), errors.Is(e, detectionrules.ErrScope):
		return ErrInvalid
	case errors.Is(e, detectionrules.ErrLimit):
		return ErrLimit
	}
	return e
}
func DetailTx(ctx context.Context, tx pgx.Tx, s Scope, id string) (Record, error) {
	var r Record
	if e := authorize(tx, s, false); e != nil {
		return r, e
	}
	if !identifier.MatchString(id) {
		return r, ErrInvalid
	}
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT r.body,r.hash FROM adtr.simulation_rule_catalog_heads h JOIN adtr.simulation_rule_catalog_revisions r USING(tenant_id,domain_id,rule_id,revision) WHERE h.tenant_id=$1 AND h.domain_id=$2 AND h.rule_id=$3`, s.Tenant, s.Domain, id).Scan(&b, &h)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if e = decode(b, MaxRecordBytes, &r); e != nil {
		return r, e
	}
	got := r.Hash
	r.Hash = ""
	want := digest(r)
	r.Hash = got
	if got != h || h != want || r.ID != id || r.Revision != r.Snapshot.Revision || len(r.Snapshot.Entries) != 1 || r.Snapshot.Entries[0].ID != id || r.Contract != Contract || !r.Simulation || r.LearningSupported {
		return Record{}, ErrInvalid
	}
	_, e = rulecatalog.Execute(ctx, rulecatalog.Request{Scope: coreScope(s), Snapshot: &r.Snapshot, Action: "detail", ID: id})
	return r, mapCore(e)
}
func ListTx(ctx context.Context, tx pgx.Tx, s Scope, query string, offset, limit int) (Page, error) {
	p := Page{Items: []Summary{}, Offset: offset, Limit: limit}
	if e := authorize(tx, s, false); e != nil {
		return p, e
	}
	if (query != "" && !validName(query)) || offset < 0 || offset > MaxRules || limit < 1 || limit > MaxPage {
		return p, ErrInvalid
	}
	rows, e := tx.Query(ctx, `SELECT summary FROM adtr.simulation_rule_catalog_heads WHERE tenant_id=$1 AND domain_id=$2 AND strpos(rule_id,$3)>0 ORDER BY rule_id LIMIT 33`, s.Tenant, s.Domain, query)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	all := []Summary{}
	for rows.Next() {
		var b []byte
		var v Summary
		if e = rows.Scan(&b); e != nil {
			return p, e
		}
		if e = decode(b, 4096, &v); e != nil {
			return p, e
		}
		all = append(all, v)
	}
	if e = rows.Err(); e != nil {
		return p, e
	}
	if len(all) > MaxRules {
		return p, ErrLimit
	}
	p.Count = len(all)
	if offset < len(all) {
		end := offset + limit
		if end > len(all) {
			end = len(all)
		}
		p.Items = all[offset:end]
	}
	_, e = marshalBounded(p, MaxResponseBytes)
	return p, e
}
func receipt(ctx context.Context, tx pgx.Tx, s Scope, op string) (Receipt, string, error) {
	var r Receipt
	var b []byte
	var h string
	e := tx.QueryRow(ctx, `SELECT request_hash,body FROM adtr.simulation_rule_catalog_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4`, s.Tenant, s.Domain, s.ActorID, op).Scan(&h, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, "", ErrNotFound
	}
	if e != nil {
		return r, "", e
	}
	e = decode(b, MaxResponseBytes, &r)
	if e == nil && (r.RequestHash != h || digest(r.Intent) != h || r.Intent.OperationID != op || r.Intent.DomainID != s.Domain || r.Intent.Action != r.Action || r.ActorID != s.ActorID || r.OperationID != op || !r.Simulation) {
		e = ErrInvalid
	}
	return r, h, e
}
func ReceiptTx(ctx context.Context, tx pgx.Tx, s Scope, op string) (Receipt, error) {
	if e := authorize(tx, s, false); e != nil {
		return Receipt{}, e
	}
	if !identifier.MatchString(op) {
		return Receipt{}, ErrInvalid
	}
	r, _, e := receipt(ctx, tx, s, op)
	if e == nil && r.Action == "copy-template" && !s.DetectionRead {
		return Receipt{}, ErrDenied
	}
	return r, e
}
func saveReceipt(ctx context.Context, tx pgx.Tx, s Scope, h string, r Receipt, audit AuditFunc) error {
	b, e := marshalBounded(r, MaxResponseBytes)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_rule_catalog_receipts VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, s.ActorID, r.OperationID, h, b)
	if e != nil {
		return e
	}
	return audit(ctx, tx, r)
}
func ExportTx(ctx context.Context, tx pgx.Tx, s Scope, id string) (Export, error) {
	out := Export{Simulation: true, Contract: Contract, ID: id, Publications: []Publication{}}
	if _, e := DetailTx(ctx, tx, s, id); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, `SELECT body FROM adtr.simulation_rule_catalog_publications WHERE tenant_id=$1 AND domain_id=$2 AND rule_id=$3 ORDER BY version LIMIT 9`, s.Tenant, s.Domain, id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var p Publication
		if e = rows.Scan(&b); e != nil {
			return out, e
		}
		if e = decode(b, MaxRecordBytes, &p); e != nil {
			return out, e
		}
		out.Publications = append(out.Publications, p)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Publications) > rulecatalog.MaxPublished {
		return out, ErrLimit
	}
	_, e = marshalBounded(out, MaxResponseBytes)
	return out, e
}
