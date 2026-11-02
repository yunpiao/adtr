package simulationdetectionschedules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/tasks"
)

var payloadIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var payloadHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validatePayload(raw json.RawMessage) (json.RawMessage, error) {
	var p Payload
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 4096 || d.Decode(&p) != nil || d.Decode(&struct{}{}) != io.EOF || !payloadIdentifier.MatchString(p.ScheduleID) || p.Revision < 1 || p.Revision > MaxRevisions || p.ScheduledAt.IsZero() || p.ScheduledAt.Year() < 1 || p.ScheduledAt.Year() > 9999 || p.ScheduledAt.Nanosecond() != 0 || !payloadIdentifier.MatchString(p.TemplateID) || !payloadIdentifier.MatchString(p.DatasetID) || p.TemplateVersion < 1 || p.TemplateVersion > simulationdetection.MaxVersions || !payloadHash.MatchString(p.TemplateHash) || !payloadHash.MatchString(p.FixtureHash) || p.MappingVersion != simulationdetection.MappingVersion {
		return nil, ErrInvalid
	}
	p.ScheduledAt = p.ScheduledAt.UTC()
	return json.Marshal(p)
}

func executionDenied(err error) bool {
	var e *tasks.Error
	return errors.Is(err, tasks.ErrAuthorization) || errors.Is(err, ErrDenied) || errors.As(err, &e) && e.Status == 403
}

// NewKind is private and unschedulable at the generic API boundary: the schedule
// module admits it with an atomic occurrence intent. This retains the task
// registry's requirement that final-publication-gated kinds are unschedulable.
func NewKind(auth tasks.Authorizer) tasks.Kind {
	k := tasks.Kind{Name: KindName, Version: 1, OwnerScoped: true, ReplaySafe: true, CancelDiscardsResult: true, MaxAttempts: 1, Lease: 30 * time.Second, Heartbeat: 5 * time.Second, Timeout: 10 * time.Second, RetryBase: time.Second, RetryCap: time.Second, Validate: validatePayload}
	check := func(ctx context.Context, tx pgx.Tx, t tasks.Task) (Payload, error) {
		var p Payload
		if auth == nil {
			return p, ErrDenied
		}
		epoch, err := auth(ctx, tx, tasks.Principal{TenantID: t.TenantID, ActorID: t.ActorID}, tasks.Scope{DomainID: t.DomainID, TaskName: KindName}, tasks.Execute)
		if err != nil {
			return p, err
		}
		if epoch == "" || epoch != t.AuthorizationVersion {
			return p, tasks.ErrAuthorization
		}
		raw, err := validatePayload(t.Payload)
		if err != nil {
			return p, err
		}
		if json.Unmarshal(raw, &p) != nil {
			return p, ErrInvalid
		}
		if tx == nil {
			return p, ErrDenied
		}
		var intended bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_detection_schedule_intents WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND schedule_id=$4 AND revision=$5 AND scheduled_at=$6 AND task_id=$7 AND payload_hash=$8)`, t.TenantID, t.DomainID, t.ActorID, p.ScheduleID, p.Revision, p.ScheduledAt, t.ID, digest(p)).Scan(&intended)
		if err != nil {
			return p, err
		}
		if !intended {
			return p, ErrDenied
		}
		return p, nil
	}
	k.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		if ex.WithTx == nil {
			return tasks.Outcome{State: tasks.Failed, Code: "invalid_input"}
		}
		var result []byte
		_, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			p, err := check(ctx, tx, ex.Task)
			if err != nil {
				return 0, nil, nil, err
			}
			s := Scope{Tenant: ex.Task.TenantID, Domain: ex.Task.DomainID, ActorID: ex.Task.ActorID}
			template, events, err := loadPinned(ctx, tx, s, p.Payload)
			if err != nil {
				return 0, nil, nil, err
			}
			program, err := detectionrules.Compile(detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}, template.Rules)
			if err != nil {
				return 0, nil, nil, err
			}
			matches, err := program.Evaluate(ctx, mapped(s, events))
			if err != nil {
				return 0, nil, nil, err
			}
			result, err = json.Marshal(executionResult{Simulation: true, Contract: detectionrules.ContractVersion, Payload: p, EventCount: len(events), Matches: matches})
			if err != nil {
				return 0, nil, nil, err
			}
			if len(result) > 60000 {
				return 0, nil, nil, ErrLimit
			}
			if _, err = check(ctx, tx, ex.Task); err != nil {
				return 0, nil, nil, err
			}
			return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
		})
		if err != nil {
			state := tasks.Failed
			code := "evaluation_failed"
			if executionDenied(err) {
				code = "authorization_revoked"
			}
			if errors.Is(err, ErrConfiguration) {
				code = "configuration_changed"
			}
			if errors.Is(err, tasks.ErrCancelRequested) || errors.Is(err, context.Canceled) {
				state = tasks.Cancelled
				code = "cancelled"
			}
			if errors.Is(err, ErrLimit) || errors.Is(err, detectionrules.ErrLimit) {
				code = "budget_exceeded"
			}
			return tasks.Outcome{State: state, Code: code}
		}
		return tasks.Outcome{State: tasks.Succeeded, Result: result}
	}
	k.ValidateSuccess = func(ctx context.Context, tx pgx.Tx, t tasks.Task, result json.RawMessage) (string, error) {
		p, err := check(ctx, tx, t)
		if err != nil {
			if executionDenied(err) {
				return "authorization_revoked", nil
			}
			if errors.Is(err, ErrInvalid) {
				return "invalid_result", nil
			}
			return "", err
		}
		_, _, err = loadPinned(ctx, tx, Scope{Tenant: t.TenantID, Domain: t.DomainID, ActorID: t.ActorID}, p.Payload)
		if err != nil {
			if errors.Is(err, ErrConfiguration) {
				return "configuration_changed", nil
			}
			if errors.Is(err, ErrInvalid) || errors.Is(err, simulationdetection.ErrInvalid) {
				return "invalid_result", nil
			}
			if errors.Is(err, ErrLimit) || errors.Is(err, detectionrules.ErrLimit) {
				return "budget_exceeded", nil
			}
			return "", err
		}
		var r executionResult
		d := json.NewDecoder(bytes.NewReader(result))
		d.DisallowUnknownFields()
		if len(result) > 60000 || d.Decode(&r) != nil || d.Decode(&struct{}{}) != io.EOF || !r.Simulation || r.Contract != detectionrules.ContractVersion || r.Payload != p || r.EventCount < 0 || r.EventCount > detectionrules.MaxEvents {
			return "invalid_result", nil
		}
		return "", nil
	}
	return k
}

type executionResult struct {
	Simulation bool   `json:"simulation"`
	Contract   string `json:"contract"`
	Payload
	EventCount int                    `json:"eventCount"`
	Matches    []detectionrules.Match `json:"matches"`
}
