package systemhealth

import (
	"testing"
	"time"
)

func TestSchemaEightWorkerRequiresActualSchedulerCycle(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	cycle := func(kind WorkerCycle) WorkerCycleActivity {
		return WorkerCycleActivity{WorkerID: "worker-one", Cycle: kind, Status: WorkerCycleSuccess, LastActivityAt: &now}
	}
	out := WorkerActivity{CheckedAt: now, Cycles: []WorkerCycleActivity{cycle(WorkerQueue), cycle(WorkerRecovery)}}
	evaluateWorkerActivity(&out, true)
	if out.Availability == Available {
		t.Fatal("worker became healthy without scheduler evidence")
	}
	out.Cycles = append(out.Cycles, cycle(WorkerScheduler))
	evaluateWorkerActivity(&out, true)
	if out.Availability != Available {
		t.Fatal("complete current worker evidence rejected")
	}
	out.Cycles[2].Status = WorkerCycleFailure
	out.Cycles[2].Reason = "cycle_failed"
	evaluateWorkerActivity(&out, true)
	if out.Availability == Available || out.Reason != "worker_cycle_failed" {
		t.Fatal("scheduler failure hidden")
	}
	old := now.Add(-16 * time.Second)
	out.Cycles[2].Status = WorkerCycleSuccess
	out.Cycles[2].LastActivityAt = &old
	evaluateWorkerActivity(&out, true)
	if out.Availability == Available {
		t.Fatal("stale scheduler evidence accepted")
	}
	if !validWorkerCycle("worker-one", WorkerScheduler, WorkerCycleSuccess, "") || !validWorkerCycle("worker-one", WorkerScheduler, WorkerCycleFailure, "cycle_failed") || validWorkerCycle("worker-one", WorkerScheduler, WorkerCycleProgress, "") {
		t.Fatal("scheduler activity contract invalid")
	}
}
