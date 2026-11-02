package main

import (
	"context"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"time"
)

// Each pass has a bounded deadline and candidate limit in the engine. A failed
// transaction may retry on the next pass; committed occurrence intents prevent
// duplicate admission. Root cancellation interrupts both a pass and its wait.
func runSimulationDetectionScheduleLoop(ctx context.Context, tick func(context.Context) (int, error), report func(error)) {
	delay := simulationdetectionschedules.PollInterval
	for ctx.Err() == nil {
		pass, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := tick(pass)
		cancel()
		if err != nil && ctx.Err() == nil {
			report(err)
			delay = min(5*time.Second, delay*2)
		} else {
			delay = simulationdetectionschedules.PollInterval
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
