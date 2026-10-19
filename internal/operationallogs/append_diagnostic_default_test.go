//go:build !adtr_ci_dbadmission

package operationallogs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type unreadDiagnosticContext struct{}

func (unreadDiagnosticContext) Deadline() (time.Time, bool) { panic("default diagnostic read context") }
func (unreadDiagnosticContext) Done() <-chan struct{}       { panic("default diagnostic read context") }
func (unreadDiagnosticContext) Err() error                  { panic("default diagnostic read context") }
func (unreadDiagnosticContext) Value(any) any               { panic("default diagnostic read context") }

func TestDefaultJournalDiagnosticDoesNotSampleContext(t *testing.T) {
	var ctx context.Context = unreadDiagnosticContext{}
	original := errors.New("synthetic private detail")
	got := newStoreAppendError(ctx, "report", original, true)
	if !got.Uncertain || !reflect.DeepEqual(got.Err, safeError(original)) {
		t.Fatal("default error semantics changed")
	}
	observed := observeAppendFailure(ctx, ctx, original)
	r := &Recorder{}
	r.recordAppendFailureLocked(invalidACKDiagnostic(observed))
	if !reflect.DeepEqual(r.diagnostic, recorderDiagnostic{}) {
		t.Fatal("default diagnostic retained state")
	}
}
