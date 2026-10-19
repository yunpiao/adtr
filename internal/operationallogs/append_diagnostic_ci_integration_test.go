//go:build integration && adtr_ci_dbadmission

package operationallogs_test

import (
	"context"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/operationallogs"
)

type diagnosticRetryStore struct {
	fixture *journalFixture
	calls   int
}

func (s *diagnosticRetryStore) Append(ctx context.Context, e operationallogs.Event, o operationallogs.RecorderObservation) (operationallogs.Event, error) {
	s.calls++
	stored, err := s.fixture.store.Append(ctx, e, o)
	if s.calls == 1 {
		// The first real SQL transaction fails. Removing this fixture-only trigger
		// permits the recorder's existing retry, without adding a retry in Store.
		s.fixture.exec(`DROP TRIGGER journal_diagnostic_failure ON adtr.operational_log_reports`)
	}
	return stored, err
}

func TestActivityCandidateJournalFirstFailureSurvivesStoreSanitization(t *testing.T) {
	f := newJournalFixture(t)
	f.exec(`CREATE FUNCTION adtr.journal_diagnostic_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic private detail' USING ERRCODE='40001'; END $$`)
	f.exec(`CREATE TRIGGER journal_diagnostic_failure BEFORE INSERT ON adtr.operational_log_reports FOR EACH ROW EXECUTE FUNCTION adtr.journal_diagnostic_failure()`)
	backend := &diagnosticRetryStore{fixture: f}
	recorder, err := operationallogs.NewRecorder(backend, operationallogs.Worker)
	if err != nil {
		t.Fatal("cannot create fixture recorder")
	}
	if !recorder.Emit(operationallogs.ServiceStartRequested, operationallogs.Attempted, operationallogs.NoReason) {
		t.Fatal("fixture event rejected")
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	if recorder.Close(ctx) != nil {
		t.Fatal("fixture recorder did not drain")
	}
	stats := recorder.Stats()
	if backend.calls != 2 || stats.Accepted != 1 || stats.Acknowledged != 1 || stats.WriteFailures != 1 || stats.Unacknowledged != 0 || stats.Abandoned != 0 {
		t.Fatal("existing retry/counters changed")
	}
	diagnostic := recorder.CIJournalFirstFailure()
	if diagnostic.Stage != "report" || diagnostic.ErrorCategory != "postgres" || diagnostic.ContextState != "active" || diagnostic.RecorderContextState != "active" {
		t.Fatal("real Store first failure projection missing")
	}
	var count, failures int64
	if err = f.db.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.operational_log_events),write_failures FROM adtr.operational_log_reports WHERE process_id=$1`, stats.ProcessID).Scan(&count, &failures); err != nil {
		t.Fatal("cannot read fixture delivery evidence")
	}
	if count != 1 || failures != 1 {
		t.Fatal("stored failure counter was cleared")
	}
}
