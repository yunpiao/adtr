//go:build integration && adtr_ci_dbadmission

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
)

func TestActivityCandidateRealCommittedAttempt(t *testing.T) {
	for _, kindName := range []string{"computer_assets.v3.export", "group_assets.v3.export", "user_assets.v3.export"} {
		t.Run(kindName, func(t *testing.T) {
			f := newTaskFixture(t)
			kind := syntheticTaskKind(1, syntheticTaskSuccess)
			kind.Name = kindName
			e := f.engine(kind)
			f.grant(taskTestPrincipal, "one", kindName)
			input := syntheticTaskInput("one", "candidate")
			input.TaskName = kindName
			task := f.mustSubmit(e, taskTestPrincipal, input)
			lease := f.start(e, "candidate-worker-0")
			conn, err := dbadmission.ConnectConfig(f.ctx, f.cfg.Copy())
			if err != nil {
				t.Fatal("candidate connection failed")
			}
			defer conn.Close(context.Background())
			calls := 0
			var seen *activityCandidateProof
			activity := func(_ context.Context, candidate ActivityCandidate) {
				calls++
				var ok bool
				seen, ok = candidate.(*activityCandidateProof)
				if !ok || f.snapshot(task.ID).ResultVersion != lease.Task.ResultVersion+1 || f.snapshot(task.ID).Progress != 10 {
					t.Fatal("candidate preceded durable fenced commit")
				}
				release, _, err := BindActivityCandidate(candidate, conn, "candidate-worker")
				if err != nil {
					t.Fatal("candidate bind failed")
				}
				defer release()
				if _, _, err := BindActivityCandidate(candidate, conn, "candidate-worker"); err == nil {
					t.Fatal("candidate bound twice")
				}
			}
			work := func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
				return 10, []byte(`{}`), []byte(`{}`), nil
			}
			f.exec("CREATE TABLE adtr.candidate_parent(id integer PRIMARY KEY); CREATE TABLE adtr.candidate_child(parent integer REFERENCES adtr.candidate_parent(id) DEFERRABLE INITIALLY DEFERRED)")
			commitFailure := func(c context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
				_, err := tx.Exec(c, "INSERT INTO adtr.candidate_child VALUES(1)")
				return 10, []byte(`{}`), []byte(`{}`), err
			}
			if _, err := e.withActivityCandidate(f.ctx, lease, lease.Task.ResultVersion, commitFailure, activity); err == nil || calls != 0 {
				t.Fatal("failed commit emitted activity candidate")
			}
			version, err := e.withActivityCandidate(f.ctx, lease, lease.Task.ResultVersion, work, activity)
			if err != nil || version != lease.Task.ResultVersion+1 || calls != 1 || seen == nil || !seen.revoked {
				t.Fatal("committed candidate lifecycle failed")
			}
			// This synthetic commit intentionally has no exporter snapshot table:
			// the proof is candidate identity, never snapshot/capture eligibility.
			if _, _, err := BindActivityCandidate(seen, conn, "candidate-worker"); err == nil {
				t.Fatal("callback-return candidate remained eligible")
			}
			if _, err := e.withActivityCandidate(f.ctx, lease, version, nil, activity); err == nil || calls != 1 {
				t.Fatal("nil work semantics changed")
			}
			failed := func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
				return 10, nil, nil, errors.New("synthetic rollback")
			}
			if _, err := e.withActivityCandidate(f.ctx, lease, version, failed, activity); err == nil || calls != 1 {
				t.Fatal("failed fenced work emitted candidate")
			}
			later := func(_ context.Context, candidate ActivityCandidate) {
				if candidate != nil {
					t.Fatal("later commit minted candidate")
				}
				calls++
			}
			if _, err := e.withActivityCandidate(f.ctx, lease, version, work, later); err != nil || calls != 2 {
				t.Fatal("ordinary later activity was lost")
			}
		})
	}
}

func TestActivityCandidateRevokedWhenCallbackSkipsOrPanics(t *testing.T) {
	for _, panicCallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "skip", true: "panic"}[panicCallback], func(t *testing.T) {
			f := newTaskFixture(t)
			kind := syntheticTaskKind(1, syntheticTaskSuccess)
			kind.Name = "user_assets.v3.export"
			e := f.engine(kind)
			f.grant(taskTestPrincipal, "one", kind.Name)
			input := syntheticTaskInput("one", "callback-exit")
			input.TaskName = kind.Name
			f.mustSubmit(e, taskTestPrincipal, input)
			lease := f.start(e, "candidate-worker-0")
			var seen *activityCandidateProof
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				_, err := e.withActivityCandidate(f.ctx, lease, lease.Task.ResultVersion,
					func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
						return 10, []byte(`{}`), []byte(`{}`), nil
					},
					func(_ context.Context, candidate ActivityCandidate) {
						seen, _ = candidate.(*activityCandidateProof)
						if panicCallback {
							panic("synthetic callback failure")
						}
						// Equivalent to recorder coalescing: do not consume or bind.
					})
				if err != nil {
					t.Fatal("candidate transaction failed")
				}
			}()
			if seen == nil || !seen.revoked || seen.consumed || panicked != panicCallback {
				t.Fatal("unconsumed callback candidate survived its scope")
			}
			if _, _, err := BindActivityCandidate(seen, nil, "candidate-worker"); err == nil {
				t.Fatal("returned callback candidate was reusable")
			}
		})
	}
}
