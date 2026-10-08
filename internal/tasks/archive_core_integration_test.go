//go:build integration

package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestArchiveCoreBackfillsOnlyUnambiguousTerminalEvidence(t *testing.T) {
	// Seed the actual pre-core schema, with its existing history guards intact.
	// No terminal timestamp column or trigger exists before this migration.
	f := newTaskFixtureWithSchema(t, queueSchema+ScheduleSchema)
	first := time.Date(2026, 9, 1, 2, 3, 4, 123456000, time.UTC)
	type evidence struct {
		state  State
		action string
		at     time.Time
	}
	cases := []struct {
		id       string
		state    State
		events   []evidence
		backfill bool
	}{
		{"success", Succeeded, []evidence{{Succeeded, "finished", first.Add(time.Hour)}, {Succeeded, "finished", first}}, true},
		{"failure", Failed, []evidence{{Failed, "authorization_rejected", first}}, true},
		{"partial", PartialFailed, []evidence{{PartialFailed, "finished", first}}, true},
		{"exhausted", DeadLetter, []evidence{{DeadLetter, "attempts_exhausted", first}}, true},
		{"cancelled", Cancelled, []evidence{{Cancelled, "cancel_requested", first}}, true},
		{"expired", DeadLetter, []evidence{{DeadLetter, "lease_recovered", first}}, true},
		{"unknown-kind", Failed, []evidence{{Failed, "kind_rejected", first}}, true},
		{"conflicting", Failed, []evidence{{Succeeded, "finished", first}, {Failed, "finished", first.Add(time.Hour)}}, false},
		{"missing", Succeeded, nil, false},
		{"nonterminal-evidence", Failed, []evidence{{Running, "finished", first}}, false},
		{"mismatched", Succeeded, []evidence{{Failed, "finished", first}}, false},
		{"untrusted-action", Succeeded, []evidence{{Succeeded, "submitted", first}}, false},
		{"queued", Queued, []evidence{{Succeeded, "finished", first}}, false},
		{"running", Running, []evidence{{Succeeded, "finished", first}}, false},
		{"retrying", RetryWait, []evidence{{Failed, "finished", first}}, false},
		{"cancelling", CancelRequested, []evidence{{Cancelled, "cancel_requested", first}}, false},
	}
	for _, tc := range cases {
		f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,
 payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts,created_at,updated_at)
 VALUES($1,$2,'domain-a','synthetic',1,'{}','synthetic-hash',$3,'synthetic-v1',$1,$4,2,$5,$6)`,
			tc.id, taskTestPrincipal.TenantID, taskTestPrincipal.ActorID, tc.state, first.Add(-48*time.Hour), first.Add(48*time.Hour))
		for _, event := range tc.events {
			f.exec(`INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,
 attempt,result_version,fencing_token,authorization_version,occurred_at)
 VALUES($1,$2,'domain-a',$3,$4,$5,1,1,1,'synthetic-v1',$6)`,
				tc.id, taskTestPrincipal.TenantID, taskTestPrincipal.ActorID, event.action, event.state, event.at)
		}
	}
	f.exec(ArchiveCoreSchema)
	for _, tc := range cases {
		var terminal *time.Time
		var state State
		var created, updated time.Time
		if err := f.db.QueryRow(f.ctx, `SELECT terminal_at,state,created_at,updated_at FROM adtr.tasks WHERE task_id=$1`, tc.id).
			Scan(&terminal, &state, &created, &updated); err != nil {
			t.Fatal(err)
		}
		if tc.backfill {
			if terminal == nil || !terminal.Equal(first) {
				t.Errorf("%s terminal_at=%v, want exact earliest evidence %v", tc.id, terminal, first)
			}
		} else if terminal != nil {
			t.Errorf("%s terminal_at=%v, want unknown", tc.id, terminal)
		}
		if state != tc.state || !created.Equal(first.Add(-48*time.Hour)) || !updated.Equal(first.Add(48*time.Hour)) {
			t.Errorf("%s migration changed task state or lifecycle metadata", tc.id)
		}
	}

	// A migrated known timestamp and a legacy unknown timestamp are both
	// immutable. Neither legacy task can be restarted in its existing row.
	before := taskCoreRecords(f)
	for _, id := range []string{"success", "missing"} {
		_, err := f.db.Exec(f.ctx, `UPDATE adtr.tasks SET terminal_at=$2 WHERE task_id=$1`, id, first.Add(time.Minute))
		requireCoreGuardError(t, err, "terminal time is immutable")
		_, err = f.db.Exec(f.ctx, `UPDATE adtr.tasks SET state='queued' WHERE task_id=$1`, id)
		requireCoreGuardError(t, err, "terminal task cannot restart")
	}
	if after := taskCoreRecords(f); after != before {
		t.Fatal("rejected legacy task mutation changed durable records")
	}
}

func TestArchiveCoreRecordsFirstActualTerminalTransition(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, syntheticTaskSuccess))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	task := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "terminal-once"))
	if task.TerminalAt != nil {
		t.Fatalf("queued task already has terminal time %v", task.TerminalAt)
	}
	var before, after time.Time
	if err := f.db.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if worked, err := e.RunOne(f.ctx, "terminal-time-worker"); err != nil || !worked {
		t.Fatalf("actual execution: worked=%v error=%v", worked, err)
	}
	var terminal *time.Time
	var state State
	if err := f.db.QueryRow(f.ctx, `SELECT terminal_at,state,clock_timestamp() FROM adtr.tasks WHERE task_id=$1`, task.ID).
		Scan(&terminal, &state, &after); err != nil {
		t.Fatal(err)
	}
	if state != Succeeded || terminal == nil || terminal.Before(before) || terminal.After(after) {
		t.Fatalf("terminal transition: state=%s time=%v, want succeeded between %v and %v", state, terminal, before, after)
	}
	var finished time.Time
	if err := f.db.QueryRow(f.ctx, `SELECT occurred_at FROM adtr.task_events WHERE task_id=$1 AND action='finished'`, task.ID).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if terminal.After(finished) {
		t.Fatal("terminal timestamp was recorded after the committed finish event")
	}
	f.exec(`UPDATE adtr.tasks SET progress=73,updated_at=clock_timestamp() WHERE task_id=$1`, task.ID)
	var retained time.Time
	var progress int
	if err := f.db.QueryRow(f.ctx, `SELECT terminal_at,progress FROM adtr.tasks WHERE task_id=$1`, task.ID).Scan(&retained, &progress); err != nil {
		t.Fatal(err)
	}
	if progress != 73 || !retained.Equal(*terminal) {
		t.Fatalf("metadata update changed first terminal time: got %v progress=%d, want %v", retained, progress, terminal)
	}
	beforeRecords := taskCoreRecords(f)
	_, err := f.db.Exec(f.ctx, `UPDATE adtr.tasks SET terminal_at=NULL WHERE task_id=$1`, task.ID)
	requireCoreGuardError(t, err, "terminal time is immutable")
	_, err = f.db.Exec(f.ctx, `UPDATE adtr.tasks SET state='running' WHERE task_id=$1`, task.ID)
	requireCoreGuardError(t, err, "terminal task cannot restart")
	if taskCoreRecords(f) != beforeRecords {
		t.Fatal("rejected fresh terminal task mutation changed durable records")
	}
}

func TestTaskScheduleNamespaceRejectsManualSubmissionAndRecovery(t *testing.T) {
	f := newTaskFixture(t)
	e := f.engine(syntheticTaskKind(2, func(context.Context, Execution) Outcome { return Outcome{State: Failed, Code: "synthetic_failure"} }))
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	before := taskCoreRecords(f)
	_, err := f.submit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "schedule_reserved"))
	requireCoreProblem(t, err, 400, "reserved_idempotency_key")
	if taskCoreRecords(f) != before {
		t.Fatal("reserved manual submission changed durable records")
	}
	original := f.mustSubmit(e, taskTestPrincipal, syntheticTaskInput("domain-a", "ordinary-recoverable"))
	if worked, err := e.RunOne(f.ctx, "reserved-recovery-worker"); err != nil || !worked {
		t.Fatalf("prepare recoverable task: worked=%v error=%v", worked, err)
	}
	f.requireState(original.ID, Failed, 1)
	before = taskCoreRecords(f)
	err = f.transact(func(tx pgx.Tx) error {
		_, err := e.RecoverTx(f.ctx, tx, taskTestPrincipal, original.ID, "schedule_reserved_recovery")
		return err
	})
	requireCoreProblem(t, err, 400, "reserved_idempotency_key")
	if taskCoreRecords(f) != before {
		t.Fatal("reserved recovery changed task, event, outbox or occurrence records")
	}
}

func TestTaskScheduledSubmissionRequiresOptInAndPinnedEpoch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		schedulable bool
		epoch       string
		code        string
	}{
		{"not-opted-in", false, "synthetic-v1", "task_not_schedulable"},
		{"missing-pinned-epoch", true, "", "invalid_schedule_occurrence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTaskFixture(t)
			kind := syntheticTaskKind(2, syntheticTaskSuccess)
			kind.Schedulable = tc.schedulable
			e := f.engine(kind)
			f.grant(taskTestPrincipal, "domain-a", "synthetic")
			in := coreScheduledInput()
			in.ExpectedAuthorizationVersion = tc.epoch
			before := taskCoreRecords(f)
			_, err := submitCoreScheduled(f, e, taskTestPrincipal, in)
			requireCoreProblem(t, err, 400, tc.code)
			if taskCoreRecords(f) != before {
				t.Fatal("rejected scheduled submission changed durable records")
			}
		})
	}
}

func TestTaskScheduledSubmissionCannotAdoptUnboundLegacyTask(t *testing.T) {
	f := newTaskFixture(t)
	kind := syntheticTaskKind(2, syntheticTaskSuccess)
	kind.Schedulable = true
	e := f.engine(kind)
	f.grant(taskTestPrincipal, "domain-a", "synthetic")
	in := coreScheduledInput()
	when := in.ScheduledAt.UTC().Truncate(time.Microsecond)
	sum := sha256.Sum256([]byte(in.ScheduleID + ":" + when.Format(time.RFC3339Nano)))
	legacy := in.SubmitInput
	legacy.IdempotencyKey = "schedule_" + hex.EncodeToString(sum[:])
	// Reproduce a trusted historical manual submission before the public API
	// reserved this namespace. It matches actor, epoch and payload, but has no
	// occurrence binding and must never be silently adopted by the scheduler.
	if err := f.transact(func(tx pgx.Tx) error {
		_, err := e.submit(f.ctx, tx, taskTestPrincipal, legacy, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := taskCoreRecords(f)
	_, err := submitCoreScheduled(f, e, taskTestPrincipal, in)
	requireCoreProblem(t, err, 409, "schedule_occurrence_conflict")
	if taskCoreRecords(f) != before {
		t.Fatal("legacy adoption attempt changed task, event, outbox or occurrence records")
	}
}

func TestTaskScheduledReplayRequiresOriginalActorAndEpoch(t *testing.T) {
	for _, scenario := range []string{"different-actor", "incorrect-pinned-epoch", "changed-current-epoch"} {
		t.Run(scenario, func(t *testing.T) {
			f := newTaskFixture(t)
			kind := syntheticTaskKind(2, syntheticTaskSuccess)
			kind.Schedulable = true
			// Deliberately not OwnerScoped: this must exercise the scheduler's
			// binding guards even for kinds with tenant-wide task visibility.
			e := f.engine(kind)
			f.grant(taskTestPrincipal, "domain-a", "synthetic")
			other := Principal{TenantID: taskTestPrincipal.TenantID, ActorID: taskTestPrincipal.ActorID + 1}
			f.grant(other, "domain-a", "synthetic")
			in := coreScheduledInput()
			original, err := submitCoreScheduled(f, e, taskTestPrincipal, in)
			if err != nil || original.Replayed {
				t.Fatalf("initial bound occurrence: replayed=%v error=%v", original.Replayed, err)
			}
			before := taskCoreRecords(f)
			replay, err := submitCoreScheduled(f, e, taskTestPrincipal, in)
			if err != nil || !replay.Replayed || replay.Task.ID != original.Task.ID || taskCoreRecords(f) != before {
				t.Fatalf("authorized bound replay changed records: replayed=%v task=%s error=%v", replay.Replayed, replay.Task.ID, err)
			}
			principal := taskTestPrincipal
			switch scenario {
			case "different-actor":
				principal = other
			case "incorrect-pinned-epoch":
				in.ExpectedAuthorizationVersion = "synthetic-v2"
			case "changed-current-epoch":
				f.exec(`UPDATE adtr.synthetic_task_grants SET version='synthetic-v2' WHERE actor_id=$1`, principal.ActorID)
				in.ExpectedAuthorizationVersion = "synthetic-v2"
			}
			_, err = submitCoreScheduled(f, e, principal, in)
			if scenario == "incorrect-pinned-epoch" {
				if !errors.Is(err, ErrAuthorization) {
					t.Fatalf("incorrect pinned epoch: got %v, want authorization rejection", err)
				}
			} else {
				requireCoreProblem(t, err, 409, "schedule_occurrence_conflict")
			}
			if taskCoreRecords(f) != before {
				t.Fatal("rejected bound replay changed task, event, outbox or occurrence records")
			}
		})
	}
}

func coreScheduledInput() ScheduledInput {
	return ScheduledInput{
		SubmitInput: syntheticTaskInput("domain-a", "ignored-scheduler-key"),
		ScheduleID:  "synthetic-core-schedule", ExpectedAuthorizationVersion: "synthetic-v1",
		ScheduledAt: time.Date(2026, 10, 7, 17, 30, 0, 123456789, time.FixedZone("synthetic-offset", 8*60*60)),
	}
}

func submitCoreScheduled(f *taskFixture, e *Engine, p Principal, in ScheduledInput) (Submission, error) {
	var out Submission
	err := f.transact(func(tx pgx.Tx) (err error) {
		out, err = e.SubmitScheduledTx(f.ctx, tx, p, in)
		return err
	})
	return out, err
}

// Compare complete persisted rows, not just counts: failed admission must not
// mutate an existing task or binding either, even if no new rows are appended.
func taskCoreRecords(f *taskFixture) string {
	f.t.Helper()
	var records string
	err := f.db.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'tasks',(SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY task_id),'[]'::jsonb) FROM adtr.tasks t),
 'events',(SELECT COALESCE(jsonb_agg(to_jsonb(e) ORDER BY id),'[]'::jsonb) FROM adtr.task_events e),
 'outbox',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY id),'[]'::jsonb) FROM adtr.task_outbox o),
 'occurrences',(SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY tenant_id,schedule_id,scheduled_at),'[]'::jsonb) FROM adtr.task_schedule_occurrences s))`).Scan(&records)
	if err != nil {
		f.t.Fatal(err)
	}
	return records
}

func requireCoreProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	var problem *Error
	if !errors.As(err, &problem) || problem.Status != status || problem.Code != code {
		t.Fatalf("got error %v, want task error %d %s", err, status, code)
	}
}

func requireCoreGuardError(t *testing.T, err error, message string) {
	t.Helper()
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "P0001" || databaseError.Message != message {
		t.Fatalf("got error %v, want terminal guard rejection %q", err, message)
	}
}
