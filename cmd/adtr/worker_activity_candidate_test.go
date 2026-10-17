package main

import (
	"context"
	"errors"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
)

func TestActivityCandidateRecorderSharesCoalescing(t *testing.T) {
	for _, recordError := range []bool{false, true} {
		calls := 0
		recorder := newWorkerActivityCandidateRecorder(func(_ context.Context, a tasks.WorkerActivity, candidate tasks.ActivityCandidate) error {
			calls++
			if candidate != nil || a.WorkerID != "synthetic-worker" {
				t.Fatal("ordinary event acquired candidate identity")
			}
			if recordError {
				return errors.New("synthetic observation failure")
			}
			return nil
		})
		ordinary := func(a tasks.WorkerActivity) { recorder(context.Background(), a, nil) }
		progress := tasks.WorkerActivity{WorkerID: "synthetic-worker", Cycle: "queue", Status: "progress"}
		ordinary(progress)
		recorder(context.Background(), progress, nil)
		ordinary(progress)
		if calls != 1 {
			t.Fatal("ordinary and candidate paths did not share coalescing")
		}
		progress.Status = "success"
		ordinary(progress)
		recorder(context.Background(), progress, nil)
		if calls != 2 {
			t.Fatal("status transition or coalescing changed")
		}
	}
}

func TestActivityCandidateLegacyRecorderRemainsCompatible(t *testing.T) {
	calls := 0
	recorder := newWorkerActivityRecorder(func(context.Context, tasks.WorkerActivity) error { calls++; return nil })
	a := tasks.WorkerActivity{WorkerID: "synthetic-worker", Cycle: "queue", Status: "progress"}
	recorder(context.Background(), a)
	recorder(context.Background(), a)
	if calls != 1 {
		t.Fatal("legacy coalescing changed")
	}
}
