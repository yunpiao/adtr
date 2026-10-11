package systemhealth

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func cgroupFixture() map[string]string {
	return map[string]string{
		"/proc/self/cgroup":                          "0::/parent/child\n",
		"/proc/self/mountinfo":                       "29 23 0:26 / /sys/fs/cgroup ro - cgroup2 cgroup rw\n",
		"/sys/fs/cgroup/parent/child/memory.current": "60\n",
		"/sys/fs/cgroup/parent/child/memory.max":     "100\n",
		"/sys/fs/cgroup/parent/memory.max":           "80\n",
		"/sys/fs/cgroup/memory.max":                  "max\n",
	}
}
func sampleFixture(files map[string]string) ContainerMemoryMetric {
	return sampleCgroup(time.Unix(123, 0).UTC(), func(p string) ([]byte, error) {
		v, ok := files[p]
		if !ok {
			return nil, errors.New("private source read failed")
		}
		return []byte(v), nil
	}, fixtureCgroupIdentity)
}
func TestCgroupVisibleHierarchyAndNamespace(t *testing.T) {
	files := cgroupFixture()
	m := sampleFixture(files)
	if m.Source != "cgroup_v2" || m.LimitState != "finite" || m.Hierarchy != "visible" || !validContainerMemory(&m, m.ObservedAt) {
		t.Fatalf("invalid metric %+v", m)
	}
	requireDecimal(t, m.UsageBytes, "60")
	requireDecimal(t, m.LimitBytes, "80")
	requirePercent(t, m.Percent, 75)
	files["/sys/fs/cgroup/parent/child/memory.current"] = "120"
	m = sampleFixture(files)
	requirePercent(t, m.Percent, 150)
	files = map[string]string{"/proc/self/cgroup": "0::/\n", "/proc/self/mountinfo": "29 23 0:26 / /sys/fs/cgroup ro - cgroup2 cgroup rw\n", "/sys/fs/cgroup/memory.current": "25", "/sys/fs/cgroup/memory.max": "100"}
	m = sampleFixture(files)
	requirePercent(t, m.Percent, 25)
	files["/proc/self/cgroup"] = "0::/docker/id\n"
	files["/proc/self/mountinfo"] = "29 23 0:26 /docker/id /sys/fs/cgroup ro - cgroup2 cgroup rw\n"
	m = sampleFixture(files)
	requirePercent(t, m.Percent, 25)
}
func TestCgroupUnknownUnlimitedAndFailuresNeverInventRatio(t *testing.T) {
	for _, tc := range []struct{ name, key, value, reason string }{
		{"unlimited leaf", "/sys/fs/cgroup/parent/child/memory.max", "max", ""},
		{"corrupt limit", "/sys/fs/cgroup/parent/memory.max", "broken", "invalid_data"},
		{"zero limit", "/sys/fs/cgroup/parent/memory.max", "0", "zero_limit"},
		{"negative usage", "/sys/fs/cgroup/parent/child/memory.current", "-1", "invalid_data"},
		{"overflow", "/sys/fs/cgroup/parent/child/memory.current", "18446744073709551616", "invalid_data"},
		{"traversal", "/proc/self/cgroup", "0::/../../secret", "invalid_data"},
		{"ambiguous", "/proc/self/cgroup", "0::/\n0::/other", "invalid_data"},
		{"deleted", "/proc/self/cgroup", "0::/parent/child (deleted)", "read_failed"},
		{"oversized", "/sys/fs/cgroup/parent/child/memory.current", strings.Repeat("1", maxProcBytes+1), "invalid_data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := cgroupFixture()
			f[tc.key] = tc.value
			m := sampleFixture(f)
			if tc.reason == "" {
				requireDecimal(t, m.LimitBytes, "80")
				return
			}
			if m.Availability == Available || m.Reason != tc.reason || m.Percent != nil || m.UsageBytes != nil || m.LimitBytes != nil {
				t.Fatalf("failed data became measurement %+v", m)
			}
			if !validContainerMemory(&m, m.ObservedAt) {
				t.Fatal("failed metric not valid")
			}
		})
	}
	f := cgroupFixture()
	for p := range f {
		if strings.HasSuffix(p, "memory.max") {
			f[p] = "max"
		}
	}
	m := sampleFixture(f)
	if m.LimitState != "unlimited" || m.Availability != Unavailable || m.Reason != "unlimited" || m.Percent != nil || m.LimitBytes != nil {
		t.Fatalf("unlimited misrepresented %+v", m)
	}
	requireDecimal(t, m.UsageBytes, "60")
	delete(f, "/sys/fs/cgroup/parent/memory.max")
	m = sampleFixture(f)
	if m.Reason != "read_failed" || m.Percent != nil {
		t.Fatal("unreadable parent ignored")
	}
}
func TestCgroupV1UsesVisibleHierarchyAndExplicitAccounting(t *testing.T) {
	f := map[string]string{"/proc/self/cgroup": "3:cpu,memory:/child\n", "/proc/self/mountinfo": "29 23 0:26 / /sys/fs/cgroup/memory ro - cgroup cgroup rw,memory\n", "/sys/fs/cgroup/memory/child/memory.usage_in_bytes": "50", "/sys/fs/cgroup/memory/child/memory.limit_in_bytes": "100", "/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712", "/sys/fs/cgroup/memory/child/memory.use_hierarchy": "1", "/sys/fs/cgroup/memory/memory.use_hierarchy": "1"}
	m := sampleFixture(f)
	if m.Source != "cgroup_v1" {
		t.Fatal(m)
	}
	requirePercent(t, m.Percent, 50)
	f["/sys/fs/cgroup/memory/memory.use_hierarchy"] = "0"
	m = sampleFixture(f)
	if m.Reason != "hierarchy_disabled" || m.Percent != nil {
		t.Fatal("disabled ancestor ignored", m)
	}
	f["/sys/fs/cgroup/memory/memory.use_hierarchy"] = "1"
	f["/sys/fs/cgroup/memory/child/memory.use_hierarchy"] = "0"
	m = sampleFixture(f)
	if m.Reason != "hierarchy_disabled" || m.Percent != nil {
		t.Fatal(m)
	}
}
func TestCgroupMembershipChangeAndLegacyCompatibility(t *testing.T) {
	f := cgroupFixture()
	calls := 0
	m := sampleCgroup(time.Unix(123, 0), func(p string) ([]byte, error) {
		if p == "/proc/self/cgroup" {
			calls++
			if calls > 1 {
				return []byte("0::/moved"), nil
			}
		}
		return []byte(f[p]), nil
	}, fixtureCgroupIdentity)
	if m.Reason != "cgroup_changed" || m.Percent != nil {
		t.Fatal(m)
	}
	if !validContainerMemory(nil, time.Time{}) {
		t.Fatal("old persisted snapshot rejected")
	}
	m = sampleFixture(f)
	bad := m
	wrong := 0.0
	bad.Percent = &wrong
	if validContainerMemory(&bad, bad.ObservedAt) {
		t.Fatal("inconsistent stored ratio accepted")
	}
}

