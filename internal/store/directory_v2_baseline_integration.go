//go:build integration

package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MigrateDirectoryV2BaselineForTest installs historical schema15 through the
// real migration sequence for absence, collision and upgrade tests. It cannot
// downgrade a database and is not compiled into production. Runtime fixtures
// must use Migrate and the exact current application schema instead.
func MigrateDirectoryV2BaselineForTest(ctx context.Context, config *pgx.ConnConfig) error {
	return migrateTo(ctx, config, 15)
}
