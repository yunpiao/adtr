//go:build integration

package credentialuse_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const (
	cuTenant       = "credential-guard-test"
	cuRole         = "credential_role_00000001"
	cuOtherRole    = "credential_role_00000002"
	cuAccount      = "credential_account_000001"
	cuOtherAccount = "credential_account_000002"
)

type governanceFixture struct {
	ctx     context.Context
	config  *pgx.ConnConfig
	conn    *pgx.Conn
	users   map[string]int64
	initial credentialuse.Receipt
}

// The fixture installs the production migrator, including all cross-module
// triggers. There is intentionally no missing-table fallback or fake version.
func newGovernanceFixture(t *testing.T) *governanceFixture {
	t.Helper()
	return newGovernanceFixtureAtVersion(t, store.SchemaVersion)
}

// Only explicit fragment tests use historical schemas; ordinary governance
// tests keep the real current migrator and readiness contract.
func newGovernanceFixtureAtVersion(t *testing.T, version int) *governanceFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL is required: real PostgreSQL credential governance integration is blocked")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated PostgreSQL test configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("isolated PostgreSQL test connection failed", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	name := fmt.Sprintf("adtr_credential_guards_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error("remove isolated governance database", err)
		}
	})
	cfg = cfg.Copy()
	cfg.Database = name
	migrate := store.Migrate
	if version == store.SchemaVersion {
		// Runtime fixtures always use the complete production migrator.
	} else if version == 14 {
		migrate = store.MigrateDirectoryBaselineForTest
	} else if version == 15 {
		migrate = store.MigrateDirectoryV2BaselineForTest
	} else {
		t.Fatal("unsupported fixture schema version")
	}
	if err = migrate(ctx, cfg); err != nil {
		t.Fatal("install real governance migration", err)
	}
	if version == store.SchemaVersion {
		if err = store.Ready(ctx, cfg); err != nil {
			t.Fatal("migrated database is not current", err)
		}
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	f := &governanceFixture{ctx: ctx, config: cfg, conn: conn, users: make(map[string]int64)}
	var actual int
	if err = conn.QueryRow(ctx, `SELECT version FROM adtr.schema_version`).Scan(&actual); err != nil || actual != version {
		t.Fatal("fixture did not install the requested historical/current schema", actual, err)
	}
	var installed bool
	if err = conn.QueryRow(ctx, `SELECT to_regclass('adtr.operation_account_use_grants') IS NOT NULL
 AND EXISTS(SELECT FROM pg_trigger WHERE tgrelid='adtr.users'::regclass AND tgname='credential_use_user_guard' AND NOT tgisinternal)`).Scan(&installed); err != nil || !installed {
		t.Fatal("production credential governance tables and guards are required", err)
	}
	f.exec(t, conn, `INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)
 VALUES($1,10,extract(epoch FROM clock_timestamp()+interval '1 year')::bigint,'synthetic-tenant','Guard tests')`, cuTenant)
	f.exec(t, conn, `INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,'Credential role'),($1,$3,'Other role')`, cuTenant, cuRole, cuOtherRole)
	f.exec(t, conn, `INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT $1,r,mark,true,true FROM unnest(ARRAY[$2,$3]::text[]) r CROSS JOIN unnest(ARRAY['domains','tasks','operation_accounts']) mark`, cuTenant, cuRole, cuOtherRole)
	f.exec(t, conn, `INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES($1,'domain-one','one.invalid'),($1,'domain-two','two.invalid')`, cuTenant)
	f.exec(t, conn, `INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port,credential_mode)
 VALUES($1,'domain-one','one.invalid','dc.one.invalid','starttls','389','unconfigured'),($1,'domain-two','two.invalid','dc.two.invalid','starttls','389','unconfigured')`, cuTenant)
	f.exec(t, conn, `INSERT INTO adtr.resource_groups(tenant_id,id,name)
 VALUES($1,'admin-group','Admin scope'),($1,'role-group','Credential scope'),($1,'other-group','Other scope'),($1,'spare-group','Spare scope')`, cuTenant)
	f.exec(t, conn, `INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id)
 VALUES($1,'admin-group','domain-one'),($1,'role-group','domain-one'),($1,'other-group','domain-one'),($1,'other-group','domain-two')`, cuTenant)
	f.exec(t, conn, `INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id)
 VALUES($1,'platform_admin','admin-group'),($1,$2,'role-group'),($1,$3,'other-group')`, cuTenant, cuRole, cuOtherRole)
	for _, u := range []struct {
		name, tenant, role, custom       string
		disabled, forced, expired, noMFA bool
	}{
		{name: "admin", tenant: cuTenant, role: "platform_admin"},
		{name: "disabled-admin", tenant: cuTenant, role: "platform_admin", disabled: true},
		{name: "forced-admin", tenant: cuTenant, role: "platform_admin", forced: true},
		{name: "expired-admin", tenant: cuTenant, role: "platform_admin", expired: true},
		{name: "no-mfa-admin", tenant: cuTenant, role: "platform_admin", noMFA: true},
		{name: "foreign-admin", tenant: "foreign-tenant", role: "platform_admin"},
		{name: "member", tenant: cuTenant, role: "viewer", custom: cuRole},
		{name: "disabled-member", tenant: cuTenant, role: "viewer", custom: cuRole, disabled: true},
		{name: "forced-member", tenant: cuTenant, role: "viewer", custom: cuRole, forced: true},
		{name: "expired-member", tenant: cuTenant, role: "viewer", custom: cuRole, expired: true},
		{name: "other-member", tenant: cuTenant, role: "viewer", custom: cuOtherRole},
	} {
		var id int64
		err = conn.QueryRow(ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,disabled,must_change,password_updated_at,mfa_secret)
 VALUES($1,$2,'synthetic-hash',$3,$4,$5,$6,clock_timestamp()-CASE WHEN $7 THEN interval '100 days' ELSE interval '1 minute' END,
 CASE WHEN $8 THEN '' ELSE 'synthetic-mfa' END) RETURNING id`, u.tenant, u.name, u.role, u.custom, u.disabled, u.forced, u.expired, u.noMFA).Scan(&id)
		if err != nil {
			t.Fatal("seed synthetic actor", err)
		}
		f.users[u.name] = id
	}
	tx := f.begin(t)
	for _, a := range []struct{ id, domain string }{{cuAccount, "domain-one"}, {cuOtherAccount, "domain-two"}} {
		f.exec(t, tx, `INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES($1,$2,$3,'Synthetic account')`, cuTenant, a.domain, a.id)
		f.exec(t, tx, `INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext)
 VALUES($1,$2,$3,1,'synthetic-unavailable-key',decode(repeat('ab',32),'hex'))`, cuTenant, a.domain, a.id)
		f.exec(t, tx, `INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES($1,$2,'operation_accounts',$3)`, cuTenant, a.domain, a.id)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("commit complete account fixture", err)
	}
	f.initial = f.grant(t, cuRole, "initial-allow")
	return f
}

type governanceExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (f *governanceFixture) exec(t *testing.T, db governanceExecer, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *governanceFixture) begin(t *testing.T) pgx.Tx {
	t.Helper()
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func (f *governanceFixture) principal(name string) tasks.Principal {
	tenant := cuTenant
	if name == "foreign-admin" {
		tenant = "foreign-tenant"
	}
	return tasks.Principal{TenantID: tenant, ActorID: f.users[name]}
}

func (f *governanceFixture) context(t *testing.T, tx pgx.Tx, name string, op credentialuse.GovernanceOperation) {
	t.Helper()
	if err := credentialuse.SetGovernanceContext(f.ctx, tx, f.principal(name), op); err != nil {
		t.Fatal(err)
	}
}

func (f *governanceFixture) self(t *testing.T, tx pgx.Tx, name string, op credentialuse.SelfOperation) {
	t.Helper()
	if err := credentialuse.SetSelfContext(f.ctx, tx, f.principal(name), op); err != nil {
		t.Fatal(err)
	}
}

func (f *governanceFixture) grant(t *testing.T, role, key string) credentialuse.Receipt {
	t.Helper()
	tx := f.begin(t)
	f.context(t, tx, "admin", credentialuse.GrantUse)
	r, err := credentialuse.New().MutateTx(f.ctx, tx, f.principal("admin"), "/grant", credentialuse.Input{
		AccountID: cuAccount, RoleID: role, Purpose: credentialuse.Purpose,
		ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: key,
	}, []string{"domain-one"})
	if err != nil {
		t.Fatal("create explicit fixture allow", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

func requireGovernanceDenied(t *testing.T, err error) {
	t.Helper()
	if !credentialuse.IsGovernanceError(err) {
		t.Fatalf("expected SQL credential governance rejection, got %v", err)
	}
}

func clearGovernanceContext(t *testing.T, f *governanceFixture, tx pgx.Tx) {
	f.exec(t, tx, `SELECT set_config('adtr.credential_use_protocol','',true),set_config('adtr.credential_use_tenant','',true),
 set_config('adtr.credential_use_actor','',true),set_config('adtr.credential_use_operation','',true),set_config('adtr.credential_use_scope','',true)`)
}

func TestCredentialUseSQLRequiresCompatibleScopedAdmin(t *testing.T) {
	f := newGovernanceFixture(t)
	t.Run("effective-view-binds-current-actor-role", func(t *testing.T) {
		tx := f.begin(t)
		_, err := credentialuse.New().EffectiveTx(f.ctx, tx, f.principal("admin"), cuRole, cuAccount, []string{"domain-one"})
		var denied *credentialuse.Error
		if !errors.As(err, &denied) || denied.Status != 404 {
			t.Fatal("effective view exposed another role's grant", err)
		}
		own, err := credentialuse.New().EffectiveTx(f.ctx, tx, f.principal("member"), cuRole, cuAccount, []string{"domain-one"})
		if err != nil || !own.ExplicitlyGranted || !own.Eligible || own.GrantRevision != f.initial.GrantRevision {
			t.Fatal("effective view lost the actor's own current grant", own, err)
		}
	})
	const insertGrant = `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by)
 VALUES($1,'domain-one',$2,'domain.connection_test',$3,1,true,$4)`
	for _, name := range []string{"old-binary", "member", "foreign-admin", "disabled-admin", "forced-admin", "expired-admin", "no-mfa-admin"} {
		t.Run(name, func(t *testing.T) {
			tx := f.begin(t)
			actor := f.users["admin"]
			if name != "old-binary" {
				f.context(t, tx, name, credentialuse.GrantUse)
				actor = f.users[name]
			}
			_, err := tx.Exec(f.ctx, insertGrant, cuTenant, cuAccount, cuOtherRole, actor)
			requireGovernanceDenied(t, err)
		})
	}
	for _, tc := range []struct{ name, sql string }{
		{"old-protocol", `SELECT set_config('adtr.credential_use_protocol','credential_governance_v0',true)`},
		{"wrong-operation", `SELECT set_config('adtr.credential_use_operation','manage_roles',true)`},
		{"foreign-actor-id", `SELECT set_config('adtr.credential_use_actor',(SELECT id::text FROM adtr.users WHERE username='foreign-admin'),true)`},
		{"malformed-scope", `SELECT set_config('adtr.credential_use_scope','not-json',true)`},
		{"scope-root-scalar", `SELECT set_config('adtr.credential_use_scope','"wrong-shape"',true)`},
		{"scope-domains-object", `SELECT adtr.credential_use_capture_scope(); SELECT set_config('adtr.credential_use_scope',jsonb_set(current_setting('adtr.credential_use_scope')::jsonb,'{domains}','{}')::text,true)`},
		{"scope-domains-scalar", `SELECT adtr.credential_use_capture_scope(); SELECT set_config('adtr.credential_use_scope',jsonb_set(current_setting('adtr.credential_use_scope')::jsonb,'{domains}','"domain-one"')::text,true)`},
		{"scope-domains-null", `SELECT adtr.credential_use_capture_scope(); SELECT set_config('adtr.credential_use_scope',jsonb_set(current_setting('adtr.credential_use_scope')::jsonb,'{domains}','null')::text,true)`},
		{"scope-domains-nonstring-element", `SELECT adtr.credential_use_capture_scope(); SELECT set_config('adtr.credential_use_scope',jsonb_set(current_setting('adtr.credential_use_scope')::jsonb,'{domains}','["domain-one",42]')::text,true)`},
		{"scope-operation-mismatch", `SELECT adtr.credential_use_capture_scope(); SELECT set_config('adtr.credential_use_scope',jsonb_set(current_setting('adtr.credential_use_scope')::jsonb,'{operation}','"revoke"')::text,true)`},
		{"scope-actor-mismatch", `SELECT adtr.credential_use_capture_scope(); SELECT set_config('adtr.credential_use_scope',jsonb_set(current_setting('adtr.credential_use_scope')::jsonb,'{actor}','"0"')::text,true)`},
		{"scope-tenant-mismatch", `SELECT adtr.credential_use_capture_scope(); SELECT set_config('adtr.credential_use_scope',jsonb_set(current_setting('adtr.credential_use_scope')::jsonb,'{tenant}','"foreign"')::text,true)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.begin(t)
			f.context(t, tx, "admin", credentialuse.GrantUse)
			f.exec(t, tx, tc.sql)
			_, err := tx.Exec(f.ctx, insertGrant, cuTenant, cuAccount, cuOtherRole, f.users["admin"])
			requireGovernanceDenied(t, err)
		})
	}
	t.Run("missing-exact-domain-scope", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.GrantUse)
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by)
 VALUES($1,'domain-two',$2,'domain.connection_test',$3,1,true,$4)`, cuTenant, cuOtherAccount, cuOtherRole, f.users["admin"])
		requireGovernanceDenied(t, err)
	})
	t.Run("wrong-current-credential-revision", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.GrantUse)
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by)
 VALUES($1,'domain-one',$2,'domain.connection_test',$3,2,true,$4)`, cuTenant, cuAccount, cuOtherRole, f.users["admin"])
		requireGovernanceDenied(t, err)
	})
}

