package simulationdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationrisk"
)

type eventSource struct {
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
	Bytes   int             `json:"bytes"`
}
type caseSource struct {
	ID            string          `json:"id"`
	State         string          `json:"state"`
	Mark          string          `json:"mark"`
	Revision      uint64          `json:"revision"`
	UpdatedAt     *time.Time      `json:"updatedAt"`
	Audit         json.RawMessage `json:"audit"`
	AuditBytes    int             `json:"auditBytes"`
	EvidenceCount int             `json:"evidenceCount"`
}
type detectionSource struct {
	ID             string          `json:"id"`
	CompletedAt    *time.Time      `json:"completedAt"`
	PayloadVersion int             `json:"payloadVersion"`
	ResultVersion  int64           `json:"resultVersion"`
	Payload        json.RawMessage `json:"payload"`
	Result         json.RawMessage `json:"result"`
	Template       json.RawMessage `json:"template"`
	TemplateHash   string          `json:"templateHash"`
	PayloadBytes   int             `json:"payloadBytes"`
	ResultBytes    int             `json:"resultBytes"`
	TemplateBytes  int             `json:"templateBytes"`
	Intended       bool            `json:"intended"`
}
type source struct {
	Oversized bool                           `json:"oversized"`
	AsOf      time.Time                      `json:"asOf"`
	Dataset   simulationevents.Dataset       `json:"dataset"`
	Events    []eventSource                  `json:"events"`
	Cases     []caseSource                   `json:"cases"`
	Resets    []simulationrisk.SnapshotReset `json:"resets"`
	Detection *detectionSource               `json:"detection"`
}

