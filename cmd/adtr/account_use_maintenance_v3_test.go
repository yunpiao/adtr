package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestV3ReservationMaintenanceStartsAndJoinsAllProfiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := make(chan int, 4)
	returned := make(chan int, 4)
	failures := []error{errors.New("account"), errors.New("directory"), errors.New("v2"), errors.New("v3")}
	callback := func(i int) func(context.Context) error {
		return func(c context.Context) error {
			if c != ctx {
				t.Error("profile did not share original bounded context")
			}
			started <- i
			<-c.Done()
			returned <- i
			return failures[i]
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- reconcileCredentialReservationsV3(ctx, callback(0), callback(1), callback(2), callback(3))
	}()
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("not all profiles began before deadline")
		}
	}
	select {
	case <-done:
		t.Fatal("returned before callbacks joined")
	default:
	}
	cancel()
	select {
	case err := <-done:
		for _, want := range failures {
			if !errors.Is(err, want) {
				t.Fatal("dropped profile failure")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not join every callback")
	}
	if len(returned) != 4 {
		t.Fatal("maintenance returned while profile active")
	}
}