func TestCredentialUseSQLProtectsDormantIndirectAuthority(t *testing.T) {
	f := newGovernanceFixture(t)
	for _, dormant := range []string{"function-rights", "resource-scope"} {
		t.Run(dormant, func(t *testing.T) {
			for _, tc := range []struct {
				name, query string
				args        []any
			}{
				{"new-member", `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id) VALUES($1,'old-style-new-member','synthetic','viewer',$2)`, []any{cuTenant, cuRole}},
				{"assign-member", `UPDATE adtr.users SET role_id=$1 WHERE username='other-member'`, []any{cuRole}},
				{"reactivate-member", `UPDATE adtr.users SET disabled=false WHERE username='disabled-member'`, nil},
				{"reset-password", `UPDATE adtr.users SET password_hash='unauthorized-reset' WHERE username='member'`, nil},
				{"reset-mfa", `UPDATE adtr.users SET mfa_secret='' WHERE username='member'`, nil},
				{"new-function-mark", `INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'users',true,true)`, []any{cuTenant, cuRole}},
				{"replace-function-row", `DELETE FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark='domains'`, []any{cuTenant, cuRole}},
				{"change-function-row", `UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id=$1 AND role_id=$2 AND mark='tasks'`, []any{cuTenant, cuRole}},
				{"new-resource-binding", `INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,$2,'spare-group')`, []any{cuTenant, cuRole}},
				{"replace-resource-binding", `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, []any{cuTenant, cuRole}},
				{"new-resource-member", `INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,'role-group','domain-two')`, []any{cuTenant}},
				{"delete-linked-group", `DELETE FROM adtr.resource_groups WHERE tenant_id=$1 AND id='role-group'`, []any{cuTenant}},
				{"delete-role", `DELETE FROM adtr.access_roles WHERE tenant_id=$1 AND id=$2`, []any{cuTenant, cuRole}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					tx := f.begin(t)
					f.context(t, tx, "admin", credentialuse.ManageRoles)
					if dormant == "function-rights" {
						f.exec(t, tx, `UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='tasks'`, cuTenant, cuRole)
					} else {
						// Retain the role/group relation but remove its domain. Both
						// the missing-scope state and future restoration are protected.
						f.exec(t, tx, `DELETE FROM adtr.resource_group_members WHERE tenant_id=$1 AND group_id='role-group'`, cuTenant)
					}
					var allowed, eligible bool
					if err := tx.QueryRow(f.ctx, `SELECT allowed,adtr.credential_use_role_eligible(tenant_id,domain_id,role_id) FROM adtr.operation_account_use_grants`).Scan(&allowed, &eligible); err != nil || !allowed || eligible {
						t.Fatal("fixture must retain a dormant allow", allowed, eligible, err)
					}
					clearGovernanceContext(t, f, tx)
					_, err := tx.Exec(f.ctx, tc.query, tc.args...)
					requireGovernanceDenied(t, err)
				})
			}
		})
	}
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("tenant-renewal-expired-%t", expired), func(t *testing.T) {
			tx := f.begin(t)
			f.context(t, tx, "admin", credentialuse.ManageTenant)
			if expired {
				f.exec(t, tx, `UPDATE adtr.resource_tenant_config SET expire_time=0 WHERE tenant_id=$1`, cuTenant)
			} else {
				f.exec(t, tx, `UPDATE adtr.resource_tenant_config SET max_ad_count=0 WHERE tenant_id=$1`, cuTenant)
			}
			clearGovernanceContext(t, f, tx)
			_, err := tx.Exec(f.ctx, `UPDATE adtr.resource_tenant_config SET max_ad_count=10,expire_time=extract(epoch FROM clock_timestamp()+interval '1 year')::bigint WHERE tenant_id=$1`, cuTenant)
			requireGovernanceDenied(t, err)
		})
	}
}

func TestCredentialUseSQLExactSelfServiceAndReducingChanges(t *testing.T) {
	f := newGovernanceFixture(t)
	for _, name := range []string{"member", "forced-member", "expired-member"} {
		t.Run("self-password-"+name, func(t *testing.T) {
			tx := f.begin(t)
			f.self(t, tx, name, credentialuse.ChangePassword)
			var before int64
			if err := tx.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users[name]).Scan(&before); err != nil {
				t.Fatal(err)
			}
			var after int64
			if err := tx.QueryRow(f.ctx, `UPDATE adtr.users SET password_hash='synthetic-new-password',pass_strength='high',must_change=false,password_updated_at=clock_timestamp() WHERE id=$1 RETURNING authorization_version`, f.users[name]).Scan(&after); err != nil || after <= before {
				t.Fatal("exact self password update must succeed and invalidate old authorization", err)
			}
		})
	}
	t.Run("self-mfa-lifecycle", func(t *testing.T) {
		tx := f.begin(t)
		f.self(t, tx, "member", credentialuse.BeginMFA)
		f.exec(t, tx, `UPDATE adtr.users SET mfa_pending='synthetic-new-mfa',mfa_pending_until=clock_timestamp()+interval '5 minutes' WHERE id=$1`, f.users["member"])
		f.self(t, tx, "member", credentialuse.ConfirmMFA)
		f.exec(t, tx, `UPDATE adtr.users SET mfa_secret=mfa_pending,mfa_pending='',mfa_pending_until=NULL,mfa_last_step=12 WHERE id=$1`, f.users["member"])
		f.self(t, tx, "member", credentialuse.DisableMFA)
		f.exec(t, tx, `UPDATE adtr.users SET mfa_secret='',mfa_pending='',mfa_pending_until=NULL,mfa_last_step=-1 WHERE id=$1`, f.users["member"])
		var empty bool
		if err := tx.QueryRow(f.ctx, `SELECT mfa_secret='' AND mfa_pending='' AND mfa_pending_until IS NULL AND mfa_last_step=-1 FROM adtr.users WHERE id=$1`, f.users["member"]).Scan(&empty); err != nil || !empty {
			t.Fatal("self MFA state did not complete", err)
		}
	})
	for _, tc := range []struct {
		name, actor string
		op          credentialuse.SelfOperation
		query       string
	}{
		{"password-plus-profile", "member", credentialuse.ChangePassword, `UPDATE adtr.users SET password_hash='new-hash',must_change=false,email='changed@example.invalid' WHERE username='member'`},
		{"password-plus-role", "member", credentialuse.ChangePassword, `UPDATE adtr.users SET password_hash='new-hash',must_change=false,role_id='' WHERE username='member'`},
		{"password-plus-mfa", "member", credentialuse.ChangePassword, `UPDATE adtr.users SET password_hash='new-hash',must_change=false,mfa_secret='' WHERE username='member'`},
		{"other-user", "member", credentialuse.ChangePassword, `UPDATE adtr.users SET password_hash='new-hash',must_change=false WHERE username='forced-member'`},
		{"disabled-user", "disabled-member", credentialuse.ChangePassword, `UPDATE adtr.users SET password_hash='new-hash',must_change=false WHERE username='disabled-member'`},
		{"reactivation", "disabled-member", credentialuse.ChangePassword, `UPDATE adtr.users SET password_hash='new-hash',must_change=false,disabled=false WHERE username='disabled-member'`},
		{"mfa-plus-password", "member", credentialuse.BeginMFA, `UPDATE adtr.users SET password_hash='new-hash',mfa_pending='pending',mfa_pending_until=clock_timestamp()+interval '5 minutes' WHERE username='member'`},
		{"confirm-without-pending", "member", credentialuse.ConfirmMFA, `UPDATE adtr.users SET mfa_secret='invented',mfa_pending='',mfa_pending_until=NULL,mfa_last_step=12 WHERE username='member'`},
		{"wrong-self-class", "member", credentialuse.DisableMFA, `UPDATE adtr.users SET password_hash='new-hash',must_change=false WHERE username='member'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.begin(t)
			f.self(t, tx, tc.actor, tc.op)
			_, err := tx.Exec(f.ctx, tc.query)
			requireGovernanceDenied(t, err)
		})
	}
	t.Run("profile-disable-and-proof-bookkeeping", func(t *testing.T) {
		tx := f.begin(t)
		f.exec(t, tx, `UPDATE adtr.users SET real_name='Synthetic member',email='member@example.invalid' WHERE username='member'`)
		f.exec(t, tx, `UPDATE adtr.users SET disabled=true WHERE username='member'`)
		f.exec(t, tx, `UPDATE adtr.users SET mfa_last_step=mfa_last_step+1 WHERE username='member'`)
	})
	t.Run("mfa-proof-rewind-denied", func(t *testing.T) {
		tx := f.begin(t)
		_, err := tx.Exec(f.ctx, `UPDATE adtr.users SET mfa_last_step=mfa_last_step-1 WHERE username='member'`)
		requireGovernanceDenied(t, err)
	})
	t.Run("scoped-admin-reset", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ResetPassword)
		f.exec(t, tx, `UPDATE adtr.users SET password_hash='synthetic-admin-reset',must_change=true WHERE username='member'`)
	})
}

