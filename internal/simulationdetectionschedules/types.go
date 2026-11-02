// Package simulationdetectionschedules implements closed synthetic-only periodic detection.
package simulationdetectionschedules

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

const KindName = "simulation.detection.scheduled"
const ContractVersion = "adtr.simulation-detection-schedules.v1"
const MaxSchedules = 100
const MaxRevisions = 1024
const CandidateLimit = 32
const Lateness = 60 * time.Second
const PollInterval = time.Second

var ErrInvalid = errors.New("invalid synthetic schedule")
var ErrDenied = errors.New("synthetic schedule denied")
var ErrNotFound = errors.New("synthetic schedule not found")
var ErrConflict = errors.New("synthetic schedule conflict")
var ErrLimit = errors.New("synthetic schedule limit")
var ErrConfiguration = errors.New("synthetic schedule configuration changed")

type Scope struct {
	Tenant, Domain string
	ActorID        int64
}
type Definition struct {
	Label           string `json:"label"`
	Cycle           int    `json:"cycle"`
	TemplateID      string `json:"templateId"`
	TemplateVersion int64  `json:"templateVersion"`
	DatasetID       string `json:"datasetId"`
}
type Mutation struct {
	ID                     string `json:"id"`
	OperationID            string `json:"operationId"`
	ExpectedControlVersion int64  `json:"expectedControlVersion"`
	Definition
}
type ControlInput struct {
	ID                     string `json:"id"`
	OperationID            string `json:"operationId"`
	ExpectedControlVersion int64  `json:"expectedControlVersion"`
	Action                 string `json:"action"`
}
type Schedule struct {
	ID             string `json:"id"`
	DomainID       string `json:"domainId"`
	Revision       int64  `json:"revision"`
	ControlVersion int64  `json:"controlVersion"`
	State          string `json:"state"`
	Error          string `json:"error"`
	Definition
	Payload    simulationdetection.Payload `json:"pinned"`
	Anchor     time.Time                   `json:"anchor"`
	NextIndex  int64                       `json:"nextIndex"`
	NextAt     time.Time                   `json:"nextAt"`
	CreatedAt  time.Time                   `json:"createdAt"`
	UpdatedAt  time.Time                   `json:"updatedAt"`
	Simulation bool                        `json:"simulation"`
}
type Event struct {
	ID         int64     `json:"id"`
	Revision   int64     `json:"revision"`
	Action     string    `json:"action"`
	FirstIndex *int64    `json:"firstIndex,omitempty"`
	LastIndex  *int64    `json:"lastIndex,omitempty"`
	TaskID     string    `json:"taskId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}
type Detail struct {
	Schedule Schedule `json:"schedule"`
	Events   []Event  `json:"events"`
	HasMore  bool     `json:"hasMore"`
}
type Filter struct {
	PageIdx  int `json:"pageIdx"`
	PageSize int `json:"pageSize"`
}
type List struct {
	Schedules []Schedule `json:"schedules"`
	Page      tasks.Page `json:"page"`
}
type Receipt struct {
	OperationID    string `json:"operationId"`
	Action         string `json:"action"`
	ScheduleID     string `json:"scheduleId"`
	ControlVersion int64  `json:"controlVersion"`
	Revision       int64  `json:"revision"`
	State          string `json:"state"`
}
type Change struct {
	Receipt  Receipt  `json:"receipt"`
	Schedule Schedule `json:"schedule"`
	Replayed bool     `json:"replayed"`
}
type Audit struct {
	Action, ObjectID, OperationID string
	Version                       int64
}
type AuditFunc func(context.Context, pgx.Tx, Audit) error

// Payload binds a task to immutable schedule intent. Only the server constructs it.
type Payload struct {
	ScheduleID  string    `json:"scheduleId"`
	Revision    int64     `json:"revision"`
	ScheduledAt time.Time `json:"scheduledAt"`
	simulationdetection.Payload
}
