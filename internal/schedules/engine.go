package schedules

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var timestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$`)

type Engine struct {
	database  *pgx.ConnConfig
	queue     *tasks.Engine
	authorize tasks.Authorizer
}

func New(config *pgx.ConnConfig, queue *tasks.Engine, authorize tasks.Authorizer) (*Engine, error) {
	if config == nil || queue == nil || authorize == nil {
		return nil, errors.New("schedules require database, task engine and live authorizer")
	}
	return &Engine{database: config.Copy(), queue: queue, authorize: authorize}, nil
}

func (e *Engine) CheckSchemaTx(ctx context.Context, tx pgx.Tx) error {
	return tasks.CheckSchemaTx(ctx, tx, SchemaVersion)
}

func (e *Engine) requireSchemaTx(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return tasks.ErrSchemaGateRequired
	}
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode IN ('ShareLock','ExclusiveLock'))`).Scan(&held); err != nil {
		return err
	}
	if !held {
		return tasks.ErrSchemaGateRequired
	}
	var version int
	if err := tx.QueryRow(ctx, `SELECT version FROM adtr.schema_version WHERE singleton=true FOR SHARE`).Scan(&version); err != nil {
		return err
	}
	if version != SchemaVersion {
		return tasks.ErrSchemaIncompatible
	}
	return nil
}

