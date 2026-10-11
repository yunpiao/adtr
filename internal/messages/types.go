// Package messages projects real health task outcomes into private, bounded inbox snapshots.
// HTTP adapters authenticate and lock the current tenant/actor before calling Tx methods.
package messages

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

const (
	MaxPageSize              = 200
	MaxCandidates            = 1000
	MaxInputBytes      int64 = 2 * 1024 * 1024
	MaxUserSnapshots         = 8
	MaxTenantSnapshots       = 128
	MaxGlobalSnapshots       = 1024
	ProjectionBatch          = 200
	CleanupBatch             = 200
	SnapshotTTL              = 10 * time.Minute
	CycleTimeout             = 5 * time.Second
	PollInterval             = time.Second
)

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func problem(status int, code string) error { return &Error{status, code} }

// Identity is a server-constructed adapter input, not proof of permission.
// The adapter must hold the current tenant/actor authorization locks, verify
// the session, account gates and tasks.readable, then retain those locks until
// its enclosing transaction commits. Never decode Identity from HTTP.
type Identity struct {
	TenantID             string
	UserID               int64
	SessionHash          string
	AuthorizationVersion int64
}

type Entry struct {
	ID          string                `json:"messageId"`
	TaskID      string                `json:"taskUUID"`
	Type        string                `json:"type"`
	SubType     string                `json:"subType"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	ScopeKind   messagecore.ScopeKind `json:"scopeKind"`
	DomainID    string                `json:"domainId"`
	OccurredAt  time.Time             `json:"occurredAt"`
	IngestedAt  time.Time             `json:"ingestedAt"`
	Read        bool                  `json:"read"`
	FirstReadAt *time.Time            `json:"firstReadAt"`
}
type Counts struct {
	All    int `json:"all"`
	Unread int `json:"unread"`
	Read   int `json:"read"`
}
type Snapshot struct {
	ResultID    string    `json:"resultId"`
	ConfirmedAt time.Time `json:"confirmedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Items       []Entry   `json:"items"`
	Total       int       `json:"total"`
	Counts      Counts    `json:"counts"`
	Offset      int       `json:"offset"`
	Limit       int       `json:"limit"`
}
type Location struct {
	ResultID string `json:"resultId"`
	Found    bool   `json:"found"`
	Index    int    `json:"index"`
	Page     int    `json:"page"`
}
type Progress struct {
	Target      int `json:"target"`
	Processed   int `json:"processed"`
	NewlyRead   int `json:"newlyRead"`
	AlreadyRead int `json:"alreadyRead"`
	Failed      int `json:"failed"`
	Remaining   int `json:"remaining"`
}
type ReadReceipt struct {
	OperationID string    `json:"operationId"`
	ResultID    string    `json:"resultId"`
	MessageID   string    `json:"messageId"`
	FirstReadAt time.Time `json:"firstReadAt"`
	CommittedAt time.Time `json:"committedAt"`
	Progress    Progress  `json:"progress"`
}

type Store struct {
	database *pgx.ConnConfig
	schema   int
}

func New(config *pgx.ConnConfig, schema int) (*Store, error) {
	if config == nil || schema <= 0 {
		return nil, errors.New("messages requires database and exact schema version")
	}
	return &Store{database: config.Copy(), schema: schema}, nil
}
func (s *Store) CheckSchemaTx(ctx context.Context, tx pgx.Tx) error {
	return tasks.CheckSchemaTx(ctx, tx, s.schema)
}
func (s *Store) requireSchemaTx(ctx context.Context, tx pgx.Tx) error {
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode IN ('ShareLock','ExclusiveLock'))`).Scan(&held); err != nil {
		return err
	}
	if !held {
		return tasks.ErrSchemaGateRequired
	}
	var actual int
	if err := tx.QueryRow(ctx, `SELECT version FROM adtr.schema_version WHERE singleton=true`).Scan(&actual); err != nil {
		return err
	}
	if actual != s.schema {
		return tasks.ErrSchemaIncompatible
	}
	return nil
}
func catalog() messagecore.Catalog {
	out := messagecore.Catalog{}
	for _, state := range []string{"succeeded", "failed", "partial_failed", "dead_letter", "cancelled"} {
		out = append(out, messagecore.TypeSubType{Type: "task_result", SubType: state})
	}
	return out
}
func limits() messagecore.Limits {
	return messagecore.Limits{MaxPageSize: MaxPageSize, MaxInputMessages: MaxCandidates, MaxInputBytes: MaxInputBytes, MaxMarkTargets: 1}
}
func coreError(err error) error {
	var e *messagecore.Error
	if errors.As(err, &e) {
		status := 400
		if e.Code == messagecore.BudgetExceeded {
			status = 422
		}
		return problem(status, string(e.Code))
	}
	return err
}
