//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// ConnectConfigWithCIPhase is a per-call observation used only by the CI journal.
// Native includes pgx initialization; it does not identify DNS, dial or TLS causes.
func ConnectConfigWithCIPhase(ctx context.Context, config *pgx.ConnConfig) (*Conn, error, string) {
	return connectWithPhase(ctx, config, pgx.ConnectConfig)
}
