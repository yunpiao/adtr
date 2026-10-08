package domains

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/tasks"
)

// CredentialConfiguredSQL is a source-integrity predicate for connection alias
// c. It discloses existence only and deliberately does not imply actor use rights.
const CredentialConfiguredSQL = `(c.deleted_at IS NULL AND CASE c.credential_mode
 WHEN 'custom' THEN c.operation_account_id IS NULL AND c.operation_account_credential_revision IS NULL
 AND EXISTS(SELECT FROM adtr.domain_credentials cp WHERE cp.tenant_id=c.tenant_id AND cp.domain_id=c.domain_id AND cp.credential_revision=c.credential_revision)
 AND NOT EXISTS(SELECT FROM adtr.operation_account_dependencies cb WHERE cb.tenant_id=c.tenant_id AND cb.domain_id=c.domain_id AND cb.consumer_kind='domain.connection_binding')
 WHEN 'operation_account' THEN NOT EXISTS(SELECT FROM adtr.domain_credentials cp WHERE cp.tenant_id=c.tenant_id AND cp.domain_id=c.domain_id)
 AND (SELECT count(*) FROM adtr.operation_account_dependencies cb WHERE cb.tenant_id=c.tenant_id AND cb.domain_id=c.domain_id AND cb.consumer_kind='domain.connection_binding')=1
 AND EXISTS(SELECT FROM adtr.operation_accounts ca JOIN adtr.operation_account_credentials cc
 ON cc.tenant_id=ca.tenant_id AND cc.domain_id=ca.domain_id AND cc.account_id=ca.account_id AND cc.credential_revision=ca.credential_revision
 JOIN adtr.operation_account_dependencies cb ON cb.tenant_id=ca.tenant_id AND cb.domain_id=ca.domain_id AND cb.account_id=ca.account_id
 AND cb.consumer_kind='domain.connection_binding' AND cb.object_id=c.domain_id AND cb.account_credential_revision=ca.credential_revision AND cb.connection_credential_generation=c.credential_revision
 WHERE ca.tenant_id=c.tenant_id AND ca.domain_id=c.domain_id AND ca.account_id=c.operation_account_id
 AND ca.credential_revision=c.operation_account_credential_revision AND ca.deleted_at IS NULL AND cc.envelope_version=2)
 ELSE false END)`

// SetSourceContext conveys the server-authenticated proof provenance. Call it
// only after fresh proof succeeds. SQL independently checks identity and scope.
func SetSourceContext(ctx context.Context, tx pgx.Tx, p tasks.Principal, op string) error {
	if p.TenantID == "" || p.ActorID <= 0 || op != "reference" && op != "custom" && op != "detach" {
		return problem(403, "credential_source_authorization_required")
	}
	_, err := tx.Exec(ctx, `SELECT set_config('adtr.domain_source_protocol','account_reference_source_v1',true),set_config('adtr.domain_source_tenant',$1,true),set_config('adtr.domain_source_actor',$2,true),set_config('adtr.domain_source_operation',$3,true)`, p.TenantID, strconv.FormatInt(p.ActorID, 10), op)
	return err
}

