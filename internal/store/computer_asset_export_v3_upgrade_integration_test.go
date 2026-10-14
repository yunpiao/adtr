//go:build integration

package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yunpiao/adtr/internal/domains"
)

func newComputerAssetExportUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newGroupAssetExportUpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 22); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 22
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("schema22 accepted by schema23 binary")
	}
	return f
}
func TestSchema23PreservesHistoryAndRegistersNoAutomaticGrant(t *testing.T) {
	f := newComputerAssetExportUpgradeFixture(t)
	grant := f.grantDirectoryV3("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV3(engine, "first", grant)
	run, done := f.start(engine, opened, task, "schema23-old-worker")
	stageNonemptyDirectoryV3HistoryForV4Test(t, f, task)
	f.finish(run, done, false)
	// Seed nonempty schema22 user artifacts. Migration23 must preserve every
	// receipt and byte while registering only the independent computer producer.
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'historical-user-export',$1,'first','user_assets.v3.export',1,'{}',repeat('d',64),id,authorization_version::text,'historical-user-export','queued',1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID)
	f.exec(`INSERT INTO adtr.user_asset_export_snapshots(task_id,tenant_id,actor_id,authorization_version,observation_id,source_sha256,payload_hash,row_count) SELECT task_id,tenant_id,actor_id,authorization_version,$2,repeat('a',64),payload_hash,0 FROM adtr.tasks WHERE task_id=$1`, "historical-user-export", task.ID)
	f.exec(`INSERT INTO adtr.user_asset_export_chunks(task_id,fencing_token,chunk_no,data,sha256) VALUES($1,1,0,decode('50','hex'),repeat('b',64))`, "historical-user-export")
	f.exec(`INSERT INTO adtr.user_asset_export_manifests(task_id,fencing_token,byte_count,chunk_count,row_count,sha256) VALUES($1,1,1,1,0,repeat('b',64))`, "historical-user-export")
	// Preserve a separate nonempty group artifact family as well.
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'historical-group-export',$1,'first','group_assets.v3.export',1,'{}',repeat('d',64),id,authorization_version::text,'historical-group-export','queued',1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID)
	f.exec(`INSERT INTO adtr.group_asset_export_snapshots(task_id,tenant_id,actor_id,authorization_version,observation_id,source_sha256,payload_hash,row_count) SELECT task_id,tenant_id,actor_id,authorization_version,$2,repeat('a',64),payload_hash,0 FROM adtr.tasks WHERE task_id=$1`, "historical-group-export", task.ID)
	f.exec(`INSERT INTO adtr.group_asset_export_chunks(task_id,fencing_token,chunk_no,data,sha256) VALUES($1,1,0,decode('50','hex'),repeat('b',64))`, "historical-group-export")
	f.exec(`INSERT INTO adtr.group_asset_export_manifests(task_id,fencing_token,byte_count,chunk_count,row_count,sha256) VALUES($1,1,1,1,0,repeat('b',64))`, "historical-group-export")
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
	if err := f.conn.QueryRow(f.ctx, `SELECT version=23 AND NOT EXISTS(SELECT FROM adtr.access_permissions WHERE mark='computer_asset_exports') AND NOT EXISTS(SELECT FROM adtr.tasks WHERE kind='computer_assets.v3.export') AND to_regclass('adtr.computer_asset_export_snapshots') IS NOT NULL AND to_regclass('adtr.computer_asset_export_chunks') IS NOT NULL AND to_regclass('adtr.computer_asset_export_manifests') IS NOT NULL FROM adtr.schema_version`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration invented grant/data or missed artifacts", err)
	}
	if err := Ready(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.assertOldEngineRejected(engine, task)
}
func TestSchema23RefusesReservedTaskAndForeignObjectsAtomically(t *testing.T) {
	for _, mode := range []string{"task", "table", "function"} {
		t.Run(mode, func(t *testing.T) {
			f := newComputerAssetExportUpgradeFixture(t)
			switch mode {
			case "task":
				f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'legacy-export-task',$1,'first','computer_assets.v3.export',1,'{"historical":true}',repeat('d',64),id,authorization_version::text,'legacy-export-key','queued',1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID)
			case "table":
				f.exec(`CREATE TABLE adtr.computer_asset_export_chunks(foreign_data text)`)
			case "function":
				f.exec(`CREATE FUNCTION adtr.computer_asset_export_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$;`)
			}
			before, catalog := f.snapshot(), f.catalogSnapshot()
			for range 2 {
				if err := Migrate(f.ctx, f.config); !errors.Is(err, ErrComputerAssetExportV3ReservedCollision) {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
					t.Fatal("blocked migration changed history/catalog")
				}
			}
		})
	}
}
func TestSchema23RequiresOriginalDirectoryExecutorQuiescence(t *testing.T) {
	f := newComputerAssetExportUpgradeFixture(t)
	grant := f.grantDirectoryV3("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV3(engine, "first", grant)
	run, done := f.start(engine, opened, task, "schema23-open-worker")
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
