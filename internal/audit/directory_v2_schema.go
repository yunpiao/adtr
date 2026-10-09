package audit

import "strings"

// DirectoryV2ViewSchema extends only the v2 vocabulary. Historical joins and
// projections retain their exact meaning; v2 results require a matching v2 task.
var DirectoryV2ViewSchema = func() string {
	s := strings.ReplaceAll(DirectoryViewSchema,
		"'domain_directory_submit','domain_directory_cancel'",
		"'domain_directory_submit','domain_directory_cancel','domain_directory_v2_submit','domain_directory_v2_cancel'")
	s = strings.ReplaceAll(s, "'domain_test_result','domain_directory_result'",
		"'domain_test_result','domain_directory_result','domain_directory_v2_result'")
	// A missing or wrong-profile v2 task has no available result. Keep the
	// historical expression unchanged for every other action, including NULL.
	s = strings.Replace(s,
		"(a.action IN ('domain_test_result','domain_directory_result','domain_directory_v2_result') AND t.state IN ('succeeded','failed','partial_failed','dead_letter'))",
		"(a.action IN ('domain_test_result','domain_directory_result','domain_directory_v2_result') AND t.state IN ('succeeded','failed','partial_failed','dead_letter') AND (a.action<>'domain_directory_v2_result' OR t.task_id IS NOT NULL))", 1)
	return strings.Replace(s,
		"t.kind IN ('domain.connection_test','domain.account_connection_test','domain.directory_read')",
		"((a.action IN ('domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result') AND t.kind='domain.directory_read.v2') OR (a.action NOT IN ('domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result') AND t.kind IN ('domain.connection_test','domain.account_connection_test','domain.directory_read')))", 1)
}()
