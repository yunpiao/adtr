package domains

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/groupassetxlsx"
	"github.com/yunpiao/adtr/internal/tasks"
)

func (s *Store) GroupAssetExportV3Kind() tasks.Kind {
	return tasks.Kind{Name: GroupAssetExportV3KindName, Version: 1, MaxAttempts: 3, Lease: 30 * time.Second, Heartbeat: 5 * time.Second, Timeout: 5 * time.Minute, RetryBase: 5 * time.Second, RetryCap: time.Minute, RetryCodes: []string{"database_unavailable"}, ReplaySafe: true, CancelDiscardsResult: true, OwnerScoped: true, Validate: ValidateGroupAssetExportV3Payload, Execute: s.executeGroupAssetExportV3, ValidateSuccess: s.validateGroupAssetExportSuccess}
}

// The engine/API has established current durable actor/domain authority before
// calling this helper. The observation lock binds its validated body and digest.
func (s *Store) groupAssetExportSourceTx(ctx context.Context, tx pgx.Tx, tenant string, p GroupAssetExportV3Payload) ([]directoryassets.PublicObjectV3, string, error) {
	if _, err := s.userAssetsV2SelectionTx(ctx, tx, tenant, []string{p.DomainID}, p.DomainID, p.ExpectedRevision, p.ExpectedCredentialRevision); err != nil {
		return nil, "", err
	}
	var digest string
	err := tx.QueryRow(ctx, `SELECT sha256 FROM adtr.domain_directory_observations WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND dictionary_version=3 FOR SHARE`, tenant, p.DomainID, p.ObservationID).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", problem(409, "directory_observation_unavailable")
	}
	if err != nil {
		return nil, "", err
	}
	_, o, err := s.loadDirectoryV3ObservationTx(ctx, tx, tenant, p.DomainID, p.ObservationID)
	defer o.Discard()
	if err != nil {
		return nil, "", err
	}
	rows, _, err := groupAssetsV3Rows(ctx, o, p.Filter())
	if err != nil {
		return nil, "", err
	}
	if len(rows) > GroupAssetExportMaxRows {
		return nil, "", problem(422, "directory_limit_exceeded")
	}
	return rows, digest, nil
}

type groupAssetExportResult struct {
	ArtifactToken int64  `json:"artifactToken"`
	RowCount      int    `json:"rowCount"`
	ByteCount     int64  `json:"byteCount"`
	SHA256        string `json:"sha256"`
}

type groupAssetExportRun struct {
	store    *Store
	ctx      context.Context
	ex       tasks.Execution
	payload  GroupAssetExportV3Payload
	version  int64
	progress int
	rows     []directoryassets.PublicObjectV3
	digest   string
}

func (r *groupAssetExportRun) tx(fn func(context.Context, pgx.Tx) error) error {
	next, err := r.ex.WithTx(r.ctx, r.version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		rows, digest, e := r.store.groupAssetExportSourceTx(ctx, tx, r.ex.Task.TenantID, r.payload)
		if e != nil {
			return 0, nil, nil, e
		}
		if r.digest != "" && (digest != r.digest || len(rows) != len(r.rows)) {
			return 0, nil, nil, problem(409, "directory_observation_unavailable")
		}
		if e = fn(ctx, tx); e != nil {
			return 0, nil, nil, e
		}
		return r.progress, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
	})
	if err == nil {
		r.version = next
	}
	return err
}

func (s *Store) executeGroupAssetExportV3(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	if ex.WithTx == nil {
		return tasks.Outcome{State: tasks.Failed, Code: "fencing_required"}
	}
	raw, err := ValidateGroupAssetExportV3Payload(ex.Task.Payload)
	if err != nil {
		return groupAssetExportFailed(err)
	}
	run := &groupAssetExportRun{store: s, ctx: ctx, ex: ex, version: ex.Task.ResultVersion, progress: max(10, ex.Task.Progress)}
	if json.Unmarshal(raw, &run.payload) != nil || run.payload.DomainID != ex.Task.DomainID {
		return tasks.Outcome{State: tasks.Failed, Code: "invalid_input"}
	}
	defer func() { clear(run.rows) }()
	err = run.tx(func(ctx context.Context, tx pgx.Tx) error {
		var e error
		run.rows, run.digest, e = s.groupAssetExportSourceTx(ctx, tx, ex.Task.TenantID, run.payload)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO adtr.group_asset_export_snapshots(task_id,tenant_id,actor_id,authorization_version,observation_id,source_sha256,payload_hash,row_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, ex.Task.ID, ex.Task.TenantID, ex.Task.ActorID, ex.Task.AuthorizationVersion, run.payload.ObservationID, run.digest, ex.Task.PayloadHash, len(run.rows))
		if e != nil {
			return e
		}
		var matches bool
		e = tx.QueryRow(ctx, `SELECT tenant_id=$2 AND actor_id=$3 AND authorization_version=$4 AND observation_id=$5 AND source_sha256=$6 AND payload_hash=$7 AND row_count=$8 FROM adtr.group_asset_export_snapshots WHERE task_id=$1`, ex.Task.ID, ex.Task.TenantID, ex.Task.ActorID, ex.Task.AuthorizationVersion, run.payload.ObservationID, run.digest, ex.Task.PayloadHash, len(run.rows)).Scan(&matches)
		if e != nil {
			return e
		}
		if !matches {
			return problem(409, "artifact_conflict")
		}
		if _, e = tx.Exec(ctx, `DELETE FROM adtr.group_asset_export_chunks WHERE task_id=$1 AND fencing_token<>$2`, ex.Task.ID, ex.Task.FencingToken); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `DELETE FROM adtr.group_asset_export_manifests WHERE task_id=$1 AND fencing_token<>$2`, ex.Task.ID, ex.Task.FencingToken)
		return e
	})
	if err != nil {
		return groupAssetExportFailed(err)
	}
	writer := &groupAssetExportChunkWriter{run: run, digest: sha256.New(), buffer: make([]byte, 0, 1048576)}
	defer clear(writer.buffer)
	ordinal := 0
	next := func() ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ordinal == len(run.rows) {
			return nil, io.EOF
		}
		cells := groupAssetExportCells(run.rows[ordinal], run.payload.ScreenColumns)
		ordinal++
		run.progress = max(run.progress, min(90, 10+ordinal*80/max(1, len(run.rows))))
		return cells, nil
	}
	if err = groupassetxlsx.Write(ctx, writer, groupAssetExportHeaders(run.payload.ScreenColumns), next); err != nil {
		return groupAssetExportFailed(err)
	}
	if err = writer.flush(); err != nil {
		return groupAssetExportFailed(err)
	}
	result := groupAssetExportResult{ex.Task.FencingToken, len(run.rows), writer.total, hex.EncodeToString(writer.digest.Sum(nil))}
	run.progress = max(run.progress, 95)
	err = run.tx(func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO adtr.group_asset_export_manifests(task_id,fencing_token,byte_count,chunk_count,row_count,sha256) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, ex.Task.ID, ex.Task.FencingToken, writer.total, writer.chunks, len(run.rows), result.SHA256)
		if e != nil {
			return e
		}
		var matches bool
		e = tx.QueryRow(ctx, `SELECT byte_count=$3 AND chunk_count=$4 AND row_count=$5 AND sha256=$6 FROM adtr.group_asset_export_manifests WHERE task_id=$1 AND fencing_token=$2`, ex.Task.ID, ex.Task.FencingToken, writer.total, writer.chunks, len(run.rows), result.SHA256).Scan(&matches)
		if e != nil {
			return e
		}
		if !matches {
			return problem(409, "artifact_conflict")
		}
		return nil
	})
	if err != nil {
		return groupAssetExportFailed(err)
	}
	output, _ := json.Marshal(result)
	return tasks.Outcome{State: tasks.Succeeded, Result: output}
}

