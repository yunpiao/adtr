//go:build !adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestDefaultConnectDelegatesUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := &pgx.ConnConfig{}
	want := errors.New("synthetic failure")
	calls := 0
	conn, err := connectWith(ctx, config, func(got context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
		calls++
		if got != ctx || cfg != config {
			t.Fatal("delegate inputs changed")
		}
		return nil, want
	})
	if conn != nil || err != want || calls != 1 {
		t.Fatal("default delegate contract failed")
	}
}

func TestDefaultConnectNilNativeStillFails(t *testing.T) {
	calls := 0
	conn, err := connectWith(context.Background(), &pgx.ConnConfig{}, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
		calls++
		return nil, nil
	})
	if conn != nil || err == nil || calls != 1 {
		t.Fatal("nil native default failure changed")
	}
}
