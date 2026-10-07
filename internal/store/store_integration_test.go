//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
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