func (s *Store) sourceFingerprint(p tasks.Principal, op string, in SourceInput) (string, error) {
	// Use a private wire shape: SourceInput's public marshaler always redacts it.
	raw, err := json.Marshal(struct {
		Version                                string
		Tenant                                 string
		Actor                                  int64
		Operation                              string
		DomainID                               string
		ExpectedRevision                       string
		ExpectedConnectionCredentialGeneration string
		AccountID                              string
		ExpectedAccountRevision                string
		ExpectedAccountCredentialRevision      string
		ExpectedGrantRevision                  string
		Username                               *string
		Password                               *string
		IdempotencyKey                         string
	}{"domain-source-v1", p.TenantID, p.ActorID, op, in.DomainID, in.ExpectedRevision, in.ExpectedConnectionCredentialGeneration, in.AccountID, in.ExpectedAccountRevision, in.ExpectedAccountCredentialRevision, in.ExpectedGrantRevision, in.Username, in.Password, in.IdempotencyKey})
	if err != nil {
		return "", err
	}
	defer clear(raw)
	if op == "custom" {
		if !s.runtime.Enabled() {
			return "", problem(503, "domain_key_unavailable")
		}
		out, err := s.runtime.Vault().Fingerprint("domain-source-custom-v1", p.TenantID, raw)
		if err != nil {
			return "", problem(503, "domain_key_unavailable")
		}
		return out, nil
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SourceReceiptTx requires current domain write rights from the caller. Persisted
// active exact scope is rechecked here without requiring account rights or a key.
func (s *Store) SourceReceiptTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, key string, allowed []string) (SourceReceipt, error) {
	var out SourceReceipt
	err := tx.QueryRow(ctx, `SELECT m.operation,m.domain_id,m.connection_revision::text,m.connection_credential_generation::text,m.credential_source,
 c.deleted_at IS NOT NULL,c.connection_revision::text,c.credential_revision::text,c.credential_mode
 FROM adtr.domain_credential_source_mutations m JOIN adtr.domain_connections c ON c.tenant_id=m.tenant_id AND c.domain_id=m.domain_id
 JOIN adtr.resource_domains rd ON rd.tenant_id=c.tenant_id AND rd.id=c.domain_id AND rd.active
 WHERE m.tenant_id=$1 AND m.actor_id=$2 AND m.idempotency_key=$3 AND m.domain_id=ANY($4::text[])
 AND EXISTS(SELECT FROM adtr.users u JOIN adtr.resource_role_groups rg ON rg.tenant_id=u.tenant_id AND rg.role_id=COALESCE(NULLIF(u.role_id,''),u.role)
 JOIN adtr.resource_group_members gm ON gm.tenant_id=rg.tenant_id AND gm.group_id=rg.group_id AND gm.domain_id=m.domain_id
 WHERE u.tenant_id=m.tenant_id AND u.id=$2 AND NOT u.disabled AND NOT u.must_change AND u.password_updated_at>clock_timestamp()-interval '90 days')`, p.TenantID, p.ActorID, key, allowed).Scan(&out.Operation, &out.DomainID, &out.Revision, &out.ConnectionCredentialGeneration, &out.CredentialSource, &out.Deleted, &out.CurrentRevision, &out.CurrentConnectionCredentialGeneration, &out.CurrentCredentialSource)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, problem(404, "not_found")
	}
	out.Result = "SUCCESS"
	out.Replayed = true
	return out, err
}

