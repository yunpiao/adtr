package tasks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type Engine struct {
	database              *pgx.ConnConfig
	registry              *Registry
	authorize             Authorizer
	expectedSchemaVersion int
	quiescenceMu          sync.Mutex
	quiescenceOwners      map[string]*quiescenceSlot
	quiescenceCursor      string
}

func New(config *pgx.ConnConfig, registry *Registry, authorize Authorizer, expectedSchemaVersion int) (*Engine, error) {
	if config == nil || registry == nil || len(registry.kinds) == 0 || authorize == nil || expectedSchemaVersion <= 0 {
		return nil, errors.New("task engine requires database, explicit kinds, live authorizer and exact schema version")
	}
	return &Engine{database: config.Copy(), registry: registry, authorize: authorize, expectedSchemaVersion: expectedSchemaVersion}, nil
}
func (e *Engine) Kinds() []KindInfo { return e.registry.Kinds() }
func principalOK(p Principal) bool  { return identifier.MatchString(p.TenantID) && p.ActorID > 0 }
func denied(err error) bool {
	var p *Error
	return errors.Is(err, ErrAuthorization) || errors.As(err, &p) && p.Status == 403
}
func (e *Engine) auth(ctx context.Context, tx pgx.Tx, p Principal, k Kind, domain string, action Action) (string, error) {
	if !principalOK(p) {
		return "", problem(403, "forbidden")
	}
	if !identifier.MatchString(domain) || (k.Platform && domain != "platform") || (!k.Platform && domain == "platform") {
		return "", problem(400, "invalid_domain")
	}
	version, err := e.authorize(ctx, tx, p, k.scope(domain), action)
	if err == nil && version == "" {
		return "", ErrAuthorization
	}
	return version, err
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

const taskColumns = `task_id,kind,domain_id,payload_version,state,error_code,created_at,updated_at,attempt,max_attempts,progress,result_version,result,cursor,parent_task_id,next_attempt_at,tenant_id,actor_id,authorization_version,payload,payload_hash,idempotency_key,lease_owner,lease_until,fencing_token,terminal_at`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Kind, &t.DomainID, &t.PayloadVersion, &t.State, &t.Error, &t.CreatedAt, &t.UpdatedAt, &t.Attempt, &t.MaxAttempts, &t.Progress, &t.ResultVersion, &t.Result, &t.Cursor, &t.ParentID, &t.NextAttemptAt, &t.TenantID, &t.ActorID, &t.AuthorizationVersion, &t.Payload, &t.PayloadHash, &t.IdempotencyKey, &t.LeaseOwner, &t.LeaseUntil, &t.FencingToken, &t.TerminalAt)
	t.SourceState = t.State.Source()
	if t.TerminalAt != nil {
		v := t.TerminalAt.UTC()
		t.TerminalAt = &v
	}
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	if t.NextAttemptAt != nil {
		v := t.NextAttemptAt.UTC()
		t.NextAttemptAt = &v
	}
	return t, err
}
func taskByID(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (Task, error) {
	if !identifier.MatchString(id) {
		return Task{}, problem(400, "invalid_task_id")
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	t, err := scanTask(tx.QueryRow(ctx, "SELECT "+taskColumns+" FROM adtr.tasks WHERE tenant_id=$1 AND task_id=$2"+suffix, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		err = problem(404, "not_found")
	}
	return t, err
}
func appendEvent(ctx context.Context, tx pgx.Tx, t Task, actor int64, action string) error {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO adtr.task_events(task_id,tenant_id,domain_id,actor_id,action,state,attempt,result_version,fencing_token,authorization_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, t.ID, t.TenantID, t.DomainID, actor, action, t.State, t.Attempt, t.ResultVersion, t.FencingToken, t.AuthorizationVersion).Scan(&id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO adtr.task_outbox(task_id,event_id) VALUES($1,$2)", t.ID, id)
	return err
}
func (e *Engine) SubmitTx(ctx context.Context, tx pgx.Tx, p Principal, in SubmitInput) (Submission, error) {
	if strings.HasPrefix(in.IdempotencyKey, "schedule_") {
		return Submission{}, problem(400, "reserved_idempotency_key")
	}
	return e.submit(ctx, tx, p, in, "")
}
func (e *Engine) submit(ctx context.Context, tx pgx.Tx, p Principal, in SubmitInput, parent string) (Submission, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Submission{}, err
	}
	k, err := e.registry.kind(in.TaskName)
	if err != nil {
		return Submission{}, err
	}
	if in.PayloadVersion != k.Version {
		return Submission{}, problem(400, "unsupported_payload_version")
	}
	if !identifier.MatchString(in.IdempotencyKey) {
		return Submission{}, problem(400, "invalid_idempotency_key")
	}
	raw, err := canonicalObject(in.Payload)
	if err != nil {
		return Submission{}, err
	}
	payload, err := k.Validate(raw)
	if err != nil {
		return Submission{}, err
	}
	payload, err = canonicalObject(payload)
	if err != nil {
		return Submission{}, err
	}
	version, err := e.auth(ctx, tx, p, k, in.DomainID, Write)
	if err != nil {
		return Submission{}, err
	}
	hash := sha256.Sum256(append([]byte(fmt.Sprintf("%d:%s:", k.Version, parent)), payload...))
	hashString := hex.EncodeToString(hash[:])
	id := newID()
	tag, err := tx.Exec(ctx, `INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts,parent_task_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'queued',$11,$12) ON CONFLICT(tenant_id,domain_id,kind,idempotency_key) DO NOTHING`, id, p.TenantID, in.DomainID, k.Name, k.Version, payload, hashString, p.ActorID, version, in.IdempotencyKey, k.MaxAttempts, parent)
	if err != nil {
		return Submission{}, err
	}
	t, err := scanTask(tx.QueryRow(ctx, "SELECT "+taskColumns+" FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND kind=$3 AND idempotency_key=$4", p.TenantID, in.DomainID, k.Name, in.IdempotencyKey))
	if err != nil {
		return Submission{}, err
	}
	if k.OwnerScoped && t.ActorID != p.ActorID {
		return Submission{}, problem(409, "idempotency_conflict")
	}
	if k.OwnerScoped && t.AuthorizationVersion != version {
		return Submission{}, problem(409, "authorization_epoch_changed")
	}
	if t.PayloadHash != hashString {
		return Submission{}, problem(409, "idempotency_conflict")
	}
	replayed := tag.RowsAffected() == 0
	if !replayed {
		action := "submitted"
		if parent != "" {
			action = "recovered"
		}
		if err = appendEvent(ctx, tx, t, p.ActorID, action); err != nil {
			return Submission{}, err
		}
	}
	if err = applyTaskVisibility(ctx, tx, &t); err != nil {
		return Submission{}, err
	}
	return Submission{t, replayed}, nil
}
func (e *Engine) DetailTx(ctx context.Context, tx pgx.Tx, p Principal, id string) (Detail, error) {
	return e.DetailVisibilityTx(ctx, tx, p, id, "active")
}
func (e *Engine) DetailVisibilityTx(ctx context.Context, tx pgx.Tx, p Principal, id, visibility string) (Detail, error) {
	if !validTaskVisibility(visibility) {
		return Detail{}, problem(400, "invalid_visibility")
	}
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Detail{}, err
	}
	t, err := taskByID(ctx, tx, p.TenantID, id, false)
	if err != nil {
		return Detail{}, err
	}
	k, err := e.registry.kind(t.Kind)
	if err != nil {
		return Detail{}, problem(404, "not_found")
	}
	version, err := e.auth(ctx, tx, p, k, t.DomainID, Read)
	if err != nil {
		if denied(err) {
			return Detail{}, problem(404, "not_found")
		}
		return Detail{}, err
	}
	t, err = taskByID(ctx, tx, p.TenantID, id, false)
	if err != nil {
		return Detail{}, err
	}
	if k.OwnerScoped && (t.ActorID != p.ActorID || t.AuthorizationVersion != version) {
		return Detail{}, problem(404, "not_found")
	}
	if err = applyTaskVisibility(ctx, tx, &t); err != nil {
		return Detail{}, err
	}
	if (visibility == "" || visibility == "active") && t.Archived || visibility == "archived" && !t.Archived {
		return Detail{}, problem(404, "not_found")
	}
	rows, err := tx.Query(ctx, "SELECT id,action,state,attempt,result_version,occurred_at FROM adtr.task_events WHERE tenant_id=$1 AND task_id=$2 ORDER BY id LIMIT 1001", p.TenantID, id)
	if err != nil {
		return Detail{}, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var v Event
		if err = rows.Scan(&v.ID, &v.Action, &v.State, &v.Attempt, &v.ResultVersion, &v.CreatedAt); err != nil {
			return Detail{}, err
		}
		v.CreatedAt = v.CreatedAt.UTC()
		events = append(events, v)
	}
	if err = rows.Err(); err != nil {
		return Detail{}, err
	}
	if len(events) > 1000 {
		return Detail{}, problem(422, "result_too_large")
	}
	return Detail{t, events}, nil
}
func (e *Engine) ListTx(ctx context.Context, tx pgx.Tx, p Principal, f Filter) (List, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return List{}, err
	}
	if !principalOK(p) {
		return List{}, problem(403, "forbidden")
	}
	if !validTaskVisibility(f.Visibility) {
		return List{}, problem(400, "invalid_visibility")
	}
	if f.PageIdx == 0 {
		f.PageIdx = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.PageIdx < 1 || f.PageIdx > 1000000 || f.PageSize < 1 || f.PageSize > 100 || f.DomainID != "" && !identifier.MatchString(f.DomainID) || f.State != "" && !f.State.Valid() {
		return List{}, problem(400, "invalid_input")
	}
	if f.TaskName != "" {
		if _, err := e.registry.kind(f.TaskName); err != nil {
			return List{}, err
		}
	}
	// Authorize each existing scope before count/pagination. Unknown kinds stay hidden.
	rows, err := tx.Query(ctx, `SELECT DISTINCT domain_id,kind FROM adtr.tasks WHERE tenant_id=$1 AND ($2='' OR domain_id=$2) AND ($3='' OR kind=$3) ORDER BY domain_id,kind`, p.TenantID, f.DomainID, f.TaskName)
	if err != nil {
		return List{}, err
	}
	scopes := map[string]Scope{}
	for rows.Next() {
		var d, k string
		if err = rows.Scan(&d, &k); err != nil {
			rows.Close()
			return List{}, err
		}
		if kind, ok := e.registry.kinds[k]; ok {
			scopes[d+"\x00"+k] = kind.scope(d)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return List{}, err
	}
	for _, k := range e.registry.kinds {
		if f.DomainID != "" && !k.Platform && f.DomainID != "platform" && (f.TaskName == "" || f.TaskName == k.Name) {
			scopes[f.DomainID+"\x00"+k.Name] = k.scope(f.DomainID)
		}
		if k.Platform && (f.DomainID == "" || f.DomainID == "platform") && (f.TaskName == "" || f.TaskName == k.Name) {
			scopes["platform\x00"+k.Name] = k.scope("platform")
		}
	}
	keys := []string{}
	for key := range scopes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := []string{}
	ownerPairs := []string{}
	ownerKinds := []string{}
	for name, kind := range e.registry.kinds {
		if kind.OwnerScoped {
			ownerKinds = append(ownerKinds, name)
		}
	}
	for _, key := range keys {
		s := scopes[key]
		version, authErr := e.authorize(ctx, tx, p, s, Read)
		err = authErr
		if err == nil {
			pairs = append(pairs, s.DomainID+"/"+s.TaskName)
			if e.registry.kinds[s.TaskName].OwnerScoped {
				ownerPairs = append(ownerPairs, s.DomainID+"/"+s.TaskName+"/"+version)
			}
		} else if !denied(err) {
			return List{}, err
		}
	}
	if len(pairs) == 0 {
		return List{}, problem(403, "forbidden")
	}
	var total int
	where := `tenant_id=$1 AND (domain_id||'/'||kind)=ANY($2::text[]) AND ($3='' OR state=$3) AND (NOT kind=ANY($4::text[]) OR (actor_id=$5 AND (domain_id||'/'||kind||'/'||authorization_version)=ANY($6::text[])))`
	if f.Visibility == "archived" {
		where += ` AND EXISTS(SELECT FROM adtr.task_visibility v WHERE v.tenant_id=adtr.tasks.tenant_id AND v.task_id=adtr.tasks.task_id AND v.archived)`
	} else if f.Visibility != "all" {
		where += ` AND NOT EXISTS(SELECT FROM adtr.task_visibility v WHERE v.tenant_id=adtr.tasks.tenant_id AND v.task_id=adtr.tasks.task_id AND v.archived)`
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM adtr.tasks WHERE "+where, p.TenantID, pairs, string(f.State), ownerKinds, p.ActorID, ownerPairs).Scan(&total); err != nil {
		return List{}, err
	}
	offset := (f.PageIdx - 1) * f.PageSize
	rows, err = tx.Query(ctx, "SELECT "+taskColumns+" FROM adtr.tasks WHERE "+where+" ORDER BY created_at DESC,task_id DESC LIMIT $7 OFFSET $8", p.TenantID, pairs, string(f.State), ownerKinds, p.ActorID, ownerPairs, f.PageSize, offset)
	if err != nil {
		return List{}, err
	}
	defer rows.Close()
	out := List{Page: Page{f.PageIdx, f.PageSize, total, (total + f.PageSize - 1) / f.PageSize}, Tasks: []Task{}}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return List{}, err
		}
		out.Tasks = append(out.Tasks, t)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	if err = applyTaskListVisibility(ctx, tx, out.Tasks); err != nil {
		return out, err
	}
	out.Exhausted = offset+len(out.Tasks) >= total
	return out, nil
}
func (e *Engine) CancelTx(ctx context.Context, tx pgx.Tx, p Principal, id string) (Task, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Task{}, err
	}
	// Read scope without a task lock, acquire authorization lock, then lock/recheck task.
	t, err := taskByID(ctx, tx, p.TenantID, id, false)
	if err != nil {
		return Task{}, err
	}
	k, err := e.registry.kind(t.Kind)
	if err != nil {
		return Task{}, err
	}
	if k.OwnerScoped && t.ActorID != p.ActorID {
		return Task{}, problem(404, "not_found")
	}
	if _, err = e.auth(ctx, tx, p, k, t.DomainID, Write); err != nil {
		if denied(err) {
			return Task{}, problem(404, "not_found")
		}
		return Task{}, err
	}
	t, err = taskByID(ctx, tx, p.TenantID, id, true)
	if err != nil {
		return Task{}, err
	}
	if err = applyTaskVisibility(ctx, tx, &t); err != nil {
		return Task{}, err
	}
	if t.Archived {
		return Task{}, problem(409, "task_archived")
	}
	if t.State == CancelRequested || t.State == Cancelled {
		return t, nil
	}
	if t.State.Terminal() {
		return Task{}, problem(409, "task_terminal")
	}
	state := Cancelled
	if t.State == Running {
		state = CancelRequested
	}
	query := `UPDATE adtr.tasks SET state=$3,error_code=CASE WHEN $3='cancelled' THEN 'cancelled' ELSE error_code END,updated_at=clock_timestamp(),result_version=result_version+1,lease_owner=CASE WHEN $3='cancelled' THEN '' ELSE lease_owner END,lease_until=CASE WHEN $3='cancelled' THEN NULL ELSE lease_until END,next_attempt_at=NULL WHERE tenant_id=$1 AND task_id=$2 RETURNING ` + taskColumns
	t, err = scanTask(tx.QueryRow(ctx, query, p.TenantID, id, string(state)))
	if err != nil {
		return Task{}, err
	}
	if err = appendEvent(ctx, tx, t, p.ActorID, "cancel_requested"); err != nil {
		return Task{}, err
	}
	return t, nil
}
func (e *Engine) RecoverTx(ctx context.Context, tx pgx.Tx, p Principal, id, key string) (Submission, error) {
	if strings.HasPrefix(key, "schedule_") {
		return Submission{}, problem(400, "reserved_idempotency_key")
	}
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Submission{}, err
	}
	t, err := taskByID(ctx, tx, p.TenantID, id, false)
	if err != nil {
		return Submission{}, err
	}
	k, err := e.registry.kind(t.Kind)
	if err != nil {
		return Submission{}, err
	}
	if k.OwnerScoped && t.ActorID != p.ActorID {
		return Submission{}, problem(404, "not_found")
	}
	if _, err = e.auth(ctx, tx, p, k, t.DomainID, Write); err != nil {
		if denied(err) {
			return Submission{}, problem(404, "not_found")
		}
		return Submission{}, err
	}
	if k.SingleAttemptOnly {
		return Submission{}, problem(409, "task_not_recoverable")
	}
	if err = applyTaskVisibility(ctx, tx, &t); err != nil {
		return Submission{}, err
	}
	if t.Archived {
		return Submission{}, problem(409, "task_archived")
	}
	if t.State != DeadLetter && t.State != Failed {
		return Submission{}, problem(409, "task_not_recoverable")
	}
	return e.submit(ctx, tx, p, SubmitInput{t.Kind, t.DomainID, t.PayloadVersion, t.Payload, key}, t.ID)
}
func (e *Engine) transaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := operationContext(ctx)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, e.database.Copy())
	if err != nil {
		return err
	}
	defer closeTaskConnection(conn)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTaskTransaction(tx)
	if err = e.CheckSchemaTx(ctx, tx); err != nil {
		return err
	}
	if err = fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (e *Engine) probe(ctx context.Context) error {
	return e.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var present bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM information_schema.tables WHERE table_schema='adtr' AND table_name='tasks')").Scan(&present); err != nil {
			return err
		}
		if !present {
			return ErrSchemaIncompatible
		}
		return nil
	})
}

