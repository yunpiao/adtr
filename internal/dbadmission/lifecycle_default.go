//go:build !adtr_ci_dbadmission

package dbadmission

import "context"

func acquireLifecycle(context.Context) (lifecyclePermit, error) {
	return nil, nil
}
