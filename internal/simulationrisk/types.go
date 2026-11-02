// Package simulationrisk evaluates immutable, explicitly synthetic datasets.
// Callers supply live authorization, schema gates and own transaction rollback.
package simulationrisk

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

const Contract = "adtr.risk.simulated.v1"
const MappingVersion = "illustrative-event-risk-v1"
const MaxResets = 1024

var ErrInvalid = simulationevents.ErrInvalid
var ErrDenied = simulationevents.ErrDenied
var ErrNotFound = simulationevents.ErrNotFound
var ErrConflict = simulationevents.ErrConflict
var ErrLimit = simulationevents.ErrLimit

// Scope is derived from current risk-specific authorization, never client JSON.
type Scope struct {
	Tenant, Domain string
	Read, Manage   bool
}
type Result struct {
	riskscore.Result
	Dataset          simulationevents.Dataset `json:"dataset"`
	Revision         uint64                   `json:"revision"`
	Contract         string                   `json:"contract"`
	MappingVersion   string                   `json:"mappingVersion"`
	SourceEventCount int                      `json:"sourceEventCount"`
	NoRiskEventCount int                      `json:"noRiskEventCount"`
}
type ResetInput struct {
	DatasetID, Subject, OperationID, Actor, Reason string
	ExpectedRevision                               uint64
}
type Receipt struct {
	Simulation  bool   `json:"simulation"`
	OperationID string `json:"operationId"`
	Actor       string `json:"actor"`
	Reason      string `json:"reason"`
	Result      Result `json:"result"`
}

// AuditFunc must insert the application audit using this same transaction.
// Nil is rejected. On any error the caller MUST roll back, never commit.
type AuditFunc func(context.Context, pgx.Tx, Receipt) error
