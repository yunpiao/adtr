//go:build integration

package store

import (
	"reflect"
	"testing"
)

func newSimulationScheduleUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newOperationalUpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 26); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 26
	return f
}

func TestSchema27UpgradePreservesHistoryAndGrants(t *testing.T) {
	f := newSimulationScheduleUpgradeFixture(t)
	before := f.snapshot()
	delete(before, "schema_version")
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema26 accepted by current binary")
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
	if err := f.conn.QueryRow(f.ctx, `SELECT version=27 AND NOT EXISTS(SELECT FROM adtr.access_permissions WHERE mark='simulation_detection_schedules') AND to_regclass('adtr.simulation_detection_schedules') IS NOT NULL AND to_regclass('adtr.simulation_detection_schedule_intents') IS NOT NULL FROM adtr.schema_version`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration missing independent schema or invented grant", err)
	}
	if err := Ready(f.ctx, f.config); err != nil {
		t.Fatal(err)
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

func TestSchema27FailureRollsBackPermissionAndTables(t *testing.T) {
	f := newSimulationScheduleUpgradeFixture(t)
	// A deliberate isolated fixture collision makes migration27 fail after its
	// permission DDL. The whole transaction, including that DDL, must roll back.
	f.exec(`CREATE TABLE adtr.simulation_detection_schedule_intents (sentinel text)`)
	before := f.snapshot()
	catalog := f.catalogSnapshot()
	if err := Migrate(f.ctx, f.config); err == nil {
		t.Fatal("collision migration unexpectedly succeeded")
	}
	if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
		t.Fatal("failed migration partially modified schema or history")
	}
	var version int
	if err := f.conn.QueryRow(f.ctx, `SELECT version FROM adtr.schema_version`).Scan(&version); err != nil || version != 26 {
		t.Fatal("failed migration changed marker", version, err)
	}
}
