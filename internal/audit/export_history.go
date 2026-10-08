package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

const maxHistoryResultBytes = 4096

var exportDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var historyErrorPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// snapshotScopeSQL accepts only internal SQL expressions. It is shared by the
// list and dedicated detail check so captured-domain policy cannot drift.
func snapshotScopeSQL(taskIDExpression string) string {
	return `NOT EXISTS(SELECT FROM (SELECT DISTINCT domain_id FROM adtr.audit_export_rows WHERE task_id=` + taskIDExpression + `
 AND domain_id IS NOT NULL AND NOT(source='task' AND domain_id='platform')) captured
 WHERE NOT EXISTS(SELECT FROM adtr.resource_tenant_config c
 JOIN adtr.resource_domains d ON d.tenant_id=c.tenant_id
 JOIN adtr.resource_group_members m ON m.tenant_id=d.tenant_id AND m.domain_id=d.id
 JOIN adtr.resource_role_groups g ON g.tenant_id=m.tenant_id AND g.group_id=m.group_id
 JOIN adtr.users scope_user ON scope_user.tenant_id=g.tenant_id AND COALESCE(NULLIF(scope_user.role_id,''),scope_user.role)=g.role_id
 WHERE c.tenant_id=$1 AND scope_user.id=$2 AND d.id=captured.domain_id AND d.active AND c.max_ad_count>0
 AND c.expire_time>extract(epoch FROM clock_timestamp())
 AND (SELECT count(*) FROM adtr.resource_domains counted WHERE counted.tenant_id=c.tenant_id AND counted.active)<=c.max_ad_count))`
}

