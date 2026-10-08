package audit

import "strings"

// Only schema14 installs this producer. Control records contain no journal data.
var OperationalLogViewSchema = strings.Replace(
	strings.TrimSuffix(strings.TrimSpace(AccountReferenceViewSchema), ";"),
	"source NOT IN ('audit','domain','operation_account','credential_use')",
	"source NOT IN ('audit','domain','operation_account','credential_use','operational_log')", 1,
) + `
UNION ALL
SELECT 'operational_log',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,'system_logs_'||a.action,
 jsonb_strip_nulls(jsonb_build_object('taskUUID',a.task_id,'path',a.audit_path,'requestId',a.audit_request_id)),
 'SUCCESS',true,a.occurred_at,9,NULL::text,NULL::text,a.audit_path,a.audit_request_id FROM adtr.operational_log_audit a;
`
