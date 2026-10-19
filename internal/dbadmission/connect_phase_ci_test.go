//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"testing"
)

func TestConnectPhaseDistinguishesAdmissionFromNativeError(t *testing.T) {
	t.Run("admission", func(t *testing.T) {
		g := installTestGate(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		conn, err, phase := connectWithPhase(ctx, nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
			calls++
			return nil, errors.New("unexpected")
		})
		if conn != nil || err != context.Canceled || phase != "admission" || calls != 0 || g.total != 0 || g.pending != 0 || g.invalid {
			t.Fatal("admission boundary changed")
		}
	})
	for _, rawPresent := range []bool{false, true} {
		t.Run(map[bool]string{false: "native_nil", true: "native_partial"}[rawPresent], func(t *testing.T) {
			g := installTestGate(t)
			ctx := context.Background()
			var want error = context.DeadlineExceeded
			if rawPresent {
				want = errors.New("private connector failure")
			}
			calls := 0
			conn, err, phase := connectWithPhase(ctx, nil, func(got context.Context, _ *pgx.ConnConfig) (*pgx.Conn, error) {
				calls++
				if got != ctx {
					t.Fatal("context replaced")
				}
				if rawPresent {
					return &pgx.Conn{}, want
				}
				return nil, want
			})
			if conn != nil || err != want || phase != "native" || calls != 1 || !g.invalid || g.pending != 0 || g.total != 1 || g.firstInvalidReason != invalidConnectFailed {
				t.Fatal("native failure semantics changed")
			}
		})
	}
	t.Run("nil_without_error", func(t *testing.T) {
		g := installTestGate(t)
		conn, err, phase := connectWithPhase(context.Background(), nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) { return nil, nil })
		if conn != nil || err == nil || phase != "native" || !g.invalid {
			t.Fatal("missing raw accepted")
		}
	})
}

func TestConnectPhaseSuccessIsPerCallAndOriginalWrapperRetainsError(t *testing.T) {
	coordinator.Lock()
	absent := coordinator.gate == nil
	coordinator.Unlock()
	if !absent {
		t.Fatal("unexpected installed gate")
	}
	raw := &pgx.Conn{}
	ctx := context.Background()
	config := &pgx.ConnConfig{}
	conn, err, phase := connectWithPhase(ctx, config, func(got context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
		if got != ctx || cfg != config {
			t.Fatal("delegate inputs changed")
		}
		return raw, nil
	})
	if conn == nil || conn.raw != raw || err != nil || phase != "none" {
		t.Fatal("success changed")
	}
	want := errors.New("private error")
	_, got := connectWith(ctx, nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) { return nil, want })
	if got != want {
		t.Fatal("original wrapper replaced error")
	}
}
