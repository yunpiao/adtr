//go:build linux && cgroupdocker

package systemhealth

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Executed only by the required Docker harness; no skip or inferred success.
func TestDockerActualCgroupMemory(t *testing.T) {
	expected := os.Getenv("ADTR_TEST_CGROUP_LIMIT")
	if expected == "" {
		t.Fatal("Docker harness limit expectation required")
	}
	m := readContainerMemory(time.Now().UTC())
	if !validContainerMemory(&m, m.ObservedAt) {
		t.Fatal("invalid real observation")
	}
	if expected != "unconfigured" {
		if m.LimitBytes == nil || *m.LimitBytes != expected || m.Availability != Available || m.UsageBytes == nil || m.Percent == nil {
			t.Fatalf("Docker limit not observed %+v", m)
		}
	}
	// Independently inspect the namespace-local kernel control files. Private
	// namespace is Docker's default here; tests do not alter host cgroups.
	usageFile, limitFile := "/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory.max"
	if m.Source == "cgroup_v1" {
		usageFile, limitFile = "/sys/fs/cgroup/memory/memory.usage_in_bytes", "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	}
	usage, err := os.ReadFile(usageFile)
	if err != nil {
		t.Fatal(err)
	}
	limit, err := os.ReadFile(limitFile)
	if err != nil {
		t.Fatal(err)
	}
	if m.UsageBytes == nil {
		t.Fatalf("actual accounting absent %+v", m)
	}
	u, err := strconv.ParseUint(*m.UsageBytes, 10, 64)
	if err != nil || u == 0 {
		t.Fatal("real idle process still accounts memory")
	}
	if m.LimitBytes != nil {
		l, _ := strconv.ParseUint(*m.LimitBytes, 10, 64)
		if m.Percent == nil || *m.Percent != float64(u)/float64(l)*100 {
			t.Fatal("ratio does not match sampled real accounting")
		}
	}
	rawLimit := strings.TrimSpace(string(limit))
	unlimited := rawLimit == "max"
	if !unlimited {
		n, e := strconv.ParseUint(rawLimit, 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		unlimited = m.Source == "cgroup_v1" && n >= uint64(1<<63-1)-65535
	}
	if unlimited {
		if m.LimitState != "unlimited" || m.LimitBytes != nil || m.Percent != nil {
			t.Fatal("kernel unlimited limit did not agree with sample")
		}
	} else if m.LimitBytes == nil || *m.LimitBytes != rawLimit || m.LimitState != "finite" {
		t.Fatal("kernel finite limit did not agree with sample")
	}
	data, _ := json.Marshal(m)
	t.Logf("actual Docker memory=%s independent_current=%s independent_max=%s", data, usage, limit)
}
