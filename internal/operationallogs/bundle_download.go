package operationallogs

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// verifyBundleBytes validates the actual two-entry archive independently of
// metadata eligibility. Nothing is streamed to an HTTP response before this.
func verifyBundleBytes(ctx context.Context, data []byte, want bundleManifest) error {
	invalid := func() error { return problem(500, "artifact_invalid") }
	if len(data) == 0 || len(data) > MaxBundleBytes {
		return invalid()
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) != 2 {
		return invalid()
	}
	var parts [2][]byte
	for i, name := range []string{"manifest.json", "events.jsonl"} {
		if err = ctx.Err(); err != nil {
			return err
		}
		f := z.File[i]
		limit := MaxBundleBytes
		if i == 0 {
			limit = maxBundleRecordBytes
		}
		if f.Name != name || f.FileInfo().IsDir() || f.Method != zip.Store || f.UncompressedSize64 > uint64(limit) || f.CompressedSize64 != f.UncompressedSize64 {
			return invalid()
		}
		r, e := f.Open()
		if e != nil {
			return invalid()
		}
		parts[i], e = io.ReadAll(io.LimitReader(r, int64(limit)+1))
		closeErr := r.Close()
		if e != nil || closeErr != nil || len(parts[i]) > limit || uint64(len(parts[i])) != f.UncompressedSize64 {
			return invalid()
		}
	}
	m, ok := decodeBundleManifest(parts[0])
	if !ok {
		return invalid()
	}
	a, _ := json.Marshal(m)
	b, _ := json.Marshal(want)
	if !bytes.Equal(a, b) || !bytes.Equal(a, parts[0]) || bundleDigest(parts[1]) != m.JSONLSHA256 {
		return invalid()
	}
	if len(parts[1]) > 0 && parts[1][len(parts[1])-1] != '\n' {
		return invalid()
	}
	scanner := bufio.NewScanner(bytes.NewReader(parts[1]))
	scanner.Buffer(make([]byte, maxBundleRecordBytes+1), maxBundleRecordBytes+1)
	count := 0
	var previous Event
	for scanner.Scan() {
		if err = ctx.Err(); err != nil {
			return err
		}
		raw := scanner.Bytes()
		if !bundleObject(raw, "schemaVersion", "eventId", "processId", "module", "code", "outcome", "severity", "reason", "observedAt", "recordedAt") {
			return invalid()
		}
		var e Event
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&e) != nil || d.Decode(&struct{}{}) != io.EOF || validateEvent(e, true) != nil || !bundleEventSelected(e, m.Selection) || count > 0 && !bundleEventBefore(previous, e) {
			return invalid()
		}
		canonical, _ := json.Marshal(e)
		if !bytes.Equal(canonical, raw) {
			return invalid()
		}
		count++
		if count > m.RowCount {
			return invalid()
		}
		previous = e
	}
	if scanner.Err() != nil || count != m.RowCount {
		return invalid()
	}
	return nil
}

// ReadArtifactTx returns only a complete validated protected byte sequence.
// Snapshot metadata alone establishes eligibility, never successful download.
func ReadArtifactTx(ctx context.Context, tx pgx.Tx, p Principal, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	epoch, err := RequireReadTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	record, err := loadBundleRecord(ctx, tx, p, id, epoch)
	if err != nil {
		return nil, err
	}
	if record.Task.State != tasks.Succeeded {
		return nil, problem(409, "bundle_not_ready")
	}
	status, result, manifest := bundleMetadataStatus(record)
	if status != "eligible" {
		return nil, problem(500, "artifact_invalid")
	}
	data := make([]byte, 0, int(result.ByteCount))
	rows, err := tx.Query(ctx, `SELECT chunk_no,data,sha256 FROM adtr.operational_log_bundle_chunks WHERE task_id=$1 AND fencing_token=$2 ORDER BY chunk_no`, id, result.ArtifactToken)
	if err != nil {
		return nil, err
	}
	index := 0
	for rows.Next() {
		var n int
		var part []byte
		var digest string
		if err = rows.Scan(&n, &part, &digest); err != nil {
			rows.Close()
			return nil, err
		}
		if n != index || index >= record.Manifest.Chunks || len(part) == 0 || len(part) > bundleChunkBytes || index < record.Manifest.Chunks-1 && len(part) != bundleChunkBytes || int64(len(data)+len(part)) > result.ByteCount || bundleDigest(part) != digest {
			rows.Close()
			return nil, problem(500, "artifact_invalid")
		}
		data = append(data, part...)
		index++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if index != record.Manifest.Chunks || int64(len(data)) != result.ByteCount || bundleDigest(data) != result.SHA256 {
		return nil, problem(500, "artifact_invalid")
	}
	if err = verifyBundleBytes(ctx, data, manifest); err != nil {
		return nil, err
	}
	// The lock prevents policy writes; this final check also covers natural
	// password expiry while reading and validating bounded bytes.
	current, err := RequireReadTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if current != epoch {
		return nil, tasks.ErrAuthorization
	}
	return data, nil
}
