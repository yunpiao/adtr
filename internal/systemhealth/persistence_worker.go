package systemhealth

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func validWorkerCycle(workerID string, cycle WorkerCycle, status WorkerCycleStatus, code string) bool {
	if !validID(workerID) || (cycle != WorkerQueue && cycle != WorkerRecovery && cycle != WorkerScheduler) {
		return false
	}
	switch status {
	case WorkerCycleSuccess:
		return code == ""
	case WorkerCycleProgress:
		return cycle == WorkerQueue && code == ""
	case WorkerCycleFailure:
		return code == "cycle_failed" || code == "database_unavailable" || code == "execution_failed" || code == "schema_incompatible"
	default:
		return false
	}
}

// RecordWorkerCycle is called only after a real queue/recovery result. Progress
// records a successful executor lease renewal/checkpoint, not business progress.
// A standalone timer must never call this method to manufacture healthy state.
func (s *Store) RecordWorkerCycle(ctx context.Context, workerID string, cycle WorkerCycle, status WorkerCycleStatus, code string) error {
	if cycle == WorkerScheduler && s.expectedSchema < 8 {
		return problem(400, "unsupported_worker_cycle")
	}
	if !validWorkerCycle(workerID, cycle, status, code) {
		return problem(400, "invalid_worker_activity")
	}
	return s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `WITH observed AS (SELECT clock_timestamp() AS at)
INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code,last_activity_at,last_success_at)
SELECT $1,$2,$3,$4,$5,at,CASE WHEN $4='success' THEN at ELSE NULL END FROM observed
ON CONFLICT(tenant_id,worker_id,cycle) DO UPDATE SET status=EXCLUDED.status,error_code=EXCLUDED.error_code,
 last_activity_at=EXCLUDED.last_activity_at,
 last_success_at=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.last_activity_at ELSE adtr.system_worker_activity.last_success_at END`, s.owner, workerID, string(cycle), string(status), code)
		return err
	})
}

func (s *Store) WorkerActivityTx(ctx context.Context, tx pgx.Tx, tenant string) (WorkerActivity, error) {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	out, err := s.workerActivity(ctx, tx, tenant)
	return out, safeError(err)
}
func (s *Store) workerActivity(ctx context.Context, tx pgx.Tx, tenant string) (WorkerActivity, error) {
	out := WorkerActivity{Cycles: []WorkerCycleActivity{}, Availability: Unavailable, Reason: "no_worker_activity", StaleAfterSeconds: int64(WorkerStaleAfter / time.Second)}
	if err := s.scope(tenant, LocalInstanceID); err != nil {
		return out, err
	}
	if err := s.requireTx(ctx, tx); err != nil {
		return out, err
	}
	// Return the latest 32 processes, including both cycles from each process.
	// This bounds response growth without deleting previous observations.
	rows, err := tx.Query(ctx, `SELECT worker_id,cycle,status,error_code,last_activity_at,last_success_at FROM adtr.system_worker_activity
WHERE tenant_id=$1 AND worker_id IN (SELECT worker_id FROM adtr.system_worker_activity WHERE tenant_id=$1 GROUP BY worker_id ORDER BY max(last_activity_at) DESC,worker_id LIMIT 32)
ORDER BY worker_id,cycle`, tenant)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item WorkerCycleActivity
		var at time.Time
		if err = rows.Scan(&item.WorkerID, &item.Cycle, &item.Status, &item.Reason, &at, &item.LastSuccessAt); err != nil {
			return out, err
		}
		item.LastActivityAt = &at
		out.Cycles = append(out.Cycles, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	// Read the clock after the rows: a concurrent real cycle may commit while
	// the SELECT starts, and must not be mistaken for a future observation.
	rows.Close()
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&out.CheckedAt); err != nil {
		return out, err
	}
	evaluateWorkerActivity(&out, s.expectedSchema >= 8)
	return out, nil
}

func evaluateWorkerActivity(out *WorkerActivity, requireScheduler ...bool) {
	needsScheduler := len(requireScheduler) > 0 && requireScheduler[0]
	healthy := map[string]map[WorkerCycle]bool{}
	activeFailure := false
	for i := range out.Cycles {
		item := &out.Cycles[i]
		item.Availability = Unavailable
		if item.Status == WorkerCycleProgress {
			item.Evidence = "executor_lease_or_checkpoint"
		} else {
			item.Evidence = "completed_cycle"
		}
		if item.LastActivityAt == nil {
			item.Reason = "no_worker_activity"
			continue
		}
		age := out.CheckedAt.Sub(*item.LastActivityAt)
		if age < 0 {
			item.Reason = "clock_skew"
			continue
		}
		if age > WorkerStaleAfter {
			item.Reason = "stale_worker_activity"
			continue
		}
		if item.Status == WorkerCycleFailure {
			activeFailure = true
			continue
		}
		if item.Status != WorkerCycleSuccess && item.Status != WorkerCycleProgress {
			item.Reason = "invalid_worker_activity"
			continue
		}
		item.Availability = Available
		item.Reason = ""
		if healthy[item.WorkerID] == nil {
			healthy[item.WorkerID] = map[WorkerCycle]bool{}
		}
		healthy[item.WorkerID][item.Cycle] = true
	}
	out.Availability = Unavailable
	out.Reason = "no_worker_activity"
	if len(out.Cycles) > 0 {
		out.Reason = "stale_or_incomplete_worker_activity"
	}
	for _, cycles := range healthy {
		if cycles[WorkerQueue] && cycles[WorkerRecovery] && (!needsScheduler || cycles[WorkerScheduler]) {
			out.Availability = Available
			out.Reason = ""
			break
		}
	}
	if activeFailure {
		out.Availability = Unavailable
		out.Reason = "worker_cycle_failed"
	}
}
