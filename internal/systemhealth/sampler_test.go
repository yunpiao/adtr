package systemhealth

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	data    map[ProcFile]string
	errors  map[ProcFile]error
	stats   FileSystemStats
	statErr error
	paths   []string
	now     time.Time
}

func newFixture(t *testing.T) (*fixture, *Sampler) {
	t.Helper()
	f := &fixture{
		data: map[ProcFile]string{
			ProcStat:    "cpu  100 10 20 200 30 4 6 0 5 2\ncpu0 0 0 0 0\n",
			ProcMeminfo: "MemTotal: 1000 kB\nMemFree: 10 kB\nMemAvailable: 250 kB\nCached: 400 kB\n",
			ProcUptime:  "123.99 456.78\n", ProcLoadavg: "1.25 2.5 3.75 1/99 12\n",
		},
		errors: make(map[ProcFile]error),
		stats:  FileSystemStats{BlockSize: 4096, Blocks: 100, FreeBlocks: 40, AvailableBlocks: 30, FS: "ext"},
		now:    time.Date(2026, 10, 7, 1, 2, 3, 0, time.FixedZone("fixture", 3600)),
	}
	sampler, err := NewSampler(Config{
		ReadProc: func(file ProcFile) ([]byte, error) { return []byte(f.data[file]), f.errors[file] },
		StatFS: func(path string) (FileSystemStats, error) {
			f.paths = append(f.paths, path)
			return f.stats, f.statErr
		},
		Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, sampler
}

func requirePercent(t *testing.T, value *float64, want float64) {
	t.Helper()
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || math.Abs(*value-want) > 1e-10 {
		t.Fatalf("percent = %v, want %.12f", value, want)
	}
}

func requireDecimal(t *testing.T, value *string, want string) {
	t.Helper()
	if value == nil || *value != want {
		t.Fatalf("decimal = %v, want %s", value, want)
	}
}

func TestSnapshotUsesActualSourceSemantics(t *testing.T) {
	f, sampler := newFixture(t)
	snapshot := sampler.Sample()
	if snapshot.Instance != LocalInstanceID || !snapshot.ObservedAt.Equal(f.now) || snapshot.ObservedAt.Location() != time.UTC {
		t.Fatalf("incorrect instance/time: %+v", snapshot)
	}
	if snapshot.CPU.Percent != nil || snapshot.CPU.Availability != WarmingUp || snapshot.CPU.Reason != "first_observation" {
		t.Fatalf("first CPU observation was fabricated: %+v", snapshot.CPU)
	}
	requirePercent(t, snapshot.Memory.Percent, 75)
	requireDecimal(t, snapshot.Memory.TotalBytes, "1024000")
	requireDecimal(t, snapshot.Memory.AvailableBytes, "256000")
	requireDecimal(t, snapshot.Memory.UsedBytes, "768000")
	if snapshot.Uptime.Seconds == nil || *snapshot.Uptime.Seconds != 123 {
		t.Fatalf("uptime must be elapsed kernel seconds: %+v", snapshot.Uptime)
	}
	if snapshot.Load.Values == nil || *snapshot.Load.Values != (LoadAverages{One: 1.25, Five: 2.5, Fifteen: 3.75}) {
		t.Fatalf("wrong load averages: %+v", snapshot.Load)
	}
	if len(snapshot.Storage) != 1 || len(f.paths) != 1 || f.paths[0] != "/" {
		t.Fatalf("wrong default storage source: %v, %v", snapshot.Storage, f.paths)
	}
	disk := snapshot.Storage[0]
	if disk.ID != RuntimeRootID || disk.Mount != "/" || disk.FS != "ext" {
		t.Fatalf("wrong storage labels: %+v", disk)
	}
	requireDecimal(t, disk.TotalBytes, "409600")
	requireDecimal(t, disk.UsedBytes, "245760")
	requireDecimal(t, disk.FreeBytes, "122880")
	requireDecimal(t, disk.ReservedBytes, "40960")
	requirePercent(t, disk.Percent, 100*2.0/3)
	for _, meta := range []Metadata{snapshot.CPU.Metadata, snapshot.Memory.Metadata, snapshot.Uptime.Metadata, snapshot.Load.Metadata, disk.Metadata} {
		if !meta.ObservedAt.Equal(f.now) || meta.Source == "" || meta.Scope == "" {
			t.Fatalf("missing metric provenance: %+v", meta)
		}
	}
	if snapshot.CPU.Scope != KernelScope || snapshot.Memory.Scope != KernelScope || disk.Scope != FilesystemScope {
		t.Fatal("kernel-visible resources and runtime filesystem scopes must remain explicit")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || !strings.Contains(string(encoded), `"percent":null`) || !strings.Contains(string(encoded), `"totalBytes":"409600"`) {
		t.Fatalf("nullable percentages/string bytes JSON invalid: %s; %v", encoded, err)
	}
}

func TestCPUDeltaExcludesGuestAndIOWait(t *testing.T) {
	f, sampler := newFixture(t)
	first := sampler.Sample()
	f.now = f.now.Add(time.Second)
	// Busy counters increase by 60; idle + iowait increase by 40. Guest
	// increases by 27 but is already in user/nice and must not be added.
	f.data[ProcStat] = "cpu 140 20 30 220 50 4 6 0 25 9\n"
	second := sampler.Sample()
	requirePercent(t, second.CPU.Percent, 60)
	if second.CPU.Availability != Available || second.CPU.Reason != "" || second.CPU.IntervalStart == nil || !second.CPU.IntervalStart.Equal(first.CPU.ObservedAt) {
		t.Fatalf("invalid CPU interval metadata: %+v", second.CPU)
	}
}

func TestCPUFailureResetAndRecovery(t *testing.T) {
	tests := []struct {
		name, stat, reason string
		err                error
		advance            bool
	}{
		{name: "zero delta", stat: "cpu 100 10 20 200 30 4 6 0 5 2", reason: "zero_delta", advance: true},
		{name: "counter reset", stat: "cpu 99 20 30 220 50 4 6 0 5 2", reason: "counter_reset", advance: true},
		{name: "shape reset", stat: "cpu 140 20 30 220", reason: "counter_reset", advance: true},
		{name: "clock reset", stat: "cpu 140 20 30 220 50 4 6 0 5 2", reason: "clock_reset"},
		{name: "malformed", stat: "cpu nope", reason: "invalid_data", advance: true},
		{name: "read failure", err: errors.New("secret /private/path"), reason: "read_failed", advance: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f, sampler := newFixture(t)
			sampler.Sample()
			if test.advance {
				f.now = f.now.Add(time.Second)
			}
			f.data[ProcStat], f.errors[ProcStat] = test.stat, test.err
			value := sampler.Sample().CPU
			if value.Percent != nil || value.Availability != Unavailable || value.Reason != test.reason || value.IntervalStart != nil {
				t.Fatalf("invalid CPU value after %s: %+v", test.name, value)
			}
			if test.reason == "invalid_data" || test.reason == "read_failed" {
				f.errors[ProcStat], f.data[ProcStat] = nil, "cpu 140 20 30 220 50 4 6 0 5 2"
				f.now = f.now.Add(time.Second)
				value = sampler.Sample().CPU
				if value.Availability != WarmingUp || value.Percent != nil {
					t.Fatalf("recovery used a stale baseline: %+v", value)
				}
			}
		})
	}
}

func TestStorageBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		stats  FileSystemStats
		reason string
	}{
		{"negative block size", FileSystemStats{BlockSize: -1, Blocks: 100, FS: "ext"}, "invalid_data"},
		{"zero block size", FileSystemStats{BlockSize: 0, Blocks: 100, FS: "ext"}, "invalid_data"},
		{"overflow", FileSystemStats{BlockSize: 4096, Blocks: math.MaxUint64, FS: "ext"}, "invalid_data"},
		{"free exceeds total", FileSystemStats{BlockSize: 1, Blocks: 100, FreeBlocks: 101, FS: "ext"}, "invalid_data"},
		{"available exceeds free", FileSystemStats{BlockSize: 1, Blocks: 100, FreeBlocks: 40, AvailableBlocks: 41, FS: "ext"}, "invalid_data"},
		{"zero capacity", FileSystemStats{BlockSize: 4096, FS: "ext"}, "zero_capacity"},
		{"only reserved", FileSystemStats{BlockSize: 1, Blocks: 100, FreeBlocks: 100, FS: "ext"}, "zero_capacity"},
		{"invalid fs label", FileSystemStats{BlockSize: 1, Blocks: 100, FS: "ext\nunsafe"}, "invalid_data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f, sampler := newFixture(t)
			f.stats = test.stats
			disk := sampler.Sample().Storage[0]
			if disk.Availability != Unavailable || disk.Reason != test.reason || disk.Percent != nil || disk.TotalBytes != nil || disk.UsedBytes != nil || disk.FreeBytes != nil || disk.ReservedBytes != nil {
				t.Fatalf("invalid filesystem stats became measurements: %+v", disk)
			}
		})
	}
	f, sampler := newFixture(t)
	f.stats = FileSystemStats{BlockSize: 1, Blocks: math.MaxUint64, FreeBlocks: 0, AvailableBlocks: 0, FS: "ext"}
	disk := sampler.Sample().Storage[0]
	requireDecimal(t, disk.TotalBytes, "18446744073709551615")
	requireDecimal(t, disk.UsedBytes, "18446744073709551615")
	requirePercent(t, disk.Percent, 100)
	f.stats = FileSystemStats{BlockSize: 1, Blocks: 100, FreeBlocks: 100, AvailableBlocks: 100, FS: "ext"}
	disk = sampler.Sample().Storage[0]
	requirePercent(t, disk.Percent, 0)
}

