// Package dbadmission owns the lifetime of worker database connections.
// The normal build is a direct pgx delegate. The CI-only implementation is
// deliberately not installed by the application in this first verification slice.
package dbadmission

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Conn does not expose or embed the underlying connection. A transaction may
// use it only through pgx.Tx; the repository checker forbids Tx.Conn escapes.
type Conn struct {
	raw    *pgx.Conn
	permit lifecyclePermit
	mu     sync.Mutex
	closed bool
}

// closeObservation contains only already-computed close facts, never errors or identity.
type closeObservation struct {
	alreadyClosed, closeFailed, cleanupDone bool
}

type lifecyclePermit interface {
	connected(*Conn)
	closingConnection()
	failed()
	closedObserved(closeObservation)
}

// ConnectConfig obtains admission before calling pgx, including its initialization.
func ConnectConfig(ctx context.Context, config *pgx.ConnConfig) (*Conn, error) {
	return connectWith(ctx, config, pgx.ConnectConfig)
}

func connectWith(ctx context.Context, config *pgx.ConnConfig, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) (*Conn, error) {
	permit, err := acquireLifecycle(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := connect(ctx, config)
	if err != nil || raw == nil {
		if permit != nil {
			// A failed startup may leave asynchronous cleanup. Never claim drain.
			permit.failed()
		}
		if err == nil {
			err = errors.New("database connection unavailable")
		}
		return nil, err
	}
	conn := &Conn{raw: raw, permit: permit}
	if permit != nil {
		permit.connected(conn)
	}
	return conn, nil
}

func (c *Conn) Begin(ctx context.Context) (pgx.Tx, error) {
	return c.raw.Begin(ctx)
}

// Exec preserves the native context, arguments, command tag and error unchanged.
func (c *Conn) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return c.raw.Exec(ctx, sql, args...)
}

func (c *Conn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.raw.QueryRow(ctx, sql, args...)
}

func (c *Conn) Close(ctx context.Context) error {
	if c.permit == nil {
		return c.raw.Close(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.raw.Close(ctx)
	}
	c.closed = true
	c.permit.closingConnection()
	// A native transaction may close its connection on an error. Such an
	// unobserved close invalidates this attempt rather than proving quiescence.
	alreadyClosed := c.raw.IsClosed()
	err := c.raw.Close(ctx)
	cleanupDone := false
	select {
	case <-c.raw.PgConn().CleanupDone():
		cleanupDone = true
	default:
	}
	c.permit.closedObserved(closeObservation{alreadyClosed, err != nil, cleanupDone})
	return err
}
