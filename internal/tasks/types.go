// Package tasks implements a durable, fenced PostgreSQL queue. It has no AD adapter.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type State string

const (
	Queued          State = "queued"
	Running         State = "running"
	RetryWait       State = "retry_wait"
	CancelRequested State = "cancel_requested"
	Succeeded       State = "succeeded"
	Failed          State = "failed"
	PartialFailed   State = "partial_failed"
	DeadLetter      State = "dead_letter"
	Cancelled       State = "cancelled"
)

func (s State) Terminal() bool {
	return s == Succeeded || s == Failed || s == PartialFailed || s == DeadLetter || s == Cancelled
}
func (s State) Valid() bool {
	return s == Queued || s == Running || s == RetryWait || s == CancelRequested || s.Terminal()
}
func (s State) Source() string {
	switch s {
	case Queued:
		return "PENDING"
	case Running, CancelRequested:
		return "STARTED"
	case RetryWait:
		return "RETRY"
	case Succeeded:
		return "SUCCESS"
	case Cancelled:
		return "REVOKED"
	default:
		return "FAILURE"
	}
}

type Action string

const (
	Read    Action = "read"
	Write   Action = "write"
	Execute Action = "execute"
)

// Principal is only constructed by the authenticated application adapter. Never decode it from HTTP.
type Principal struct {
	TenantID string
	ActorID  int64
}
type Scope struct {
	DomainID, TaskName string
	Platform           bool
}

// Authorizer must recheck persisted identity, function and resource grants in tx,
// acquiring the shared tenant authorization lock before user/task locks. The
// version is a nonempty monotonic actor authorization epoch, pinned at submission.
// Any later epoch mismatch stops the old task even after grants are restored.
type Authorizer func(context.Context, pgx.Tx, Principal, Scope, Action) (string, error)

type Task struct {
	ID                   string          `json:"taskUUID"`
	TerminalAt           *time.Time      `json:"terminalAt"`
	Archived             bool            `json:"archived"`
	VisibilityVersion    int64           `json:"visibilityVersion"`
	Kind                 string          `json:"taskName"`
	DomainID             string          `json:"domainId"`
	PayloadVersion       int             `json:"payloadVersion"`
	State                State           `json:"state"`
	SourceState          string          `json:"sourceState"`
	Error                string          `json:"error"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
	Attempt              int             `json:"attempt"`
	MaxAttempts          int             `json:"maxAttempts"`
	Progress             int             `json:"progress"`
	ResultVersion        int64           `json:"resultVersion"`
	Result               json.RawMessage `json:"result"`
	Cursor               json.RawMessage `json:"cursor"`
	ParentID             string          `json:"parentTaskUUID"`
	NextAttemptAt        *time.Time      `json:"nextAttemptAt,omitempty"`
	TenantID             string          `json:"-"`
	ActorID              int64           `json:"-"`
	AuthorizationVersion string          `json:"-"`
	Payload              json.RawMessage `json:"-"`
	PayloadHash          string          `json:"-"`
	IdempotencyKey       string          `json:"-"`
	LeaseOwner           string          `json:"-"`
	LeaseUntil           *time.Time      `json:"-"`
	FencingToken         int64           `json:"-"`
}
type Event struct {
	ID            int64     `json:"id"`
	Action        string    `json:"action"`
	State         State     `json:"state"`
	Attempt       int       `json:"attempt"`
	ResultVersion int64     `json:"resultVersion"`
	CreatedAt     time.Time `json:"createdAt"`
}
type Detail struct {
	Task   Task    `json:"task"`
	Events []Event `json:"events"`
}
type Submission struct {
	Task     Task `json:"task"`
	Replayed bool `json:"replayed"`
}
type SubmitInput struct {
	TaskName       string          `json:"taskName"`
	DomainID       string          `json:"domainId"`
	PayloadVersion int             `json:"payloadVersion"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotencyKey"`
}
type Filter struct {
	Visibility        string
	PageIdx, PageSize int
	DomainID          string
	State             State
	TaskName          string
}
type Page struct {
	Index int `json:"pageIdx"`
	Size  int `json:"pageSize"`
	Total int `json:"total"`
	Pages int `json:"totalPage"`
}
type List struct {
	Page      Page   `json:"page"`
	Tasks     []Task `json:"tasks"`
	Exhausted bool   `json:"exhausted"`
}
type KindInfo struct {
	TaskName       string `json:"taskName"`
	PayloadVersion int    `json:"payloadVersion"`
	Scope          string `json:"scope"`
	MaxAttempts    int    `json:"maxAttempts"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func problem(status int, code string) error { return &Error{status, code} }

var ErrLeaseLost = errors.New("task lease lost")
var ErrNoTask = errors.New("no eligible task")
var ErrCancelRequested = errors.New("task cancellation requested")
var ErrAuthorization = errors.New("task authorization revoked")

// Execution is provided to a registered executor. Checkpoint atomically commits
// cursor/result/progress and rejects stale result versions and fencing tokens.
type FencedWork func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error)

type Execution struct {
	// WithTx atomically commits replay-safe module data with a fenced checkpoint.
	// The callback uses its bounded context and must not manage transactions.
	WithTx     func(context.Context, int64, FencedWork) (int64, error)
	Task       Task
	Probe      func(context.Context) error
	Checkpoint func(context.Context, int, json.RawMessage, json.RawMessage, int64) (int64, error)
}
type Outcome struct {
	State     State
	Result    json.RawMessage
	Code      string
	Retryable bool
}
type Executor func(context.Context, Execution) Outcome

// Kind policies are immutable after registry construction. Only explicitly safe
// replayable executors may register, or an explicit single-attempt executor whose
// failed/lost execution cannot be replayed or recovered.
type Kind struct {
	Name                      string
	Version                   int
	Platform                  bool
	MaxAttempts               int
	Lease, Heartbeat, Timeout time.Duration
	RetryBase, RetryCap       time.Duration
	RetryCodes                []string
	ReplaySafe                bool
	SingleAttemptOnly         bool
	// CancelDiscardsResult is for staged artifacts whose publication is Finish.
	// It must never be used to conceal an already completed external effect.
	CancelDiscardsResult bool
	// OwnerScoped protects private artifacts on every generic task API.
	OwnerScoped bool
	Schedulable bool
	Validate    func(json.RawMessage) (json.RawMessage, error)
	Execute     Executor
	// OnQuiesced acknowledges only that this exact started executor returned,
	// including all of its defers. It must be idempotent, honor its bounded context,
	// and must not manage the transaction, resolve secrets, or publish task results.
	// It runs without actor, grant, or lease authorization and may run again after
	// any transaction error, including an uncertain commit. Only single-attempt,
	// unschedulable kinds may register it.
	OnQuiesced func(context.Context, pgx.Tx, QuiescedAttempt) error
}
type Lease struct {
	Task  Task
	Owner string
	Token int64
}

// ScheduledInput is a trusted scheduler entrypoint, never a browser request.
type ScheduledInput struct {
	SubmitInput
	ExpectedAuthorizationVersion string
	ScheduleID                   string
	ScheduledAt                  time.Time
}
