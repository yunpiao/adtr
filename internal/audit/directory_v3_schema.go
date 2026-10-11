package audit

import "strings"

// Exact action-to-kind joins preserve old audit projections and isolate v3.
var DirectoryV3ViewSchema = func() string {
	s := strings.ReplaceAll(DirectoryViewSchema, "'domain_directory_submit','domain_directory_cancel'", "'domain_directory_submit','domain_directory_cancel','domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v3_submit','domain_directory_v3_cancel'")
	s = strings.ReplaceAll(s, "'domain_test_result','domain_directory_result'", "'domain_test_result','domain_directory_result','domain_directory_v2_result','domain_directory_v3_result'")
	s = strings.Replace(s, "(a.action IN ('domain_test_result','domain_directory_result','domain_directory_v2_result','domain_directory_v3_result') AND t.state IN ('succeeded','failed','partial_failed','dead_letter'))", "(a.action IN ('domain_test_result','domain_directory_result','domain_directory_v2_result','domain_directory_v3_result') AND t.state IN ('succeeded','failed','partial_failed','dead_letter') AND (a.action NOT IN ('domain_directory_v2_result','domain_directory_v3_result') OR t.task_id IS NOT NULL))", 1)
	return strings.Replace(s, "t.kind IN ('domain.connection_test','domain.account_connection_test','domain.directory_read')", "((a.action IN ('domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result') AND t.kind='domain.directory_read.v2') OR (a.action IN ('domain_directory_v3_submit','domain_directory_v3_cancel','domain_directory_v3_result') AND t.kind='domain.directory_read.v3') OR (a.action NOT IN ('domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result','domain_directory_v3_submit','domain_directory_v3_cancel','domain_directory_v3_result') AND t.kind IN ('domain.connection_test','domain.account_connection_test','domain.directory_read')))", 1)
}()
