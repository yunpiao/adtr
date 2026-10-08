package audit

import "strings"

// AccountReferenceViewSchema changes only the migration-13 projection. Historical
// producer migrations stay reproducible, and account/grant pointers remain
// absent from ordinary domain audit and export metadata.
var AccountReferenceViewSchema = func() string {
	s := strings.ReplaceAll(CredentialUseViewSchema,
		"'domain_create','domain_update','domain_delete','domain_test_submit'",
		"'domain_create','domain_update','domain_delete','domain_test_submit','domain_credential_reference','domain_credential_custom','domain_credential_detach'")
	s = strings.Replace(s, "'domainId',a.domain_id,'oldRevision'", "'domainId',a.domain_id,'credentialSource',a.credential_source,'oldRevision'", 1)
	return strings.Replace(s, "t.tenant_id=a.tenant_id AND t.task_id=a.task_id",
		"t.tenant_id=a.tenant_id AND t.domain_id=a.domain_id AND t.task_id=a.task_id AND t.kind IN ('domain.connection_test','domain.account_connection_test')", 1)
}()
