//go:build integration && adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestConnectFailureIntegrationCancellationAdmissionBoundary(t *testing.T) {
	for _, beforeAdmission := range []bool{true, false} {
		name := "after_admission_native_failure"
		if beforeAdmission {
			name = "before_admission_no_native_call"
		}
		t.Run(name, func(t *testing.T) {
			ctx, stopFixture := context.WithTimeout(context.Background(), 10*time.Second)
			defer stopFixture()
			config := integrationConfig(t)
			// Establish that the configured owned PostgreSQL fixture is live;
			// this test never replaces its host, port, role or authentication.
			observer, err := pgx.ConnectConfig(ctx, config)
			if err != nil {
				t.Fatal("owned PostgreSQL fixture connection failed")
			}
			defer observer.Close(context.Background())
			var one int
			if observer.QueryRow(ctx, "SELECT 1").Scan(&one) != nil || one != 1 {
				t.Fatal("owned PostgreSQL fixture observation failed")
			}
			g := installTestGate(t)
			root, cancel := context.WithCancel(ctx)
			defer cancel()
			g.application, g.witness = true, &diagnosticWitness{}
			ObserveCIWorkerContext(root)
			if beforeAdmission {
				cancel()
			}
			calls := 0
			var nativeErr error
			var nativeRaw *pgx.Conn
			conn, err, phase := connectWithPhase(root, config, func(c context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
				calls++
				// Cancel only after admission. Native pgx is the real connector;
				// its original error is neither substituted nor reclassified here.
				cancel()
				nativeRaw, nativeErr = pgx.ConnectConfig(c, cfg)
				return nativeRaw, nativeErr
			})
			if nativeRaw != nil {
				defer nativeRaw.Close(context.Background())
			}
			if conn != nil {
				defer conn.Close(context.Background())
				t.Fatal("cancelled connection unexpectedly returned a managed handle")
			}
			if beforeAdmission {
				if phase != "admission" || err != context.Canceled || calls != 0 || nativeErr != nil || g.invalid || g.pending != 0 || g.total != 0 || g.firstConnectFailure != nil || g.connectFailureDiagnostic() != nil || g.witness.(*diagnosticWitness).stores != 0 {
					t.Fatal("admission cancellation invented a native connect failure")
				}
				return
			}
			if phase != "native" || calls != 1 || nativeErr == nil || err != nativeErr || !errors.Is(nativeErr, context.Canceled) || nativeRaw != nil {
				t.Fatal("native cancelled connector facts or error identity changed")
			}
			d := g.connectFailureDiagnostic()
			if d == nil || d.Event != "owned_ci_first_connect_failure" || d.BirthPhase != "open" || d.BirthCaptureEnded || d.BirthRecoveryAuthorized || d.FailurePhase != "open" || d.ContextState != "canceled" || d.ErrorCategory != "canceled" || !d.ErrorMatchesContext || d.RootContextState != "canceled" || !d.RawNil {
				t.Fatal("real native cancellation did not produce exact connect observations")
			}
			if !g.invalid || g.firstInvalidReason != invalidConnectFailed || g.pending != 0 || g.open != 0 || g.total != 1 || g.recoveryQualified != 0 || g.witness.(*diagnosticWitness).stores == 0 {
				t.Fatal("connect diagnostic changed original sticky failure accounting")
			}
			for _, ticket := range g.tickets {
				if ticket.state != 'f' {
					t.Fatal("failed native connect retained a live lifecycle state")
				}
			}
		})
	}
}
