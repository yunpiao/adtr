package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
)

const SchemaVersion = 3

// Ready checks the schema, not just the TCP port. Each probe owns its connection.
func Ready(ctx context.Context, config *pgx.ConnConfig) error {
	conn, err := pgx.ConnectConfig(ctx, config.Copy())
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
// Versioned migrations are serialized and atomic; runtime readiness never migrates.
func Migrate(ctx context.Context, config *pgx.ConnConfig) error {
	conn, err := pgx.ConnectConfig(ctx, config.Copy())
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
	if err := tx.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton = true").Scan(&version); err != nil || (version < 1 || version > SchemaVersion) {
		return errors.New("unsupported schema version; refusing migration")
	}
	if version < 2 {
		if _, err := tx.Exec(ctx, auth.Schema); err != nil {
			return errors.New("identity schema migration failed")
		}
		version = 2
	}
	if version < 3 {
		if _, err := tx.Exec(ctx, auth.SchemaV3); err != nil {
			return errors.New("access schema migration failed")
		}
		version = 3
	}
	if _, err := tx.Exec(ctx, "UPDATE adtr.schema_version SET version=$1 WHERE singleton=true", version); err != nil {
		return errors.New("schema version update failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migration commit failed")
	}
	return nil
}
