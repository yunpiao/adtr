//go:build integration

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivateArtifactSuccessGateAtomicFailureAndPrecedence(t *testing.T) {
	for _, mode := range []string{"success", "invalid", "database", "cancel", "revoke", "unsafe-code"} {
		t.Run(mode, func(t *testing.T) {
			f := newTaskFixture(t)
			kind := syntheticTaskKind(2, syntheticTaskSuccess)
			kind.OwnerScoped = true
			kind.CancelDiscardsResult = true
			called := false
			failure := errors.New("synthetic database failure")
			kind.ValidateSuccess = func(ctx context.Context, tx pgx.Tx, task Task, result json.RawMessage) (string, error) {
				called = true
				if tx.Conn() != nil || tx.Commit(ctx) == nil || tx.Rollback(ctx) == nil || task.State != Running || string(result) != `{"artifact":true}` {
					t.Fatal("unbounded/unlocked success gate")
				}
				switch mode {
				case "invalid":
					return "artifact_invalid", nil
				case "database":
					return "", failure
				case "unsafe-code":
					return "secret unsafe code", nil
				}
				return "", nil
			}
			e := f.engine(kind)
			f.grant(taskTestPrincipal, "one", "synthetic")
			task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("one", "success-gate"))
			lease := f.start(e, "gate-owner")
			if mode == "cancel" {
				if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "revoke" {
				f.exec("UPDATE adtr.synthetic_task_grants SET executable=false")
			}
			result, err := e.Finish(f.ctx, lease, Outcome{State: Succeeded, Result: []byte(`{"artifact":true}`), Retryable: true})
			switch mode {
			case "success":
				if err != nil || result.State != Succeeded || !called {
					t.Fatal(result, err)
				}
			case "invalid":
				if err != nil || result.State != Failed || result.Error != "artifact_invalid" || string(result.Result) != "{}" || !called {
					t.Fatal(result, err)
				}
			case "database", "unsafe-code":
				if err == nil || f.snapshot(task.ID).State != Running || !called {
					t.Fatal("gate error committed")
				}
			case "cancel":
				if err != nil || result.State != Cancelled || called {
					t.Fatal("cancellation did not win")
				}
			case "revoke":
				if err != nil || result.State != Failed || result.Error != "authorization_revoked" || called {
					t.Fatal("revocation did not win")
				}
			}
		})
	}
}
