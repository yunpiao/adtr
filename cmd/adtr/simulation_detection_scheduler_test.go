package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSimulationDetectionSchedulerCancellationAndDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	called := make(chan time.Duration, 1)
	go func() {
		defer close(done)
		runSimulationDetectionScheduleLoop(ctx, func(pass context.Context) (int, error) {
			deadline, ok := pass.Deadline()
			if !ok {
				called <- 0
			} else {
				called <- time.Until(deadline)
			}
			<-pass.Done()
			return 0, pass.Err()
		}, func(error) { t.Error("root cancellation reported as cycle failure") })
	}()
	select {
	case bound := <-called:
		if bound <= 0 || bound > 5*time.Second {
			t.Fatalf("pass deadline %v", bound)
		}
	case <-time.After(time.Second):
		t.Fatal("no immediate pass")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler failed to join")
	}
}
func TestSimulationDetectionSchedulerReportsFailureAndCancelsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sentinel := errors.New("temporary transaction failure")
	done := make(chan struct{})
	reported := make(chan error, 1)
	go func() {
		defer close(done)
		runSimulationDetectionScheduleLoop(ctx, func(context.Context) (int, error) { return 0, sentinel }, func(err error) { reported <- err; cancel() })
	}()
	select {
	case err := <-reported:
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("failure not reported")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("backoff wait failed to cancel")
	}
}
func TestSimulationDetectionSchedulerCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runSimulationDetectionScheduleLoop(ctx, func(context.Context) (int, error) { t.Fatal("cancelled loop admitted work"); return 0, nil }, func(error) { t.Fatal("unexpected error") })
}
