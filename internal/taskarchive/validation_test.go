package taskarchive

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/tasks"
)

func TestArchiveCutoffIsExactUTC(t *testing.T) {
	for _, raw := range []string{"2026-01-02T03:04:05Z", "2026-01-02T03:04:05.123456Z"} {
		if _, err := ParseBefore(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"", "2026-01-02", "2026-01-02T03:04:05", "2026-01-02T03:04:05+00:00", "2026-01-02T03:04:05.1234567Z", "2026-02-30T00:00:00Z", "0000-01-01T00:00:00Z", "2026-01-02T03:04:60Z", " 2026-01-02T03:04:05Z"} {
		if _, err := ParseBefore(raw); err == nil {
			t.Fatal("accepted invalid cutoff", raw)
		}
	}
}
func TestTargetValidationAndStableOrder(t *testing.T) {
	in := []Target{{"b", 2}, {"a", 0}}
	out, err := validateTargets(in, "操作原因", "key-1")
	if err != nil || out[0].TaskID != "a" || in[0].TaskID != "b" {
		t.Fatal(out, in, err)
	}
	for _, bad := range [][]Target{nil, {{"a", 0}, {"a", 1}}, {{"a", -1}}, {{"bad/id", 0}}, make([]Target, 101)} {
		if _, err = validateTargets(bad, "reason", "key"); err == nil {
			t.Fatal("accepted targets", bad)
		}
	}
	for _, reason := range []string{"", " ", " leading", "trailing ", "new\nline", strings.Repeat("界", 501), string([]byte{0xff})} {
		if _, err = validateTargets(in, reason, "key"); err == nil {
			t.Fatal("accepted reason", reason)
		}
	}
	if _, err = validateTargets(in, strings.Repeat("界", 500), "key"); err != nil {
		t.Fatal(err)
	}
	if _, err = validateTargets(in, "reason", "bad/key"); err == nil {
		t.Fatal("accepted malformed key")
	}
}
func TestEligibilityRequiresTerminalEvidenceAndStrictCutoff(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	before := now.Add(-time.Hour)
	terminal := before.Add(-time.Microsecond)
	base := lockedTarget{kind: HealthKind, domain: "platform", state: tasks.Succeeded, terminalAt: &terminal}
	if !eligible(base, &before, now, true) {
		t.Fatal("eligible health task rejected")
	}
	for _, state := range []tasks.State{tasks.Queued, tasks.Running, tasks.RetryWait, tasks.CancelRequested} {
		item := base
		item.state = state
		if eligible(item, &before, now, true) {
			t.Fatal("active state", state)
		}
	}
	for _, mutate := range []func(*lockedTarget){
		func(t *lockedTarget) { t.terminalAt = nil },
		func(t *lockedTarget) { t.terminalAt = &before },
		func(t *lockedTarget) { t.kind = "audit.export" },
		func(t *lockedTarget) { t.domain = "domain-a" },
		func(t *lockedTarget) { t.archived = true },
		func(t *lockedTarget) { future := now.Add(time.Second); t.leaseUntil = &future },
	} {
		item := base
		mutate(&item)
		if eligible(item, &before, now, true) {
			t.Fatal("ineligible item accepted", item)
		}
	}
	base.archived = true
	if !eligible(base, nil, now, false) {
		t.Fatal("restore rejected")
	}
	base.archived = false
	if eligible(base, nil, now, false) {
		t.Fatal("active record restored")
	}
}
func TestRequestHashBindsOperationCutoffReasonAndVersion(t *testing.T) {
	before, _ := ParseBefore("2026-01-02T00:00:00Z")
	targets := []Target{{"a", 0}, {"b", 2}}
	original := requestHash("archive", targets, &before, "reason")
	for _, hash := range []string{
		requestHash("restore", targets, nil, "reason"),
		requestHash("archive", targets, &before, "changed"),
		requestHash("archive", []Target{{"a", 1}, {"b", 2}}, &before, "reason"),
		requestHash("archive", targets, nil, "reason"),
	} {
		if original == hash {
			t.Fatal("request identity collision")
		}
	}
}
func TestCandidatePaginationRejectsAmbiguousQueries(t *testing.T) {
	f, err := ParseFilter(url.Values{"before": {"2026-01-02T00:00:00Z"}})
	if err != nil || f.PageIdx != 1 || f.PageSize != 20 {
		t.Fatal(f, err)
	}
	for _, raw := range []string{"before=2026-01-02T00:00:00Z&before=2026-01-02T00:00:00Z", "before=2026-01-02T00:00:00Z&pageIdx=01", "before=2026-01-02T00:00:00Z&pageIdx=1000001", "before=2026-01-02T00:00:00Z&pageSize=101", "before=2026-01-02T00:00:00Z&state=failed", "pageIdx=1"} {
		q, _ := url.ParseQuery(raw)
		if _, err := ParseFilter(q); err == nil {
			t.Fatal("accepted query", raw)
		}
	}
}
