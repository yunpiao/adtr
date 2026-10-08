package systemhealth

import "time"

const (
	OwnerTenantID       = "default"
	SampleCadence       = 15 * time.Second
	SampleStaleAfter    = 45 * time.Second
	WorkerStaleAfter    = 15 * time.Second
	MaxHistoryWindow    = 24 * time.Hour
	MaxHistoryPoints    = 5760
	DefaultAlarmPercent = 85
)

// Error contains only a stable public code, never database or filesystem details.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return e.Code }

type Current struct {
	Instance       string       `json:"instance"`
	Snapshot       *Snapshot    `json:"snapshot"`
	Availability   Availability `json:"availability"`
	Reason         string       `json:"reason,omitempty"`
	Stale          bool         `json:"stale"`
	AvailableSince *time.Time   `json:"availableSince"`
	CheckedAt      time.Time    `json:"checkedAt"`
}

type HistoryInput struct {
	Instance  string `json:"instance"`
	GraphType string `json:"graphType"`
	StorageID string `json:"storageId,omitempty"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
}
type DataStatistics struct {
	Max     *float64 `json:"max"`
	Avg     *float64 `json:"avg"`
	Min     *float64 `json:"min"`
	Current *float64 `json:"current"`
}
type HistoryData struct {
	Timestamp      []int64        `json:"timestamp"`
	Value          []string       `json:"value"`
	DataStatistics DataStatistics `json:"dataStatistics"`
}
type HistorySeries struct {
	Mode string      `json:"mode"`
	Data HistoryData `json:"data"`
}
type HistoryGap struct {
	StartTime int64 `json:"startTime"`
	EndTime   int64 `json:"endTime"`
}
type History struct {
	Instance              string          `json:"instance"`
	GraphType             string          `json:"graphType"`
	Info                  []HistorySeries `json:"info"`
	Gaps                  []HistoryGap    `json:"gaps"`
	AvailableSince        *time.Time      `json:"availableSince"`
	SampleIntervalSeconds int64           `json:"sampleIntervalSeconds"`
}

type AlarmInput struct {
	Instance         string `json:"instance"`
	MountID          string `json:"storageId"`
	Percent          int    `json:"percent"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type AlarmSetting struct {
	Instance     string     `json:"instance"`
	MountID      string     `json:"storageId"`
	AlarmPercent int        `json:"alarmPercent"`
	Revision     int64      `json:"revision"`
	UpdatedAt    *time.Time `json:"updatedAt"`
}
type StorageItem struct {
	StorageMetric
	AlarmPercent  int   `json:"alarmPercent"`
	Revision      int64 `json:"revision"`
	AlarmExceeded *bool `json:"alarmExceeded"`
}
type StorageView struct {
	Instance     string        `json:"instance"`
	Storage      []StorageItem `json:"storage"`
	Total        int           `json:"total"`
	Page         int           `json:"page"`
	PageSize     int           `json:"pageSize"`
	Availability Availability  `json:"availability"`
	Reason       string        `json:"reason,omitempty"`
	Stale        bool          `json:"stale"`
	ObservedAt   *time.Time    `json:"observedAt"`
	CheckedAt    time.Time     `json:"checkedAt"`
}

type WorkerCycle string

const (
	WorkerQueue     WorkerCycle = "queue"
	WorkerRecovery  WorkerCycle = "recovery"
	WorkerScheduler WorkerCycle = "scheduler"
)

type WorkerCycleStatus string

const (
	WorkerCycleSuccess  WorkerCycleStatus = "success"
	WorkerCycleFailure  WorkerCycleStatus = "failure"
	WorkerCycleProgress WorkerCycleStatus = "progress"
)

type WorkerCycleActivity struct {
	WorkerID       string            `json:"workerId"`
	Cycle          WorkerCycle       `json:"cycle"`
	Status         WorkerCycleStatus `json:"status"`
	Evidence       string            `json:"evidence"`
	LastActivityAt *time.Time        `json:"lastActivityAt"`
	LastSuccessAt  *time.Time        `json:"lastSuccessAt"`
	Availability   Availability      `json:"availability"`
	Reason         string            `json:"reason,omitempty"`
}

// StoreOptions contains trusted installation configuration, never request data.
// A nil StorageTargets slice registers only runtime-root. Empty disables it.
type StoreOptions struct {
	OwnerTenantID  string
	StorageTargets []StorageTarget
}
type WorkerActivity struct {
	Cycles            []WorkerCycleActivity `json:"cycles"`
	Availability      Availability          `json:"availability"`
	Reason            string                `json:"reason,omitempty"`
	CheckedAt         time.Time             `json:"checkedAt"`
	StaleAfterSeconds int64                 `json:"staleAfterSeconds"`
}
