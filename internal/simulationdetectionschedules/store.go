package simulationdetectionschedules

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/tasks"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Engine struct {
	database  *pgx.ConnConfig
	queue     *tasks.Engine
	authorize tasks.Authorizer
}

// New requires an authorizer that serializes tenant then actor before schedule
// locks, and returns a nonempty durable authorization epoch. No grants are cached.
func New(c *pgx.ConnConfig, q *tasks.Engine, a tasks.Authorizer) (*Engine, error) {
	if c == nil || q == nil || a == nil {
		return nil, ErrInvalid
	}
	return &Engine{c.Copy(), q, a}, nil
}
func (e *Engine) CheckSchemaTx(ctx context.Context, tx pgx.Tx) error {
	return tasks.CheckSchemaTx(ctx, tx, schemaversion.Current)
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func valid(s string) bool { return identifier.MatchString(s) }
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func newID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return "sds_" + hex.EncodeToString(b[:]), nil
}
func (e *Engine) auth(ctx context.Context, tx pgx.Tx, s Scope, a tasks.Action) (string, error) {
	if tx == nil || !valid(s.Tenant) || !valid(s.Domain) || s.Domain == "platform" || s.ActorID <= 0 {
		return "", ErrDenied
	}
	if err := e.CheckSchemaTx(ctx, tx); err != nil {
		return "", err
	}
	epoch, err := e.authorize(ctx, tx, tasks.Principal{TenantID: s.Tenant, ActorID: s.ActorID}, tasks.Scope{TaskName: KindName, DomainID: s.Domain}, a)
	if err == nil && epoch == "" {
		return "", ErrDenied
	}
	return epoch, err
}
func validateDefinition(d Definition) error {
	if !utf8.ValidString(d.Label) || d.Label != strings.TrimSpace(d.Label) || utf8.RuneCountInString(d.Label) < 1 || utf8.RuneCountInString(d.Label) > 80 || d.Cycle < 0 || d.Cycle > 2 || !valid(d.TemplateID) || !valid(d.DatasetID) || d.TemplateVersion < 1 || d.TemplateVersion > simulationdetection.MaxVersions {
		return ErrInvalid
	}
	for _, r := range d.Label {
		if unicode.IsControl(r) {
			return ErrInvalid
		}
	}
	return nil
}
func pin(ctx context.Context, tx pgx.Tx, s Scope, d Definition) (simulationdetection.Payload, error) {
	p := simulationdetection.Payload{TemplateID: d.TemplateID, TemplateVersion: d.TemplateVersion, DatasetID: d.DatasetID, MappingVersion: simulationdetection.MappingVersion}
	// Lock template before reading version so an edit cannot race validation.
	var current int64
	var archived bool
	err := tx.QueryRow(ctx, `SELECT version,archived FROM adtr.simulation_detection_templates WHERE tenant_id=$1 AND domain_id=$2 AND template_id=$3 FOR SHARE`, s.Tenant, s.Domain, d.TemplateID).Scan(&current, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrConfiguration
	}
	if err != nil {
		return p, err
	}
	if archived || current != d.TemplateVersion {
		return p, ErrConfiguration
	}
	t, err := simulationdetection.DetailTx(ctx, tx, simulationdetection.Scope{Tenant: s.Tenant, Domain: s.Domain, ActorID: s.ActorID, Read: true}, d.TemplateID)
	if err != nil {
		return p, err
	}
	p.TemplateHash = t.Hash
	err = tx.QueryRow(ctx, `SELECT fixture_hash FROM adtr.simulation_event_datasets WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, d.DatasetID).Scan(&p.FixtureHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrConfiguration
	}
	if err != nil {
		return p, err
	}
	_, _, err = loadPinned(ctx, tx, s, p)
	return p, err
}

const selectSchedule = `SELECT s.schedule_id,s.domain_id,s.revision,s.control_version,s.state,s.error_code,r.definition,r.payload,r.anchor,s.next_index,s.next_at,s.created_at,s.updated_at FROM adtr.simulation_detection_schedules s JOIN adtr.simulation_detection_schedule_revisions r USING(tenant_id,domain_id,actor_id,schedule_id,revision)`

func scan(row pgx.Row) (Schedule, error) {
	var s Schedule
	var d, p []byte
	err := row.Scan(&s.ID, &s.DomainID, &s.Revision, &s.ControlVersion, &s.State, &s.Error, &d, &p, &s.Anchor, &s.NextIndex, &s.NextAt, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	if json.Unmarshal(d, &s.Definition) != nil || json.Unmarshal(p, &s.Payload) != nil {
		return s, ErrConfiguration
	}
	s.Simulation = true
	return s, nil
}
func get(ctx context.Context, tx pgx.Tx, s Scope, id string, lock bool) (Schedule, error) {
	if !valid(id) {
		return Schedule{}, ErrInvalid
	}
	q := selectSchedule + ` WHERE s.tenant_id=$1 AND s.domain_id=$2 AND s.actor_id=$3 AND s.schedule_id=$4`
	if lock {
		q += ` FOR UPDATE OF s`
	}
	out, e := scan(tx.QueryRow(ctx, q, s.Tenant, s.Domain, s.ActorID, id))
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrNotFound
	}
	return out, e
}
func appendEvent(ctx context.Context, tx pgx.Tx, s Scope, v Schedule, action string, first, last *int64, task string) error {
	_, e := tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_schedule_events(tenant_id,domain_id,actor_id,schedule_id,revision,action,first_index,last_index,task_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, s.Tenant, s.Domain, s.ActorID, v.ID, v.Revision, action, first, last, task)
	return e
}
func replay(ctx context.Context, tx pgx.Tx, s Scope, key, hash string) (Change, bool, error) {
	var h string
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT request_hash,receipt FROM adtr.simulation_detection_schedule_operations WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4`, s.Tenant, s.Domain, s.ActorID, key).Scan(&h, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return Change{}, false, nil
	}
	if e != nil {
		return Change{}, false, e
	}
	if h != hash {
		return Change{}, false, ErrConflict
	}
	var r Receipt
	if json.Unmarshal(raw, &r) != nil {
		return Change{}, false, ErrConfiguration
	}
	v, e := get(ctx, tx, s, r.ScheduleID, false)
	return Change{Receipt: r, Schedule: v, Replayed: true}, true, e
}
func save(ctx context.Context, tx pgx.Tx, s Scope, v Schedule, action, key, hash string, audit AuditFunc) (Change, error) {
	if audit == nil {
		return Change{}, ErrDenied
	}
	r := Receipt{OperationID: key, Action: action, ScheduleID: v.ID, Revision: v.Revision, ControlVersion: v.ControlVersion, State: v.State}
	b, _ := json.Marshal(r)
	if _, e := tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_schedule_operations(tenant_id,domain_id,actor_id,operation_id,request_hash,receipt) VALUES($1,$2,$3,$4,$5,$6)`, s.Tenant, s.Domain, s.ActorID, key, hash, b); e != nil {
		return Change{}, e
	}
	if e := appendEvent(ctx, tx, s, v, action, nil, nil, ""); e != nil {
		return Change{}, e
	}
	if e := audit(ctx, tx, Audit{Action: action, ObjectID: v.ID, OperationID: key, Version: v.ControlVersion}); e != nil {
		return Change{}, e
	}
	return Change{Receipt: r, Schedule: v}, nil
}
func insertRevision(ctx context.Context, tx pgx.Tx, s Scope, v Schedule) error {
	d, _ := json.Marshal(v.Definition)
	p, _ := json.Marshal(v.Payload)
	_, e := tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_schedule_revisions(tenant_id,domain_id,actor_id,schedule_id,revision,definition,payload,anchor) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, s.Tenant, s.Domain, s.ActorID, v.ID, v.Revision, d, p, v.Anchor)
	return e
}
func (e *Engine) CreateTx(ctx context.Context, tx pgx.Tx, s Scope, in Mutation, audit AuditFunc) (Change, error) {
	if !valid(in.OperationID) || in.ID != "" || in.ExpectedControlVersion != 0 || audit == nil {
		return Change{}, ErrInvalid
	}
	if er := validateDefinition(in.Definition); er != nil {
		return Change{}, er
	}
	epoch, er := e.auth(ctx, tx, s, tasks.Write)
	if er != nil {
		return Change{}, er
	}
	h := digest(struct {
		Action string
		Input  Mutation
	}{"create", in})
	if out, ok, er := replay(ctx, tx, s, in.OperationID, h); er != nil || ok {
		return out, er
	}
	// Actor lock acquired by authorizer serializes per-owner capacity and operations.
	var count int
	if er = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_detection_schedules WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3`, s.Tenant, s.Domain, s.ActorID).Scan(&count); er != nil {
		return Change{}, er
	}
	if count >= MaxSchedules {
		return Change{}, ErrLimit
	}
	p, er := pin(ctx, tx, s, in.Definition)
	if er != nil {
		return Change{}, er
	}
	id, er := newID()
	if er != nil {
		return Change{}, er
	}
	var now time.Time
	if er = tx.QueryRow(ctx, `SELECT date_trunc('second',clock_timestamp())`).Scan(&now); er != nil {
		return Change{}, er
	}
	next, er := CalendarAt(now, in.Cycle, 1)
	if er != nil {
		return Change{}, er
	}
	v := Schedule{ID: id, DomainID: s.Domain, Revision: 1, ControlVersion: 1, State: "paused", Definition: in.Definition, Payload: p, Anchor: now, NextIndex: 1, NextAt: next}
	_, er = tx.Exec(ctx, `INSERT INTO adtr.simulation_detection_schedules(tenant_id,domain_id,actor_id,schedule_id,revision,control_version,authorization_version,state,next_index,next_at) VALUES($1,$2,$3,$4,1,1,$5,'paused',1,$6)`, s.Tenant, s.Domain, s.ActorID, id, epoch, next)
	if er != nil {
		return Change{}, er
	}
	if er = insertRevision(ctx, tx, s, v); er != nil {
		return Change{}, er
	}
	v, er = get(ctx, tx, s, id, false)
	if er != nil {
		return Change{}, er
	}
	return save(ctx, tx, s, v, "created", in.OperationID, h, audit)
}
func (e *Engine) UpdateTx(ctx context.Context, tx pgx.Tx, s Scope, in Mutation, audit AuditFunc) (Change, error) {
	if !valid(in.OperationID) || !valid(in.ID) || in.ExpectedControlVersion < 1 || audit == nil {
		return Change{}, ErrInvalid
	}
	if er := validateDefinition(in.Definition); er != nil {
		return Change{}, er
	}
	epoch, er := e.auth(ctx, tx, s, tasks.Write)
	if er != nil {
		return Change{}, er
	}
	h := digest(struct {
		Action string
		Input  Mutation
	}{"edit", in})
	if out, ok, er := replay(ctx, tx, s, in.OperationID, h); er != nil || ok {
		return out, er
	}
	v, er := get(ctx, tx, s, in.ID, true)
	if er != nil {
		return Change{}, er
	}
	if er = checkMutable(ctx, tx, s, v, epoch, in.ExpectedControlVersion); er != nil {
		return Change{}, er
	}
	if v.Revision >= MaxRevisions {
		return Change{}, ErrLimit
	}
	p, er := pin(ctx, tx, s, in.Definition)
	if er != nil {
		return Change{}, er
	}
	var now time.Time
	if er = tx.QueryRow(ctx, `SELECT date_trunc('second',clock_timestamp())`).Scan(&now); er != nil {
		return Change{}, er
	}
	next, er := CalendarAt(now, in.Cycle, 1)
	if er != nil {
		return Change{}, er
	}
	v.Revision++
	v.Definition = in.Definition
	v.Payload = p
	v.Anchor = now
	if er = insertRevision(ctx, tx, s, v); er != nil {
		return Change{}, er
	}
	_, er = tx.Exec(ctx, `UPDATE adtr.simulation_detection_schedules SET revision=$1,control_version=control_version+1,state='paused',error_code='',next_index=1,next_at=$2,updated_at=clock_timestamp() WHERE schedule_id=$3`, v.Revision, next, v.ID)
	if er != nil {
		return Change{}, er
	}
	v, er = get(ctx, tx, s, v.ID, false)
	if er != nil {
		return Change{}, er
	}
	return save(ctx, tx, s, v, "edited", in.OperationID, h, audit)
}
func checkMutable(ctx context.Context, tx pgx.Tx, s Scope, v Schedule, epoch string, expected int64) error {
	if v.ControlVersion != expected || v.State == "archived" || v.State == "authorization_blocked" {
		return ErrConflict
	}
	var original string
	if er := tx.QueryRow(ctx, `SELECT authorization_version FROM adtr.simulation_detection_schedules WHERE schedule_id=$1 AND tenant_id=$2 AND domain_id=$3 AND actor_id=$4`, v.ID, s.Tenant, s.Domain, s.ActorID).Scan(&original); er != nil {
		return er
	}
	if epoch != original {
		return ErrDenied
	}
	return nil
}
func (e *Engine) ControlTx(ctx context.Context, tx pgx.Tx, s Scope, in ControlInput, audit AuditFunc) (Change, error) {
	action := map[string]string{"enable": "enabled", "pause": "paused", "archive": "archived"}[in.Action]
	if action == "" || !valid(in.ID) || !valid(in.OperationID) || in.ExpectedControlVersion < 1 || audit == nil {
		return Change{}, ErrInvalid
	}
	epoch, er := e.auth(ctx, tx, s, tasks.Write)
	if er != nil {
		return Change{}, er
	}
	h := digest(in)
	if out, ok, er := replay(ctx, tx, s, in.OperationID, h); er != nil || ok {
		return out, er
	}
	v, er := get(ctx, tx, s, in.ID, true)
	if er != nil {
		return Change{}, er
	}
	if er = checkMutable(ctx, tx, s, v, epoch, in.ExpectedControlVersion); er != nil {
		return Change{}, er
	}
	if in.Action == "enable" {
		if v.State == "configuration_blocked" {
			return Change{}, ErrConfiguration
		}
		if _, _, er = loadPinned(ctx, tx, s, v.Payload); er != nil {
			return Change{}, er
		}
		var now time.Time
		if er = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); er != nil {
			return Change{}, er
		}
		due, err := latestIndex(v.Anchor, v.Cycle, now)
		if err != nil {
			return Change{}, err
		}
		if v.State != "enabled" && due >= v.NextIndex {
			first, last := v.NextIndex, due
			if er = appendEvent(ctx, tx, s, v, "skipped_paused", &first, &last, ""); er != nil {
				return Change{}, er
			}
			v.NextIndex = due + 1
			v.NextAt, er = CalendarAt(v.Anchor, v.Cycle, v.NextIndex)
			if er != nil {
				return Change{}, er
			}
		}
	}
	_, er = tx.Exec(ctx, `UPDATE adtr.simulation_detection_schedules SET state=$1,control_version=control_version+1,next_index=$2,next_at=$3,updated_at=clock_timestamp() WHERE schedule_id=$4`, action, v.NextIndex, v.NextAt, v.ID)
	if er != nil {
		return Change{}, er
	}
	v, er = get(ctx, tx, s, v.ID, false)
	if er != nil {
		return Change{}, er
	}
	return save(ctx, tx, s, v, action, in.OperationID, h, audit)
}
func page(f Filter) (Filter, error) {
	if f.PageIdx < 1 || f.PageSize < 1 || f.PageSize > 50 || f.PageIdx > 10000 {
		return f, ErrInvalid
	}
	return f, nil
}
func (e *Engine) ListTx(ctx context.Context, tx pgx.Tx, s Scope, f Filter) (List, error) {
	if _, er := page(f); er != nil {
		return List{}, er
	}
	if _, er := e.auth(ctx, tx, s, tasks.Read); er != nil {
		return List{}, er
	}
	out := List{Schedules: []Schedule{}}
	var total int64
	if er := tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_detection_schedules WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3`, s.Tenant, s.Domain, s.ActorID).Scan(&total); er != nil {
		return out, er
	}
	out.Page = tasks.Page{Index: f.PageIdx, Size: f.PageSize, Total: int(total), Pages: int((total + int64(f.PageSize) - 1) / int64(f.PageSize))}
	rows, er := tx.Query(ctx, selectSchedule+` WHERE s.tenant_id=$1 AND s.domain_id=$2 AND s.actor_id=$3 ORDER BY s.created_at DESC,s.schedule_id LIMIT $4 OFFSET $5`, s.Tenant, s.Domain, s.ActorID, f.PageSize, (f.PageIdx-1)*f.PageSize)
	if er != nil {
		return out, er
	}
	defer rows.Close()
	for rows.Next() {
		v, er := scan(rows)
		if er != nil {
			return out, er
		}
		out.Schedules = append(out.Schedules, v)
	}
	return out, rows.Err()
}
func (e *Engine) DetailTx(ctx context.Context, tx pgx.Tx, s Scope, id string, pageIdx, pageSize int) (Detail, error) {
	if _, er := page(Filter{pageIdx, pageSize}); er != nil {
		return Detail{}, er
	}
	if _, er := e.auth(ctx, tx, s, tasks.Read); er != nil {
		return Detail{}, er
	}
	v, er := get(ctx, tx, s, id, false)
	if er != nil {
		return Detail{}, er
	}
	out := Detail{Schedule: v, Events: []Event{}}
	rows, er := tx.Query(ctx, `SELECT id,revision,action,first_index,last_index,task_id,created_at FROM adtr.simulation_detection_schedule_events WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND schedule_id=$4 ORDER BY id DESC LIMIT $5 OFFSET $6`, s.Tenant, s.Domain, s.ActorID, id, pageSize+1, (pageIdx-1)*pageSize)
	if er != nil {
		return out, er
	}
	defer rows.Close()
	for rows.Next() {
		var ev Event
		if er = rows.Scan(&ev.ID, &ev.Revision, &ev.Action, &ev.FirstIndex, &ev.LastIndex, &ev.TaskID, &ev.CreatedAt); er != nil {
			return out, er
		}
		out.Events = append(out.Events, ev)
	}
	if len(out.Events) > pageSize {
		out.HasMore = true
		out.Events = out.Events[:pageSize]
	}
	return out, rows.Err()
}
func (e *Engine) ReadRunTx(ctx context.Context, tx pgx.Tx, s Scope, taskID string) (tasks.Task, error) {
	if !valid(taskID) {
		return tasks.Task{}, ErrInvalid
	}
	if _, er := e.auth(ctx, tx, s, tasks.Read); er != nil {
		return tasks.Task{}, er
	}
	var exists bool
	er := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adtr.simulation_detection_schedule_intents WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND task_id=$4)`, s.Tenant, s.Domain, s.ActorID, taskID).Scan(&exists)
	if er != nil {
		return tasks.Task{}, er
	}
	if !exists {
		return tasks.Task{}, ErrNotFound
	}
	v, er := e.queue.DetailTx(ctx, tx, tasks.Principal{TenantID: s.Tenant, ActorID: s.ActorID}, taskID)
	if er != nil {
		return tasks.Task{}, er
	}
	if v.Task.Kind != KindName || v.Task.DomainID != s.Domain || v.Task.ActorID != s.ActorID {
		return tasks.Task{}, ErrNotFound
	}
	return v.Task, nil
}

// ReceiptTx returns the immutable receipt alongside current state; an earlier
// enable receipt never reports a later paused schedule as enabled.
func (e *Engine) ReceiptTx(ctx context.Context, tx pgx.Tx, s Scope, operationID string) (Change, error) {
	if !valid(operationID) {
		return Change{}, ErrInvalid
	}
	if _, err := e.auth(ctx, tx, s, tasks.Read); err != nil {
		return Change{}, err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT receipt FROM adtr.simulation_detection_schedule_operations WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND operation_id=$4`, s.Tenant, s.Domain, s.ActorID, operationID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Change{}, ErrNotFound
	}
	if err != nil {
		return Change{}, err
	}
	var r Receipt
	if json.Unmarshal(raw, &r) != nil {
		return Change{}, ErrConfiguration
	}
	v, err := get(ctx, tx, s, r.ScheduleID, false)
	return Change{Receipt: r, Schedule: v, Replayed: true}, err
}
