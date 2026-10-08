package audit

import "strings"

// DomainViewSchema is installed only by migration 9, after domain tables exist.
// Earlier migrations retain their table-independent projection. Domain controls
// are immutable and cannot be hidden through the presentation visibility API.
var domainProjection = strings.Replace(
	strings.TrimSuffix(strings.TrimSpace(ViewSchema), ";"),
	"source<>'audit'", "source NOT IN ('audit','domain')", 1,
) + `
UNION ALL
SELECT 'domain',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('domainId',a.domain_id,'oldRevision',a.old_revision::text,'revision',a.new_revision::text,'credentialRevision',a.credential_revision::text,'taskUUID',NULLIF(a.task_id,''),'code',CASE WHEN a.action<>'domain_test_result' THEN a.result END,'observationCode',CASE WHEN a.action='domain_test_result' THEN a.result END,'taskState',CASE WHEN a.action='domain_test_result' THEN t.state END,'path',a.audit_path,'requestId',a.audit_request_id)),
 CASE WHEN a.action IN ('domain_create','domain_update','domain_delete','domain_test_submit') THEN 'SUCCESS' WHEN a.action='domain_test_result' AND t.state='succeeded' AND a.result='success' THEN 'SUCCESS' WHEN a.action='domain_test_result' AND t.state IN ('failed','partial_failed','dead_letter') THEN 'FAIL' ELSE 'NONE' END,
 (a.action IN ('domain_create','domain_update','domain_delete','domain_test_submit') OR (a.action='domain_test_result' AND t.state IN ('succeeded','failed','partial_failed','dead_letter'))),
 a.occurred_at,9,a.domain_id,NULL::text,a.audit_path,a.audit_request_id FROM adtr.domain_audit a LEFT JOIN adtr.tasks t ON t.tenant_id=a.tenant_id AND t.task_id=a.task_id;
`

var DomainViewSchema = domainProjection + `
DROP INDEX adtr.audit_export_domains;
CREATE INDEX audit_export_domains ON adtr.audit_export_rows(task_id,domain_id) WHERE domain_id IS NOT NULL;
`
