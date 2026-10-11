package domains

const DirectoryAuditV3Schema = `
ALTER TABLE adtr.domain_audit DROP CONSTRAINT domain_audit_action_check;
ALTER TABLE adtr.domain_audit ADD CONSTRAINT domain_audit_action_check CHECK(action IN ('domain_create','domain_update','domain_delete','domain_test_submit','domain_test_result','domain_credential_reference','domain_credential_custom','domain_credential_detach','domain_directory_submit','domain_directory_cancel','domain_directory_result','domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result','domain_directory_v3_submit','domain_directory_v3_cancel','domain_directory_v3_result'));
`