func problem(status int, code string) error { return &tasks.Error{Status: status, Code: code} }
func denied(err error) bool {
	var p *tasks.Error
	return errors.Is(err, tasks.ErrAuthorization) || errors.As(err, &p) && p.Status == 403
}
func (e *Engine) auth(ctx context.Context, tx pgx.Tx, p tasks.Principal, action tasks.Action) (string, error) {
	if !identifier.MatchString(p.TenantID) || p.ActorID <= 0 {
		return "", tasks.ErrAuthorization
	}
	epoch, err := e.authorize(ctx, tx, p, tasks.Scope{DomainID: "platform", TaskName: HealthKind, Platform: true}, action)
	if err == nil && epoch == "" {
		return "", tasks.ErrAuthorization
	}
	return epoch, err
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func validateCreate(in CreateInput) (CreateInput, time.Time, error) {
	if in.TaskName != HealthKind || in.DomainID != "platform" || in.PayloadVersion != 1 {
		return in, time.Time{}, problem(400, "unsupported_schedule_kind")
	}
	if !utf8.ValidString(in.Label) || strings.TrimSpace(in.Label) != in.Label || utf8.RuneCountInString(in.Label) < 1 || utf8.RuneCountInString(in.Label) > 80 {
		return in, time.Time{}, problem(400, "invalid_input")
	}
	for _, c := range in.Label {
		if unicode.IsControl(c) {
			return in, time.Time{}, problem(400, "invalid_input")
		}
	}
	if !identifier.MatchString(in.IdempotencyKey) || in.IntervalSeconds < MinIntervalSeconds || in.IntervalSeconds > MaxIntervalSeconds {
		return in, time.Time{}, problem(400, "invalid_input")
	}
	var payload map[string]json.RawMessage
	if !utf8.Valid(in.Payload) || len(in.Payload) > 65536 || json.Unmarshal(in.Payload, &payload) != nil || payload == nil || len(payload) != 0 {
		return in, time.Time{}, problem(400, "invalid_input")
	}
	if !timestamp.MatchString(in.StartAt) {
		return in, time.Time{}, problem(400, "invalid_input")
	}
	if !strings.HasSuffix(in.StartAt, "Z") {
		offset := in.StartAt[len(in.StartAt)-6:]
		hour, _ := strconv.Atoi(offset[1:3])
		minute, _ := strconv.Atoi(offset[4:6])
		if hour > 23 || minute > 59 {
			return in, time.Time{}, problem(400, "invalid_input")
		}
	}
	anchor, err := time.Parse(time.RFC3339, in.StartAt)
	if err != nil || anchor.Year() < 1 || anchor.UTC().Year() > 9999 || anchor.UTC().Year() < 1 {
		return in, time.Time{}, problem(400, "invalid_input")
	}
	in.StartAt = anchor.UTC().Format(time.RFC3339)
	in.Payload = json.RawMessage(`{}`)
	return in, anchor.UTC(), nil
}

// Grid arithmetic uses integral Unix seconds rather than time.Duration, which
// silently saturates when a downtime interval spans more than about 290 years.
func gridAt(anchor time.Time, interval, index int64) (time.Time, error) {
	if interval < MinIntervalSeconds || interval > MaxIntervalSeconds || index < 0 || anchor.Year() < 1 || anchor.Year() > 9999 || index > (253402300799-anchor.Unix())/interval {
		return time.Time{}, problem(409, "schedule_time_overflow")
	}
	seconds := anchor.Unix() + interval*index
	if seconds > 253402300799 || seconds < -62135596800 {
		return time.Time{}, problem(409, "schedule_time_overflow")
	}
	return time.Unix(seconds, 0).UTC(), nil
}
func latestDue(anchor, now time.Time, interval int64) int64 {
	if now.Before(anchor) {
		return -1
	}
	return (now.Unix() - anchor.Unix()) / interval
}

const columns = `schedule_id,label,kind,domain_id,payload_version,start_at,interval_seconds,state,control_version,next_index,next_at,last_task_id,last_scheduled_at,error_code,created_at,updated_at,tenant_id,actor_id,authorization_version,payload,definition_hash,creation_key`

func scan(row pgx.Row) (Schedule, error) {
	var s Schedule
	var next time.Time
	err := row.Scan(&s.ID, &s.Label, &s.TaskName, &s.DomainID, &s.PayloadVersion, &s.StartAt, &s.IntervalSeconds, &s.State, &s.ControlVersion, &s.NextIndex, &next, &s.LastTaskID, &s.LastScheduledAt, &s.Error, &s.CreatedAt, &s.UpdatedAt, &s.TenantID, &s.ActorID, &s.AuthorizationVersion, &s.Payload, &s.DefinitionHash, &s.CreationKey)
	s.StartAt = s.StartAt.UTC()
	s.CreatedAt = s.CreatedAt.UTC()
	s.UpdatedAt = s.UpdatedAt.UTC()
	if s.State == Enabled {
		next = next.UTC()
		s.NextAt = &next
	}
	if s.LastScheduledAt != nil {
		v := s.LastScheduledAt.UTC()
		s.LastScheduledAt = &v
	}
	return s, err
}
func byID(ctx context.Context, tx pgx.Tx, p tasks.Principal, id string, lock bool) (Schedule, error) {
	if !identifier.MatchString(id) {
		return Schedule{}, problem(400, "invalid_input")
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	s, err := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM adtr.task_schedules WHERE tenant_id=$1 AND actor_id=$2 AND schedule_id=$3"+suffix, p.TenantID, p.ActorID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return s, problem(404, "not_found")
	}
	return s, err
}
func appendEvent(ctx context.Context, tx pgx.Tx, s Schedule, action string, first, last *int64, taskID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO adtr.task_schedule_events(schedule_id,tenant_id,actor_id,authorization_version,action,first_index,last_index,task_id,control_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, s.ID, s.TenantID, s.ActorID, s.AuthorizationVersion, action, first, last, taskID, s.ControlVersion)
	return err
}

func (e *Engine) CreateTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, input CreateInput) (Creation, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Creation{}, err
	}
	in, anchor, err := validateCreate(input)
	if err != nil {
		return Creation{}, err
	}
	epoch, err := e.auth(ctx, tx, p, tasks.Write)
	if err != nil {
		return Creation{}, err
	}
	definitionHash := hash(in)
	old, err := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM adtr.task_schedules WHERE tenant_id=$1 AND actor_id=$2 AND creation_key=$3", p.TenantID, p.ActorID, in.IdempotencyKey))
	if err == nil {
		if old.DefinitionHash != definitionHash {
			return Creation{}, problem(409, "idempotency_conflict")
		}
		if old.AuthorizationVersion != epoch {
			return Creation{}, problem(409, "authorization_epoch_changed")
		}
		return Creation{Schedule: old, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Creation{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return Creation{}, err
	}
	if anchor.Before(now.Add(60 * time.Second)) {
		return Creation{}, problem(400, "invalid_start_at")
	}
	id, err := newID()
	if err != nil {
		return Creation{}, err
	}
	s, err := scan(tx.QueryRow(ctx, `INSERT INTO adtr.task_schedules(schedule_id,tenant_id,actor_id,authorization_version,label,kind,domain_id,payload_version,payload,start_at,interval_seconds,definition_hash,creation_key,next_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$10) RETURNING `+columns, id, p.TenantID, p.ActorID, epoch, in.Label, in.TaskName, in.DomainID, in.PayloadVersion, in.Payload, anchor, in.IntervalSeconds, definitionHash, in.IdempotencyKey))
	if err != nil {
		return Creation{}, err
	}
	if err = appendEvent(ctx, tx, s, "created", nil, nil, ""); err != nil {
		return Creation{}, err
	}
	return Creation{Schedule: s}, nil
}

func validateFilter(f Filter) (Filter, error) {
	if f.PageIdx == 0 {
		f.PageIdx = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.PageIdx < 1 || f.PageIdx > 1000000 || f.PageSize < 1 || f.PageSize > 100 || f.State != "" && !f.State.Valid() {
		return f, problem(400, "invalid_input")
	}
	return f, nil
}
func page(f Filter, total int) tasks.Page {
	return tasks.Page{Index: f.PageIdx, Size: f.PageSize, Total: total, Pages: (total + f.PageSize - 1) / f.PageSize}
}
func (e *Engine) ListTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, f Filter) (List, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return List{}, err
	}
	f, err := validateFilter(f)
	if err != nil {
		return List{}, err
	}
	if _, err = e.auth(ctx, tx, p, tasks.Read); err != nil {
		return List{}, err
	}
	var total int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.task_schedules WHERE tenant_id=$1 AND actor_id=$2 AND ($3='' OR state=$3)`, p.TenantID, p.ActorID, f.State).Scan(&total); err != nil {
		return List{}, err
	}
	rows, err := tx.Query(ctx, "SELECT "+columns+` FROM adtr.task_schedules WHERE tenant_id=$1 AND actor_id=$2 AND ($3='' OR state=$3) ORDER BY created_at DESC,schedule_id DESC LIMIT $4 OFFSET $5`, p.TenantID, p.ActorID, f.State, f.PageSize, (f.PageIdx-1)*f.PageSize)
	if err != nil {
		return List{}, err
	}
	defer rows.Close()
	result := List{Page: page(f, total), Schedules: []Schedule{}, Exhausted: f.PageIdx*f.PageSize >= total}
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return List{}, err
		}
		result.Schedules = append(result.Schedules, s)
	}
	return result, rows.Err()
}

func (e *Engine) DetailTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, id string, f Filter) (Detail, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Detail{}, err
	}
	f, err := validateFilter(f)
	if err != nil || f.State != "" {
		return Detail{}, problem(400, "invalid_input")
	}
	if _, err = e.auth(ctx, tx, p, tasks.Read); err != nil {
		return Detail{}, err
	}
	s, err := byID(ctx, tx, p, id, false)
	if err != nil {
		return Detail{}, err
	}
	var total int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.task_schedule_events WHERE tenant_id=$1 AND schedule_id=$2`, p.TenantID, id).Scan(&total); err != nil {
		return Detail{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,action,first_index,last_index,task_id,control_version,occurred_at FROM adtr.task_schedule_events WHERE tenant_id=$1 AND schedule_id=$2 ORDER BY id DESC LIMIT $3 OFFSET $4`, p.TenantID, id, f.PageSize, (f.PageIdx-1)*f.PageSize)
	if err != nil {
		return Detail{}, err
	}
	defer rows.Close()
	out := Detail{Schedule: s, Page: page(f, total), Events: []Event{}, Exhausted: f.PageIdx*f.PageSize >= total}
	for rows.Next() {
		var v Event
		if err = rows.Scan(&v.ID, &v.Action, &v.FirstIndex, &v.LastIndex, &v.TaskID, &v.ControlVersion, &v.CreatedAt); err != nil {
			return Detail{}, err
		}
		v.CreatedAt = v.CreatedAt.UTC()
		if v.FirstIndex != nil && v.LastIndex != nil {
			first, er := gridAt(s.StartAt, s.IntervalSeconds, *v.FirstIndex)
			if er != nil {
				return Detail{}, er
			}
			last, er := gridAt(s.StartAt, s.IntervalSeconds, *v.LastIndex)
			if er != nil {
				return Detail{}, er
			}
			count := *v.LastIndex - *v.FirstIndex + 1
			v.FirstAt = &first
			v.LastAt = &last
			v.Count = &count
		}
		out.Events = append(out.Events, v)
	}
	return out, rows.Err()
}
