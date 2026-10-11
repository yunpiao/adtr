//go:build integration

package store

import (
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/messagecore"
	"github.com/yunpiao/adtr/internal/messages"
	"github.com/yunpiao/adtr/internal/tasks"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVersionSeventeenBulkUpgradePreservesMessageHistory(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 17); err != nil {
		t.Fatal(err)
	}

	// Generate nonempty schema17 message history through the actual health engine,
	// snapshot and single-read persistence, before installing any bulk tables.
	engine, err := tasks.New(f.config, tasks.ProductionRegistry(), auth.NewTaskAuthorizer(), 17)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.ConnectConfig(f.ctx, f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(f.ctx)
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.CheckSchemaTx(f.ctx, tx, 17); err != nil {
		t.Fatal(err)
	}
	_, err = engine.SubmitTx(f.ctx, tx, f.actor, tasks.SubmitInput{TaskName: "infrastructure.health", DomainID: "platform", PayloadVersion: 1, Payload: json.RawMessage(`{}`), IdempotencyKey: "upgrade-health"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if worked, err := engine.RunOne(f.ctx, "upgrade-health-worker"); err != nil || !worked {
		t.Fatal(worked, err)
	}
	inbox, err := messages.New(f.config, 17)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = inbox.ProjectOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
	var epoch int64
	if err = f.conn.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.actor.ActorID).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	id := messages.Identity{TenantID: f.actor.TenantID, UserID: f.actor.ActorID, SessionHash: strings.Repeat("a", 64), AuthorizationVersion: epoch}
	tx, err = conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.CheckSchemaTx(f.ctx, tx, 17); err != nil {
		t.Fatal(err)
	}
	live, err := auth.NewTaskAuthorizer()(f.ctx, tx, f.actor, tasks.Scope{DomainID: "platform", TaskName: "infrastructure.health", Platform: true}, tasks.Read)
	if err != nil || live != strconv.FormatInt(epoch, 10) {
		t.Fatal(live, err)
	}
	now := time.Now().Add(time.Second)
	snap, err := inbox.QueryTx(f.ctx, tx, id, messagecore.Query{ReadFilter: messagecore.ReadAll, Sort: messagecore.SortAscending, Limit: 20}, now)
	if err != nil || snap.Total != 1 {
		t.Fatal(snap, err)
	}
	if _, err = inbox.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "upgrade-original-receipt", now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	delete(before, "schema_version")
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema18 accepted17")
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Migrate(f.ctx, f.config); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	if err := Ready(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	after := f.snapshot()
	for table, want := range before {
		if after[table] != want {
			t.Fatalf("bulk migration modified %s", table)
		}
	}
	var valid bool
	err = f.conn.QueryRow(f.ctx, `SELECT version=18 AND to_regclass('adtr.message_bulk_active_queue') IS NOT NULL AND to_regclass('adtr.message_bulk_active_actor') IS NOT NULL AND NOT EXISTS(SELECT FROM adtr.message_bulk_operations) AND NOT EXISTS(SELECT FROM adtr.message_bulk_targets) AND NOT EXISTS(SELECT FROM adtr.message_bulk_receipts) AND (SELECT count(*)=1 AND min(after_id)=0 AND min(upper_id)=0 FROM adtr.message_bulk_cursor) FROM adtr.schema_version`).Scan(&valid)
	if err != nil || !valid {
		t.Fatal("bulk schema missing or invented history", valid, err)
	}
	if err := migrateTo(f.ctx, f.config, 17); err == nil {
		t.Fatal("downgraded18")
	}
}
