package domains

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type ComputerAssetExportV3Artifact struct {
	RowCount  int    `json:"rowCount"`
	ByteCount int64  `json:"byteCount"`
	SHA256    string `json:"sha256"`
	Filename  string `json:"filename"`
}
type ComputerAssetExportV3Detail struct {
	TaskID         string                         `json:"taskID"`
	Task           tasks.Task                     `json:"task"`
	Request        ComputerAssetExportV3Payload   `json:"request"`
	DownloadReady  bool                           `json:"downloadReady"`
	DownloadStatus string                         `json:"downloadStatus"`
	Artifact       *ComputerAssetExportV3Artifact `json:"artifact,omitempty"`
}

func ComputerAssetExportV3Filename(id string) string { return "computer-assets-v3-" + id + ".xlsx" }

func (s *Store) SubmitComputerAssetExportV3Tx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, principal tasks.Principal, payload ComputerAssetExportV3Payload, key string) (tasks.Submission, error) {
	raw, _ := json.Marshal(payload)
	raw, err := ValidateComputerAssetExportV3Payload(raw)
	if err != nil {
		return tasks.Submission{}, err
	}
	if json.Unmarshal(raw, &payload) != nil {
		return tasks.Submission{}, problem(400, "invalid_input")
	}
	// Current authentication/proof is already checked. An original receipt remains
	// recoverable even when its immutable source is no longer eligible.
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND kind=$3 AND idempotency_key=$4)`, principal.TenantID, payload.DomainID, ComputerAssetExportV3KindName, key).Scan(&exists)
	if err != nil {
		return tasks.Submission{}, err
	}
	if !exists {
		if _, _, err = s.computerAssetExportSourceTx(ctx, tx, principal.TenantID, payload); err != nil {
			return tasks.Submission{}, err
		}
	}
	return engine.SubmitTx(ctx, tx, principal, tasks.SubmitInput{TaskName: ComputerAssetExportV3KindName, DomainID: payload.DomainID, PayloadVersion: 1, Payload: raw, IdempotencyKey: key})
}

func (s *Store) ComputerAssetExportV3ReceiptTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, domain, key string) (tasks.Task, ComputerAssetExportV3Payload, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT task_id FROM adtr.tasks WHERE tenant_id=$1 AND actor_id=$2 AND domain_id=$3 AND kind=$4 AND idempotency_key=$5`, p.TenantID, p.ActorID, domain, ComputerAssetExportV3KindName, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = problem(404, "not_found")
	}
	if err != nil {
		return tasks.Task{}, ComputerAssetExportV3Payload{}, err
	}
	return s.ComputerAssetExportV3TaskTx(ctx, tx, engine, p, id)
}
func (s *Store) ComputerAssetExportV3TaskTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, id string) (tasks.Task, ComputerAssetExportV3Payload, error) {
	d, err := engine.DetailTx(ctx, tx, p, id)
	if err != nil {
		return tasks.Task{}, ComputerAssetExportV3Payload{}, err
	}
	if d.Task.Kind != ComputerAssetExportV3KindName || d.Task.ActorID != p.ActorID || d.Task.TenantID != p.TenantID || d.Task.Archived {
		return tasks.Task{}, ComputerAssetExportV3Payload{}, problem(404, "not_found")
	}
	raw, err := ValidateComputerAssetExportV3Payload(d.Task.Payload)
	var request ComputerAssetExportV3Payload
	if err != nil || json.Unmarshal(raw, &request) != nil || request.DomainID != d.Task.DomainID {
		return tasks.Task{}, request, problem(500, "artifact_invalid")
	}
	return d.Task, request, nil
}

func (s *Store) ComputerAssetExportV3DetailTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, id string) (ComputerAssetExportV3Detail, error) {
	t, request, err := s.ComputerAssetExportV3TaskTx(ctx, tx, engine, p, id)
	if err != nil {
		return ComputerAssetExportV3Detail{}, err
	}
	out := ComputerAssetExportV3Detail{TaskID: t.ID, Task: t, Request: request, DownloadStatus: "pending"}
	switch t.State {
	case tasks.Cancelled:
		out.DownloadStatus = "cancelled"
	case tasks.Failed, tasks.PartialFailed, tasks.DeadLetter:
		out.DownloadStatus = "failed"
	case tasks.Succeeded:
		result, _, e := s.computerAssetExportArtifactTx(ctx, tx, t, request, t.Result, false)
		if e != nil {
			var de *Error
			if errors.As(e, &de) {
				switch de.Code {
				case "selection_changed":
					out.DownloadStatus = "source_changed"
				case "directory_observation_unavailable", "not_found":
					out.DownloadStatus = "source_unavailable"
				case "artifact_invalid":
					out.DownloadStatus = "artifact_invalid"
				default:
					return out, e
				}
				return out, nil
			}
			return out, e
		}
		out.DownloadReady = true
		out.DownloadStatus = "ready"
		out.Artifact = &ComputerAssetExportV3Artifact{result.RowCount, result.ByteCount, result.SHA256, ComputerAssetExportV3Filename(t.ID)}
	}
	return out, nil
}

