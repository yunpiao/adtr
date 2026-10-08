//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
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

func TestVersionTwelveAccountReferenceUpgradePreservesCustomAdmissions(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated PostgreSQL required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("adtr_account_reference_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(c, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	historical := `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version VALUES(true,12);` +
		auth.Schema + auth.SchemaV3 + auth.ResourceSchema + auth.TaskPermissionSchema + auth.TaskAuthorizationSchema + tasks.Schema +
		auth.AuditPermissionSchema + audit.Schema + auth.SystemPermissionMarks + systemhealth.Schema +
		auth.SchedulePermissionSchema + auth.ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema +
		auth.DomainPermissionSchema + domains.Schema + audit.DomainViewSchema + auth.OperationAccountPermissionSchema + operationaccounts.Schema + auth.ProfileSchema + audit.OperationAccountViewSchema +
		credentialuse.Schema + audit.CredentialUseViewSchema
	if _, err = conn.Exec(ctx, historical); err != nil {
		t.Fatal(err)
	}
	var absent bool
	if err = conn.QueryRow(ctx, `SELECT version=12 AND to_regclass('adtr.domain_account_task_uses') IS NULL FROM adtr.schema_version`).Scan(&absent); err != nil || !absent {
		t.Fatal("fixture is not historical v12", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('upgrade',2,253402300799,'synthetic-upgrade','Synthetic');
 INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('upgrade','old-domain','old.invalid',true);
 INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change) VALUES('upgrade','synthetic-upgrade-user','synthetic-hash','platform_admin',false);
 INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port) VALUES('upgrade','old-domain','old.invalid','dc.old.invalid','starttls','389');
 INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES('upgrade','old-domain',1,'synthetic-old-key',decode(repeat('71',40),'hex'));
 BEGIN;
 INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES('upgrade','old-domain','old-account','Synthetic old account');
 INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) VALUES('upgrade','old-domain','old-account',1,'synthetic-account-key',decode(repeat('72',40),'hex'));
 INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES('upgrade','old-domain','operation_accounts','old-account');
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES('upgrade','old-domain','old-account','unknown.synthetic','keep-blocked');
 COMMIT;`); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "succeeded"} {
		_, err = conn.Exec(ctx, `INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts)
 SELECT $1,'upgrade','old-domain','domain.connection_test',1,'{"connectionRevision":"1","credentialRevision":"1","policyRevision":"historical-policy","diagnosticGeneration":"1"}',repeat('a',64),id,'historical-epoch',$1,$2,1 FROM adtr.users WHERE username='synthetic-upgrade-user'`, "historical-"+state, state)
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func(query string) string {
		t.Helper()
		var v string
		if e := conn.QueryRow(ctx, query).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	taskQuery := `SELECT jsonb_agg(to_jsonb(t) ORDER BY task_id)::text FROM adtr.tasks t`
	pairQuery := `SELECT jsonb_agg(to_jsonb(k) ORDER BY domain_id)::text FROM adtr.domain_credentials k`
	epochQuery := `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'epoch',authorization_version) ORDER BY id),'[]')::text FROM adtr.users`
	oldTasks, oldPairs, oldEpochs := snapshot(taskQuery), snapshot(pairQuery), snapshot(epochQuery)
	for range 2 {
		if err = Migrate(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot(taskQuery) != oldTasks || snapshot(pairQuery) != oldPairs || snapshot(epochQuery) != oldEpochs {
		t.Fatal("migration rewrote historical task/pair/authority data")
	}
	var valid bool
	err = conn.QueryRow(ctx, `SELECT version=$1 AND
 (SELECT credential_mode='custom' AND operation_account_id IS NULL AND operation_account_credential_revision IS NULL AND credential_revision=1 FROM adtr.domain_connections WHERE tenant_id='upgrade' AND domain_id='old-domain') AND
 NOT EXISTS(SELECT FROM adtr.domain_account_task_uses) AND
 NOT EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind IN ('domain.connection_binding','domain.account_connection_test')) AND
 EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind='unknown.synthetic' AND object_id='keep-blocked' AND account_credential_revision IS NULL AND connection_credential_generation IS NULL)
 FROM adtr.schema_version`, SchemaVersion).Scan(&valid)
	if err != nil || !valid {
		t.Fatal("migration fabricated use authority or changed old blockers", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = tasks.CheckSchemaTx(ctx, tx, 12); !errors.Is(err, tasks.ErrSchemaIncompatible) {
		t.Fatal("old binary accepted schema13", err)
	}
}
