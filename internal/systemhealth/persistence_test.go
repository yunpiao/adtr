package systemhealth

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

func unitStore(t *testing.T, opts ...StoreOptions) *Store {
	t.Helper()
	cfg, err := pgx.ParseConfig("postgres://synthetic:synthetic@localhost/synthetic?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(cfg, 7, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func requireCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != status || e.Code != code {
		t.Fatalf("error = %v, want %d %s", err, status, code)
	}
}

func TestStoreCopiesTrustedConfigAndAllowlist(t *testing.T) {
	cfg, _ := pgx.ParseConfig("postgres://synthetic@localhost/test?sslmode=disable")
	targets := []StorageTarget{{ID: "data", Path: "/data", Mount: "data volume"}}
	s, err := NewStore(cfg, 7, StoreOptions{OwnerTenantID: "tenant-owner", StorageTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Host = "untrusted.example"
	targets[0].Path = "/etc"
	if s.config.Host != "localhost" || s.targets[0].Path != "/data" || s.OwnerTenantID() != "tenant-owner" {
		t.Fatal("trusted config was not copied")
	}
	requireCode(t, s.scope("default", LocalInstanceID), 403, "forbidden")
	requireCode(t, s.scope("tenant-owner", "http://host/"), 404, "instance_not_found")
	if _, err = NewStore(nil, 7); err == nil {
		t.Fatal("nil config accepted")
	}
	if _, err = NewStore(cfg, 0); err == nil {
		t.Fatal("missing exact schema accepted")
	}
	if _, err = NewStore(cfg, 7, StoreOptions{OwnerTenantID: "../tenant"}); err == nil {
		t.Fatal("invalid owner accepted")
	}
	if _, err = NewStore(cfg, 7, StoreOptions{StorageTargets: []StorageTarget{{ID: RuntimeRootID, Path: "/etc", Mount: "/"}}}); err == nil {
		t.Fatal("reserved target override accepted")
	}
}

func TestHistoryInputBoundsAndAllowlists(t *testing.T) {
	s := unitStore(t)
	now := time.Unix(200000, 0)
	base := HistoryInput{Instance: LocalInstanceID, GraphType: "cpu_basic", StartTime: 190000, EndTime: 200000}
	if err := s.validateHistory(base, now); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*HistoryInput)
		code   string
	}{
		{"negative", func(in *HistoryInput) { in.StartTime = -1 }, "invalid_time_range"},
		{"empty", func(in *HistoryInput) { in.StartTime = in.EndTime }, "invalid_time_range"},
		{"future", func(in *HistoryInput) { in.EndTime++ }, "invalid_time_range"},
		{"window", func(in *HistoryInput) { in.StartTime = in.EndTime - 86401 }, "invalid_time_range"},
		{"graph", func(in *HistoryInput) { in.GraphType = "cpu_percent FROM secret" }, "invalid_graph_type"},
		{"cpu storage", func(in *HistoryInput) { in.StorageID = RuntimeRootID }, "invalid_storage_id"},
		{"disk missing", func(in *HistoryInput) { in.GraphType = "disk_usage" }, "storage_not_found"},
		{"disk path", func(in *HistoryInput) { in.GraphType = "disk_usage"; in.StorageID = "/etc" }, "storage_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := base
			test.change(&in)
			var e *Error
			if err := s.validateHistory(in, now); !errors.As(err, &e) || e.Code != test.code {
				t.Fatalf("wrong validation: %v", err)
			}
		})
	}
	base.StartTime = base.EndTime - 86400
	base.GraphType = "disk_usage"
	base.StorageID = RuntimeRootID
	if err := s.validateHistory(base, now); err != nil {
		t.Fatal("exact 24-hour range rejected", err)
	}
}

func TestHistoryPreservesActualSamplesAndNullEmptyStatistics(t *testing.T) {
	in := HistoryInput{StartTime: 100, EndTime: 200}
	data, gaps := historyData(in, []time.Time{time.Unix(115, 0), time.Unix(130, 0), time.Unix(160, 0)}, []float64{10.25, 30.75, 20.5})
	if len(data.Value) != 3 || data.Value[0] != "10.25" || data.Timestamp[2] != 160 {
		t.Fatalf("values resampled: %+v", data)
	}
	requirePercent(t, data.DataStatistics.Min, 10.25)
	requirePercent(t, data.DataStatistics.Max, 30.75)
	requirePercent(t, data.DataStatistics.Avg, 20.5)
	requirePercent(t, data.DataStatistics.Current, 20.5)
	if len(gaps) != 2 || gaps[0] != (HistoryGap{145, 160}) || gaps[1] != (HistoryGap{175, 200}) {
		t.Fatalf("missed sample gaps: %+v", gaps)
	}
	empty, gaps := historyData(in, nil, nil)
	encoded, err := json.Marshal(empty)
	if err != nil || !strings.Contains(string(encoded), `"timestamp":[]`) || !strings.Contains(string(encoded), `"value":[]`) || !strings.Contains(string(encoded), `"max":null`) {
		t.Fatalf("empty output: %s %v", encoded, err)
	}
	if len(gaps) != 1 || gaps[0] != (HistoryGap{100, 200}) {
		t.Fatal("empty interval must remain unobserved")
	}
}

