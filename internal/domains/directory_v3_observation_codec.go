package domains

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

const (
	directoryObservationV3Prefix         = `{"dictionaryVersion":3,"objects":[`
	directoryObservationV3Source         = `],"source":`
	directoryObservationV3MaxRows        = 10000
	directoryObservationV3MaxSourceBytes = 8192
)

// encodeDirectoryObservationV3 is a non-wired, local storage codec. Its fixed
// dictionary-3 envelope retains binary supplemental values as base64; it does
// not publish an observation or authorize any use of directory credentials.
// It never modifies or takes ownership of the caller's observation.
func encodeDirectoryObservationV3(observation ldapconnection.DirectoryV3Observation) ([]byte, error) {
	bad := func() ([]byte, error) { return nil, problem(422, "invalid_directory_observation") }
	if observation.Objects == nil || len(observation.Objects) > directoryObservationV3MaxRows {
		return bad()
	}
	source, err := encodeDirectorySourceV2(observation.Source)
	if err != nil {
		return bad()
	}
	defer clear(source)
	used := len(directoryObservationV3Prefix) + len(directoryObservationV3Source) + len(source) + 1
	pieces := make([]json.RawMessage, 0, len(observation.Objects))
	defer func() { clearDirectoryObservationV3Pieces(pieces) }()
	seen := make(map[string]bool, len(observation.Objects))
	for _, object := range observation.Objects {
		if seen[object.Base.GUID] || !ldapconnection.DirectoryDNInDomain(object.Base.DN, observation.Source.Domain) {
			return bad()
		}
		piece, err := directoryassets.EncodeStoredObjectV3(object)
		if err != nil {
			clear(piece)
			return bad()
		}
		extra := len(piece)
		if len(pieces) > 0 {
			extra++
		}
		if len(piece) > directoryassets.MaxStoredObjectV3Bytes || extra > directoryObservationMaxBytes-used {
			clear(piece)
			return bad()
		}
		used += extra
		pieces = append(pieces, piece)
		seen[object.Base.GUID] = true
	}
	// Allocate once, after the complete envelope budget is known. Temporary
	// object/source encodings are cleared on both success and failure.
	raw := make([]byte, 0, used)
	raw = append(raw, directoryObservationV3Prefix...)
	for i, piece := range pieces {
		if i > 0 {
			raw = append(raw, ',')
		}
		raw = append(raw, piece...)
	}
	raw = append(raw, directoryObservationV3Source...)
	raw = append(raw, source...)
	raw = append(raw, '}')
	return raw, nil
}

// decodeDirectoryObservationV3 accepts only the exact bounded canonical body,
// digest, count and dictionary version. Success transfers independently owned
// objects to the caller, which must call Discard. Failure returns a zero result
// and clears all owned raw values; the supplied raw body is never modified.
func decodeDirectoryObservationV3(raw []byte, digest string, count int) (out ldapconnection.DirectoryV3Observation, returnedErr error) {
	bad := func() error { return problem(409, "directory_observation_unavailable") }
	defer func() {
		if returnedErr != nil {
			out.Discard()
		}
	}()
	if len(raw) == 0 || len(raw) > directoryObservationMaxBytes || count < 0 || count > directoryObservationV3MaxRows || len(digest) != 64 {
		return out, bad()
	}
	if !preflightDirectoryObservationV3(raw, count) {
		return out, bad()
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return out, bad()
	}
	fields, err := directoryObservationV3Fields(raw)
	defer clearDirectoryObservationV3Pieces(fields)
	if err != nil || !bytes.Equal(fields[0], []byte("3")) || len(fields[2]) > directoryObservationV3MaxSourceBytes {
		return out, bad()
	}
	if json.Unmarshal(fields[2], &out.Source) != nil {
		return out, bad()
	}
	source, err := encodeDirectorySourceV2(out.Source)
	defer clear(source)
	if err != nil || !bytes.Equal(source, fields[2]) {
		return out, bad()
	}
	reader := json.NewDecoder(bytes.NewReader(fields[1]))
	token, err := reader.Token()
	if err != nil || token != json.Delim('[') {
		return out, bad()
	}
	out.Objects = make([]directoryassets.StoredObjectV3, 0, count)
	for reader.More() {
		if len(out.Objects) >= count {
			return out, bad()
		}
		var piece json.RawMessage
		if err := reader.Decode(&piece); err != nil || len(piece) > directoryassets.MaxStoredObjectV3Bytes {
			clear(piece)
			return out, bad()
		}
		object, err := directoryassets.DecodeStoredObjectV3(piece)
		clear(piece)
		if err != nil {
			return out, bad()
		}
		out.Objects = append(out.Objects, object)
	}
	if token, err = reader.Token(); err != nil || token != json.Delim(']') || len(out.Objects) != count {
		return out, bad()
	}
	if _, err = reader.Token(); err != io.EOF {
		return out, bad()
	}
	canonical, err := encodeDirectoryObservationV3(out)
	defer clear(canonical)
	if err != nil || !bytes.Equal(canonical, raw) {
		return out, bad()
	}
	return out, nil
}

