// Package simulationdnsip owns synthetic fixed observations and task-bound XLSX artifacts.
// Every API requires the caller's live-authorized transaction; errors require rollback.
package simulationdnsip

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/tasks"
)

const Contract = "adtr.simulation-dns-ip.v1"
const KindName = "simulation.dns_ip.export"
const MaxObservations = 8
const MaxExports = 64
const MaxObservationBytes = 8 * 1024 * 1024
const MaxArtifactBytes = 8 * 1024 * 1024
const MaxResponseBytes = 1024 * 1024

var ErrInvalid = errors.New("simulation dns ip: invalid input")
var ErrDenied = errors.New("simulation dns ip: denied")
var ErrNotFound = errors.New("simulation dns ip: not found")
var ErrConflict = errors.New("simulation dns ip: conflict")
var ErrLimit = errors.New("simulation dns ip: budget exceeded")

type Scope struct {
	Tenant, Domain string
	ActorID        int64
	Read, Manage   bool
}
type Fixture struct {
	ID, DNSDomain, Source string
	Records               []dnsipassets.Record
}
type Observation struct {
	ID          string `json:"id"`
	DNSDomain   string `json:"dnsDomain"`
	Source      string `json:"source"`
	Hash        string `json:"hash"`
	RecordCount int    `json:"recordCount"`
	Complete    bool   `json:"complete"`
	Contract    string `json:"contract"`
	Simulation  bool   `json:"simulation"`
}
type Query struct {
	ObservationID string `json:"observationId"`
	Keyword       string `json:"keyword"`
	Family        string `json:"family"`
	Offset        int    `json:"offset"`
	Limit         int    `json:"limit"`
}
type Page struct {
	Observation Observation          `json:"observation"`
	Query       Query                `json:"query"`
	Rows        []dnsipassets.Record `json:"rows"`
	Total       int                  `json:"total"`
	Exhausted   bool                 `json:"exhausted"`
}
type ExportInput struct {
	ObservationID   string `json:"observationId"`
	ObservationHash string `json:"observationHash"`
	Keyword         string `json:"keyword"`
	Family          string `json:"family"`
	OperationID     string `json:"operationId"`
}
type Payload struct {
	ObservationID   string `json:"observationId"`
	ObservationHash string `json:"observationHash"`
	DNSDomain       string `json:"dnsDomain"`
	Keyword         string `json:"keyword"`
	Family          string `json:"family"`
	Contract        string `json:"contract"`
}
type Manifest struct {
	Contract   string   `json:"contract"`
	Simulation bool     `json:"simulation"`
	TaskID     string   `json:"taskId"`
	Payload    Payload  `json:"payload"`
	RowCount   int      `json:"rowCount"`
	Columns    []string `json:"columns"`
	Bytes      int      `json:"bytes"`
	SHA256     string   `json:"sha256"`
	Filename   string   `json:"filename"`
}
type Intent struct {
	DomainID    string       `json:"domainId"`
	Action      string       `json:"action"`
	OperationID string       `json:"operationId"`
	Export      *ExportInput `json:"export,omitempty"`
	TaskID      string       `json:"taskId"`
}
type Receipt struct {
	Contract    string            `json:"contract"`
	Simulation  bool              `json:"simulation"`
	OperationID string            `json:"operationId"`
	Action      string            `json:"action"`
	ActorID     int64             `json:"actorId"`
	Intent      Intent            `json:"intent"`
	RequestHash string            `json:"requestHash"`
	Submission  *tasks.Submission `json:"submission,omitempty"`
	Task        *tasks.Task       `json:"task,omitempty"`
}
type AuditFunc func(context.Context, pgx.Tx, Receipt) error
