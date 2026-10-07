package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Claim reserves a queued task without starting a business attempt. It holds no
// tenant authorization lock and performs no execution. Each domain has one live
// lease across all worker processes, so one failing domain cannot monopolize slots.
func (e *Engine) Claim(ctx context.Context, owner string) (Lease, error) {
	if !identifier.MatchString(owner) {
		return Lease{}, problem(400, "invalid_worker_owner")
	}
	var lease Lease
	found := false
	err := e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT "+taskColumns+` FROM adtr.tasks WHERE task_id IN (SELECT DISTINCT ON (q.tenant_id,q.domain_id) q.task_id FROM adtr.tasks q WHERE ((q.state='queued' AND (q.lease_until IS NULL OR q.lease_until<=clock_timestamp())) OR (q.state='retry_wait' AND q.next_attempt_at<=clock_timestamp())) AND NOT EXISTS(SELECT FROM adtr.tasks a WHERE a.tenant_id=q.tenant_id AND a.domain_id=q.domain_id AND a.lease_until>clock_timestamp() AND a.state IN ('queued','running','cancel_requested')) ORDER BY q.tenant_id,q.domain_id,q.created_at,q.task_id) ORDER BY created_at,task_id FOR UPDATE SKIP LOCKED LIMIT 64`)
		if err != nil {
			return err
		}
		candidates := []Task{}
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			next, claimed, err := e.claimCandidate(ctx, tx, candidate, owner)
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			lease = next
			found = true
			break
		}
		return nil
	})
	if err != nil {
		return Lease{}, err
	}
	if !found {
		return Lease{}, ErrNoTask
	}
	return lease, nil
}

