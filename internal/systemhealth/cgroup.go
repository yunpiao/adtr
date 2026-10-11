package systemhealth

import (
	"errors"
	"io/fs"
	"math"
	"path"
	"strings"
	"time"
)

// ContainerMemoryMetric is cgroup accounting, not process RSS or host RAM.
// The limit is the tightest visible ancestor limit; hidden ancestors and shared
// ancestor consumers can impose additional pressure, so this is not headroom.
type ContainerMemoryMetric struct {
	Metadata
	UsageBytes *string  `json:"usageBytes"`
	LimitBytes *string  `json:"limitBytes"`
	Percent    *float64 `json:"percent"`
	LimitState string   `json:"limitState"`
	Hierarchy  string   `json:"hierarchy"`
	SourceID   string   `json:"sourceID,omitempty"`
}

var errCgroupNotFound = errors.New("cgroup memory hierarchy unavailable")

type cgroupLocation struct{ version, mount, directory, mountRoot string }

func cgroupMountPath(value string) (string, error) {
	value = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
	if !validPath(value) {
		return "", errInvalidData
	}
	return value, nil
}
func resolveCgroup(membership, mounts []byte) (cgroupLocation, error) {
	if len(membership) == 0 || len(membership) > maxProcBytes || len(mounts) == 0 || len(mounts) > maxProcBytes {
		return cgroupLocation{}, errInvalidData
	}
	paths := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(membership)), "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 || !validPath(f[2]) {
			return cgroupLocation{}, errInvalidData
		}
		version := ""
		if f[0] == "0" && f[1] == "" {
			version = "v2"
		} else if containsCSV(f[1], "memory") {
			version = "v1"
		}
		if version != "" {
			if _, ok := paths[version]; ok {
				return cgroupLocation{}, errInvalidData
			}
			paths[version] = f[2]
		}
	}
	// Prefer the explicitly registered v1 memory controller on hybrid systems.
	for _, version := range []string{"v1", "v2"} {
		member, ok := paths[version]
		if !ok {
			continue
		}
		var found *cgroupLocation
		for _, line := range strings.Split(string(mounts), "\n") {
			before, after, ok := strings.Cut(line, " - ")
			if !ok {
				continue
			}
			a, b := strings.Fields(before), strings.Fields(after)
			if len(a) < 6 || len(b) < 3 {
				return cgroupLocation{}, errInvalidData
			}
			if version == "v2" && b[0] != "cgroup2" || version == "v1" && (b[0] != "cgroup" || !containsCSV(b[2], "memory")) {
				continue
			}
			root, err := cgroupMountPath(a[3])
			if err != nil {
				return cgroupLocation{}, err
			}
			mount, err := cgroupMountPath(a[4])
			if err != nil {
				return cgroupLocation{}, err
			}
			relative := ""
			if root == "/" {
				relative = member
			} else if member == root {
				relative = "/"
			} else if strings.HasPrefix(member, root+"/") {
				relative = strings.TrimPrefix(member, root)
			} else {
				continue
			}
			candidate := cgroupLocation{version: version, mount: mount, directory: path.Join(mount, relative), mountRoot: root}
			if found != nil {
				return cgroupLocation{}, errInvalidData
			}
			found = &candidate
		}
		if found != nil {
			return *found, nil
		}
	}
	return cgroupLocation{}, errCgroupNotFound
}
func containsCSV(value, want string) bool {
	for _, part := range strings.Split(value, ",") {
		if part == want {
			return true
		}
	}
	return false
}

