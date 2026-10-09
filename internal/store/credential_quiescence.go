package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
)

var ErrOperationalLogKindCollision = errors.New("migration blocked: reserved operational log task kind already exists")

var ErrDirectoryTaskKindCollision = errors.New("migration blocked: reserved directory task kind already exists")
var ErrDirectoryDependencyKindCollision = errors.New("migration blocked: reserved directory dependency kind already exists")
var ErrDirectoryV2ReservedCollision = errors.New("migration blocked: reserved directory v2 identity already exists")
var ErrDirectoryV2SchemaShape = errors.New("migration blocked: unexpected historical directory schema")

// ErrCredentialUseMigrationBlocked is deliberately free of tenant, account,
// worker and task identifiers. Migration cannot manufacture execution evidence.
var ErrCredentialUseMigrationBlocked = errors.New("migration blocked: account credential use is not confirmed quiescent")

// A stopped executor still needs its original exact-schema acknowledgement
// transaction. Refuse the upgrade promptly while any such use remains opened;
// do not wait under the exclusive gate that the acknowledgement itself needs.
func requireCredentialUseQuiescence(ctx context.Context, tx pgx.Tx, installed, target int) error {
	if installed < 13 || installed >= target {
		return nil
	}
	var exclusive, opened bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode='ExclusiveLock'),EXISTS(SELECT FROM adtr.domain_account_task_uses WHERE state='opened')`).Scan(&exclusive, &opened)
	if err != nil {
		return errors.New("credential-use migration check failed")
	}
	if !exclusive {
		return errors.New("exclusive migration gate required")
	}
	// The directory ledger first exists in schema15. Earlier upgrades must not
	// query a missing relation; later upgrades must drain both exact ledgers.
	if installed >= 15 && !opened {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.domain_directory_task_uses WHERE state='opened')`).Scan(&opened); err != nil {
			return errors.New("directory credential-use migration check failed")
		}
	}
	if opened {
		return ErrCredentialUseMigrationBlocked
	}
	return nil
}

