package operationallogs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type HistoryFilter struct {
	PageIdx, PageSize int
	Status            []string
}
type BundleDetail struct {
	Task           tasks.Task `json:"task"`
	DownloadReady  bool       `json:"downloadReady"`
	ArtifactStatus string     `json:"artifactStatus"`
	RowCount       *int       `json:"rowCount,omitempty"`
	SnapshotAt     *time.Time `json:"snapshotAt,omitempty"`
	DownloadPath   string     `json:"downloadPath,omitempty"`
	Coverage       *Coverage  `json:"coverage,omitempty"`
}
type BundleHistory struct {
	Page      Page           `json:"page"`
	List      []BundleDetail `json:"list"`
	Exhausted bool           `json:"exhausted"`
}

func ParseHistoryFilter(q url.Values) (HistoryFilter, error) {
	f := HistoryFilter{PageIdx: 1, PageSize: 20, Status: []string{}}
	bad := func() (HistoryFilter, error) { return HistoryFilter{}, problem(400, "invalid_input") }
	for key, values := range q {
		if len(values) == 0 {
			return bad()
		}
		if key == "status" {
			if len(values) > 9 {
				return bad()
			}
			seen := map[string]bool{}
			for _, v := range values {
				if !tasks.State(v).Valid() || seen[v] {
					return bad()
				}
				seen[v] = true
				f.Status = append(f.Status, v)
			}
			continue
		}
		if len(values) != 1 {
			return bad()
		}
		v := values[0]
		n, err := strconv.Atoi(v)
		if err != nil || strconv.Itoa(n) != v {
			return bad()
		}
		switch key {
		case "pageIdx":
			if n < 1 || n > 1000000 {
				return bad()
			}
			f.PageIdx = n
		case "pageSize":
			if n != -1 && (n < 1 || n > 100) {
				return bad()
			}
			f.PageSize = n
		default:
			return bad()
		}
	}
	if f.PageSize == -1 && f.PageIdx != 1 {
		return bad()
	}
	sort.Strings(f.Status)
	return f, nil
}

type bundleSnapshotMetadata struct {
	Matches    bool      `json:"matches"`
	Epoch      string    `json:"epoch"`
	Rows       int       `json:"rows"`
	CapturedAt time.Time `json:"capturedAt"`
	Selection  Selection `json:"selection"`
	Coverage   Coverage  `json:"coverage"`
}
type bundleArtifactMetadata struct {
	Token       int64           `json:"token"`
	Bytes       int64           `json:"bytes"`
	Chunks      int             `json:"chunks"`
	Rows        int             `json:"rows"`
	SHA256      string          `json:"sha256"`
	JSONLSHA256 string          `json:"jsonlSHA256"`
	Manifest    json.RawMessage `json:"manifest"`
}
type bundleRecord struct {
	Task     tasks.Task              `json:"task"`
	Epoch    string                  `json:"privateEpoch"`
	Fence    int64                   `json:"privateFence"`
	Result   json.RawMessage         `json:"privateResult"`
	Payload  json.RawMessage         `json:"privatePayload"`
	Snapshot *bundleSnapshotMetadata `json:"privateSnapshot"`
	Manifest *bundleArtifactMetadata `json:"privateManifest"`
}

func (bundleRecord) String() string   { return "[private operational bundle metadata]" }
func (bundleRecord) GoString() string { return "[private operational bundle metadata]" }
func (bundleRecord) MarshalJSON() ([]byte, error) {
	return []byte(`"[private operational bundle metadata]"`), nil
}

