// Package systemhealth samples the API runtime's kernel-visible resources.
// It does not discover remote nodes, inspect database storage, execute commands,
// or manufacture historical measurements.
package systemhealth

import (
	"errors"
	"time"
)

const (
	LocalInstanceID   = "local-api"
	RuntimeRootID     = "runtime-root"
	KernelScope       = "kernel_visible"
	FilesystemScope   = "runtime_filesystem"
	MaxStorageTargets = 32
	maxProcBytes      = 1 << 20
)

// Availability distinguishes an actual measurement from a missing value.
type Availability string

const (
	Available   Availability = "available"
	Unavailable Availability = "unavailable"
	WarmingUp   Availability = "warming_up"
	Unsupported Availability = "unsupported"
)

// Metadata describes the observation, including failed attempts. Reason is a
// stable safe code; underlying OS errors and configured paths are never exposed.
type Metadata struct {
	ObservedAt   time.Time    `json:"observedAt"`
	Source       string       `json:"source"`
	Scope        string       `json:"scope"`
	Availability Availability `json:"availability"`
	Reason       string       `json:"reason,omitempty"`
}

type PercentMetric struct {
	Metadata
	Percent       *float64   `json:"percent"`
	IntervalStart *time.Time `json:"intervalStart,omitempty"`
}

type MemoryMetric struct {
	Metadata
	Percent        *float64 `json:"percent"`
	TotalBytes     *string  `json:"totalBytes"`
	AvailableBytes *string  `json:"availableBytes"`
	UsedBytes      *string  `json:"usedBytes"`
}

type UptimeMetric struct {
	Metadata
	Seconds *uint64 `json:"seconds"`
}

type LoadAverages struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

type LoadMetric struct {
	Metadata
	Values *LoadAverages `json:"values"`
}

type StorageMetric struct {
	Metadata
	ID            string   `json:"id"`
	Mount         string   `json:"mount"`
	FS            string   `json:"fs"`
	TotalBytes    *string  `json:"totalBytes"`
	UsedBytes     *string  `json:"usedBytes"`
	FreeBytes     *string  `json:"freeBytes"`
	ReservedBytes *string  `json:"reservedBytes"`
	Percent       *float64 `json:"percent"`
}

type Snapshot struct {
	Instance   string          `json:"instance"`
	ObservedAt time.Time       `json:"observedAt"`
	CPU        PercentMetric   `json:"cpu"`
	Memory     MemoryMetric    `json:"memory"`
	Uptime     UptimeMetric    `json:"uptime"`
	Load       LoadMetric      `json:"load"`
	Storage    []StorageMetric `json:"storage"`
}

// ProcFile identifies one fixed kernel source, never an arbitrary path.
type ProcFile string

const (
	ProcStat    ProcFile = "stat"
	ProcMeminfo ProcFile = "meminfo"
	ProcUptime  ProcFile = "uptime"
	ProcLoadavg ProcFile = "loadavg"
)

// FileSystemStats uses statfs blocks. FreeBlocks includes reserved space;
// AvailableBlocks excludes it. FS is a filesystem-type label, never a device URL.
type FileSystemStats struct {
	BlockSize       int64
	Blocks          uint64
	FreeBlocks      uint64
	AvailableBlocks uint64
	FS              string
}

// StorageTarget is a trusted server-startup registration. Path stays private;
// Mount is its public display label. Never construct these from request data.
// runtime-root is reserved for Path "/" and Mount "/".
type StorageTarget struct {
	ID    string
	Path  string
	Mount string
}

// Config is trusted startup configuration. A nil Storage slice registers only
// runtime-root; an explicitly empty slice disables storage sampling. Hooks are
// for deterministic tests or trusted adapters, not request-controlled I/O.
type Config struct {
	Storage  []StorageTarget
	ReadProc func(ProcFile) ([]byte, error)
	StatFS   func(string) (FileSystemStats, error)
	Now      func() time.Time
}

var (
	ErrInvalidConfig = errors.New("invalid system health sampler configuration")
	ErrUnsupported   = errors.New("system health sampling unsupported")
)
