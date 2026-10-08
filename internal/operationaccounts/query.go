package operationaccounts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const scopedAccounts = ` FROM adtr.operation_accounts a` + liveParentJoin + ` WHERE a.tenant_id=$1 AND a.domain_id=ANY($2::text[]) AND a.deleted_at IS NULL AND (cardinality($3::text[])=0 OR d.canonical_domain=ANY($3::text[])) AND (position(lower($4) in lower(d.canonical_domain))>0 OR position(lower($4) in lower(a.label))>0)`

func page(total int, f Filter) (Page, int, error) {
	size := f.PageSize
	if size == -1 {
		if total > 1000 {
			return Page{}, 0, problem(422, "result_too_large")
		}
		size = 1000
	}
	if size < 1 || size > 1000 || f.PageIdx < 1 || f.PageIdx > 1000000 {
		return Page{}, 0, problem(400, "invalid_input")
	}
	p := Page{Index: f.PageIdx, Size: f.PageSize, Total: total}
	if total > 0 {
		p.Pages = (total + size - 1) / size
	}
	return p, size, nil
}
func (s *Store) ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f Filter) (List, error) {
	if !s.Available() {
		return List{}, problem(503, "domain_key_unavailable")
	}
	if f.Domains == nil {
		f.Domains = []string{}
	}
	args := []any{tenant, allowed, f.Domains, f.Keyword}
	var count int
	if e := tx.QueryRow(ctx, `SELECT count(*)`+scopedAccounts, args...).Scan(&count); e != nil {
		return List{}, e
	}
	p, size, e := page(count, f)
	if e != nil {
		return List{}, e
	}
	out := List{Page: p, List: []Account{}}
	offset := (f.PageIdx - 1) * size
	args = append(args, size, offset)
	rs, e := tx.Query(ctx, `SELECT `+accountColumns+scopedAccounts+` ORDER BY a.created_at DESC,a.account_id DESC LIMIT $5 OFFSET $6`, args...)
	if e != nil {
		return out, e
	}
	defer rs.Close()
	for rs.Next() {
		v, e := readRow(rs)
		if e != nil {
			return out, e
		}
		out.List = append(out.List, v.Account)
	}
	out.Exhausted = offset+len(out.List) >= count
	return out, rs.Err()
}
func (s *Store) DetailTx(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string) (Account, error) {
	if !s.Available() {
		return Account{}, problem(503, "domain_key_unavailable")
	}
	v, e := getRow(ctx, tx, tenant, id, allowed, false)
	return v.Account, e
}

type ParentDomain struct {
	DomainID string `json:"domainId"`
	Domain   string `json:"domain"`
	Revision string `json:"revision"`
}
type DomainList struct {
	Page      Page           `json:"page"`
	Domains   []ParentDomain `json:"domains"`
	Exhausted bool           `json:"exhausted"`
}

func (s *Store) DomainsTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f Filter) (DomainList, error) {
	if !s.Available() {
		return DomainList{}, problem(503, "domain_key_unavailable")
	}
	const scope = ` FROM adtr.domain_connections d JOIN adtr.resource_domains rd ON rd.tenant_id=d.tenant_id AND rd.id=d.domain_id AND rd.active WHERE d.tenant_id=$1 AND d.domain_id=ANY($2::text[]) AND d.deleted_at IS NULL AND position(lower($3) in lower(d.canonical_domain))>0`
	args := []any{tenant, allowed, f.Keyword}
	var total int
	if e := tx.QueryRow(ctx, `SELECT count(*)`+scope, args...).Scan(&total); e != nil {
		return DomainList{}, e
	}
	p, size, e := page(total, f)
	if e != nil {
		return DomainList{}, e
	}
	out := DomainList{Page: p, Domains: []ParentDomain{}}
	offset := (f.PageIdx - 1) * size
	args = append(args, size, offset)
	rs, e := tx.Query(ctx, `SELECT d.domain_id,d.canonical_domain,d.connection_revision::text`+scope+` ORDER BY d.created_at DESC,d.domain_id DESC LIMIT $4 OFFSET $5`, args...)
	if e != nil {
		return out, e
	}
	defer rs.Close()
	for rs.Next() {
		var d ParentDomain
		if e = rs.Scan(&d.DomainID, &d.Domain, &d.Revision); e != nil {
			return out, e
		}
		out.Domains = append(out.Domains, d)
	}
	out.Exhausted = offset+len(out.Domains) >= total
	return out, rs.Err()
}
