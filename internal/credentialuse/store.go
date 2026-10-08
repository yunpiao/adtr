package credentialuse

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Callers hold the schema -> tenant -> actor locks and current normal authority.
// Scope comes from the authenticated actor, never a request field. All errors
// require rollback, including failures after proof consumption or audit writes.
const accountSelect = `SELECT a.account_id,a.domain_id,d.canonical_domain,a.label,a.revision::text,a.credential_revision::text
 FROM adtr.operation_accounts a JOIN adtr.domain_connections d ON d.tenant_id=a.tenant_id AND d.domain_id=a.domain_id
 JOIN adtr.resource_domains rd ON rd.tenant_id=a.tenant_id AND rd.id=a.domain_id
 WHERE a.tenant_id=$1 AND a.account_id=$2 AND a.domain_id=ANY($3::text[]) AND a.deleted_at IS NULL AND d.deleted_at IS NULL AND rd.active`

func notFound(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return problem(404, "not_found")
	}
	return e
}
func readAccount(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string, lock bool) (Account, error) {
	var a Account
	q := accountSelect
	if lock {
		q += ` FOR UPDATE OF a,d`
	}
	e := tx.QueryRow(ctx, q, tenant, id, allowed).Scan(&a.AccountID, &a.DomainID, &a.Domain, &a.Label, &a.Revision, &a.CredentialRevision)
	return a, notFound(e)
}
func (s *Store) AccountsTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f Filter) (AccountList, error) {
	return s.AccountsForPurposeTx(ctx, tx, tenant, allowed, f, Purpose)
}

// AccountsForPurposeTx requires the caller to select the exact authorized purpose.
// It does not grant authority or enable a consumer. Legacy callers use AccountsTx.
func (s *Store) AccountsForPurposeTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f Filter, purpose string) (AccountList, error) {
	if err := validatePurpose(purpose); err != nil {
		return AccountList{}, err
	}
	out := AccountList{ConsumerEnabled: consumerEnabledForPurpose(purpose), List: []Account{}}
	if f.PageIdx < 1 || f.PageIdx > 1000000 || f.PageSize < 10 || f.PageSize > 50 || f.PageSize%10 != 0 {
		return out, problem(400, "invalid_input")
	}
	const scope = ` FROM adtr.operation_accounts a JOIN adtr.domain_connections d ON d.tenant_id=a.tenant_id AND d.domain_id=a.domain_id
 JOIN adtr.resource_domains rd ON rd.tenant_id=a.tenant_id AND rd.id=a.domain_id
 WHERE a.tenant_id=$1 AND a.domain_id=ANY($2::text[]) AND a.deleted_at IS NULL AND d.deleted_at IS NULL AND rd.active
 AND (position(lower($3) in lower(a.label))>0 OR position(lower($3) in lower(d.canonical_domain))>0)
 AND EXISTS(SELECT FROM adtr.operation_account_use_grants g WHERE g.tenant_id=a.tenant_id AND g.domain_id=a.domain_id AND g.account_id=a.account_id AND g.purpose=$4 AND g.allowed)`
	var total int
	if e := tx.QueryRow(ctx, `SELECT count(*)`+scope, tenant, allowed, f.Keyword, purpose).Scan(&total); e != nil {
		return out, e
	}
	out.Page = Page{Index: f.PageIdx, Size: f.PageSize, Total: total}
	if total > 0 {
		out.Page.Pages = (total + f.PageSize - 1) / f.PageSize
	}
	offset := (f.PageIdx - 1) * f.PageSize
	rs, e := tx.Query(ctx, `SELECT a.account_id,a.domain_id,d.canonical_domain,a.label,a.revision::text,a.credential_revision::text`+scope+` ORDER BY a.created_at DESC,a.account_id DESC LIMIT $5 OFFSET $6`, tenant, allowed, f.Keyword, purpose, f.PageSize, offset)
	if e != nil {
		return out, e
	}
	defer rs.Close()
	for rs.Next() {
		var a Account
		if e = rs.Scan(&a.AccountID, &a.DomainID, &a.Domain, &a.Label, &a.Revision, &a.CredentialRevision); e != nil {
			return out, e
		}
		out.List = append(out.List, a)
	}
	out.Exhausted = offset+len(out.List) >= total
	return out, rs.Err()
}
func (s *Store) GrantsTx(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string) (GrantList, error) {
	return s.GrantsForPurposeTx(ctx, tx, tenant, id, allowed, Purpose)
}

