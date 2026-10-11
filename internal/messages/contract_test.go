package messages

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
	"strings"
	"testing"
	"time"
)

func TestConstructorAndIdentityFailClosed(t *testing.T) {
	if _, err := New(nil, 17); err == nil {
		t.Fatal("nil database accepted")
	}
	if _, err := New(&pgx.ConnConfig{}, 0); err == nil {
		t.Fatal("zero schema accepted")
	}
	valid := Identity{TenantID: "synthetic", UserID: 1, SessionHash: strings.Repeat("a", 64), AuthorizationVersion: 1}
	if !identityOK(valid) {
		t.Fatal("valid identity rejected")
	}
	for _, change := range []func(*Identity){func(i *Identity) { i.TenantID = "" }, func(i *Identity) { i.UserID = 0 }, func(i *Identity) { i.SessionHash = "raw-cookie" }, func(i *Identity) { i.AuthorizationVersion = 0 }} {
		i := valid
		change(&i)
		if identityOK(i) {
			t.Fatalf("invalid identity accepted: %+v", i)
		}
	}
}
func TestFixedPageDTOAndPureOracle(t *testing.T) {
	at := time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)
	req := messagecore.Request{Scope: messagecore.Scope{TenantID: "synthetic", UserID: "1", IncludePlatform: true}, Catalog: catalog(), Limits: limits(), ConfirmedAt: at, Query: messagecore.Query{Sort: messagecore.SortDescending, ReadFilter: messagecore.ReadAll, Limit: 20}, ReadState: messagecore.ReadState{TenantID: "synthetic", UserID: "1", FirstReadAt: map[string]time.Time{"health-2": at.Add(-time.Minute)}}}
	for _, id := range []string{"health-2", "health-10"} {
		req.Messages = append(req.Messages, messagecore.Message{ID: id, TenantID: "synthetic", ScopeKind: messagecore.ScopePlatform, Type: "task_result", SubType: "succeeded", Title: "Infrastructure health task succeeded", OccurredAt: at.Add(-time.Hour), IngestedAt: at})
	}
	core, err := messagecore.NewResult(req)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loaded{id: "result", expires: at.Add(SnapshotTTL), core: core, data: snapshotData{TaskIDs: map[string]string{"health-2": "task-2", "health-10": "task-10"}}}
	page, err := snapshot.page(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Counts != (Counts{All: 2, Unread: 1, Read: 1}) || page.Items[0].ID != "health-10" || page.Items[0].FirstReadAt != nil || page.Items[1].FirstReadAt == nil {
		t.Fatalf("wrong fixed page: %+v", page)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"resultId"`, `"taskUUID"`, `"firstReadAt":null`, `"occurredAt"`, `"counts"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing DTO field %s: %s", key, raw)
		}
	}
	for _, key := range []string{"SessionHash", "AuthorizationVersion", "payload", "cursor"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("internal field leaked: %s", key)
		}
	}
	page.Items[0].Title = "changed"
	again, _ := snapshot.page(0, 20)
	if again.Items[0].Title == "changed" {
		t.Fatal("DTO mutation changed snapshot")
	}
}
func TestCoreErrorsAndProtectionLimits(t *testing.T) {
	for _, tc := range []struct {
		code   messagecore.Code
		status int
	}{{messagecore.InvalidFilter, 400}, {messagecore.BudgetExceeded, 422}} {
		err := coreError(&messagecore.Error{Code: tc.code, Field: "static"})
		var e *Error
		if !errors.As(err, &e) || e.Status != tc.status || e.Code != string(tc.code) {
			t.Fatalf("error mapping=%v", err)
		}
	}
	if MaxCandidates != 1000 || MaxInputBytes != 2*1024*1024 || MaxGlobalSnapshots != 1024 || ProjectionBatch != 200 || CleanupBatch != 200 || limits().MaxMarkTargets != 1 {
		t.Fatal("unreviewed guard drift")
	}
}

func TestSQLMicrosecondCeilingPreservesHalfOpenIntervals(t *testing.T) {
	base := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	for _, nanos := range []int{0, 1, 999, 1000, 1001, 1500, 999999999} {
		source := base.Add(time.Duration(nanos))
		got := ceilMicrosecond(source)
		if got.Before(source) || got.Sub(source) >= time.Microsecond || got.Nanosecond()%1000 != 0 {
			t.Fatalf("invalid ceil %s => %s", source, got)
		}
	}
	max := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	if got := ceilMicrosecond(max); got.Year() != 10000 || got.Nanosecond() != 0 {
		t.Fatalf("year carry=%s", got)
	}
	beforeEpoch := time.Date(1900, 1, 1, 0, 0, 0, 1500, time.UTC)
	if got := ceilMicrosecond(beforeEpoch); got.Nanosecond() != 2000 {
		t.Fatalf("pre-epoch rounding=%s", got)
	}
}
