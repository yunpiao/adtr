package systemhealth

import (
	"errors"
	"math"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Sampler serializes observations so CPU deltas and injected adapters remain
// race-safe. Its allowlist is copied on construction and cannot be changed by a
// sampling request. Keep one sampler per API process to retain CPU observations.
type Sampler struct {
	mu         sync.Mutex
	storage    []StorageTarget
	readProc   func(ProcFile) ([]byte, error)
	statFS     func(string) (FileSystemStats, error)
	now        func() time.Time
	previous   *cpuObservation
	previousAt time.Time
}

func NewSampler(config Config) (*Sampler, error) {
	targets := config.Storage
	if targets == nil {
		targets = []StorageTarget{{ID: RuntimeRootID, Path: "/", Mount: "/"}}
	}
	if len(targets) > MaxStorageTargets {
		return nil, ErrInvalidConfig
	}
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if !validID(target.ID) || seen[target.ID] || !validPath(target.Path) || !validLabel(target.Mount, 256) {
			return nil, ErrInvalidConfig
		}
		if target.ID == RuntimeRootID && (target.Path != "/" || target.Mount != "/") {
			return nil, ErrInvalidConfig
		}
		seen[target.ID] = true
	}
	if config.ReadProc == nil {
		config.ReadProc = readProc
	}
	if config.StatFS == nil {
		config.StatFS = statFS
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Sampler{
		storage: append([]StorageTarget(nil), targets...), readProc: config.ReadProc,
		statFS: config.StatFS, now: config.Now,
	}, nil
}

func validID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i, c := range value {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_') {
			continue
		}
		return false
	}
	return true
}

func validLabel(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func validPath(value string) bool {
	return validLabel(value, 4096) && strings.HasPrefix(value, "/") && path.Clean(value) == value
}

// Sample takes one real observation of every configured source. It neither
// sleeps to invent a CPU interval nor returns stale values after a read failure.
// CPU needs two successful observations; call it on the server's sample cadence.
func (s *Sampler) Sample() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	result := Snapshot{Instance: LocalInstanceID, ObservedAt: now, Storage: make([]StorageMetric, 0, len(s.storage))}
	result.CPU = s.sampleCPU(now)
	result.Memory = s.sampleMemory(now)
	result.Uptime = s.sampleUptime(now)
	result.Load = s.sampleLoad(now)
	for _, target := range s.storage {
		result.Storage = append(result.Storage, s.sampleStorage(now, target))
	}
	return result
}

func metadata(now time.Time, source, scope string) Metadata {
	return Metadata{ObservedAt: now, Source: source, Scope: scope, Availability: Available}
}

func unavailable(meta *Metadata, reason string) {
	meta.Availability, meta.Reason = Unavailable, reason
}

func failedRead(meta *Metadata, err error) {
	if errors.Is(err, ErrUnsupported) {
		meta.Availability, meta.Reason = Unsupported, "unsupported_platform"
		return
	}
	unavailable(meta, "read_failed")
}

func (s *Sampler) read(file ProcFile, meta *Metadata) ([]byte, bool) {
	data, err := s.readProc(file)
	if err != nil {
		failedRead(meta, err)
		return nil, false
	}
	if len(data) == 0 || len(data) > maxProcBytes {
		unavailable(meta, "invalid_data")
		return nil, false
	}
	return data, true
}

