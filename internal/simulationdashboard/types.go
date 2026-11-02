// Package simulationdashboard composes bounded synthetic evidence, never production health.
package simulationdashboard

import (
	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"time"
)

const Contract = "adtr.dashboard.simulated.v1"

var ErrInvalid = simulationevents.ErrInvalid
var ErrDenied = simulationevents.ErrDenied
var ErrNotFound = simulationevents.ErrNotFound
var ErrLimit = simulationevents.ErrLimit
var ErrConflict = simulationevents.ErrConflict

// Scope must be derived from live checks of all three simulation read grants.
// AuthorizationVersion is the current detection task authorization epoch.
type Scope struct {
	Tenant, Domain       string
	ActorID              int64
	AuthorizationVersion string
	Read                 bool
}
type Input struct{ DatasetID, Subject, Window string }
type Window struct {
	Preset        string    `json:"preset"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	BucketSeconds int       `json:"bucketSeconds"`
}
type Coverage struct {
	Complete             bool       `json:"complete"`
	EventCount           int        `json:"eventCount"`
	CaseCount            int        `json:"caseCount"`
	SourceFirstAt        *time.Time `json:"sourceFirstAt"`
	SourceLastAt         *time.Time `json:"sourceLastAt"`
	ExcludedFutureEvents int        `json:"excludedFutureEvents"`
}
type Marks struct {
	Unmarked   int `json:"unmarked"`
	Benign     int `json:"benign"`
	Suspicious int `json:"suspicious"`
}
type Cases struct {
	Scope  string `json:"scope"`
	Total  int    `json:"total"`
	Open   int    `json:"open"`
	Closed int    `json:"closed"`
	Marks  Marks  `json:"marks"`
}
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}
type ActivityBucket struct {
	Start        time.Time      `json:"start"`
	CountsByKind map[string]int `json:"countsByKind"`
}
type Activity struct {
	Scope           string           `json:"scope"`
	Total           int              `json:"total"`
	ByKind          []KindCount      `json:"byKind"`
	Buckets         []ActivityBucket `json:"buckets"`
	TopActors       []Count          `json:"topActors"`
	TopDevices      []Count          `json:"topDevices"`
	TopSourceIPs    []Count          `json:"topSourceIPs"`
	TopLimit        int              `json:"topLimit"`
	DistinctActors  int              `json:"distinctActors"`
	DistinctDevices int              `json:"distinctDevices"`
}
type CloseBucket struct {
	Start time.Time `json:"start"`
	Count int       `json:"count"`
}
type Throughput struct {
	Status        string        `json:"status"`
	Unit          string        `json:"unit"`
	WindowTotal   int           `json:"windowTotal"`
	Last60Seconds int           `json:"last60Seconds"`
	Buckets       []CloseBucket `json:"buckets"`
}
type Risk struct {
	Status         string            `json:"status"`
	Subject        string            `json:"subject,omitempty"`
	MappingVersion string            `json:"mappingVersion,omitempty"`
	Revision       uint64            `json:"revision,omitempty"`
	Score          *int              `json:"score,omitempty"`
	Trend          []riskscore.Point `json:"trend"`
	ResetBoundary  *time.Time        `json:"resetBoundary"`
}
type Detection struct {
	Status          string     `json:"status"`
	TaskUUID        string     `json:"taskUUID,omitempty"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
	TemplateID      string     `json:"templateId,omitempty"`
	TemplateVersion int64      `json:"templateVersion,omitempty"`
	TemplateHash    string     `json:"templateHash,omitempty"`
	EventCount      *int       `json:"eventCount,omitempty"`
	MatchCount      *int       `json:"matchCount,omitempty"`
}
type Unavailable struct {
	Metric string `json:"metric"`
	Reason string `json:"reason"`
}
type Summary struct {
	Contract              string                   `json:"contract"`
	Simulation            bool                     `json:"simulation"`
	ProductAccepted       bool                     `json:"productAccepted"`
	SnapshotID            string                   `json:"snapshotId"`
	AsOf                  time.Time                `json:"asOf"`
	Window                Window                   `json:"window"`
	Dataset               simulationevents.Dataset `json:"dataset"`
	Coverage              Coverage                 `json:"coverage"`
	Cases                 Cases                    `json:"cases"`
	EventActivity         Activity                 `json:"eventActivity"`
	CaseCloseThroughput   Throughput               `json:"caseCloseThroughput"`
	Risk                  Risk                     `json:"risk"`
	LatestManualDetection Detection                `json:"latestManualDetection"`
	Unavailable           []Unavailable            `json:"unavailable"`
}
