package operationaccounts

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Tx callers must already hold schema -> tenant -> actor locks, live function
// authorization and current tenant eligibility. All errors require rollback.
// Scope is intersected again here; it never comes from client input.
type row struct {
	Account
	revision, credential int64
	deleted              *time.Time
}

const accountColumns = `a.account_id,a.domain_id,d.canonical_domain,a.label,a.revision,a.credential_revision,a.created_at,a.updated_at,a.deleted_at`
const liveParentJoin = ` JOIN adtr.domain_connections d ON d.tenant_id=a.tenant_id AND d.domain_id=a.domain_id AND d.deleted_at IS NULL JOIN adtr.resource_domains rd ON rd.tenant_id=d.tenant_id AND rd.id=d.domain_id AND rd.active `

func readRow(r pgx.Row) (row, error) {
	var v row
	e := r.Scan(&v.AccountID, &v.DomainID, &v.Domain, &v.Label, &v.revision, &v.credential, &v.CreatedAt, &v.UpdatedAt, &v.deleted)
	v.Revision = strconv.FormatInt(v.revision, 10)
	v.CredentialRevision = strconv.FormatInt(v.credential, 10)
	v.CreatedAt = v.CreatedAt.UTC()
	v.UpdatedAt = v.UpdatedAt.UTC()
	v.StorageState = "saved"
	v.VerificationState = "unverified"
	v.CredentialConfigured = v.deleted == nil
	return v, e
}
func notFound(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return problem(404, "not_found")
	}
	return e
}
func (s *Store) ScopeAccountTx(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string) (string, error) {
	var domain string
	e := tx.QueryRow(ctx, `SELECT a.domain_id FROM adtr.operation_accounts a`+liveParentJoin+`WHERE a.tenant_id=$1 AND a.account_id=$2 AND a.domain_id=ANY($3::text[])`, tenant, id, allowed).Scan(&domain)
	return domain, notFound(e)
}
func lockDomain(ctx context.Context, tx pgx.Tx, tenant, domain string, allowed []string) (string, error) {
	var revision string
	e := tx.QueryRow(ctx, `SELECT d.connection_revision::text FROM adtr.domain_connections d JOIN adtr.resource_domains rd ON rd.tenant_id=d.tenant_id AND rd.id=d.domain_id AND rd.active WHERE d.tenant_id=$1 AND d.domain_id=$2 AND d.domain_id=ANY($3::text[]) AND d.deleted_at IS NULL FOR UPDATE OF d`, tenant, domain, allowed).Scan(&revision)
	return revision, notFound(e)
}
func getRow(ctx context.Context, tx pgx.Tx, tenant, id string, allowed []string, lock bool) (row, error) {
	q := `SELECT ` + accountColumns + ` FROM adtr.operation_accounts a` + liveParentJoin + `WHERE a.tenant_id=$1 AND a.account_id=$2 AND a.domain_id=ANY($3::text[]) AND a.deleted_at IS NULL`
	if lock {
		q += ` FOR UPDATE OF a`
	}
	v, e := readRow(tx.QueryRow(ctx, q, tenant, id, allowed))
	return v, notFound(e)
}
func (s *Store) ReceiptTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, key string, allowed []string) (Receipt, error) {
	var r Receipt
	e := tx.QueryRow(ctx, `SELECT m.operation,m.account_id,m.domain_id,m.revision::text,m.credential_revision::text,a.deleted_at IS NOT NULL,a.revision::text,a.credential_revision::text
 FROM adtr.operation_account_mutations m JOIN adtr.operation_accounts a ON a.tenant_id=m.tenant_id AND a.domain_id=m.domain_id AND a.account_id=m.account_id`+liveParentJoin+`
 WHERE m.tenant_id=$1 AND m.actor_id=$2 AND m.idempotency_key=$3 AND a.domain_id=ANY($4::text[])`, p.TenantID, p.ActorID, key, allowed).Scan(&r.Operation, &r.AccountID, &r.DomainID, &r.Revision, &r.CredentialRevision, &r.Deleted, &r.CurrentRevision, &r.CurrentCredentialRevision)
	r.Result = "SUCCESS"
	r.VerificationState = "unverified"
	r.Replayed = true
	return r, notFound(e)
}
func (s *Store) fingerprint(p tasks.Principal, operation string, in Input) (string, error) {
	if !s.Available() {
		return "", problem(503, "domain_key_unavailable")
	}
	// Typed field order is canonical. Omitted vs supplied optional fields remain
	// distinct, except a new account's omitted label is the same as an empty one.
	label := in.Label
	if operation == "create" && label == nil {
		empty := ""
		label = &empty
	}
	raw, e := json.Marshal(struct {
		Operation, Tenant, IdempotencyKey                                               string
		Actor                                                                           int64
		DomainID, AccountID, ExpectedDomainRevision, ExpectedRevision, ConfirmAccountID string
		Label, Username, Password                                                       *string
	}{operation, p.TenantID, in.IdempotencyKey, p.ActorID, in.DomainID, in.AccountID, in.ExpectedDomainRevision, in.ExpectedRevision, in.ConfirmAccountID, label, in.Username, in.Password})
	if e != nil {
		return "", e
	}
	defer clear(raw)
	v, e := s.runtime.Vault().Fingerprint("operation-account-"+operation+"-v1", p.TenantID, raw)
	if e != nil {
		return "", problem(503, "domain_key_unavailable")
	}
	return v, nil
}
func (s *Store) replay(ctx context.Context, tx pgx.Tx, p tasks.Principal, in Input, operation, fingerprint string, allowed []string) (Receipt, bool, error) {
	var prior, kind string
	e := tx.QueryRow(ctx, `SELECT operation,fingerprint FROM adtr.operation_account_mutations WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, p.TenantID, p.ActorID, in.IdempotencyKey).Scan(&kind, &prior)
	if errors.Is(e, pgx.ErrNoRows) {
		return Receipt{}, false, nil
	}
	if e != nil {
		return Receipt{}, false, e
	}
	// Current receipt scope is checked before comparing the private fingerprint.
	r, e := s.ReceiptTx(ctx, tx, p, in.IdempotencyKey, allowed)
	if e != nil {
		return Receipt{}, false, e
	}
	if kind != operation || subtle.ConstantTimeCompare([]byte(prior), []byte(fingerprint)) != 1 {
		return Receipt{}, false, problem(409, "idempotency_conflict")
	}
	return r, true, nil
}
func (s *Store) seal(tenant, domain, account string, revision int64, in Input) (domainconfig.OperationSealedCredential, error) {
	raw, e := json.Marshal(struct {
		Version  int    `json:"version"`
		Username string `json:"username"`
		Password string `json:"password"`
	}{2, *in.Username, *in.Password})
	if e != nil {
		return domainconfig.OperationSealedCredential{}, e
	}
	defer clear(raw)
	v, e := s.runtime.Vault().SealOperationCredential(tenant, domain, account, revision, raw)
	if e != nil {
		return v, problem(503, "credential_unavailable")
	}
	return v, nil
}
func saveCredential(ctx context.Context, tx pgx.Tx, p tasks.Principal, domain, id string, revision int64, sealed domainconfig.OperationSealedCredential) error {
	if revision == 1 {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) VALUES($1,$2,$3,$4,$5,$6)`, p.TenantID, domain, id, revision, sealed.KeyID, sealed.Ciphertext)
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE adtr.operation_account_credentials SET credential_revision=$4,key_id=$5,ciphertext=$6,created_at=clock_timestamp() WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND credential_revision=$4-1`, p.TenantID, domain, id, revision, sealed.KeyID, sealed.Ciphertext)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return problem(503, "credential_unavailable")
	}
	return nil
}
func finish(ctx context.Context, tx pgx.Tx, p tasks.Principal, in Input, operation, fp, domain, id string, old, next, oldCred, nextCred int64) (Receipt, error) {
	_, e := tx.Exec(ctx, `INSERT INTO adtr.operation_account_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,revision,credential_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.TenantID, p.ActorID, in.IdempotencyKey, operation, fp, domain, id, next, nextCred)
	if e != nil {
		return Receipt{}, e
	}
	result := "saved"
	if operation == "delete" {
		result = "deleted"
	}
	_, e = tx.Exec(ctx, `INSERT INTO adtr.operation_account_audit(tenant_id,actor_id,domain_id,account_id,action,old_revision,new_revision,old_credential_revision,new_credential_revision,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, p.TenantID, p.ActorID, domain, id, "operation_account_"+operation, old, next, oldCred, nextCred, result)
	if e != nil {
		return Receipt{}, e
	}
	rev, cred := strconv.FormatInt(next, 10), strconv.FormatInt(nextCred, 10)
	return Receipt{Result: "SUCCESS", Operation: operation, AccountID: id, DomainID: domain, Revision: rev, CredentialRevision: cred, VerificationState: "unverified", Deleted: operation == "delete", CurrentRevision: rev, CurrentCredentialRevision: cred}, nil
}
func (s *Store) MutateTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, path string, in Input, allowed []string) (Receipt, error) {
	if e := ValidateInput(path, &in); e != nil {
		return Receipt{}, e
	}
	operation := strings.TrimPrefix(path, "/")
	fp, e := s.fingerprint(p, operation, in)
	if e != nil {
		return Receipt{}, e
	}
	// Authentication and proof have already happened. Replays precede obsolete
	// CAS/deleted checks, while both requested and original scopes remain live.
	if r, found, e := s.replay(ctx, tx, p, in, operation, fp, allowed); e != nil || found {
		return r, e
	}
	domain := in.DomainID
	if operation != "create" {
		domain, e = s.ScopeAccountTx(ctx, tx, p.TenantID, in.AccountID, allowed)
		if e != nil {
			return Receipt{}, e
		}
	}
	parentRevision, e := lockDomain(ctx, tx, p.TenantID, domain, allowed)
	if e != nil {
		return Receipt{}, e
	}
	if operation == "create" {
		if parentRevision != in.ExpectedDomainRevision {
			return Receipt{}, problem(409, "revision_conflict")
		}
		var b [18]byte
		if _, e = rand.Read(b[:]); e != nil {
			return Receipt{}, e
		}
		id := base64.RawURLEncoding.EncodeToString(b[:])
		sealed, e := s.seal(p.TenantID, domain, id, 1, in)
		if e != nil {
			return Receipt{}, e
		}
		defer clear(sealed.Ciphertext)
		label := ""
		if in.Label != nil {
			label = *in.Label
		}
		if _, e = tx.Exec(ctx, `INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES($1,$2,$3,$4)`, p.TenantID, domain, id, label); e != nil {
			return Receipt{}, e
		}
		if e = saveCredential(ctx, tx, p, domain, id, 1, sealed); e != nil {
			return Receipt{}, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES($1,$2,'operation_accounts',$3)`, p.TenantID, domain, id); e != nil {
			return Receipt{}, e
		}
		return finish(ctx, tx, p, in, operation, fp, domain, id, 0, 1, 0, 1)
	}
	v, e := getRow(ctx, tx, p.TenantID, in.AccountID, allowed, true)
	if e != nil {
		return Receipt{}, e
	}
	if v.Revision != in.ExpectedRevision {
		return Receipt{}, problem(409, "revision_conflict")
	}
	changePair := in.Username != nil || operation == "delete"
	if v.revision == math.MaxInt64 || changePair && v.credential == math.MaxInt64 {
		return Receipt{}, problem(409, "revision_exhausted")
	}
	label := v.Label
	if in.Label != nil {
		label = *in.Label
	}
	if operation == "update" && !changePair && label == v.Label {
		return Receipt{}, problem(409, "no_change")
	}
	if changePair {
		var used bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3)`, p.TenantID, domain, v.AccountID).Scan(&used); e != nil {
			return Receipt{}, e
		}
		if used {
			return Receipt{}, problem(409, "account_in_use")
		}
	}
	nextCred := v.credential
	if changePair {
		nextCred++
	}
	tag, e := tx.Exec(ctx, `UPDATE adtr.operation_accounts SET label=$4,revision=revision+1,credential_revision=$5,updated_at=clock_timestamp(),deleted_at=CASE WHEN $6 THEN clock_timestamp() ELSE NULL END WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND revision=$7 AND deleted_at IS NULL`, p.TenantID, domain, v.AccountID, label, nextCred, operation == "delete", v.revision)
	if e != nil {
		return Receipt{}, e
	}
	if tag.RowsAffected() != 1 {
		return Receipt{}, problem(409, "revision_conflict")
	}
	if operation == "delete" {
		if _, e = tx.Exec(ctx, `DELETE FROM adtr.operation_account_credentials WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3`, p.TenantID, domain, v.AccountID); e != nil {
			return Receipt{}, e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM adtr.domain_dependencies WHERE tenant_id=$1 AND domain_id=$2 AND module='operation_accounts' AND object_id=$3`, p.TenantID, domain, v.AccountID); e != nil {
			return Receipt{}, e
		}
	} else if changePair {
		sealed, e := s.seal(p.TenantID, domain, v.AccountID, nextCred, in)
		if e != nil {
			return Receipt{}, e
		}
		defer clear(sealed.Ciphertext)
		if e = saveCredential(ctx, tx, p, domain, v.AccountID, nextCred, sealed); e != nil {
			return Receipt{}, e
		}
	}
	return finish(ctx, tx, p, in, operation, fp, domain, v.AccountID, v.revision, v.revision+1, v.credential, nextCred)
}
