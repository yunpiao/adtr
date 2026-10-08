//go:build integration

package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func TestExistingVersionElevenUpgradePreservesDataWithoutCredentialAuthority(t *testing.T) {
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
	name := fmt.Sprintf("adtr_credential_use_upgrade_%d", time.Now().UnixNano())
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

	// Install the actual v11 producers. In particular, never run the current
	// migrator and roll its version back, or install credentialuse.Schema here.
	prior := `CREATE SCHEMA adtr;
CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0));
INSERT INTO adtr.schema_version VALUES(true,11);` +
		auth.Schema + auth.SchemaV3 + auth.ResourceSchema + auth.TaskPermissionSchema + auth.TaskAuthorizationSchema + tasks.Schema +
		auth.AuditPermissionSchema + audit.Schema + auth.SystemPermissionMarks + systemhealth.Schema +
		auth.SchedulePermissionSchema + auth.ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema +
		auth.DomainPermissionSchema + domains.Schema + audit.DomainViewSchema +
		auth.OperationAccountPermissionSchema + operationaccounts.Schema + auth.ProfileSchema + audit.OperationAccountViewSchema
	if _, err = conn.Exec(ctx, prior); err != nil {
		t.Fatal(err)
	}
	var historical bool
	if err = conn.QueryRow(ctx, `SELECT version=11
AND to_regclass('adtr.profile_avatars') IS NOT NULL
AND to_regclass('adtr.operation_account_use_grants') IS NULL
AND to_regclass('adtr.operation_account_use_mutations') IS NULL
AND to_regclass('adtr.operation_account_use_audit') IS NULL
AND to_regclass('adtr.credential_use_revision') IS NULL
AND position('credential_use' in pg_get_viewdef('adtr.audit_source'::regclass))=0
AND NOT EXISTS(SELECT FROM pg_trigger WHERE tgrelid='adtr.users'::regclass AND tgname LIKE 'credential_use_%')
FROM adtr.schema_version WHERE singleton`).Scan(&historical); err != nil || !historical {
		t.Fatal("fixture is not the historical v11 schema", err)
	}
	if Ready(ctx, cfg) == nil {
		t.Fatal("v11 accepted as current")
	}
	if _, err = conn.Exec(ctx, `
INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)
 VALUES('old',3,253402300799,'old-tenant','Existing tenant');
INSERT INTO adtr.resource_domains(tenant_id,id,name,active)
 VALUES('old','old-domain','old.invalid',true),('old','ungranted-domain','ungranted.invalid',true);
INSERT INTO adtr.access_roles(tenant_id,id,name,remark)
 VALUES('old','old-role','Existing operator','Keep existing function grants'),('old','denied-role','Existing denied role','Keep explicit denials');
INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES
 ('old','old-role','users',false,false),('old','old-role','domains',true,true),
 ('old','old-role','tasks',true,true),('old','old-role','operation_accounts',true,true),
 ('old','denied-role','domains',false,false),('old','denied-role','tasks',true,false),
 ('old','denied-role','operation_accounts',false,false);
INSERT INTO adtr.resource_groups(tenant_id,id,name,mark)
 VALUES('old','old-group','Existing scope','Synthetic exact domain scope');
INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('old','old-group','old-domain');
INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id)
 VALUES('old','old-role','old-group'),('old','platform_admin','old-group');
INSERT INTO adtr.users(tenant_id,username,password_hash,pass_strength,role,role_id,must_change,mfa_secret,mfa_last_step,mobile,email,remark,address,real_name,department,post)
 VALUES('old','old-user','synthetic-password-hash','high','viewer','old-role',false,'synthetic-mfa-secret',42,'synthetic-mobile','old@example.invalid','Existing profile','Synthetic address','Synthetic user','Synthetic department','Synthetic position');
INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change,mfa_secret)
 VALUES('old','old-admin','synthetic-admin-hash','platform_admin',false,'synthetic-admin-mfa');
INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,disabled)
 VALUES('old','denied-user','synthetic-denied-hash','viewer','denied-role',true,false),
 ('other','other-user','synthetic-other-hash','viewer','',false,true);
INSERT INTO adtr.sessions(token_hash,user_id,expires_at)
 SELECT 'synthetic-session-'||username,id,'2040-01-01T00:00:00Z' FROM adtr.users WHERE tenant_id='old';
INSERT INTO adtr.auth_attempts(bucket,count,window_start) VALUES('synthetic-attempt',2,'2026-01-01T00:00:00Z');
INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port)
 VALUES('old','old-domain','old.invalid','dc.old.invalid','starttls','389');`); err != nil {
		t.Fatal(err)
	}
	var actorID, adminID int64
	if err = conn.QueryRow(ctx, `SELECT (SELECT id FROM adtr.users WHERE username='old-user'),
(SELECT id FROM adtr.users WHERE username='old-admin')`).Scan(&actorID, &adminID); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"ADTR_DOMAIN_KEY_ID": "synthetic-v11",
		"ADTR_DOMAIN_KEY":    base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32)),
	}
	runtime, err := domainconfig.Load(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	domainPlaintext := []byte(`{"version":1,"username":"reader@old.invalid","password":"Synthetic v11 domain credential"}`)
	defer clear(domainPlaintext)
	domainSecret, err := runtime.Vault().Seal("old", "old-domain", 1, domainPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	accountPlaintext := []byte(`{"version":1,"username":"operator@old.invalid","password":"Synthetic v11 operation credential"}`)
	defer clear(accountPlaintext)
	accountSecret, err := runtime.Vault().SealOperationCredential("old", "old-domain", "old-account", 1, accountPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	avatar := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	avatar.SetNRGBA(0, 0, color.NRGBA{R: 120, G: 60, B: 180, A: 255})
	var avatarPNG bytes.Buffer
	if err = png.Encode(&avatarPNG, avatar); err != nil {
		t.Fatal(err)
	}

	// The complete credential pair and its domain pin commit with all normal
	// triggers and deferred constraints enabled, just as a v11 account would.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`SELECT set_config('adtr.audit_username','synthetic-v11-writer',true),set_config('adtr.audit_ip','192.0.2.11',true),set_config('adtr.audit_path','/synthetic/v11',true),set_config('adtr.audit_request_id','synthetic-v11-request',true)`, nil},
		{`INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES('old','old-domain',1,$1,$2)`, []any{domainSecret.KeyID, domainSecret.Ciphertext}},
		{`INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES('old','old-domain','old-account','Existing operation account')`, nil},
		{`INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext) VALUES('old','old-domain','old-account',1,$1,$2)`, []any{accountSecret.KeyID, accountSecret.Ciphertext}},
		{`INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES('old','old-domain','operation_accounts','old-account')`, nil},
		{`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES('old','old-domain','old-account','reserved.synthetic','old-consumer')`, nil},
		{`INSERT INTO adtr.domain_creation_receipts(tenant_id,actor_id,idempotency_key,fingerprint,domain_id,connection_revision) VALUES('old',$1,'synthetic-domain-create',repeat('b',64),'old-domain',1)`, []any{actorID}},
		{`INSERT INTO adtr.operation_account_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,revision,credential_revision) VALUES('old',$1,'synthetic-account-create','create',repeat('a',64),'old-domain','old-account',1,1)`, []any{actorID}},
		{`INSERT INTO adtr.operation_account_audit(tenant_id,actor_id,domain_id,account_id,action,old_revision,new_revision,old_credential_revision,new_credential_revision,result) VALUES('old',$1,'old-domain','old-account','operation_account_create',0,1,0,1,'saved')`, []any{actorID}},
		{`INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result) VALUES('old',$1,'old-domain','domain_create',0,1,1,'saved')`, []any{actorID}},
		{`INSERT INTO adtr.resource_audit(tenant_id,actor_id,action,target_id) VALUES('old',$1,'resource_roles_replace','old-role')`, []any{adminID}},
		{`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES('old',$1,'login',$1),('old',$1,'profile_avatar_update',$1)`, []any{actorID}},
		{`INSERT INTO adtr.profile_avatars(user_id,tenant_id,png,updated_at) VALUES($1,'old',$2,'2026-01-02T03:04:05Z')`, []any{actorID, avatarPNG.Bytes()}},
		{`INSERT INTO adtr.audit_visibility(tenant_id,source,source_id,hidden,version) SELECT 'old','auth',id,true,7 FROM adtr.auth_audit WHERE action='login'`, nil},
		{`INSERT INTO adtr.audit_visibility_revisions(tenant_id,revision) VALUES('old',7)`, nil},
		{`INSERT INTO adtr.audit_events(tenant_id,actor_id,action,target_id,reason,visibility_version) SELECT 'old',$1,'audit_hide','auth:'||id,'Synthetic historical visibility',7 FROM adtr.auth_audit WHERE action='login'`, []any{adminID}},
	} {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Capture every historical table, sequence and view, not only selected
	// columns. This covers passwords/MFA, all sessions, explicit denials, exact
	// F47 memberships, old audit metadata/visibility and stored actor epochs.
	// Never print snapshots: they contain synthetic credential material.
	rows, err := conn.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='adtr' AND c.relkind IN ('r','p','S','v','m') AND c.relname<>'schema_version' ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	relations, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	// Capture the historical column set before migration. New additive columns
	// are asserted by the v12 upgrade test; every old value remains byte-for-byte.
	historicalColumns := make(map[string]string, len(relations))
	for _, relation := range relations {
		rs, e := conn.Query(ctx, `SELECT attname FROM pg_attribute WHERE attrelid=to_regclass($1) AND attnum>0 AND NOT attisdropped ORDER BY attnum`, "adtr."+relation)
		if e != nil {
			t.Fatal(e)
		}
		cols, e := pgx.CollectRows(rs, pgx.RowTo[string])
		if e != nil {
			t.Fatal(e)
		}
		for i := range cols {
			cols[i] = pgx.Identifier{cols[i]}.Sanitize()
		}
		historicalColumns[relation] = strings.Join(cols, ",")
	}
	snapshot := func(relation string) string {
		t.Helper()
		var data string
		columns := "*"
		if old, ok := historicalColumns[relation]; ok {
			columns = old
		}
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb)::text FROM (SELECT ` + columns + ` FROM ` + pgx.Identifier{"adtr", relation}.Sanitize() + `) r`
		if e := conn.QueryRow(ctx, query).Scan(&data); e != nil {
			t.Fatalf("snapshot %s: %v", relation, e)
		}
		return data
	}
	before := make(map[string]string, len(relations))
	for _, relation := range relations {
		before[relation] = snapshot(relation)
	}
	assertPreserved := func() {
		t.Helper()
		if e := Ready(ctx, cfg); e != nil {
			t.Fatal(e)
		}
		var version int
		if e := conn.QueryRow(ctx, `SELECT version FROM adtr.schema_version WHERE singleton`).Scan(&version); e != nil || version != SchemaVersion {
			t.Fatal("migration did not install current schema", e)
		}
		for _, relation := range relations {
			if snapshot(relation) != before[relation] {
				t.Fatalf("migration changed existing %s data", relation)
			}
		}
		for _, table := range []string{"operation_account_use_grants", "operation_account_use_mutations", "operation_account_use_audit"} {
			if snapshot(table) != "[]" {
				t.Fatalf("migration invented %s records", table)
			}
		}
		var unused, noncycling bool
		if e := conn.QueryRow(ctx, `SELECT NOT is_called FROM adtr.credential_use_revision`).Scan(&unused); e != nil || !unused {
			t.Fatal("migration consumed a credential grant revision", e)
		}
		if e := conn.QueryRow(ctx, `SELECT NOT seqcycle AND seqtypid='bigint'::regtype FROM pg_sequence WHERE seqrelid='adtr.credential_use_revision'::regclass`).Scan(&noncycling); e != nil || !noncycling {
			t.Fatal("credential grant revisions are not noncycling bigint values", e)
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
	var keptPNG []byte
	if err = conn.QueryRow(ctx, `SELECT png FROM adtr.profile_avatars WHERE user_id=$1 AND tenant_id='old'`, actorID).Scan(&keptPNG); err != nil || !bytes.Equal(keptPNG, avatarPNG.Bytes()) {
		t.Fatal("existing profile avatar changed", err)
	}

	// Check installed enabled guards, including statement-time scope capture and
	// deferred final-state protection. Empty grant tables alone cannot prove that
	// the migration installed the old-binary/indirect-delegation barrier.
	for _, guard := range []struct{ table, trigger string }{
		{"operation_account_use_grants", "credential_use_grant_guard"},
		{"operation_account_use_grants", "credential_use_grant_event"},
		{"operation_account_use_grants", "credential_use_grants_no_truncate"},
		{"operation_account_use_mutations", "credential_use_receipt_guard"},
		{"operation_account_use_mutations", "credential_use_receipts_immutable"},
		{"operation_account_use_mutations", "credential_use_receipts_no_truncate"},
		{"operation_account_use_audit", "credential_use_audit_immutable"},
		{"operation_account_use_audit", "credential_use_audit_no_truncate"},
		{"operation_account_use_audit", "credential_use_audit_capture"},
		{"operation_accounts", "credential_use_account_invalidate"},
		{"users", "credential_use_user_guard"},
		{"users", "credential_use_capture_users"},
		{"access_permissions", "credential_use_permissions"},
		{"access_permissions", "credential_use_capture_permissions"},
		{"access_roles", "credential_use_role_delete"},
		{"access_roles", "credential_use_capture_roles"},
		{"resource_role_groups", "credential_use_role_groups"},
		{"resource_role_groups", "credential_use_capture_role_groups"},
		{"resource_group_members", "credential_use_group_members"},
		{"resource_group_members", "credential_use_capture_members"},
		{"resource_groups", "credential_use_group_delete"},
		{"resource_groups", "credential_use_capture_groups"},
		{"resource_tenant_config", "credential_use_tenant_guard"},
		{"resource_tenant_config", "credential_use_capture_tenant"},
	} {
		var enabled bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_trigger WHERE tgrelid=to_regclass($1) AND tgname=$2 AND tgenabled IN ('O','A') AND NOT tgisinternal)`, "adtr."+guard.table, guard.trigger).Scan(&enabled); err != nil || !enabled {
			t.Fatalf("governance trigger %s is not enabled: %v", guard.trigger, err)
		}
	}
	for _, guard := range []struct{ table, trigger string }{
		{"operation_account_use_grants", "credential_use_admin_scope_grants"},
		{"resource_group_members", "credential_use_admin_scope_members"},
		{"resource_role_groups", "credential_use_admin_scope_bindings"},
	} {
		var deferred bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_trigger WHERE tgrelid=to_regclass($1) AND tgname=$2 AND tgenabled IN ('O','A') AND tgdeferrable AND tginitdeferred)`, "adtr."+guard.table, guard.trigger).Scan(&deferred); err != nil || !deferred {
			t.Fatalf("governance invariant %s is not deferred and enabled: %v", guard.trigger, err)
		}
	}
	var protected bool
	if err = conn.QueryRow(ctx, `SELECT NOT adtr.audit_event_deletable('domain','domain_create')
AND NOT adtr.audit_event_deletable('operation_account','operation_account_create')
AND NOT adtr.audit_event_deletable('credential_use','credential_use_grant')
AND NOT adtr.audit_event_deletable('credential_use','credential_use_revoke')
AND NOT adtr.audit_event_deletable('credential_use','credential_use_pair_replaced')
AND NOT adtr.audit_event_deletable('credential_use','credential_use_account_deleted')
AND NOT adtr.audit_event_deletable('credential_use','credential_use_role_deleted')`).Scan(&protected); err != nil || !protected {
		t.Fatal("migration weakened protected audit controls", err)
	}

	// Exercise the installed barrier and projection in a transaction rolled back
	// below. Synthetic audit rows are test probes, never migration side effects.
	probe, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback(context.Background())
	expectRejected := func(statement, code, constraint string, args ...any) {
		t.Helper()
		savepoint, e := probe.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = savepoint.Exec(ctx, statement, args...)
		var databaseError *pgconn.PgError
		denied := errors.As(e, &databaseError) && databaseError.Code == code && (constraint == "" || databaseError.ConstraintName == constraint)
		if rollbackErr := savepoint.Rollback(ctx); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if !denied {
			t.Fatalf("installed guard did not reject the mutation with %s", code)
		}
	}
	for _, role := range []string{"platform_admin", "old-role"} {
		var eligible bool
		if err = probe.QueryRow(ctx, `SELECT adtr.credential_use_role_eligible('old','old-domain',$1)`, role).Scan(&eligible); err != nil || !eligible {
			t.Fatal("historical function/scope eligibility was not preserved", err)
		}
		expectRejected(`INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by)