func TestCgroupHostRootHasNoMemoryLimitFiles(t *testing.T) {
	f := cgroupFixture()
	delete(f, "/sys/fs/cgroup/memory.max")
	f["/sys/fs/cgroup/cgroup.controllers"] = "cpu memory pids"
	read := func(p string) ([]byte, error) {
		v, ok := f[p]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return []byte(v), nil
	}
	m := sampleCgroup(time.Unix(123, 0), read, fixtureCgroupIdentity)
	requirePercent(t, m.Percent, 75)
	f["/sys/fs/cgroup/memory.current"] = "500"
	m = sampleCgroup(time.Unix(123, 0), read, fixtureCgroupIdentity)
	if m.Availability == Available || m.Percent != nil {
		t.Fatal("missing limit on accounting-enabled root was ignored")
	}
	delete(f, "/sys/fs/cgroup/memory.current")
	delete(f, "/sys/fs/cgroup/cgroup.controllers")
	m = sampleCgroup(time.Unix(123, 0), read, fixtureCgroupIdentity)
	if m.Availability == Available {
		t.Fatal("unverified root assumed unlimited")
	}
}

func fixtureCgroupIdentity(string) (string, error) { return strings.Repeat("a", 64), nil }

func TestCgroupIdentityChangeReadFailureAndInvalidIdentity(t *testing.T) {
	files := cgroupFixture()
	read := func(p string) ([]byte, error) { return []byte(files[p]), nil }
	for _, mode := range []string{"changed", "failed", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			m := sampleCgroup(time.Unix(123, 0), read, func(string) (string, error) {
				calls++
				if mode == "failed" {
					return "", errors.New("secret inaccessible namespace")
				}
				if mode == "invalid" {
					return "internal-path", nil
				}
				if calls == 2 {
					return strings.Repeat("b", 64), nil
				}
				return fixtureCgroupIdentity("")
			})
			if m.Availability == Available || m.SourceID != "" || m.Percent != nil {
				t.Fatalf("uncertain source became trustworthy %+v", m)
			}
		})
	}
}

func TestNoCgroupHierarchyIsNotUnsupportedLinux(t *testing.T) {
	f := cgroupFixture()
	f["/proc/self/mountinfo"] = "1 0 0:1 / / rw - ext4 /dev/test rw\n"
	m := sampleFixture(f)
	if m.Availability != Unavailable || m.Reason != "cgroup_not_found" || m.Percent != nil {
		t.Fatal(m)
	}
}
