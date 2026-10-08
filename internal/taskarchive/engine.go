package taskarchive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type Engine struct{ authorize tasks.Authorizer }

// The authorizer must check both tasks and task_archive grants, lock the
// current tenant then actor, and return the actor's current authorization epoch.
func New(authorize tasks.Authorizer) (*Engine, error) {
	if authorize == nil {
		return nil, errors.New("task archival requires a live authorizer")
	}
	return &Engine{authorize: authorize}, nil
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
	// This only verifies the gate already acquired before any identity locks.
	var version int
	if err := tx.QueryRow(ctx, `SELECT version FROM adtr.schema_version WHERE singleton=true FOR SHARE`).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tasks.ErrSchemaIncompatible
		}
		return err
	}
	if version != SchemaVersion {
		return tasks.ErrSchemaIncompatible
	}
	return nil
}
func (e *Engine) auth(ctx context.Context, tx pgx.Tx, p tasks.Principal, action tasks.Action) (string, error) {
	if !identifier.MatchString(p.TenantID) || p.ActorID <= 0 {
		return "", tasks.ErrAuthorization
	}
	epoch, err := e.authorize(ctx, tx, p, tasks.Scope{TaskName: HealthKind, DomainID: "platform", Platform: true}, action)
	if err == nil && epoch == "" {
		return "", tasks.ErrAuthorization
	}
	return epoch, err
}
func databaseNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now.UTC(), err
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

const eligiblePredicate = `t.tenant_id=$1 AND t.kind='infrastructure.health' AND t.domain_id='platform'
 AND t.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled')
 AND t.terminal_at IS NOT NULL AND t.terminal_at<$2
 AND (t.lease_until IS NULL OR t.lease_until<=$3) AND NOT COALESCE(v.archived,false)`

// CandidatesTx is advisory. Mutations independently recheck every target after
// stable task/visibility locks. Counts and pagination use the same scoped filter.
func (e *Engine) CandidatesTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, f Filter) (Candidates, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Candidates{}, err
	}
	before, err := ParseBefore(f.Before)
	if err != nil {
		return Candidates{}, err
	}
	if f.PageIdx == 0 {
		f.PageIdx = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.PageIdx < 1 || f.PageIdx > 1000000 || f.PageSize < 1 || f.PageSize > 100 {
		return Candidates{}, problem(400, "invalid_input")
	}
	if _, err = e.auth(ctx, tx, p, tasks.Read); err != nil {
		return Candidates{}, err
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return Candidates{}, err
	}
	if before.After(now) {
		return Candidates{}, problem(400, "invalid_before")
	}
	from := ` FROM adtr.tasks t LEFT JOIN adtr.task_visibility v ON v.task_id=t.task_id AND v.tenant_id=t.tenant_id WHERE ` + eligiblePredicate
	var total int
	if err = tx.QueryRow(ctx, `SELECT count(*)`+from, p.TenantID, before, now).Scan(&total); err != nil {
		return Candidates{}, err
	}
	out := Candidates{Before: before, Page: tasks.Page{Index: f.PageIdx, Size: f.PageSize, Total: total, Pages: (total + f.PageSize - 1) / f.PageSize}, Tasks: []Candidate{}}
	offset := (f.PageIdx - 1) * f.PageSize
	out.Exhausted = offset+f.PageSize >= total
	rows, err := tx.Query(ctx, `SELECT t.task_id,t.kind,t.domain_id,t.state,t.terminal_at,COALESCE(v.visibility_version,0)`+from+` ORDER BY t.terminal_at,t.task_id LIMIT $4 OFFSET $5`, p.TenantID, before, now, f.PageSize, offset)
	if err != nil {
		return Candidates{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Candidate
		if err = rows.Scan(&c.TaskID, &c.TaskName, &c.DomainID, &c.State, &c.TerminalAt, &c.VisibilityVersion); err != nil {
			return Candidates{}, err
		}
		c.TerminalAt = c.TerminalAt.UTC()
		out.Tasks = append(out.Tasks, c)
	}
	return out, rows.Err()
}

func (e *Engine) ArchiveTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, in ArchiveInput) (Result, error) {
	before, err := ParseBefore(in.Before)
	if err != nil {
		return Result{}, err
	}
	return e.mutateTx(ctx, tx, p, "archive", in.Targets, &before, in.Reason, in.IdempotencyKey)
}
func (e *Engine) RestoreTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, in RestoreInput) (Result, error) {
	return e.mutateTx(ctx, tx, p, "restore", in.Targets, nil, in.Reason, in.IdempotencyKey)
}

type lockedTarget struct {
	id, kind, domain       string
	state                  tasks.State
	terminalAt, leaseUntil *time.Time
	archived               bool
	version                int64
}

func eligible(t lockedTarget, before *time.Time, now time.Time, archive bool) bool {
	if t.kind != HealthKind || t.domain != "platform" || !t.state.Terminal() || t.leaseUntil != nil && t.leaseUntil.After(now) || t.archived == archive {
		return false
	}
	return !archive || t.terminalAt != nil && t.terminalAt.Before(*before)
}

