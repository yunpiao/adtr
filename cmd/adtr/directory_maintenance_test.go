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
		done <- reconcileCredentialReservations(ctx, func(ctx context.Context) error { close(started); <-ctx.Done(); return accountErr }, func(ctx context.Context) error { <-started; close(directoryRan); return directoryErr })
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
