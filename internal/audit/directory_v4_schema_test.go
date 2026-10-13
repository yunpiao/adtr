package audit

import (
	"strings"
	"testing"
)

func TestDirectoryV4AuditViewKeepsExactActionKindJoins(t *testing.T) {
	for _, version := range []string{"2", "3", "4"} {
		exact := "(a.action IN ('domain_directory_v" + version + "_submit','domain_directory_v" + version + "_cancel','domain_directory_v" + version + "_result') AND t.kind='domain.directory_read.v" + version + "')"
		if !strings.Contains(DirectoryV4ViewSchema, exact) {
			t.Fatal("missing exact versioned audit branch", version)
		}
	}
	if !strings.Contains(DirectoryV4ViewSchema, "a.action NOT IN ('domain_directory_v2_result','domain_directory_v3_result','domain_directory_v4_result') OR t.task_id IS NOT NULL") {
		t.Fatal("versioned orphan result was exposed")
	}
	if strings.Contains(DirectoryV3ViewSchema, "domain_directory_v4") {
		t.Fatal("historical v3 view changed")
	}
}
