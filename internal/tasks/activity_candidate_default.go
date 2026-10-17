//go:build !adtr_ci_dbadmission

package tasks

import (
	"context"

	"github.com/yunpiao/adtr/internal/dbadmission"
)

func (e *Engine) withActivityCandidate(ctx context.Context, lease Lease, expected int64, work FencedWork, activity func(context.Context, ActivityCandidate)) (int64, error) {
	version, err := e.WithTx(ctx, lease, expected, work)
	if err == nil && activity != nil {
		activity(ctx, nil)
	}
	return version, err
}

// BindActivityCandidate is inactive in ordinary builds.
func BindActivityCandidate(ActivityCandidate, *dbadmission.Conn, string) (func(), error) {
	return func() {}, nil
}
