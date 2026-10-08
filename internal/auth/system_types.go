package auth

import (
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/yunpiao/adtr/internal/systemhealth"
)

type systemStatus struct {
	MemoryUsagePercent *float64                   `json:"memoryUsagePercent"`
	CPUUsagePercent    *float64                   `json:"cpuUsagePercent"`
	DiskUsagePercent   *float64                   `json:"diskUsagePercent"`
	BootTime           *uint64                    `json:"bootTime"`
	LoadAverage        *systemhealth.LoadAverages `json:"LoadAverage"`
}
type systemVersion struct {
	EngineVersion          *string `json:"engineVersion"`
	MajorVersion           *string `json:"majorVersion"`
	EngineVersionTimestamp *int64  `json:"engineVersionTimestamp"`
	MajorVersionTimestamp  *int64  `json:"majorVersionTimestamp"`
	OSPlatform             string  `json:"OSPlatform"`
	OSVersion              *string `json:"OSVersion"`
	GoVersion              string  `json:"goVersion"`
	Revision               *string `json:"revision"`
	Modified               *bool   `json:"modified"`
}
type systemBasic struct {
	IP              *string `json:"ip"`
	SystemName      string  `json:"systemName"`
	CompanyName     *string `json:"companyName"`
	OfficialWebsite *string `json:"officialWebsite"`
}
type systemUpgrade struct {
	EngineVersion *string `json:"upgradeEngineVersion"`
	MajorVersion  *string `json:"upgradeMajorVersion"`
}
type systemInfo struct {
	Status            systemStatus         `json:"status"`
	Version           systemVersion        `json:"version"`
	Basic             systemBasic          `json:"basic"`
	Upgrade           systemUpgrade        `json:"upgrade"`
	SystemCurrentTime time.Time            `json:"systemCurrentTime"`
	Current           systemhealth.Current `json:"current"`
	Capabilities      map[string]string    `json:"capabilities"`
}
type systemNode struct {
	Instance     string                    `json:"instance"`
	HostName     *string                   `json:"hostName"`
	IP           *string                   `json:"ip"`
	Scope        string                    `json:"scope"`
	Availability systemhealth.Availability `json:"availability"`
}
type systemDependency struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason"`
	CheckedAt  time.Time  `json:"checkedAt"`
	ObservedAt *time.Time `json:"observedAt"`
	Source     string     `json:"source"`
	Scope      string     `json:"scope"`
}
type systemHealth struct {
	Result       string                      `json:"result"`
	CheckedAt    time.Time                   `json:"checkedAt"`
	Dependencies []systemDependency          `json:"dependencies"`
	Worker       systemhealth.WorkerActivity `json:"worker"`
}

func systemInfoValue(current systemhealth.Current, now time.Time) systemInfo {
	v := systemInfo{
		Version: systemVersion{OSPlatform: runtime.GOOS, GoVersion: runtime.Version()},
		Basic:   systemBasic{SystemName: "ADTR"}, SystemCurrentTime: now,
		Current:      current,
		Capabilities: map[string]string{"engine": "not_configured", "license": "unsupported", "upgrade": "not_configured", "companyMetadata": "not_configured", "osVersion": "unavailable"},
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					revision := setting.Value
					v.Version.Revision = &revision
				}
			case "vcs.modified":
				if setting.Value == "true" || setting.Value == "false" {
					modified := setting.Value == "true"
					v.Version.Modified = &modified
				}
			}
		}
	}
	// Legacy convenience fields only carry fresh measurements. The complete
	// observation, including source and stale/missing reasons, stays in current.
	if snap := current.Snapshot; snap != nil && !current.Stale && current.Availability == systemhealth.Available {
		v.Status.MemoryUsagePercent = snap.Memory.Percent
		v.Status.CPUUsagePercent = snap.CPU.Percent
		v.Status.BootTime = snap.Uptime.Seconds
		v.Status.LoadAverage = snap.Load.Values
		for _, disk := range snap.Storage {
			if disk.ID == systemhealth.RuntimeRootID {
				v.Status.DiskUsagePercent = disk.Percent
			}
		}
	}
	return v
}

