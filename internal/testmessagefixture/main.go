//go:build integration

// testmessagefixture submits synthetic health work through the real engine.
// This internal submission deliberately does not claim HTTP/MFA coverage.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/messages"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

func run() error {
	if os.Getenv("ADTR_E2E_MESSAGE_FIXTURE") != "owned-isolated-health" || len(os.Args) != 2 {
		return errors.New("fixture configuration")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("ADTR_DATABASE_URL"))
	if err != nil {
		return errors.New("fixture configuration")
	}
	if cfg.Database != "adtr_e2e" || cfg.Host != "127.0.0.1" {
		return errors.New("fixture database boundary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if os.Args[1] == "bulk-once" {
		inbox, err := messages.New(cfg, store.SchemaVersion)
		if err != nil {
			return err
		}
		return inbox.BulkOnce(ctx, auth.NewTaskAuthorizer())
	}
	if os.Args[1] != "seed" && os.Args[1] != "seed-late" {
		return errors.New("fixture command")
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var actor int64
	if err = conn.QueryRow(ctx, `SELECT id FROM adtr.users WHERE tenant_id='default' AND username=$1`, os.Getenv("ADTR_E2E_USERNAME")).Scan(&actor); err != nil {
		return err
	}
	engine, err := tasks.New(cfg, tasks.ProductionRegistry(), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		return err
	}
	ids := []string{}
	start, end := 0, 61
	if os.Args[1] == "seed-late" {
		start, end = 61, 62
	}
	for i := start; i < end; i++ {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		submission, err := engine.SubmitTx(ctx, tx, tasks.Principal{TenantID: "default", ActorID: actor}, tasks.SubmitInput{TaskName: "infrastructure.health", DomainID: "platform", PayloadVersion: 1, Payload: json.RawMessage(`{}`), IdempotencyKey: fmt.Sprintf("browser-bulk-health-%02d", i)})
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		ids = append(ids, submission.Task.ID)
	}
	return json.NewEncoder(os.Stdout).Encode(ids)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "isolated message fixture failed")
		os.Exit(1)
	}
}
