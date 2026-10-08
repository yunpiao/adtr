package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Reservations are reconciled outside the task engine's claim/recovery
// transactions. This pass can release only never-opened terminal uses; executor
// completion acknowledgements are separately retained by the task engine.
func runAccountUseMaintenance(ctx context.Context, reconcile func(context.Context) error, report func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	runAccountUseMaintenanceTicks(ctx, ticker.C, reconcile, report)
}
func runAccountUseMaintenanceTicks(ctx context.Context, ticks <-chan time.Time, reconcile func(context.Context) error, report func(error)) {
	for {
		if ctx.Err() != nil {
			return
		}
		cycle, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := reconcile(cycle)
		cancel()
		if err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

// Both bounded passes begin in the same cycle instead of making the second
// inherit the first pass's exhausted deadline. There are exactly two joined
// callbacks; the caller's existing shutdown/cycle budget is unchanged.
func reconcileCredentialReservations(ctx context.Context, account, directory func(context.Context) error) error {
	var accountErr, directoryErr error
	var joined sync.WaitGroup
	joined.Add(2)
	go func() { defer joined.Done(); accountErr = account(ctx) }()
	go func() { defer joined.Done(); directoryErr = directory(ctx) }()
	joined.Wait()
	return errors.Join(accountErr, directoryErr)
}
