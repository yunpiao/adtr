//go:build !adtr_ci_dbadmission

package operationallogs

import "context"

type appendDiagnostic struct{}
type recorderDiagnostic struct{}

func newStoreAppendError(_ context.Context, _ string, err error, uncertain bool) *AppendError {
	return &AppendError{Err: safeError(err), Uncertain: uncertain}
}
func observeAppendFailure(context.Context, context.Context, error) appendDiagnostic {
	return appendDiagnostic{}
}
func invalidACKDiagnostic(appendDiagnostic) appendDiagnostic { return appendDiagnostic{} }
func (*Recorder) recordAppendFailureLocked(appendDiagnostic) {}
