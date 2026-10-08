//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Requires an isolated disposable DB. This test never drops any schema or table.
func TestMigrationAndReadiness(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: missing integration environment is not a pass")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("test database connection failed")
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_namespace WHERE nspname='adtr')").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("test database must be fresh; refusing to modify an existing adtr schema")
	}
	if Ready(ctx, config) == nil {
		t.Fatal("unmigrated DB must not be ready")
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Migrate(ctx, config); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := Ready(ctx, config); err != nil {
		t.Fatal(err)
	}
	var authTable bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('adtr.users') IS NOT NULL").Scan(&authTable); err != nil || !authTable {
		t.Fatal("identity migration missing", err)
	}
	if _, err := conn.Exec(ctx, "UPDATE adtr.schema_version SET version=$1", SchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if Ready(ctx, config) == nil {
		t.Fatal("future schema must not be ready")
	}
	if Migrate(ctx, config) == nil {
		t.Fatal("future schema must not be overwritten")
	}
	var version int
	if err := conn.QueryRow(ctx, "SELECT version FROM adtr.schema_version").Scan(&version); err != nil || version != SchemaVersion+1 {
		t.Fatal("migration changed incompatible schema")
	}
}

func TestExistingVersionOneUpgrade(t *testing.T) {
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(os.Getenv("ADTR_TEST_DATABASE_URL"))
	if err != nil || os.Getenv("ADTR_TEST_DATABASE_URL") == "" {
		t.Fatal("isolated database required")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("adtr_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `CREATE SCHEMA adtr;
 CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0));
 INSERT INTO adtr.schema_version VALUES(true,1);
 CREATE TABLE adtr.upgrade_fixture(value text NOT NULL);
 INSERT INTO adtr.upgrade_fixture VALUES('preserve-existing-data')`); err != nil {
		t.Fatal(err)
	}
	if Ready(ctx, cfg) == nil {
		t.Fatal("v1 must not be ready for v2 application")
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal("upgrade not idempotent", err)
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var preserved string
	if err = conn.QueryRow(ctx, "SELECT value FROM adtr.upgrade_fixture").Scan(&preserved); err != nil || preserved != "preserve-existing-data" {
		t.Fatal("upgrade lost prior data", err)
	}
	var tables int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='adtr' AND table_name IN ('users','sessions','auth_attempts','auth_audit')").Scan(&tables); err != nil || tables != 4 {
		t.Fatal("upgrade incomplete", tables, err)
	}
}

func TestExistingVersionTwoUpgrade(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("adtr_identity_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version VALUES(true,2);`+auth.Schema); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,email) VALUES('default','prior-user','synthetic-hash','viewer','prior@example.test') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES('synthetic-token',$1,now()+interval '1 hour')", userID); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var username, email, hash, role, roleID string
	if err = conn.QueryRow(ctx, "SELECT username,email,password_hash,role,role_id FROM adtr.users WHERE id=$1", userID).Scan(&username, &email, &hash, &role, &roleID); err != nil {
		t.Fatal(err)
	}
	if username != "prior-user" || email != "prior@example.test" || hash != "synthetic-hash" || role != "viewer" || roleID != "" {
		t.Fatal("identity upgrade lost or changed existing identity")
	}
	var session bool
	if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.sessions WHERE token_hash='synthetic-token' AND user_id=$1)", userID).Scan(&session); err != nil || !session {
		t.Fatal("upgrade lost prior session", err)
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestExistingVersionThreeUpgrade(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("adtr_access_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version VALUES(true,3);`+auth.Schema+auth.SchemaV3); err != nil {
		t.Fatal(err)
	}
	const roleID = "rrrrrrrrrrrrrrrrrrrrrrrr"
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default',$1,'Existing role')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,'users',true,false)", roleID); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id) VALUES('default','prior-member','synthetic-hash','viewer',$1) RETURNING id", roleID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES('prior-v3-session',$1,now()+interval '1 hour')", userID); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var actualRole string
	var readable, writeable bool
	if err = conn.QueryRow(ctx, "SELECT u.role_id,p.readable,p.writeable FROM adtr.users u JOIN adtr.access_permissions p ON p.tenant_id=u.tenant_id AND p.role_id=u.role_id WHERE u.id=$1 AND p.mark='users'", userID).Scan(&actualRole, &readable, &writeable); err != nil {
		t.Fatal(err)
	}
	if actualRole != roleID || !readable || writeable {
		t.Fatal("resource migration changed existing function-role grant")
	}
	var domains, groups int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.resource_domains),(SELECT count(*) FROM adtr.resource_groups)").Scan(&domains, &groups); err != nil || domains != 0 || groups != 0 {
		t.Fatal("migration invented AD inventory or grants", err)
	}
	var tableCount int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='adtr' AND table_name IN ('resource_tenant_config','resource_domains','resource_groups','resource_group_members','resource_role_groups','resource_audit')").Scan(&tableCount); err != nil || tableCount != 6 {
		t.Fatal("resource migration incomplete", tableCount, err)
	}
	var session bool
	if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.sessions WHERE token_hash='prior-v3-session' AND user_id=$1)", userID).Scan(&session); err != nil || !session {
		t.Fatal("resource migration lost previous session", err)
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestExistingVersionFourUpgrade(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("adtr_task_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version VALUES(true,4);`+auth.Schema+auth.SchemaV3+auth.ResourceSchema); err != nil {
		t.Fatal(err)
	}
	const roleID = "rrrrrrrrrrrrrrrrrrrrrrrr"
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default',$1,'Existing role')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,'users',true,false)", roleID); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change) VALUES('default','prior-task-user','synthetic-hash','viewer',$1,false) RETURNING id", roleID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES('prior-v4-session',$1,now()+interval '1 hour')", userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('default','prior-domain','Existing synthetic domain'); INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('default','prior-group','Existing group'); INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('default','prior-group','prior-domain')"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('default',$1,'prior-group')", roleID); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	var session, grant bool
	if err = conn.QueryRow(ctx, "SELECT authorization_version,EXISTS(SELECT FROM adtr.sessions WHERE user_id=$1 AND token_hash='prior-v4-session'),EXISTS(SELECT FROM adtr.resource_role_groups WHERE tenant_id='default' AND role_id=$2 AND group_id='prior-group') FROM adtr.users WHERE id=$1", userID, roleID).Scan(&epoch, &session, &grant); err != nil || epoch <= 0 || !session || !grant {
		t.Fatal("task migration lost prior state or epoch", err)
	}
	var tasksCount, permissionCount int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.tasks),(SELECT count(*) FROM adtr.access_permissions WHERE mark='tasks')").Scan(&tasksCount, &permissionCount); err != nil || tasksCount != 0 || permissionCount != 0 {
		t.Fatal("migration invented tasks or broadened custom permissions", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.NewTaskAuthorizer()(ctx, tx, tasks.Principal{TenantID: "default", ActorID: userID}, tasks.Scope{DomainID: "platform", TaskName: "infrastructure.health", Platform: true}, tasks.Read)
	_ = tx.Rollback(ctx)
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("old custom role gained new task access", err)
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestExistingVersionFiveUpgrade(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("adtr_audit_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version VALUES(true,5);`+auth.Schema+auth.SchemaV3+auth.ResourceSchema+auth.TaskPermissionSchema+auth.TaskAuthorizationSchema+tasks.Schema); err != nil {
		t.Fatal(err)
	}
	const roleID = "rrrrrrrrrrrrrrrrrrrrrrrr"
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default',$1,'Existing role')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,'users',true,false)", roleID); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change) VALUES('default','prior-task-user','synthetic-hash','viewer',$1,false) RETURNING id", roleID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES('prior-v5-session',$1,now()+interval '1 hour')", userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('default','prior-domain','Existing synthetic domain'); INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('default','prior-group','Existing group'); INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('default','prior-group','prior-domain')"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('default',$1,'prior-group')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES('default',$1,'user_update',$1)", userID); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	var session, grant bool
	if err = conn.QueryRow(ctx, "SELECT authorization_version,EXISTS(SELECT FROM adtr.sessions WHERE user_id=$1 AND token_hash='prior-v5-session'),EXISTS(SELECT FROM adtr.resource_role_groups WHERE tenant_id='default' AND role_id=$2 AND group_id='prior-group') FROM adtr.users WHERE id=$1", userID, roleID).Scan(&epoch, &session, &grant); err != nil || epoch <= 0 || !session || !grant {
		t.Fatal("task migration lost prior state or epoch", err)
	}
	var tasksCount, permissionCount int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.tasks),(SELECT count(*) FROM adtr.access_permissions WHERE mark='tasks')").Scan(&tasksCount, &permissionCount); err != nil || tasksCount != 0 || permissionCount != 0 {
		t.Fatal("migration invented tasks or broadened custom permissions", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.NewTaskAuthorizer()(ctx, tx, tasks.Principal{TenantID: "default", ActorID: userID}, tasks.Scope{DomainID: "platform", TaskName: "infrastructure.health", Platform: true}, tasks.Read)
	_ = tx.Rollback(ctx)
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("old custom role gained new task access", err)
	}
	var historical, broadened int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.auth_audit WHERE action='user_update' AND audit_username IS NULL AND audit_ip IS NULL AND audit_path IS NULL),(SELECT count(*) FROM adtr.access_permissions WHERE mark IN ('audit','audit_exports'))").Scan(&historical, &broadened); err != nil || historical != 1 || broadened != 0 {
		t.Fatal("audit migration invented metadata or rights", err)
	}
	if _, err = conn.Exec(ctx, "UPDATE adtr.auth_audit SET audit_username='forged'"); err == nil {
		t.Fatal("migration lost immutable history guard")
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestExistingVersionSixUpgrade(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("adtr_health_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version VALUES(true,6);`+auth.Schema+auth.SchemaV3+auth.ResourceSchema+auth.TaskPermissionSchema+auth.TaskAuthorizationSchema+tasks.Schema+auth.AuditPermissionSchema+audit.Schema); err != nil {
		t.Fatal(err)
	}
	const roleID = "rrrrrrrrrrrrrrrrrrrrrrrr"
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default',$1,'Existing role')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,'users',true,false)", roleID); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change) VALUES('default','prior-task-user','synthetic-hash','viewer',$1,false) RETURNING id", roleID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES('prior-v6-session',$1,now()+interval '1 hour')", userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('default','prior-domain','Existing synthetic domain'); INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('default','prior-group','Existing group'); INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('default','prior-group','prior-domain')"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('default',$1,'prior-group')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES('default',$1,'user_update',$1)", userID); err != nil {
		t.Fatal(err)
	}
	if err = Ready(ctx, cfg); err == nil {
		t.Fatal("prior schema accepted before explicit health migration")
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	var session, grant bool
	if err = conn.QueryRow(ctx, "SELECT authorization_version,EXISTS(SELECT FROM adtr.sessions WHERE user_id=$1 AND token_hash='prior-v6-session'),EXISTS(SELECT FROM adtr.resource_role_groups WHERE tenant_id='default' AND role_id=$2 AND group_id='prior-group') FROM adtr.users WHERE id=$1", userID, roleID).Scan(&epoch, &session, &grant); err != nil || epoch <= 0 || !session || !grant {
		t.Fatal("task migration lost prior state or epoch", err)
	}
	var tasksCount, permissionCount int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.tasks),(SELECT count(*) FROM adtr.access_permissions WHERE mark='tasks')").Scan(&tasksCount, &permissionCount); err != nil || tasksCount != 0 || permissionCount != 0 {
		t.Fatal("migration invented tasks or broadened custom permissions", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.NewTaskAuthorizer()(ctx, tx, tasks.Principal{TenantID: "default", ActorID: userID}, tasks.Scope{DomainID: "platform", TaskName: "infrastructure.health", Platform: true}, tasks.Read)
	_ = tx.Rollback(ctx)
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("old custom role gained new task access", err)
	}
	var historical, broadened int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.auth_audit WHERE action='user_update' AND audit_username IS NULL AND audit_ip IS NULL AND audit_path IS NULL),(SELECT count(*) FROM adtr.access_permissions WHERE mark IN ('audit','audit_exports','system'))").Scan(&historical, &broadened); err != nil || historical != 1 || broadened != 0 {
		t.Fatal("audit migration invented metadata or rights", err)
	}
	if _, err = conn.Exec(ctx, "UPDATE adtr.auth_audit SET audit_username='forged'"); err == nil {
		t.Fatal("migration lost immutable history guard")
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestExistingVersionSevenUpgrade(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("adtr_maintenance_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `CREATE SCHEMA adtr; CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version VALUES(true,7);`+auth.Schema+auth.SchemaV3+auth.ResourceSchema+auth.TaskPermissionSchema+auth.TaskAuthorizationSchema+strings.Replace(tasks.Schema, tasks.ArchiveCoreSchema, "", 1)+auth.AuditPermissionSchema+strings.Replace(audit.Schema, audit.ViewSchema, auditVersionSevenView, 1)+auth.SystemPermissionMarks+systemhealth.Schema); err != nil {
		t.Fatal(err)
	}
	var protectedFunctionExists bool
	if err = conn.QueryRow(ctx, "SELECT to_regprocedure('adtr.audit_event_deletable(text,text)') IS NOT NULL").Scan(&protectedFunctionExists); err != nil || protectedFunctionExists {
		t.Fatal("v7 fixture already contains v8 audit protection", err)
	}
	const roleID = "rrrrrrrrrrrrrrrrrrrrrrrr"
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default',$1,'Existing role')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,'users',true,false)", roleID); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err = conn.QueryRow(ctx, "INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change) VALUES('default','prior-task-user','synthetic-hash','viewer',$1,false) RETURNING id", roleID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES('prior-v7-session',$1,now()+interval '1 hour')", userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('default','prior-domain','Existing synthetic domain'); INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('default','prior-group','Existing group'); INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('default','prior-group','prior-domain')"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('default',$1,'prior-group')", roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES('default',$1,'user_update',$1)", userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts,created_at,updated_at) SELECT 'legacy-health','default','platform','infrastructure.health',1,'{}','synthetic',$1,authorization_version::text,'legacy-key','succeeded',5,'2020-01-01T00:00:00Z','2020-01-02T03:04:05Z' FROM adtr.users WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version,occurred_at) SELECT task_id,tenant_id,domain_id,actor_id,'finished',state,1,1,1,authorization_version,'2020-01-02T03:04:05Z' FROM adtr.tasks WHERE task_id='legacy-health'`); err != nil {
		t.Fatal(err)
	}
	if err = Ready(ctx, cfg); err == nil {
		t.Fatal("prior schema accepted before explicit health migration")
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	var session, grant bool
	if err = conn.QueryRow(ctx, "SELECT authorization_version,EXISTS(SELECT FROM adtr.sessions WHERE user_id=$1 AND token_hash='prior-v7-session'),EXISTS(SELECT FROM adtr.resource_role_groups WHERE tenant_id='default' AND role_id=$2 AND group_id='prior-group') FROM adtr.users WHERE id=$1", userID, roleID).Scan(&epoch, &session, &grant); err != nil || epoch <= 0 || !session || !grant {
		t.Fatal("task migration lost prior state or epoch", err)
	}
	var tasksCount, permissionCount int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.tasks),(SELECT count(*) FROM adtr.access_permissions WHERE mark='tasks')").Scan(&tasksCount, &permissionCount); err != nil || tasksCount != 1 || permissionCount != 0 {
		t.Fatal("migration invented tasks or broadened custom permissions", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.NewTaskAuthorizer()(ctx, tx, tasks.Principal{TenantID: "default", ActorID: userID}, tasks.Scope{DomainID: "platform", TaskName: "infrastructure.health", Platform: true}, tasks.Read)
	_ = tx.Rollback(ctx)
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("old custom role gained new task access", err)
	}
	var historical, broadened int
	if err = conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM adtr.auth_audit WHERE action='user_update' AND audit_username IS NULL AND audit_ip IS NULL AND audit_path IS NULL),(SELECT count(*) FROM adtr.access_permissions WHERE mark IN ('audit','audit_exports','system','schedules','task_archive'))").Scan(&historical, &broadened); err != nil || historical != 1 || broadened != 0 {
		t.Fatal("audit migration invented metadata or rights", err)
	}
	if _, err = conn.Exec(ctx, "UPDATE adtr.auth_audit SET audit_username='forged'"); err == nil {
		t.Fatal("migration lost immutable history guard")
	}
	var terminal time.Time
	var retained bool
	if err = conn.QueryRow(ctx, "SELECT terminal_at,NOT EXISTS(SELECT FROM adtr.task_visibility WHERE task_id='legacy-health') FROM adtr.tasks WHERE task_id='legacy-health'").Scan(&terminal, &retained); err != nil || !retained || !terminal.Equal(time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatal("legacy terminal evidence/visibility lost", err)
	}
	var deletable bool
	if err = conn.QueryRow(ctx, "SELECT adtr.audit_event_deletable('auth','task.archive')").Scan(&deletable); err != nil || deletable {
		t.Fatal("maintenance control audit not protected", err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status) VALUES('default','migration-proof','scheduler','success')"); err != nil {
		t.Fatal("scheduler observation migration missing", err)
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}