// GrantsForPurposeTx requires the caller to select the exact authorized purpose.
// It does not grant authority or enable a consumer. Legacy callers use GrantsTx.
func (s *Store) GrantsForPurposeTx(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string, purpose string) (GrantList, error) {
	if err := validatePurpose(purpose); err != nil {
		return GrantList{}, err
	}
	out := GrantList{ConsumerEnabled: consumerEnabledForPurpose(purpose), Grants: []Grant{}}
	a, e := readAccount(ctx, tx, tenant, id, allowed, false)
	if e != nil {
		return out, e
	}
	out.Account = a
	rs, e := tx.Query(ctx, `SELECT g.role_id,COALESCE(r.name,g.role_id),g.purpose,g.allowed,g.grant_revision::text,g.account_credential_revision::text,g.updated_at
 FROM adtr.operation_account_use_grants g LEFT JOIN adtr.access_roles r ON r.tenant_id=g.tenant_id AND r.id=g.role_id
 WHERE g.tenant_id=$1 AND g.domain_id=$2 AND g.account_id=$3 AND g.purpose=$4 ORDER BY g.role_id LIMIT 1001`, tenant, a.DomainID, id, purpose)
	if e != nil {
		return out, e
	}
	defer rs.Close()
	for rs.Next() {
		var g Grant
		if e = rs.Scan(&g.RoleID, &g.RoleName, &g.Purpose, &g.Allowed, &g.GrantRevision, &g.AccountCredentialRevision, &g.UpdatedAt); e != nil {
			return out, e
		}
		g.UpdatedAt = g.UpdatedAt.UTC()
		out.Grants = append(out.Grants, g)
	}
	if e = rs.Err(); e != nil {
		return out, e
	}
	if len(out.Grants) > 1000 {
		return GrantList{}, problem(422, "result_too_large")
	}
	return out, nil
}
func (s *Store) RolesTx(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string) (RoleList, error) {
	return s.RolesForPurposeTx(ctx, tx, tenant, id, allowed, Purpose)
}

// RolesForPurposeTx requires the caller to select the exact authorized purpose.
// It does not grant authority or enable a consumer. Legacy callers use RolesTx.
func (s *Store) RolesForPurposeTx(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string, purpose string) (RoleList, error) {
	if err := validatePurpose(purpose); err != nil {
		return RoleList{}, err
	}
	out := RoleList{ConsumerEnabled: consumerEnabledForPurpose(purpose), Roles: []Role{}}
	a, e := readAccount(ctx, tx, tenant, id, allowed, false)
	if e != nil {
		return out, e
	}
	query := `WITH roles AS (SELECT 'platform_admin'::text id,'platform_admin'::text name UNION ALL SELECT id,name FROM adtr.access_roles WHERE tenant_id=$1)
 SELECT r.id,r.name,(SELECT count(*) FROM adtr.users u WHERE u.tenant_id=$1 AND COALESCE(NULLIF(u.role_id,''),u.role)=r.id)
 FROM roles r WHERE adtr.credential_use_role_eligible($1,$2,r.id) ORDER BY r.id LIMIT 1001`
	args := []any{tenant, a.DomainID}
	if isDirectoryPurpose(purpose) {
		query = strings.Replace(query, "adtr.credential_use_role_eligible($1,$2,r.id)", "adtr.credential_use_role_eligible_for_purpose($1,$2,r.id,$3)", 1)
		args = append(args, purpose)
	}
	rs, e := tx.Query(ctx, query, args...)
	if e != nil {
		return out, e
	}
	defer rs.Close()
	for rs.Next() {
		var r Role
		if e = rs.Scan(&r.RoleID, &r.RoleName, &r.MemberCount); e != nil {
			return out, e
		}
		out.Roles = append(out.Roles, r)
	}
	if e = rs.Err(); e != nil {
		return out, e
	}
	if len(out.Roles) > 1000 {
		return RoleList{}, problem(422, "result_too_large")
	}
	return out, nil
}
func (s *Store) EffectiveTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, role, id string, allowed []string) (Effective, error) {
	return s.EffectiveForPurposeTx(ctx, tx, p, role, id, allowed, Purpose)
}

