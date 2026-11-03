// Package simulationassetassessment stores bounded, immutable synthetic reports.
// Callers supply current authenticated scope in an owned transaction and roll
// back on every error. No function reads a directory or accepts client evidence.
package simulationassetassessment

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/assetassessment"
	"time"
)

const Contract = "adtr.simulation-asset-assessment.v1"
const MaxObjects = 64
const MaxReports = 64
const MaxPageSize = 20
const MaxRecordBytes = 262144
const MaxResponseBytes = 524288

var (
	ErrInvalid  = errors.New("simulation asset assessment: invalid")
	ErrDenied   = errors.New("simulation asset assessment: denied")
	ErrNotFound = errors.New("simulation asset assessment: not found")
	ErrConflict = errors.New("simulation asset assessment: conflict")
	ErrLimit    = errors.New("simulation asset assessment: budget exceeded")
)

type Scope struct {
	Tenant, Domain string
	ActorID        int64
	Read, Manage   bool
	Epoch          int64
}
type Scenario struct {
	ID   string    `json:"id"`
	AsOf time.Time `json:"asOf"`
}
type Observation struct {
	Contract           string                  `json:"contract"`
	Simulation         bool                    `json:"simulation"`
	ID                 string                  `json:"id"`
	TenantID           string                  `json:"tenantId"`
	DomainID           string                  `json:"domainId"`
	Complete           bool                    `json:"complete"`
	CompletenessReason string                  `json:"completenessReason"`
	Objects            []assetassessment.Input `json:"objects"`
	Scenarios          []Scenario              `json:"scenarios"`
	Hash               string                  `json:"hash"`
}
type CreateInput struct {
	ID                 string `json:"id"`
	OperationID        string `json:"operationId"`
	ObservationID      string `json:"observationId"`
	ExpectedSourceHash string `json:"expectedSourceHash"`
	ScenarioID         string `json:"scenarioId"`
}
type Summary struct {
	Total         int            `json:"total"`
	Complete      bool           `json:"complete"`
	Kinds         map[string]int `json:"kinds"`
	AccountStatus map[string]int `json:"accountStatus"`
	Activity      map[string]int `json:"activity"`
	GroupType     map[string]int `json:"groupType"`
	GroupScope    map[string]int `json:"groupScope"`
	Reasons       map[string]int `json:"reasons"`
}
type Report struct {
	Contract      string                   `json:"contract"`
	Simulation    bool                     `json:"simulation"`
	ID            string                   `json:"id"`
	TenantID      string                   `json:"tenantId"`
	DomainID      string                   `json:"domainId"`
	ActorID       int64                    `json:"actorId"`
	Epoch         int64                    `json:"epoch"`
	ObservationID string                   `json:"observationId"`
	SourceHash    string                   `json:"sourceHash"`
	ScenarioID    string                   `json:"scenarioId"`
	Version       string                   `json:"version"`
	AsOf          time.Time                `json:"asOf"`
	CreatedAt     time.Time                `json:"createdAt"`
	Rows          []assetassessment.Result `json:"rows"`
	Summary       Summary                  `json:"summary"`
	Hash          string                   `json:"hash"`
}
type Receipt struct {
	Contract    string      `json:"contract"`
	Simulation  bool        `json:"simulation"`
	OperationID string      `json:"operationId"`
	RequestHash string      `json:"requestHash"`
	Input       CreateInput `json:"input"`
	Report      Report      `json:"report"`
}
type Filter struct {
	Kind, AccountStatus, Activity, GroupType, GroupScope string
	Offset, Limit                                        int
}
type RowsPage struct {
	ReportID   string                   `json:"reportId"`
	ReportHash string                   `json:"reportHash"`
	Summary    Summary                  `json:"summary"`
	Matched    int                      `json:"matched"`
	Offset     int                      `json:"offset"`
	Limit      int                      `json:"limit"`
	Rows       []assetassessment.Result `json:"rows"`
}
type ReportSummary struct {
	ID         string    `json:"id"`
	SourceHash string    `json:"sourceHash"`
	ScenarioID string    `json:"scenarioId"`
	AsOf       time.Time `json:"asOf"`
	CreatedAt  time.Time `json:"createdAt"`
	Hash       string    `json:"hash"`
	Summary    Summary   `json:"summary"`
}
type HistoryPage struct {
	Total   int             `json:"total"`
	Offset  int             `json:"offset"`
	Limit   int             `json:"limit"`
	Reports []ReportSummary `json:"reports"`
}
type AuditFunc func(context.Context, pgx.Tx, Receipt) error