func (s *Store) ReadComputerAssetExportV3Tx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, id string) ([]byte, ComputerAssetExportV3Artifact, error) {
	t, request, err := s.ComputerAssetExportV3TaskTx(ctx, tx, engine, p, id)
	if err != nil {
		return nil, ComputerAssetExportV3Artifact{}, err
	}
	if t.State != tasks.Succeeded {
		return nil, ComputerAssetExportV3Artifact{}, problem(409, "export_not_ready")
	}
	result, data, err := s.computerAssetExportArtifactTx(ctx, tx, t, request, t.Result, true)
	if err != nil {
		return nil, ComputerAssetExportV3Artifact{}, err
	}
	return data, ComputerAssetExportV3Artifact{result.RowCount, result.ByteCount, result.SHA256, ComputerAssetExportV3Filename(t.ID)}, nil
}

func (s *Store) computerAssetExportArtifactTx(ctx context.Context, tx pgx.Tx, t tasks.Task, p ComputerAssetExportV3Payload, raw json.RawMessage, read bool) (computerAssetExportResult, []byte, error) {
	invalid := func() (computerAssetExportResult, []byte, error) {
		return computerAssetExportResult{}, nil, problem(500, "artifact_invalid")
	}
	result, err := decodeComputerAssetExportResult(raw)
	if err != nil {
		return invalid()
	}
	if result.ArtifactToken != t.FencingToken {
		return invalid()
	}
	rows, digest, err := s.computerAssetExportSourceTx(ctx, tx, t.TenantID, p)
	if err != nil {
		return result, nil, err
	}
	var matches bool
	var chunks int
	err = tx.QueryRow(ctx, `SELECT s.tenant_id=$2 AND s.actor_id=$3 AND s.authorization_version=$4 AND s.observation_id=$5 AND s.source_sha256=$6 AND s.payload_hash=$7 AND s.row_count=$8 AND m.row_count=s.row_count AND m.byte_count=$9 AND m.sha256=$10,m.chunk_count FROM adtr.computer_asset_export_snapshots s JOIN adtr.computer_asset_export_manifests m ON m.task_id=s.task_id AND m.fencing_token=$11 WHERE s.task_id=$1`, t.ID, t.TenantID, t.ActorID, t.AuthorizationVersion, p.ObservationID, digest, t.PayloadHash, result.RowCount, result.ByteCount, result.SHA256, result.ArtifactToken).Scan(&matches, &chunks)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid()
	}
	if err != nil {
		return result, nil, err
	}
	if !matches || len(rows) != result.RowCount || chunks < 1 || chunks > 32 || int64(chunks) != (result.ByteCount+1048575)/1048576 {
		return invalid()
	}
	if !read {
		return result, nil, nil
	}
	data := make([]byte, 0, int(result.ByteCount))
	valid := false
	defer func() {
		if !valid {
			clear(data)
		}
	}()
	rs, err := tx.Query(ctx, `SELECT chunk_no,data,sha256 FROM adtr.computer_asset_export_chunks WHERE task_id=$1 AND fencing_token=$2 ORDER BY chunk_no`, t.ID, result.ArtifactToken)
	if err != nil {
		return result, nil, err
	}
	defer rs.Close()
	n := 0
	for rs.Next() {
		var index int
		var part []byte
		var hash string
		if err = rs.Scan(&index, &part, &hash); err != nil {
			return result, nil, err
		}
		sum := sha256.Sum256(part)
		expected := 1048576
		if n == chunks-1 {
			expected = int(result.ByteCount) - n*1048576
		}
		if index != n || len(part) != expected || len(data)+len(part) > int(result.ByteCount) || hash != hex.EncodeToString(sum[:]) {
			clear(part)
			return invalid()
		}
		data = append(data, part...)
		clear(part)
		n++
	}
	if err = rs.Err(); err != nil {
		return result, nil, err
	}
	sum := sha256.Sum256(data)
	if n != chunks || int64(len(data)) != result.ByteCount || hex.EncodeToString(sum[:]) != result.SHA256 {
		return invalid()
	}
	valid = true
	return result, data, nil
}

func (s *Store) validateComputerAssetExportSuccess(ctx context.Context, tx pgx.Tx, t tasks.Task, raw json.RawMessage) (string, error) {
	var p ComputerAssetExportV3Payload
	normalized, err := ValidateComputerAssetExportV3Payload(t.Payload)
	if err != nil || json.Unmarshal(normalized, &p) != nil || p.DomainID != t.DomainID {
		return "invalid_input", nil
	}
	_, data, err := s.computerAssetExportArtifactTx(ctx, tx, t, p, raw, true)
	clear(data)
	if err == nil {
		return "", nil
	}
	var de *Error
	if errors.As(err, &de) {
		switch de.Code {
		case "selection_changed", "directory_observation_unavailable", "not_found":
			return "export_source_changed", nil
		case "artifact_invalid":
			return "artifact_invalid", nil
		}
	}
	return "", err
}
