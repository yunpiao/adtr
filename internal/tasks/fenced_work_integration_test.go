//go:build integration

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestFencedWorkCommitsAtomicallyAndRollsBackEveryFailure(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "one", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("one", "fenced-data"))
	lease := f.start(e, "fenced-owner")
	f.exec("CREATE TABLE adtr.synthetic_artifact (id text PRIMARY KEY)")
	callback := func(id string, fail bool) FencedWork {
		return func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("callback missing bounded context")
			}
			if tx.Conn() != nil || tx.Commit(ctx) == nil || tx.Rollback(ctx) == nil {
				t.Fatal("callback can manage transaction")
			}
			if _, err := tx.Exec(ctx, "INSERT INTO adtr.synthetic_artifact VALUES($1)", id); err != nil {
				return 0, nil, nil, err
			}
			if fail {
				return 0, nil, nil, errors.New("synthetic callback failure")
			}
			return 25, json.RawMessage(`{"ordinal":1}`), json.RawMessage(`{}`), nil
		}
	}
	v, err := e.WithTx(f.ctx, lease, lease.Task.ResultVersion, callback("committed", false))
	if err != nil {
		t.Fatal(err)
	}
	if v != lease.Task.ResultVersion+1 || f.snapshot(task.ID).Progress != 25 {
		t.Fatal("checkpoint not committed")
	}
	if _, err = e.WithTx(f.ctx, lease, v, callback("rolled-back", true)); err == nil {
		t.Fatal("callback failure accepted")
	}
	called := false
	if _, err = e.WithTx(f.ctx, lease, v-1, func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		called = true
		return 25, []byte(`{}`), []byte(`{}`), nil
	}); err == nil || called {
		t.Fatal("stale result version invoked callback")
	}
	f.exec("UPDATE adtr.tasks SET lease_until=clock_timestamp()+interval '150 milliseconds' WHERE task_id=$1", task.ID)
	_, err = e.WithTx(f.ctx, lease, v, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		if _, err := tx.Exec(ctx, "INSERT INTO adtr.synthetic_artifact VALUES('expired'); SELECT pg_sleep(0.2)"); err != nil {
			return 0, nil, nil, err
		}
		return 30, []byte(`{}`), []byte(`{}`), nil
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired lease: %v", err)
	}
	var count int
	if err = f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr.synthetic_artifact").Scan(&count); err != nil || count != 1 {
		t.Fatalf("module writes escaped rollback: %d %v", count, err)
	}
	if f.snapshot(task.ID).ResultVersion != v {
		t.Fatal("failed callbacks advanced checkpoint")
	}
}

func TestFencedWorkRejectsCancellationAndRevocationBeforeCallback(t *testing.T) {
	for _, mode := range []string{"cancel", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			f := newTaskFixture(t)
			e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
			f.grant(taskTestPrincipal, "one", "synthetic")
			task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("one", "guard"))
			lease := f.start(e, "guard-owner")
			want := ErrAuthorization
			if mode == "cancel" {
				if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
					t.Fatal(err)
				}
				want = ErrCancelRequested
			} else {
				f.exec("UPDATE adtr.synthetic_task_grants SET executable=false")
			}
			called := false
			_, err := e.WithTx(f.ctx, lease, lease.Task.ResultVersion, func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
				called = true
				return 0, []byte(`{}`), []byte(`{}`), nil
			})
			if !errors.Is(err, want) || called {
				t.Fatalf("guard invoked callback: %v %v", err, called)
			}
		})
	}
}

