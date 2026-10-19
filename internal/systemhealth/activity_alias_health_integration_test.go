//go:build integration && adtr_ci_dbadmission

package systemhealth

import (
	"github.com/jackc/pgx/v5"
	"testing"
)

// The CI sentinel is deliberately unhealthy, never fabricated production health.
func TestActivityCandidateSentinelHealthSideEffectAndExactCleanup(t *testing.T) {
	f := newHealthFixture(t)
	for _, cycle := range []WorkerCycle{WorkerQueue, WorkerRecovery} {
		if f.store.RecordWorkerCycle(f.ctx, "real-worker", cycle, WorkerCycleSuccess, "") != nil {
			t.Fatal("ordinary activity")
		}
	}
	read := func() WorkerActivity {
		t.Helper()
		var result WorkerActivity
		if f.transact(func(tx pgx.Tx) error {
			var err error
			result, err = f.store.WorkerActivityTx(f.ctx, tx, OwnerTenantID)
			return err
		}) != nil {
			t.Fatal("health read")
		}
		return result
	}
	if read().Availability != Available {
		t.Fatal("ordinary worker not healthy")
	}
	const alias = "ci-activity-xjhrbwphlzfwschmfnm4kqsunp2qeowpnarubert6q2qx5ospmha"
	f.exec("INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code,last_success_at) VALUES('default',$1,'queue','failure','cycle_failed',NULL)", alias)
	if got := read(); got.Availability != Unavailable || got.Reason != "worker_cycle_failed" || len(got.Cycles) != 3 {
		t.Fatal("sentinel health effect missing")
	}
	// An insert collision cannot overwrite the original row.
	if _, err := f.db.Exec(f.ctx, "INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code) VALUES('default',$1,'queue','success','')", alias); err == nil {
		t.Fatal("collision accepted")
	}
	if got := read(); got.Availability != Unavailable || got.Reason != "worker_cycle_failed" {
		t.Fatal("collision changed health")
	}
	tag, err := f.db.Exec(f.ctx, "DELETE FROM adtr.system_worker_activity WHERE tenant_id='default' AND worker_id=$1 AND cycle='queue'", alias)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("sentinel cleanup")
	}
	got := read()
	if got.Availability != Available || len(got.Cycles) != 2 {
		t.Fatal("original health did not recover")
	}
	for _, cycle := range got.Cycles {
		if cycle.WorkerID != "real-worker" {
			t.Fatal("nonowned row survived")
		}
	}
}
