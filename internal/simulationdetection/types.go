// Package simulationdetection stores synthetic-only, version-pinned detection declarations.
// Callers supply live authorization and own transaction commit/rollback.
package simulationdetection

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/tasks"
)

const KindName = "simulation.detection"
const MappingVersion = "adtr.simulation-detection.mapping.v1"
const MaxTemplates = 100
const MaxVersions = 1024

var ErrInvalid = errors.New("invalid simulation detection input")
var ErrDenied = errors.New("simulation detection denied")
var ErrNotFound = errors.New("simulation detection not found")
var ErrConflict = errors.New("simulation detection conflict")
var ErrLimit = errors.New("simulation detection budget exceeded")

type Scope struct {
	Tenant, Domain string
	ActorID        int64
	Read, Manage   bool
}
type Template struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Version    int64                 `json:"version"`
	Hash       string                `json:"hash"`
	Rules      []detectionrules.Rule `json:"rules"`
	Archived   bool                  `json:"archived"`
	Simulation bool                  `json:"simulation"`
}
type Mutation struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	OperationID     string                `json:"operationId"`
	ExpectedVersion int64                 `json:"expectedVersion"`
	Rules           []detectionrules.Rule `json:"rules"`
	Archive         bool                  `json:"archive"`
}
type RunInput struct {
	TemplateID      string `json:"templateId"`
	DatasetID       string `json:"datasetId"`
	OperationID     string `json:"operationId"`
	TemplateVersion int64  `json:"templateVersion"`
}
type Audit struct {
	Action, ObjectID, OperationID string
	Version                       int64
	Hash                          string
}
type AuditFunc func(context.Context, pgx.Tx, Audit) error
type Receipt struct {
	OperationID string            `json:"operationId"`
	Kind        string            `json:"kind"`
	Template    *Template         `json:"template,omitempty"`
	Submission  *tasks.Submission `json:"submission,omitempty"`
	Task        *tasks.Task       `json:"task,omitempty"`
}
type Payload struct {
	TemplateID      string `json:"templateId"`
	TemplateVersion int64  `json:"templateVersion"`
	TemplateHash    string `json:"templateHash"`
	DatasetID       string `json:"datasetId"`
	FixtureHash     string `json:"fixtureHash"`
	MappingVersion  string `json:"mappingVersion"`
}
type Result struct {
	Simulation bool   `json:"simulation"`
	Contract   string `json:"contract"`
	Payload
	EventCount int                    `json:"eventCount"`
	Matches    []detectionrules.Match `json:"matches"`
}
