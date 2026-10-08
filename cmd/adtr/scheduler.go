package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/tasks"
)

func runScheduleLoop(ctx context.Context, engine *schedules.Engine, owner string, activity func(context.Context, tasks.WorkerActivity)) {
	delay := schedules.PollInterval
	for ctx.Err() == nil {
		_, err := engine.Tick(ctx)
		if ctx.Err() == nil {
			status, code := "success", ""
			if err != nil {
				status, code = "failure", "cycle_failed"
			}
			activity(ctx, tasks.WorkerActivity{WorkerID: owner, Cycle: "scheduler", Status: status, Code: code})
		}
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "schedule admission cycle failed")
			delay = min(5*time.Second, delay*2)
		} else {
			delay = schedules.PollInterval
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
