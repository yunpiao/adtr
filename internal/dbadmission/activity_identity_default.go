//go:build !adtr_ci_dbadmission

package dbadmission

import "context"

func RegisterActivityIdentity(_ *Conn, _ ActivityIdentity, workerID string) (func(), string, error) {
	return func() {}, workerID, nil
}
func AwaitActivitySelection(context.Context, ActivityIdentity) error { return nil }
