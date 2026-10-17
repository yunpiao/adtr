//go:build !adtr_ci_dbadmission

package dbadmission

func RegisterActivityIdentity(*Conn, ActivityIdentity) (func(), error) {
	return func() {}, nil
}
