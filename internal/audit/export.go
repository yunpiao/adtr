package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/auditxlsx"
	"github.com/yunpiao/adtr/internal/tasks"
)

type ExportPayload struct {
	StartTm      string   `json:"startTm,omitempty"`
	EndTm        string   `json:"endTm,omitempty"`
	Keyword      string   `json:"keyword,omitempty"`
	FilterEvent  []string `json:"filterEvent"`
	CreateSort   int      `json:"createSort"`
	LogTypeList  []int    `json:"logTypeList"`
	SelectColumn []string `json:"selectColumn"`
}

func (p ExportPayload) Filter() Filter {
	return Filter{StartTm: p.StartTm, EndTm: p.EndTm, Keyword: p.Keyword, FilterEvent: p.FilterEvent, CreateSort: p.CreateSort, LogTypeList: p.LogTypeList, Visibility: "visible"}
}
func ValidateExportPayload(raw json.RawMessage) (json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, problem(400, "invalid_input")
	}
	fields := map[string]json.RawMessage{}
	object := json.NewDecoder(bytes.NewReader(raw))
	token, err := object.Token()
	if err != nil || token != json.Delim('{') {
		return nil, problem(400, "invalid_input")
	}
	for object.More() {
		token, err = object.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, problem(400, "invalid_input")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, problem(400, "invalid_input")
		}
		var value json.RawMessage
		if object.Decode(&value) != nil {
			return nil, problem(400, "invalid_input")
		}
		fields[key] = value
	}
	if token, err = object.Token(); err != nil || token != json.Delim('}') {
		return nil, problem(400, "invalid_input")
	}
	if _, err = object.Token(); err != io.EOF {
		return nil, problem(400, "invalid_input")
	}
	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, problem(400, "invalid_input")
		}
		if key == "createSort" && string(value) != "1" && string(value) != "-1" {
			return nil, problem(400, "invalid_input")
		}
	}
	var p ExportPayload
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return nil, problem(400, "invalid_input")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return nil, problem(400, "invalid_input")
	}
	f, e := NormalizeFilter(p.Filter())
	if e != nil {
		return nil, e
	}
	if e = ValidateColumns(p.SelectColumn); e != nil {
		return nil, e
	}
	p.StartTm = f.StartTm
	p.EndTm = f.EndTm
	p.CreateSort = f.CreateSort
	p.FilterEvent = f.FilterEvent
	p.LogTypeList = f.LogTypeList
	return json.Marshal(p)
}
func Kind() tasks.Kind {
	return tasks.Kind{Name: ExportKind, Version: 1, Platform: true, MaxAttempts: 3, Lease: 30 * time.Second, Heartbeat: 5 * time.Second, Timeout: 30 * time.Minute, RetryBase: 5 * time.Second, RetryCap: time.Minute, RetryCodes: []string{"database_unavailable"}, ReplaySafe: true, CancelDiscardsResult: true, OwnerScoped: true, Validate: ValidateExportPayload, Execute: execute}
}

type snapshot struct {
	Rows               int
	CapturedAt         time.Time
	VisibilityRevision int64
}
type exportResult struct {
	ArtifactToken int64     `json:"artifactToken"`
	RowCount      int       `json:"rowCount"`
	SnapshotAt    time.Time `json:"snapshotAt"`
	SHA256        string    `json:"sha256"`
	ByteCount     int64     `json:"byteCount"`
}
type exportRun struct {
	ctx      context.Context
	ex       tasks.Execution
	version  int64
	progress int
	snapshot snapshot
	payload  ExportPayload
}