type requestIdentity struct {
	Action  string     `json:"action"`
	Targets []Target   `json:"targets"`
	Before  *time.Time `json:"before,omitempty"`
	Reason  string     `json:"reason"`
}

func requestHash(action string, targets []Target, before *time.Time, reason string) string {
	raw, _ := json.Marshal(requestIdentity{action, targets, before, reason})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (e *Engine) mutateTx(ctx context.Context, tx pgx.Tx, p tasks.Principal, action string, input []Target, before *time.Time, reason, key string) (Result, error) {
	if err := e.requireSchemaTx(ctx, tx); err != nil {
		return Result{}, err
	}
	targets, err := validateTargets(input, reason, key)
	if err != nil {
		return Result{}, err
	}
	epoch, err := e.auth(ctx, tx, p, tasks.Write)
	if err != nil {
		return Result{}, err
	}
	hash := requestHash(action, targets, before, reason)
	var oldHash string
	var oldJSON []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,receipt FROM adtr.task_archive_operations WHERE tenant_id=$1 AND actor_id=$2 AND operation_key=$3`, p.TenantID, p.ActorID, key).Scan(&oldHash, &oldJSON)
	if err == nil {
		if oldHash != hash {
			return Result{}, problem(409, "idempotency_conflict")
		}
		var receipt Receipt
		if err = json.Unmarshal(oldJSON, &receipt); err != nil {
			return Result{}, err
		}
		return Result{Receipt: receipt, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	locked := make([]lockedTarget, 0, len(targets))
	// Every caller holds schema -> tenant -> actor before these sorted locks.
	// The task lock also serializes creation of its previously absent visibility.
	for _, target := range targets {
		var item lockedTarget
		err = tx.QueryRow(ctx, `SELECT task_id,kind,domain_id,state,terminal_at,lease_until FROM adtr.tasks WHERE tenant_id=$1 AND task_id=$2 FOR UPDATE`, p.TenantID, target.TaskID).Scan(&item.id, &item.kind, &item.domain, &item.state, &item.terminalAt, &item.leaseUntil)
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, problem(404, "not_found")
		}
		if err != nil {
			return Result{}, err
		}
		err = tx.QueryRow(ctx, `SELECT archived,visibility_version FROM adtr.task_visibility WHERE tenant_id=$1 AND task_id=$2 FOR UPDATE`, p.TenantID, target.TaskID).Scan(&item.archived, &item.version)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Result{}, err
		}
		locked = append(locked, item)
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	archive := action == "archive"
	if before != nil && before.After(now) {
		return Result{}, problem(400, "invalid_before")
	}
	for i, item := range locked {
		if item.version != targets[i].VisibilityVersion {
			return Result{}, problem(409, "visibility_conflict")
		}
		if item.version == math.MaxInt64 {
			return Result{}, problem(409, "visibility_version_exhausted")
		}
		if !eligible(item, before, now, archive) {
			return Result{}, problem(409, "task_not_archivable")
		}
	}
	id, err := newID()
	if err != nil {
		return Result{}, err
	}
	receipt := Receipt{ID: id, Action: action, Before: before, Reason: reason, OccurredAt: now, Targets: make([]ChangedTarget, 0, len(locked))}
	for _, item := range locked {
		receipt.Targets = append(receipt.Targets, ChangedTarget{item.id, archive, item.version + 1})
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return Result{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO adtr.task_archive_operations(operation_id,tenant_id,actor_id,authorization_version,operation_key,request_hash,action,receipt,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, p.TenantID, p.ActorID, epoch, key, hash, action, raw, now); err != nil {
		return Result{}, err
	}
	for _, item := range locked {
		var archivedAt *time.Time
		var archivedBy *int64
		if archive {
			archivedAt = &now
			archivedBy = &p.ActorID
		}
		tag, e := tx.Exec(ctx, `INSERT INTO adtr.task_visibility(task_id,tenant_id,archived,visibility_version,archived_at,archived_by,reason) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(task_id) DO UPDATE SET archived=EXCLUDED.archived,visibility_version=EXCLUDED.visibility_version,archived_at=EXCLUDED.archived_at,archived_by=EXCLUDED.archived_by,reason=EXCLUDED.reason
 WHERE adtr.task_visibility.tenant_id=EXCLUDED.tenant_id AND adtr.task_visibility.visibility_version=$8 AND adtr.task_visibility.archived=$9`, item.id, p.TenantID, archive, item.version+1, archivedAt, archivedBy, reason, item.version, item.archived)
		if e != nil {
			return Result{}, e
		}
		if tag.RowsAffected() != 1 {
			return Result{}, problem(409, "visibility_conflict")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO adtr.task_archive_events(operation_id,task_id,tenant_id,actor_id,authorization_version,action,previous_version,visibility_version,archived,reason,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, item.id, p.TenantID, p.ActorID, epoch, action, item.version, item.version+1, archive, reason, now); err != nil {
			return Result{}, err
		}
	}
	return Result{Receipt: receipt}, nil
}
