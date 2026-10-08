package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestSystemPermissionPathsAndOwner(t *testing.T) {
	read := map[string]AccessAuth{"system": {Readable: true}}
	write := map[string]AccessAuth{"system": {Readable: true, Writeable: true}}
	for suffix, route := range systemRoutes {
		path := "/api/system" + suffix
		if got := systemPathAllowed(route.method, path, "default", read); got != !route.write {
			t.Fatal(path, got)
		}
		if !systemPathAllowed(route.method, path, "default", write) {
			t.Fatal(path)
		}
		for _, tenant := range []string{"", "other", "Default"} {
			if systemPathAllowed(route.method, path, tenant, write) {
				t.Fatal("cross-installation scope", tenant, path)
			}
		}
		if systemPathAllowed(route.method, path, "default", nil) || systemPathAllowed("DELETE", path, "default", write) {
			t.Fatal("default allow", path)
		}
	}
	for _, path := range []string{"/api/system", "/api/system/", "/api/systems/info", "/api/system/info/", "/api/system/storage/settings/"} {
		if systemPathAllowed("GET", path, "default", write) {
			t.Fatal("unknown route", path)
		}
	}
}

func TestSystemStrictQueryAndBoundaries(t *testing.T) {
	for _, tc := range []struct{ path, query string }{
		{"/info", ""}, {"/nodes", ""}, {"/health", ""}, {"/services", ""}, {"/services", "type=port"},
		{"/resources/current", "instance=local-api"}, {"/storage", "instance=local-api&page=100000&pageSize=50"},
		{"/resources/history", "instance=local-api&startTime=0&endTime=86400&graphType=cpu_basic"},
		{"/resources/history", "instance=local-api&startTime=10&endTime=20&graphType=disk_usage&storageId=runtime-root"},
	} {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = parseSystemQuery(tc.path, q); err != nil {
			t.Fatal(tc, err)
		}
	}
	for _, tc := range []struct{ path, query string }{
		{"/info", "tenant=default"}, {"/health", "type=all"}, {"/nodes", "host=localhost"},
		{"/services", "type="}, {"/services", "type=all&type=all"}, {"/services", "type=http://localhost"},
		{"/resources/current", ""}, {"/resources/current", "instance=local-api&instance=local-api"}, {"/resources/current", "instance=../../tmp"},
		{"/storage", "instance=local-api&page=0"}, {"/storage", "instance=local-api&page=01"}, {"/storage", "instance=local-api&page=100001"},
		{"/storage", "instance=local-api&pageSize=1"}, {"/storage", "instance=local-api&pageSize=60"},
		{"/storage", "instance=local-api&page%20pageSize=10"},
		{"/resources/history", "instance=local-api&startTime=0&endTime=86401&graphType=cpu_basic"},
		{"/resources/history", "instance=local-api&startTime=-1&endTime=20&graphType=cpu_basic"},
		{"/resources/history", "instance=local-api&startTime=20&endTime=20&graphType=cpu_basic"},
		{"/resources/history", "instance=local-api&startTime=01&endTime=20&graphType=cpu_basic"},
		{"/resources/history", "instance=local-api&startTime=0&endTime=20&graphType=cpu_basic&storageId=runtime-root"},
		{"/resources/history", "instance=local-api&startTime=0&endTime=20&graphType=disk_usage"},
		{"/resources/history", "instance=local-api&startTime=0&endTime=20&graphType=disk_usage&storageId=/"},
		{"/resources/history", "instance=local-api&startTime=0&endTime=20&graphType=invalid"},
	} {
		q, _ := url.ParseQuery(tc.query)
		if _, err := parseSystemQuery(tc.path, q); err == nil {
			t.Fatal("accepted", tc)
		}
	}
}

const systemGoodBody = `{"instance":"local-api","storageId":"runtime-root","setType":"alarm","percent":85,"expectedRevision":0,"actorPassword":"synthetic-password","totpCode":"123456"}`

