//go:build !linux

package systemhealth

import "time"

func readContainerMemory(now time.Time) ContainerMemoryMetric {
	return ContainerMemoryMetric{Metadata: Metadata{ObservedAt: now, Source: "cgroup", Scope: "runtime_cgroup", Availability: Unsupported, Reason: "unsupported_platform"}, LimitState: "unknown", Hierarchy: "visible"}
}
