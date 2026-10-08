package operationallogs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/tasks"
)

const bundleTestID = "00000000-0000-4000-8000-000000000001"

func bundleFixture() (bundleSnapshot, []Event) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 123456000, time.UTC)
	s := bundleSnapshot{Rows: 2, CapturedAt: at, Selection: Selection{"2026-10-07T11:00:00Z", "2026-10-07T13:00:00Z", []string{"api", "worker"}}, Coverage: Coverage{Mode: "best_effort", GapsPossible: true, JournalCreatedAt: at.Add(-time.Hour)}}
	events := []Event{{1, "00000000-0000-4000-8000-000000000003", "10000000-0000-4000-8000-000000000001", API, ServiceStartRequested, Attempted, Info, NoReason, at.Add(-time.Second), at}, {1, "00000000-0000-4000-8000-000000000002", "20000000-0000-4000-8000-000000000001", Worker, QueueCycle, Completed, Info, NoReason, at.Add(-2 * time.Second), at.Add(-time.Second)}}
	first := events[1].RecordedAt
	s.Coverage.FirstRecordedAt = &first
	return s, events
}

func TestBundlePayloadExactCanonicalContract(t *testing.T) {
	valid := `{"startTm":"2026-10-07T12:00:00+01:00","endTm":"2026-10-07T13:00:00+01:00","systemType":["worker","api"]}`
	got, err := ValidateBundlePayload([]byte(valid))
	if err != nil || string(got) != `{"startTm":"2026-10-07T11:00:00Z","endTm":"2026-10-07T12:00:00Z","systemType":["api","worker"]}` {
		t.Fatalf("canonical payload: %s %v", got, err)
	}
	for _, raw := range []string{`null`, `[]`, `{}`, strings.Replace(valid, `"systemType":["worker","api"]`, `"systemType":null`, 1), strings.Replace(valid, `"systemType":["worker","api"]`, `"systemType":[]`, 1), strings.Replace(valid, `"worker","api"`, `"api","api"`, 1), strings.Replace(valid, `"worker"`, `"collector"`, 1), strings.TrimSuffix(valid, "}") + `,"fileName":"/etc/passwd"}`, strings.TrimSuffix(valid, "}") + `,"actorPassword":"secret"}`, strings.TrimSuffix(valid, "}") + `,"startTm":"2026-10-07T11:00:00Z"}`, valid + ` {}`, strings.Replace(valid, "13:00:00+01:00", "13:00:00", 1), strings.Replace(valid, "2026-10-07T13", "2026-10-09T13", 1), strings.Replace(valid, "12:00:00+01:00", "12:00:00.1234567+01:00", 1)} {
		if _, err := ValidateBundlePayload([]byte(raw)); err == nil {
			t.Errorf("accepted invalid payload %s", raw)
		}
	}
}

func TestBundleZIPContainsExactJournalEventsAndManifest(t *testing.T) {
	s, events := bundleFixture()
	data, m, err := buildBundle(context.Background(), bundleTestID, s, events)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := buildBundle(context.Background(), bundleTestID, s, events)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatal("same snapshot produced different ZIP bytes", err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) != 2 {
		t.Fatal("not a two-entry ZIP", err)
	}
	var lines []byte
	for i, want := range []string{"manifest.json", "events.jsonl"} {
		f := z.File[i]
		if f.Name != want || f.Method != zip.Store {
			t.Fatalf("unexpected archive member %+v", f.FileHeader)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			var manifest bundleManifest
			if json.Unmarshal(body, &manifest) != nil || manifest.FormatVersion != 1 || manifest.TaskUUID != bundleTestID || manifest.RowCount != 2 || !manifest.Coverage.GapsPossible || manifest.Coverage.Mode != "best_effort" {
				t.Fatal("manifest missing provenance")
			}
		} else {
			lines = body
		}
	}
	if bundleDigest(lines) != m.JSONLSHA256 {
		t.Fatal("JSONL digest mismatch")
	}
	parts := bytes.Split(bytes.TrimSuffix(lines, []byte{'\n'}), []byte{'\n'})
	if len(parts) != 2 {
		t.Fatal("wrong row count")
	}
	for i, line := range parts {
		want, _ := json.Marshal(events[i])
		if !bytes.Equal(line, want) {
			t.Fatalf("event DTO drift: %s", line)
		}
	}
	if err = verifyBundleBytes(context.Background(), data, m); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"actorPassword", "password_hash", "tenant_id", "lease_owner", "audit_username", "fileName", "rawError"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("private field %s in archive", forbidden)
		}
	}
}