func TestStagedArtifactCancellationWinsBeforePublication(t *testing.T) {
	f := newTaskFixture(t)
	kind := syntheticTaskKind(2, syntheticTaskSuccess)
	kind.CancelDiscardsResult = true
	e := f.engine(kind)
	f.grant(taskTestPrincipal, "one", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("one", "cancel-artifact"))
	lease := f.start(e, "artifact-owner")
	if _, err := f.cancel(e, taskTestPrincipal, task.ID); err != nil {
		t.Fatal(err)
	}
	finished, err := e.Finish(f.ctx, lease, Outcome{State: Succeeded, Result: json.RawMessage(`{"artifactToken":"staged"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != Cancelled || finished.Error != "cancelled" || string(finished.Result) != "{}" {
		t.Fatalf("cancelled artifact published: %#v", finished)
	}
	var races int
	if err = f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND action='completed-with-cancel-race'", task.ID).Scan(&races); err != nil || races != 0 {
		t.Fatal("staged artifact misreported external-effect race")
	}
}

func TestOwnerScopedKindsHideOtherActorsBeforePagination(t *testing.T) {
	f := newTaskFixture(t)
	kind := syntheticTaskKind(2, syntheticTaskSuccess)
	kind.OwnerScoped = true
	e := f.engine(kind)
	other := Principal{TenantID: taskTestPrincipal.TenantID, ActorID: 42}
	f.grant(taskTestPrincipal, "one", "synthetic")
	f.grant(other, "one", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("one", "private-key"))
	if _, err := f.submit(e, other, syntheticTaskInput("one", "private-key")); err == nil {
		t.Fatal("cross-actor replay accepted")
	} else {
		requireTaskProblem(t, err, 409)
	}
	if _, err := f.detail(e, other, task.ID); err == nil {
		t.Fatal("cross-actor detail exposed")
	} else {
		requireTaskProblem(t, err, 404)
	}
	if _, err := f.cancel(e, other, task.ID); err == nil {
		t.Fatal("cross-actor cancel permitted")
	} else {
		requireTaskProblem(t, err, 404)
	}
	var list List
	if err := f.transact(func(tx pgx.Tx) (err error) {
		list, err = e.ListTx(f.ctx, tx, other, Filter{PageIdx: 1, PageSize: 1})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if list.Page.Total != 0 || len(list.Tasks) != 0 {
		t.Fatal("foreign owner included before pagination")
	}
	f.exec("UPDATE adtr.synthetic_task_grants SET version='synthetic-v2' WHERE actor_id=$1", taskTestPrincipal.ActorID)
	if _, err := f.detail(e, taskTestPrincipal, task.ID); err == nil {
		t.Fatal("stale epoch detail exposed")
	} else {
		requireTaskProblem(t, err, 404)
	}
	if _, err := f.submit(e, taskTestPrincipal, syntheticTaskInput("one", "private-key")); err == nil {
		t.Fatal("stale epoch replay accepted")
	} else {
		requireTaskProblem(t, err, 409)
	}
	if err := f.transact(func(tx pgx.Tx) (err error) { list, err = e.ListTx(f.ctx, tx, taskTestPrincipal, Filter{}); return err }); err != nil {
		t.Fatal(err)
	}
	if list.Page.Total != 0 || len(list.Tasks) != 0 {
		t.Fatal("stale epoch included before pagination")
	}
}

func TestFencedWorkRechecksTimeBasedAuthorizationAfterCallback(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "one", "synthetic")
	f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("one", "time-expiry"))
	lease := f.start(e, "time-owner")
	f.exec("CREATE TABLE adtr.synthetic_artifact (id text PRIMARY KEY)")
	var expires time.Time
	e.authorize = func(ctx context.Context, tx pgx.Tx, p Principal, s Scope, a Action) (string, error) {
		if !expires.IsZero() && !time.Now().Before(expires) {
			return "", ErrAuthorization
		}
		return syntheticTaskAuthorizer(ctx, tx, p, s, a)
	}
	_, err := e.WithTx(f.ctx, lease, lease.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		expires = time.Now().Add(50 * time.Millisecond)
		if _, err := tx.Exec(ctx, "INSERT INTO adtr.synthetic_artifact VALUES('expired');SELECT pg_sleep(0.2)"); err != nil {
			return 0, nil, nil, err
		}
		return 25, []byte(`{}`), []byte(`{}`), nil
	})
	if !errors.Is(err, ErrAuthorization) {
		t.Fatalf("time-expired callback committed: %v", err)
	}
	var count int
	if err = f.db.QueryRow(f.ctx, "SELECT count(*) FROM adtr.synthetic_artifact").Scan(&count); err != nil || count != 0 {
		t.Fatal("expired work escaped rollback", err)
	}
}