func TestStorageRegistrationAllowlist(t *testing.T) {
	tests := []StorageTarget{
		{ID: "", Path: "/", Mount: "/"}, {ID: "../disk", Path: "/", Mount: "/"},
		{ID: "https://disk", Path: "/", Mount: "/"}, {ID: "UPPER", Path: "/", Mount: "/"},
		{ID: strings.Repeat("a", 65), Path: "/", Mount: "/"}, {ID: "disk", Path: "relative", Mount: "/"},
		{ID: "disk", Path: "/a/../secret", Mount: "/"}, {ID: "disk", Path: "/a\x00", Mount: "/"},
		{ID: "disk", Path: "/", Mount: ""}, {ID: "disk", Path: "/", Mount: "mount\n"},
		{ID: RuntimeRootID, Path: "/database", Mount: "/"}, {ID: RuntimeRootID, Path: "/", Mount: "database"},
	}
	for _, target := range tests {
		if _, err := NewSampler(Config{Storage: []StorageTarget{target}}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("accepted invalid target %+v: %v", target, err)
		}
	}
	duplicate := StorageTarget{ID: "disk", Path: "/trusted", Mount: "data"}
	if _, err := NewSampler(Config{Storage: []StorageTarget{duplicate, duplicate}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("accepted duplicate IDs")
	}
	targets := make([]StorageTarget, MaxStorageTargets+1)
	for i := range targets {
		targets[i] = StorageTarget{ID: fmt.Sprintf("disk-%d", i), Path: "/", Mount: "disk"}
	}
	if _, err := NewSampler(Config{Storage: targets}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("accepted more than 32 targets")
	}
	var calls []string
	sampler, err := NewSampler(Config{Storage: targets[:MaxStorageTargets], StatFS: func(path string) (FileSystemStats, error) {
		calls = append(calls, path)
		return FileSystemStats{}, errors.New("failed")
	}})
	if err != nil {
		t.Fatal(err)
	}
	targets[0].Path = "/mutated-after-registration"
	snapshot := sampler.Sample()
	if len(snapshot.Storage) != MaxStorageTargets || len(calls) != MaxStorageTargets || calls[0] != "/" {
		t.Fatal("registrations were not bounded and copied")
	}
	noStorage, err := NewSampler(Config{Storage: []StorageTarget{}, StatFS: func(string) (FileSystemStats, error) {
		t.Fatal("disabled storage performed I/O")
		return FileSystemStats{}, nil
	}})
	if err != nil || len(noStorage.Sample().Storage) != 0 {
		t.Fatal("explicitly empty storage allowlist was ignored")
	}
}

func TestErrorsAreSafeAndFailuresAreIsolated(t *testing.T) {
	f, sampler := newFixture(t)
	sampler.Sample()
	f.errors[ProcStat] = errors.New("read /private/credential?token=secret failed")
	f.statErr = errors.New("postgres://user:secret@private-database/data")
	snapshot := sampler.Sample()
	if snapshot.CPU.Reason != "read_failed" || snapshot.Storage[0].Reason != "read_failed" || snapshot.Memory.Availability != Available {
		t.Fatalf("source failure isolation invalid: %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private", "secret", "postgres", "credential"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("raw error leaked: %s", encoded)
		}
	}
	for _, file := range []ProcFile{ProcStat, ProcMeminfo, ProcUptime, ProcLoadavg} {
		f.errors[file] = fmt.Errorf("wrapped: %w", ErrUnsupported)
	}
	f.statErr = ErrUnsupported
	snapshot = sampler.Sample()
	for _, meta := range []Metadata{snapshot.CPU.Metadata, snapshot.Memory.Metadata, snapshot.Uptime.Metadata, snapshot.Load.Metadata, snapshot.Storage[0].Metadata} {
		if meta.Availability != Unsupported || meta.Reason != "unsupported_platform" {
			t.Fatalf("unsupported source not explicit: %+v", meta)
		}
	}
}

func TestOversizedAndEmptyProcSourcesAreUnavailable(t *testing.T) {
	for _, content := range []string{"", strings.Repeat("0", maxProcBytes+1)} {
		f, sampler := newFixture(t)
		for _, file := range []ProcFile{ProcStat, ProcMeminfo, ProcUptime, ProcLoadavg} {
			f.data[file] = content
		}
		snapshot := sampler.Sample()
		for _, meta := range []Metadata{snapshot.CPU.Metadata, snapshot.Memory.Metadata, snapshot.Uptime.Metadata, snapshot.Load.Metadata} {
			if meta.Availability != Unavailable || meta.Reason != "invalid_data" {
				t.Fatalf("unbounded or empty source accepted: %+v", meta)
			}
		}
	}
}

func TestSamplerConcurrentObservations(t *testing.T) {
	var tick uint64
	sampler, err := NewSampler(Config{
		Storage: []StorageTarget{},
		Now:     func() time.Time { tick++; return time.Unix(int64(tick), 0) },
		ReadProc: func(file ProcFile) ([]byte, error) {
			switch file {
			case ProcStat:
				return fmt.Appendf(nil, "cpu %d 0 0 %d 0 0 0 0 0 0", tick, tick), nil
			case ProcMeminfo:
				return []byte("MemTotal: 100 kB\nMemAvailable: 50 kB\n"), nil
			case ProcUptime:
				return []byte("1.0 1.0"), nil
			case ProcLoadavg:
				return []byte("0 0 0 1/1 1"), nil
			default:
				return nil, errInvalidData
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan Snapshot, 32)
	for range 32 {
		workers.Go(func() { results <- sampler.Sample() })
	}
	workers.Wait()
	close(results)
	warming, ready := 0, 0
	for result := range results {
		if result.CPU.Availability == WarmingUp {
			warming++
		} else if result.CPU.Availability == Available {
			ready++
			requirePercent(t, result.CPU.Percent, 50)
		} else {
			t.Fatalf("concurrent sample invalid: %+v", result.CPU)
		}
	}
	if warming != 1 || ready != 31 {
		t.Fatalf("observations were not serialized: warming=%d ready=%d", warming, ready)
	}
}