func TestCredentialUseSQLScopeSnapshotAndDeferredInvariant(t *testing.T) {
	f := newGovernanceFixture(t)
	t.Run("custom-only-allow-still-needs-management-scope", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id='platform_admin'`, cuTenant)
		requireGovernanceDenied(t, tx.Commit(f.ctx))
	})
	t.Run("revoke-before-final-scope-removal", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.RevokeUse)
		f.exec(t, tx, `UPDATE adtr.operation_account_use_grants SET allowed=false,updated_by=$1`, f.users["admin"])
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id='platform_admin'`, cuTenant)
		// Execute the actual deferred invariant without changing the fixture
		// shared by the following snapshot tests.
		f.exec(t, tx, `SET CONSTRAINTS ALL IMMEDIATE`)
	})
	f.grant(t, "platform_admin", "explicit-admin-self-grant")
	t.Run("delete-reinsert-admin-membership", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `DELETE FROM adtr.resource_group_members WHERE tenant_id=$1 AND group_id='admin-group'`, cuTenant)
		f.exec(t, tx, `INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,'admin-group','domain-one')`, cuTenant)
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal("valid temporary scope gap must commit", err)
		}
	})
	t.Run("delete-reinsert-admin-group-cascade", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `DELETE FROM adtr.resource_groups WHERE tenant_id=$1 AND id='admin-group'`, cuTenant)
		f.exec(t, tx, `INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES($1,'admin-group','Admin scope')`, cuTenant)
		f.exec(t, tx, `INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,'admin-group','domain-one')`, cuTenant)
		f.exec(t, tx, `INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,'platform_admin','admin-group')`, cuTenant)
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal("parent snapshot must survive FK cascades", err)
		}
	})
	t.Run("last-scope-removal-rejected-at-commit", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id='platform_admin'`, cuTenant)
		requireGovernanceDenied(t, tx.Commit(f.ctx))
		var kept bool
		if err := f.conn.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id='platform_admin')`, cuTenant).Scan(&kept); err != nil || !kept {
			t.Fatal("failed invariant did not roll back scope", err)
		}
	})
	t.Run("trusted-helper-resets-prewrite-snapshot", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `DELETE FROM adtr.resource_group_members WHERE tenant_id=$1 AND group_id='admin-group'`, cuTenant)
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,'admin-group','domain-one')`, cuTenant)
		requireGovernanceDenied(t, err)
	})
}

func TestCredentialUseSQLContextEndsWithTransaction(t *testing.T) {
	f := newGovernanceFixture(t)
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit-%t", commit), func(t *testing.T) {
			tx := f.begin(t)
			f.context(t, tx, "admin", credentialuse.ManageRoles)
			f.exec(t, tx, `UPDATE adtr.access_permissions SET readable=readable WHERE tenant_id=$1 AND role_id=$2 AND mark='tasks'`, cuTenant, cuRole)
			var err error
			if commit {
				err = tx.Commit(f.ctx)
			} else {
				err = tx.Rollback(f.ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, setting := range []string{"protocol", "tenant", "actor", "operation", "scope"} {
				var empty bool
				if err = f.conn.QueryRow(f.ctx, `SELECT COALESCE(current_setting($1,true),'')=''`, "adtr.credential_use_"+setting).Scan(&empty); err != nil || !empty {
					t.Fatal("transaction context leaked", setting, err)
				}
			}
			next := f.begin(t)
			_, err = next.Exec(f.ctx, `UPDATE adtr.access_permissions SET readable=readable WHERE tenant_id=$1 AND role_id=$2 AND mark='tasks'`, cuTenant, cuRole)
			requireGovernanceDenied(t, err)
		})
	}
}

func TestCredentialUseSQLTenantRecreationAndExactAdminReset(t *testing.T) {
	f := newGovernanceFixture(t)
	t.Run("deleted-config-cannot-be-recreated-by-old-binary", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageTenant)
		f.exec(t, tx, `DELETE FROM adtr.resource_tenant_config WHERE tenant_id=$1`, cuTenant)
		clearGovernanceContext(t, f, tx)
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)
 VALUES($1,10,extract(epoch FROM clock_timestamp()+interval '1 year')::bigint,'synthetic-tenant','Guard tests')`, cuTenant)
		requireGovernanceDenied(t, err)
	})
	t.Run("compatible-admin-can-renew-expired-entitlement", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageTenant)
		f.exec(t, tx, `UPDATE adtr.resource_tenant_config SET expire_time=0 WHERE tenant_id=$1`, cuTenant)
		f.context(t, tx, "admin", credentialuse.ManageTenant)
		f.exec(t, tx, `UPDATE adtr.resource_tenant_config SET expire_time=extract(epoch FROM clock_timestamp()+interval '1 year')::bigint WHERE tenant_id=$1`, cuTenant)
	})
	t.Run("expired-entitlement-allows-revocation", func(t *testing.T) {
		tx := f.begin(t)
		f.context(t, tx, "admin", credentialuse.ManageTenant)
		f.exec(t, tx, `UPDATE adtr.resource_tenant_config SET expire_time=0 WHERE tenant_id=$1`, cuTenant)
		f.context(t, tx, "admin", credentialuse.RevokeUse)
		f.exec(t, tx, `UPDATE adtr.operation_account_use_grants SET allowed=false,updated_by=$1`, f.users["admin"])
	})
	for _, tc := range []struct{ name, fields string }{
		{"mfa", `password_hash='new-hash',must_change=true,mfa_secret=''`},
		{"profile", `password_hash='new-hash',must_change=true,email='changed@example.invalid'`},
		{"role", `password_hash='new-hash',must_change=true,role_id=''`},
		{"not-forced", `password_hash='new-hash',must_change=false`},
	} {
		t.Run("reset-password-plus-"+tc.name, func(t *testing.T) {
			tx := f.begin(t)
			f.context(t, tx, "admin", credentialuse.ResetPassword)
			_, err := tx.Exec(f.ctx, `UPDATE adtr.users SET `+tc.fields+` WHERE username='member'`)
			requireGovernanceDenied(t, err)
		})
	}
}

