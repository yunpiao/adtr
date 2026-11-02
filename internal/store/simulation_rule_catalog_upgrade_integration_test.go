//go:build integration

package store

import (
	"reflect"
	"testing"
)

func newSimulationRuleCatalogUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newOperationalUpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 28); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 28
	return f
}

func TestSchema29UpgradePreservesHistoryAndGrants(t *testing.T) {
	f := newSimulationRuleCatalogUpgradeFixture(t)
	before := f.snapshot()
	delete(before, "schema_version")
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema28 accepted by current binary")
	}
	if err := migrateTo(f.ctx, f.config, 29); err != nil {
		t.Fatal(err)
	}
	after := f.snapshot()
	for table, want := range before {
		if after[table] != want {
			t.Fatalf("migration rewrote %s", table)
		}
	}
	var valid bool
	if err := f.conn.QueryRow(f.ctx, `SELECT version=29 AND NOT EXISTS(SELECT FROM adtr.access_permissions WHERE mark IN ('simulation_rule_catalog','simulation_rule_catalog_publish')) AND to_regclass('adtr.simulation_rule_catalog_heads') IS NOT NULL AND to_regclass('adtr.simulation_rule_catalog_receipts') IS NOT NULL FROM adtr.schema_version`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration missing independent schema or invented grant", err)
	}
	if err := Ready(f.ctx, f.config); err == nil {
		t.Fatal("historical schema29 accepted by current binary")
	}
	catalog := f.catalogSnapshot()
	snapshot := f.snapshot()
	if err := migrateTo(f.ctx, f.config, 29); err != nil {
		t.Fatal(err)
	}
	if catalog != f.catalogSnapshot() || !reflect.DeepEqual(snapshot, f.snapshot()) {
		t.Fatal("repeat migration changed data or catalog")
	}
}

func TestSchema29FailureRollsBackPermissionAndTables(t *testing.T) {
	f := newSimulationRuleCatalogUpgradeFixture(t)
	// A deliberate isolated fixture collision makes migration29 fail after its
	// permission DDL. The whole transaction, including that DDL, must roll back.
	f.exec(`CREATE TABLE adtr.simulation_rule_catalog_receipts (sentinel text)`)
	before := f.snapshot()
	catalog := f.catalogSnapshot()
	if err := migrateTo(f.ctx, f.config, 29); err == nil {
		t.Fatal("collision migration unexpectedly succeeded")
	}
	if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
		t.Fatal("failed migration partially modified schema or history")
	}
	var version int
	if err := f.conn.QueryRow(f.ctx, `SELECT version FROM adtr.schema_version`).Scan(&version); err != nil || version != 28 {
		t.Fatal("failed migration changed marker", version, err)
	}
}

// Independent connections contend on the explicit migrator's transaction lock;
// both callers must observe the same complete schema, never partial DDL.
func TestSchema29ConcurrentUpgrade(t *testing.T) {
	f := newSimulationRuleCatalogUpgradeFixture(t)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- migrateTo(f.ctx, f.config, 29) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := Ready(f.ctx, f.config); err == nil {
		t.Fatal("historical schema29 accepted by current binary")
	}
	var grants int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.access_permissions WHERE mark IN ('simulation_rule_catalog','simulation_rule_catalog_publish')").Scan(&grants); err != nil || grants != 0 {
		t.Fatal("concurrent migration invented grants", grants, err)
	}
}
