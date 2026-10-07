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