func systemNodesValue(current systemhealth.Current) any {
	var hostname *string
	if value, err := os.Hostname(); err == nil && value != "" && validAccessText(value, 255) {
		hostname = &value
	}
	return struct {
		Nodes []systemNode `json:"nodeList"`
	}{[]systemNode{{Instance: systemhealth.LocalInstanceID, HostName: hostname, Scope: systemhealth.KernelScope, Availability: current.Availability}}}
}

func systemHealthValue(current systemhealth.Current, worker systemhealth.WorkerActivity, filter string, now time.Time) systemHealth {
	deps := []systemDependency{
		{ID: "api", Name: "ADTR API", Type: "service", Status: "healthy", Reason: "serving_request", CheckedAt: now, ObservedAt: &now, Source: "authenticated_request", Scope: "process"},
		{ID: "postgresql", Name: "PostgreSQL", Type: "port", Status: "healthy", Reason: "protocol_and_schema_verified", CheckedAt: now, ObservedAt: &now, Source: "postgresql_protocol", Scope: "configured_dependency"},
		{ID: "sampler", Name: "Runtime resource sampler", Type: "service", Status: "unknown", Reason: current.Reason, CheckedAt: now, Source: "persisted_observation", Scope: systemhealth.KernelScope},
		{ID: "worker", Name: "ADTR worker", Type: "service", Status: "unknown", Reason: worker.Reason, CheckedAt: now, Source: "worker_cycle_activity", Scope: "configured_dependency"},
		{ID: "cache", Name: "Cache", Type: "service", Status: "not_configured", Reason: "not_configured", CheckedAt: now, Source: "server_configuration", Scope: "configured_dependency"},
		{ID: "engine", Name: "Analysis engine", Type: "engine", Status: "not_configured", Reason: "not_configured", CheckedAt: now, Source: "server_configuration", Scope: "configured_dependency"},
	}
	if current.Snapshot != nil {
		deps[2].ObservedAt = &current.Snapshot.ObservedAt
		deps[2].Status = "unhealthy"
		if !current.Stale && current.Availability == systemhealth.Available {
			deps[2].Status, deps[2].Reason = systemSamplerStatus(*current.Snapshot)
		}
	}
	if worker.Availability == systemhealth.Available {
		deps[3].Status, deps[3].Reason = "healthy", "fresh_worker_cycles"
	} else if len(worker.Cycles) > 0 {
		deps[3].Status = "unhealthy"
	}
	for _, cycle := range worker.Cycles {
		if cycle.LastActivityAt != nil && (deps[3].ObservedAt == nil || cycle.LastActivityAt.After(*deps[3].ObservedAt)) {
			deps[3].ObservedAt = cycle.LastActivityAt
		}
	}
	result := systemHealth{Result: "healthy", CheckedAt: now, Dependencies: []systemDependency{}, Worker: worker}
	for _, dep := range deps {
		if filter != "all" && dep.Type != filter {
			continue
		}
		result.Dependencies = append(result.Dependencies, dep)
		if dep.Status == "unknown" || dep.Status == "unhealthy" {
			result.Result = "degraded"
		}
	}
	return result
}

func systemSamplerStatus(snapshot systemhealth.Snapshot) (string, string) {
	available, unsupported, count := 0, 0, 2+len(snapshot.Storage)
	check := func(availability systemhealth.Availability, percent *float64) {
		if availability == systemhealth.Available && percent != nil {
			available++
		}
		if availability == systemhealth.Unsupported {
			unsupported++
		}
	}
	check(snapshot.CPU.Availability, snapshot.CPU.Percent)
	check(snapshot.Memory.Availability, snapshot.Memory.Percent)
	for _, metric := range snapshot.Storage {
		check(metric.Availability, metric.Percent)
	}
	if available == count {
		return "healthy", "fresh_resource_observation"
	}
	if unsupported == count {
		return "unhealthy", "unsupported_platform"
	}
	if available > 0 {
		return "unhealthy", "partial_resource_observation"
	}
	return "unhealthy", "unavailable_resource_observation"
}
