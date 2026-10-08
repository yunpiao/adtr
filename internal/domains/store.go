package domains

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/tasks"
)

// All Tx methods require the caller's schema -> tenant -> actor locks and live
// function/resource checks. An error requires rolling back the whole transaction.
type row struct {
	Connection
	revision, credential, generation int64
	deleted                          *time.Time
	latest                           string
	accountID                        string
	accountCredential                int64
}

const coreColumns = `domain_id,canonical_domain,dc_hostname,dial_ip,port,transport_mode,connection_revision,credential_revision,created_at,updated_at,deleted_at,COALESCE(latest_test_task_id,''),diagnostic_generation,credential_mode,COALESCE(operation_account_id,''),COALESCE(operation_account_credential_revision,0)`

func readRow(r pgx.Row) (row, error) {
	var v row
	e := r.Scan(&v.DomainID, &v.Domain, &v.DCHostName, &v.LDAPAddr, &v.Port, &v.Mode, &v.revision, &v.credential, &v.CreatedAt, &v.UpdatedAt, &v.deleted, &v.latest, &v.generation, &v.CredentialSource, &v.accountID, &v.accountCredential)
	v.Revision = strconv.FormatInt(v.revision, 10)
	v.CredentialRevision = strconv.FormatInt(v.credential, 10)
	v.ConnectionCredentialGeneration = v.CredentialRevision
	v.CreatedAt = v.CreatedAt.UTC()
	v.UpdatedAt = v.UpdatedAt.UTC()
	v.ConnectionState = "unverified"
	v.LatestTaskUUID = v.latest
	return v, e
}
func getRow(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (row, error) {
	q := "SELECT " + coreColumns + " FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id=$2 AND deleted_at IS NULL"
	if lock {
		q += " FOR UPDATE"
	}
	v, e := readRow(tx.QueryRow(ctx, q, tenant, id))
	if errors.Is(e, pgx.ErrNoRows) {
		e = problem(404, "not_found")
	}
	return v, e
}
func randomID() (string, error) {
	var b [18]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (s *Store) seal(tenant, id string, revision int64, in Input) (domainconfig.SealedCredential, error) {
	if !s.runtime.Enabled() {
		return domainconfig.SealedCredential{}, problem(503, "domain_key_unavailable")
	}
	raw, e := json.Marshal(struct {
		Version  int    `json:"version"`
		Username string `json:"username"`
		Password string `json:"password"`
	}{1, *in.Username, *in.Password})
	if e != nil {
		return domainconfig.SealedCredential{}, problem(400, "invalid_input")
	}
	defer clear(raw)
	sealed, e := s.runtime.Vault().Seal(tenant, id, revision, raw)
	if e != nil {
		return sealed, problem(503, "credential_unavailable")
	}
	return sealed, nil
}
func audit(ctx context.Context, tx pgx.Tx, p tasks.Principal, id, action string, old, next, credential int64, task, code string) error {
	_, e := tx.Exec(ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.TenantID, p.ActorID, id, action, old, next, credential, task, code)
	return e
}

func EnrollmentEligible(ctx context.Context, tx pgx.Tx, tenant string, now time.Time) (int, int, error) {
	var max, count int
	var expires int64
	e := tx.QueryRow(ctx, `SELECT max_ad_count,expire_time,(SELECT count(*) FROM adtr.resource_domains d WHERE d.tenant_id=c.tenant_id AND d.active) FROM adtr.resource_tenant_config c WHERE tenant_id=$1`, tenant).Scan(&max, &expires, &count)
	if errors.Is(e, pgx.ErrNoRows) {
		return 0, 0, problem(409, "tenant_not_configured")
	}
	if e != nil {
		return 0, 0, e
	}
	if expires <= now.Unix() {
		return 0, 0, problem(403, "tenant_expired")
	}
	if max <= 0 || count > max {
		return 0, 0, problem(403, "tenant_domain_limit_exceeded")
	}
	return max, count, nil
}
func (s *Store) ReceiptTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, key string) (Receipt, error) {
	var v Receipt
	var n int64
	e := tx.QueryRow(ctx, `SELECT c.domain_id,c.canonical_domain,r.connection_revision,c.deleted_at IS NOT NULL FROM adtr.domain_creation_receipts r JOIN adtr.domain_connections c ON c.tenant_id=r.tenant_id AND c.domain_id=r.domain_id WHERE r.tenant_id=$1 AND r.actor_id=$2 AND r.idempotency_key=$3`, p.TenantID, p.ActorID, key).Scan(&v.DomainID, &v.Domain, &n, &v.Deleted)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, problem(404, "not_found")
	}
	v.Revision = strconv.FormatInt(n, 10)
	v.RequiresResourceAssignment = true
	return v, e
}
func (s *Store) CreateTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, in Input, now time.Time) (Mutation, error) {
	if e := ValidateInput("/create", &in); e != nil {
		return Mutation{}, e
	}
	if !s.runtime.Enabled() {
		return Mutation{}, problem(503, "domain_key_unavailable")
	}
	// Encode only the exact normalized nonproof request; the keyed digest is the
	// only retained representation of this secret-bearing request.
	raw, e := json.Marshal(struct{ Domain, DC, IP, Port, Username, Password string }{in.Domain, in.DCHostName, in.LDAPAddr, in.Port, *in.Username, *in.Password})
	if e != nil {
		return Mutation{}, e
	}
	defer clear(raw)
	fingerprint, e := s.runtime.Vault().Fingerprint("domain-create-v1", p.TenantID, raw)
	if e != nil {
		return Mutation{}, problem(503, "domain_key_unavailable")
	}
	var old, operation string
	e = tx.QueryRow(ctx, `SELECT fingerprint,creation_operation FROM adtr.domain_creation_receipts WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, p.TenantID, p.ActorID, in.IdempotencyKey).Scan(&old, &operation)
	if e == nil {
		if operation != "custom" || subtle.ConstantTimeCompare([]byte(old), []byte(fingerprint)) != 1 {
			return Mutation{}, problem(409, "idempotency_conflict")
		}
		r, e := s.ReceiptTx(ctx, tx, p, in.IdempotencyKey)
		return Mutation{Result: "SUCCESS", DomainID: r.DomainID, Revision: r.Revision, RequiresResourceAssignment: true, Replayed: true}, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return Mutation{}, e
	}
	max, count, e := EnrollmentEligible(ctx, tx, p.TenantID, now)
	if e != nil {
		return Mutation{}, e
	}
	if count >= max {
		return Mutation{}, problem(409, "domain_capacity_exceeded")
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.domain_connections WHERE tenant_id=$1 AND canonical_domain=$2 AND deleted_at IS NULL)`, p.TenantID, in.Domain).Scan(&exists); e != nil {
		return Mutation{}, e
	}
	if exists {
		return Mutation{}, problem(409, "domain_conflict")
	}
	id, e := randomID()
	if e != nil {
		return Mutation{}, e
	}
	sealed, e := s.seal(p.TenantID, id, 1, in)
	if e != nil {
		return Mutation{}, e
	}
	defer clear(sealed.Ciphertext)
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES($1,$2,$3,true)`, p.TenantID, id, in.Domain); e != nil {
		return Mutation{}, e
	}
	mode := "starttls"
	if in.Port == "636" {
		mode = "ldaps"
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,dial_ip,port,transport_mode) VALUES($1,$2,$3,$4,$5,$6,$7)`, p.TenantID, id, in.Domain, in.DCHostName, in.LDAPAddr, in.Port, mode); e != nil {
		return Mutation{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES($1,$2,1,$3,$4)`, p.TenantID, id, sealed.KeyID, sealed.Ciphertext); e != nil {
		return Mutation{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO adtr.domain_creation_receipts(tenant_id,actor_id,idempotency_key,fingerprint,domain_id,connection_revision) VALUES($1,$2,$3,$4,$5,1)`, p.TenantID, p.ActorID, in.IdempotencyKey, fingerprint, id); e != nil {
		return Mutation{}, e
	}
	if e = audit(ctx, tx, p, id, "domain_create", 0, 1, 1, "", "saved"); e != nil {
		return Mutation{}, e
	}
	return Mutation{Result: "SUCCESS", DomainID: id, Revision: "1", RequiresResourceAssignment: true}, nil
}
func (s *Store) UpdateTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, in Input) (Mutation, error) {
	if e := ValidateInput("/update", &in); e != nil {
		return Mutation{}, e
	}
	v, e := getRow(ctx, tx, p.TenantID, in.DomainID, true)
	if e != nil {
		return Mutation{}, e
	}
	if v.Revision != in.ExpectedRevision {
		return Mutation{}, problem(409, "revision_conflict")
	}
	if in.Username != nil && v.CredentialSource != SourceCustom {
		return Mutation{}, problem(409, "credential_source_changed")
	}
	if v.DCHostName == in.DCHostName && v.LDAPAddr == in.LDAPAddr && v.Port == in.Port && in.Username == nil {
		return Mutation{}, problem(409, "no_change")
	}
	nextCred := v.credential
	if in.Username != nil {
		nextCred++
		sealed, e := s.seal(p.TenantID, v.DomainID, nextCred, in)
		if e != nil {
			return Mutation{}, e
		}
		defer clear(sealed.Ciphertext)
		if _, e = tx.Exec(ctx, `INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,domain_id) DO UPDATE SET credential_revision=EXCLUDED.credential_revision,key_id=EXCLUDED.key_id,ciphertext=EXCLUDED.ciphertext,created_at=clock_timestamp()`, p.TenantID, v.DomainID, nextCred, sealed.KeyID, sealed.Ciphertext); e != nil {
			return Mutation{}, e
		}
	}
	mode := "starttls"
	if in.Port == "636" {
		mode = "ldaps"
	}
	tag, e := tx.Exec(ctx, `UPDATE adtr.domain_connections SET dc_hostname=$4,dial_ip=$5,port=$6,transport_mode=$7,connection_revision=connection_revision+1,credential_revision=$8,latest_test_task_id=NULL,diagnostic_generation=diagnostic_generation+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND domain_id=$2 AND connection_revision=$3 AND deleted_at IS NULL`, p.TenantID, v.DomainID, v.revision, in.DCHostName, in.LDAPAddr, in.Port, mode, nextCred)
	if e != nil {
		return Mutation{}, e
	}
	if tag.RowsAffected() != 1 {
		return Mutation{}, problem(409, "revision_conflict")
	}
	if e = audit(ctx, tx, p, v.DomainID, "domain_update", v.revision, v.revision+1, nextCred, "", "saved"); e != nil {
		return Mutation{}, e
	}
	return Mutation{Result: "SUCCESS", DomainID: v.DomainID, Revision: strconv.FormatInt(v.revision+1, 10)}, nil
}
func (s *Store) DeleteTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, in Input) (Mutation, error) {
	if e := ValidateInput("/delete", &in); e != nil {
		return Mutation{}, e
	}
	v, e := getRow(ctx, tx, p.TenantID, in.DomainID, true)
	if e != nil {
		return Mutation{}, e
	}
	if v.Revision != in.ExpectedRevision {
		return Mutation{}, problem(409, "revision_conflict")
	}
	if in.ConfirmDomain != v.Domain {
		return Mutation{}, problem(400, "domain_confirmation_mismatch")
	}
	var used bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.domain_dependencies WHERE tenant_id=$1 AND domain_id=$2) OR EXISTS(SELECT FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND kind<>$3 AND state IN ('queued','running','retry_wait','cancel_requested'))`, p.TenantID, v.DomainID, KindName).Scan(&used)
	if e != nil {
		return Mutation{}, e
	}
	if used {
		return Mutation{}, problem(409, "domain_in_use")
	}
	if _, e = tx.Exec(ctx, `UPDATE adtr.domain_connections SET deleted_at=clock_timestamp(),updated_at=clock_timestamp(),connection_revision=connection_revision+1,credential_revision=credential_revision+1,diagnostic_generation=diagnostic_generation+1,latest_test_task_id=NULL,credential_mode='unconfigured',operation_account_id=NULL,operation_account_credential_revision=NULL WHERE tenant_id=$1 AND domain_id=$2`, p.TenantID, v.DomainID); e != nil {
		return Mutation{}, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM adtr.domain_credentials WHERE tenant_id=$1 AND domain_id=$2`, p.TenantID, v.DomainID); e != nil {
		return Mutation{}, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM adtr.resource_group_members WHERE tenant_id=$1 AND domain_id=$2`, p.TenantID, v.DomainID); e != nil {
		return Mutation{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE adtr.resource_domains SET active=false WHERE tenant_id=$1 AND id=$2`, p.TenantID, v.DomainID); e != nil {
		return Mutation{}, e
	}
	if e = audit(ctx, tx, p, v.DomainID, "domain_delete", v.revision, v.revision+1, v.credential+1, "", "disconnected"); e != nil {
		return Mutation{}, e
	}
	return Mutation{Result: "SUCCESS", DomainID: v.DomainID}, nil
}
