package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"regexp"
	"strings"
)

var ErrDirectoryV3ReservedCollision = errors.New("migration blocked: reserved directory v3 identity already exists")
var ErrDirectoryV3SchemaShape = errors.New("migration blocked: unexpected historical directory v3 baseline")

func directoryV3SchemaCheckFailed(n uint8) error {
	return fmt.Errorf("%w (check %d)", ErrDirectoryV3SchemaShape, n)
}
func requireDirectoryV3Baseline(ctx context.Context, tx pgx.Tx) error {
	var exclusive, collision bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode='ExclusiveLock'),
 EXISTS(SELECT FROM adtr.tasks WHERE kind='domain.directory_read.v3') OR EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read.v3')
 OR EXISTS(SELECT FROM adtr.domain_directory_task_uses WHERE purpose='domain.directory_read.v3' OR dictionary_version=3)
 OR EXISTS(SELECT FROM adtr.domain_directory_observations WHERE dictionary_version=3)
 OR EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v3') OR EXISTS(SELECT FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read.v3') OR EXISTS(SELECT FROM adtr.operation_account_use_audit WHERE purpose='domain.directory_read.v3')
 OR EXISTS(SELECT FROM adtr.domain_audit WHERE action IN ('domain_directory_v3_submit','domain_directory_v3_cancel','domain_directory_v3_result'))
 OR EXISTS(SELECT FROM pg_proc WHERE pronamespace='adtr'::regnamespace AND proname IN ('domain_directory_observation_canonical_v3','domain_directory_identity_v3'))`).Scan(&exclusive, &collision); err != nil || !exclusive {
		return directoryV3SchemaCheckFailed(1)
	}
	if collision {
		return ErrDirectoryV3ReservedCollision
	}
	if err := directoryV3HistoricalFunctionsAndTriggers(ctx, tx); err != nil {
		return err
	}
	// Parse the exact historical CHECK definitions into an empty temp relation;
	// compare server-normalized definitions, not PostgreSQL-version strings.
	sql := `CREATE TEMP TABLE adtr_directory_v3_expected_checks(purpose text,consumer_kind text,account_credential_revision bigint,connection_credential_generation bigint,action text,dictionary_version integer) ON COMMIT DROP;`
	pattern := regexp.MustCompile(`ALTER TABLE adtr\.([a-z_]+) ADD CONSTRAINT ([a-z_]+) CHECK\(([^;]+);`)
	matches := pattern.FindAllStringSubmatch(credentialuse.DirectoryV2PurposeSchema+domains.DirectoryDependencyV2Schema+domains.DirectoryAuditV2Schema+domains.DirectoryUseV2Schema, -1)
	if len(matches) != 6 {
		return directoryV3SchemaCheckFailed(2)
	}
	for _, m := range matches {
		sql += "ALTER TABLE pg_temp.adtr_directory_v3_expected_checks ADD CONSTRAINT " + pgx.Identifier{m[2]}.Sanitize() + " CHECK(" + m[3] + ";\n"
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		return directoryV3SchemaCheckFailed(3)
	}
	for _, m := range matches {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_constraint a JOIN pg_constraint e ON e.conrelid='pg_temp.adtr_directory_v3_expected_checks'::regclass AND e.conname=a.conname WHERE a.conrelid=to_regclass($1) AND a.conname=$2 AND a.contype='c' AND a.convalidated AND NOT a.connoinherit AND pg_get_constraintdef(a.oid)=pg_get_constraintdef(e.oid))`, "adtr."+m[1], m[2]).Scan(&valid); err != nil || !valid {
			return directoryV3SchemaCheckFailed(4)
		}
	}
	return directoryV3Tables(ctx, tx)
}
func directoryV3HistoricalFunctionsAndTriggers(ctx context.Context, tx pgx.Tx) error {
	// Preserve migration order and take the last historical replacement. The
	// widened purpose depends on every governance guard, receipt, audit event
	// and epoch helper, not merely on the functions replaced by migration16.
	schema := auth.Schema + auth.TaskAuthorizationSchema + audit.Schema + domains.Schema + credentialuse.Schema + domains.AccountReferenceSchema + credentialuse.DirectoryPurposeSchema + domains.DirectoryDependencySchema + domains.DirectoryUseSchema + domains.DirectoryObservationSchema + audit.DirectoryViewSchema + credentialuse.DirectoryV2PurposeSchema + domains.DirectoryDependencyV2Schema + domains.DirectoryUseV2Schema + domains.DirectoryObservationV2Schema
	pattern := regexp.MustCompile(`(?s)CREATE(?: OR REPLACE)? FUNCTION adtr\.([a-z_]+)\(([^)]*)\) RETURNS (boolean|trigger|void|bigint|text) LANGUAGE (sql|SQL|plpgsql)( STABLE| IMMUTABLE)?( STRICT)? AS \$\$(.*?)\$\$;`)
	matches := pattern.FindAllStringSubmatch(schema, -1)
	declarations := regexp.MustCompile(`CREATE(?: OR REPLACE)? FUNCTION adtr\.[a-z_]+\(`).FindAllString(schema, -1)
	if len(matches) == 0 || len(matches) != len(declarations) {
		return directoryV3SchemaCheckFailed(12)
	}
	functions := make(map[string][]string, len(matches))
	for _, match := range matches {
		functions[match[1]] = match
	}
	for _, match := range functions {
		var args, argNames []string
		for _, arg := range strings.Split(match[2], ",") {
			parts := strings.Fields(arg)
			if len(parts) > 0 {
				if len(parts) != 2 {
					return directoryV3SchemaCheckFailed(13)
				}
				argNames = append(argNames, parts[0])
				args = append(args, parts[len(parts)-1])
			}
		}
		volatility := "v"
		if match[5] == " STABLE" {
			volatility = "s"
		} else if match[5] == " IMMUTABLE" {
			volatility = "i"
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=to_regprocedure($1) AND p.prosrc=$2 AND l.lanname=$3 AND p.prorettype=to_regtype($4) AND p.provolatile::text=$5 AND p.proargnames IS NOT DISTINCT FROM $6::text[] AND p.proargmodes IS NULL AND p.proallargtypes IS NULL AND p.provariadic=0 AND NOT p.proretset AND p.proparallel='u' AND p.prokind='f' AND NOT p.prosecdef AND p.proisstrict=$7 AND NOT p.proleakproof AND p.proconfig IS NULL AND p.pronargdefaults=0 AND (SELECT count(*) FROM pg_proc q WHERE q.pronamespace=p.pronamespace AND q.proname=p.proname)=1)`, "adtr."+match[1]+"("+strings.Join(args, ",")+")", match[7], strings.ToLower(match[4]), match[3], volatility, argNames, match[6] != "").Scan(&valid); err != nil || !valid {
			return directoryV3SchemaCheckFailed(14)
		}
	}
	// The original guard remains authoritative after dictionary provenance is
	// added. Verify its installed trigger identity, timing, events and deferred
	// consistency behavior; an attacker-controlled replacement is never adopted.
	triggerPattern := regexp.MustCompile(`CREATE( CONSTRAINT)? TRIGGER ([a-z_]+) (BEFORE|AFTER) ([A-Z ]+) ON adtr\.([a-z_]+)( DEFERRABLE INITIALLY DEFERRED)?\s+FOR EACH (ROW|STATEMENT) EXECUTE FUNCTION adtr\.([a-z_]+)\(\);`)
	triggerMatches := triggerPattern.FindAllStringSubmatch(schema, -1)
	triggerDeclarations := regexp.MustCompile(`CREATE(?: CONSTRAINT)? TRIGGER [a-z_]+ `).FindAllString(schema, -1)
	if len(triggerMatches) == 0 || len(triggerMatches) != len(triggerDeclarations) {
		return directoryV3SchemaCheckFailed(15)
	}
	triggers := make(map[string][]string, len(triggerMatches))
	for _, match := range triggerMatches {
		triggers[match[5]+"."+match[2]] = match
	}
	for _, match := range triggers {
		bits := 0
		if match[3] == "BEFORE" {
			bits |= 2
		}
		if match[7] == "ROW" {
			bits |= 1
		}
		for event, bit := range map[string]int{"INSERT": 4, "DELETE": 8, "UPDATE": 16, "TRUNCATE": 32} {
			if strings.Contains(match[4], event) {
				bits |= bit
			}
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_trigger WHERE tgrelid=to_regclass($1) AND tgname=$2 AND tgfoid=to_regprocedure($3) AND tgtype=$4 AND tgenabled='O' AND NOT tgisinternal AND tgdeferrable=$5 AND tginitdeferred=$5 AND tgattr::text='' AND octet_length(tgargs)=0 AND tgqual IS NULL)`, "adtr."+match[5], match[2], "adtr."+match[8]+"()", bits, match[6] != "").Scan(&valid); err != nil || !valid {
			return directoryV3SchemaCheckFailed(16)
		}
	}
	for _, table := range []string{"domain_directory_task_uses", "domain_directory_observations", "domain_audit", "operation_account_use_grants", "operation_account_use_mutations", "operation_account_use_audit"} {
		var expected int
		for _, match := range triggers {
			if match[5] == table {
				expected++
			}
		}
		var actual int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid=to_regclass($1) AND NOT tgisinternal`, "adtr."+table).Scan(&actual); err != nil || actual != expected {
			return directoryV3SchemaCheckFailed(17)
		}
	}
	return nil
}

func directoryV3Tables(ctx context.Context, tx pgx.Tx) error {
	for _, d := range []struct{ schema, table, reference string }{
		{domains.DirectoryUseSchema, "domain_directory_task_uses", "adtr_directory_v3_expected_uses"},
		{domains.DirectoryObservationSchema, "domain_directory_observations", "adtr_directory_v3_expected_observations"},
	} {
		sql, err := directoryV2ReferenceTable(d.schema, d.table, d.reference)
		if err != nil {
			return directoryV3SchemaCheckFailed(30)
		}
		name := pgx.Identifier{"pg_temp", d.reference}.Sanitize()
		sql += "ALTER TABLE " + name + " ADD COLUMN dictionary_version integer NOT NULL DEFAULT 1;"
		if d.table == "domain_directory_task_uses" {
			sql += "ALTER TABLE " + name + " DROP CONSTRAINT " + pgx.Identifier{d.reference + "_purpose_check"}.Sanitize() + "; ALTER TABLE " + name + " ADD CONSTRAINT domain_directory_v3_expected_profile CHECK((purpose='domain.directory_read' AND dictionary_version=1) OR (purpose='domain.directory_read.v2' AND dictionary_version=2));"
		} else {
			sql += "ALTER TABLE " + name + " ADD CONSTRAINT domain_directory_v3_expected_version CHECK(dictionary_version IN (1,2)); CREATE INDEX adtr_directory_v3_expected_latest ON " + name + "(tenant_id,domain_id,dictionary_version,created_at DESC,task_id);"
		}
		if _, err = tx.Exec(ctx, sql); err != nil {
			return directoryV3SchemaCheckFailed(31)
		}
		for _, snapshot := range []string{directoryV2Columns, directoryV2Checks, directoryV2Indexes} {
			var actual, expected string
			if err = tx.QueryRow(ctx, snapshot, "adtr."+d.table).Scan(&actual); err != nil {
				return directoryV3SchemaCheckFailed(32)
			}
			if err = tx.QueryRow(ctx, snapshot, "pg_temp."+d.reference).Scan(&expected); err != nil || actual != expected {
				return directoryV3SchemaCheckFailed(33)
			}
		}
	}
	// Validate the real foreign keys separately: PostgreSQL does not allow a
	// temporary reference table to have a foreign key to a permanent table.
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT
 (SELECT count(*)=2
 AND count(*) FILTER(WHERE conkey=ARRAY[3]::smallint[] AND confrelid='adtr.tasks'::regclass AND confkey=ARRAY[1]::smallint[])=1
 AND count(*) FILTER(WHERE conkey=ARRAY[1,2,4]::smallint[] AND confrelid='adtr.operation_accounts'::regclass AND confkey=ARRAY[1,2,3]::smallint[])=1
 AND bool_and(convalidated AND NOT condeferrable AND NOT condeferred AND confupdtype='a' AND confdeltype='a' AND confmatchtype='s' AND
 ((conkey=ARRAY[3]::smallint[] AND confrelid='adtr.tasks'::regclass AND confkey=ARRAY[1]::smallint[]) OR
 (conkey=ARRAY[1,2,4]::smallint[] AND confrelid='adtr.operation_accounts'::regclass AND confkey=ARRAY[1,2,3]::smallint[]))) FROM pg_constraint WHERE conrelid='adtr.domain_directory_task_uses'::regclass AND contype='f')
 AND (SELECT count(*)=1 AND bool_and(convalidated AND NOT condeferrable AND NOT condeferred AND confupdtype='a' AND confdeltype='a' AND confmatchtype='s' AND conkey=ARRAY[1,2,3]::smallint[] AND confrelid='adtr.domain_directory_task_uses'::regclass AND confkey=ARRAY[1,2,3]::smallint[]) FROM pg_constraint WHERE conrelid='adtr.domain_directory_observations'::regclass AND contype='f')
 AND NOT EXISTS(SELECT FROM pg_class WHERE oid IN ('adtr.domain_directory_task_uses'::regclass,'adtr.domain_directory_observations'::regclass) AND (relkind<>'r' OR relpersistence<>'p' OR relrowsecurity OR relforcerowsecurity OR relispartition))
 AND NOT EXISTS(SELECT FROM pg_inherits WHERE inhrelid IN ('adtr.domain_directory_task_uses'::regclass,'adtr.domain_directory_observations'::regclass) OR inhparent IN ('adtr.domain_directory_task_uses'::regclass,'adtr.domain_directory_observations'::regclass))`).Scan(&valid); err != nil || !valid {
		return directoryV3SchemaCheckFailed(5)
	}

	const prefix = "CREATE OR REPLACE VIEW adtr.audit_source AS"
	pos := strings.Index(audit.DirectoryV2ViewSchema, prefix)
	if pos < 0 {
		return directoryV3SchemaCheckFailed(34)
	}
	if _, err := tx.Exec(ctx, "CREATE TEMP VIEW adtr_directory_v3_expected_audit AS"+audit.DirectoryV2ViewSchema[pos+len(prefix):]); err != nil {
		return directoryV3SchemaCheckFailed(35)
	}
	if err := tx.QueryRow(ctx, `SELECT pg_get_viewdef('adtr.audit_source'::regclass)=pg_get_viewdef('pg_temp.adtr_directory_v3_expected_audit'::regclass) AND EXISTS(SELECT FROM pg_class WHERE oid='adtr.audit_source'::regclass AND relkind='v' AND reloptions IS NULL)`).Scan(&valid); err != nil || !valid {
		return directoryV3SchemaCheckFailed(36)
	}
	return nil
}