// requireDirectoryV2Baseline refuses partial/foreign installations. References
// are empty transaction-local tables made from the frozen historical DDL: the
// server compares its own catalog representation, without rewriting old rows or
// depending on a PostgreSQL release's pretty-printing of CHECK expressions.
func requireDirectoryV2Baseline(ctx context.Context, tx pgx.Tx) error {
	var exclusive, collision bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode='ExclusiveLock'),
 EXISTS(SELECT FROM adtr.tasks WHERE kind='domain.directory_read.v2')
 OR EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read.v2')
 OR EXISTS(SELECT FROM adtr.domain_directory_task_uses WHERE purpose='domain.directory_read.v2')
 OR EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v2')
 OR EXISTS(SELECT FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read.v2')
 OR EXISTS(SELECT FROM adtr.operation_account_use_audit WHERE purpose='domain.directory_read.v2')
 OR EXISTS(SELECT FROM adtr.domain_audit WHERE action IN ('domain_directory_v2_submit','domain_directory_v2_cancel','domain_directory_v2_result'))
 OR EXISTS(SELECT FROM pg_attribute WHERE attrelid IN ('adtr.domain_directory_task_uses'::regclass,'adtr.domain_directory_observations'::regclass) AND attname='dictionary_version' AND NOT attisdropped)
 OR EXISTS(SELECT FROM pg_class WHERE relnamespace='adtr'::regnamespace AND relname IN ('domain_directory_versioned_use_identity','domain_directory_observations_versioned_latest'))
 OR EXISTS(SELECT FROM pg_proc WHERE pronamespace='adtr'::regnamespace AND proname IN ('domain_directory_observation_canonical_v2','domain_directory_observation_base64_v2'))
 OR EXISTS(SELECT FROM pg_constraint WHERE connamespace='adtr'::regnamespace AND conname IN ('domain_directory_use_profile','domain_directory_observations_dictionary_version_check'))`).Scan(&exclusive, &collision); err != nil || !exclusive {
		return directoryV2SchemaCheckFailed(1)
	}
	if collision {
		return ErrDirectoryV2ReservedCollision
	}
	for _, definition := range []struct{ schema, table, reference string }{
		{domains.DirectoryUseSchema, "domain_directory_task_uses", "adtr_directory_v2_expected_uses"},
		{domains.DirectoryObservationSchema, "domain_directory_observations", "adtr_directory_v2_expected_observations"},
	} {
		sql, err := directoryV2ReferenceTable(definition.schema, definition.table, definition.reference)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, sql); err != nil {
			return directoryV2SchemaCheckFailed(2)
		}
		for _, snapshot := range []string{directoryV2Columns, directoryV2Checks, directoryV2Indexes} {
			var actual, expected string
			if err = tx.QueryRow(ctx, snapshot, "adtr."+definition.table).Scan(&actual); err != nil {
				return directoryV2SchemaCheckFailed(3)
			}
			if err = tx.QueryRow(ctx, snapshot, "pg_temp."+definition.reference).Scan(&expected); err != nil || actual != expected {
				return directoryV2SchemaCheckFailed(4)
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
		return directoryV2SchemaCheckFailed(5)
	}
	if err := directoryV2PurposeChecks(ctx, tx); err != nil {
		return err
	}
	if err := directoryV2HistoricalFunctionsAndTriggers(ctx, tx); err != nil {
		return err
	}
	const historicalView = "CREATE OR REPLACE VIEW adtr.audit_source AS"
	index := strings.Index(audit.DirectoryViewSchema, historicalView)
	if index < 0 {
		return directoryV2SchemaCheckFailed(6)
	}
	if _, err := tx.Exec(ctx, "CREATE TEMP VIEW adtr_directory_v2_expected_audit AS"+audit.DirectoryViewSchema[index+len(historicalView):]); err != nil {
		return directoryV2SchemaCheckFailed(7)
	}
	if err := tx.QueryRow(ctx, `SELECT pg_get_viewdef('adtr.audit_source'::regclass)=pg_get_viewdef('pg_temp.adtr_directory_v2_expected_audit'::regclass) AND EXISTS(SELECT FROM pg_class WHERE oid='adtr.audit_source'::regclass AND relkind='v' AND reloptions IS NULL)`).Scan(&valid); err != nil || !valid {
		return directoryV2SchemaCheckFailed(8)
	}
	return nil
}

var directoryV2InlineReference = regexp.MustCompile(` REFERENCES adtr\.[a-z_]+\([a-z_,]+\)`)

func directoryV2ReferenceTable(schema, table, reference string) (string, error) {
	start := strings.Index(schema, "CREATE TABLE adtr."+table+" (")
	if start < 0 {
		return "", ErrDirectoryV2SchemaShape
	}
	body := schema[start:]
	end := strings.Index(body, "\n);")
	if end < 0 {
		return "", ErrDirectoryV2SchemaShape
	}
	lines := strings.Split(body[:end], "\n")
	lines[0] = "CREATE TEMP TABLE " + pgx.Identifier{reference}.Sanitize() + " ("
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "FOREIGN KEY(") {
			out = append(out, directoryV2InlineReference.ReplaceAllString(line, ""))
		}
	}
	sql := strings.TrimSuffix(strings.TrimSpace(strings.Join(out, "\n")), ",") + "\n) ON COMMIT DROP;\n"
	indexPattern := regexp.MustCompile(`CREATE (UNIQUE )?INDEX ([a-z_]+) ON adtr\.` + regexp.QuoteMeta(table) + `(\([^;]+;)`)
	for i, match := range indexPattern.FindAllStringSubmatch(schema, -1) {
		// Names are private and fixed by this function; no record or request
		// contributes an SQL identifier or an expression.
		name := reference + "_" + strings.Repeat("i", i+1)
		sql += "CREATE " + match[1] + "INDEX " + pgx.Identifier{name}.Sanitize() + " ON " + pgx.Identifier{reference}.Sanitize() + match[3] + "\n"
	}
	return sql, nil
}

const directoryV2Columns = `SELECT COALESCE(jsonb_agg(jsonb_build_array(a.attnum,a.attname,a.atttypid,a.atttypmod,a.attnotnull,a.attcollation,a.attidentity,a.attgenerated,a.attisdropped,pg_get_expr(d.adbin,d.adrelid)) ORDER BY a.attnum),'[]'::jsonb)::text FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=to_regclass($1) AND a.attnum>0`

// Foreign keys and deferred constraint triggers have separate strict catalog
// checks below. The empty reference tables intentionally contain neither.
const directoryV2Checks = `SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT jsonb_build_array(contype,pg_get_constraintdef(oid),convalidated,condeferrable,condeferred,connoinherit) v FROM pg_constraint WHERE conrelid=to_regclass($1) AND contype NOT IN ('f','t')) s`
const directoryV2Indexes = `SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT jsonb_build_array(c.relam,i.indisunique,i.indisprimary,i.indisexclusion,i.indisvalid,i.indisready,i.indnkeyatts,i.indnatts,i.indkey::text,i.indcollation::text,i.indclass::text,i.indoption::text,pg_get_expr(i.indexprs,i.indrelid),pg_get_expr(i.indpred,i.indrelid)) v FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid=to_regclass($1)) s`

func directoryV2PurposeChecks(ctx context.Context, tx pgx.Tx) error {
	// Use the original migration text to create exact reference CHECKs on a
	// private empty relation; no handwritten pg_get_constraintdef golden.
	sql := `CREATE TEMP TABLE adtr_directory_v2_expected_checks(purpose text,consumer_kind text,account_credential_revision bigint,connection_credential_generation bigint,action text) ON COMMIT DROP;`
	type check struct{ table, name string }
	var checks []check
	pattern := regexp.MustCompile(`ALTER TABLE adtr\.([a-z_]+) ADD CONSTRAINT ([a-z_]+) CHECK\(([^;]+);`)
	for _, match := range pattern.FindAllStringSubmatch(credentialuse.DirectoryPurposeSchema+domains.DirectoryDependencySchema+domains.DirectoryAuditSchema, -1) {
		checks = append(checks, check{match[1], match[2]})
		sql += "ALTER TABLE pg_temp.adtr_directory_v2_expected_checks ADD CONSTRAINT " + pgx.Identifier{match[2]}.Sanitize() + " CHECK(" + match[3] + ";\n"
	}
	if len(checks) != 5 {
		return directoryV2SchemaCheckFailed(9)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		return directoryV2SchemaCheckFailed(10)
	}
	for _, check := range checks {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_constraint a JOIN pg_constraint e ON e.conrelid='pg_temp.adtr_directory_v2_expected_checks'::regclass AND e.conname=a.conname WHERE a.conrelid=to_regclass($1) AND a.conname=$2 AND a.contype='c' AND a.convalidated AND NOT a.connoinherit AND pg_get_constraintdef(a.oid)=pg_get_constraintdef(e.oid))`, "adtr."+check.table, check.name).Scan(&valid); err != nil || !valid {
			return directoryV2SchemaCheckFailed(11)
		}
	}
	return nil
}

