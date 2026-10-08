//go:build integration

package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestExistingVersionTenUpgradePreservesIdentityAuthorityAndCredentials(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated PostgreSQL required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("adtr_profile_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
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

	// Build the historical v10 producers directly, without ProfileSchema or a
	// current migration followed by a version-counter rollback. Audit projections
	// are refreshable, so remove their v11-only action from the v10 fixture too.
	prior := `CREATE SCHEMA adtr;
CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0));
INSERT INTO adtr.schema_version VALUES(true,10);` +
		auth.Schema + auth.SchemaV3 + auth.ResourceSchema + auth.TaskPermissionSchema + auth.TaskAuthorizationSchema + tasks.Schema +
		auth.AuditPermissionSchema + audit.Schema + auth.SystemPermissionMarks + systemhealth.Schema +
		auth.SchedulePermissionSchema + auth.ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema +
		auth.DomainPermissionSchema + domains.Schema + audit.DomainViewSchema +
		auth.OperationAccountPermissionSchema + operationaccounts.Schema + audit.OperationAccountViewSchema
	prior = strings.ReplaceAll(prior, ",'profile_avatar_update'", "")
	if _, err = conn.Exec(ctx, prior); err != nil {
		t.Fatal(err)
	}
	var version int
	var profileAbsent, oldProjection bool
	if err = conn.QueryRow(ctx, `SELECT version,to_regclass('adtr.profile_avatars') IS NULL,
position('profile_avatar_update' in pg_get_viewdef('adtr.audit_source'::regclass))=0
FROM adtr.schema_version WHERE singleton`).Scan(&version, &profileAbsent, &oldProjection); err != nil {
		t.Fatal(err)
	}
	if version != 10 || !profileAbsent || !oldProjection {
		t.Fatal("fixture is not the historical v10 schema")
	}
	if Ready(ctx, cfg) == nil {
		t.Fatal("v10 accepted as current")
	}

	if _, err = conn.Exec(ctx, `
INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('old',3,2000000000,'old-tenant','Existing tenant');
INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('old','old-domain','old.invalid',true);
INSERT INTO adtr.access_roles(tenant_id,id,name,remark) VALUES('old','old-role','Existing role','Keep existing grants');
INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES
 ('old','old-role','users',false,false),('old','old-role','domains',true,false),('old','old-role','operation_accounts',true,true);
INSERT INTO adtr.resource_groups(tenant_id,id,name,mark) VALUES('old','old-group','Existing scope','Synthetic domain scope');
INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('old','old-group','old-domain');
INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('old','old-role','old-group');
INSERT INTO adtr.users(tenant_id,username,password_hash,pass_strength,role,role_id,must_change,mfa_secret,mobile,email,remark,address,real_name,department,post)
 VALUES('old','old-user','synthetic-password-hash','high','viewer','old-role',false,'synthetic-mfa-secret','synthetic-mobile','old@example.invalid','Existing profile','Synthetic address','Synthetic user','Synthetic department','Synthetic position');
INSERT INTO adtr.users(tenant_id,username,password_hash,role,disabled) VALUES('other','other-user','synthetic-other-hash','viewer',true);
INSERT INTO adtr.sessions(token_hash,user_id,expires_at) SELECT 'synthetic-session-hash',id,'2040-01-01T00:00:00Z' FROM adtr.users WHERE username='old-user';
INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port)
 VALUES('old','old-domain','old.invalid','dc.old.invalid','starttls','389');`); err != nil {
		t.Fatal(err)
	}
	var actorID int64
	if err = conn.QueryRow(ctx, "SELECT id FROM adtr.users WHERE username='old-user'").Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"ADTR_DOMAIN_KEY_ID": "synthetic-v1",
		"ADTR_DOMAIN_KEY":    base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x76}, 32)),
	}
	runtime, err := domainconfig.Load(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	domainPlaintext := []byte(`{"version":1,"username":"reader@old.invalid","password":"Synthetic old domain credential"}`)
	defer clear(domainPlaintext)
	domainSecret, err := runtime.Vault().Seal("old", "old-domain", 1, domainPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	accountPlaintext := []byte(`{"version":1,"username":"operator@old.invalid","password":"Synthetic old operation credential"}`)
	defer clear(accountPlaintext)
	accountSecret, err := runtime.Vault().SealOperationCredential("old", "old-domain", "old-account", 1, accountPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	// Keep normal triggers and deferred pair/pin constraints enabled. An existing
	// operation account must include its encrypted credential and domain pin.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES('old','old-domain',1,$1,$2)`, []any{domainSecret.KeyID, domainSecret.Ciphertext}},
		{`INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES('old','old-domain','old-account','Existing operation account')`, nil},
		{`INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) VALUES('old','old-domain','old-account',1,$1,$2)`, []any{accountSecret.KeyID, accountSecret.Ciphertext}},
		{`INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES('old','old-domain','operation_accounts','old-account')`, nil},
		{`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES('old','old-domain','old-account','reserved.synthetic','old-consumer')`, nil},
		{`INSERT INTO adtr.operation_account_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,revision,credential_revision) VALUES('old',$1,'synthetic-create','create',repeat('a',64),'old-domain','old-account',1,1)`, []any{actorID}},
		{`INSERT INTO adtr.operation_account_audit(tenant_id,actor_id,domain_id,account_id,action,old_revision,new_revision,old_credential_revision,new_credential_revision,result) VALUES('old',$1,'old-domain','old-account','operation_account_create',0,1,0,1,'saved')`, []any{actorID}},
		{`INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result) VALUES('old',$1,'old-domain','domain_create',0,1,1,'saved')`, []any{actorID}},
		{`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES('old',$1,'login',$1)`, []any{actorID}},
	} {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Compare every persisted field, including profile text, password/MFA data,
	// session validity, actor epochs, explicit denials, scopes and receipt history.
	// Never print snapshots on failure because they contain credential material.
	tables := []string{
		"users", "sessions", "access_roles", "access_permissions", "resource_tenant_config",
		"resource_domains", "resource_groups", "resource_group_members", "resource_role_groups",
		"domain_connections", "domain_credentials", "domain_dependencies", "domain_audit",
		"operation_accounts", "operation_account_credentials", "operation_account_dependencies",
		"operation_account_mutations", "operation_account_audit", "auth_audit", "audit_source",
		"task_authorization_epoch",
	}
	snapshot := func(table string) string {
		t.Helper()
		var data string
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb)::text FROM ` + pgx.Identifier{"adtr", table}.Sanitize() + ` r`
		if e := conn.QueryRow(ctx, query).Scan(&data); e != nil {
			t.Fatalf("snapshot %s: %v", table, e)
		}
		return data
	}
	before := make(map[string]string, len(tables))
	for _, table := range tables {
		before[table] = snapshot(table)
	}
	assertPreserved := func() {
		t.Helper()
		if e := Ready(ctx, cfg); e != nil {
			t.Fatal(e)
		}
		var avatars int
		if e := conn.QueryRow(ctx, "SELECT count(*) FROM adtr.profile_avatars").Scan(&avatars); e != nil {
			t.Fatal(e)
		}
		if avatars != 0 {
			t.Fatal("migration invented profile avatars")
		}
		for _, table := range tables {
			if snapshot(table) != before[table] {
				t.Fatalf("migration changed existing %s data", table)
			}
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- Migrate(ctx, cfg)
		}()
	}
	close(start)
	for range 2 {
		if e := <-results; e != nil {
			t.Error(e)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	assertPreserved()
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	assertPreserved()

	var keptDomain domainconfig.SealedCredential
	if err = conn.QueryRow(ctx, `SELECT key_id,ciphertext FROM adtr.domain_credentials WHERE tenant_id='old' AND domain_id='old-domain' AND credential_revision=1`).Scan(&keptDomain.KeyID, &keptDomain.Ciphertext); err != nil {
		t.Fatal(err)
	}
	openedDomain, err := runtime.Vault().Open("old", "old-domain", 1, keptDomain)
	defer clear(openedDomain)
	if err != nil || !bytes.Equal(openedDomain, domainPlaintext) {
		t.Fatal("existing domain credential no longer decrypts")
	}
	var keptAccount domainconfig.OperationSealedCredential
	if err = conn.QueryRow(ctx, `SELECT key_id,ciphertext FROM adtr.operation_account_credentials WHERE tenant_id='old' AND domain_id='old-domain' AND account_id='old-account' AND credential_revision=1`).Scan(&keptAccount.KeyID, &keptAccount.Ciphertext); err != nil {
		t.Fatal(err)
	}
	openedAccount, err := runtime.Vault().OpenOperationCredential("old", "old-domain", "old-account", 1, keptAccount)
	defer clear(openedAccount)
	if err != nil || !bytes.Equal(openedAccount, accountPlaintext) {
		t.Fatal("existing operation credential no longer decrypts")
	}
	var protected bool
	if err = conn.QueryRow(ctx, `SELECT NOT adtr.audit_event_deletable('domain','domain_create') AND NOT adtr.audit_event_deletable('operation_account','operation_account_create')`).Scan(&protected); err != nil || !protected {
		t.Fatal("migration weakened existing audit controls", err)
	}
	// The historical projection could not classify this newly supported action.
	// Verify migration 11 refreshed it without losing either existing producer.
	var eventID int64
	if err = conn.QueryRow(ctx, `INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES('old',$1,'profile_avatar_update',$1) RETURNING id`, actorID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	var result string
	var available bool
	if err = conn.QueryRow(ctx, `SELECT event_result,result_available FROM adtr.audit_source WHERE source='auth' AND source_id=$1`, eventID).Scan(&result, &available); err != nil || result != "SUCCESS" || !available {
		t.Fatal("profile audit projection was not upgraded", err)
	}
}
