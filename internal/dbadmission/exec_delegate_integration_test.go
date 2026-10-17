//go:build integration

package dbadmission

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIntegrationExecPreservesNativeContract(t *testing.T) {
	url := os.Getenv("ADTR_TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("isolated PostgreSQL fixture required")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid fixture configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connection failed")
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "CREATE TEMP TABLE exec_delegate_contract (value text, ordinal integer)"); err != nil {
		t.Fatal("fixture creation failed")
	}
	args := []any{"unchanged argument", int32(7)}
	tag, err := conn.Exec(ctx, "INSERT INTO exec_delegate_contract VALUES ($1,$2)", args...)
	if err != nil || tag.String() != "INSERT 0 1" || tag.RowsAffected() != 1 {
		t.Fatal("native command tag lost")
	}
	var value string
	var ordinal int32
	if err := conn.QueryRow(ctx, "SELECT value,ordinal FROM exec_delegate_contract").Scan(&value, &ordinal); err != nil || value != args[0] || ordinal != args[1] {
		t.Fatal("variadic arguments changed")
	}
	_, err = conn.Exec(ctx, "SELECT 1/0")
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "22012" {
		t.Fatal("native PostgreSQL error lost")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = conn.Exec(canceled, "SELECT 1")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation lost")
	}
}
