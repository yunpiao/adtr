package domains

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/tasks"
)

const AccountKindName = "domain.account_connection_test"

// IsConnectionTestKind is an exact allowlist, never a namespace match.
func IsConnectionTestKind(kind string) bool { return kind == KindName || kind == AccountKindName }

type accountPinnedPayload struct {
	CredentialSource               string `json:"credentialSource"`
	ConnectionRevision             string `json:"connectionRevision"`
	ConnectionCredentialGeneration string `json:"connectionCredentialGeneration"`
	AccountID                      string `json:"accountId"`
	AccountCredentialRevision      string `json:"accountCredentialRevision"`
	GrantRoleID                    string `json:"grantRoleId"`
	GrantRevision                  string `json:"grantRevision"`
	PolicyRevision                 string `json:"policyRevision"`
	DiagnosticGeneration           string `json:"diagnosticGeneration"`
}

func (p accountPinnedPayload) json() json.RawMessage { b, _ := json.Marshal(p); return b }

func validateAccountPayload(raw json.RawMessage) (json.RawMessage, error) {
	if !exactObject(raw, "credentialSource", "connectionRevision", "connectionCredentialGeneration", "accountId", "accountCredentialRevision", "grantRoleId", "grantRevision", "policyRevision", "diagnosticGeneration") {
		return nil, problem(400, "invalid_payload")
	}
	var p accountPinnedPayload
	if json.Unmarshal(raw, &p) != nil || p.CredentialSource != "operation_account" || !ValidID(p.AccountID) || !validAccountGrantRole(p.GrantRoleID) {
		return nil, problem(400, "invalid_payload")
	}
	for _, value := range []string{p.ConnectionRevision, p.ConnectionCredentialGeneration, p.AccountCredentialRevision, p.GrantRevision, p.DiagnosticGeneration} {
		if _, err := Revision(value); err != nil {
			return nil, problem(400, "invalid_payload")
		}
	}
	// Reuse only the common revision/hash grammar; the custom schema stays exact.
	if _, err := validatePayload((pinnedPayload{p.ConnectionRevision, p.ConnectionCredentialGeneration, p.PolicyRevision, p.DiagnosticGeneration}).json()); err != nil {
		return nil, err
	}
	return p.json(), nil
}

// Grant roles follow the existing access-role grammar. Viewer has no eligible
// credential-use grant; all custom role IDs are exactly 24 base64url characters.
func validAccountGrantRole(role string) bool {
	if role == "platform_admin" {
		return true
	}
	if len(role) != 24 {
		return false
	}
	for _, c := range role {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Account label/record revision deliberately does not participate in use pins.
// Caller owns schema, tenant, actor and connection locks before these locks.
func accountGrantTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, v row) (string, string, error) {
	var role, grant string
	err := tx.QueryRow(ctx, `SELECT g.role_id,g.grant_revision::text
 FROM adtr.operation_accounts a
 JOIN adtr.operation_account_credentials pair ON pair.tenant_id=a.tenant_id AND pair.domain_id=a.domain_id AND pair.account_id=a.account_id AND pair.credential_revision=a.credential_revision AND pair.envelope_version=2
 JOIN adtr.users u ON u.tenant_id=a.tenant_id AND u.id=$4
 JOIN adtr.operation_account_use_grants g ON g.tenant_id=a.tenant_id AND g.domain_id=a.domain_id AND g.account_id=a.account_id AND g.purpose='domain.connection_test' AND g.role_id=COALESCE(NULLIF(u.role_id,''),u.role)
 WHERE a.tenant_id=$1 AND a.domain_id=$2 AND a.account_id=$3 AND a.deleted_at IS NULL AND a.credential_revision=$5
 AND g.allowed AND g.account_credential_revision=a.credential_revision
 AND NOT u.disabled AND NOT u.must_change AND u.password_updated_at>clock_timestamp()-interval '90 days'
 AND adtr.credential_use_role_eligible(a.tenant_id,a.domain_id,g.role_id)
 AND EXISTS(SELECT FROM adtr.operation_account_dependencies d WHERE d.tenant_id=a.tenant_id AND d.domain_id=a.domain_id AND d.account_id=a.account_id AND d.consumer_kind='domain.connection_binding' AND d.object_id=a.domain_id AND d.account_credential_revision=a.credential_revision AND d.connection_credential_generation=$6)
 FOR UPDATE OF a,g`, p.TenantID, v.DomainID, v.accountID, p.ActorID, v.accountCredential, v.credential).Scan(&role, &grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", tasks.ErrAuthorization
	}
	return role, grant, err
}

func (s *Store) submitAccountTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, in Input, v row) (tasks.Submission, error) {
	role, grant, err := accountGrantTx(ctx, tx, p, v)
	if err != nil {
		return tasks.Submission{}, err
	}
	payload := accountPinnedPayload{"operation_account", v.Revision, v.CredentialRevision, v.accountID, strconv.FormatInt(v.accountCredential, 10), role, grant, s.policyRevision(), strconv.FormatInt(v.generation+1, 10)}
	out, err := engine.SubmitTx(ctx, tx, p, tasks.SubmitInput{TaskName: AccountKindName, DomainID: v.DomainID, PayloadVersion: 1, Payload: payload.json(), IdempotencyKey: in.IdempotencyKey})
	if err != nil {
		return out, familyConflict(err)
	}
	if out.Replayed {
		return tasks.Submission{}, problem(409, "idempotency_conflict")
	}
	if err = reserveAccountUseTx(ctx, tx, out.Task, payload); err != nil {
		return tasks.Submission{}, err
	}
	if err = recordTestSubmission(ctx, tx, p, v, out.Task.ID); err != nil {
		return tasks.Submission{}, err
	}
	return out, nil
}

func familyConflict(err error) error {
	var db *pgconn.PgError
	if errors.As(err, &db) && db.Code == "23505" && db.ConstraintName == "domain_test_family_idempotency" {
		return problem(409, "idempotency_conflict")
	}
	return err
}

func recordTestSubmission(ctx context.Context, tx pgx.Tx, p tasks.Principal, v row, id string) error {
	if _, err := tx.Exec(ctx, `UPDATE adtr.domain_connections SET latest_test_task_id=$3,diagnostic_generation=diagnostic_generation+1 WHERE tenant_id=$1 AND domain_id=$2`, p.TenantID, v.DomainID, id); err != nil {
		return err
	}
	return audit(ctx, tx, p, v.DomainID, "domain_test_submit", v.revision, v.revision, v.credential, id, "queued")
}