// SourceReplayTx runs before fresh proof, obsolete CAS or grant checks. Reference
// HTTP replay additionally requires current account metadata permission.
func (s *Store) SourceReplayTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, op string, in SourceInput, allowed []string) (*SourceReceipt, error) {
	if err := ValidateSourceInput(op, &in); err != nil {
		return nil, err
	}
	var old, oldOp string
	err := tx.QueryRow(ctx, `SELECT operation,fingerprint FROM adtr.domain_credential_source_mutations WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, p.TenantID, p.ActorID, in.IdempotencyKey).Scan(&oldOp, &old)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.SourceReceiptTx(ctx, tx, p, in.IdempotencyKey, allowed)
	if err != nil {
		return nil, err
	}
	fingerprint, err := s.sourceFingerprint(p, op, in)
	if err != nil {
		return nil, err
	}
	if oldOp != op || subtle.ConstantTimeCompare([]byte(old), []byte(fingerprint)) != 1 {
		return nil, problem(409, "idempotency_conflict")
	}
	return &out, nil
}

// MutateSourceTx requires schema -> tenant -> actor locks, current normal rights
// and SetSourceContext after fresh proof. Every error requires whole-tx rollback.
func (s *Store) MutateSourceTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, role, op string, in SourceInput, allowed []string) (SourceReceipt, error) {
	if err := ValidateSourceInput(op, &in); err != nil {
		return SourceReceipt{}, err
	}
	if old, err := s.SourceReplayTx(ctx, tx, p, op, in, allowed); err != nil {
		return SourceReceipt{}, err
	} else if old != nil {
		return *old, nil
	}
	var scoped bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.resource_domains rd JOIN adtr.resource_group_members m ON m.tenant_id=rd.tenant_id AND m.domain_id=rd.id JOIN adtr.resource_role_groups rg ON rg.tenant_id=m.tenant_id AND rg.group_id=m.group_id JOIN adtr.users u ON u.tenant_id=rg.tenant_id AND COALESCE(NULLIF(u.role_id,''),u.role)=rg.role_id WHERE rd.tenant_id=$1 AND rd.id=$2 AND rd.active AND rd.id=ANY($4::text[]) AND u.id=$3 AND rg.role_id=$5)`, p.TenantID, in.DomainID, p.ActorID, allowed, role).Scan(&scoped); err != nil {
		return SourceReceipt{}, err
	}
	if !scoped {
		return SourceReceipt{}, problem(404, "not_found")
	}
	if op != "detach" {
		if _, _, err := EnrollmentEligible(ctx, tx, p.TenantID, time.Now()); err != nil {
			return SourceReceipt{}, err
		}
	}
	v, err := getRow(ctx, tx, p.TenantID, in.DomainID, true)
	if err != nil {
		return SourceReceipt{}, err
	}
	// Serialize old and new accounts in ID order before touching either child.
	// The tenant lock excludes ordinary source/account races; NOWAIT guards still
	// reject direct SQL that arrives with an inverted parent/tenant lock order.
	locked, err := tx.Query(ctx, `SELECT account_id FROM adtr.operation_accounts WHERE tenant_id=$1 AND domain_id=$2 AND account_id=ANY($3::text[]) ORDER BY account_id FOR UPDATE`, p.TenantID, in.DomainID, []string{v.accountID, in.AccountID})
	if err != nil {
		return SourceReceipt{}, err
	}
	for locked.Next() {
		var id string
		if err = locked.Scan(&id); err != nil {
			locked.Close()
			return SourceReceipt{}, err
		}
	}
	err = locked.Err()
	locked.Close()
	if err != nil {
		return SourceReceipt{}, err
	}
	var accountRevision, accountCredentialRevision string
	if op == "reference" {
		// Only a current own-role allow makes the selected account visible to this
		// operation. Compare CAS only after this eligibility test.
		err = tx.QueryRow(ctx, `SELECT a.revision::text,a.credential_revision::text FROM adtr.operation_accounts a WHERE a.tenant_id=$1 AND a.domain_id=$2 AND a.account_id=$3 AND a.deleted_at IS NULL FOR UPDATE`, p.TenantID, in.DomainID, in.AccountID).Scan(&accountRevision, &accountCredentialRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			return SourceReceipt{}, problem(404, "not_found")
		}
		if err != nil {
			return SourceReceipt{}, err
		}
		effective, err := credentialuse.New().EffectiveTx(ctx, tx, p, role, in.AccountID, []string{in.DomainID})
		if err != nil {
			return SourceReceipt{}, err
		}
		if !effective.ExplicitlyGranted || !effective.Eligible {
			return SourceReceipt{}, problem(404, "not_found")
		}
		var grantRevision string
		if err = tx.QueryRow(ctx, `SELECT grant_revision::text FROM adtr.operation_account_use_grants WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND role_id=$4 AND purpose='domain.connection_test' AND allowed FOR UPDATE`, p.TenantID, in.DomainID, in.AccountID, role).Scan(&grantRevision); err != nil {
			return SourceReceipt{}, err
		}
		if accountRevision != in.ExpectedAccountRevision || accountCredentialRevision != in.ExpectedAccountCredentialRevision || grantRevision != in.ExpectedGrantRevision {
			return SourceReceipt{}, problem(409, "revision_conflict")
		}
	}
	if v.Revision != in.ExpectedRevision || v.CredentialRevision != in.ExpectedConnectionCredentialGeneration {
		return SourceReceipt{}, problem(409, "revision_conflict")
	}
	if op == "reference" && v.CredentialSource == SourceOperationAccount && v.accountID == in.AccountID || op == "detach" && v.CredentialSource == SourceUnconfigured {
		return SourceReceipt{}, problem(409, "no_change")
	}
	fingerprint, err := s.sourceFingerprint(p, op, in)
	if err != nil {
		return SourceReceipt{}, err
	}
	var sealed domainconfig.SealedCredential
	if op == "custom" {
		sealed, err = s.seal(p.TenantID, in.DomainID, v.credential+1, Input{Username: in.Username, Password: in.Password})
		if err != nil {
			return SourceReceipt{}, err
		}
		defer clear(sealed.Ciphertext)
	}
	nextSource := SourceUnconfigured
	var account any
	var accountRevisionPin any
	if op == "custom" {
		nextSource = SourceCustom
	}
	if op == "reference" {
		nextSource = SourceOperationAccount
		account = in.AccountID
		accountRevisionPin = accountCredentialRevision
	}
	_, err = tx.Exec(ctx, `UPDATE adtr.domain_connections SET credential_mode=$3,operation_account_id=$4,operation_account_credential_revision=$5,connection_revision=connection_revision+1,credential_revision=credential_revision+1,diagnostic_generation=diagnostic_generation+1,latest_test_task_id=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND domain_id=$2`, p.TenantID, in.DomainID, nextSource, account, accountRevisionPin)
	if err != nil {
		return SourceReceipt{}, err
	}
	// Remove only the previous binding. Opened/reserved uses and unknown consumers
	// remain durable blockers even when their source is no longer selected.
	if _, err = tx.Exec(ctx, `DELETE FROM adtr.operation_account_dependencies WHERE tenant_id=$1 AND domain_id=$2 AND consumer_kind='domain.connection_binding' AND object_id=$2`, p.TenantID, in.DomainID); err != nil {
		return SourceReceipt{}, err
	}
	if op == "custom" {
		_, err = tx.Exec(ctx, `INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,domain_id) DO UPDATE SET credential_revision=EXCLUDED.credential_revision,key_id=EXCLUDED.key_id,ciphertext=EXCLUDED.ciphertext,created_at=clock_timestamp()`, p.TenantID, in.DomainID, v.credential+1, sealed.KeyID, sealed.Ciphertext)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM adtr.domain_credentials WHERE tenant_id=$1 AND domain_id=$2`, p.TenantID, in.DomainID)
	}
	if err != nil {
		return SourceReceipt{}, err
	}
	if op == "reference" {
		if _, err = tx.Exec(ctx, `INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation) VALUES($1,$2,$3,'domain.connection_binding',$2,$4,$5)`, p.TenantID, in.DomainID, in.AccountID, accountCredentialRevision, v.credential+1); err != nil {
			return SourceReceipt{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.domain_credential_source_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,connection_revision,connection_credential_generation,credential_source) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.TenantID, p.ActorID, in.IdempotencyKey, op, fingerprint, in.DomainID, v.revision+1, v.credential+1, nextSource); err != nil {
		return SourceReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result,credential_source,operation_account_id,account_credential_revision) VALUES($1,$2,$3,$4,$5,$6,$7,'','saved',$8,$9,$10)`, p.TenantID, p.ActorID, in.DomainID, "domain_credential_"+op, v.revision, v.revision+1, v.credential+1, nextSource, account, accountRevisionPin); err != nil {
		return SourceReceipt{}, err
	}
	revision, generation := strconv.FormatInt(v.revision+1, 10), strconv.FormatInt(v.credential+1, 10)
	return SourceReceipt{Result: "SUCCESS", Operation: op, DomainID: in.DomainID, Revision: revision, ConnectionCredentialGeneration: generation, CredentialSource: nextSource, CurrentRevision: revision, CurrentConnectionCredentialGeneration: generation, CurrentCredentialSource: nextSource}, nil
}

