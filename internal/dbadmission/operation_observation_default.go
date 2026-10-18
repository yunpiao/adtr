//go:build !adtr_ci_dbadmission

package dbadmission

import "github.com/jackc/pgx/v5"

func observeLifecycleConfig(config *pgx.ConnConfig, _ lifecyclePermit) *pgx.ConnConfig { return config }
