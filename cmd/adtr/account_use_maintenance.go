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

// All bounded passes begin in the same cycle instead of making a later one
// inherit an earlier pass's exhausted deadline. There are exactly three joined
// callbacks; the caller's existing shutdown/cycle budget is unchanged.
func reconcileCredentialReservations(ctx context.Context, account, directory, directoryV2 func(context.Context) error) error {
	var accountErr, directoryErr, directoryV2Err error
	var joined sync.WaitGroup
	joined.Add(3)
	go func() { defer joined.Done(); accountErr = account(ctx) }()
	go func() { defer joined.Done(); directoryErr = directory(ctx) }()
	go func() { defer joined.Done(); directoryV2Err = directoryV2(ctx) }()
	joined.Wait()
	return errors.Join(accountErr, directoryErr, directoryV2Err)
}

// The new fixed profile shares the existing cycle deadline; all four passes join.
func reconcileCredentialReservationsV3(ctx context.Context, account, directory, directoryV2, directoryV3 func(context.Context) error) error {
	var previousErr, v3Err error
	var joined sync.WaitGroup
	joined.Add(2)
	go func() {
		defer joined.Done()
		previousErr = reconcileCredentialReservations(ctx, account, directory, directoryV2)
	}()
	go func() { defer joined.Done(); v3Err = directoryV3(ctx) }()
	joined.Wait()
	return errors.Join(previousErr, v3Err)
}

// The fifth fixed purpose is reconciled independently within the same budget.
// All callbacks are joined before shutdown or another maintenance cycle.
func reconcileCredentialReservationsV4(ctx context.Context, account, directory, directoryV2, directoryV3, directoryV4 func(context.Context) error) error {
	var previousErr, v4Err error
	var joined sync.WaitGroup
	joined.Add(2)
	go func() {
		defer joined.Done()
		previousErr = reconcileCredentialReservationsV3(ctx, account, directory, directoryV2, directoryV3)
	}()
	go func() { defer joined.Done(); v4Err = directoryV4(ctx) }()
	joined.Wait()
	return errors.Join(previousErr, v4Err)
}