func (s *Sampler) sampleCPU(now time.Time) PercentMetric {
	result := PercentMetric{Metadata: metadata(now, "proc_stat", KernelScope)}
	data, ok := s.read(ProcStat, &result.Metadata)
	if !ok {
		s.previous = nil
		return result
	}
	current, err := parseCPU(data)
	if err != nil {
		s.previous = nil
		unavailable(&result.Metadata, "invalid_data")
		return result
	}
	previous, previousAt := s.previous, s.previousAt
	s.previous, s.previousAt = &current, now
	if previous == nil {
		result.Availability, result.Reason = WarmingUp, "first_observation"
		return result
	}
	if !now.After(previousAt) {
		unavailable(&result.Metadata, "clock_reset")
		return result
	}
	if current.count != previous.count {
		unavailable(&result.Metadata, "counter_reset")
		return result
	}
	for i, value := range current.counters {
		if value < previous.counters[i] {
			unavailable(&result.Metadata, "counter_reset")
			return result
		}
	}
	total := current.total - previous.total
	if total == 0 {
		unavailable(&result.Metadata, "zero_delta")
		return result
	}
	idle := current.counters[3] - previous.counters[3]
	iowait := current.counters[4] - previous.counters[4]
	if idle > total || iowait > total-idle {
		unavailable(&result.Metadata, "invalid_data")
		return result
	}
	percent := percentage(total-idle-iowait, total)
	result.Percent, result.IntervalStart = &percent, &previousAt
	return result
}

func (s *Sampler) sampleMemory(now time.Time) MemoryMetric {
	result := MemoryMetric{Metadata: metadata(now, "proc_meminfo", KernelScope)}
	data, ok := s.read(ProcMeminfo, &result.Metadata)
	if !ok {
		return result
	}
	total, available, reason := parseMemory(data)
	if reason != "" {
		unavailable(&result.Metadata, reason)
		return result
	}
	used := total - available
	percent := percentage(used, total)
	result.Percent = &percent
	result.TotalBytes, result.AvailableBytes, result.UsedBytes = decimal(total), decimal(available), decimal(used)
	return result
}

func (s *Sampler) sampleUptime(now time.Time) UptimeMetric {
	result := UptimeMetric{Metadata: metadata(now, "proc_uptime", KernelScope)}
	data, ok := s.read(ProcUptime, &result.Metadata)
	if !ok {
		return result
	}
	seconds, err := parseUptime(data)
	if err != nil {
		unavailable(&result.Metadata, "invalid_data")
		return result
	}
	result.Seconds = &seconds
	return result
}

func (s *Sampler) sampleLoad(now time.Time) LoadMetric {
	result := LoadMetric{Metadata: metadata(now, "proc_loadavg", KernelScope)}
	data, ok := s.read(ProcLoadavg, &result.Metadata)
	if !ok {
		return result
	}
	values, err := parseLoad(data)
	if err != nil {
		unavailable(&result.Metadata, "invalid_data")
		return result
	}
	result.Values = &values
	return result
}

func (s *Sampler) sampleStorage(now time.Time, target StorageTarget) StorageMetric {
	result := StorageMetric{Metadata: metadata(now, "statfs", FilesystemScope), ID: target.ID, Mount: target.Mount}
	stats, err := s.statFS(target.Path)
	if err != nil {
		failedRead(&result.Metadata, err)
		return result
	}
	if stats.BlockSize <= 0 || stats.FreeBlocks > stats.Blocks || stats.AvailableBlocks > stats.FreeBlocks ||
		stats.Blocks > math.MaxUint64/uint64(stats.BlockSize) || !validLabel(stats.FS, 64) {
		unavailable(&result.Metadata, "invalid_data")
		return result
	}
	result.FS = stats.FS
	size := uint64(stats.BlockSize)
	total := stats.Blocks * size
	used := (stats.Blocks - stats.FreeBlocks) * size
	free := stats.AvailableBlocks * size
	reserved := (stats.FreeBlocks - stats.AvailableBlocks) * size
	if total == 0 || used+free == 0 {
		unavailable(&result.Metadata, "zero_capacity")
		return result
	}
	percent := percentage(used, used+free)
	result.TotalBytes, result.UsedBytes, result.FreeBytes, result.ReservedBytes = decimal(total), decimal(used), decimal(free), decimal(reserved)
	result.Percent = &percent
	return result
}

func percentage(numerator, denominator uint64) float64 {
	return float64(numerator) / float64(denominator) * 100
}

func decimal(value uint64) *string {
	result := strconv.FormatUint(value, 10)
	return &result
}