func TestCredentialUseSQLAutomaticAccountInvalidation(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleting-%t", deleting), func(t *testing.T) {
			f := newGovernanceFixture(t)
			var initialEpoch int64
			if err := f.conn.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["member"]).Scan(&initialEpoch); err != nil {
				t.Fatal(err)
			}
			tx := f.begin(t)
			f.context(t, tx, "member", credentialuse.MutateAccount)
			f.exec(t, tx, `UPDATE adtr.operation_accounts SET label='Metadata-only edit',revision=revision+1 WHERE account_id=$1`, cuAccount)
			var kept, sameEpoch bool
			if err := tx.QueryRow(f.ctx, `SELECT (SELECT allowed FROM adtr.operation_account_use_grants WHERE role_id=$1),
 (SELECT authorization_version=$2 FROM adtr.users WHERE id=$3)`, cuRole, initialEpoch, f.users["member"]).Scan(&kept, &sameEpoch); err != nil || !kept || !sameEpoch {
				t.Fatal("label edit invalidated authority", err)
			}
			if deleting {
				f.exec(t, tx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1,deleted_at=clock_timestamp() WHERE account_id=$1`, cuAccount)
				f.exec(t, tx, `DELETE FROM adtr.operation_account_credentials WHERE account_id=$1`, cuAccount)
				f.exec(t, tx, `DELETE FROM adtr.domain_dependencies WHERE module='operation_accounts' AND object_id=$1`, cuAccount)
			} else {
				f.exec(t, tx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE account_id=$1`, cuAccount)
				f.exec(t, tx, `UPDATE adtr.operation_account_credentials SET credential_revision=credential_revision+1,ciphertext=decode(repeat('cd',32),'hex') WHERE account_id=$1`, cuAccount)
			}
			if err := tx.Commit(f.ctx); err != nil {
				t.Fatal("custom account writer must atomically invalidate stored allows", err)
			}
			var allowed bool
			var bound, revision, attributed, epoch int64
			if err := f.conn.QueryRow(f.ctx, `SELECT g.allowed,g.account_credential_revision,g.grant_revision,g.updated_by,u.authorization_version
 FROM adtr.operation_account_use_grants g JOIN adtr.users u ON u.id=$1 WHERE g.account_id=$2`, f.users["member"], cuAccount).Scan(&allowed, &bound, &revision, &attributed, &epoch); err != nil {
				t.Fatal(err)
			}
			oldRevision, _ := strconv.ParseInt(f.initial.GrantRevision, 10, 64)
			if allowed || bound != 1 || revision <= oldRevision || attributed != f.users["member"] || epoch <= initialEpoch {
				t.Fatal("automatic revoke lost the old credential binding, fresh revision, actual writer, or epoch")
			}
			var auditActor int64
			var action string
			if err := f.conn.QueryRow(f.ctx, `SELECT actor_id,action FROM adtr.operation_account_use_audit WHERE account_id=$1 ORDER BY id DESC LIMIT 1`, cuAccount).Scan(&auditActor, &action); err != nil {
				t.Fatal(err)
			}
			want := "credential_use_pair_replaced"
			if deleting {
				want = "credential_use_account_deleted"
			}
			if auditActor != f.users["member"] || action != want {
				t.Fatal("automatic invalidation used the old grant creator or wrong event", auditActor, action)
			}
		})
	}
	t.Run("unknown-dependency-still-blocks-pair-replacement", func(t *testing.T) {
		f := newGovernanceFixture(t)
		tx := f.begin(t)
		f.exec(t, tx, `INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES($1,'domain-one',$2,'synthetic.unknown','test-dependency')`, cuTenant, cuAccount)
		f.context(t, tx, "member", credentialuse.MutateAccount)
		_, err := tx.Exec(f.ctx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE account_id=$1`, cuAccount)
		requireSQLState(t, err, "23514")
	})
}

func TestCredentialUseSQLRoleCascadeKeepsAuditAndReceipt(t *testing.T) {
	f := newGovernanceFixture(t)
	const role = "credential_role_00000003"
	f.exec(t, f.conn, `INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,'Disposable credential role')`, cuTenant, role)
	f.exec(t, f.conn, `INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT $1,$2,mark,true,true FROM unnest(ARRAY['domains','tasks','operation_accounts']) mark`, cuTenant, role)
	f.exec(t, f.conn, `INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,$2,'role-group')`, cuTenant, role)
	receipt := f.grant(t, role, "disposable-role-grant")
	tx := f.begin(t)
	f.context(t, tx, "admin", credentialuse.ManageRoles)
	f.exec(t, tx, `DELETE FROM adtr.access_roles WHERE tenant_id=$1 AND id=$2`, cuTenant, role)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal("authorized role cascade failed", err)
	}
	var grants, receipts, events int
	var actor int64
	if err := f.conn.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM adtr.operation_account_use_grants WHERE role_id=$1),
 (SELECT count(*) FROM adtr.operation_account_use_mutations WHERE role_id=$1),
 (SELECT count(*) FROM adtr.operation_account_use_audit WHERE role_id=$1),
 (SELECT actor_id FROM adtr.operation_account_use_audit WHERE role_id=$1 AND action='credential_use_role_deleted')`, role).Scan(&grants, &receipts, &events, &actor); err != nil {
		t.Fatal(err)
	}
	if grants != 0 || receipts != 1 || events != 2 || actor != f.users["admin"] {
		t.Fatal("role cascade erased historical identity or omitted its audit", grants, receipts, events, actor)
	}
	tx = f.begin(t)
	recovered, err := credentialuse.New().ReceiptTx(f.ctx, tx, f.principal("admin"), "disposable-role-grant", []string{"domain-one"})
	if err != nil || recovered.GrantRevision != receipt.GrantRevision || recovered.RoleID != role || recovered.CurrentGrantRevision != "0" || recovered.CurrentAllowed {
		t.Fatal("receipt no longer recovers the committed original outcome", recovered, err)
	}
}

