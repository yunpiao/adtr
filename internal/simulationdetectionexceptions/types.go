// Package simulationdetectionexceptions persists synthetic-only exception annotations.
// Callers own the authenticated transaction and must roll back any returned error.
package simulationdetectionexceptions

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionexceptions"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"time"
)

const Contract = "adtr.simulation-detection-exceptions.v1"
const MaxEntries = 32
const MaxRevisions = 256
const MaxEvaluations = 64
const MaxRecordBytes = 256 * 1024
const MaxResponseBytes = 512 * 1024

var (
	ErrInvalid  = errors.New("simulation detection exceptions: invalid input")
	ErrDenied   = errors.New("simulation detection exceptions: denied")
	ErrNotFound = errors.New("simulation detection exceptions: not found")
	ErrConflict = errors.New("simulation detection exceptions: conflict")
	ErrLimit    = errors.New("simulation detection exceptions: budget exceeded")
)

type Scope struct {
	Tenant, Domain                          string
	ActorID                                 int64
	Read, Manage, DetectionRead, EventsRead bool
}

// Declaration never accepts a digest, enabled/archived state, scope or identity.
// Rule bindings are derived from a currently readable succeeded source task.
type Declaration struct {
	ID           string                     `json:"id"`
	Global       bool                       `json:"global"`
	SourceTaskID string                     `json:"sourceTaskId"`
	RuleID       string                     `json:"ruleId"`
	Reason       string                     `json:"reason"`
	NotBefore    time.Time                  `json:"notBefore"`
	ExpiresAt    time.Time                  `json:"expiresAt"`
	Conditions   []detectionrules.Condition `json:"conditions"`
}
type Mutation struct {
	Action           string        `json:"action"`
	OperationID      string        `json:"operationId"`
	ExpectedRevision uint64        `json:"expectedRevision"`
	ID               string        `json:"id"`
	Declaration      *Declaration  `json:"declaration"`
	Entries          []Declaration `json:"entries"`
}
type PreviewInput struct {
	SourceTaskID string    `json:"sourceTaskId"`
	EventID      string    `json:"eventId"`
	RuleID       string    `json:"ruleId"`
	ID           string    `json:"id"`
	Reason       string    `json:"reason"`
	NotBefore    time.Time `json:"notBefore"`
	ExpiresAt    time.Time `json:"expiresAt"`
}
type EvaluationInput struct {
	ID               string `json:"id"`
	OperationID      string `json:"operationId"`
	SourceTaskID     string `json:"sourceTaskId"`
	ExpectedRevision uint64 `json:"expectedRevision"`
}
type Record struct {
	Contract    string                       `json:"contract"`
	Simulation  bool                         `json:"simulation"`
	Snapshot    detectionexceptions.Snapshot `json:"snapshot"`
	SourceTasks map[string]string            `json:"sourceTasks"`
	ActorID     int64                        `json:"actorId"`
	Action      string                       `json:"action"`
	At          time.Time                    `json:"at"`
	Hash        string                       `json:"hash"`
}
type Evaluation struct {
	Contract         string                               `json:"contract"`
	Simulation       bool                                 `json:"simulation"`
	ID               string                               `json:"id"`
	ActorID          int64                                `json:"actorId"`
	SourceTaskID     string                               `json:"sourceTaskId"`
	SourceResultHash string                               `json:"sourceResultHash"`
	Source           simulationdetection.Result           `json:"source"`
	Revision         uint64                               `json:"revision"`
	SnapshotHash     string                               `json:"snapshotHash"`
	EvaluatedAt      time.Time                            `json:"evaluatedAt"`
	Matches          []detectionexceptions.AnnotatedMatch `json:"matches"`
	Hash             string                               `json:"hash"`
}
type EvaluationSummary struct {
	ID           string    `json:"id"`
	SourceTaskID string    `json:"sourceTaskId"`
	Revision     uint64    `json:"revision"`
	EvaluatedAt  time.Time `json:"evaluatedAt"`
}
type Intent struct {
	DomainID         string        `json:"domainId"`
	Action           string        `json:"action"`
	OperationID      string        `json:"operationId"`
	ExpectedRevision uint64        `json:"expectedRevision"`
	ID               string        `json:"id"`
	Declaration      *Declaration  `json:"declaration"`
	Entries          []Declaration `json:"entries"`
	SourceTaskID     string        `json:"sourceTaskId"`
}
type Receipt struct {
	Contract     string      `json:"contract"`
	Simulation   bool        `json:"simulation"`
	OperationID  string      `json:"operationId"`
	Action       string      `json:"action"`
	ActorID      int64       `json:"actorId"`
	At           time.Time   `json:"at"`
	Intent       Intent      `json:"intent"`
	RequestHash  string      `json:"requestHash"`
	Revision     uint64      `json:"revision"`
	SnapshotHash string      `json:"snapshotHash"`
	Evaluation   *Evaluation `json:"evaluation,omitempty"`
}
type AuditFunc func(context.Context, pgx.Tx, Receipt) error