// SourceDetailTx requires the caller's current domains-read and persisted active
// exact-domain scope. accountReadable must include normal live account read scope;
// testAuthorized must include all ordinary current task/domain test permissions.
func (s *Store) SourceDetailTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, role, id string, accountReadable, testAuthorized bool) (SourceDetail, error) {
	v, err := getRow(ctx, tx, p.TenantID, id, false)
	if err != nil {
		return SourceDetail{}, err
	}
	out := SourceDetail{DomainID: id, Revision: v.Revision, ConnectionCredentialGeneration: v.CredentialRevision, CredentialSource: v.CredentialSource}
	if err = tx.QueryRow(ctx, `SELECT `+CredentialConfiguredSQL+` FROM adtr.domain_connections c WHERE c.tenant_id=$1 AND c.domain_id=$2`, p.TenantID, id).Scan(&out.CredentialConfigured); err != nil {
		return SourceDetail{}, err
	}
	eligible := v.CredentialSource == SourceCustom
	if v.CredentialSource == SourceOperationAccount && accountReadable {
		var ref SourceReference
		err = tx.QueryRow(ctx, `SELECT account_id,label,revision::text,credential_revision::text FROM adtr.operation_accounts WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND deleted_at IS NULL`, p.TenantID, id, v.accountID).Scan(&ref.AccountID, &ref.Label, &ref.AccountRevision, &ref.AccountCredentialRevision)
		if err != nil {
			return SourceDetail{}, err
		}
		effective, err := credentialuse.New().EffectiveTx(ctx, tx, p, role, v.accountID, []string{id})
		if err != nil {
			return SourceDetail{}, err
		}
		ref.Purpose = credentialuse.Purpose
		ref.ExplicitlyGranted = effective.ExplicitlyGranted
		ref.Eligible = effective.Eligible
		ref.GrantRevision = effective.GrantRevision
		out.Reference = &ref
		eligible = effective.Eligible
	}
	if testAuthorized && eligible && out.CredentialConfigured && s.runtime.ProbeEnabled() {
		ip := netip.Addr{}
		if v.LDAPAddr != "" {
			ip, err = netip.ParseAddr(v.LDAPAddr)
			if err != nil {
				return SourceDetail{}, problem(503, "invalid_config")
			}
		}
		_, err = s.runtime.Policy().AuthorizeTarget(p.TenantID, v.Domain, v.DCHostName, ip)
		out.TestEligible = err == nil
	}
	return out, nil
}
