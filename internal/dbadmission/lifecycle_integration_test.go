//go:build integration && adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func integrationConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	url := os.Getenv("ADTR_TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("isolated PostgreSQL fixture required")
	}
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid fixture configuration")
	}
	return config
}

func TestIntegrationWholeConnectionIncludesSecondTransaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config := integrationConfig(t)
	// The independent observer is deliberately outside the test coordinator.
	observer, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("observer connection failed")
	}
	defer observer.Close(context.Background())
	g := installTestGate(t)
	conn, err := ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("admitted connection failed")
	}
	defer conn.Close(context.Background())
	var backend int
	if err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&backend); err != nil {
		t.Fatal("backend observation failed")
	}
	g.closeAdmission()
	for i := 0; i < 2; i++ {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal("existing lifetime transaction failed")
		}
		if g.locallyDrained() {
			t.Fatal("open transaction counted as drained")
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal("transaction cleanup failed")
		}
		if g.locallyDrained() {
			t.Fatal("idle open connection counted as drained")
		}
	}
	if other, err := ConnectConfig(ctx, config); err == nil || other != nil {
		t.Fatal("closed admission allowed a connection")
	}
	if err := conn.Close(ctx); err != nil || !g.locallyDrained() {
		t.Fatal("clean connection failed to drain locally")
	}
	if err := conn.Close(ctx); err != nil || !g.locallyDrained() {
		t.Fatal("repeat wrapper Close changed accounting")
	}
	for {
		var present bool
		if err := observer.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1)", backend).Scan(&present); err != nil {
			t.Fatal("independent backend observation failed")
		}
		if !present {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("backend did not disappear within test deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestIntegrationNativeBeginFailureCannotProveDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config := integrationConfig(t)
	g := installTestGate(t)
	conn, err := ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("admitted connection failed")
	}
	defer conn.Close(context.Background())
	g.closeAdmission()
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := conn.Begin(canceled); err == nil {
		t.Fatal("canceled native Begin unexpectedly succeeded")
	}
	_ = conn.Close(ctx)
	if g.locallyDrained() {
		t.Fatal("native error close counted as clean drain")
	}
}
