package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/yunpiao/adtr/internal/tasks"
)

// Reports only observations delivered by actual worker operations. This has no
// timer and cannot manufacture liveness while a queue or executor is stuck.
func newWorkerActivityRecorder(record func(context.Context, tasks.WorkerActivity) error) func(context.Context, tasks.WorkerActivity) {
	type lastObservation struct {
		at     time.Time
		status string
	}
	var mu sync.Mutex
	last := map[string]lastObservation{}
	return func(ctx context.Context, a tasks.WorkerActivity) {
		now := time.Now()
		key := a.WorkerID + "/" + a.Cycle
		mu.Lock()
		old := last[key]
		if a.Status == old.status && now.Sub(old.at) < 5*time.Second {
			mu.Unlock()
			return
		}
		last[key] = lastObservation{now, a.Status}
		mu.Unlock()
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := record(bounded, a); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "system worker observation failed")
		}
	}
}
