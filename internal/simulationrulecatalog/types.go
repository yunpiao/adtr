// Package simulationrulecatalog persists explicitly synthetic rule lifecycles.
// The authenticated caller owns the transaction and MUST roll back on any error.
package simulationrulecatalog

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/rulecatalog"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"time"
)

const Contract = "adtr.simulation-rule-catalog.v1"
const CopyAcknowledgement = "independent-template-copy-no-cascade"
const MaxRules = 32
const MaxRevisions = 64
const MaxCases = 8
const MaxPage = 32
const MaxRecordBytes = 256 * 1024
const MaxResponseBytes = 512 * 1024

var (
	ErrInvalid         = errors.New("simulation rule catalog: invalid input")
	ErrDenied          = errors.New("simulation rule catalog: denied")
	ErrNotFound        = errors.New("simulation rule catalog: not found")
	ErrConflict        = errors.New("simulation rule catalog: revision conflict")
	ErrTransition      = errors.New("simulation rule catalog: invalid transition")
	ErrLimit           = errors.New("simulation rule catalog: budget exceeded")
	ErrUnsupportedCopy = errors.New("simulation rule catalog: field unavailable in manual dataset mapping")
)

type Scope struct {
	Tenant, Domain                                        string
	ActorID                                               int64
	Read, Manage, Publish, DetectionRead, DetectionManage bool
}

// Declaration contains only editable fields. All identity/version metadata is server-owned.
type Declaration struct {
	Combine    string                     `json:"combine"`
	Conditions []detectionrules.Condition `json:"conditions"`
}
type Fixture struct {
	ID        string            `json:"id"`
	Fields    map[string]string `json:"fields"`
	WantMatch bool              `json:"wantMatch"`
}
type Mutation struct {
	ID, Action, OperationID string
	ExpectedRevision        uint64
	Declaration             *Declaration
	Cases                   []Fixture
}
type CopyInput struct {
	ID, OperationID, TemplateID, Name, Acknowledgement string
	ExpectedRevision, PublishedVersion                 uint64
	PublishedDigest                                    string
}
type Summary struct {
	CopySupported          bool      `json:"copySupported"`
	CopyUnsupportedFields  []string  `json:"copyUnsupportedFields"`
	ID                     string    `json:"id"`
	Revision               uint64    `json:"revision"`
	DraftVersion           uint64    `json:"draftVersion"`
	LatestPublishedVersion uint64    `json:"latestPublishedVersion"`
	ActiveVersion          uint64    `json:"activeVersion"`
	Archived               bool      `json:"archived"`
	TestPassed             *bool     `json:"testPassed"`
	LastEditorID           int64     `json:"lastEditorId"`
	UpdatedAt              time.Time `json:"updatedAt"`
}
type CaseResult struct {
	ID          string `json:"id"`
	WantMatch   bool   `json:"wantMatch"`
	ActualMatch bool   `json:"actualMatch"`
}
type Record struct {
	CaseResults []CaseResult `json:"caseResults"`
	Summary
	Contract          string               `json:"contract"`
	Simulation        bool                 `json:"simulation"`
	LearningSupported bool                 `json:"learningSupported"`
	TestedBy          int64                `json:"testedBy"`
	TestRevision      uint64               `json:"testRevision"`
	Snapshot          rulecatalog.Snapshot `json:"snapshot"`
	ActorID           int64                `json:"actorId"`
	Action            string               `json:"action"`
	Hash              string               `json:"hash"`
}
type Page struct {
	Items  []Summary `json:"items"`
	Count  int       `json:"count"`
	Offset int       `json:"offset"`
	Limit  int       `json:"limit"`
}
type Publication struct {
	CaseResults  []CaseResult          `json:"caseResults"`
	Rule         rulecatalog.Published `json:"rule"`
	ReviewedBy   int64                 `json:"reviewedBy"`
	LastEditorID int64                 `json:"lastEditorId"`
	TestedBy     int64                 `json:"testedBy"`
	TestRevision uint64                `json:"testRevision"`
	PublishedAt  time.Time             `json:"publishedAt"`
}
type Export struct {
	Simulation   bool          `json:"simulation"`
	Contract     string        `json:"contract"`
	ID           string        `json:"id"`
	Publications []Publication `json:"publications"`
}
type Origin struct {
	RuleID           string `json:"ruleId"`
	PublishedVersion uint64 `json:"publishedVersion"`
	PublishedDigest  string `json:"publishedDigest"`
	CatalogRevision  uint64 `json:"catalogRevision"`
	TemplateID       string `json:"templateId"`
	TemplateVersion  int64  `json:"templateVersion"`
	TemplateHash     string `json:"templateHash"`
	Acknowledgement  string `json:"acknowledgement"`
}

// Intent is a fixed-shape, server-normalized semantic request descriptor. Browser
// recovery compares values, not Go/JavaScript object serialization or key order.
type Intent struct {
	DomainID         string       `json:"domainId"`
	ID               string       `json:"id"`
	Action           string       `json:"action"`
	OperationID      string       `json:"operationId"`
	ExpectedRevision uint64       `json:"expectedRevision"`
	Declaration      *Declaration `json:"declaration"`
	Cases            []Fixture    `json:"cases"`
	PublishedVersion uint64       `json:"publishedVersion"`
	PublishedDigest  string       `json:"publishedDigest"`
	TemplateID       string       `json:"templateId"`
	Name             string       `json:"name"`
	Acknowledgement  string       `json:"acknowledgement"`
}
type Receipt struct {
	Intent      Intent                        `json:"intent"`
	RequestHash string                        `json:"requestHash"`
	Simulation  bool                          `json:"simulation"`
	OperationID string                        `json:"operationId"`
	Action      string                        `json:"action"`
	ActorID     int64                         `json:"actorId"`
	At          time.Time                     `json:"at"`
	Rule        *Summary                      `json:"rule,omitempty"`
	Template    *simulationdetection.Template `json:"template,omitempty"`
	Origin      *Origin                       `json:"origin,omitempty"`
}
type AuditFunc func(context.Context, pgx.Tx, Receipt) error