// EffectiveForPurposeTx requires the caller to select the exact authorized purpose.
// It does not grant authority or enable a consumer. Legacy callers use EffectiveTx.
func (s *Store) EffectiveForPurposeTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, role, id string, allowed []string, purpose string) (Effective, error) {
	if err := validatePurpose(purpose); err != nil {
		return Effective{}, err
	}
	a, e := readAccount(ctx, tx, p.TenantID, id, allowed, false)
	if e != nil {
		return Effective{}, e
	}
	out := Effective{ConsumerEnabled: consumerEnabledForPurpose(purpose), AccountID: a.AccountID, DomainID: a.DomainID, Purpose: purpose, GrantRevision: "0", AccountCredentialRevision: a.CredentialRevision}
	query := `SELECT COALESCE(g.allowed,false),COALESCE(g.grant_revision::text,'0'),
 COALESCE(g.allowed AND g.account_credential_revision=a.credential_revision,false) AND adtr.credential_use_role_eligible(a.tenant_id,a.domain_id,$3)
 AND EXISTS(SELECT FROM adtr.users u WHERE u.tenant_id=a.tenant_id AND u.id=$4 AND COALESCE(NULLIF(u.role_id,''),u.role)=$3 AND NOT u.disabled AND NOT u.must_change AND u.password_updated_at>clock_timestamp()-interval '90 days')
 FROM adtr.operation_accounts a JOIN adtr.users principal ON principal.tenant_id=a.tenant_id AND principal.id=$4 AND COALESCE(NULLIF(principal.role_id,''),principal.role)=$3
 LEFT JOIN adtr.operation_account_use_grants g ON g.tenant_id=a.tenant_id AND g.domain_id=a.domain_id AND g.account_id=a.account_id AND g.role_id=$3 AND g.purpose=$5
 WHERE a.tenant_id=$1 AND a.account_id=$2`
	if isDirectoryPurpose(purpose) {
		query = strings.Replace(query, "adtr.credential_use_role_eligible(a.tenant_id,a.domain_id,$3)", "adtr.credential_use_role_eligible_for_purpose(a.tenant_id,a.domain_id,$3,$5)", 1)
	}
	e = tx.QueryRow(ctx, query, p.TenantID, id, role, p.ActorID, purpose).Scan(&out.ExplicitlyGranted, &out.GrantRevision, &out.Eligible)
	return out, notFound(e)
}
func (s *Store) ReceiptTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, key string, allowed []string) (Receipt, error) {
	return s.ReceiptForPurposeTx(ctx, tx, p, key, allowed, Purpose)
}