func TestSnapshotPersistenceRejectsFabricatedValuesAndTargets(t *testing.T) {
	s := unitStore(t)
	_, sampler := newFixture(t)
	snapshot := sampler.Sample()
	if err := s.validateSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	nan := math.NaN()
	snapshot.Memory.Percent = &nan
	requireCode(t, s.validateSnapshot(snapshot), 400, "invalid_observation")
	snapshot = sampler.Sample()
	snapshot.Storage[0].ID = "/etc"
	requireCode(t, s.validateSnapshot(snapshot), 400, "invalid_observation")
	snapshot = sampler.Sample()
	snapshot.Storage[0].Mount = "/private/path"
	requireCode(t, s.validateSnapshot(snapshot), 400, "invalid_observation")
	snapshot = sampler.Sample()
	snapshot.CPU.Availability = Unavailable
	value := 1.0
	snapshot.CPU.Percent = &value
	requireCode(t, s.validateSnapshot(snapshot), 400, "invalid_observation")
}

func TestSafeErrorsAndMissingSchemaGate(t *testing.T) {
	requireCode(t, safeError(errors.New("postgres://secret:password@private.host/path")), 503, "health_storage_unavailable")
	requireCode(t, safeError(context.DeadlineExceeded), 503, "health_timeout")
	requireCode(t, safeError(tasks.ErrSchemaIncompatible), 503, "schema_incompatible")
	s := unitStore(t)
	_, err := s.CurrentTx(context.Background(), nil, OwnerTenantID, LocalInstanceID)
	requireCode(t, err, 503, "schema_gate_required")
	_, err = s.SetAlarmTx(context.Background(), nil, OwnerTenantID, 42, AlarmInput{Instance: LocalInstanceID, MountID: RuntimeRootID, Percent: 85, ExpectedRevision: 0})
	requireCode(t, err, 503, "schema_gate_required")
}

func TestWorkerHealthRequiresActualSameProcessCycles(t *testing.T) {
	now := time.Unix(1000, 0)
	fresh := now.Add(-5 * time.Second)
	stale := now.Add(-16 * time.Second)
	item := func(id string, cycle WorkerCycle, status WorkerCycleStatus, at time.Time) WorkerCycleActivity {
		return WorkerCycleActivity{WorkerID: id, Cycle: cycle, Status: status, LastActivityAt: &at}
	}
	out := WorkerActivity{CheckedAt: now, Cycles: []WorkerCycleActivity{item("one", WorkerQueue, WorkerCycleSuccess, fresh), item("two", WorkerRecovery, WorkerCycleSuccess, fresh)}}
	evaluateWorkerActivity(&out)
	if out.Availability == Available {
		t.Fatal("different incomplete processes combined as healthy")
	}
	out.Cycles[1].WorkerID = "one"
	evaluateWorkerActivity(&out)
	if out.Availability != Available {
		t.Fatal("fresh completed cycles not healthy")
	}
	out.Cycles[0].Status = WorkerCycleProgress
	evaluateWorkerActivity(&out)
	if out.Availability != Available || out.Cycles[0].Evidence != "executor_lease_or_checkpoint" {
		t.Fatal("control-loop activity basis missing")
	}
	out.Cycles[1].Status = WorkerCycleFailure
	out.Cycles[1].Reason = "cycle_failed"
	evaluateWorkerActivity(&out)
	if out.Availability == Available || out.Reason != "worker_cycle_failed" {
		t.Fatal("failed cycle retained healthy state")
	}
	out.Cycles[1].Status = WorkerCycleSuccess
	out.Cycles[1].LastActivityAt = &stale
	evaluateWorkerActivity(&out)
	if out.Availability == Available {
		t.Fatal("stale recovery retained healthy state")
	}
	if validWorkerCycle("one", WorkerRecovery, WorkerCycleProgress, "") || validWorkerCycle("one", WorkerQueue, WorkerCycleFailure, "database password leaked") {
		t.Fatal("invalid activity accepted")
	}
}

func TestCollectorRequiresMatchingTrustedTargetsAndHonorsCancellation(t *testing.T) {
	s := unitStore(t)
	_, sampler := newFixture(t)
	c, err := NewCollector(s, sampler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(c.Run(ctx), context.Canceled) {
		t.Fatal("cancelled collector did not stop")
	}
	requireCode(t, c.Collect(ctx), 503, "health_timeout")
	different, _ := NewSampler(Config{Storage: []StorageTarget{}})
	if _, err = NewCollector(s, different); err == nil {
		t.Fatal("collector accepted mismatched trusted registries")
	}
}