func (r *exportRun) tx(fn func(context.Context, pgx.Tx) error) error {
	v, e := r.ex.WithTx(r.ctx, r.version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		if e := authorizeSnapshotScope(ctx, tx, r.ex.Task.TenantID, r.ex.Task.ActorID, r.ex.Task.ID); e != nil {
			return 0, nil, nil, e
		}
		if e := fn(ctx, tx); e != nil {
			return 0, nil, nil, e
		}
		if e := authorizeSnapshotScope(ctx, tx, r.ex.Task.TenantID, r.ex.Task.ActorID, r.ex.Task.ID); e != nil {
			return 0, nil, nil, e
		}
		return r.progress, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
	})
	if e == nil {
		r.version = v
	}
	return e
}
func execute(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	if ex.WithTx == nil {
		return tasks.Outcome{State: tasks.Failed, Code: "fencing_required"}
	}
	raw, err := ValidateExportPayload(ex.Task.Payload)
	if err != nil {
		return failed(err)
	}
	run := &exportRun{ctx: ctx, ex: ex, version: ex.Task.ResultVersion, progress: max(10, ex.Task.Progress)}
	if json.Unmarshal(raw, &run.payload) != nil {
		return tasks.Outcome{State: tasks.Failed, Code: "invalid_input"}
	}
	if err = run.capture(); err != nil {
		return failed(err)
	}
	writer := &chunkWriter{run: run, digest: sha256.New(), buffer: make([]byte, 0, 1048576)}
	ordinal := 0
	batch := []Row{}
	pos := 0
	next := func() ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pos == len(batch) {
			if ordinal >= run.snapshot.Rows {
				return nil, io.EOF
			}
			batch = nil
			pos = 0
			err := run.tx(func(opCtx context.Context, tx pgx.Tx) error {
				rows, e := tx.Query(opCtx, `SELECT row_data FROM adtr.audit_export_rows WHERE task_id=$1 AND ordinal>$2 ORDER BY ordinal LIMIT 10000`, ex.Task.ID, ordinal)
				if e != nil {
					return e
				}
				defer rows.Close()
				for rows.Next() {
					var raw []byte
					var row Row
					if e = rows.Scan(&raw); e != nil {
						return e
					}
					if e = json.Unmarshal(raw, &row); e != nil {
						return e
					}
					batch = append(batch, row)
				}
				return rows.Err()
			})
			if err != nil {
				return nil, err
			}
			if len(batch) == 0 {
				return nil, problem(500, "snapshot_incomplete")
			}
		}
		cells := batch[pos].Cells(run.payload.SelectColumn)
		pos++
		ordinal++
		run.progress = max(run.progress, min(90, 10+ordinal*80/max(1, run.snapshot.Rows)))
		return cells, nil
	}
	if err = auditxlsx.Write(ctx, writer, Headers(run.payload.SelectColumn), next); err != nil {
		return failed(err)
	}
	if err = writer.flush(); err != nil {
		return failed(err)
	}
	result := exportResult{ex.Task.FencingToken, run.snapshot.Rows, run.snapshot.CapturedAt, hex.EncodeToString(writer.digest.Sum(nil)), writer.total}
	run.progress = max(run.progress, 95)
	err = run.tx(func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.audit_export_manifests(task_id,fencing_token,byte_count,chunk_count,row_count,sha256) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, ex.Task.ID, ex.Task.FencingToken, writer.total, writer.chunks, run.snapshot.Rows, result.SHA256)
		if e != nil {
			return e
		}
		var matches bool
		e = tx.QueryRow(ctx, `SELECT byte_count=$3 AND chunk_count=$4 AND row_count=$5 AND sha256=$6 FROM adtr.audit_export_manifests WHERE task_id=$1 AND fencing_token=$2`, ex.Task.ID, ex.Task.FencingToken, writer.total, writer.chunks, run.snapshot.Rows, result.SHA256).Scan(&matches)
		if e != nil {
			return e
		}
		if !matches {
			return problem(409, "artifact_conflict")
		}
		return nil
	})
	if err != nil {
		return failed(err)
	}
	output, _ := json.Marshal(result)
	return tasks.Outcome{State: tasks.Succeeded, Result: output}
}
func failed(err error) tasks.Outcome {
	out := tasks.Outcome{State: tasks.Failed, Code: "export_failed"}
	var ae *Error
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, tasks.ErrCancelRequested):
		out.State = tasks.Cancelled
		out.Code = "cancelled"
	case errors.Is(err, tasks.ErrAuthorization):
		out.Code = "authorization_revoked"
	case errors.Is(err, auditxlsx.ErrCellTooLong):
		out.Code = "export_cell_too_long"
	case errors.Is(err, auditxlsx.ErrInvalidText):
		out.Code = "export_invalid_text"
	case errors.Is(err, tasks.ErrLeaseLost):
		out.Code = "lease_lost"
	case errors.Is(err, context.Canceled):
		out.Code = "worker_interrupted"
		out.Retryable = true
	case errors.Is(err, context.DeadlineExceeded):
		out.Code = "export_timeout"
	case errors.As(err, &ae):
		out.Code = ae.Code
	case errors.As(err, &pg) && pg.ConstraintName == "audit_export_row_limit":
		out.Code = "result_too_large"
	case errors.As(err, &pg) && (pg.Code == "40001" || pg.Code == "40P01" || pg.Code == "57P01" || pg.Code == "53300"):
		out.Code = "database_unavailable"
		out.Retryable = true
	}
	return out
}
func (r *exportRun) capture() error {
	return r.tx(func(ctx context.Context, tx pgx.Tx) error {
		t := r.ex.Task
		var storedTenant, storedVersion string
		var storedActor int64
		var storedColumns []byte
		err := tx.QueryRow(ctx, `SELECT tenant_id,actor_id,authorization_version,columns,row_count,captured_at,visibility_revision FROM adtr.audit_export_snapshots WHERE task_id=$1`, t.ID).Scan(&storedTenant, &storedActor, &storedVersion, &storedColumns, &r.snapshot.Rows, &r.snapshot.CapturedAt, &r.snapshot.VisibilityRevision)
		cols, _ := json.Marshal(r.payload.SelectColumn)
		if err == nil {
			var selected []string
			if json.Unmarshal(storedColumns, &selected) != nil {
				return problem(500, "snapshot_invalid")
			}
			same, _ := json.Marshal(selected)
			r.snapshot.CapturedAt = r.snapshot.CapturedAt.UTC()
			if storedTenant != t.TenantID || storedActor != t.ActorID || storedVersion != t.AuthorizationVersion || !bytes.Equal(same, cols) {
				return problem(409, "snapshot_conflict")
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var role string
		err = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(role_id,''),role) FROM adtr.users WHERE tenant_id=$1 AND id=$2`, t.TenantID, t.ActorID).Scan(&role)
		if err != nil {
			return err
		}
		f, _ := NormalizeFilter(r.payload.Filter())
		sql, args := scopedQuery(Principal{t.TenantID, t.ActorID, role}, f)
		args = append(args, t.ID, t.ActorID, t.AuthorizationVersion, cols)
		n := len(args)
		query := `WITH selected AS MATERIALIZED (SELECT * FROM (` + sql + `) source_rows ORDER BY ` + order(f) + ` LIMIT 100001), frozen AS MATERIALIZED (SELECT *,row_number() OVER(ORDER BY ` + order(f) + `) ordinal FROM selected), snapshot AS (
 INSERT INTO adtr.audit_export_snapshots(task_id,tenant_id,actor_id,authorization_version,visibility_revision,row_count,columns)
 SELECT ` + fmt.Sprintf(`$%d,$1,$%d,$%d,COALESCE((SELECT revision FROM adtr.audit_visibility_revisions WHERE tenant_id=$1),0),(SELECT count(*) FROM frozen),$%d::jsonb`, n-3, n-2, n-1, n) + ` RETURNING task_id,row_count,captured_at,visibility_revision), rows AS (
 INSERT INTO adtr.audit_export_rows(task_id,ordinal,source,source_id,domain_id,row_data)
 SELECT snapshot.task_id,frozen.ordinal,frozen.source,frozen.source_id,frozen.domain_id,` + rowJSON + ` FROM frozen CROSS JOIN snapshot RETURNING ordinal)
 SELECT row_count,captured_at,visibility_revision,(SELECT count(*) FROM rows) FROM snapshot`
		var inserted int
		err = tx.QueryRow(ctx, query, args...).Scan(&r.snapshot.Rows, &r.snapshot.CapturedAt, &r.snapshot.VisibilityRevision, &inserted)
		if err != nil {
			return err
		}
		if inserted != r.snapshot.Rows {
			return problem(500, "snapshot_incomplete")
		}
		r.snapshot.CapturedAt = r.snapshot.CapturedAt.UTC()
		return nil
	})
}

type chunkWriter struct {
	run    *exportRun
	digest hash.Hash
	buffer []byte
	total  int64
	chunks int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if int64(len(p))+w.total > MaxArtifactBytes {
		return 0, problem(422, "artifact_too_large")
	}
	done := 0
	for len(p) > 0 {
		n := min(len(p), 1048576-len(w.buffer))
		w.buffer = append(w.buffer, p[:n]...)
		_, _ = w.digest.Write(p[:n])
		w.total += int64(n)
		done += n
		p = p[n:]
		if len(w.buffer) == 1048576 {
			if err := w.flush(); err != nil {
				return done, err
			}
		}
	}
	return done, nil
}
func (w *chunkWriter) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}
	digest := sha256.Sum256(w.buffer)
	hash := hex.EncodeToString(digest[:])
	err := w.run.tx(func(ctx context.Context, tx pgx.Tx) error {
		t := w.run.ex.Task
		_, e := tx.Exec(ctx, `INSERT INTO adtr.audit_export_chunks(task_id,fencing_token,chunk_no,data,sha256) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, t.ID, t.FencingToken, w.chunks, w.buffer, hash)
		if e != nil {
			return e
		}
		var matches bool
		e = tx.QueryRow(ctx, `SELECT data=$4 AND sha256=$5 FROM adtr.audit_export_chunks WHERE task_id=$1 AND fencing_token=$2 AND chunk_no=$3`, t.ID, t.FencingToken, w.chunks, w.buffer, hash).Scan(&matches)
		if e != nil {
			return e
		}
		if !matches {
			return problem(409, "artifact_conflict")
		}
		return nil
	})
	if err == nil {
		w.chunks++
		w.buffer = w.buffer[:0]
	}
	return err
}

