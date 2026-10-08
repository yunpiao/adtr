// Package schedules admits bounded periodic health tasks on an immutable UTC grid.
package schedules

import (
	"encoding/json"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"time"

	"github.com/yunpiao/adtr/internal/tasks"
)

const SchemaVersion = schemaversion.Current
const HealthKind = "infrastructure.health"
const MinIntervalSeconds = 60
const MaxIntervalSeconds = 86400

type State string

const (
	Paused               State = "paused"
	Enabled              State = "enabled"
	AuthorizationBlocked State = "authorization_blocked"
)

func (s State) Valid() bool { return s == Paused || s == Enabled || s == AuthorizationBlocked }

type Schedule struct {
	ID                   string          `json:"scheduleUUID"`
	Label                string          `json:"label"`
	TaskName             string          `json:"taskName"`
	DomainID             string          `json:"domainId"`
	PayloadVersion       int             `json:"payloadVersion"`
	StartAt              time.Time       `json:"startAt"`
	IntervalSeconds      int64           `json:"intervalSeconds"`
	State                State           `json:"state"`
	ControlVersion       int64           `json:"controlVersion"`
	NextAt               *time.Time      `json:"nextAt,omitempty"`
	LastTaskID           string          `json:"lastTaskUUID"`
	LastScheduledAt      *time.Time      `json:"lastScheduledAt,omitempty"`
	Error                string          `json:"error"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
	TenantID             string          `json:"-"`
	ActorID              int64           `json:"-"`
	AuthorizationVersion string          `json:"-"`
	Payload              json.RawMessage `json:"-"`
	DefinitionHash       string          `json:"-"`
	CreationKey          string          `json:"-"`
	NextIndex            int64           `json:"-"`
}

type CreateInput struct {
	Label           string          `json:"label"`
	TaskName        string          `json:"taskName"`
	DomainID        string          `json:"domainId"`
	PayloadVersion  int             `json:"payloadVersion"`
	Payload         json.RawMessage `json:"payload"`
	StartAt         string          `json:"startAt"`
	IntervalSeconds int64           `json:"intervalSeconds"`
	IdempotencyKey  string          `json:"idempotencyKey"`
}

type ControlInput struct {
	ScheduleID             string `json:"scheduleUUID"`
	ExpectedControlVersion int64  `json:"expectedControlVersion"`
	IdempotencyKey         string `json:"idempotencyKey"`
}

type Creation struct {
	Schedule Schedule `json:"schedule"`
	Replayed bool     `json:"replayed"`
}

// Receipt is the immutable result of a control operation. Schedule is the current
// state, so replaying an earlier enable after pause cannot imply a new enable.
type Receipt struct {
	OperationID    string    `json:"operationUUID"`
	ScheduleID     string    `json:"scheduleUUID"`
	Action         string    `json:"action"`
	State          State     `json:"state"`
	ControlVersion int64     `json:"controlVersion"`
	CreatedAt      time.Time `json:"createdAt"`
}
type Control struct {
	Receipt  Receipt  `json:"receipt"`
	Schedule Schedule `json:"schedule"`
	Replayed bool     `json:"replayed"`
}
type Filter struct {
	PageIdx, PageSize int
	State             State
}
type List struct {
	Page      tasks.Page `json:"page"`
	Schedules []Schedule `json:"schedules"`
	Exhausted bool       `json:"exhausted"`
}
type Event struct {
	ID             int64      `json:"id"`
	Action         string     `json:"action"`
	FirstIndex     *int64     `json:"firstIndex,omitempty"`
	LastIndex      *int64     `json:"lastIndex,omitempty"`
	Count          *int64     `json:"count,omitempty"`
	FirstAt        *time.Time `json:"firstAt,omitempty"`
	LastAt         *time.Time `json:"lastAt,omitempty"`
	TaskID         string     `json:"taskUUID"`
	ControlVersion int64      `json:"controlVersion"`
	CreatedAt      time.Time  `json:"createdAt"`
}
type Detail struct {
	Schedule  Schedule   `json:"schedule"`
	Page      tasks.Page `json:"page"`
	Events    []Event    `json:"events"`
	Exhausted bool       `json:"exhausted"`
}
