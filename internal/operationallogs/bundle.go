package operationallogs

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/tasks"
)

const (
	BundleKindName       = "system.logs_bundle"
	MaxBundleRows        = 10000
	MaxBundleBytes       = 16 * 1024 * 1024
	bundleChunkBytes     = 1024 * 1024
	maxBundleRecordBytes = 4096
)

type BundlePayload struct {
	StartTm    string   `json:"startTm"`
	EndTm      string   `json:"endTm"`
	SystemType []string `json:"systemType"`
}

// bundleObject rejects missing, duplicate, unknown and null fields before a
// typed decode. In particular, proof/identity/path fields never enter tasks.
func bundleObject(raw []byte, keys ...string) bool {
	return bundleObjectWithNull(raw, "", keys...)
}
func bundleObjectWithNull(raw []byte, nullable string, keys ...string) bool {
	if len(raw) == 0 || len(raw) > maxBundleRecordBytes || !utf8.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		t, err = d.Token()
		k, ok := t.(string)
		if err != nil || !ok || seen[k] {
			return false
		}
		allowed := false
		for _, key := range keys {
			allowed = allowed || k == key
		}
		if !allowed {
			return false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || k != nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		seen[k] = true
	}
	if t, err = d.Token(); err != nil || t != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return err == io.EOF && len(seen) == len(keys)
}

func ValidateBundlePayload(raw json.RawMessage) (json.RawMessage, error) {
	if !bundleObject(raw, "startTm", "endTm", "systemType") {
		return nil, problem(400, "invalid_input")
	}
	var p BundlePayload
	if json.Unmarshal(raw, &p) != nil || p.StartTm == "" || p.EndTm == "" || len(p.SystemType) == 0 {
		return nil, problem(400, "invalid_input")
	}
	s, err := NormalizeSelection(p.StartTm, p.EndTm, p.SystemType)
	if err != nil {
		return nil, err
	}
	return json.Marshal(BundlePayload{s.StartTm, s.EndTm, s.SystemType})
}

func Kind() tasks.Kind {
	return tasks.Kind{Name: BundleKindName, Version: 1, Platform: true, MaxAttempts: 1, Lease: 30 * time.Second, Heartbeat: 5 * time.Second, Timeout: 2 * time.Minute, RetryBase: 10 * time.Millisecond, RetryCap: 10 * time.Millisecond, SingleAttemptOnly: true, OwnerScoped: true, CancelDiscardsResult: true, Validate: ValidateBundlePayload, Execute: executeBundle}
}

type bundleSnapshot struct {
	Rows       int
	CapturedAt time.Time
	Selection  Selection
	Coverage   Coverage
}
type bundleManifest struct {
	FormatVersion int       `json:"formatVersion"`
	TaskUUID      string    `json:"taskUUID"`
	SnapshotAt    time.Time `json:"snapshotAt"`
	RowCount      int       `json:"rowCount"`
	Selection     Selection `json:"selection"`
	Coverage      Coverage  `json:"coverage"`
	JSONLSHA256   string    `json:"jsonlSHA256"`
}
type bundleResult struct {
	ArtifactToken int64     `json:"artifactToken"`
	RowCount      int       `json:"rowCount"`
	SnapshotAt    time.Time `json:"snapshotAt"`
	SHA256        string    `json:"sha256"`
	ByteCount     int64     `json:"byteCount"`
}

var bundleDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var bundleErrorPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func bundleDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

type bundleLimitBuffer struct{ bytes.Buffer }

func (b *bundleLimitBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxBundleBytes-b.Len() {
		return 0, problem(422, "artifact_too_large")
	}
	return b.Buffer.Write(p)
}

