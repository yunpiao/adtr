package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDirectoryMaintenanceDoesNotWaitForBlockedAccountPass(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := make(chan struct{})
	directoryRan := make(chan struct{})
	done := make(chan error, 1)
	accountErr := errors.New("account cleanup failed")
	directoryErr := errors.New("directory cleanup failed")
	go func() {
		done <- reconcileCredentialReservations(ctx, func(ctx context.Context) error { close(started); <-ctx.Done(); return accountErr }, func(ctx context.Context) error { <-started; close(directoryRan); return directoryErr }, func(context.Context) error { return nil })
	}()
	select {
	case <-directoryRan:
	case <-ctx.Done():
		t.Fatal("directory cleanup starved behind account pass")
	}
	select {
	case <-done:
		t.Fatal("returned before both callbacks stopped")
	default:
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, accountErr) || !errors.Is(err, directoryErr) {
			t.Fatal("cleanup errors lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup callbacks were not joined")
	}
}

func TestVersionedDirectoryMaintenanceDoesNotStarveBehindBothLegacyPasses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := make(chan struct{}, 3)
	results := []error{errors.New("account"), errors.New("directory1"), errors.New("directory2")}
	pass := func(index int) func(context.Context) error {
		return func(c context.Context) error {
			started <- struct{}{}
			<-c.Done()
			return results[index]
		}
	}
	done := make(chan error, 1)
	go func() { done <- reconcileCredentialReservations(ctx, pass(0), pass(1), pass(2)) }()
	for range 3 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("a profile was starved")
		}
	}
	select {
	case <-done:
		t.Fatal("cleanup returned before all callbacks stopped")
	default:
	}
	cancel()
	select {
	case err := <-done:
		for _, want := range results {
			if !errors.Is(err, want) {
				t.Fatal("cleanup lost profile error", err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup callbacks were not joined")
	}
}
