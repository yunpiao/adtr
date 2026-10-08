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

func TestWorkerCyclesExpireIndependentlyAfterActivityStops(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	observed := []time.Time{now.Add(-2 * time.Second), now.Add(-14 * time.Second), now.Add(-time.Second)}
	wantObserved := append([]time.Time(nil), observed...)
	kinds := []WorkerCycle{WorkerQueue, WorkerRecovery, WorkerScheduler}
	out := WorkerActivity{}
	for i, kind := range kinds {
		out.Cycles = append(out.Cycles, WorkerCycleActivity{
			WorkerID: "stopped-worker", Cycle: kind, Status: WorkerCycleSuccess,
			LastActivityAt: &observed[i], LastSuccessAt: &observed[i],
		})
	}
	for _, tc := range []struct {
		name         string
		elapsed      time.Duration
		availability Availability
		cycles       [3]Availability
	}{
		{"all fresh", 0, Available, [3]Availability{Available, Available, Available}},
		{"oldest at exact stale boundary", time.Second, Available, [3]Availability{Available, Available, Available}},
		{"aggregate expires before remaining cycles", 2 * time.Second, Unavailable, [3]Availability{Available, Unavailable, Available}},
		{"all evidence eventually stale", 15 * time.Second, Unavailable, [3]Availability{Unavailable, Unavailable, Unavailable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out.CheckedAt = now.Add(tc.elapsed)
			evaluateWorkerActivity(&out, true)
			if out.Availability != tc.availability {
				t.Fatalf("aggregate availability=%s, want %s", out.Availability, tc.availability)
			}
			if len(out.Cycles) != len(kinds) {
				t.Fatal("staleness must not discard observed cycle evidence")
			}
			for i, item := range out.Cycles {
				reason := ""
				if tc.cycles[i] == Unavailable {
					reason = "stale_worker_activity"
				}
				if item.Availability != tc.cycles[i] || item.Reason != reason {
					t.Fatalf("%s availability=%s reason=%q, want %s %q", item.Cycle, item.Availability, item.Reason, tc.cycles[i], reason)
				}
				if item.WorkerID != "stopped-worker" || item.Cycle != kinds[i] || item.LastActivityAt == nil || !item.LastActivityAt.Equal(wantObserved[i]) || item.LastSuccessAt == nil || !item.LastSuccessAt.Equal(wantObserved[i]) {
					t.Fatal("evaluating freshness changed the observed cycle identity or timestamps")
				}
			}
		})
	}
}