// buildBundle never opens files or chooses names from data. Store compression is
// deliberate: the entire exact archive, including headers, stays under the cap.
func buildBundle(ctx context.Context, id string, s bundleSnapshot, events []Event) ([]byte, bundleManifest, error) {
	var manifest bundleManifest
	s.CapturedAt = s.CapturedAt.UTC()
	normalizeCoverage(&s.Coverage)
	if !validUUID(id) || s.Rows != len(events) || s.Rows < 0 || s.Rows > MaxBundleRows || !validBundleSnapshot(s) {
		return nil, manifest, problem(500, "snapshot_invalid")
	}
	var jsonl bundleLimitBuffer
	var previous *Event
	for i := range events {
		if err := ctx.Err(); err != nil {
			return nil, manifest, err
		}
		e := events[i]
		e.ObservedAt = e.ObservedAt.UTC()
		e.RecordedAt = e.RecordedAt.UTC()
		if validateEvent(e, true) != nil || !bundleEventSelected(e, s.Selection) || previous != nil && !bundleEventBefore(*previous, e) {
			return nil, manifest, problem(500, "snapshot_invalid")
		}
		line, err := json.Marshal(e)
		if err != nil || len(line) > maxBundleRecordBytes {
			return nil, manifest, problem(500, "snapshot_invalid")
		}
		if _, err = jsonl.Write(append(line, '\n')); err != nil {
			return nil, manifest, err
		}
		previous = &events[i]
	}
	manifest = bundleManifest{1, id, s.CapturedAt.UTC(), s.Rows, s.Selection, s.Coverage, bundleDigest(jsonl.Bytes())}
	raw, err := json.Marshal(manifest)
	if err != nil || len(raw) > maxBundleRecordBytes {
		return nil, manifest, problem(500, "snapshot_invalid")
	}
	var output bundleLimitBuffer
	w := zip.NewWriter(&output)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", raw}, {"events.jsonl", jsonl.Bytes()}} {
		if err = ctx.Err(); err != nil {
			return nil, manifest, err
		}
		h := &zip.FileHeader{Name: entry.name, Method: zip.Store, Modified: s.CapturedAt.UTC()}
		h.SetMode(0600)
		part, e := w.CreateHeader(h)
		if e != nil {
			return nil, manifest, e
		}
		if _, e = part.Write(entry.data); e != nil {
			return nil, manifest, e
		}
	}
	if err = w.Close(); err != nil {
		return nil, manifest, err
	}
	return output.Bytes(), manifest, nil
}

func validBundleSnapshot(s bundleSnapshot) bool {
	if s.CapturedAt.IsZero() || s.CapturedAt.Year() < 1980 || s.CapturedAt.Nanosecond()%1000 != 0 || s.Coverage.Mode != "best_effort" || !s.Coverage.GapsPossible || s.Coverage.JournalCreatedAt.IsZero() || s.Coverage.JournalCreatedAt.After(s.CapturedAt) || s.Coverage.FirstRecordedAt != nil && (s.Coverage.FirstRecordedAt.IsZero() || s.Coverage.FirstRecordedAt.After(s.CapturedAt)) {
		return false
	}
	canonical, err := NormalizeSelection(s.Selection.StartTm, s.Selection.EndTm, s.Selection.SystemType)
	if err != nil || s.Selection.StartTm == "" || s.Selection.EndTm == "" || len(s.Selection.SystemType) == 0 {
		return false
	}
	a, _ := json.Marshal(canonical)
	b, _ := json.Marshal(s.Selection)
	return bytes.Equal(a, b)
}
func bundleEventSelected(e Event, s Selection) bool {
	start, err := time.Parse(time.RFC3339Nano, s.StartTm)
	if err != nil {
		return false
	}
	end, err := time.Parse(time.RFC3339Nano, s.EndTm)
	if err != nil {
		return false
	}
	if e.RecordedAt.Before(start) || !e.RecordedAt.Before(end) {
		return false
	}
	for _, m := range s.SystemType {
		if string(e.Module) == m {
			return true
		}
	}
	return false
}
func bundleEventBefore(a, b Event) bool {
	return a.RecordedAt.After(b.RecordedAt) || a.RecordedAt.Equal(b.RecordedAt) && a.EventID > b.EventID
}

type bundleRun struct {
	ctx      context.Context
	ex       tasks.Execution
	version  int64
	progress int
	payload  BundlePayload
	snapshot bundleSnapshot
}

func (r *bundleRun) tx(fn func(context.Context, pgx.Tx) error) error {
	v, err := r.ex.WithTx(r.ctx, r.version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		if err := RequireSchema(ctx, tx); err != nil {
			return 0, nil, nil, err
		}
		epoch, err := bundleAuthority(ctx, tx, Principal{r.ex.Task.TenantID, r.ex.Task.ActorID}, true)
		if err != nil {
			return 0, nil, nil, err
		}
		if epoch != r.ex.Task.AuthorizationVersion {
			return 0, nil, nil, tasks.ErrAuthorization
		}
		if err = fn(ctx, tx); err != nil {
			return 0, nil, nil, err
		}
		return r.progress, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
	})
	if err == nil {
		r.version = v
	}
	return err
}

