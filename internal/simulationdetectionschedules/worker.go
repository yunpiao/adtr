package simulationdetectionschedules

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

const operationTimeout = 5 * time.Second

func closeConnection(c *dbadmission.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Close(ctx)
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

type candidate struct {
	scope Scope
	id    string
}

func (e *Engine) Tick(ctx context.Context) (int, error) {
	list, err := e.candidates(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	var failures []error
	for _, c := range list {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}
		ok, err := e.admit(ctx, c)
		if err != nil {
			failures = append(failures, err)
		} else if ok {
			count++
		}
	}
	return count, errors.Join(failures...)
}
func (e *Engine) candidates(parent context.Context) ([]candidate, error) {
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	defer cancel()
	c, err := dbadmission.ConnectConfig(ctx, e.database.Copy())
	if err != nil {
		return nil, err
	}
	defer closeConnection(c)
	tx, err := c.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if err = e.CheckSchemaTx(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id,domain_id,actor_id,schedule_id FROM adtr.simulation_detection_schedules WHERE state='enabled' AND next_at<=clock_timestamp() ORDER BY next_at,schedule_id LIMIT $1`, CandidateLimit)
	if err != nil {
		return nil, err
	}
	list := []candidate{}
	for rows.Next() {
		var v candidate
		if err = rows.Scan(&v.scope.Tenant, &v.scope.Domain, &v.scope.ActorID, &v.id); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return list, nil
}
func denied(err error) bool {
	var t *tasks.Error
	return errors.Is(err, ErrDenied) || errors.Is(err, tasks.ErrAuthorization) || (errors.As(err, &t) && t.Status == 403)
}
func block(ctx context.Context, tx pgx.Tx, s Scope, v Schedule, state, code string) error {
	if _, err := tx.Exec(ctx, `UPDATE adtr.simulation_detection_schedules SET state=$1,error_code=$2,control_version=control_version+1,updated_at=clock_timestamp() WHERE schedule_id=$3`, state, code, v.ID); err != nil {
		return err
	}
	return appendEvent(ctx, tx, s, v, state, nil, nil, "")
}
func (e *Engine) admit(parent context.Context, c candidate) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	defer cancel()
	conn, err := dbadmission.ConnectConfig(ctx, e.database.Copy())
	if err != nil {
		return false, err
	}
	defer closeConnection(conn)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	epoch, authErr := e.auth(ctx, tx, c.scope, tasks.Execute)
	if authErr != nil && !denied(authErr) {
		return false, authErr
	}
	v, err := get(ctx, tx, c.scope, c.id, true)
	if err != nil {
		return false, err
	}
	if v.State != "enabled" {
		return false, nil
	}
	var original string
	if err = tx.QueryRow(ctx, `SELECT authorization_version FROM adtr.simulation_detection_schedules WHERE schedule_id=$1`, v.ID).Scan(&original); err != nil {
		return false, err
	}
	if authErr != nil || epoch != original {
		if err = block(ctx, tx, c.scope, v, "authorization_blocked", "authorization_revoked"); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return false, err
	}
	due, err := latestIndex(v.Anchor, v.Cycle, now)
	if err != nil {
		return false, err
	}
	if due < v.NextIndex {
		return false, nil
	}
	when, err := CalendarAt(v.Anchor, v.Cycle, due)
	if err != nil {
		return false, err
	}
	next, err := CalendarAt(v.Anchor, v.Cycle, due+1)
	if err != nil {
		if err = block(ctx, tx, c.scope, v, "configuration_blocked", "schedule_time_overflow"); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if _, _, err = loadPinned(ctx, tx, c.scope, v.Payload); err != nil {
		if !errors.Is(err, ErrConfiguration) {
			return false, err
		}
		if err = block(ctx, tx, c.scope, v, "configuration_blocked", "configuration_changed"); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	// Historical occurrences are never backfilled. The current one is eligible
	// only within its bounded lateness window; all skips remain durable history.
	lastMissed := due - 1
	if now.Sub(when) > Lateness {
		lastMissed = due
	}
	if lastMissed >= v.NextIndex {
		first, last := v.NextIndex, lastMissed
		if err = appendEvent(ctx, tx, c.scope, v, "skipped_misfire", &first, &last, ""); err != nil {
			return false, err
		}
	}
	admitted := false
	if lastMissed < due {
		var overlap bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_detection_schedule_intents i JOIN adtr.tasks t ON t.task_id=i.task_id AND t.tenant_id=i.tenant_id WHERE i.tenant_id=$1 AND i.schedule_id=$2 AND t.state IN ('queued','running','retry_wait','cancel_requested'))`, c.scope.Tenant, v.ID).Scan(&overlap)
		if err != nil {
			return false, err
		}
		if overlap {
			if err = appendEvent(ctx, tx, c.scope, v, "skipped_overlap", &due, &due, ""); err != nil {
				return false, err
			}
		} else {
			p := Payload{ScheduleID: v.ID, Revision: v.Revision, ScheduledAt: when, Payload: v.Payload}
			raw, _ := json.Marshal(p)
			// Generic scheduled submission rejects final-publication validation kinds.
			// Keep that guard unchanged: this module owns its occurrence key and intent.
			key := "sds_" + digest(struct {
				ID string
				At time.Time
			}{v.ID, when})
			out, err := e.queue.SubmitTx(ctx, tx, tasks.Principal{TenantID: c.scope.Tenant, ActorID: c.scope.ActorID}, tasks.SubmitInput{TaskName: KindName, DomainID: c.scope.Domain, PayloadVersion: 1, Payload: raw, IdempotencyKey: key})
			if err != nil {
				return false, err
			}
			if out.Task.ActorID != c.scope.ActorID || out.Task.TenantID != c.scope.Tenant || out.Task.DomainID != c.scope.Domain || out.Task.Kind != KindName || out.Task.AuthorizationVersion != original || !samePayload(out.Task.Payload, p) {
				return false, ErrConflict
			}
			if out.Replayed {
				var bound string
				err = tx.QueryRow(ctx, `SELECT task_id FROM adtr.simulation_detection_schedule_intents WHERE tenant_id=$1 AND schedule_id=$2 AND scheduled_at=$3 AND domain_id=$4 AND actor_id=$5 AND revision=$6 AND payload_hash=$7`, c.scope.Tenant, v.ID, when, c.scope.Domain, c.scope.ActorID, v.Revision, digest(p)).Scan(&bound)
				if err != nil || bound != out.Task.ID {
					if err != nil && !errors.Is(err, pgx.ErrNoRows) {
						return false, err
					}
					return false, ErrConflict
				}
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_schedule_intents(tenant_id,domain_id,actor_id,schedule_id,revision,scheduled_at,task_id,payload_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, c.scope.Tenant, c.scope.Domain, c.scope.ActorID, v.ID, v.Revision, when, out.Task.ID, digest(p))
				if err != nil {
					return false, err
				}
			}
			if err = appendEvent(ctx, tx, c.scope, v, "admitted", &due, &due, out.Task.ID); err != nil {
				return false, err
			}
			admitted = true
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE adtr.simulation_detection_schedules SET next_index=$1,next_at=$2,updated_at=clock_timestamp() WHERE schedule_id=$3`, due+1, next, v.ID); err != nil {
		return false, err
	}
	return admitted, tx.Commit(ctx)
}

// samePayload compares the complete validated meaning, not JSON key order.
// The task engine canonicalizes objects before storing them.
func samePayload(raw json.RawMessage, expected Payload) bool {
	normalized, err := validatePayload(raw)
	if err != nil {
		return false
	}
	var actual Payload
	if json.Unmarshal(normalized, &actual) != nil {
		return false
	}
	return actual == expected
}
