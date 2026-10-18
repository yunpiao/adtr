//go:build adtr_ci_dbadmission

package main

import (
	"context"
	"github.com/yunpiao/adtr/internal/dbadmission"
)

func observeCIWorkerContext(ctx context.Context, stop context.CancelFunc) {
	dbadmission.ObserveCIWorkerContext(ctx)
	dbadmission.ConfigureCIShutdown(stop)
}
