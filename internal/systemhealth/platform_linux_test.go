//go:build linux

package systemhealth

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This is real local /proc + statfs evidence, not a fixture or an AD/Windows test.
// Capture the exact reads so assertions are not racy with changing host counters.
func TestRealLinuxSample(t *testing.T) {
	var cpuReads []string
	var memoryRead string
	var storageRead FileSystemStats
	sampler, err := NewSampler(Config{
		ReadProc: func(file ProcFile) ([]byte, error) {
			data, err := readProc(file)
			if file == ProcStat {
				cpuReads = append(cpuReads, string(data))
			}
			if file == ProcMeminfo {
				memoryRead = string(data)
			}
			return data, err
		},
		StatFS: func(path string) (FileSystemStats, error) {
			if path != "/" {
				t.Fatalf("runtime-root used %q", path)
			}
			value, err := statFS(path)
			storageRead = value
			return value, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := sampler.Sample()
	if first.CPU.Availability != WarmingUp || first.CPU.Percent != nil {
		t.Fatalf("real CPU did not warm up: %+v", first.CPU)
	}
	time.Sleep(25 * time.Millisecond)
	snapshot := sampler.Sample()
	for _, meta := range []Metadata{snapshot.CPU.Metadata, snapshot.Memory.Metadata, snapshot.Uptime.Metadata, snapshot.Load.Metadata, snapshot.Storage[0].Metadata} {
		if meta.Availability != Available {
			t.Fatalf("real Linux sampling unavailable (not skipped): %+v", meta)
		}
	}
	// Independent arithmetic directly over the captured kernel counters.
	parseCounters := func(raw string) (total, idle uint64) {
		line, _, _ := strings.Cut(raw, "\n")
		fields := strings.Fields(line)
		for i := 1; i <= 8; i++ {
			number, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			total += number
			if i == 4 || i == 5 {
				idle += number
			}
		}
		return
	}
	if len(cpuReads) != 2 {
		t.Fatal("expected two actual CPU observations")
	}
	total1, idle1 := parseCounters(cpuReads[0])
	total2, idle2 := parseCounters(cpuReads[1])
	wantCPU := float64((total2-total1)-(idle2-idle1)) / float64(total2-total1) * 100
	requirePercent(t, snapshot.CPU.Percent, wantCPU)
	var totalKB, availableKB uint64
	for _, line := range strings.Split(memoryRead, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		if fields[0] == "MemTotal:" || fields[0] == "MemAvailable:" {
			number, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if fields[0] == "MemTotal:" {
				totalKB = number
			} else {
				availableKB = number
			}
		}
	}
	requirePercent(t, snapshot.Memory.Percent, float64(totalKB-availableKB)/float64(totalKB)*100)
	requireDecimal(t, snapshot.Memory.TotalBytes, strconv.FormatUint(totalKB*1024, 10))
	disk := snapshot.Storage[0]
	requireDecimal(t, disk.TotalBytes, strconv.FormatUint(storageRead.Blocks*uint64(storageRead.BlockSize), 10))
	requireDecimal(t, disk.UsedBytes, strconv.FormatUint((storageRead.Blocks-storageRead.FreeBlocks)*uint64(storageRead.BlockSize), 10))
	requireDecimal(t, disk.FreeBytes, strconv.FormatUint(storageRead.AvailableBlocks*uint64(storageRead.BlockSize), 10))
	requireDecimal(t, disk.ReservedBytes, strconv.FormatUint((storageRead.FreeBlocks-storageRead.AvailableBlocks)*uint64(storageRead.BlockSize), 10))
	if snapshot.Uptime.Seconds == nil || *snapshot.Uptime.Seconds == 0 || snapshot.Load.Values == nil || math.IsNaN(snapshot.Load.Values.One) {
		t.Fatal("real uptime/load missing")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("real local Linux observation: %s", encoded)
}

func TestLinuxProcAllowlist(t *testing.T) {
	for _, source := range []ProcFile{"../etc/passwd", "/etc/passwd", "https://example.invalid", "", "stat/../meminfo"} {
		if data, err := readProc(source); err == nil || data != nil {
			t.Fatalf("read unregistered proc source %q", source)
		}
	}
}

func TestFilesystemTypeLabels(t *testing.T) {
	for _, test := range []struct {
		kind uint64
		want string
	}{{0xef53, "ext"}, {0x794c7630, "overlay"}, {0x123456, "type-0x123456"}} {
		if got := filesystemName(test.kind); got != test.want {
			t.Errorf("filesystem type label %x = %q, want %q", test.kind, got, test.want)
		}
	}
}
