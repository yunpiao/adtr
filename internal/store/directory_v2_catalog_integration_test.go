//go:build integration

package store

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
)

// Keep this test separate from the upgrade tests: every stage gets a fresh
// transaction, so a rejected reference DDL cannot hide subsequent diagnostics.
// All persistent data belongs to the existing isolated synthetic fixture.
func TestDirectoryV2BaselineCatalog(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	inTransaction := func(name string, check func(*testing.T, pgx.Tx)) {
		t.Run(name, func(t *testing.T) {
			tx, err := f.conn.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			if _, err := tx.Exec(f.ctx, "SELECT pg_advisory_xact_lock(734192801)"); err != nil {
				t.Fatal(err)
			}
			check(t, tx)
		})
	}

	for _, definition := range []struct{ schema, table, reference string }{
		{domains.DirectoryUseSchema, "domain_directory_task_uses", "adtr_directory_v2_expected_uses"},
		{domains.DirectoryObservationSchema, "domain_directory_observations", "adtr_directory_v2_expected_observations"},
	} {
		inTransaction(definition.table, func(t *testing.T, tx pgx.Tx) {
			sql, err := directoryV2ReferenceTable(definition.schema, definition.table, definition.reference)
			if err != nil {
				t.Fatalf("reference construction: %v", err)
			}
			if _, err := tx.Exec(f.ctx, sql); err != nil {
				t.Fatalf("reference execution: %v", err)
			}
			for _, snapshot := range []struct{ name, sql string }{
				{"columns", directoryV2Columns},
				{"checks", directoryV2Checks},
				{"indexes", directoryV2Indexes},
			} {
				var actual, expected string
				if err := tx.QueryRow(f.ctx, snapshot.sql, "adtr."+definition.table).Scan(&actual); err != nil {
					t.Fatalf("%s actual catalog: %v", snapshot.name, err)
				}
				if err := tx.QueryRow(f.ctx, snapshot.sql, "pg_temp."+definition.reference).Scan(&expected); err != nil {
					t.Fatalf("%s reference catalog: %v", snapshot.name, err)
				}
				if actual != expected {
					// These snapshots contain DDL metadata, never fixture rows.
					t.Errorf("%s mismatch\nactual: %s\nexpected: %s", snapshot.name, actual, expected)
				}
			}
		})
	}
	inTransaction("purpose_checks", func(t *testing.T, tx pgx.Tx) {
		if err := directoryV2PurposeChecks(f.ctx, tx); err != nil {
			t.Fatal(err)
		}
	})
	inTransaction("historical_functions_and_triggers", func(t *testing.T, tx pgx.Tx) {
		if err := directoryV2HistoricalFunctionsAndTriggers(f.ctx, tx); err != nil {
			t.Error(err)
		}
	})
	inTransaction("complete_guard", func(t *testing.T, tx pgx.Tx) {
		if err := requireDirectoryV2Baseline(f.ctx, tx); err != nil {
			t.Fatal(err)
		}
	})

	// Diagnose the exact production function predicates without printing
	// function bodies, identifiers from rows, credentials, or connection strings.
	inTransaction("function_predicates", func(t *testing.T, tx pgx.Tx) {
		schema := auth.Schema + auth.TaskAuthorizationSchema + audit.Schema + domains.Schema + credentialuse.Schema + domains.AccountReferenceSchema + credentialuse.DirectoryPurposeSchema + domains.DirectoryDependencySchema + domains.DirectoryUseSchema + domains.DirectoryObservationSchema + audit.DirectoryViewSchema
		pattern := regexp.MustCompile(`(?s)CREATE(?: OR REPLACE)? FUNCTION adtr\.([a-z_]+)\(([^)]*)\) RETURNS (boolean|trigger|void|bigint) LANGUAGE (sql|SQL|plpgsql)( STABLE| IMMUTABLE)? AS \$\$(.*?)\$\$;`)
		matches := pattern.FindAllStringSubmatch(schema, -1)
		declarations := regexp.MustCompile(`CREATE(?: OR REPLACE)? FUNCTION adtr\.[a-z_]+\(`).FindAllString(schema, -1)
		if len(matches) == 0 || len(matches) != len(declarations) {
			t.Fatalf("function parser matched %d of %d declarations", len(matches), len(declarations))
		}
		functions := make(map[string][]string)
		for _, match := range matches {
			functions[match[1]] = match
		}
		var names []string
		for name := range functions {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			match := functions[name]
			var args, argNames []string
			for _, arg := range strings.Split(match[2], ",") {
				parts := strings.Fields(arg)
				if len(parts) == 0 {
					continue
				}
				if len(parts) != 2 {
					t.Fatalf("unsupported historical argument syntax in %s", name)
				}
				argNames = append(argNames, parts[0])
				args = append(args, parts[1])
			}
			volatility := "v"
			if match[5] == " STABLE" {
				volatility = "s"
			} else if match[5] == " IMMUTABLE" {
				volatility = "i"
			}
			var failures string
			err := tx.QueryRow(f.ctx, `WITH flags AS (
 SELECT jsonb_build_object(
  'body',p.prosrc=$2,'language',l.lanname=$3,'return_type',p.prorettype=to_regtype($4),
  'volatility',p.provolatile::text=$5,'argument_names',p.proargnames IS NOT DISTINCT FROM $6::text[],
  'argument_modes',p.proargmodes IS NULL,'all_argument_types',p.proallargtypes IS NULL,
  'variadic',p.provariadic=0,'set_returning',NOT p.proretset,'parallel',p.proparallel='u',
  'kind',p.prokind='f','security_definer',NOT p.prosecdef,'strict',NOT p.proisstrict,
  'leakproof',NOT p.proleakproof,'config',p.proconfig IS NULL,'defaults',p.pronargdefaults=0,
  'overloads',(SELECT count(*) FROM pg_proc q WHERE q.pronamespace=p.pronamespace AND q.proname=p.proname)=1) AS value
 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=to_regprocedure($1)
)
 SELECT CASE WHEN NOT EXISTS(SELECT FROM flags) THEN 'missing function'
 ELSE COALESCE((SELECT string_agg(e.key,',' ORDER BY e.key) FROM flags, jsonb_each(flags.value) AS e(key,value)
 WHERE e.value IS DISTINCT FROM 'true'::jsonb),'') END`,
				"adtr."+name+"("+strings.Join(args, ",")+")", match[6], strings.ToLower(match[4]), match[3], volatility, argNames).Scan(&failures)
			if err != nil {
				t.Fatalf("function catalog query for %s: %v", name, err)
			}
			if failures != "" {
				t.Errorf("function %s failed predicates: %s", name, failures)
			}
		}
	})
}