// sourceSQL is deliberately ONE statement: every source and statement_timestamp
// shares its PostgreSQL MVCC snapshot after the caller's live authorization.
// LIMIT+1 detects overflow rather than silently truncating. Oversized JSON bodies
// become null sentinels with byte counts BEFORE aggregation and fail closed.
const sourceSQL = `
WITH d AS MATERIALIZED (
 SELECT * FROM adtr.simulation_event_datasets
 WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3
), ev AS MATERIALIZED (
 SELECT event_id,payload,octet_length(payload::text) bytes FROM adtr.simulation_events
 WHERE (tenant_id,domain_id,dataset_id,fixture_hash)=(SELECT tenant_id,domain_id,dataset_id,fixture_hash FROM d)
 ORDER BY event_id LIMIT 4097
), cs AS MATERIALIZED (
 SELECT * FROM adtr.simulation_event_cases
 WHERE (tenant_id,domain_id,dataset_id,fixture_hash)=(SELECT tenant_id,domain_id,dataset_id,fixture_hash FROM d)
 ORDER BY case_id LIMIT 101
), rs AS MATERIALIZED (
 SELECT revision,reset_id,at,reason,actor,operation_id FROM adtr.simulation_risk_resets
 WHERE (tenant_id,domain_id,dataset_id,fixture_hash)=(SELECT tenant_id,domain_id,dataset_id,fixture_hash FROM d) AND subject=$4
 ORDER BY revision LIMIT 1025
), latest AS MATERIALIZED (
 SELECT t.* FROM adtr.tasks t
 LEFT JOIN adtr.task_visibility v ON v.tenant_id=t.tenant_id AND v.task_id=t.task_id
 WHERE t.tenant_id=$1 AND t.domain_id=$2 AND t.actor_id=$5 AND t.authorization_version=$6
 AND t.kind='simulation.detection' AND t.state='succeeded' AND NOT COALESCE(v.archived,false)
 AND t.payload->>'datasetId'=$3 AND t.payload->>'fixtureHash'=(SELECT fixture_hash FROM d)
 ORDER BY t.terminal_at DESC NULLS LAST,t.task_id DESC LIMIT 1
)
SELECT jsonb_build_object(
 'asOf',statement_timestamp(),
 'oversized',EXISTS(SELECT 1 FROM rs WHERE octet_length(reset_id)>128 OR octet_length(actor)>128 OR octet_length(operation_id)>128 OR octet_length(reason)>512),
 'dataset',jsonb_build_object('id',d.dataset_id,'fixture',d.fixture,'fixtureHash',d.fixture_hash,'contract',d.contract,'simulation',d.simulation,'eventCount',d.event_count),
 'events',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',CASE WHEN octet_length(event_id)<=128 THEN event_id ELSE '' END,'bytes',bytes,'payload',CASE WHEN bytes<=4096 THEN payload ELSE NULL END) ORDER BY event_id) FROM ev),'[]'::jsonb),
 'cases',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',CASE WHEN octet_length(c.case_id)<=128 THEN c.case_id ELSE '' END,'state',c.state,'mark',c.mark,'revision',c.revision,'updatedAt',c.updated_at,
 'auditBytes',COALESCE(octet_length(a.audit::text),0),'audit',CASE WHEN octet_length(a.audit::text)<=4096 THEN a.audit ELSE NULL END,
 'evidenceCount',(SELECT count(*) FROM (SELECT 1 FROM adtr.simulation_event_case_evidence e WHERE (e.tenant_id,e.domain_id,e.dataset_id,e.fixture_hash,e.case_id)=(c.tenant_id,c.domain_id,c.dataset_id,c.fixture_hash,c.case_id) LIMIT 4097) bounded)
 ) ORDER BY c.case_id) FROM cs c LEFT JOIN adtr.simulation_event_transitions a ON (a.tenant_id,a.domain_id,a.dataset_id,a.fixture_hash,a.case_id,a.revision)=(c.tenant_id,c.domain_id,c.dataset_id,c.fixture_hash,c.case_id,c.revision)),'[]'::jsonb),
 'resets',COALESCE((SELECT jsonb_agg(jsonb_build_object('revision',revision,'id',CASE WHEN octet_length(reset_id)<=128 THEN reset_id ELSE '' END,'at',at,'reason',CASE WHEN octet_length(reason)<=512 THEN reason ELSE '' END,'actor',CASE WHEN octet_length(actor)<=128 THEN actor ELSE '' END,'operationId',CASE WHEN octet_length(operation_id)<=128 THEN operation_id ELSE '' END) ORDER BY revision) FROM rs),'[]'::jsonb),
 'detection',(SELECT jsonb_build_object('id',CASE WHEN octet_length(t.task_id)<=128 THEN t.task_id ELSE '' END,'completedAt',t.terminal_at,'payloadVersion',t.payload_version,'resultVersion',t.result_version,
 'payloadBytes',octet_length(t.payload::text),'resultBytes',octet_length(t.result::text),'templateBytes',COALESCE(octet_length(v.body::text),0),
 'payload',CASE WHEN octet_length(t.payload::text)<=4096 THEN t.payload ELSE NULL END,
 'result',CASE WHEN octet_length(t.result::text)<=120000 THEN t.result ELSE NULL END,
 'template',CASE WHEN octet_length(v.body::text)<=120000 THEN v.body ELSE NULL END,'templateHash',COALESCE(v.hash,''),
 'intended',EXISTS(SELECT 1 FROM adtr.simulation_detection_receipts r WHERE r.tenant_id=$1 AND r.domain_id=$2 AND r.actor_id=$5 AND r.receipt->>'kind'='run' AND r.receipt->'submission'->'task'->>'taskUUID'=t.task_id)
 ) FROM latest t LEFT JOIN adtr.simulation_detection_versions v ON v.tenant_id=t.tenant_id AND v.domain_id=t.domain_id AND v.template_id=t.payload->>'templateId' AND v.version::text=t.payload->>'templateVersion')
) FROM d`

// SummaryTx performs no writes, commits, locks or additional source queries.
// The caller owns auth/schema gates, the transaction and rollback on any error.
func SummaryTx(ctx context.Context, tx pgx.Tx, s Scope, in Input) (Summary, error) {
	if tx == nil || !s.Read || !valid(s.Tenant, 128) || !valid(s.Domain, 128) || s.ActorID <= 0 || !valid(s.AuthorizationVersion, 4096) {
		return Summary{}, ErrDenied
	}
	if !valid(in.DatasetID, 128) || (in.Subject != "" && !valid(in.Subject, 128)) {
		return Summary{}, ErrInvalid
	}
	if _, _, e := windowSpec(in.Window); e != nil {
		return Summary{}, e
	}
	var b []byte
	if e := tx.QueryRow(ctx, sourceSQL, s.Tenant, s.Domain, in.DatasetID, in.Subject, s.ActorID, s.AuthorizationVersion).Scan(&b); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return Summary{}, ErrNotFound
		}
		return Summary{}, e
	}
	if len(b) > 24<<20 {
		return Summary{}, ErrLimit
	}
	var src source
	if e := json.Unmarshal(b, &src); e != nil {
		return Summary{}, ErrInvalid
	}
	return aggregate(ctx, s, in, src)
}