func TestSystemStrictSettingsBody(t *testing.T) {
	good := func(raw string) error {
		r := httptest.NewRequest("POST", "/api/system/storage/settings", strings.NewReader(raw))
		_, err := decodeSystemRequest(httptest.NewRecorder(), r)
		return err
	}
	for _, raw := range []string{systemGoodBody, strings.Replace(systemGoodBody, `"percent":85`, `"percent":90`, 1)} {
		if err := good(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		strings.Replace(systemGoodBody, `"percent":85`, `"percent":84`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"percent":91`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"percent":85.0`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"percent":"85"`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"percent":null`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"percent":85,"percent":85`, 1),
		strings.Replace(systemGoodBody, `"percent":85,`, ``, 1),
		strings.Replace(systemGoodBody, `"expectedRevision":0`, `"expectedRevision":-1`, 1),
		strings.Replace(systemGoodBody, `"expectedRevision":0`, `"expectedRevision":9223372036854775807`, 1),
		strings.Replace(systemGoodBody, `"123456"`, `"12x456"`, 1),
		strings.Replace(systemGoodBody, `"runtime-root"`, `"/etc/passwd"`, 1),
		strings.Replace(systemGoodBody, `"setType":"alarm"`, `"setType":"Alarm"`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"tenant":"default","percent":85`, 1),
		systemGoodBody + `{}`, `[]`, `null`, strings.Repeat(" ", 4097),
	} {
		if err := good(raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, raw := range []string{
		strings.Replace(systemGoodBody, `"alarm"`, `"log"`, 1),
		strings.Replace(systemGoodBody, `"alarm"`, `"autoClear"`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"storageDataAlarmValue":85,"percent":85`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"storageLogValue":30,"percent":85`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"storageAutoClearValue":90,"percent":85`, 1),
		strings.Replace(systemGoodBody, `"percent":85`, `"mount":"/","percent":85`, 1),
	} {
		status, code := systemHTTPError(good(raw))
		if status != 422 || code != "unsupported" {
			t.Fatal("deprecated field silently accepted", status, code)
		}
	}
}

func TestSystemHTTPRejectsBeforeDatabaseAndSanitizesErrors(t *testing.T) {
	s := &Service{origin: "http://localhost:8080"}
	for _, tc := range []struct {
		method, path, body, origin, content string
		status                              int
	}{
		{"GET", "/api/system/info/", "", "", "", 404}, {"POST", "/api/system/info", "", "", "", 405},
		{"GET", "/api/system/info?bad=1", "", "", "", 400}, {"GET", "/api/system/info?bad=%zz", "", "", "", 400},
		{"GET", "/api/system/info", "{}", "", "", 400}, {"GET", "/api/system/info", "", "", "", 503},
		{"POST", "/api/system/storage/settings", systemGoodBody, "https://evil.test", "application/json", 403},
		{"POST", "/api/system/storage/settings", systemGoodBody, s.origin, "text/plain", 400},
		{"POST", "/api/system/storage/settings?instance=local-api", systemGoodBody, s.origin, "application/json", 400},
		{"POST", "/api/system/storage/settings", systemGoodBody, s.origin, "application/json", 503},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		s.SystemHandler(nil).ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{errors.New("password=secret"), 500, "internal"}, {context.DeadlineExceeded, 503, "database_unavailable"},
		{tasks.ErrSchemaIncompatible, 503, "schema_incompatible"}, {&systemhealth.Error{Status: 409, Code: "revision_conflict"}, 409, "revision_conflict"},
	} {
		if status, code := systemHTTPError(tc.err); status != tc.status || code != tc.code {
			t.Fatal(status, code)
		}
	}
}

func TestSystemUnknownAndStaleDoNotBecomeHealthyOrZero(t *testing.T) {
	now := time.Now().UTC()
	current := systemhealth.Current{Availability: systemhealth.Unavailable, Reason: "no_samples", CheckedAt: now}
	worker := systemhealth.WorkerActivity{Availability: systemhealth.Unavailable, Reason: "no_worker_activity", Cycles: []systemhealth.WorkerCycleActivity{}}
	info := systemInfoValue(current, now)
	if info.Status.CPUUsagePercent != nil || info.Status.DiskUsagePercent != nil || info.Version.EngineVersion != nil || info.Basic.IP != nil || info.Upgrade.EngineVersion != nil {
		t.Fatal("fabricated information", info)
	}
	value := 42.0
	current.Snapshot = &systemhealth.Snapshot{CPU: systemhealth.PercentMetric{Percent: &value}, ObservedAt: now.Add(-time.Hour)}
	current.Stale = true
	if systemInfoValue(current, now).Status.CPUUsagePercent != nil {
		t.Fatal("stale legacy convenience value")
	}
	current.Stale = false
	current.Reason = "clock_skew"
	current.Snapshot.ObservedAt = now.Add(time.Hour)
	if systemInfoValue(current, now).Status.CPUUsagePercent != nil {
		t.Fatal("future observation exposed as current convenience value")
	}
	health := systemHealthValue(current, worker, "all", now)
	if health.Result != "degraded" {
		t.Fatal(health)
	}
	for _, dep := range health.Dependencies {
		if dep.ID == "worker" && dep.Status != "unknown" || dep.ID == "sampler" && dep.Status != "unhealthy" || (dep.ID == "cache" || dep.ID == "engine") && dep.Status != "not_configured" {
			t.Fatal(dep)
		}
	}
	port := systemHealthValue(current, worker, "port", now)
	if len(port.Dependencies) != 1 || port.Dependencies[0].ID != "postgresql" || port.Dependencies[0].Source != "postgresql_protocol" {
		t.Fatal(port)
	}
	raw, err := json.Marshal(info)
	if err != nil || !strings.Contains(string(raw), `"cpuUsagePercent":null`) {
		t.Fatal(string(raw), err)
	}
}

func TestSystemFreshSampleHealthRequiresActualMeasurements(t *testing.T) {
	percent := 42.0
	for _, tc := range []struct {
		name, wantReason  string
		cpu, memory, disk systemhealth.Availability
		wantStatus        string
	}{
		{"healthy", "fresh_resource_observation", systemhealth.Available, systemhealth.Available, systemhealth.Available, "healthy"},
		{"warmup", "partial_resource_observation", systemhealth.WarmingUp, systemhealth.Available, systemhealth.Available, "unhealthy"},
		{"failed", "unavailable_resource_observation", systemhealth.Unavailable, systemhealth.Unavailable, systemhealth.Unavailable, "unhealthy"},
		{"unsupported", "unsupported_platform", systemhealth.Unsupported, systemhealth.Unsupported, systemhealth.Unsupported, "unhealthy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := func(a systemhealth.Availability) *float64 {
				if a == systemhealth.Available {
					return &percent
				}
				return nil
			}
			snapshot := systemhealth.Snapshot{
				CPU:     systemhealth.PercentMetric{Metadata: systemhealth.Metadata{Availability: tc.cpu}, Percent: value(tc.cpu)},
				Memory:  systemhealth.MemoryMetric{Metadata: systemhealth.Metadata{Availability: tc.memory}, Percent: value(tc.memory)},
				Storage: []systemhealth.StorageMetric{{Metadata: systemhealth.Metadata{Availability: tc.disk}, Percent: value(tc.disk)}},
			}
			status, reason := systemSamplerStatus(snapshot)
			if status != tc.wantStatus || reason != tc.wantReason {
				t.Fatal(status, reason)
			}
		})
	}
}
