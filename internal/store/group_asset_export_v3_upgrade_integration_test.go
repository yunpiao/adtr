//go:build integration

package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yunpiao/adtr/internal/domains"
)

func newGroupAssetExportUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newUserAssetExportUpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 21); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 21
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema21 accepted by schema22 binary")
	}
	return f
}
func TestSchema22PreservesHistoryAndRegistersNoAutomaticGrant(t *testing.T) {
	f := newGroupAssetExportUpgradeFixture(t)
	grant := f.grantDirectoryV3("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV3(engine, "first", grant)
	run, done := f.start(engine, opened, task, "schema22-old-worker")
	stageNonemptyDirectoryV3HistoryForV4Test(t, f, task)
	f.finish(run, done, false)
	// Seed nonempty schema21 user artifacts. Migration22 must preserve every
	// receipt and byte while registering only the independent group producer.
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'historical-user-export',$1,'first','user_assets.v3.export',1,'{}',repeat('d',64),id,authorization_version::text,'historical-user-export','queued',1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID)
	f.exec(`INSERT INTO adtr.user_asset_export_snapshots(task_id,tenant_id,actor_id,authorization_version,observation_id,source_sha256,payload_hash,row_count) SELECT task_id,tenant_id,actor_id,authorization_version,$2,repeat('a',64),payload_hash,0 FROM adtr.tasks WHERE task_id=$1`, "historical-user-export", task.ID)
	f.exec(`INSERT INTO adtr.user_asset_export_chunks(task_id,fencing_token,chunk_no,data,sha256) VALUES($1,1,0,decode('50','hex'),repeat('b',64))`, "historical-user-export")
	f.exec(`INSERT INTO adtr.user_asset_export_manifests(task_id,fencing_token,byte_count,chunk_count,row_count,sha256) VALUES($1,1,1,1,0,repeat('b',64))`, "historical-user-export")
	before, projection := f.snapshot(), f.auditProjection()
	delete(before, "schema_version")
	if err := migrateTo(f.ctx, f.config, 22); err != nil {
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
	if err := migrateTo(f.ctx, f.config, 22); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, f.snapshot()) || catalog != f.catalogSnapshot() {
		t.Fatal("repeat migration changed history/catalog")
	}
	var valid bool
	if err := f.conn.QueryRow(f.ctx, `SELECT version=22 AND NOT EXISTS(SELECT FROM adtr.access_permissions WHERE mark='group_asset_exports') AND NOT EXISTS(SELECT FROM adtr.tasks WHERE kind='group_assets.v3.export') AND to_regclass('adtr.group_asset_export_snapshots') IS NOT NULL AND to_regclass('adtr.group_asset_export_chunks') IS NOT NULL AND to_regclass('adtr.group_asset_export_manifests') IS NOT NULL FROM adtr.schema_version`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration invented grant/data or missed artifacts", err)
	}
	if err := Ready(f.ctx, f.config); err == nil {
		t.Fatal("historical schema22 accepted by schema23 runtime")
	}
	f.assertOldEngineRejected(engine, task)
}
func TestSchema22RefusesReservedTaskAndForeignObjectsAtomically(t *testing.T) {
	for _, mode := range []string{"task", "table", "function"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupAssetExportUpgradeFixture(t)
			switch mode {
			case "task":
				f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'legacy-export-task',$1,'first','group_assets.v3.export',1,'{"historical":true}',repeat('d',64),id,authorization_version::text,'legacy-export-key','queued',1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID)
			case "table":
				f.exec(`CREATE TABLE adtr.group_asset_export_chunks(foreign_data text)`)
			case "function":
				f.exec(`CREATE FUNCTION adtr.group_asset_export_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$;`)
			}
			before, catalog := f.snapshot(), f.catalogSnapshot()
			for range 2 {
				if err := migrateTo(f.ctx, f.config, 22); !errors.Is(err, ErrGroupAssetExportV3ReservedCollision) {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
					t.Fatal("blocked migration changed history/catalog")
				}
			}
		})
	}
}
func TestSchema22RequiresOriginalDirectoryExecutorQuiescence(t *testing.T) {
	f := newGroupAssetExportUpgradeFixture(t)
	grant := f.grantDirectoryV3("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV3(engine, "first", grant)
	run, done := f.start(engine, opened, task, "schema22-open-worker")
	before := f.snapshot()
	if err := migrateTo(f.ctx, f.config, 22); !errors.Is(err, ErrCredentialUseMigrationBlocked) {
		t.Fatal("opened source use did not block upgrade", err)
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("blocked migration changed rows")
	}
	f.finish(run, done, false)
	if err := migrateTo(f.ctx, f.config, 22); err != nil {
		t.Fatal(err)
	}
}