// Copy only raw JSON here; source and object bounds precede typed or base64
// decoding. Reject duplicate/unknown/missing keys instead of accepting JSON's
// last-key-wins behavior. All returned pieces are owned, never aliases of raw.
func directoryObservationV3Fields(raw []byte) (fields []json.RawMessage, returnedErr error) {
	bad := func() error { return problem(409, "directory_observation_unavailable") }
	reader := json.NewDecoder(bytes.NewReader(raw))
	token, err := reader.Token()
	if err != nil || token != json.Delim('{') {
		return nil, bad()
	}
	fields = make([]json.RawMessage, 3)
	defer func() {
		if returnedErr != nil {
			clearDirectoryObservationV3Pieces(fields)
			fields = nil
		}
	}()
	for reader.More() {
		token, err := reader.Token()
		if err != nil {
			return fields, bad()
		}
		index := -1
		switch token {
		case "dictionaryVersion":
			index = 0
		case "objects":
			index = 1
		case "source":
			index = 2
		}
		if index < 0 || fields[index] != nil {
			return fields, bad()
		}
		if reader.Decode(&fields[index]) != nil {
			return fields, bad()
		}
	}
	if token, err = reader.Token(); err != nil || token != json.Delim('}') {
		return fields, bad()
	}
	if _, err = reader.Token(); err != io.EOF {
		return fields, bad()
	}
	for _, field := range fields {
		if field == nil {
			return fields, bad()
		}
	}
	return fields, nil
}

func clearDirectoryObservationV3Pieces(pieces []json.RawMessage) {
	for _, piece := range pieces {
		clear(piece)
	}
	clear(pieces)
}

// Inspect the canonical envelope and object spans without allocating. This runs
// before any JSON decoder, so an oversized object cannot first allocate the
// entire observation budget. The strict JSON codecs still validate all syntax.
func preflightDirectoryObservationV3(raw []byte, count int) bool {
	if !bytes.HasPrefix(raw, []byte(directoryObservationV3Prefix)) {
		return false
	}
	pos := len(directoryObservationV3Prefix)
	for i := 0; i < count; i++ {
		if i > 0 {
			if pos >= len(raw) || raw[pos] != ',' {
				return false
			}
			pos++
		}
		n := directoryV3CompositeSpan(raw[pos:], directoryassets.MaxStoredObjectV3Bytes)
		if n == 0 {
			return false
		}
		pos += n
	}
	if !bytes.HasPrefix(raw[pos:], []byte(directoryObservationV3Source)) {
		return false
	}
	pos += len(directoryObservationV3Source)
	n := directoryV3CompositeSpan(raw[pos:], directoryObservationV3MaxSourceBytes)
	return n > 0 && pos+n+1 == len(raw) && raw[pos+n] == '}'
}
func directoryV3CompositeSpan(raw []byte, limit int) int {
	if len(raw) == 0 || raw[0] != '{' {
		return 0
	}
	depth := 0
	quoted := false
	escaped := false
	for i, c := range raw {
		if i >= limit {
			return 0
		}
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 64 {
				return 0
			}
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
			if depth < 0 {
				return 0
			}
		}
	}
	return 0
}
