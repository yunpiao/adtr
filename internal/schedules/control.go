package schedules

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

func (e *Engine) ControlTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, action string, in ControlInput) (Control, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Control{}, err
	}
	if (action != "enable" && action != "pause") || !identifier.MatchString(in.ScheduleID) || !identifier.MatchString(in.IdempotencyKey) || in.ExpectedControlVersion < 1 {
		return Control{}, problem(400, "invalid_input")
	}
	epoch, err := e.auth(ctx, tx, p, tasks.Write)
	if err != nil {
		return Control{}, err
	}
	s, err := byID(ctx, tx, p, in.ScheduleID, true)
	if err != nil {
		return Control{}, err
	}
	if s.AuthorizationVersion != epoch || s.State == AuthorizationBlocked {
		return Control{}, problem(409, "authorization_epoch_changed")
	}
	requestHash := hash(struct {
		Action string
		Input  ControlInput
	}{action, in})
	var receipt Receipt
	var oldHash string
	err = tx.QueryRow(ctx, `SELECT operation_id,schedule_id,action,state,control_version,occurred_at,request_hash FROM adtr.task_schedule_operations WHERE tenant_id=$1 AND actor_id=$2 AND operation_key=$3`, p.TenantID, p.ActorID, in.IdempotencyKey).Scan(&receipt.OperationID, &receipt.ScheduleID, &receipt.Action, &receipt.State, &receipt.ControlVersion, &receipt.CreatedAt, &oldHash)
	if err == nil {
		if oldHash != requestHash {
			return Control{}, problem(409, "idempotency_conflict")
		}
		receipt.CreatedAt = receipt.CreatedAt.UTC()
		return Control{Receipt: receipt, Schedule: s, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Control{}, err
	}
	if s.ControlVersion != in.ExpectedControlVersion {
		return Control{}, problem(409, "control_version_conflict")
	}
	if s.ControlVersion == math.MaxInt64 {
		return Control{}, problem(409, "control_version_conflict")
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return Control{}, err
	}
	first := s.NextIndex
	if action == "enable" && s.State == Paused {
		due := latestDue(s.StartAt, now, s.IntervalSeconds)
		if due >= s.NextIndex {
			if err = appendEvent(ctx, tx, s, "skipped_paused", &first, &due, ""); err != nil {
				return Control{}, err
			}
			s.NextIndex = due + 1
		}
	}
	next, err := gridAt(s.StartAt, s.IntervalSeconds, s.NextIndex)
	if err != nil {
		return Control{}, err
	}
	state := Paused
	if action == "enable" {
		state = Enabled
	}
	s, err = scan(tx.QueryRow(ctx, `UPDATE adtr.task_schedules SET state=$1,control_version=control_version+1,next_index=$2,next_at=$3,error_code='',updated_at=clock_timestamp() WHERE schedule_id=$4 RETURNING `+columns, state, s.NextIndex, next, s.ID))
	if err != nil {
		return Control{}, err
	}
	event := "paused"
	if action == "enable" {
		event = "enabled"
	}
	if err = appendEvent(ctx, tx, s, event, nil, nil, ""); err != nil {
		return Control{}, err
	}
	id, err := newID()
	if err != nil {
		return Control{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO adtr.task_schedule_operations(operation_id,tenant_id,actor_id,operation_key,request_hash,schedule_id,action,state,control_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING operation_id,schedule_id,action,state,control_version,occurred_at`, id, p.TenantID, p.ActorID, in.IdempotencyKey, requestHash, s.ID, action, s.State, s.ControlVersion).Scan(&receipt.OperationID, &receipt.ScheduleID, &receipt.Action, &receipt.State, &receipt.ControlVersion, &receipt.CreatedAt)
	receipt.CreatedAt = receipt.CreatedAt.UTC()
	if err != nil {
		return Control{}, err
	}
	return Control{Receipt: receipt, Schedule: s}, nil
}
