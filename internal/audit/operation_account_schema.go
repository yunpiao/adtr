package audit

import "strings"

// OperationAccountViewSchema is installed only after the migration-10 producer.
// The SQL scope and snapshot checks apply to every domain-bearing source.
var OperationAccountViewSchema = strings.Replace(
	strings.TrimSuffix(strings.TrimSpace(domainProjection), ";"),
	"source NOT IN ('audit','domain')", "source NOT IN ('audit','domain','operation_account')", 1,
) + `
UNION ALL
SELECT 'operation_account',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('domainId',a.domain_id,'accountId',a.account_id,'oldRevision',a.old_revision::text,'revision',a.new_revision::text,'oldCredentialRevision',a.old_credential_revision::text,'credentialRevision',a.new_credential_revision::text,'code',a.result,'path',a.audit_path,'requestId',a.audit_request_id)),
 'SUCCESS',true,a.occurred_at,9,a.domain_id,NULL::text,a.audit_path,a.audit_request_id FROM adtr.operation_account_audit a;
`
