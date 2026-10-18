//go:build !adtr_ci_dbadmission

package dbadmission

import "context"

func observeLifecycleConnectFailure(_ context.Context, permit lifecyclePermit, _ error, _ bool) {
	if permit != nil {
		permit.failed()
	}
}