func TestBundleEmptyWindowAndRejectInvalidSnapshot(t *testing.T) {
	s, events := bundleFixture()
	s.Rows = 0
	data, m, err := buildBundle(context.Background(), bundleTestID, s, nil)
	if err != nil || verifyBundleBytes(context.Background(), data, m) != nil || m.JSONLSHA256 != bundleDigest(nil) {
		t.Fatal("empty genuine window failed", err)
	}
	s.Rows = 2
	for _, mutate := range []func(*bundleSnapshot, []Event){func(s *bundleSnapshot, _ []Event) { s.Rows = 1 }, func(s *bundleSnapshot, _ []Event) { s.Rows = MaxBundleRows + 1 }, func(s *bundleSnapshot, _ []Event) { s.Coverage.GapsPossible = false }, func(s *bundleSnapshot, _ []Event) { s.Selection.SystemType = []string{"api"} }, func(_ *bundleSnapshot, e []Event) { e[0].Reason = "raw credentials" }, func(_ *bundleSnapshot, e []Event) { e[0], e[1] = e[1], e[0] }, func(_ *bundleSnapshot, e []Event) { e[1] = e[0] }} {
		copySnapshot := s
		copyEvents := append([]Event(nil), events...)
		mutate(&copySnapshot, copyEvents)
		if _, _, err = buildBundle(context.Background(), bundleTestID, copySnapshot, copyEvents); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = buildBundle(cancelled, bundleTestID, s, events); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	var b bundleLimitBuffer
	if _, err = b.Write(make([]byte, MaxBundleBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Write([]byte{1}); err == nil {
		t.Fatal("16 MiB cap ignored")
	}
}

func TestBundleMaximumRowsRemainRealBoundedJSONL(t *testing.T) {
	s, example := bundleFixture()
	s.Rows = MaxBundleRows
	events := make([]Event, MaxBundleRows)
	for i := range events {
		events[i] = example[0]
		events[i].EventID = fmt.Sprintf("00000000-0000-4000-8000-%012d", MaxBundleRows-i)
	}
	data, m, err := buildBundle(context.Background(), bundleTestID, s, events)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > MaxBundleBytes || m.RowCount != MaxBundleRows || len(data) <= bundleChunkBytes {
		t.Fatal("maximum-row bundle is not properly bounded")
	}
	if err = verifyBundleBytes(context.Background(), data, m); err != nil {
		t.Fatal(err)
	}
	s.Rows++
	events = append(events, example[0])
	if _, _, err = buildBundle(context.Background(), bundleTestID, s, events); err == nil {
		t.Fatal("10001 rows accepted")
	}
}

func bundleRecordFixture(t *testing.T) (bundleRecord, []byte, bundleManifest) {
	t.Helper()
	s, events := bundleFixture()
	data, m, err := buildBundle(context.Background(), bundleTestID, s, events)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(bundleResult{3, s.Rows, s.CapturedAt, bundleDigest(data), int64(len(data))})
	payload, _ := json.Marshal(s.Selection)
	manifest, _ := json.Marshal(m)
	r := bundleRecord{Task: tasks.Task{ID: bundleTestID, Kind: BundleKindName, State: tasks.Succeeded, Result: []byte(`{"secret":"never"}`), Cursor: []byte(`{"secret":"never"}`)}, Epoch: "42", Fence: 3, Result: result, Payload: payload, Snapshot: &bundleSnapshotMetadata{true, "42", s.Rows, s.CapturedAt, s.Selection, s.Coverage}, Manifest: &bundleArtifactMetadata{3, int64(len(data)), 1, s.Rows, bundleDigest(data), m.JSONLSHA256, manifest}}
	return r, data, m
}

func TestBundleMetadataEligibilityDoesNotClaimVerifiedBytes(t *testing.T) {
	r, data, m := bundleRecordFixture(t)
	detail := projectBundleRecord(r)
	if !detail.DownloadReady || detail.ArtifactStatus != "eligible" || string(detail.Task.Result) != "{}" || string(detail.Task.Cursor) != "{}" {
		t.Fatal("eligible redacted detail incorrect")
	}
	data[len(data)/2] ^= 1
	if verifyBundleBytes(context.Background(), data, m) == nil {
		t.Fatal("corrupt bytes accepted")
	}
	if !projectBundleRecord(r).DownloadReady {
		t.Fatal("metadata availability was confused with byte verification")
	}
	for _, mutate := range []func(*bundleRecord){func(r *bundleRecord) { r.Fence++ }, func(r *bundleRecord) { r.Epoch = "43" }, func(r *bundleRecord) { r.Snapshot = nil }, func(r *bundleRecord) { r.Manifest.Chunks++ }, func(r *bundleRecord) { r.Manifest.JSONLSHA256 = strings.Repeat("0", 64) }, func(r *bundleRecord) { r.Snapshot.Rows++ }, func(r *bundleRecord) { r.Result = []byte(`{"artifactToken":3,"artifactToken":3}`) }, func(r *bundleRecord) { r.Payload = []byte(`{"private":"hidden"}`) }, func(r *bundleRecord) { r.Task.ID = "00000000-0000-4000-8000-000000000009" }} {
		copyRecord, _, _ := bundleRecordFixture(t)
		mutate(&copyRecord)
		d := projectBundleRecord(copyRecord)
		if d.DownloadReady || d.ArtifactStatus != "artifact_invalid" || d.RowCount != nil || d.Coverage != nil || d.SnapshotAt != nil || d.DownloadPath != "" || string(d.Task.Result) != "{}" || string(d.Task.Cursor) != "{}" {
			t.Fatal("invalid metadata leaked or enabled download")
		}
	}
	r.Task.State = tasks.Cancelled
	d := projectBundleRecord(r)
	if d.DownloadReady || d.ArtifactStatus != "not_ready" || d.RowCount != nil {
		t.Fatal("cancelled staging published")
	}
	for _, text := range []string{fmt.Sprint(r), fmt.Sprintf("%#v", r), func() string { v, _ := json.Marshal(r); return string(v) }()} {
		if strings.Contains(text, "never") || strings.Contains(text, "artifactToken") {
			t.Fatal("private metadata printable")
		}
	}
}

func TestBundleMalformedPrivateMetadataStaysPerRow(t *testing.T) {
	r, _, _ := bundleRecordFixture(t)
	for _, bad := range []string{`{"coverage":{"journalCreatedAt":"not-a-time"}}`, `{"rows":"not-an-integer"}`, `{"selection":{"systemType":"private"}}`, `null`} {
		raw, err := json.Marshal(map[string]any{"task": r.Task, "privateEpoch": r.Epoch, "privateFence": r.Fence, "privateResult": r.Result, "privatePayload": r.Payload, "privateSnapshot": json.RawMessage(bad), "privateManifest": r.Manifest})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeBundleRecord(raw)
		if err != nil {
			t.Fatal("private corruption erased task history", err)
		}
		d := projectBundleRecord(decoded)
		if d.Task.ID != r.Task.ID || d.Task.State != tasks.Succeeded || d.ArtifactStatus != "artifact_invalid" || d.DownloadReady {
			t.Fatal("bad metadata not isolated")
		}
	}
}

func TestBundleCanonicalUTCAndExactNestedManifest(t *testing.T) {
	s, events := bundleFixture()
	data, manifest, err := buildBundle(context.Background(), bundleTestID, s, events)
	if err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("synthetic", 2*60*60)
	s.CapturedAt = s.CapturedAt.In(zone)
	s.Coverage.JournalCreatedAt = s.Coverage.JournalCreatedAt.In(zone)
	first := s.Coverage.FirstRecordedAt.In(zone)
	s.Coverage.FirstRecordedAt = &first
	for i := range events {
		events[i].ObservedAt = events[i].ObservedAt.In(zone)
		events[i].RecordedAt = events[i].RecordedAt.In(zone)
	}
	again, _, err := buildBundle(context.Background(), bundleTestID, s, events)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatal("DB timezone changed canonical archive", err)
	}
	raw, _ := json.Marshal(manifest)
	for _, bad := range []string{strings.Replace(string(raw), `"firstRecordedAt":"2026-10-07T11:59:59.123456Z"`, `"extra":true`, 1), strings.Replace(string(raw), `"mode":"best_effort"`, `"mode":"best_effort","mode":"best_effort"`, 1), strings.Replace(string(raw), `"systemType":["api","worker"]`, `"systemType":["api","worker"],"systemType":["api","worker"]`, 1)} {
		if _, ok := decodeBundleManifest([]byte(bad)); ok {
			t.Fatal("ambiguous nested manifest accepted", bad)
		}
	}
}

func TestBundleHistoryFilterBoundaries(t *testing.T) {
	f, err := ParseHistoryFilter(url.Values{})
	if err != nil || f.PageIdx != 1 || f.PageSize != 20 {
		t.Fatal("defaults", err)
	}
	for _, q := range []string{"pageIdx=0", "pageIdx=01", "pageIdx=1000001", "pageSize=101", "pageSize=-1&pageIdx=2", "pageIdx=1&pageIdx=1", "status=SUCCESS", "status=running&status=running", "status=", "fileName=x", "tenant=default", "startTm=2026-01-01T00:00:00Z"} {
		v, _ := url.ParseQuery(q)
		if _, err := ParseHistoryFilter(v); err == nil {
			t.Errorf("accepted %s", q)
		}
	}
	f, err = ParseHistoryFilter(url.Values{"pageSize": {"-1"}, "status": {"succeeded", "running"}})
	if err != nil || f.Status[0] != "running" {
		t.Fatal("canonical states", err)
	}
}

func TestBundleKindFencesAndNeverRetries(t *testing.T) {
	k := Kind()
	if _, err := tasks.NewRegistry(k); err != nil {
		t.Fatal(err)
	}
	if !k.SingleAttemptOnly || k.ReplaySafe || k.Schedulable || !k.OwnerScoped || !k.CancelDiscardsResult || k.MaxAttempts != 1 || len(k.RetryCodes) != 0 || k.Timeout != 2*time.Minute {
		t.Fatal("unsafe bundle policy")
	}
	if got := k.Execute(context.Background(), tasks.Execution{}); got.Code != "fencing_required" || got.State != tasks.Failed {
		t.Fatal("unfenced execution accepted")
	}
	for _, err := range []error{tasks.ErrCancelRequested, tasks.ErrLeaseLost, tasks.ErrAuthorization, context.Canceled, context.DeadlineExceeded, errors.New("database password=never")} {
		got := bundleFailed(err)
		if got.Retryable || bytes.Contains(got.Result, []byte("never")) || strings.Contains(got.Code, "never") {
			t.Fatal("unsafe failed outcome")
		}
		if errors.Is(err, tasks.ErrCancelRequested) && got.State != tasks.Cancelled {
			t.Fatal("cancel lost")
		}
	}
}
