//go:build !adtr_ci_dbadmission

package operationallogs

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
)

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

func connectJournal(ctx context.Context, config *pgx.ConnConfig) (*dbadmission.Conn, error, string) {
	conn, err := dbadmission.ConnectConfig(ctx, config)
	return conn, err, "none"
}
func newStoreConnectError(ctx context.Context, err error, _ string) *AppendError {
	return newStoreAppendError(ctx, "connect", err, false)
}