func TestCredentialUseSQLImmutableHistoryAndServerRevisions(t *testing.T) {
	f := newGovernanceFixture(t)
	for _, tc := range []struct {
		name, query string
		governance  bool
	}{
		{"grant-identity", `UPDATE adtr.operation_account_use_grants SET purpose='different.purpose'`, true},
		{"grant-revision", `UPDATE adtr.operation_account_use_grants SET grant_revision=grant_revision+1`, true},
		{"grant-provenance", `UPDATE adtr.operation_account_use_grants SET updated_by=0`, true},
		{"grant-delete", `DELETE FROM adtr.operation_account_use_grants`, true},
		{"receipt-update", `UPDATE adtr.operation_account_use_mutations SET fingerprint=repeat('0',64)`, false},
		{"receipt-delete", `DELETE FROM adtr.operation_account_use_mutations`, false},
		{"audit-update", `UPDATE adtr.operation_account_use_audit SET actor_id=0`, false},
		{"audit-delete", `DELETE FROM adtr.operation_account_use_audit`, false},
		{"grant-truncate", `TRUNCATE adtr.operation_account_use_grants`, false},
		{"receipt-truncate", `TRUNCATE adtr.operation_account_use_mutations`, false},
		{"audit-truncate", `TRUNCATE adtr.operation_account_use_audit`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.begin(t)
			f.context(t, tx, "admin", credentialuse.GrantUse)
			_, err := tx.Exec(f.ctx, tc.query)
			if tc.governance {
				requireGovernanceDenied(t, err)
			} else {
				requireSQLState(t, err, "P0001")
			}
		})
	}
	t.Run("unchanged-custom-grant-is-a-real-no-op", func(t *testing.T) {
		tx := f.begin(t)
		var epoch int64
		if err := tx.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["member"]).Scan(&epoch); err != nil {
			t.Fatal(err)
		}
		f.exec(t, tx, `UPDATE adtr.operation_account_use_grants SET allowed=allowed`)
		var same, unchanged bool
		if err := tx.QueryRow(f.ctx, `SELECT
 (SELECT grant_revision::text=$1 FROM adtr.operation_account_use_grants),
 (SELECT authorization_version=$2 FROM adtr.users WHERE id=$3)`, f.initial.GrantRevision, epoch, f.users["member"]).Scan(&same, &unchanged); err != nil || !same || !unchanged {
			t.Fatal("no-op grant update changed authorization", err)
		}
		var count int
		if err := tx.QueryRow(f.ctx, `SELECT count(*) FROM adtr.operation_account_use_audit`).Scan(&count); err != nil || count != 1 {
			t.Fatal("no-op SQL update invented an effective audit change", err)
		}
	})
	t.Run("context-free-receipt-insertion", func(t *testing.T) {
		tx := f.begin(t)
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.operation_account_use_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed)
 SELECT tenant_id,actor_id,'forged-key',operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed FROM adtr.operation_account_use_mutations`)
		requireGovernanceDenied(t, err)
	})
	for _, forged := range []struct{ name, actor, revision string }{
		{"wrong-actor", "0", "grant_revision"},
		{"wrong-current-revision", "actor_id", "grant_revision+1"},
	} {
		t.Run("receipt-"+forged.name, func(t *testing.T) {
			tx := f.begin(t)
			f.context(t, tx, "admin", credentialuse.GrantUse)
			_, err := tx.Exec(f.ctx, `INSERT INTO adtr.operation_account_use_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed)
 SELECT tenant_id,`+forged.actor+`,'forged-key',operation,fingerprint,domain_id,account_id,role_id,purpose,`+forged.revision+`,account_credential_revision,allowed FROM adtr.operation_account_use_mutations`)
			requireGovernanceDenied(t, err)
		})
	}
}

func TestCredentialUseSQLAuditAndReceiptFailuresRollBackWholeMutation(t *testing.T) {
	f := newGovernanceFixture(t)
	for _, table := range []string{"operation_account_use_audit", "operation_account_use_mutations"} {
		t.Run(table, func(t *testing.T) {
			var beforeEpoch, beforeProof int64
			if err := f.conn.QueryRow(f.ctx, `SELECT authorization_version,mfa_last_step FROM adtr.users WHERE id=$1`, f.users["admin"]).Scan(&beforeEpoch, &beforeProof); err != nil {
				t.Fatal(err)
			}
			tx := f.begin(t)
			// A test-only failing sink verifies the production trigger/store
			// transaction boundary. It never disables a production guard and
			// is itself removed by the failed transaction's rollback.
			f.exec(t, tx, `CREATE FUNCTION adtr.synthetic_credential_sink_failure() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic credential sink failure' USING ERRCODE='P0001'; END; $$`)
			f.exec(t, tx, `CREATE TRIGGER synthetic_credential_sink_failure BEFORE INSERT ON adtr.`+table+` FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_credential_sink_failure()`)
			f.exec(t, tx, `UPDATE adtr.users SET mfa_last_step=mfa_last_step+1 WHERE id=$1`, f.users["admin"])
			f.context(t, tx, "admin", credentialuse.GrantUse)
			_, err := credentialuse.New().MutateTx(f.ctx, tx, f.principal("admin"), "/grant", credentialuse.Input{
				AccountID: cuAccount, RoleID: cuOtherRole, Purpose: credentialuse.Purpose,
				ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "failed-sink-grant",
			}, []string{"domain-one"})
			requireSQLState(t, err, "P0001")
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			var afterEpoch, afterProof int64
			if err = f.conn.QueryRow(f.ctx, `SELECT authorization_version,mfa_last_step FROM adtr.users WHERE id=$1`, f.users["admin"]).Scan(&afterEpoch, &afterProof); err != nil {
				t.Fatal(err)
			}
			var grants, audits, receipts int
			if err = f.conn.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.operation_account_use_grants),
 (SELECT count(*) FROM adtr.operation_account_use_audit),(SELECT count(*) FROM adtr.operation_account_use_mutations)`).Scan(&grants, &audits, &receipts); err != nil {
				t.Fatal(err)
			}
			if afterEpoch != beforeEpoch || afterProof != beforeProof || grants != 1 || audits != 1 || receipts != 1 {
				t.Fatal("failed durable sink left consumed proof, changed authorization, or partial grant/history")
			}
		})
	}
}

func requireSQLState(t *testing.T, err error, state string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != state {
		t.Fatalf("expected PostgreSQL %s, got %v", state, err)
	}
}

func (f *governanceFixture) secondConnection(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.ConnectConfig(f.ctx, f.config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func TestCredentialUseSQLInversionFailsWithoutWaiting(t *testing.T) {
	f := newGovernanceFixture(t)
	for _, account := range []string{cuAccount, cuOtherAccount} {
		t.Run("account-epoch-"+account, func(t *testing.T) {
			other := f.secondConnection(t)
			rowTx, err := other.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rowTx.Rollback(context.Background())
			f.exec(t, rowTx, `SELECT id FROM adtr.users WHERE id=$1 FOR UPDATE`, f.users["member"])
			tenantTx := f.begin(t)
			f.context(t, tenantTx, "member", credentialuse.MutateAccount)
			f.exec(t, tenantTx, `SELECT adtr.task_auth_lock($1)`, cuTenant)
			f.exec(t, tenantTx, `SET LOCAL statement_timeout='1500ms'`)
			start := time.Now()
			_, err = tenantTx.Exec(f.ctx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE account_id=$1`, account)
			requireSQLState(t, err, "55P03")
			if time.Since(start) >= 1500*time.Millisecond {
				t.Fatal("actor lock inversion waited instead of failing NOWAIT")
			}
			if err = tenantTx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			var unchanged bool
			if err = f.conn.QueryRow(f.ctx, `SELECT revision=1 AND credential_revision=1 FROM adtr.operation_accounts WHERE account_id=$1`, account).Scan(&unchanged); err != nil || !unchanged {
				t.Fatal("failed epoch lock left a partial account revision", err)
			}
			var allowed bool
			if err = f.conn.QueryRow(f.ctx, `SELECT allowed FROM adtr.operation_account_use_grants WHERE account_id=$1`, cuAccount).Scan(&allowed); err != nil || !allowed {
				t.Fatal("failed invalidation did not roll back grant", err)
			}
		})
	}
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{"user-row-to-tenant", `UPDATE adtr.users SET real_name='blocked' WHERE id=$1`, []any{f.users["member"]}},
		{"new-user-before-grant-existence-check", `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id) VALUES($1,'blocked-new-user','synthetic','viewer',$2)`, []any{cuTenant, cuOtherRole}},
		{"new-function-before-grant-existence-check", `INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'users',true,true)`, []any{cuTenant, cuOtherRole}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := f.secondConnection(t)
			rowTx, err := other.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rowTx.Rollback(context.Background())
			f.exec(t, rowTx, `SELECT id FROM adtr.users WHERE id=$1 FOR UPDATE`, f.users["member"])
			tenantTx := f.begin(t)
			f.exec(t, tenantTx, `SELECT adtr.task_auth_lock($1)`, cuTenant)
			f.exec(t, rowTx, `SET LOCAL statement_timeout='1500ms'`)
			start := time.Now()
			_, err = rowTx.Exec(f.ctx, tc.sql, tc.args...)
			requireSQLState(t, err, "40001")
			if time.Since(start) >= 1500*time.Millisecond {
				t.Fatal("row-to-tenant inversion waited")
			}
		})
	}
}