func executeBundle(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	if ex.WithTx == nil {
		return tasks.Outcome{State: tasks.Failed, Code: "fencing_required"}
	}
	raw, err := ValidateBundlePayload(ex.Task.Payload)
	if err != nil {
		return bundleFailed(err)
	}
	r := &bundleRun{ctx: ctx, ex: ex, version: ex.Task.ResultVersion, progress: max(10, ex.Task.Progress)}
	if json.Unmarshal(raw, &r.payload) != nil {
		return bundleFailed(problem(400, "invalid_input"))
	}
	if err = r.capture(); err != nil {
		return bundleFailed(err)
	}
	events := make([]Event, 0, r.snapshot.Rows)
	err = r.tx(func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT ordinal,event_id,event_data FROM adtr.operational_log_bundle_rows WHERE task_id=$1 AND fencing_token=$2 ORDER BY ordinal`, ex.Task.ID, ex.Task.FencingToken)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var ordinal int
			var eventID string
			var data []byte
			var event Event
			if e = rows.Scan(&ordinal, &eventID, &data); e != nil {
				return e
			}
			if ordinal != len(events)+1 || ordinal > MaxBundleRows || !bundleObject(data, "schemaVersion", "eventId", "processId", "module", "code", "outcome", "severity", "reason", "observedAt", "recordedAt") || json.Unmarshal(data, &event) != nil || event.EventID != eventID {
				return problem(500, "snapshot_invalid")
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	if err != nil {
		return bundleFailed(err)
	}
	data, manifest, err := buildBundle(ctx, ex.Task.ID, r.snapshot, events)
	if err != nil {
		return bundleFailed(err)
	}
	r.progress = 80
	chunks := 0
	for offset := 0; offset < len(data); offset += bundleChunkBytes {
		part := data[offset:min(offset+bundleChunkBytes, len(data))]
		index := chunks
		err = r.tx(func(ctx context.Context, tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `INSERT INTO adtr.operational_log_bundle_chunks(task_id,fencing_token,chunk_no,data,sha256) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, ex.Task.ID, ex.Task.FencingToken, index, part, bundleDigest(part))
			if e != nil {
				return e
			}
			var matches bool
			e = tx.QueryRow(ctx, `SELECT data=$4 AND sha256=$5 FROM adtr.operational_log_bundle_chunks WHERE task_id=$1 AND fencing_token=$2 AND chunk_no=$3`, ex.Task.ID, ex.Task.FencingToken, index, part, bundleDigest(part)).Scan(&matches)
			if e != nil {
				return e
			}
			if !matches {
				return problem(409, "artifact_conflict")
			}
			return nil
		})
		if err != nil {
			return bundleFailed(err)
		}
		chunks++
	}
	result := bundleResult{ex.Task.FencingToken, r.snapshot.Rows, r.snapshot.CapturedAt, bundleDigest(data), int64(len(data))}
	manifestJSON, _ := json.Marshal(manifest)
	r.progress = 95
	err = r.tx(func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.operational_log_bundle_manifests(task_id,fencing_token,byte_count,chunk_count,row_count,sha256,jsonl_sha256,manifest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, ex.Task.ID, ex.Task.FencingToken, result.ByteCount, chunks, result.RowCount, result.SHA256, manifest.JSONLSHA256, manifestJSON)
		if e != nil {
			return e
		}
		var matches bool
		e = tx.QueryRow(ctx, `SELECT byte_count=$3 AND chunk_count=$4 AND row_count=$5 AND sha256=$6 AND jsonl_sha256=$7 AND manifest=$8::jsonb FROM adtr.operational_log_bundle_manifests WHERE task_id=$1 AND fencing_token=$2`, ex.Task.ID, ex.Task.FencingToken, result.ByteCount, chunks, result.RowCount, result.SHA256, manifest.JSONLSHA256, manifestJSON).Scan(&matches)
		if e != nil {
			return e
		}
		if !matches {
			return problem(409, "artifact_conflict")
		}
		return nil
	})
	if err != nil {
		return bundleFailed(err)
	}
	output, _ := json.Marshal(result)
	return tasks.Outcome{State: tasks.Succeeded, Result: output}
}