// sampleCgroup only reads fixed kernel control files beneath a discovered mount.
func sampleCgroup(now time.Time, read func(string) ([]byte, error), identity func(string) (string, error)) ContainerMemoryMetric {
	result := ContainerMemoryMetric{Metadata: metadata(now, "cgroup", "runtime_cgroup"), LimitState: "unknown", Hierarchy: "visible"}
	fail := func(err error) ContainerMemoryMetric {
		failedRead(&result.Metadata, err)
		if errors.Is(err, errCgroupNotFound) {
			unavailable(&result.Metadata, "cgroup_not_found")
		}
		if errors.Is(err, errInvalidData) {
			unavailable(&result.Metadata, "invalid_data")
		}
		return result
	}
	bounded := func(file string) ([]byte, error) {
		b, e := read(file)
		if e == nil && (len(b) == 0 || len(b) > maxProcBytes) {
			e = errInvalidData
		}
		return b, e
	}
	membership, err := bounded("/proc/self/cgroup")
	if err != nil {
		return fail(err)
	}
	mounts, err := bounded("/proc/self/mountinfo")
	if err != nil {
		return fail(err)
	}
	location, err := resolveCgroup(membership, mounts)
	if err != nil {
		return fail(err)
	}
	result.Source = "cgroup_" + location.version
	sourceID, err := identity(location.directory)
	if err != nil {
		return fail(err)
	}
	if !validSourceID(sourceID) {
		return fail(errInvalidData)
	}
	usageFile, limitFile := "memory.current", "memory.max"
	if location.version == "v1" {
		usageFile, limitFile = "memory.usage_in_bytes", "memory.limit_in_bytes"
	}
	raw, err := bounded(path.Join(location.directory, usageFile))
	if err != nil {
		return fail(err)
	}
	usage, err := unsigned(strings.TrimSpace(string(raw)))
	if err != nil {
		return fail(err)
	}
	var limit uint64
	depth := 0
	for dir := location.directory; ; dir = path.Dir(dir) {
		depth++
		if depth > 128 {
			return fail(errInvalidData)
		}
		raw, err = bounded(path.Join(dir, limitFile))
		if errors.Is(err, fs.ErrNotExist) && location.version == "v2" && dir == location.mount && dir != location.directory && location.mountRoot == "/" {
			// The hierarchy root has no memory.current/max files. Verify both
			// absences plus an available memory controller; never ignore an
			// unreadable limit or assume a namespace-root leaf is unbounded.
			_, currentErr := bounded(path.Join(dir, "memory.current"))
			controllers, controllerErr := bounded(path.Join(dir, "cgroup.controllers"))
			if errors.Is(currentErr, fs.ErrNotExist) && controllerErr == nil && containsWord(string(controllers), "memory") {
				break
			}
		}
		if err != nil {
			return fail(err)
		}
		text := strings.TrimSpace(string(raw))
		unlimited := location.version == "v2" && text == "max"
		var value uint64
		if !unlimited {
			value, err = unsigned(text)
			if err != nil {
				return fail(errInvalidData)
			}
			if value == 0 {
				unavailable(&result.Metadata, "zero_limit")
				return result
			}
			if location.version == "v1" && value >= uint64(math.MaxInt64)-65535 {
				unlimited = true
			}
		}
		if !unlimited && (limit == 0 || value < limit) {
			limit = value
		}
		if location.version == "v1" {
			raw, err = bounded(path.Join(dir, "memory.use_hierarchy"))
			if err != nil {
				return fail(err)
			}
			if strings.TrimSpace(string(raw)) != "1" {
				unavailable(&result.Metadata, "hierarchy_disabled")
				return result
			}
		}
		if dir == location.mount {
			break
		}
		if !strings.HasPrefix(dir, location.mount+"/") {
			return fail(errInvalidData)
		}
	}
	// Migration while reading would combine different sources; discard it.
	end, err := bounded("/proc/self/cgroup")
	if err != nil {
		return fail(err)
	}
	if string(end) != string(membership) {
		unavailable(&result.Metadata, "cgroup_changed")
		return result
	}
	endID, err := identity(location.directory)
	if err != nil {
		return fail(err)
	}
	if endID != sourceID {
		unavailable(&result.Metadata, "cgroup_changed")
		return result
	}
	result.SourceID = sourceID
	result.UsageBytes = decimal(usage)
	if limit == 0 {
		result.LimitState = "unlimited"
		unavailable(&result.Metadata, "unlimited")
		return result
	}
	result.LimitState = "finite"
	result.LimitBytes = decimal(limit)
	percent := percentage(usage, limit)
	result.Percent = &percent
	return result
}

func validContainerMemory(m *ContainerMemoryMetric, at time.Time) bool {
	if m == nil {
		return true
	} // Observations written before this additive contract.
	if m.SourceID != "" && !validSourceID(m.SourceID) {
		return false
	}
	if !m.ObservedAt.Equal(at) || m.Scope != "runtime_cgroup" || m.Hierarchy != "visible" || (m.Source != "cgroup_v1" && m.Source != "cgroup_v2" && m.Source != "cgroup") {
		return false
	}
	if m.LimitState == "finite" {
		if m.Source == "cgroup" {
			return false
		}
		if m.Availability != Available || m.Reason != "" || m.UsageBytes == nil || m.LimitBytes == nil || m.Percent == nil {
			return false
		}
		usage, e1 := unsigned(*m.UsageBytes)
		limit, e2 := unsigned(*m.LimitBytes)
		return e1 == nil && e2 == nil && limit > 0 && !math.IsNaN(*m.Percent) && !math.IsInf(*m.Percent, 0) && *m.Percent == percentage(usage, limit)
	}
	if m.Percent != nil || m.LimitBytes != nil || (m.Availability != Unavailable && m.Availability != Unsupported) {
		return false
	}
	if m.LimitState == "unlimited" {
		if m.Source == "cgroup" {
			return false
		}
		if m.Reason != "unlimited" || m.UsageBytes == nil {
			return false
		}
		_, err := unsigned(*m.UsageBytes)
		return err == nil
	}
	return m.LimitState == "unknown" && m.UsageBytes == nil && m.Reason != ""
}

func containsWord(value, want string) bool {
	for _, word := range strings.Fields(value) {
		if word == want {
			return true
		}
	}
	return false
}

func validSourceID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
