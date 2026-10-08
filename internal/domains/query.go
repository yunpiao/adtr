package domains

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Only terminal task publication plus matching immutable evidence can represent
// a successful observation. Staged checkpoints and superseded requests cannot.
// The predicate is deliberately total: missing JSON fields, wrong versions and
// the other source's payload can never fall through to testing or verified.
const observationPinsExpr = `(t.task_id IS NOT NULL AND t.payload_version=1
 AND (t.payload->>'connectionRevision') IS NOT DISTINCT FROM c.connection_revision::text
 AND (t.payload->>'policyRevision') IS NOT DISTINCT FROM $3::text
 AND (t.payload->>'diagnosticGeneration') IS NOT DISTINCT FROM c.diagnostic_generation::text
 AND ((t.kind='domain.connection_test' AND c.credential_mode='custom'
 AND (t.payload->>'credentialRevision') IS NOT DISTINCT FROM c.credential_revision::text)
 OR (t.kind='domain.account_connection_test' AND c.credential_mode='operation_account'
 AND (t.payload->>'credentialSource') IS NOT DISTINCT FROM 'operation_account'::text
 AND (t.payload->>'connectionCredentialGeneration') IS NOT DISTINCT FROM c.credential_revision::text
 AND (t.payload->>'accountId') IS NOT DISTINCT FROM c.operation_account_id
 AND (t.payload->>'accountCredentialRevision') IS NOT DISTINCT FROM c.operation_account_credential_revision::text
 AND EXISTS(SELECT FROM adtr.domain_account_task_uses u WHERE u.task_id=t.task_id AND u.tenant_id=t.tenant_id AND u.domain_id=t.domain_id
 AND u.account_id=c.operation_account_id AND u.account_credential_revision=c.operation_account_credential_revision
 AND (t.payload->>'grantRoleId') IS NOT DISTINCT FROM u.grant_role_id
 AND (t.payload->>'grantRevision') IS NOT DISTINCT FROM u.grant_revision::text))))`
const configuredExpr = CredentialConfiguredSQL
const stateExpr = `CASE WHEN NOT ` + observationPinsExpr + ` THEN 'unverified'
 WHEN t.state IN ('queued','running','retry_wait','cancel_requested') THEN 'testing'
 WHEN t.state='succeeded' AND EXISTS(SELECT FROM adtr.domain_diagnostics d WHERE d.tenant_id=c.tenant_id AND d.domain_id=c.domain_id AND d.task_id=t.task_id AND d.connection_revision=c.connection_revision AND d.credential_revision=c.credential_revision AND d.policy_revision=$3 AND d.diagnostic_generation=c.diagnostic_generation AND d.successful_observation) THEN 'verified'
 WHEN t.state IN ('failed','partial_failed','dead_letter','cancelled') THEN 'error' ELSE 'unverified' END`
const scopeCTE = `WITH scoped AS (SELECT c.*,` + stateExpr + ` AS connection_state FROM adtr.domain_connections c LEFT JOIN adtr.tasks t ON t.tenant_id=c.tenant_id AND t.domain_id=c.domain_id AND t.task_id=c.latest_test_task_id AND t.kind IN ('domain.connection_test','domain.account_connection_test') WHERE c.tenant_id=$1 AND c.domain_id=ANY($2::text[]) AND c.deleted_at IS NULL)
`
const filterWhere = ` WHERE (cardinality($4::text[])=0 OR canonical_domain=ANY($4::text[])) AND (cardinality($5::text[])=0 OR connection_state=ANY($5::text[])) AND (position(lower($6) in lower(canonical_domain))>0 OR position(lower($6) in lower(dc_hostname))>0)`

