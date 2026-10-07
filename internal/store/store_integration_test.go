//go:build integration

package store

import (
	"context"
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
	if Ready(ctx, dsn) == nil {
		t.Fatal("unmigrated DB must not be ready")
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Migrate(ctx, dsn); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := Ready(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "UPDATE adtr.schema_version SET version=2"); err != nil {
		t.Fatal(err)
	}
	if Ready(ctx, dsn) == nil {
		t.Fatal("future schema must not be ready")
	}
	if Migrate(ctx, dsn) == nil {
		t.Fatal("future schema must not be overwritten")
	}
	var version int
	if err := conn.QueryRow(ctx, "SELECT version FROM adtr.schema_version").Scan(&version); err != nil || version != 2 {
		t.Fatal("migration changed incompatible schema")
	}
}