VALUES('old','old-domain','old-account','domain.connection_test',$1,1,true,$2)`, "42501", "credential_use_governance", role, adminID)
	}
	if _, err = probe.Exec(ctx, `SELECT set_config('adtr.audit_username','synthetic-v12-writer',true),set_config('adtr.audit_ip','192.0.2.12',true),set_config('adtr.audit_path','/synthetic/v12',true),set_config('adtr.audit_request_id','synthetic-v12-request',true)`); err != nil {
		t.Fatal(err)
	}
	var eventID int64
	if err = probe.QueryRow(ctx, `INSERT INTO adtr.operation_account_use_audit(tenant_id,actor_id,domain_id,account_id,role_id,purpose,action,old_grant_revision,new_grant_revision,account_credential_revision,allowed,result)
VALUES('old',$1,'old-domain','old-account','old-role','domain.connection_test','credential_use_revoke',1,2,1,false,'revoked') RETURNING id`, adminID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	var projected bool
	if err = probe.QueryRow(ctx, `SELECT tenant_id='old' AND user_id=$2 AND domain_id='old-domain'
AND event='credential_use_revoke' AND event_result='SUCCESS' AND result_available AND log_type=8
AND login_user='synthetic-v12-writer' AND source_ip='192.0.2.12'
AND audit_path='/synthetic/v12' AND audit_request_id='synthetic-v12-request'
AND event_args=jsonb_build_object('domainId','old-domain','accountId','old-account','roleId','old-role',
'purpose','domain.connection_test','oldGrantRevision','1','grantRevision','2','accountCredentialRevision','1',
'allowed',false,'code','revoked','path','/synthetic/v12','requestId','synthetic-v12-request')
FROM adtr.audit_source WHERE source='credential_use' AND source_id=$1`, eventID, adminID).Scan(&projected); err != nil || !projected {
		t.Fatal("credential use audit projection is not domain-scoped and metadata-only", err)
	}
	expectRejected(`UPDATE adtr.operation_account_use_audit SET result='saved' WHERE id=$1`, "P0001", "", eventID)
	expectRejected(`DELETE FROM adtr.operation_account_use_audit WHERE id=$1`, "P0001", "", eventID)
	for _, table := range []string{"operation_account_use_grants", "operation_account_use_mutations", "operation_account_use_audit"} {
		expectRejected("TRUNCATE "+pgx.Identifier{"adtr", table}.Sanitize(), "P0001", "")
	}
	if err = probe.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertPreserved()
}