func (s *Store) ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f Filter) (List, error) {
	if allowed == nil {
		allowed = []string{}
	}
	if f.Domains == nil {
		f.Domains = []string{}
	}
	if f.Statuses == nil {
		f.Statuses = []string{}
	}
	args := []any{tenant, allowed, s.policyRevision(), f.Domains, f.Statuses, f.Keyword}
	var total int
	if e := tx.QueryRow(ctx, scopeCTE+"SELECT count(*) FROM scoped"+filterWhere, args...).Scan(&total); e != nil {
		return List{}, e
	}
	size := f.PageSize
	if size == -1 {
		if total > 1000 {
			return List{}, problem(422, "result_too_large")
		}
		size = 1000
	}
	if size < 1 || f.PageIdx < 1 {
		return List{}, problem(400, "invalid_input")
	}
	out := List{Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: total}, List: []Connection{}}
	if total > 0 {
		out.Page.Pages = (total + size - 1) / size
	}
	offset := (f.PageIdx - 1) * size
	direction := "DESC"
	if f.Ascending {
		direction = "ASC"
	}
	args = append(args, size, offset)
	rs, e := tx.Query(ctx, scopeCTE+"SELECT "+coreColumns+" FROM scoped"+filterWhere+" ORDER BY created_at "+direction+",domain_id "+direction+" LIMIT $7 OFFSET $8", args...)
	if e != nil {
		return out, e
	}
	values := []row{}
	for rs.Next() {
		v, e := readRow(rs)
		if e != nil {
			rs.Close()
			return out, e
		}
		values = append(values, v)
	}
	e = rs.Err()
	rs.Close()
	if e != nil {
		return out, e
	}
	for _, v := range values {
		if e = s.decorate(ctx, tx, tenant, &v); e != nil {
			return out, e
		}
		out.List = append(out.List, v.Connection)
	}
	out.Exhausted = offset+len(out.List) >= total
	return out, nil
}
func (s *Store) DetailTx(ctx context.Context, tx pgx.Tx, tenant, id string) (Connection, error) {
	v, e := getRow(ctx, tx, tenant, id, false)
	if e != nil {
		return Connection{}, e
	}
	if e = s.decorate(ctx, tx, tenant, &v); e != nil {
		return Connection{}, e
	}
	return v.Connection, nil
}
func (s *Store) decorate(ctx context.Context, tx pgx.Tx, tenant string, v *row) error {
	if e := tx.QueryRow(ctx, `SELECT `+configuredExpr+` FROM adtr.domain_connections c WHERE c.tenant_id=$1 AND c.domain_id=$2`, tenant, v.DomainID).Scan(&v.CredentialConfigured); e != nil {
		return e
	}
	if v.latest == "" {
		return nil
	}
	query := `SELECT ` + stateExpr + ` FROM adtr.domain_connections c LEFT JOIN adtr.tasks t ON t.tenant_id=c.tenant_id AND t.domain_id=c.domain_id AND t.task_id=c.latest_test_task_id AND t.kind IN ('domain.connection_test','domain.account_connection_test') WHERE c.tenant_id=$1 AND c.domain_id=$2`
	if e := tx.QueryRow(ctx, query, tenant, v.DomainID, s.policyRevision()).Scan(&v.ConnectionState); e != nil {
		return e
	}
	d, e := s.diagnostic(ctx, tx, tenant, v.DomainID, v.latest)
	if e != nil {
		return e
	}
	v.LastDiagnostic = d
	return nil
}
func (s *Store) diagnostic(ctx context.Context, tx pgx.Tx, tenant, id, task string) (*Diagnostic, error) {
	var d Diagnostic
	e := tx.QueryRow(ctx, `SELECT d.task_id,d.connection_revision::text,d.credential_revision::text,d.code,d.stage,d.observed_at,d.elapsed_ms,d.successful_observation,d.dc_hostname,d.naming_context
 FROM adtr.domain_diagnostics d JOIN adtr.tasks t ON t.task_id=d.task_id AND t.tenant_id=d.tenant_id AND t.domain_id=d.domain_id
 JOIN adtr.domain_connections c ON c.tenant_id=d.tenant_id AND c.domain_id=d.domain_id
 WHERE d.tenant_id=$1 AND d.domain_id=$2 AND d.task_id=$3 AND c.deleted_at IS NULL
 AND c.connection_revision=d.connection_revision AND c.credential_revision=d.credential_revision AND d.policy_revision=$4
 AND c.diagnostic_generation=d.diagnostic_generation AND c.latest_test_task_id=d.task_id
 AND `+strings.ReplaceAll(observationPinsExpr, "$3", "$4")+` AND t.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled')
	 AND (NOT d.successful_observation OR t.state='succeeded')`, tenant, id, task, s.policyRevision()).Scan(&d.TaskUUID, &d.Revision, &d.CredentialRevision, &d.Code, &d.Stage, &d.ObservedAt, &d.ElapsedMilliseconds, &d.SuccessfulObservation, &d.DCHostName, &d.NamingContext)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	d.ObservedAt = d.ObservedAt.UTC()
	if d.SuccessfulObservation {
		d.Code, d.Stage = "ok", "complete"
	}
	return &d, nil
}
func (s *Store) TestResultTx(ctx context.Context, tx pgx.Tx, task tasks.Task) (*Diagnostic, error) {
	return s.diagnostic(ctx, tx, task.TenantID, task.DomainID, task.ID)
}
