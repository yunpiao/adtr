//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

// B1's open dependency namespace may contain names B2 now reserves. Refusing
// the upgrade preserves their unknown provenance and blocker semantics. Never
// manufacture pins or delete a collision merely to make migration succeed.
func TestVersionTwelveReservedDependencyCollisionRollsBackMigration(t *testing.T) {
	for _, mode := range []string{"dependency", "task"} {
		t.Run(mode, func(t *testing.T) { testVersionTwelveReservedCollision(t, mode) })
	}
}

func testVersionTwelveReservedCollision(t *testing.T, mode string) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: isolated PostgreSQL migration test is blocked")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated PostgreSQL configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("adtr_account_reference_collision_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	historical := `CREATE SCHEMA adtr;CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0));INSERT INTO adtr.schema_version VALUES(true,12);` +
		auth.Schema + auth.SchemaV3 + auth.ResourceSchema + auth.TaskPermissionSchema + auth.TaskAuthorizationSchema + tasks.Schema +
		auth.AuditPermissionSchema + audit.Schema + auth.SystemPermissionMarks + systemhealth.Schema +
		auth.SchedulePermissionSchema + auth.ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema +
		auth.DomainPermissionSchema + domains.Schema + audit.DomainViewSchema + auth.OperationAccountPermissionSchema + operationaccounts.Schema + auth.ProfileSchema + audit.OperationAccountViewSchema +
		credentialuse.Schema + audit.CredentialUseViewSchema
	if _, err = conn.Exec(ctx, historical); err != nil {
		t.Fatal("install historical v12 producers", err)
	}
	bindingKind, testKind := "domain.connection_binding", "domain.account_connection_test"
	if mode == "task" {
		bindingKind, testKind = "unknown.synthetic_binding", "unknown.synthetic_test"
	}
	seed := `BEGIN;
 INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('collision',2,253402300799,'synthetic-collision','Synthetic');
 INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('collision','old-domain','old.invalid',true);
 INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port) VALUES('collision','old-domain','old.invalid','dc.old.invalid','starttls','389');
 INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES('collision','old-domain',1,'synthetic-old-key',decode(repeat('71',40),'hex'));
 INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES('collision','old-domain','old-account','Synthetic old account');
 INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) VALUES('collision','old-domain','old-account',1,'synthetic-account-key',decode(repeat('72',40),'hex'));
 INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES('collision','old-domain','operation_accounts','old-account');
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES
 ('collision','old-domain','old-account',$1,'legacy-opaque-binding'),
 ('collision','old-domain','old-account',$2,'legacy-opaque-test'),
 ('collision','old-domain','old-account','unknown.synthetic','unrelated-blocker');
 COMMIT;`
	seed = strings.ReplaceAll(strings.ReplaceAll(seed, "$1", "'"+bindingKind+"'"), "$2", "'"+testKind+"'")
	if _, err = conn.Exec(ctx, seed); err != nil {
		t.Fatal("seed historical unknown dependency provenance", err)
	}
	if mode == "task" {
		if _, err = conn.Exec(ctx, `INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts)
   VALUES('unknown-prior-task','collision','old-domain','domain.account_connection_test',1,'{}','opaque-prior-payload-hash',123,'prior-authority','prior-task-key','queued',1)`); err != nil {
			t.Fatal("seed previously unknown task", err)
		}
	}

	snapshot := func() string {
		t.Helper()
		var out string
		err := conn.QueryRow(ctx, `SELECT jsonb_build_object(
 'tasks',(SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY task_id),'[]') FROM adtr.tasks t),
 'dependencies',(SELECT jsonb_agg(to_jsonb(d) ORDER BY consumer_kind,object_id) FROM adtr.operation_account_dependencies d),
 'connections',(SELECT jsonb_agg(to_jsonb(c) ORDER BY domain_id) FROM adtr.domain_connections c),
 'pairs',(SELECT jsonb_agg(to_jsonb(p) ORDER BY domain_id) FROM adtr.domain_credentials p),
 'accountPairs',(SELECT jsonb_agg(to_jsonb(p) ORDER BY account_id) FROM adtr.operation_account_credentials p),
 'constraints',(SELECT jsonb_agg(pg_get_constraintdef(oid) ORDER BY conname) FROM pg_constraint WHERE conrelid IN ('adtr.domain_connections'::regclass,'adtr.operation_account_dependencies'::regclass)),
 'view',pg_get_viewdef('adtr.audit_source'::regclass))::text`).Scan(&out)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	for range 2 {
		err = Migrate(ctx, cfg)
		// Migrate deliberately sanitizes database errors. Its public failure plus
		// the complete unchanged snapshot proves safe refusal without depending
		// on an internal PostgreSQL cause escaping that boundary.
		if err == nil {
			t.Fatal("reserved-name collision was silently adopted by migration")
		}
		var unchanged bool
		if err = conn.QueryRow(ctx, `SELECT version=12 AND to_regclass('adtr.domain_account_task_uses') IS NULL
   AND NOT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='adtr' AND table_name='domain_connections' AND column_name='operation_account_id')
   AND NOT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='adtr' AND table_name='operation_account_dependencies' AND column_name='account_credential_revision')
   FROM adtr.schema_version`).Scan(&unchanged); err != nil || !unchanged {
			t.Fatal("failed migration left partial schema/version", err)
		}
		if snapshot() != before {
			t.Fatal("failed migration changed historical schema or blocker data")
		}
	}
}