// SubmitScheduledTx atomically binds one immutable schedule occurrence to one
// logical task. A caller must roll back on every error, as for all Tx methods.
func (e *Engine) SubmitScheduledTx(ctx context.Context, tx pgx.Tx, p Principal, in ScheduledInput) (Submission, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Submission{}, err
	}
	if !identifier.MatchString(in.ScheduleID) || in.ScheduledAt.IsZero() || in.ExpectedAuthorizationVersion == "" {
		return Submission{}, problem(400, "invalid_schedule_occurrence")
	}
	k, err := e.registry.kind(in.TaskName)
	if err != nil {
		return Submission{}, err
	}
	if !k.Schedulable {
		return Submission{}, problem(400, "task_not_schedulable")
	}
	version, err := e.auth(ctx, tx, p, k, in.DomainID, Write)
	if err != nil {
		return Submission{}, err
	}
	if version != in.ExpectedAuthorizationVersion {
		return Submission{}, ErrAuthorization
	}
	when := in.ScheduledAt.UTC().Truncate(time.Microsecond)
	sum := sha256.Sum256([]byte(in.ScheduleID + ":" + when.Format(time.RFC3339Nano)))
	in.IdempotencyKey = "schedule_" + hex.EncodeToString(sum[:])
	out, err := e.submit(ctx, tx, p, in.SubmitInput, "")
	if err != nil {
		return Submission{}, err
	}
	if out.Task.ActorID != p.ActorID || out.Task.TenantID != p.TenantID || out.Task.AuthorizationVersion != in.ExpectedAuthorizationVersion {
		return Submission{}, problem(409, "schedule_occurrence_conflict")
	}
	var bound string
	err = tx.QueryRow(ctx, `SELECT task_id FROM adtr.task_schedule_occurrences WHERE tenant_id=$1 AND schedule_id=$2 AND scheduled_at=$3`, p.TenantID, in.ScheduleID, when).Scan(&bound)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, err
	}
	if out.Replayed && (err != nil || bound != out.Task.ID) {
		return Submission{}, problem(409, "schedule_occurrence_conflict")
	}
	if bound != "" && bound != out.Task.ID {
		return Submission{}, problem(409, "schedule_occurrence_conflict")
	}
	_, err = tx.Exec(ctx, `INSERT INTO adtr.task_schedule_occurrences(tenant_id,schedule_id,scheduled_at,task_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, p.TenantID, in.ScheduleID, when, out.Task.ID)
	if err != nil {
		return Submission{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT task_id FROM adtr.task_schedule_occurrences WHERE tenant_id=$1 AND schedule_id=$2 AND scheduled_at=$3`, p.TenantID, in.ScheduleID, when).Scan(&bound); err != nil {
		return Submission{}, err
	}
	if bound != out.Task.ID {
		return Submission{}, problem(409, "schedule_occurrence_conflict")
	}
	return out, nil
}

const operationTimeout = 5 * time.Second

func operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, operationTimeout)
}
func closeTaskConnection(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}
func rollbackTaskTransaction(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