type groupAssetExportChunkWriter struct {
	run    *groupAssetExportRun
	digest hash.Hash
	buffer []byte
	total  int64
	chunks int
}

func (w *groupAssetExportChunkWriter) Write(p []byte) (int, error) {
	if int64(len(p))+w.total > GroupAssetExportMaxBytes {
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
func (w *groupAssetExportChunkWriter) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}
	sum := sha256.Sum256(w.buffer)
	digest := hex.EncodeToString(sum[:])
	err := w.run.tx(func(ctx context.Context, tx pgx.Tx) error {
		t := w.run.ex.Task
		_, e := tx.Exec(ctx, `INSERT INTO adtr.group_asset_export_chunks(task_id,fencing_token,chunk_no,data,sha256) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, t.ID, t.FencingToken, w.chunks, w.buffer, digest)
		if e != nil {
			return e
		}
		var same bool
		e = tx.QueryRow(ctx, `SELECT data=$4 AND sha256=$5 FROM adtr.group_asset_export_chunks WHERE task_id=$1 AND fencing_token=$2 AND chunk_no=$3`, t.ID, t.FencingToken, w.chunks, w.buffer, digest).Scan(&same)
		if e != nil {
			return e
		}
		if !same {
			return problem(409, "artifact_conflict")
		}
		return nil
	})
	if err == nil {
		w.chunks++
		clear(w.buffer)
		w.buffer = w.buffer[:0]
	}
	return err
}

func groupAssetExportFailed(err error) tasks.Outcome {
	out := tasks.Outcome{State: tasks.Failed, Code: "export_failed"}
	var de *Error
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, tasks.ErrCancelRequested):
		out.State = tasks.Cancelled
		out.Code = "cancelled"
	case errors.Is(err, tasks.ErrAuthorization):
		out.Code = "authorization_revoked"
	case errors.Is(err, tasks.ErrLeaseLost):
		out.Code = "lease_lost"
	case errors.Is(err, groupassetxlsx.ErrInvalidText):
		out.Code = "export_invalid_text"
	case errors.Is(err, groupassetxlsx.ErrCellTooLong):
		out.Code = "export_cell_too_long"
	case errors.Is(err, context.Canceled):
		out.Code = "worker_interrupted"
		out.Retryable = true
	case errors.Is(err, context.DeadlineExceeded):
		out.Code = "export_timeout"
	case errors.As(err, &de):
		out.Code = de.Code
	case errors.As(err, &pg) && (pg.Code == "40001" || pg.Code == "40P01" || pg.Code == "57P01" || pg.Code == "53300"):
		out.Code = "database_unavailable"
		out.Retryable = true
	}
	return out
}

func decodeGroupAssetExportResult(raw json.RawMessage) (groupAssetExportResult, error) {
	var r groupAssetExportResult
	if ValidateGroupAssetExportV3JSON(raw) != nil {
		return r, problem(500, "artifact_invalid")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 {
		return r, problem(500, "artifact_invalid")
	}
	for _, key := range []string{"artifactToken", "rowCount", "byteCount", "sha256"} {
		if _, ok := fields[key]; !ok {
			return r, problem(500, "artifact_invalid")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || r.ArtifactToken <= 0 || r.RowCount < 0 || r.RowCount > GroupAssetExportMaxRows || r.ByteCount < 1 || r.ByteCount > GroupAssetExportMaxBytes || len(r.SHA256) != 64 {
		return r, problem(500, "artifact_invalid")
	}
	decoded, e := hex.DecodeString(r.SHA256)
	if e != nil || hex.EncodeToString(decoded) != r.SHA256 {
		return r, problem(500, "artifact_invalid")
	}
	return r, nil
}
