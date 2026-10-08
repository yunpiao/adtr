package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/tasks"
)

func TestHistoryFilterContract(t *testing.T) {
	defaults, err := ParseHistoryFilter(url.Values{})
	if err != nil || defaults.PageIdx != 1 || defaults.PageSize != 20 || defaults.SortTm != -1 || defaults.Status == nil {
		t.Fatal(defaults, err)
	}
	accepted := []string{"pageSize=1", "pageSize=100", "pageSize=-1", "pageIdx=1000000&pageSize=50", "modelType=Audit", "status=queued&status=cancel_requested&status=cancelled", "startTm=2026-10-07T17%3A00%3A00%2B02%3A00&endTm=2026-10-07T15%3A00%3A00.000001Z", "startTm=0001-01-01T00%3A00%3A00Z", "endTm=9999-12-31T23%3A59%3A59.999999Z", "sortTm=1"}
	for _, raw := range accepted {
		q, e := url.ParseQuery(raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ParseHistoryFilter(q); e != nil {
			t.Errorf("valid %s: %v", raw, e)
		}
	}
	cases := []struct{ query, code string }{
		{"pageIdx=0", "invalid_input"}, {"pageIdx=01", "invalid_input"}, {"pageIdx=%2B1", "invalid_input"}, {"pageIdx=1000001", "invalid_input"}, {"pageSize=0", "invalid_input"}, {"pageSize=101", "invalid_input"}, {"pageSize=-2", "invalid_input"}, {"pageSize=1.0", "invalid_input"}, {"pageSize=-1&pageIdx=2", "invalid_input"}, {"pageIdx=1&pageIdx=1", "invalid_input"},
		{"sortTm=0", "invalid_input"}, {"sortTm=01", "invalid_input"}, {"status=padding", "invalid_input"}, {"status=SUCCESS", "invalid_input"}, {"status=queued&status=queued", "invalid_input"}, {"status=queued,running", "invalid_input"}, {"status=", "invalid_input"}, {"state=queued", "invalid_input"},
		{"modelType=Audit&modelType=Audit", "invalid_input"}, {"modelType=Audit&modelType=Alert", "unsupported_model_type"}, {"modelType=audit", "unsupported_model_type"}, {"modelType=Audit%0A", "invalid_input"}, {"modelType=%FF", "invalid_input"}, {"appType=1", "unsupported_app_type"}, {"file_name=anything", "invalid_input"}, {"visibility=all", "invalid_input"},
		{"startTm=2026-10-07+15%3A00%3A00", "invalid_input"}, {"startTm=2026-10-07T15%3A00%3A00", "invalid_input"}, {"startTm=2026-02-29T00%3A00%3A00Z", "invalid_input"}, {"startTm=2026-01-01T00%3A00%3A60Z", "invalid_input"}, {"startTm=2026-01-01T00%3A00%3A00.1234567Z", "invalid_input"}, {"startTm=2026-01-01T00%3A00%3A00%2B24%3A00", "invalid_input"}, {"startTm=2026-01-01T00%3A00%3A00%2B01%3A60", "invalid_input"}, {"startTm=0000-01-01T00%3A00%3A00Z", "invalid_input"}, {"startTm=0001-01-01T00%3A00%3A00%2B01%3A00", "invalid_input"}, {"endTm=9999-12-31T23%3A59%3A59-01%3A00", "invalid_input"},
		{"startTm=2026-10-07T15%3A00%3A00Z&endTm=2026-10-07T17%3A00%3A00%2B02%3A00", "invalid_input"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			q, e := url.ParseQuery(tc.query)
			if e != nil {
				t.Fatal(e)
			}
			_, e = ParseHistoryFilter(q)
			var failure *Error
			if !errors.As(e, &failure) || failure.Status != 400 || failure.Code != tc.code {
				t.Fatal("wrong rejection", e, tc.code)
			}
		})
	}
	q := url.Values{"status": {"running", "failed"}, "startTm": {"2026-10-07T17:00:00.123456+02:00"}}
	copy := q.Encode()
	f, e := ParseHistoryFilter(q)
	if e != nil || q.Encode() != copy || f.StartTm != "2026-10-07T15:00:00.123456Z" {
		t.Fatal("input mutation or wrong UTC normalization", e)
	}
}