type ExportDetail struct {
	Task          tasks.Task `json:"task"`
	DownloadReady bool       `json:"downloadReady"`
	RowCount      *int       `json:"rowCount,omitempty"`
	SnapshotAt    *time.Time `json:"snapshotAt,omitempty"`
	DownloadPath  string     `json:"downloadPath,omitempty"`
}

func DetailTx(ctx context.Context, tx pgx.Tx, p Principal, id string) (ExportDetail, error) {
	var out ExportDetail
	if err := requireSchema(ctx, tx); err != nil {
		return out, err
	}
	var epoch string
	err := tx.QueryRow(ctx, `SELECT authorization_version::text FROM adtr.users WHERE tenant_id=$1 AND id=$2`, p.TenantID, p.ActorID).Scan(&epoch)
	if err != nil {
		return out, err
	}
	t := &out.Task
	err = tx.QueryRow(ctx, `SELECT task_id,kind,domain_id,payload_version,state,error_code,created_at,updated_at,attempt,max_attempts,progress,result_version,result,cursor,parent_task_id,next_attempt_at,authorization_version FROM adtr.tasks t WHERE tenant_id=$1 AND actor_id=$2 AND task_id=$3 AND kind='audit.export' AND domain_id='platform' AND NOT EXISTS(SELECT FROM adtr.task_visibility v WHERE v.task_id=t.task_id AND v.tenant_id=t.tenant_id AND v.archived)`, p.TenantID, p.ActorID, id).Scan(&t.ID, &t.Kind, &t.DomainID, &t.PayloadVersion, &t.State, &t.Error, &t.CreatedAt, &t.UpdatedAt, &t.Attempt, &t.MaxAttempts, &t.Progress, &t.ResultVersion, &t.Result, &t.Cursor, &t.ParentID, &t.NextAttemptAt, &t.AuthorizationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, problem(404, "not_found")
	}
	if err != nil {
		return out, err
	}
	if epoch != t.AuthorizationVersion {
		return out, problem(403, "authorization_revoked")
	}
	if err = authorizeSnapshotScope(ctx, tx, p.TenantID, p.ActorID, id); err != nil {
		return out, err
	}
	t.SourceState = t.State.Source()
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	if t.State == tasks.Succeeded {
		result, valid := decodeArtifactResult(t.Result)
		if !valid {
			return out, problem(500, "artifact_invalid")
		}
		var snapshotJSON, manifestJSON []byte
		var current int64
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('matches',s.tenant_id=$3 AND s.actor_id=$4,'epoch',s.authorization_version,'rows',s.row_count,'capturedAt',s.captured_at,'visibility',s.visibility_revision),
        CASE WHEN m.task_id IS NULL THEN 'null'::jsonb ELSE jsonb_build_object('token',m.fencing_token,'bytes',m.byte_count,'chunks',m.chunk_count,'rows',m.row_count,'sha256',m.sha256) END,
        COALESCE((SELECT revision FROM adtr.audit_visibility_revisions WHERE tenant_id=$3),0)
        FROM adtr.audit_export_snapshots s LEFT JOIN adtr.audit_export_manifests m ON m.task_id=s.task_id AND m.fencing_token=$2
        WHERE s.task_id=$1 AND s.tenant_id=$3 AND s.actor_id=$4`, id, result.ArtifactToken, p.TenantID, p.ActorID).Scan(&snapshotJSON, &manifestJSON, &current)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, problem(500, "artifact_invalid")
		}
		if err != nil {
			return out, err
		}
		var snapshot *artifactSnapshotMetadata
		var manifest *artifactManifestMetadata
		if err = json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return out, err
		}
		if err = json.Unmarshal(manifestJSON, &manifest); err != nil {
			return out, err
		}
		status, result := artifactMetadataStatus(t.Result, t.AuthorizationVersion, snapshot, manifest, current)
		if status == "snapshot_changed" {
			return out, problem(409, "export_snapshot_changed")
		}
		if status != "eligible" {
			return out, problem(500, "artifact_invalid")
		}
		out.DownloadReady = true
		out.RowCount = &result.RowCount
		out.SnapshotAt = &result.SnapshotAt
		out.DownloadPath = "/api/audit/exports/download?taskUUID=" + id
	}
	return out, nil
}

// ReadArtifactTx validates every chunk and the final digest before a response is
// started. The hard cap bounds buffering and avoids partial successful downloads.
func ReadArtifactTx(ctx context.Context, tx pgx.Tx, p Principal, id string) ([]byte, error) {
	detail, err := DetailTx(ctx, tx, p, id)
	if err != nil {
		return nil, err
	}
	if detail.Task.State != tasks.Succeeded {
		return nil, problem(409, "export_not_ready")
	}
	var result exportResult
	if json.Unmarshal(detail.Task.Result, &result) != nil || result.ArtifactToken <= 0 {
		return nil, problem(500, "artifact_invalid")
	}
	var revision, current int64
	err = tx.QueryRow(ctx, `SELECT visibility_revision,COALESCE((SELECT revision FROM adtr.audit_visibility_revisions WHERE tenant_id=$2),0) FROM adtr.audit_export_snapshots WHERE task_id=$1`, id, p.TenantID).Scan(&revision, &current)
	if err != nil {
		return nil, err
	}
	if revision != current {
		return nil, problem(409, "export_snapshot_changed")
	}
	var total int64
	var chunks, rows int
	var digest string
	err = tx.QueryRow(ctx, `SELECT byte_count,chunk_count,row_count,sha256 FROM adtr.audit_export_manifests WHERE task_id=$1 AND fencing_token=$2`, id, result.ArtifactToken).Scan(&total, &chunks, &rows, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, problem(409, "export_not_ready")
	}
	if err != nil {
		return nil, err
	}
	if total < 1 || total > MaxArtifactBytes || total != result.ByteCount || rows != result.RowCount || digest != result.SHA256 {
		return nil, problem(500, "artifact_invalid")
	}
	data := make([]byte, 0, int(total))
	rs, err := tx.Query(ctx, `SELECT chunk_no,data,sha256 FROM adtr.audit_export_chunks WHERE task_id=$1 AND fencing_token=$2 ORDER BY chunk_no`, id, result.ArtifactToken)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	n := 0
	for rs.Next() {
		var index int
		var part []byte
		var hash string
		if err = rs.Scan(&index, &part, &hash); err != nil {
			return nil, err
		}
		if index != n || len(part) == 0 || len(data)+len(part) > int(total) {
			return nil, problem(500, "artifact_invalid")
		}
		sum := sha256.Sum256(part)
		if hash != hex.EncodeToString(sum[:]) {
			return nil, problem(500, "artifact_invalid")
		}
		data = append(data, part...)
		n++
	}
	if err = rs.Err(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != total || n != chunks || hex.EncodeToString(sum[:]) != digest {
		return nil, problem(500, "artifact_invalid")
	}
	return data, nil
}
func ArtifactFilename(id string) string { return "audit-" + id + ".xlsx" }

// Time-based domain eligibility can expire without an authorization epoch write.
// Validate the immutable snapshot provenance on every fenced read/write and download.
func authorizeSnapshotScope(ctx context.Context, tx pgx.Tx, tenant string, actor int64, taskID string) error {
	var allowed bool
	if err := tx.QueryRow(ctx, "SELECT "+snapshotScopeSQL("$3"), tenant, actor, taskID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return tasks.ErrAuthorization
	}
	return nil
}
