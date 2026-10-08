package domains

import (
	"bytes"
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/tasks"
)

// directoryGrantTx deliberately has no connection-test fallback. Caller holds
// schema, tenant, actor and connection locks before entering the account/grant
// lock order. A metadata-only account rename does not revoke an otherwise pinned
// credential pair. A source rebind or pair/grant incarnation change does.
func directoryGrantTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, v row) (string, string, error) {
	var role, grant string
	err := tx.QueryRow(ctx, `SELECT g.role_id,g.grant_revision::text
 FROM adtr.operation_accounts a
 JOIN adtr.operation_account_credentials pair ON pair.tenant_id=a.tenant_id AND pair.domain_id=a.domain_id AND pair.account_id=a.account_id AND pair.credential_revision=a.credential_revision AND pair.envelope_version=2
 JOIN adtr.users u ON u.tenant_id=a.tenant_id AND u.id=$4
 JOIN adtr.operation_account_use_grants g ON g.tenant_id=a.tenant_id AND g.domain_id=a.domain_id AND g.account_id=a.account_id AND g.purpose=$7 AND g.role_id=COALESCE(NULLIF(u.role_id,''),u.role)
 WHERE a.tenant_id=$1 AND a.domain_id=$2 AND a.account_id=$3 AND a.deleted_at IS NULL AND a.credential_revision=$5
 AND g.allowed AND g.account_credential_revision=a.credential_revision
 AND NOT u.disabled AND NOT u.must_change AND u.password_updated_at>clock_timestamp()-interval '90 days'
 AND adtr.credential_use_role_eligible_for_purpose(a.tenant_id,a.domain_id,g.role_id,$7)
 AND EXISTS(SELECT FROM adtr.operation_account_dependencies d WHERE d.tenant_id=a.tenant_id AND d.domain_id=a.domain_id AND d.account_id=a.account_id AND d.consumer_kind='domain.connection_binding' AND d.object_id=a.domain_id AND d.account_credential_revision=a.credential_revision AND d.connection_credential_generation=$6)
 FOR UPDATE OF a,g`, p.TenantID, v.DomainID, v.accountID, p.ActorID, v.accountCredential, v.credential, credentialuse.DirectoryPurpose).Scan(&role, &grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", tasks.ErrAuthorization
	}
	return role, grant, err
}

// currentDirectory checks current authority without reading or mutating the B2
// diagnostic generation/latest-test identity. The future consumer must invoke
// this within the engine's fenced transaction at opening, every page and final
// publication; this helper alone does not open or decrypt a credential.
func (s *Store) currentDirectory(ctx context.Context, tx pgx.Tx, t tasks.Task, p directoryPinnedPayload) (row, error) {
	canonical, err := validateDirectoryPayload(t.Payload)
	if err != nil || t.Kind != DirectoryKindName || t.PayloadVersion != 1 || t.ActorID <= 0 || !bytes.Equal(canonical, p.json()) {
		return row{}, tasks.ErrAuthorization
	}
	if !s.runtime.DirectoryReadEnabled() {
		return row{}, problem(503, "directory_read_disabled")
	}
	if s.policyRevision() != p.PolicyRevision {
		return row{}, problem(409, "policy_revision_changed")
	}
	v, err := getRow(ctx, tx, t.TenantID, t.DomainID, true)
	if err != nil {
		var failure *Error
		if errors.As(err, &failure) && failure.Status == 404 {
			return row{}, problem(409, "connection_revision_changed")
		}
		return row{}, err
	}
	if v.CredentialSource != "operation_account" || v.Revision != p.ConnectionRevision || v.CredentialRevision != p.ConnectionCredentialGeneration || v.accountID != p.AccountID || strconv.FormatInt(v.accountCredential, 10) != p.AccountCredentialRevision {
		return row{}, problem(409, "connection_revision_changed")
	}
	role, grant, err := directoryGrantTx(ctx, tx, tasks.Principal{TenantID: t.TenantID, ActorID: t.ActorID}, v)
	if err != nil {
		return row{}, err
	}
	if role != p.GrantRoleID || grant != p.GrantRevision {
		return row{}, tasks.ErrAuthorization
	}
	return v, nil
}
