package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/directorychanges"
)

const directoryCLIContract = "adtr.synthetic.directorychanges.cli.v1"
const maxDirectoryInvocations = 64 // Includes exact replays, unlike MaxBatches.

// directoryShape enforces exact key spelling and presence before typed decoding.
// In particular, omitted known false/empty values must not become zero values.
func directoryShape(raw json.RawMessage, keys ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := decode(raw, &object); err != nil || object == nil || len(object) != len(keys) {
		return nil, errInput
	}
	for _, key := range keys {
		value, ok := object[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errInput
		}
	}
	return object, nil
}

func directoryFields(raw json.RawMessage) error {
	_, err := directoryShape(raw, "Name", "Enabled")
	return err
}

func directoryBaseline(raw json.RawMessage) (directorychanges.Baseline, error) {
	var b directorychanges.Baseline
	object, err := directoryShape(raw, "SourceEpoch", "Complete", "Objects")
	if err != nil {
		return b, err
	}
	var objects []json.RawMessage
	if err := decode(object["Objects"], &objects); err != nil {
		return b, err
	}
	if len(objects) > directorychanges.MaxObjects {
		return b, errInput
	}
	for _, raw := range objects {
		o, err := directoryShape(raw, "ID", "Kind", "Version", "Fields", "Deleted")
		if err != nil {
			return b, err
		}
		if err := directoryFields(o["Fields"]); err != nil {
			return b, err
		}
	}
	err = decode(raw, &b)
	return b, err
}

func directoryBatch(raw json.RawMessage, hashOnly bool) (directorychanges.Batch, error) {
	var b directorychanges.Batch
	keys := []string{"SourceEpoch", "Sequence", "PreviousHash", "Complete", "Changes"}
	if !hashOnly {
		keys = append(keys, "BatchHash")
	}
	object, err := directoryShape(raw, keys...)
	if err != nil {
		return b, err
	}
	var changes []json.RawMessage
	if err := decode(object["Changes"], &changes); err != nil {
		return b, err
	}
	if len(changes) > directorychanges.MaxChanges {
		return b, errInput
	}
	for _, raw := range changes {
		c, err := directoryShape(raw, "ID", "Operation", "Kind", "ExpectedVersion", "Fields")
		if err != nil {
			return b, err
		}
		if err := directoryFields(c["Fields"]); err != nil {
			return b, err
		}
	}
	err = decode(raw, &b)
	return b, err
}

type directoryTrace struct {
	Invocation int                     `json:"invocation"`
	Sequence   uint64                  `json:"sequence"`
	Status     string                  `json:"status"`
	NewRecords int                     `json:"newRecords"`
	Cursor     directorychanges.Cursor `json:"cursor"`
}

func directoryChanges(data []byte) (any, error) {
	// encoding/json replaces malformed UTF-8; do not silently change synthetic names.
	if !utf8.Valid(data) {
		return nil, errInput
	}
	var header struct {
		Contract  string
		Synthetic bool
		Mode      string
		Baseline  json.RawMessage
		Batches   json.RawMessage
		Batch     json.RawMessage
	}
	if err := decode(data, &header); err != nil {
		return nil, err
	}
	if !directorySurrogates(data) {
		return nil, errInput
	}
	if header.Contract != directoryCLIContract || !header.Synthetic {
		return nil, errInput
	}
	keys := []string{"contract", "synthetic", "mode"}
	switch header.Mode {
	case "hash":
		keys = append(keys, "batch")
	case "replay":
		keys = append(keys, "baseline", "batches")
	default:
		return nil, errInput
	}
	if _, err := directoryShape(data, keys...); err != nil {
		return nil, err
	}
	if header.Mode == "hash" {
		b, err := directoryBatch(header.Batch, true)
		if err != nil {
			return nil, err
		}
		b.BatchHash, err = directorychanges.HashBatch(b)
		if err != nil {
			return nil, err
		}
		return struct {
			Contract       string                 `json:"contract"`
			Synthetic      bool                   `json:"synthetic"`
			Mode           string                 `json:"mode"`
			StateValidated bool                   `json:"stateValidated"`
			Batch          directorychanges.Batch `json:"batch"`
		}{directoryCLIContract, true, "hash", false, b}, nil
	}
	baseline, err := directoryBaseline(header.Baseline)
	if err != nil {
		return nil, err
	}
	var batches []json.RawMessage
	if err := decode(header.Batches, &batches); err != nil {
		return nil, err
	}
	if len(batches) > maxDirectoryInvocations {
		return nil, errInput
	}
	state, err := directorychanges.New(baseline)
	if err != nil {
		return nil, err
	}
	trace := make([]directoryTrace, 0, len(batches))
	for i, raw := range batches {
		batch, err := directoryBatch(raw, false)
		if err != nil {
			return nil, err
		}
		previous := state.Snapshot().Cursor
		next, records, err := state.Apply(batch)
		if err != nil {
			return nil, err
		}
		state = next
		cursor := state.Snapshot().Cursor
		status := "applied"
		if cursor == previous {
			status = "replayed"
		}
		trace = append(trace, directoryTrace{i + 1, batch.Sequence, status, len(records), cursor})
	}
	return struct {
		Contract  string                    `json:"contract"`
		Synthetic bool                      `json:"synthetic"`
		Mode      string                    `json:"mode"`
		Snapshot  directorychanges.Snapshot `json:"snapshot"`
		Trace     []directoryTrace          `json:"trace"`
	}{directoryCLIContract, true, "replay", state.Snapshot(), trace}, nil
}

// directorySurrogates runs only after decode has established JSON syntax. JSON's
// \u escapes can encode invalid UTF-16 even when the input bytes are valid UTF-8;
// encoding/json otherwise replaces those silently. Escaped backslashes are text.
func directorySurrogates(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++ // Validated JSON guarantees an escaped byte follows.
		if data[i] != 'u' {
			continue
		}
		unit, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