// ReceiptForPurposeTx requires the caller to select the exact authorized purpose.
// It does not grant authority or enable a consumer. Legacy callers use ReceiptTx.
func (s *Store) ReceiptForPurposeTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, key string, allowed []string, purpose string) (Receipt, error) {
	if err := validatePurpose(purpose); err != nil {
		return Receipt{}, err
	}
	var r Receipt
	e := tx.QueryRow(ctx, `SELECT m.operation,m.account_id,m.domain_id,m.role_id,m.purpose,m.grant_revision::text,m.account_credential_revision::text,m.allowed,
 a.deleted_at IS NOT NULL,COALESCE(g.grant_revision::text,'0'),COALESCE(g.allowed,false)
 FROM adtr.operation_account_use_mutations m JOIN adtr.operation_accounts a ON a.tenant_id=m.tenant_id AND a.domain_id=m.domain_id AND a.account_id=m.account_id
 LEFT JOIN adtr.operation_account_use_grants g ON g.tenant_id=m.tenant_id AND g.domain_id=m.domain_id AND g.account_id=m.account_id AND g.role_id=m.role_id AND g.purpose=m.purpose
 WHERE m.tenant_id=$1 AND m.actor_id=$2 AND m.idempotency_key=$3 AND m.domain_id=ANY($4::text[]) AND m.purpose=$5`, p.TenantID, p.ActorID, key, allowed, purpose).Scan(&r.Operation, &r.AccountID, &r.DomainID, &r.RoleID, &r.Purpose, &r.GrantRevision, &r.AccountCredentialRevision, &r.Allowed, &r.AccountDeleted, &r.CurrentGrantRevision, &r.CurrentAllowed)
	r.Result = "SUCCESS"
	r.Replayed = true
	return r, notFound(e)
}

// ReplayTx is called after current authentication/admin/scope checks and before
// fresh proof. It neither changes state nor renews any saved authority.
func (s *Store) ReplayTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, path string, in Input, allowed []string) (*Receipt, error) {
	return s.ReplayForPurposeTx(ctx, tx, p, path, in, allowed, Purpose)
}

