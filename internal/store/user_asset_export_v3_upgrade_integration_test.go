//go:build integration

package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yunpiao/adtr/internal/domains"
)

func newUserAssetExportUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newDirectoryV4UpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 20); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 20
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema20 accepted by schema21 binary")
	}
	return f
}
func TestSchema21PreservesHistoryAndRegistersNoAutomaticGrant(t *testing.T) {
	f := newUserAssetExportUpgradeFixture(t)
	grant := f.grantDirectoryV3("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV3(engine, "first", grant)
	run, done := f.start(engine, opened, task, "schema21-old-worker")
	stageNonemptyDirectoryV3HistoryForV4Test(t, f, task)
	f.finish(run, done, false)
	before, projection := f.snapshot(), f.auditProjection()
	delete(before, "schema_version")
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	after := f.snapshot()
	for table, want := range before {
		if after[table] != want {
			t.Fatalf("migration rewrote %s", table)
		}
	}
	if projection != f.auditProjection() {
		t.Fatal("audit history reinterpreted")
	}
	catalog := f.catalogSnapshot()
	snapshot := f.snapshot()
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, f.snapshot()) || catalog != f.catalogSnapshot() {
		t.Fatal("repeat migration changed history/catalog")
	}
	var valid bool
	if err := f.conn.QueryRow(f.ctx, `SELECT version=21 AND NOT EXISTS(SELECT FROM adtr.access_permissions WHERE mark='user_asset_exports') AND NOT EXISTS(SELECT FROM adtr.tasks WHERE kind='user_assets.v3.export') AND to_regclass('adtr.user_asset_export_snapshots') IS NOT NULL AND to_regclass('adtr.user_asset_export_chunks') IS NOT NULL AND to_regclass('adtr.user_asset_export_manifests') IS NOT NULL FROM adtr.schema_version`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration invented grant/data or missed artifacts", err)
	}
	if err := Ready(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.assertOldEngineRejected(engine, task)
}
func TestSchema21RefusesReservedTaskAndForeignObjectsAtomically(t *testing.T) {
	for _, mode := range []string{"task", "table", "function"} {
		t.Run(mode, func(t *testing.T) {
			f := newUserAssetExportUpgradeFixture(t)
			switch mode {
			case "task":
				f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'legacy-export-task',$1,'first','user_assets.v3.export',1,'{"historical":true}',repeat('d',64),id,authorization_version::text,'legacy-export-key','queued',1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID)
			case "table":
				f.exec(`CREATE TABLE adtr.user_asset_export_chunks(foreign_data text)`)
			case "function":
				f.exec(`CREATE FUNCTION adtr.user_asset_export_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$;`)
			}
			before, catalog := f.snapshot(), f.catalogSnapshot()
			for range 2 {
				if err := Migrate(f.ctx, f.config); !errors.Is(err, ErrUserAssetExportV3ReservedCollision) {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
					t.Fatal("blocked migration changed history/catalog")
				}
			}
		})
	}
}
func TestSchema21RequiresOriginalDirectoryExecutorQuiescence(t *testing.T) {
	f := newUserAssetExportUpgradeFixture(t)
	grant := f.grantDirectoryV3("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV3(engine, "first", grant)
	run, done := f.start(engine, opened, task, "schema21-open-worker")
	before := f.snapshot()
	if err := Migrate(f.ctx, f.config); !errors.Is(err, ErrCredentialUseMigrationBlocked) {
		t.Fatal("opened source use did not block upgrade", err)
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("blocked migration changed rows")
	}
	f.finish(run, done, false)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
}
