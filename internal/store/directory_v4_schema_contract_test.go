package store

import (
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"regexp"
	"testing"
)

func TestSchema20BaselineAccountsForDigitBearingHistoricalValidators(t *testing.T) {
	schema := auth.Schema + auth.TaskAuthorizationSchema + audit.Schema + domains.Schema + credentialuse.Schema + domains.AccountReferenceSchema + credentialuse.DirectoryPurposeSchema + domains.DirectoryDependencySchema + domains.DirectoryUseSchema + domains.DirectoryObservationSchema + audit.DirectoryViewSchema + credentialuse.DirectoryV2PurposeSchema + domains.DirectoryDependencyV2Schema + domains.DirectoryUseV2Schema + domains.DirectoryObservationV2Schema + credentialuse.DirectoryV3PurposeSchema + domains.DirectoryDependencyV3Schema + domains.DirectoryUseV3Schema + domains.DirectoryObservationV3Schema
	functions := regexp.MustCompile(`(?s)CREATE(?: OR REPLACE)? FUNCTION adtr\.([a-z_][a-z0-9_]*)\(([^)]*)\) RETURNS (boolean|trigger|void|bigint|text) LANGUAGE (sql|SQL|plpgsql)( STABLE| IMMUTABLE)?( STRICT)? AS \$\$(.*?)\$\$;`).FindAllStringSubmatch(schema, -1)
	declarations := regexp.MustCompile(`CREATE(?: OR REPLACE)? FUNCTION adtr\.[a-z_][a-z0-9_]*\(`).FindAllString(schema, -1)
	if len(functions) == 0 || len(functions) != len(declarations) {
		t.Fatalf("parsed %d of %d declarations", len(functions), len(declarations))
	}
	names := map[string]bool{}
	for _, f := range functions {
		names[f[1]] = true
	}
	for _, name := range []string{"domain_directory_observation_canonical_v2", "domain_directory_observation_base64_v2", "domain_directory_observation_canonical_v3", "domain_directory_identity_v3"} {
		if !names[name] {
			t.Fatalf("historical validator %s omitted", name)
		}
	}
}
