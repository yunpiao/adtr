//go:build integration

package store

import (
	"sync"
	"testing"
)

func TestVersionSixteenMessagesUpgradePreservesHistory(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 16); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	delete(before, "schema_version")
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema17 runtime accepted historical16")
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
			t.Fatalf("message migration modified existing %s", table)
		}
	}
	var valid bool
	if err := f.conn.QueryRow(f.ctx, `SELECT version=$1
 AND to_regclass('adtr.messages_recipient_time') IS NOT NULL
 AND to_regclass('adtr.message_results_expiry') IS NOT NULL
 AND NOT EXISTS(SELECT FROM adtr.messages)
 AND NOT EXISTS(SELECT FROM adtr.message_reads)
 AND NOT EXISTS(SELECT FROM adtr.message_results)
 AND NOT EXISTS(SELECT FROM adtr.message_read_operations)
 AND (SELECT count(*)=1 AND min(after_id)=0 FROM adtr.message_projection_cursor)
 FROM adtr.schema_version`, SchemaVersion).Scan(&valid); err != nil || !valid {
		t.Fatal("message schema missing or migration invented user messages", err)
	}
	if err := migrateTo(f.ctx, f.config, 16); err == nil {
		t.Fatal("historical target downgraded message schema")
	}
}
