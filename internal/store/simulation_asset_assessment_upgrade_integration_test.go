//go:build integration

package store

import (
	"reflect"
	"testing"
)

func newSimulationAssetAssessmentUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newOperationalUpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 31); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 31
	return f
}

func TestSchema32UpgradePreservesHistoryAndGrants(t *testing.T) {
	f := newSimulationAssetAssessmentUpgradeFixture(t)
	before := f.snapshot()
	delete(before, "schema_version")
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema31 accepted by current binary")
	}
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	after := f.snapshot()
	for table, want := range before {
		if after[table] != want {
			t.Fatalf("migration rewrote %s", table)
		}
	}
	var valid bool
	if err := f.conn.QueryRow(f.ctx, `SELECT version=32 AND NOT EXISTS(SELECT FROM adtr.access_permissions WHERE mark='simulation_asset_assessment') AND to_regclass('adtr.simulation_asset_observations') IS NOT NULL AND to_regclass('adtr.simulation_asset_reports') IS NOT NULL AND to_regclass('adtr.simulation_asset_receipts') IS NOT NULL FROM adtr.schema_version`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration missing independent schema or invented grant", err)
	}
	if err := Ready(f.ctx, f.config); err != nil {
		t.Fatal("current schema32 rejected", err)
	}
	catalog := f.catalogSnapshot()
	snapshot := f.snapshot()
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	if catalog != f.catalogSnapshot() || !reflect.DeepEqual(snapshot, f.snapshot()) {
		t.Fatal("repeat migration changed data or catalog")
	}
}

func TestSchema32FailureRollsBackPermissionAndTables(t *testing.T) {
	f := newSimulationAssetAssessmentUpgradeFixture(t)
	// A deliberate isolated fixture collision makes migration32 fail after its
	// permission DDL. The whole transaction, including that DDL, must roll back.
	f.exec(`CREATE TABLE adtr.simulation_asset_receipts (sentinel text)`)
	before := f.snapshot()
	catalog := f.catalogSnapshot()
	if err := Migrate(f.ctx, f.config); err == nil {
		t.Fatal("collision migration unexpectedly succeeded")
	}
	if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
		t.Fatal("failed migration partially modified schema or history")
	}
	var version int
	if err := f.conn.QueryRow(f.ctx, `SELECT version FROM adtr.schema_version`).Scan(&version); err != nil || version != 31 {
		t.Fatal("failed migration changed marker", version, err)
	}
}

// Independent connections contend on the explicit migrator's transaction lock;
// both callers must observe the same complete schema, never partial DDL.
func TestSchema32ConcurrentUpgrade(t *testing.T) {
	f := newSimulationAssetAssessmentUpgradeFixture(t)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- Migrate(f.ctx, f.config) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := Ready(f.ctx, f.config); err != nil {
		t.Fatal("current schema32 rejected", err)
	}
	var grants int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.access_permissions WHERE mark='simulation_asset_assessment'").Scan(&grants); err != nil || grants != 0 {
		t.Fatal("concurrent migration invented grants", grants, err)
	}
}