func (r *bundleRun) capture() error {
	return r.tx(func(ctx context.Context, tx pgx.Tx) error {
		t := r.ex.Task
		selection, _ := json.Marshal(Selection{r.payload.StartTm, r.payload.EndTm, r.payload.SystemType})
		var storedTenant, storedEpoch string
		var actor int64
		var storedSelection, coverage []byte
		err := tx.QueryRow(ctx, `SELECT tenant_id,actor_id,authorization_version,row_count,captured_at,selection,coverage FROM adtr.operational_log_bundle_snapshots WHERE task_id=$1 AND fencing_token=$2`, t.ID, t.FencingToken).Scan(&storedTenant, &actor, &storedEpoch, &r.snapshot.Rows, &r.snapshot.CapturedAt, &storedSelection, &coverage)
		if err == nil {
			if storedTenant != t.TenantID || actor != t.ActorID || storedEpoch != t.AuthorizationVersion || json.Unmarshal(storedSelection, &r.snapshot.Selection) != nil || json.Unmarshal(coverage, &r.snapshot.Coverage) != nil {
				return problem(409, "snapshot_conflict")
			}
			canonical, _ := json.Marshal(r.snapshot.Selection)
			if !bytes.Equal(canonical, selection) {
				return problem(409, "snapshot_conflict")
			}
			r.snapshot.CapturedAt = r.snapshot.CapturedAt.UTC()
			normalizeCoverage(&r.snapshot.Coverage)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// The sentinel is rejected by the row-count constraint, rolling back both
		// snapshot and rows. Selection, count, coverage and rows share one snapshot.
		query := `WITH selected AS MATERIALIZED (SELECT *,row_number() OVER(ORDER BY recorded_at DESC,event_id COLLATE "C" DESC) ordinal FROM adtr.operational_log_events WHERE recorded_at >= $6::timestamptz AND recorded_at < $7::timestamptz AND module=ANY($8::text[]) ORDER BY recorded_at DESC,event_id COLLATE "C" DESC LIMIT 10001), frozen AS (
 INSERT INTO adtr.operational_log_bundle_snapshots(task_id,fencing_token,tenant_id,actor_id,authorization_version,row_count,selection,coverage)
 SELECT $1,$2,$3,$4,$5,(SELECT count(*) FROM selected),$9::jsonb,jsonb_build_object('mode','best_effort','gapsPossible',true,'journalCreatedAt',created_at,'firstRecordedAt',(SELECT min(recorded_at) FROM adtr.operational_log_events)) FROM adtr.operational_log_state WHERE singleton RETURNING *
), copied AS (INSERT INTO adtr.operational_log_bundle_rows(task_id,fencing_token,ordinal,event_id,event_data) SELECT frozen.task_id,frozen.fencing_token,selected.ordinal,selected.event_id,` + eventJSON + ` FROM selected CROSS JOIN frozen RETURNING ordinal)
 SELECT row_count,captured_at,selection,coverage,(SELECT count(*) FROM copied) FROM frozen`
		var inserted int
		err = tx.QueryRow(ctx, query, t.ID, t.FencingToken, t.TenantID, t.ActorID, t.AuthorizationVersion, r.payload.StartTm, r.payload.EndTm, r.payload.SystemType, selection).Scan(&r.snapshot.Rows, &r.snapshot.CapturedAt, &storedSelection, &coverage, &inserted)
		if err != nil {
			return err
		}
		if inserted != r.snapshot.Rows || json.Unmarshal(storedSelection, &r.snapshot.Selection) != nil || json.Unmarshal(coverage, &r.snapshot.Coverage) != nil {
			return problem(500, "snapshot_invalid")
		}
		r.snapshot.CapturedAt = r.snapshot.CapturedAt.UTC()
		normalizeCoverage(&r.snapshot.Coverage)
		return nil
	})
}

func bundleFailed(err error) tasks.Outcome {
	o := tasks.Outcome{State: tasks.Failed, Code: "bundle_failed"}
	var e *Error
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, tasks.ErrCancelRequested):
		o.State = tasks.Cancelled
		o.Code = "cancelled"
	case errors.Is(err, tasks.ErrAuthorization):
		o.Code = "authorization_revoked"
	case errors.Is(err, tasks.ErrLeaseLost):
		o.Code = "lease_lost"
	case errors.Is(err, context.Canceled):
		o.Code = "worker_interrupted"
	case errors.Is(err, context.DeadlineExceeded):
		o.Code = "bundle_timeout"
	case errors.As(err, &e):
		o.Code = e.Code
	case errors.As(err, &pg) && pg.ConstraintName == "operational_log_bundle_row_limit":
		o.Code = "result_too_large"
	}
	return o
}

func ArtifactFilename(id string) string { return "system-logs-" + id + ".zip" }