// claimCandidate re-reads the locked row in a fresh READ COMMITTED statement.
// Eligibility in Claim's subquery alone is insufficient: EvalPlanQual can retain
// a selected task ID after another transaction changed its state or lease.
func (e *Engine) claimCandidate(ctx context.Context, tx pgx.Tx, candidate Task, owner string) (Lease, bool, error) {
	t, err := scanTask(tx.QueryRow(ctx, "SELECT "+taskColumns+` FROM adtr.tasks WHERE task_id=$1 AND ((state='queued' AND (lease_until IS NULL OR lease_until<=clock_timestamp())) OR (state='retry_wait' AND next_attempt_at<=clock_timestamp())) FOR UPDATE`, candidate.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, err
	}
	if !sameAdmission(t, candidate) {
		return Lease{}, false, ErrLeaseLost
	}
	k, ok := e.registry.kinds[t.Kind]
	if !ok || t.PayloadVersion != k.Version {
		t, err = terminal(ctx, tx, t, Failed, "unknown_task_kind", t.Result)
		if err != nil {
			return Lease{}, false, err
		}
		return Lease{}, false, appendEvent(ctx, tx, t, t.ActorID, "kind_rejected")
	}
	var gate bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended('adtr-task-domain:'||$1||':'||$2,0))", t.TenantID, t.DomainID).Scan(&gate); err != nil {
		return Lease{}, false, err
	}
	if !gate {
		return Lease{}, false, nil
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND task_id<>$3 AND lease_until>clock_timestamp() AND state IN ('queued','running','cancel_requested'))`, t.TenantID, t.DomainID, t.ID).Scan(&active); err != nil {
		return Lease{}, false, err
	}
	if active {
		return Lease{}, false, nil
	}
	if t.State == RetryWait {
		t, err = scanTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET state='queued',next_attempt_at=NULL,result_version=result_version+1,updated_at=clock_timestamp() WHERE task_id=$1 AND state='retry_wait' AND next_attempt_at<=clock_timestamp() AND fencing_token=$2 AND result_version=$3 RETURNING `+taskColumns, t.ID, t.FencingToken, t.ResultVersion))
		if errors.Is(err, pgx.ErrNoRows) {
			return Lease{}, false, nil
		}
		if err != nil {
			return Lease{}, false, err
		}
		if err = appendEvent(ctx, tx, t, t.ActorID, "retry_queued"); err != nil {
			return Lease{}, false, err
		}
	}
	t, err = scanTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET lease_owner=$2,lease_until=clock_timestamp()+($3::bigint*interval '1 millisecond'),fencing_token=fencing_token+1,updated_at=clock_timestamp() WHERE task_id=$1 AND state='queued' AND (lease_until IS NULL OR lease_until<=clock_timestamp()) AND fencing_token=$4 AND result_version=$5 RETURNING `+taskColumns, t.ID, owner, k.Lease.Milliseconds(), t.FencingToken, t.ResultVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, err
	}
	if err = appendEvent(ctx, tx, t, t.ActorID, "leased"); err != nil {
		return Lease{}, false, err
	}
	return Lease{t, owner, t.FencingToken}, true, nil
}
func sameAdmission(a, b Task) bool {
	return a.ID == b.ID && a.TenantID == b.TenantID && a.DomainID == b.DomainID && a.Kind == b.Kind && a.PayloadVersion == b.PayloadVersion && a.ActorID == b.ActorID && a.AuthorizationVersion == b.AuthorizationVersion && a.PayloadHash == b.PayloadHash && bytes.Equal(a.Payload, b.Payload) && a.CreatedAt.Equal(b.CreatedAt) && a.IdempotencyKey == b.IdempotencyKey && a.MaxAttempts == b.MaxAttempts && a.ParentID == b.ParentID
}

// workerLock observes the immutable scope, acquires authorization/tenant locks,
// then locks and validates task fencing using the database clock.
func (e *Engine) workerLock(ctx context.Context, tx pgx.Tx, l Lease) (Task, Kind, string, error, error) {
	t, err := taskByID(ctx, tx, l.Task.TenantID, l.Task.ID, false)
	if err != nil {
		return Task{}, Kind{}, "", nil, err
	}
	if !sameAdmission(t, l.Task) {
		return Task{}, Kind{}, "", nil, ErrLeaseLost
	}
	k, err := e.registry.kind(t.Kind)
	if err != nil {
		return Task{}, Kind{}, "", nil, err
	}
	version, authErr := e.auth(ctx, tx, Principal{t.TenantID, t.ActorID}, k, t.DomainID, Execute)
	if authErr != nil && !denied(authErr) {
		return Task{}, Kind{}, "", nil, authErr
	}
	t, err = taskByID(ctx, tx, t.TenantID, t.ID, true)
	if err != nil {
		return Task{}, Kind{}, "", nil, err
	}
	if !sameAdmission(t, l.Task) {
		return Task{}, Kind{}, "", nil, ErrLeaseLost
	}
	var live bool
	err = tx.QueryRow(ctx, "SELECT lease_owner=$2 AND fencing_token=$3 AND lease_until>clock_timestamp() FROM adtr.tasks WHERE task_id=$1", t.ID, l.Owner, l.Token).Scan(&live)
	if err != nil {
		return Task{}, Kind{}, "", nil, err
	}
	if !live {
		return Task{}, Kind{}, "", nil, ErrLeaseLost
	}
	if authErr == nil && (version == "" || version != t.AuthorizationVersion) {
		authErr = ErrAuthorization
	}
	// The original admission epoch is immutable. A revoke/regrant cycle never
	// resurrects queued or running work; explicit recovery creates a new task.
	if authErr != nil {
		version = t.AuthorizationVersion
	}
	return t, k, version, authErr, nil
}
func (e *Engine) Start(ctx context.Context, l Lease) (Lease, error) {
	var out Lease
	var finalErr error
	err := e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		t, k, version, authErr, err := e.workerLock(ctx, tx, l)
		if err != nil {
			return err
		}
		if t.State != Queued {
			return ErrLeaseLost
		}
		if authErr != nil {
			t, err = terminalFenced(ctx, tx, t, Failed, "authorization_revoked", t.Result)
			if err != nil {
				return err
			}
			finalErr = ErrAuthorization
			return appendEvent(ctx, tx, t, t.ActorID, "authorization_rejected")
		}
		if t.Attempt >= t.MaxAttempts {
			t, err = terminalFenced(ctx, tx, t, DeadLetter, "attempts_exhausted", t.Result)
			if err != nil {
				return err
			}
			finalErr = ErrNoTask
			return appendEvent(ctx, tx, t, t.ActorID, "attempts_exhausted")
		}
		t, err = scanFencedTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET state='running',attempt=attempt+1,authorization_version=$2,result_version=result_version+1,error_code='',lease_until=clock_timestamp()+($3::bigint*interval '1 millisecond'),updated_at=clock_timestamp() WHERE task_id=$1 AND lease_owner=$4 AND fencing_token=$5 AND lease_until>clock_timestamp() AND state='queued' RETURNING `+taskColumns, t.ID, version, k.Lease.Milliseconds(), l.Owner, l.Token))
		if err != nil {
			return err
		}
		if err = appendEvent(ctx, tx, t, t.ActorID, "started"); err != nil {
			return err
		}
		out = Lease{t, l.Owner, l.Token}
		return nil
	})
	if err != nil {
		return Lease{}, err
	}
	return out, finalErr
}
func (e *Engine) Heartbeat(ctx context.Context, l Lease) (Lease, error) {
	var out Lease
	var finalErr error
	err := e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		t, k, version, authErr, err := e.workerLock(ctx, tx, l)
		if err != nil {
			return err
		}
		if t.State != Running && t.State != CancelRequested {
			return ErrLeaseLost
		}
		if authErr != nil && t.Error != "authorization_revoked" {
			t, err = scanFencedTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET state='cancel_requested',error_code='authorization_revoked',result_version=result_version+1,updated_at=clock_timestamp() WHERE task_id=$1 AND lease_owner=$2 AND fencing_token=$3 AND lease_until>clock_timestamp() RETURNING `+taskColumns, t.ID, l.Owner, l.Token))
			if err != nil {
				return err
			}
			if err = appendEvent(ctx, tx, t, t.ActorID, "authorization_stop_requested"); err != nil {
				return err
			}
		}
		if version == "" {
			version = t.AuthorizationVersion
		}
		t, err = scanFencedTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET lease_until=clock_timestamp()+($2::bigint*interval '1 millisecond'),authorization_version=$3,updated_at=clock_timestamp() WHERE task_id=$1 AND lease_owner=$4 AND fencing_token=$5 AND lease_until>clock_timestamp() RETURNING `+taskColumns, t.ID, k.Lease.Milliseconds(), version, l.Owner, l.Token))
		if err != nil {
			return err
		}
		out = Lease{t, l.Owner, l.Token}
		if authErr != nil || t.Error == "authorization_revoked" {
			finalErr = ErrAuthorization
		} else if t.State == CancelRequested {
			finalErr = ErrCancelRequested
		}
		return nil
	})
	if err != nil {
		return Lease{}, err
	}
	return out, finalErr
}
func (e *Engine) Progress(ctx context.Context, l Lease, progress int, cursor, result json.RawMessage, expectedVersion int64) (int64, error) {
	if progress < 0 || progress > 100 || expectedVersion < 0 {
		return 0, problem(400, "invalid_progress")
	}
	cursor, err := canonicalObject(cursor)
	if err != nil {
		return 0, err
	}
	result, err = canonicalObject(result)
	if err != nil {
		return 0, err
	}
	var v int64
	err = e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		t, _, _, authErr, err := e.workerLock(ctx, tx, l)
		if err != nil {
			return err
		}
		if authErr != nil {
			return ErrAuthorization
		}
		if t.State == CancelRequested {
			return ErrCancelRequested
		}
		if t.State != Running {
			return ErrLeaseLost
		}
		if t.ResultVersion != expectedVersion {
			return problem(409, "result_version_conflict")
		}
		if progress < t.Progress {
			return problem(400, "progress_regression")
		}
		t, err = scanFencedTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET progress=$2,cursor=$3,result=$4,result_version=result_version+1,updated_at=clock_timestamp() WHERE task_id=$1 AND lease_owner=$5 AND fencing_token=$6 AND lease_until>clock_timestamp() AND result_version=$7 AND state='running' RETURNING `+taskColumns, t.ID, progress, cursor, result, l.Owner, l.Token, expectedVersion))
		if err != nil {
			return err
		}
		v = t.ResultVersion
		return appendEvent(ctx, tx, t, t.ActorID, "progress")
	})
	return v, err
}
func scanFencedTask(row pgx.Row) (Task, error) {
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrLeaseLost
	}
	return t, err
}
func terminal(ctx context.Context, tx pgx.Tx, t Task, state State, code string, result json.RawMessage) (Task, error) {
	return scanTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET state=$2,error_code=$3,result=$4,result_version=result_version+1,progress=CASE WHEN $2='succeeded' THEN 100 ELSE progress END,lease_owner='',lease_until=NULL,next_attempt_at=NULL,updated_at=clock_timestamp() WHERE task_id=$1 RETURNING `+taskColumns, t.ID, string(state), code, result))
}
func terminalFenced(ctx context.Context, tx pgx.Tx, t Task, state State, code string, result json.RawMessage) (Task, error) {
	return scanFencedTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET state=$2,error_code=$3,result=$4,result_version=result_version+1,progress=CASE WHEN $2='succeeded' THEN 100 ELSE progress END,lease_owner='',lease_until=NULL,next_attempt_at=NULL,updated_at=clock_timestamp() WHERE task_id=$1 AND lease_owner=$5 AND fencing_token=$6 AND lease_until>clock_timestamp() AND result_version=$7 RETURNING `+taskColumns, t.ID, string(state), code, result, t.LeaseOwner, t.FencingToken, t.ResultVersion))
}
func (e *Engine) Finish(ctx context.Context, l Lease, o Outcome) (Task, error) {
	if o.State != Succeeded && o.State != Failed && o.State != PartialFailed && o.State != Cancelled {
		return Task{}, problem(400, "invalid_outcome")
	}
	if o.Code != "" && !safeCode.MatchString(o.Code) {
		return Task{}, problem(400, "invalid_error_code")
	}
	var result json.RawMessage
	var err error
	if len(o.Result) > 0 {
		result, err = canonicalObject(o.Result)
		if err != nil {
			return Task{}, err
		}
	}
	var out Task
	err = e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		t, k, _, authErr, err := e.workerLock(ctx, tx, l)
		if err != nil {
			return err
		}
		if t.State != Running && t.State != CancelRequested {
			return ErrLeaseLost
		}
		if len(result) == 0 {
			result = t.Result
		} // Keep the last atomic checkpoint unless a final result explicitly replaces it.
		state, code := o.State, o.Code
		race := t.State == CancelRequested && state != Cancelled
		if authErr != nil || t.Error == "authorization_revoked" {
			state = Failed
			code = "authorization_revoked"
			race = false
		}
		if state == Cancelled && t.State != CancelRequested {
			return problem(409, "cancellation_not_requested")
		}
		if state == PartialFailed { // Freeze per-object evidence; generic recovery never replays this task.
			if !validPartial(result) {
				return problem(400, "invalid_partial_result")
			}
		}
		canRetry := state == Failed && authErr == nil && t.Error != "authorization_revoked" && o.Retryable && (k.retry(code) || code == "worker_interrupted" && k.ReplaySafe) && t.State != CancelRequested
		if canRetry && t.Attempt < t.MaxAttempts {
			out, err = scanFencedTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET state='retry_wait',error_code=$2,result=$3,result_version=result_version+1,lease_owner='',lease_until=NULL,next_attempt_at=clock_timestamp()+($4::bigint*interval '1 millisecond'),updated_at=clock_timestamp() WHERE task_id=$1 AND lease_owner=$5 AND fencing_token=$6 AND lease_until>clock_timestamp() AND result_version=$7 RETURNING `+taskColumns, t.ID, code, result, k.delay(t.Attempt).Milliseconds(), l.Owner, l.Token, t.ResultVersion))
		} else {
			if canRetry {
				state = DeadLetter
			}
			out, err = terminalFenced(ctx, tx, t, state, code, result)
		}
		if err != nil {
			return err
		}
		if race {
			if err = appendEvent(ctx, tx, out, t.ActorID, "completed-with-cancel-race"); err != nil {
				return err
			}
		}
		return appendEvent(ctx, tx, out, t.ActorID, "finished")
	})
	return out, err
}

