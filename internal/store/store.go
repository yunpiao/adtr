package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const SchemaVersion = 1

// Ready checks the schema, not just the TCP port. Each probe owns its connection.
func Ready(ctx context.Context, databaseURL string) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer conn.Close(ctx)
	var version int
	if err := conn.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton = true").Scan(&version); err != nil || version != SchemaVersion {
		return errors.New("schema unavailable or incompatible")
	}
	return nil
}

// Migrate runs only as an explicit command. Runtime probes never mutate storage.
// This first migration creates infrastructure metadata, not business tables.
func Migrate(ctx context.Context, databaseURL string) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return errors.New("migration connection failed")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("migration transaction failed")
	}
	defer tx.Rollback(ctx)
	statements := []string{
		"SELECT pg_advisory_xact_lock(734192801)",
		"CREATE SCHEMA IF NOT EXISTS adtr",
		"CREATE TABLE IF NOT EXISTS adtr.schema_version (singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton), version integer NOT NULL CHECK (version > 0))",
		"INSERT INTO adtr.schema_version (singleton, version) VALUES (true, 1) ON CONFLICT (singleton) DO NOTHING",
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return errors.New("migration statement failed")
		}
	}
	var version int
	if err := tx.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton = true").Scan(&version); err != nil || version != SchemaVersion {
		return errors.New("unsupported schema version; refusing migration")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migration commit failed")
	}
	return nil
}
