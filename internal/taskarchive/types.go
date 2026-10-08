// Package taskarchive changes task visibility without deleting execution,
// admission, occurrence, event, outbox or result evidence.
package taskarchive

import (
	"github.com/yunpiao/adtr/internal/schemaversion"
	"time"

	"github.com/yunpiao/adtr/internal/tasks"
)

const SchemaVersion = schemaversion.Current
const HealthKind = "infrastructure.health"

type Target struct {
	TaskID            string `json:"taskUUID"`
	VisibilityVersion int64  `json:"visibilityVersion"`
}
type ArchiveInput struct {
	Targets        []Target `json:"targets"`
	Before         string   `json:"before"`
	Reason         string   `json:"reason"`
	IdempotencyKey string   `json:"idempotencyKey"`
}
type RestoreInput struct {
	Targets        []Target `json:"targets"`
	Reason         string   `json:"reason"`
	IdempotencyKey string   `json:"idempotencyKey"`
}
type Filter struct {
	Before            string
	PageIdx, PageSize int
}
type Candidate struct {
	TaskID            string      `json:"taskUUID"`
	TaskName          string      `json:"taskName"`
	DomainID          string      `json:"domainId"`
	State             tasks.State `json:"state"`
	TerminalAt        time.Time   `json:"terminalAt"`
	VisibilityVersion int64       `json:"visibilityVersion"`
}
type Candidates struct {
	Before    time.Time   `json:"before"`
	Page      tasks.Page  `json:"page"`
	Tasks     []Candidate `json:"tasks"`
	Exhausted bool        `json:"exhausted"`
}
type ChangedTarget struct {
	TaskID            string `json:"taskUUID"`
	Archived          bool   `json:"archived"`
	VisibilityVersion int64  `json:"visibilityVersion"`
}
type Receipt struct {
	ID         string          `json:"operationUUID"`
	Action     string          `json:"action"`
	Before     *time.Time      `json:"before,omitempty"`
	Reason     string          `json:"reason"`
	Targets    []ChangedTarget `json:"targets"`
	OccurredAt time.Time       `json:"occurredAt"`
}
type Result struct {
	Receipt  Receipt `json:"receipt"`
	Replayed bool    `json:"replayed"`
}
