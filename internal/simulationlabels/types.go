// Package simulationlabels provides a real transactional store over explicitly
// synthetic asset observations. Callers own live authorization and MUST roll
// back the transaction on every error, including a failed audit callback.
package simulationlabels

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/groupgraph"
	"github.com/yunpiao/adtr/internal/sensitivelabels"
	"time"
)

const Contract = "adtr.simulated-sensitive-assets.v1"
const MaxNodes = 256
const MaxEdges = 1024
const MaxGroups = 64
const MaxManualLabels = 256
const MaxDepth = 16
const MaxLabels = 4096
const MaxPage = 100
const MaxPolicies = 128
const MaxObservations = 128
const MaxRevisions = 1024
const MaxEvaluations = 4096
const MaxDatasets = 128

var (
	ErrInvalid  = errors.New("simulationlabels: invalid input")
	ErrDenied   = errors.New("simulationlabels: denied")
	ErrNotFound = errors.New("simulationlabels: not found")
	ErrConflict = errors.New("simulationlabels: conflict")
	ErrLimit    = errors.New("simulationlabels: limit exceeded")
	// ErrPageTooLarge is ONLY the serialized list-page budget, allowing clients
	// to reduce page size without masking graph, record or quota failures.
	ErrPageTooLarge = errors.New("simulationlabels: page too large")
	ErrIncomplete   = errors.New("simulationlabels: incomplete observation")
	ErrIncompatible = errors.New("simulationlabels: incompatible observations")
)

type Scope struct {
	Tenant, Domain string
	Read, Manage   bool
}
type Asset struct {
	ObjectID    string  `json:"objectId"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}
type Observation struct {
	ID       string              `json:"id"`
	Sequence uint64              `json:"sequence"`
	Complete bool                `json:"complete"`
	Snapshot groupgraph.Snapshot `json:"snapshot"`
	Assets   []Asset             `json:"assets"`
	Hash     string              `json:"hash"`
}

// Fixture is accepted ONLY by an explicitly invoked isolated setup producer.
// No HTTP route may expose SeedFixtureTx or accept these snapshots as user input.
type Fixture struct {
	ID           string        `json:"id"`
	Version      string        `json:"version"`
	Producer     string        `json:"producer"`
	Simulated    bool          `json:"simulated"`
	Observations []Observation `json:"observations"`
}
type Dataset struct {
	ID               string `json:"id"`
	Contract         string `json:"contract"`
	Producer         string `json:"producer"`
	Simulation       bool   `json:"simulation"`
	Hash             string `json:"hash"`
	ObservationCount int    `json:"observationCount"`
}
type PolicySpec struct {
	Name              string                        `json:"name"`
	SensitiveGroupIDs []string                      `json:"sensitiveGroupIds"`
	ManualLabels      []sensitivelabels.ManualLabel `json:"manualLabels"`
}
type Policy struct {
	ID        string     `json:"id"`
	DatasetID string     `json:"datasetId"`
	Revision  uint64     `json:"revision"`
	Status    string     `json:"status"`
	Spec      PolicySpec `json:"spec"`
	Hash      string     `json:"hash"`
	Actor     string     `json:"actor"`
	Reason    string     `json:"reason"`
	At        time.Time  `json:"at"`
}
type PolicyInput struct {
	DatasetID, PolicyID, Action, Actor, OperationID, Reason string
	ExpectedRevision                                        uint64
	Spec                                                    PolicySpec
}
type EvaluateInput struct {
	DatasetID, PolicyID, ObservationID, Actor, OperationID, Reason string
	ExpectedRevision                                               uint64
}
type CompareInput struct{ DatasetID, BeforeID, AfterID, Actor, OperationID, Reason string }
type Evaluation struct {
	ID          string                 `json:"id"`
	Dataset     Dataset                `json:"dataset"`
	Observation Observation            `json:"observation"`
	Policy      Policy                 `json:"policy"`
	Result      sensitivelabels.Result `json:"result"`
	Hash        string                 `json:"hash"`
	Actor       string                 `json:"actor"`
	At          time.Time              `json:"at"`
}
type Alert struct {
	ID         string                   `json:"id"`
	ObjectID   string                   `json:"objectId"`
	Changes    []sensitivelabels.Change `json:"changes"`
	Simulation bool                     `json:"simulation"`
	Delivery   string                   `json:"delivery"`
}
type Comparison struct {
	ID        string                    `json:"id"`
	DatasetID string                    `json:"datasetId"`
	BeforeID  string                    `json:"beforeId"`
	AfterID   string                    `json:"afterId"`
	Changes   sensitivelabels.ChangeSet `json:"changes"`
	Alerts    []Alert                   `json:"alerts"`
	Hash      string                    `json:"hash"`
	Actor     string                    `json:"actor"`
	At        time.Time                 `json:"at"`
}
type Receipt struct {
	Simulation  bool        `json:"simulation"`
	OperationID string      `json:"operationId"`
	Actor       string      `json:"actor"`
	Action      string      `json:"action"`
	DatasetID   string      `json:"datasetId"`
	Reason      string      `json:"reason"`
	At          time.Time   `json:"at"`
	Policy      *Policy     `json:"policy,omitempty"`
	Evaluation  *Evaluation `json:"evaluation,omitempty"`
	Comparison  *Comparison `json:"comparison,omitempty"`
}
type AuditFunc func(context.Context, pgx.Tx, Receipt) error
type Page[T any] struct {
	Items  []T `json:"items"`
	Count  int `json:"count"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}
type AssetLabels struct {
	sensitivelabels.ObjectLabels
	Name                 string  `json:"name"`
	Description          *string `json:"description"`
	IsClick              bool    `json:"isClick"`
	IsHoneypot           *bool   `json:"isHoneypot"`
	HoneypotAvailability string  `json:"honeypotAvailability"`
	Reason               string  `json:"reason"`
}
type AssetFilter struct {
	Kind, Name    string
	Offset, Limit int
}