func TestCredentialUseConcurrentCASAndRegrantIncarnations(t *testing.T) {
	f := newGovernanceFixture(t)
	input := credentialuse.Input{AccountID: cuAccount, RoleID: cuRole, Purpose: credentialuse.Purpose,
		ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: f.initial.GrantRevision, IdempotencyKey: "first-revoke"}
	first := f.begin(t)
	f.exec(t, first, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, cuTenant)
	f.context(t, first, "admin", credentialuse.RevokeUse)
	revoked, err := credentialuse.New().MutateTx(f.ctx, first, f.principal("admin"), "/revoke", input, []string{"domain-one"})
	if err != nil {
		t.Fatal(err)
	}
	secondConn := f.secondConnection(t)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		// Publish only after rollback releases every lock, so the following
		// regrant cannot race the loser's transaction cleanup.
		done <- func() error {
			tx, err := secondConn.Begin(f.ctx)
			if err != nil {
				close(ready)
				return err
			}
			defer tx.Rollback(context.Background())
			close(ready)
			if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, cuTenant); err != nil {
				return err
			}
			if err = credentialuse.SetGovernanceContext(f.ctx, tx, f.principal("admin"), credentialuse.RevokeUse); err != nil {
				return err
			}
			stale := input
			stale.IdempotencyKey = "concurrent-stale-revoke"
			_, err = credentialuse.New().MutateTx(f.ctx, tx, f.principal("admin"), "/revoke", stale, []string{"domain-one"})
			return err
		}()
	}()
	<-ready
	// Observe the second transaction actually waiting on the first, rather
	// than relying on goroutine scheduling to produce concurrent admission.
	lockWait, cancelWait := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancelWait()
	for {
		var waiting bool
		if err = first.QueryRow(lockWait, `SELECT EXISTS(SELECT FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, secondConn.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal("observe concurrent admission lock", err)
		}
		if waiting {
			break
		}
		select {
		case err = <-done:
			t.Fatal("second mutation exited before waiting for the first", err)
		case <-lockWait.Done():
			t.Fatal("second mutation never entered concurrent admission")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = first.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		var problem *credentialuse.Error
		if !errors.As(err, &problem) || problem.Status != 409 || problem.Code != "revision_conflict" {
			t.Fatal("second concurrent mutation did not observe committed CAS", err)
		}
	case <-f.ctx.Done():
		t.Fatal("concurrent mutation failed to finish")
	}
	tx := f.begin(t)
	f.context(t, tx, "admin", credentialuse.GrantUse)
	input.ExpectedGrantRevision = revoked.GrantRevision
	input.IdempotencyKey = "explicit-regrant"
	regranted, err := credentialuse.New().MutateTx(f.ctx, tx, f.principal("admin"), "/grant", input, []string{"domain-one"})
	if err != nil {
		t.Fatal(err)
	}
	oldRev, _ := strconv.ParseInt(f.initial.GrantRevision, 10, 64)
	revokeRev, _ := strconv.ParseInt(revoked.GrantRevision, 10, 64)
	newRev, _ := strconv.ParseInt(regranted.GrantRevision, 10, 64)
	if !(oldRev < revokeRev && revokeRev < newRev) {
		t.Fatal("revocation and regrant reused an old authorization incarnation")
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var receipts, audits int
	if err = f.conn.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.operation_account_use_mutations),(SELECT count(*) FROM adtr.operation_account_use_audit)`).Scan(&receipts, &audits); err != nil || receipts != 3 || audits != 3 {
		t.Fatal("CAS loser persisted a receipt or audit", receipts, audits, err)
	}
}
