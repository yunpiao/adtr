package audit

import "strings"

// CredentialUseViewSchema is refreshed only after migration12 creates its
// producer. Every row carries domain provenance and is a protected control.
var CredentialUseViewSchema = strings.Replace(
	strings.TrimSuffix(strings.TrimSpace(OperationAccountViewSchema), ";"),
	"source NOT IN ('audit','domain','operation_account')",
	"source NOT IN ('audit','domain','operation_account','credential_use')", 1,
) + `
UNION ALL
SELECT 'credential_use',a.id,a.tenant_id,NULLIF(a.actor_id,0),a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('domainId',a.domain_id,'accountId',a.account_id,'roleId',a.role_id,'purpose',a.purpose,'oldGrantRevision',a.old_grant_revision::text,'grantRevision',a.new_grant_revision::text,'accountCredentialRevision',a.account_credential_revision::text,'allowed',a.allowed,'code',a.result,'path',a.audit_path,'requestId',a.audit_request_id)),
 'SUCCESS',true,a.occurred_at,8,a.domain_id,NULL::text,a.audit_path,a.audit_request_id FROM adtr.operation_account_use_audit a;
`