func validHistoryRecord() historyRecord {
	at := time.Date(2026, 10, 7, 12, 0, 0, 123456000, time.UTC)
	result := exportResult{ArtifactToken: 7, RowCount: 0, SnapshotAt: at, SHA256: strings.Repeat("a", 64), ByteCount: 512}
	raw, _ := json.Marshal(result)
	return historyRecord{ExportHistoryRow: ExportHistoryRow{TaskUUID: "11111111-1111-4111-8111-111111111111", State: tasks.Succeeded, Progress: 100, CreatedAt: at, UpdatedAt: at, Attempt: 1, MaxAttempts: 3}, Result: raw, Epoch: "42", Snapshot: &artifactSnapshotMetadata{Matches: true, Epoch: "42", Rows: 0, CapturedAt: at, Visibility: 3}, Manifest: &artifactManifestMetadata{Token: 7, Bytes: 512, Chunks: 1, Rows: 0, SHA256: strings.Repeat("a", 64)}, Visibility: 3}
}
func TestHistoryProjectionPreservesStatesAndHidesPrivateMetadata(t *testing.T) {
	for _, state := range []tasks.State{tasks.Queued, tasks.Running, tasks.RetryWait, tasks.CancelRequested, tasks.Succeeded, tasks.Failed, tasks.PartialFailed, tasks.DeadLetter, tasks.Cancelled} {
		in := validHistoryRecord()
		in.State = state
		in.Progress = 73
		in.Error = "synthetic_failure"
		out := projectHistoryRecord(in)
		if out.State != state || out.Progress != 73 || out.Error != in.Error || out.Attempt != 1 || out.MaxAttempts != 3 {
			t.Fatal("fabricated persisted state", out)
		}
		if state == tasks.Succeeded {
			if !out.DownloadReady || out.DownloadStatus != "eligible" || out.RowCount == nil || *out.RowCount != 0 || out.SnapshotAt == nil {
				t.Fatal("zero-row workbook lost eligibility", out)
			}
		} else if out.DownloadReady || out.DownloadStatus != "not_ready" || out.RowCount != nil || out.SnapshotAt != nil || out.DownloadPath != "" {
			t.Fatal("staged artifact published before success", out)
		}
		raw, e := json.Marshal(out)
		if e != nil {
			t.Fatal(e)
		}
		for _, private := range []string{"private", "artifactToken", "sha256", "authorization", "tenantId", "actorId", "payload", "cursor", "byteCount", "chunk"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("private export metadata escaped", private)
			}
		}
	}
	in := validHistoryRecord()
	in.Error = "driver leaked a private path"
	out := projectHistoryRecord(in)
	if out.Error != "export_failed" {
		t.Fatal("unsafe error exposed")
	}
	if fmt.Sprint(in) != "[private export history metadata]" || strings.Contains(fmt.Sprintf("%#v", in), in.Epoch) {
		t.Fatal("private formatter exposed state")
	}
}
func TestHistoryProjectionInvalidArtifactsAndRevokedVisibility(t *testing.T) {
	cases := []struct {
		name, status string
		mutate       func(*historyRecord)
	}{
		{"no snapshot", "artifact_invalid", func(r *historyRecord) { r.Snapshot = nil }},
		{"wrong owner", "artifact_invalid", func(r *historyRecord) { r.Snapshot.Matches = false }},
		{"wrong epoch", "artifact_invalid", func(r *historyRecord) { r.Snapshot.Epoch = "43" }},
		{"hidden since snapshot", "snapshot_changed", func(r *historyRecord) { r.Visibility++ }},
		{"no manifest", "artifact_invalid", func(r *historyRecord) { r.Manifest = nil }},
		{"wrong token", "artifact_invalid", func(r *historyRecord) { r.Manifest.Token++ }},
		{"wrong bytes", "artifact_invalid", func(r *historyRecord) { r.Manifest.Bytes++ }},
		{"wrong manifest rows", "artifact_invalid", func(r *historyRecord) { r.Manifest.Rows++ }},
		{"wrong snapshot rows", "artifact_invalid", func(r *historyRecord) { r.Snapshot.Rows++ }},
		{"wrong time", "artifact_invalid", func(r *historyRecord) { r.Snapshot.CapturedAt = r.Snapshot.CapturedAt.Add(time.Microsecond) }},
		{"wrong digest", "artifact_invalid", func(r *historyRecord) { r.Manifest.SHA256 = strings.Repeat("b", 64) }},
		{"no chunks", "artifact_invalid", func(r *historyRecord) { r.Manifest.Chunks = 0 }},
		{"wrong JSON type", "artifact_invalid", func(r *historyRecord) {
			r.Result = []byte(strings.Replace(string(r.Result), `"artifactToken":7`, `"artifactToken":"7"`, 1))
		}},
		{"missing zero field", "artifact_invalid", func(r *historyRecord) { r.Result = []byte(strings.Replace(string(r.Result), `"rowCount":0,`, "", 1)) }},
		{"duplicate key", "artifact_invalid", func(r *historyRecord) {
			r.Result = []byte(strings.Replace(string(r.Result), `"rowCount":0,`, `"rowCount":0,"rowCount":0,`, 1))
		}},
		{"oversized result", "artifact_invalid", func(r *historyRecord) { r.Result = append([]byte(strings.Repeat(" ", 4096)), r.Result...) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validHistoryRecord()
			tc.mutate(&in)
			out := projectHistoryRecord(in)
			if out.DownloadStatus != tc.status || out.DownloadReady || out.RowCount != nil || out.SnapshotAt != nil || out.DownloadPath != "" {
				t.Fatal("invalid artifact disclosed readiness", out)
			}
		})
	}
}
func TestHistoryPrivateRecordDecodesDatabaseProjection(t *testing.T) {
	in := validHistoryRecord()
	raw := `{"taskUUID":"11111111-1111-4111-8111-111111111111","state":"succeeded","progress":100,"error":"","createdAt":"2026-10-07T12:00:00.123456Z","updatedAt":"2026-10-07T12:00:00.123456Z","attempt":1,"maxAttempts":3,"nextAttemptAt":null,"privateResult":` + string(in.Result) + `,"privateEpoch":"42","privateVisibility":3,"privateSnapshot":{"matches":true,"epoch":"42","rows":0,"capturedAt":"2026-10-07T12:00:00.123456Z","visibility":3},"privateManifest":{"token":7,"bytes":512,"chunks":1,"rows":0,"sha256":"` + strings.Repeat("a", 64) + `"}}`
	var decoded historyRecord
	if e := json.Unmarshal([]byte(raw), &decoded); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(projectHistoryRecord(in), projectHistoryRecord(decoded)) {
		t.Fatal("database metadata did not round-trip to public row")
	}
}
