package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDirectoryV4MaintenanceJoinsAllFiveAndPreservesErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan int, 5)
	returned := make(chan int, 5)
	release := make(chan struct{})
	done := make(chan error, 1)
	failures := []error{errors.New("account"), errors.New("v1"), errors.New("v2"), errors.New("v3"), errors.New("v4")}
	callback := func(i int) func(context.Context) error {
		return func(c context.Context) error {
			started <- i
			<-c.Done()
			if i == 4 {
				<-release
			}
			returned <- i
			return errors.Join(c.Err(), failures[i])
		}
	}
	go func() {
		done <- reconcileCredentialReservationsV4(ctx, callback(0), callback(1), callback(2), callback(3), callback(4))
	}()
	seen := map[int]bool{}
	for i := 0; i < 5; i++ {
		select {
		case n := <-started:
			seen[n] = true
		case <-time.After(time.Second):
			t.Fatal("pass not independently started")
		}
	}
	if len(seen) != 5 {
		t.Fatal("duplicate/missing pass")
	}
	cancel()
	for i := 0; i < 4; i++ {
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("cancelled pass did not return")
		}
	}
	select {
	case <-done:
		t.Fatal("returned before v4 joined")
	default:
	}
	close(release)
	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("v4 did not join")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancel lost")
	}
	for _, failure := range failures {
		if !errors.Is(err, failure) {
			t.Fatal("pass error lost", failure)
		}
	}
}
