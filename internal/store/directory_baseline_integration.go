//go:build integration

package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MigrateDirectoryBaselineForTest installs the exact historical schema14 using
// the real migration sequence, for fragment absence/collision tests only. It
// cannot downgrade an installed database and is absent from production builds.
// Runtime tests must use Migrate and the application's current schema contract.
func MigrateDirectoryBaselineForTest(ctx context.Context, config *pgx.ConnConfig) error {
	return migrateTo(ctx, config, 14)
}