// Invalid private metadata affects only that row's eligibility. Decode it
// separately so a malformed timestamp or nested shape cannot erase history.
func decodeBundleRecord(raw []byte) (bundleRecord, error) {
	var wire struct {
		Task     tasks.Task      `json:"task"`
		Epoch    string          `json:"privateEpoch"`
		Fence    int64           `json:"privateFence"`
		Result   json.RawMessage `json:"privateResult"`
		Payload  json.RawMessage `json:"privatePayload"`
		Snapshot json.RawMessage `json:"privateSnapshot"`
		Manifest json.RawMessage `json:"privateManifest"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return bundleRecord{}, problem(500, "artifact_invalid")
	}
	r := bundleRecord{Task: wire.Task, Epoch: wire.Epoch, Fence: wire.Fence, Result: wire.Result, Payload: wire.Payload}
	decoder := json.NewDecoder(bytes.NewReader(wire.Snapshot))
	decoder.DisallowUnknownFields()
	if len(wire.Snapshot) <= 3*maxBundleRecordBytes && decoder.Decode(&r.Snapshot) == nil && r.Snapshot != nil {
		normalizeCoverage(&r.Snapshot.Coverage)
	} else {
		r.Snapshot = nil
	}
	if len(wire.Manifest) > 2*maxBundleRecordBytes || json.Unmarshal(wire.Manifest, &r.Manifest) != nil {
		r.Manifest = nil
	}
	return r, nil
}

const bundleProjection = `jsonb_build_object(
 'task',jsonb_build_object('taskUUID',t.task_id,'taskName',t.kind,'domainId',t.domain_id,'payloadVersion',t.payload_version,'state',t.state,'error',t.error_code,
 'createdAt',t.created_at,'updatedAt',t.updated_at,'terminalAt',t.terminal_at,'archived',false,'visibilityVersion',COALESCE(v.visibility_version,0),
 'attempt',t.attempt,'maxAttempts',t.max_attempts,'progress',t.progress,'resultVersion',t.result_version,'result','{}'::jsonb,'cursor','{}'::jsonb,
 'parentTaskUUID',t.parent_task_id,'nextAttemptAt',t.next_attempt_at),
 'privateEpoch',t.authorization_version,'privateFence',t.fencing_token,
 'privateResult',CASE WHEN octet_length(t.result::text)<=4096 THEN t.result ELSE NULL END,
 'privatePayload',CASE WHEN octet_length(t.payload::text)<=4096 THEN t.payload ELSE NULL END,
 'privateSnapshot',CASE WHEN s.task_id IS NULL THEN NULL ELSE jsonb_build_object('matches',s.tenant_id=t.tenant_id AND s.actor_id=t.actor_id,'epoch',s.authorization_version,'rows',s.row_count,'capturedAt',s.captured_at,'selection',s.selection,'coverage',s.coverage) END,
 'privateManifest',CASE WHEN m.task_id IS NULL THEN NULL ELSE jsonb_build_object('token',m.fencing_token,'bytes',m.byte_count,'chunks',m.chunk_count,'rows',m.row_count,'sha256',m.sha256,'jsonlSHA256',m.jsonl_sha256,'manifest',m.manifest) END)`

const bundleJoins = ` LEFT JOIN adtr.task_visibility v ON v.task_id=t.task_id AND v.tenant_id=t.tenant_id
 LEFT JOIN adtr.operational_log_bundle_snapshots s ON s.task_id=t.task_id AND s.fencing_token=t.fencing_token
 LEFT JOIN adtr.operational_log_bundle_manifests m ON m.task_id=t.task_id AND m.fencing_token=t.fencing_token `
const bundleWhere = `t.tenant_id=$1 AND t.actor_id=$2 AND t.authorization_version=$3 AND t.kind='system.logs_bundle' AND t.domain_id='platform' AND NOT COALESCE(v.archived,false)`

func decodeBundleResult(raw json.RawMessage) (bundleResult, bool) {
	var r bundleResult
	if !bundleObject(raw, "artifactToken", "rowCount", "snapshotAt", "sha256", "byteCount") || json.Unmarshal(raw, &r) != nil || r.ArtifactToken <= 0 || r.RowCount < 0 || r.RowCount > MaxBundleRows || r.ByteCount <= 0 || r.ByteCount > MaxBundleBytes || r.SnapshotAt.IsZero() || !bundleDigestPattern.MatchString(r.SHA256) {
		return bundleResult{}, false
	}
	r.SnapshotAt = r.SnapshotAt.UTC()
	return r, true
}

func decodeBundleManifest(raw json.RawMessage) (bundleManifest, bool) {
	var m bundleManifest
	if !bundleObject(raw, "formatVersion", "taskUUID", "snapshotAt", "rowCount", "selection", "coverage", "jsonlSHA256") {
		return m, false
	}
	var nested struct {
		Selection json.RawMessage `json:"selection"`
		Coverage  json.RawMessage `json:"coverage"`
	}
	if json.Unmarshal(raw, &nested) != nil || !bundleObject(nested.Selection, "startTm", "endTm", "systemType") || !bundleObjectWithNull(nested.Coverage, "firstRecordedAt", "mode", "gapsPossible", "journalCreatedAt", "firstRecordedAt") {
		return m, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(&struct{}{}) != io.EOF || m.FormatVersion != 1 || !validUUID(m.TaskUUID) || m.RowCount < 0 || m.RowCount > MaxBundleRows || !bundleDigestPattern.MatchString(m.JSONLSHA256) || !validBundleSnapshot(bundleSnapshot{m.RowCount, m.SnapshotAt, m.Selection, m.Coverage}) {
		return bundleManifest{}, false
	}
	m.SnapshotAt = m.SnapshotAt.UTC()
	normalizeCoverage(&m.Coverage)
	return m, true
}

func bundleMetadataStatus(r bundleRecord) (string, bundleResult, bundleManifest) {
	result, ok := decodeBundleResult(r.Result)
	m := r.Manifest
	s := r.Snapshot
	invalid := func() (string, bundleResult, bundleManifest) {
		return "artifact_invalid", bundleResult{}, bundleManifest{}
	}
	if !ok || s == nil || m == nil || !s.Matches || s.Epoch != r.Epoch || r.Fence != result.ArtifactToken || m.Token != result.ArtifactToken || m.Bytes != result.ByteCount || m.Rows != result.RowCount || s.Rows != result.RowCount || !s.CapturedAt.Equal(result.SnapshotAt) || m.SHA256 != result.SHA256 || m.Chunks != int((m.Bytes+bundleChunkBytes-1)/bundleChunkBytes) || !validBundleSnapshot(bundleSnapshot{s.Rows, s.CapturedAt, s.Selection, s.Coverage}) {
		return invalid()
	}
	payload, err := ValidateBundlePayload(r.Payload)
	if err != nil {
		return invalid()
	}
	selection, _ := json.Marshal(s.Selection)
	if !bytes.Equal(payload, selection) {
		return invalid()
	}
	manifest, ok := decodeBundleManifest(m.Manifest)
	if !ok || manifest.TaskUUID != r.Task.ID || manifest.RowCount != result.RowCount || !manifest.SnapshotAt.Equal(result.SnapshotAt) || manifest.JSONLSHA256 != m.JSONLSHA256 {
		return invalid()
	}
	want := bundleManifest{1, r.Task.ID, s.CapturedAt.UTC(), s.Rows, s.Selection, s.Coverage, m.JSONLSHA256}
	wantRaw, _ := json.Marshal(want)
	gotRaw, _ := json.Marshal(manifest)
	if !bytes.Equal(wantRaw, gotRaw) {
		return invalid()
	}
	return "eligible", result, manifest
}

func projectBundleRecord(r bundleRecord) BundleDetail {
	t := r.Task
	t.Result = json.RawMessage(`{}`)
	t.Cursor = json.RawMessage(`{}`)
	t.SourceState = t.State.Source()
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	if t.TerminalAt != nil {
		v := t.TerminalAt.UTC()
		t.TerminalAt = &v
	}
	if t.NextAttemptAt != nil {
		v := t.NextAttemptAt.UTC()
		t.NextAttemptAt = &v
	}
	if t.Error != "" && !bundleErrorPattern.MatchString(t.Error) {
		t.Error = "bundle_failed"
	}
	out := BundleDetail{Task: t, ArtifactStatus: "not_ready"}
	if t.State != tasks.Succeeded {
		return out
	}
	status, result, manifest := bundleMetadataStatus(r)
	out.ArtifactStatus = status
	if status == "eligible" {
		out.DownloadReady = true
		out.RowCount = &result.RowCount
		out.SnapshotAt = &result.SnapshotAt
		out.DownloadPath = "/api/system/logs/bundles/download?taskUUID=" + t.ID
		out.Coverage = &manifest.Coverage
	}
	return out
}

func loadBundleRecord(ctx context.Context, tx pgx.Tx, p Principal, id, epoch string) (bundleRecord, error) {
	var r bundleRecord
	var raw []byte
	if !validUUID(id) {
		return r, problem(400, "invalid_input")
	}
	err := tx.QueryRow(ctx, `SELECT `+bundleProjection+` FROM adtr.tasks t `+bundleJoins+` WHERE `+bundleWhere+` AND t.task_id=$4`, p.TenantID, p.ActorID, epoch, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, problem(404, "not_found")
	}
	if err != nil {
		return r, err
	}
	return decodeBundleRecord(raw)
}
func DetailTx(ctx context.Context, tx pgx.Tx, p Principal, id string) (BundleDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	epoch, err := RequireReadTx(ctx, tx, p)
	if err != nil {
		return BundleDetail{}, err
	}
	r, err := loadBundleRecord(ctx, tx, p, id, epoch)
	if err != nil {
		return BundleDetail{}, err
	}
	return projectBundleRecord(r), nil
}

func HistoryTx(ctx context.Context, tx pgx.Tx, p Principal, f HistoryFilter) (BundleHistory, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	out := BundleHistory{List: []BundleDetail{}}
	epoch, err := RequireReadTx(ctx, tx, p)
	if err != nil {
		return out, err
	}
	q := url.Values{}
	if f.PageIdx != 0 {
		q.Set("pageIdx", strconv.Itoa(f.PageIdx))
	}
	if f.PageSize != 0 {
		q.Set("pageSize", strconv.Itoa(f.PageSize))
	}
	if len(f.Status) > 0 {
		q["status"] = f.Status
	}
	f, err = ParseHistoryFilter(q)
	if err != nil {
		return out, err
	}
	size := f.PageSize
	if size == -1 {
		size = 1001
	}
	offset := (f.PageIdx - 1) * size
	query := `WITH selected AS MATERIALIZED (SELECT t.* FROM adtr.tasks t LEFT JOIN adtr.task_visibility v ON v.task_id=t.task_id AND v.tenant_id=t.tenant_id WHERE ` + bundleWhere + ` AND (cardinality($4::text[])=0 OR t.state=ANY($4::text[]))), page_rows AS MATERIALIZED (SELECT * FROM selected ORDER BY created_at DESC,task_id COLLATE "C" DESC LIMIT $5 OFFSET $6), enriched AS (SELECT t.created_at,t.task_id,` + bundleProjection + ` row_data FROM page_rows t ` + bundleJoins + `) SELECT (SELECT count(*) FROM selected),COALESCE((SELECT jsonb_agg(row_data ORDER BY created_at DESC,task_id COLLATE "C" DESC) FROM enriched),'[]'::jsonb)`
	var total int
	var raw []byte
	if err = tx.QueryRow(ctx, query, p.TenantID, p.ActorID, epoch, f.Status, size, offset).Scan(&total, &raw); err != nil {
		return out, err
	}
	if f.PageSize == -1 && total > 1000 {
		return out, problem(422, "result_too_large")
	}
	var records []json.RawMessage
	if json.Unmarshal(raw, &records) != nil {
		return out, problem(500, "artifact_invalid")
	}
	for _, raw := range records {
		r, err := decodeBundleRecord(raw)
		if err != nil {
			return out, err
		}
		out.List = append(out.List, projectBundleRecord(r))
	}
	pages := 0
	if total > 0 {
		if f.PageSize == -1 {
			pages = 1
		} else {
			pages = (total + size - 1) / size
		}
	}
	out.Page = Page{f.PageIdx, f.PageSize, total, pages}
	out.Exhausted = offset+len(out.List) >= total
	return out, nil
}
