package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountUseMaintenanceJoinsCancelledCycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	returned := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runAccountUseMaintenanceTicks(ctx, make(chan time.Time), func(c context.Context) error { close(started); <-c.Done(); close(returned); return c.Err() }, func(error) { t.Error("shutdown should not emit a cycle failure") })
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not join its cancelled cycle")
	}
	select {
	case <-returned:
	default:
		t.Fatal("maintenance returned while database callback remained active")
	}
}
func TestAccountUseMaintenanceBoundsAndRetriesFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	reports := make(chan error, 1)
	done := make(chan struct{})
	var calls atomic.Int32
	expected := errors.New("synthetic maintenance failure")
	go func() {
		runAccountUseMaintenanceTicks(ctx, ticks, func(c context.Context) error {
			deadline, ok := c.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				t.Error("missing bounded cycle")
			}
			if calls.Add(1) == 1 {
				return expected
			}
			cancel()
			return nil
		}, func(e error) { reports <- e })
		close(done)
	}()
	if e := <-reports; !errors.Is(e, expected) {
		t.Fatal(e)
	}
	ticks <- time.Now()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retry did not finish")
	}
	if calls.Load() != 2 {
		t.Fatal("wrong maintenance cycle count", calls.Load())
	}
}
