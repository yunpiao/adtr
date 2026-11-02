package simulationdetection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"regexp"
	"time"
)

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validate(raw json.RawMessage) (json.RawMessage, error) {
	var p Payload
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 2048 || d.Decode(&p) != nil || d.Decode(&struct{}{}) != io.EOF || !identifier.MatchString(p.TemplateID) || !identifier.MatchString(p.DatasetID) || p.TemplateVersion < 1 || p.TemplateVersion > MaxVersions || !hashPattern.MatchString(p.TemplateHash) || !hashPattern.MatchString(p.FixtureHash) || p.MappingVersion != MappingVersion {
		return nil, ErrInvalid
	}
	return json.Marshal(p)
}

func authorizationDenied(err error) bool {
	var taskErr *tasks.Error
	return errors.Is(err, tasks.ErrAuthorization) || errors.Is(err, ErrDenied) || errors.As(err, &taskErr) && taskErr.Status == 403
}

// NewKind uses only the engine's existing heartbeat, context and fenced transactions.
// The injected authorizer MUST check current simulation_detection function/domain/actor grants.
func NewKind(auth tasks.Authorizer) tasks.Kind {
	k := tasks.Kind{Name: KindName, Version: 1, OwnerScoped: true, ReplaySafe: true, CancelDiscardsResult: true, MaxAttempts: 1, Lease: 30 * time.Second, Heartbeat: 5 * time.Second, Timeout: 10 * time.Second, RetryBase: time.Second, RetryCap: time.Second, Validate: validate}
	check := func(ctx context.Context, tx pgx.Tx, t tasks.Task) error {
		if auth == nil {
			return ErrDenied
		}
		v, e := auth(ctx, tx, tasks.Principal{TenantID: t.TenantID, ActorID: t.ActorID}, tasks.Scope{DomainID: t.DomainID, TaskName: KindName}, tasks.Execute)
		if e != nil {
			return e
		}
		if v == "" || v != t.AuthorizationVersion {
			return tasks.ErrAuthorization
		}
		return nil
	}
	k.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		if ex.WithTx == nil {
			return tasks.Outcome{State: tasks.Failed, Code: "invalid_input"}
		}
		raw, e := validate(ex.Task.Payload)
		if e != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "invalid_input"}
		}
		var p Payload
		_ = json.Unmarshal(raw, &p)
		var result []byte
		_, e = ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			if e := check(ctx, tx, ex.Task); e != nil {
				return 0, nil, nil, e
			}
			var intended bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_detection_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND receipt->>'kind'='run' AND receipt->'submission'->'task'->>'taskUUID'=$4)`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ActorID, ex.Task.ID).Scan(&intended); err != nil {
				return 0, nil, nil, err
			}
			if !intended {
				return 0, nil, nil, ErrDenied
			}
			s := Scope{Tenant: ex.Task.TenantID, Domain: ex.Task.DomainID, ActorID: ex.Task.ActorID, Read: true}
			t, e := version(ctx, tx, s, p.TemplateID, p.TemplateVersion)
			if e != nil {
				return 0, nil, nil, e
			}
			if t.Hash != p.TemplateHash {
				return 0, nil, nil, ErrConflict
			}
			d, events, e := dataset(ctx, tx, s, p.DatasetID)
			if e != nil {
				return 0, nil, nil, e
			}
			if d.FixtureHash != p.FixtureHash {
				return 0, nil, nil, ErrConflict
			}
			program, e := detectionrules.Compile(detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}, t.Rules)
			if e != nil {
				return 0, nil, nil, e
			}
			matches, e := program.Evaluate(ctx, mapped(s, events))
			if e != nil {
				return 0, nil, nil, e
			}
			result, e = json.Marshal(Result{Simulation: true, Contract: detectionrules.ContractVersion, Payload: p, EventCount: len(events), Matches: matches})
			if e != nil {
				return 0, nil, nil, e
			}
			if len(result) > 60000 {
				return 0, nil, nil, ErrLimit
			}
			if e = check(ctx, tx, ex.Task); e != nil {
				return 0, nil, nil, e
			}
			return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
		})
		if e != nil {
			code := "evaluation_failed"
			state := tasks.Failed
			if authorizationDenied(e) {
				code = "authorization_revoked"
			}
			if errors.Is(e, tasks.ErrCancelRequested) || errors.Is(e, context.Canceled) {
				state = tasks.Cancelled
				code = "cancelled"
			}
			if errors.Is(e, ErrLimit) || errors.Is(e, detectionrules.ErrLimit) {
				code = "budget_exceeded"
			}
			return tasks.Outcome{State: state, Code: code}
		}
		return tasks.Outcome{State: tasks.Succeeded, Result: result}
	}
	k.ValidateSuccess = func(ctx context.Context, tx pgx.Tx, t tasks.Task, result json.RawMessage) (string, error) {
		if e := check(ctx, tx, t); e != nil {
			if authorizationDenied(e) {
				return "authorization_revoked", nil
			}
			// A transient/context failure is not a durable authorization verdict.
			// Returning the error makes Engine.Finish roll back publication.
			return "", e
		}
		var r Result
		var p Payload
		if json.Unmarshal(result, &r) != nil || json.Unmarshal(t.Payload, &p) != nil || !r.Simulation || r.Contract != detectionrules.ContractVersion || r.Payload != p {
			return "invalid_result", nil
		}
		return "", nil
	}
	return k
}