// ReplayForPurposeTx requires the caller to select the exact authorized purpose.
// It does not grant authority or enable a consumer. Legacy callers use ReplayTx.
func (s *Store) ReplayForPurposeTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, path string, in Input, allowed []string, purpose string) (*Receipt, error) {
	if e := ValidateInputForPurpose(path, &in, purpose); e != nil {
		return nil, e
	}
	var prior, op, priorPurpose string
	e := tx.QueryRow(ctx, `SELECT operation,fingerprint,purpose FROM adtr.operation_account_use_mutations WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, p.TenantID, p.ActorID, in.IdempotencyKey).Scan(&op, &prior, &priorPurpose)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	r, e := s.ReceiptForPurposeTx(ctx, tx, p, in.IdempotencyKey, allowed, priorPurpose)
	if e != nil {
		return nil, e
	}
	if priorPurpose != purpose || op != strings.TrimPrefix(path, "/") || subtle.ConstantTimeCompare([]byte(prior), []byte(fingerprint(p, path, in))) != 1 {
		return nil, problem(409, "idempotency_conflict")
	}
	return &r, nil
}
func (s *Store) MutateTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, path string, in Input, allowed []string) (Receipt, error) {
	return s.MutateForPurposeTx(ctx, tx, p, path, in, allowed, Purpose)
}

// MutateForPurposeTx requires the caller to select the exact authorized purpose.
// It does not grant authority or enable a consumer. Legacy callers use MutateTx.
func (s *Store) MutateForPurposeTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, path string, in Input, allowed []string, purpose string) (Receipt, error) {
	if e := ValidateInputForPurpose(path, &in, purpose); e != nil {
		return Receipt{}, e
	}
	if r, e := s.ReplayForPurposeTx(ctx, tx, p, path, in, allowed, purpose); e != nil {
		return Receipt{}, e
	} else if r != nil {
		return *r, nil
	}
	a, e := readAccount(ctx, tx, p.TenantID, in.AccountID, allowed, true)
	if e != nil {
		return Receipt{}, e
	}
	// Do not expose revisions for a foreign/missing target or ungranted revoke.
	var exists bool
	e = tx.QueryRow(ctx, `SELECT $2='platform_admin' OR $2='viewer' OR EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=$1 AND id=$2)`, p.TenantID, in.RoleID).Scan(&exists)
	if e != nil {
		return Receipt{}, e
	}
	if !exists {
		return Receipt{}, problem(404, "not_found")
	}
	var revision, bound string
	var current bool
	e = tx.QueryRow(ctx, `SELECT grant_revision::text,account_credential_revision::text,allowed FROM adtr.operation_account_use_grants WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND role_id=$4 AND purpose=$5 FOR UPDATE`, p.TenantID, a.DomainID, in.AccountID, in.RoleID, in.Purpose).Scan(&revision, &bound, &current)
	if errors.Is(e, pgx.ErrNoRows) {
		revision = "0"
		bound = a.CredentialRevision
		if path == "/revoke" {
			return Receipt{}, problem(404, "not_found")
		}
	} else if e != nil {
		return Receipt{}, e
	}
	if a.Revision != in.ExpectedAccountRevision || a.CredentialRevision != in.ExpectedCredentialRevision || revision != in.ExpectedGrantRevision {
		return Receipt{}, problem(409, "revision_conflict")
	}
	if path == "/grant" {
		var eligible bool
		query := `SELECT adtr.credential_use_role_eligible($1,$2,$3)`
		args := []any{p.TenantID, a.DomainID, in.RoleID}
		if isDirectoryPurpose(purpose) {
			query = `SELECT adtr.credential_use_role_eligible_for_purpose($1,$2,$3,$4)`
			args = append(args, purpose)
		}
		if e = tx.QueryRow(ctx, query, args...).Scan(&eligible); e != nil {
			return Receipt{}, e
		}
		if !eligible {
			return Receipt{}, problem(422, "credential_use_target_unavailable")
		}
	}
	wanted := path == "/grant"
	previous := revision
	if revision == "0" {
		e = tx.QueryRow(ctx, `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by) VALUES($1,$2,$3,$4,$5,$6,true,$7) RETURNING grant_revision::text,account_credential_revision::text`, p.TenantID, a.DomainID, a.AccountID, in.Purpose, in.RoleID, a.CredentialRevision, p.ActorID).Scan(&revision, &bound)
	} else if current != wanted || wanted && bound != a.CredentialRevision {
		if wanted {
			bound = a.CredentialRevision
		}
		e = tx.QueryRow(ctx, `UPDATE adtr.operation_account_use_grants SET allowed=$6,account_credential_revision=$7,updated_by=$8 WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND purpose=$4 AND role_id=$5 RETURNING grant_revision::text`, p.TenantID, a.DomainID, a.AccountID, in.Purpose, in.RoleID, wanted, bound, p.ActorID).Scan(&revision)
	}
	if e != nil {
		return Receipt{}, e
	}
	op := strings.TrimPrefix(path, "/")
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.operation_account_use_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, p.TenantID, p.ActorID, in.IdempotencyKey, op, fingerprint(p, path, in), a.DomainID, a.AccountID, in.RoleID, in.Purpose, revision, bound, wanted); e != nil {
		return Receipt{}, e
	}
	// Effective changes audit in their SQL trigger. A no-op receipt still records
	// its explicit administrative decision without changing epochs or revisions.
	if previous == revision {
		result := "saved"
		if !wanted {
			result = "revoked"
		}
		if _, e = tx.Exec(ctx, `INSERT INTO adtr.operation_account_use_audit(tenant_id,actor_id,domain_id,account_id,role_id,purpose,action,old_grant_revision,new_grant_revision,account_credential_revision,allowed,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,$10,$11)`, p.TenantID, p.ActorID, a.DomainID, a.AccountID, in.RoleID, in.Purpose, "credential_use_"+op, revision, bound, wanted, result); e != nil {
			return Receipt{}, e
		}
	}
	return Receipt{Result: "SUCCESS", Operation: op, AccountID: a.AccountID, DomainID: a.DomainID, RoleID: in.RoleID, Purpose: in.Purpose, GrantRevision: revision, AccountCredentialRevision: bound, Allowed: wanted, CurrentGrantRevision: revision, CurrentAllowed: wanted}, nil
}
