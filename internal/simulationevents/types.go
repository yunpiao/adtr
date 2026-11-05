// Package simulationevents persists explicitly synthetic fixture datasets only.
// Callers own the transaction and must obtain live authorization and schema gate
// before invoking these APIs; no API commits or grants permissions.
package simulationevents

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
)

const MaxPage = 100
const MaxHistory = 1024

var ErrNotFound = errors.New("simulated event object not found")
var ErrLimit = errors.New("simulated event storage limit exceeded")
var ErrInvalid = securityevents.ErrInvalid
var ErrDenied = securityevents.ErrDenied
var ErrConflict = securityevents.ErrConflict

type Dataset struct {
	ID          string `json:"id"`
	Fixture     string `json:"fixture"`
	FixtureHash string `json:"fixtureHash"`
	Contract    string `json:"contract"`
	Simulation  bool   `json:"simulation"`
	EventCount  int    `json:"eventCount"`
}
type DatasetPage struct {
	Contract   string    `json:"contract"`
	Simulation bool      `json:"simulation"`
	Datasets   []Dataset `json:"datasets"`
	Total      int       `json:"total"`
}
type QueryResult struct {
	Simulation bool                      `json:"simulation"`
	Dataset    Dataset                   `json:"dataset"`
	Page       securityevents.Page       `json:"page"`
	Cases      []securityevents.CaseView `json:"cases"`
}
type Detail struct {
	Simulation   bool                    `json:"simulation"`
	Dataset      Dataset                 `json:"dataset"`
	Case         securityevents.CaseView `json:"case"`
	Evidence     []securityevents.Event  `json:"evidence"`
	History      []securityevents.Audit  `json:"history"`
	HistoryTotal int                     `json:"historyTotal"`
}
type TransitionInput struct {
	DatasetID, CaseID, OperationID string
	Change                         securityevents.Change
}
type Receipt struct {
	Simulation  bool                    `json:"simulation"`
	Dataset     Dataset                 `json:"dataset"`
	DatasetID   string                  `json:"datasetId"`
	OperationID string                  `json:"operationId"`
	Case        securityevents.CaseView `json:"case"`
	Audit       securityevents.Audit    `json:"audit"`
}

// AuditFunc MUST write transactional application audit using the supplied tx.
// Nil callbacks are rejected; failures require the caller to roll back.
type AuditFunc func(context.Context, pgx.Tx, Receipt) error

type FixtureCase struct {
	ID          string
	EvidenceIDs []string
}
type Fixture struct {
	ID         string
	Provenance securityevents.Provenance
	Events     []securityevents.RawEvent
	Cases      []FixtureCase
}