// RecoverExpired never trusts an expired worker's result. Only registered,
// read-only replay-safe execution is eligible for requeue. Cancellation after a
// lost executor is explicitly unconfirmed, never falsely acknowledged.
func (e *Engine) RecoverExpired(ctx context.Context) (int, error) {
	n := 0
	err := e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT "+taskColumns+` FROM adtr.tasks WHERE state IN ('running','cancel_requested') AND lease_until<=clock_timestamp() ORDER BY task_id FOR UPDATE SKIP LOCKED LIMIT 100`)
		if err != nil {
			return err
		}
		list := []Task{}
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				rows.Close()
				return err
			}
			list = append(list, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, t := range list {
			k, ok := e.registry.kinds[t.Kind]
			state := Failed
			code := "executor_lost"
			if t.State == CancelRequested {
				code = "cancellation_unconfirmed"
			} else if ok && k.ReplaySafe && t.Attempt < t.MaxAttempts {
				state = RetryWait
			} else if ok && k.ReplaySafe {
				state = DeadLetter
			}
			if state == RetryWait {
				t, err = scanTask(tx.QueryRow(ctx, `UPDATE adtr.tasks SET state='retry_wait',error_code=$2,result_version=result_version+1,lease_owner='',lease_until=NULL,fencing_token=fencing_token+1,next_attempt_at=clock_timestamp()+($3::bigint*interval '1 millisecond'),updated_at=clock_timestamp() WHERE task_id=$1 RETURNING `+taskColumns, t.ID, code, k.delay(t.Attempt).Milliseconds()))
			} else {
				t, err = terminal(ctx, tx, t, state, code, t.Result)
			}
			if err != nil {
				return err
			}
			if err = appendEvent(ctx, tx, t, t.ActorID, "lease_recovered"); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

func validPartial(result json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(result, &object) != nil || len(object) != 1 {
		return false
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(object["objects"], &items) != nil || len(items) < 2 || len(items) > 1000 {
		return false
	}
	seen := map[string]bool{}
	success, other := false, false
	for _, item := range items {
		for key := range item {
			if key != "objectId" && key != "state" && key != "error" {
				return false
			}
		}
		var id, state, code string
		if json.Unmarshal(item["objectId"], &id) != nil || !identifier.MatchString(id) || seen[id] || json.Unmarshal(item["state"], &state) != nil {
			return false
		}
		seen[id] = true
		if raw, ok := item["error"]; ok {
			if json.Unmarshal(raw, &code) != nil || code != "" && !safeCode.MatchString(code) {
				return false
			}
		}
		switch state {
		case "succeeded":
			success = true
			if code != "" {
				return false
			}
		case "failed", "uncertain":
			other = true
		default:
			return false
		}
	}
	return success && other
}