type artifactSnapshotMetadata struct {
	Matches    bool      `json:"matches"`
	Epoch      string    `json:"epoch"`
	Rows       int       `json:"rows"`
	CapturedAt time.Time `json:"capturedAt"`
	Visibility int64     `json:"visibility"`
}
type artifactManifestMetadata struct {
	Token  int64  `json:"token"`
	Bytes  int64  `json:"bytes"`
	Chunks int    `json:"chunks"`
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

// These structures never cross an HTTP boundary. Result JSON stays private even
// if corrupt storage contains data that cannot be trusted as an export result.
type historyRecord struct {
	ExportHistoryRow
	Result     json.RawMessage           `json:"privateResult"`
	Epoch      string                    `json:"privateEpoch"`
	Snapshot   *artifactSnapshotMetadata `json:"privateSnapshot"`
	Manifest   *artifactManifestMetadata `json:"privateManifest"`
	Visibility int64                     `json:"privateVisibility"`
}

func (historyRecord) String() string   { return "[private export history metadata]" }
func (historyRecord) GoString() string { return "[private export history metadata]" }
func (historyRecord) MarshalJSON() ([]byte, error) {
	return []byte(`"[private export history metadata]"`), nil
}

func decodeArtifactResult(raw json.RawMessage) (exportResult, bool) {
	var out exportResult
	if len(raw) == 0 || len(raw) > maxHistoryResultBytes {
		return out, false
	}
	keys := map[string]json.RawMessage{}
	object := json.NewDecoder(bytes.NewReader(raw))
	token, err := object.Token()
	if err != nil || token != json.Delim('{') {
		return out, false
	}
	for object.More() {
		token, err = object.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return out, false
		}
		if _, duplicate := keys[key]; duplicate {
			return out, false
		}
		var value json.RawMessage
		if object.Decode(&value) != nil {
			return out, false
		}
		keys[key] = value
	}
	if token, err = object.Token(); err != nil || token != json.Delim('}') {
		return out, false
	}
	if _, err = object.Token(); err != io.EOF || len(keys) != 5 {
		return out, false
	}
	for _, key := range []string{"artifactToken", "rowCount", "snapshotAt", "sha256", "byteCount"} {
		v, ok := keys[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return out, false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(&struct{}{}) != io.EOF || out.ArtifactToken <= 0 || out.RowCount < 0 || out.RowCount > MaxExportRows || out.ByteCount < 1 || out.ByteCount > MaxArtifactBytes || out.SnapshotAt.IsZero() || !exportDigestPattern.MatchString(out.SHA256) {
		return exportResult{}, false
	}
	out.SnapshotAt = out.SnapshotAt.UTC()
	return out, true
}
func artifactMetadataStatus(raw json.RawMessage, epoch string, snapshot *artifactSnapshotMetadata, manifest *artifactManifestMetadata, visibility int64) (string, exportResult) {
	result, ok := decodeArtifactResult(raw)
	if !ok || snapshot == nil || !snapshot.Matches || snapshot.Epoch != epoch {
		return "artifact_invalid", exportResult{}
	}
	if snapshot.Visibility != visibility {
		return "snapshot_changed", exportResult{}
	}
	if manifest == nil || manifest.Token != result.ArtifactToken || manifest.Bytes != result.ByteCount || manifest.Chunks < 1 || manifest.Rows != result.RowCount || snapshot.Rows != result.RowCount || !snapshot.CapturedAt.Equal(result.SnapshotAt) || manifest.SHA256 != result.SHA256 {
		return "artifact_invalid", exportResult{}
	}
	return "eligible", result
}
func projectHistoryRecord(in historyRecord) ExportHistoryRow {
	out := in.ExportHistoryRow
	out.FileName = ArtifactFilename(out.TaskUUID)
	out.ModelType = "Audit"
	out.FileType = "xlsx"
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	if out.NextAttemptAt != nil {
		v := out.NextAttemptAt.UTC()
		out.NextAttemptAt = &v
	}
	if out.Error != "" && !historyErrorPattern.MatchString(out.Error) {
		out.Error = "export_failed"
	}
	out.DownloadReady = false
	out.DownloadStatus = "not_ready"
	out.RowCount = nil
	out.SnapshotAt = nil
	out.DownloadPath = ""
	if out.State != tasks.Succeeded {
		return out
	}
	status, result := artifactMetadataStatus(in.Result, in.Epoch, in.Snapshot, in.Manifest, in.Visibility)
	out.DownloadStatus = status
	if status == "eligible" {
		out.DownloadReady = true
		out.RowCount = &result.RowCount
		out.SnapshotAt = &result.SnapshotAt
		out.DownloadPath = "/api/audit/exports/download?taskUUID=" + out.TaskUUID
	}
	return out
}
func normalizeHistoryFilter(f HistoryFilter) (HistoryFilter, error) {
	q := url.Values{}
	if f.PageIdx != 0 {
		q.Set("pageIdx", strconv.Itoa(f.PageIdx))
	}
	if f.PageSize != 0 {
		q.Set("pageSize", strconv.Itoa(f.PageSize))
	}
	if f.SortTm != 0 {
		q.Set("sortTm", strconv.Itoa(f.SortTm))
	}
	if len(f.Status) > 0 {
		q["status"] = append([]string(nil), f.Status...)
	}
	if f.StartTm != "" {
		q.Set("startTm", f.StartTm)
	}
	if f.EndTm != "" {
		q.Set("endTm", f.EndTm)
	}
	return ParseHistoryFilter(q)
}

func HistoryTx(ctx context.Context, tx pgx.Tx, p Principal, f HistoryFilter) (ExportHistory, error) {
	out := ExportHistory{List: []ExportHistoryRow{}}
	if err := requireSchema(ctx, tx); err != nil {
		return out, err
	}
	var err error
	f, err = normalizeHistoryFilter(f)
	if err != nil {
		return out, err
	}
	args := []any{p.TenantID, p.ActorID, f.Status}
	where := `t.tenant_id=$1 AND t.actor_id=$2 AND t.kind='audit.export' AND t.domain_id='platform'
 AND t.authorization_version=u.authorization_version::text
 AND NOT EXISTS(SELECT FROM adtr.task_visibility v WHERE v.tenant_id=t.tenant_id AND v.task_id=t.task_id AND v.archived)
 AND ` + snapshotScopeSQL("t.task_id") + ` AND (cardinality($3::text[])=0 OR t.state=ANY($3::text[]))`
	if f.StartTm != "" {
		args = append(args, f.StartTm)
		where += fmt.Sprintf(" AND t.created_at >= $%d::timestamptz", len(args))
	}
	if f.EndTm != "" {
		args = append(args, f.EndTm)
		where += fmt.Sprintf(" AND t.created_at < $%d::timestamptz", len(args))
	}
	size := f.PageSize
	if size == -1 {
		size = 1001
	}
	offset := (f.PageIdx - 1) * size
	args = append(args, size, offset)
	direction := "DESC"
	if f.SortTm == 1 {
		direction = "ASC"
	}
	order := "created_at " + direction + ",task_id COLLATE \"C\" " + direction
	query := `WITH selected AS MATERIALIZED (
 SELECT t.task_id,t.state,t.progress,t.error_code,t.created_at,t.updated_at,t.attempt,t.max_attempts,t.next_attempt_at,t.authorization_version
 FROM adtr.tasks t JOIN adtr.users u ON u.tenant_id=t.tenant_id AND u.id=t.actor_id WHERE ` + where + `
 ), page_rows AS MATERIALIZED (SELECT * FROM selected ORDER BY ` + order + fmt.Sprintf(` LIMIT $%d OFFSET $%d), enriched AS (
 SELECT p.*,jsonb_build_object('taskUUID',p.task_id,'state',p.state,'progress',p.progress,'error',p.error_code,
 'createdAt',p.created_at,'updatedAt',p.updated_at,'attempt',p.attempt,'maxAttempts',p.max_attempts,'nextAttemptAt',p.next_attempt_at,
 'privateEpoch',p.authorization_version,'privateResult',CASE WHEN octet_length(raw.result::text)<=4096 THEN raw.result ELSE NULL END,
 'privateVisibility',COALESCE((SELECT revision FROM adtr.audit_visibility_revisions WHERE tenant_id=$1),0),
 'privateSnapshot',CASE WHEN s.task_id IS NULL THEN NULL ELSE jsonb_build_object('matches',s.tenant_id=$1 AND s.actor_id=$2,'epoch',s.authorization_version,'rows',s.row_count,'capturedAt',s.captured_at,'visibility',s.visibility_revision) END,
 'privateManifest',CASE WHEN m.task_id IS NULL THEN NULL ELSE jsonb_build_object('token',m.fencing_token,'bytes',m.byte_count,'chunks',m.chunk_count,'rows',m.row_count,'sha256',m.sha256) END) AS row_data
 FROM page_rows p JOIN adtr.tasks raw ON raw.task_id=p.task_id
 LEFT JOIN adtr.audit_export_snapshots s ON s.task_id=p.task_id
 LEFT JOIN adtr.audit_export_manifests m ON m.task_id=p.task_id AND m.fencing_token::text=raw.result->>'artifactToken'
 ) SELECT (SELECT count(*) FROM selected),COALESCE((SELECT jsonb_agg(row_data ORDER BY `+order+`) FROM enriched),'[]'::jsonb)`, len(args)-1, len(args))
	var raw []byte
	var total int
	if err = tx.QueryRow(ctx, query, args...).Scan(&total, &raw); err != nil {
		return out, err
	}
	if f.PageSize == -1 && total > 1000 {
		return out, problem(422, "result_too_large")
	}
	var records []historyRecord
	if err = json.Unmarshal(raw, &records); err != nil {
		return out, err
	}
	for _, record := range records {
		out.List = append(out.List, projectHistoryRecord(record))
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
