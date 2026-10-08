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
	return strings.Replace(s,
		"t.kind IN ('domain.connection_test','domain.account_connection_test','domain.directory_read')",
		"((a.action IN ('domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result') AND t.kind='domain.directory_read.v2') OR (a.action NOT IN ('domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result') AND t.kind IN ('domain.connection_test','domain.account_connection_test','domain.directory_read')))", 1)
}()
