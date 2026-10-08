package domains

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Each projection uses one PostgreSQL statement snapshot. In particular, task
// publication cannot change state between the count, page and diagnostic reads.
// Scope is enforced here as well as in the HTTP authorization transaction.
const selectionScopeCTE = `WITH scoped AS (
 SELECT c.domain_id,c.canonical_domain,c.connection_revision,c.credential_revision,
 c.dc_hostname,c.dial_ip,c.port,c.transport_mode,c.created_at,` + stateExpr + ` AS connection_state,
 ` + configuredExpr + ` AS credential_configured,
 (SELECT jsonb_build_object('observedAt',d.observed_at,'dcHostName',d.dc_hostname,'code',CASE WHEN d.successful_observation THEN 'ok' ELSE d.code END)
 FROM adtr.domain_diagnostics d
 WHERE d.tenant_id=c.tenant_id AND d.domain_id=c.domain_id AND d.task_id=c.latest_test_task_id
 AND d.connection_revision=c.connection_revision AND d.credential_revision=c.credential_revision
 AND d.policy_revision=$3 AND d.diagnostic_generation=c.diagnostic_generation
 AND ` + observationPinsExpr + ` AND t.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled')
 AND (NOT d.successful_observation OR t.state='succeeded')) AS last_test
 FROM adtr.domain_connections c
 JOIN adtr.resource_domains r ON r.tenant_id=c.tenant_id AND r.id=c.domain_id AND r.active
 LEFT JOIN adtr.tasks t ON t.tenant_id=c.tenant_id AND t.domain_id=c.domain_id AND t.task_id=c.latest_test_task_id AND t.kind IN ('domain.connection_test','domain.account_connection_test')
 WHERE c.tenant_id=$1 AND c.domain_id=ANY($2::text[]) AND c.deleted_at IS NULL
)
`

const selectionChoiceExpr = `jsonb_build_object(
 'domainId',domain_id,'domain',canonical_domain,'revision',connection_revision::text,
 'credentialRevision',credential_revision::text,'dcHostName',dc_hostname,
 'ldapAddr',dial_ip,'port',port,'mode',transport_mode,
 'credentialConfigured',credential_configured,'connectionState',connection_state,
 'source','configured_connection','lastTest',last_test)`

func (s *Store) SelectionListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f SelectionFilter) (SelectionList, error) {
	if allowed == nil {
		allowed = []string{}
	}
	if f.PageIdx < 1 || f.PageIdx > 1000000 || f.PageSize < 10 || f.PageSize > 50 || f.PageSize%10 != 0 {
		return SelectionList{}, problem(400, "invalid_input")
	}
	offset := (f.PageIdx - 1) * f.PageSize
	out := SelectionList{Page: Page{Index: f.PageIdx, Size: f.PageSize}, List: []Choice{}}
	var raw []byte
	err := tx.QueryRow(ctx, selectionScopeCTE+`, filtered AS (
 SELECT * FROM scoped WHERE ($5::text='' OR connection_state=$5)
 AND (position(lower($4::text) in lower(canonical_domain))>0 OR position(lower($4::text) in lower(dc_hostname))>0)
), page_rows AS (
 SELECT * FROM filtered ORDER BY created_at DESC,domain_id DESC LIMIT $6 OFFSET $7
)
 SELECT (SELECT count(*) FROM filtered),
 COALESCE((SELECT jsonb_agg(`+selectionChoiceExpr+` ORDER BY created_at DESC,domain_id DESC) FROM page_rows),'[]'::jsonb)`,
		tenant, allowed, s.policyRevision(), f.Keyword, f.ObservationState, f.PageSize, offset).Scan(&out.Page.Total, &raw)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.List); err != nil {
		return out, err
	}
	for i := range out.List {
		selectionUTC(&out.List[i])
	}
	if out.Page.Total > 0 {
		out.Page.Pages = (out.Page.Total + f.PageSize - 1) / f.PageSize
	}
	out.Exhausted = offset+len(out.List) >= out.Page.Total
	return out, nil
}

func (s *Store) SelectionResolveTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, in SelectionInput) (Choice, error) {
	if allowed == nil {
		allowed = []string{}
	}
	var out Choice
	var raw []byte
	err := tx.QueryRow(ctx, selectionScopeCTE+`SELECT `+selectionChoiceExpr+` FROM scoped WHERE domain_id=$4`,
		tenant, allowed, s.policyRevision(), in.DomainID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, problem(404, "not_found")
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	// Never reveal a revision conflict until the currently active row has passed
	// tenant and exact resource scope checks in this same read.
	if out.Revision != in.ExpectedRevision || out.CredentialRevision != in.ExpectedCredentialRevision {
		return Choice{}, problem(409, "selection_changed")
	}
	selectionUTC(&out)
	return out, nil
}

func selectionUTC(c *Choice) {
	if c.LastTest != nil {
		c.LastTest.ObservedAt = c.LastTest.ObservedAt.UTC()
	}
}
