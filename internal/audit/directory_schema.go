package audit

import "strings"

// DirectoryViewSchema preserves historical producer schemas. Result events are
// successful only after the associated directory task actually succeeds; a
// staged snapshot or accepted cancel request is not execution-end evidence.
var DirectoryViewSchema = func() string {
	s := strings.ReplaceAll(OperationalLogViewSchema,
		"'domain_create','domain_update','domain_delete','domain_test_submit','domain_credential_reference','domain_credential_custom','domain_credential_detach'",
		"'domain_create','domain_update','domain_delete','domain_test_submit','domain_credential_reference','domain_credential_custom','domain_credential_detach','domain_directory_submit','domain_directory_cancel'")
	s = strings.ReplaceAll(s, "a.action<>'domain_test_result'", "a.action NOT IN ('domain_test_result','domain_directory_result')")
	s = strings.ReplaceAll(s, "a.action='domain_test_result'", "a.action IN ('domain_test_result','domain_directory_result')")
	return strings.ReplaceAll(s, "t.kind IN ('domain.connection_test','domain.account_connection_test')", "t.kind IN ('domain.connection_test','domain.account_connection_test','domain.directory_read')")
}()