func directoryV2HistoricalFunctionsAndTriggers(ctx context.Context, tx pgx.Tx) error {
	// Preserve migration order and take the last historical replacement. The
	// widened purpose depends on every governance guard, receipt, audit event
	// and epoch helper, not merely on the functions replaced by migration16.
	schema := auth.Schema + auth.TaskAuthorizationSchema + audit.Schema + domains.Schema + credentialuse.Schema + domains.AccountReferenceSchema + credentialuse.DirectoryPurposeSchema + domains.DirectoryDependencySchema + domains.DirectoryUseSchema + domains.DirectoryObservationSchema + audit.DirectoryViewSchema
	pattern := regexp.MustCompile(`(?s)CREATE(?: OR REPLACE)? FUNCTION adtr\.([a-z_]+)\(([^)]*)\) RETURNS (boolean|trigger|void|bigint) LANGUAGE (sql|SQL|plpgsql)( STABLE| IMMUTABLE)? AS \$\$(.*?)\$\$;`)
	matches := pattern.FindAllStringSubmatch(schema, -1)
	declarations := regexp.MustCompile(`CREATE(?: OR REPLACE)? FUNCTION adtr\.[a-z_]+\(`).FindAllString(schema, -1)
	if len(matches) == 0 || len(matches) != len(declarations) {
		return directoryV2SchemaCheckFailed(12)
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
					return directoryV2SchemaCheckFailed(13)
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
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=to_regprocedure($1) AND p.prosrc=$2 AND l.lanname=$3 AND p.prorettype=to_regtype($4) AND p.provolatile::text=$5 AND p.proargnames IS NOT DISTINCT FROM $6::text[] AND p.proargmodes IS NULL AND p.proallargtypes IS NULL AND p.provariadic=0 AND NOT p.proretset AND p.proparallel='u' AND p.prokind='f' AND NOT p.prosecdef AND NOT p.proisstrict AND NOT p.proleakproof AND p.proconfig IS NULL AND p.pronargdefaults=0 AND (SELECT count(*) FROM pg_proc q WHERE q.pronamespace=p.pronamespace AND q.proname=p.proname)=1)`, "adtr."+match[1]+"("+strings.Join(args, ",")+")", match[6], strings.ToLower(match[4]), match[3], volatility, argNames).Scan(&valid); err != nil || !valid {
			return directoryV2SchemaCheckFailed(14)
		}
	}
	// The original guard remains authoritative after dictionary provenance is
	// added. Verify its installed trigger identity, timing, events and deferred
	// consistency behavior; an attacker-controlled replacement is never adopted.
	triggerPattern := regexp.MustCompile(`CREATE( CONSTRAINT)? TRIGGER ([a-z_]+) (BEFORE|AFTER) ([A-Z ]+) ON adtr\.([a-z_]+)( DEFERRABLE INITIALLY DEFERRED)?\s+FOR EACH (ROW|STATEMENT) EXECUTE FUNCTION adtr\.([a-z_]+)\(\);`)
	triggerMatches := triggerPattern.FindAllStringSubmatch(schema, -1)
	triggerDeclarations := regexp.MustCompile(`CREATE(?: CONSTRAINT)? TRIGGER [a-z_]+ `).FindAllString(schema, -1)
	if len(triggerMatches) == 0 || len(triggerMatches) != len(triggerDeclarations) {
		return directoryV2SchemaCheckFailed(15)
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
			return directoryV2SchemaCheckFailed(16)
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
			return directoryV2SchemaCheckFailed(17)
		}
	}
	return nil
}

// Only a fixed source-code checkpoint reaches migration diagnostics. Never wrap
// a database error or include catalog contents, identifiers, rows or credentials.
// Preserve errors.Is for callers and fail closed at every existing guard.
func directoryV2SchemaCheckFailed(checkpoint uint8) error {
	return fmt.Errorf("%w (check %d)", ErrDirectoryV2SchemaShape, checkpoint)
}
