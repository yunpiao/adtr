package domains

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

const (
	directoryObservationV2Prefix         = `{"dictionaryVersion":2,"objects":[`
	directoryObservationV2Source         = `],"source":`
	directoryObservationV2MaxRows        = 10000
	directoryObservationV2MaxSourceBytes = 8192
)

// encodeDirectoryObservationV2 is a non-wired, local storage codec. Its fixed
// dictionary-2 envelope retains binary supplemental values as base64; it does
// not publish an observation or authorize any use of directory credentials.
// It never modifies or takes ownership of the caller's observation.
func encodeDirectoryObservationV2(observation ldapconnection.DirectoryV2Observation) ([]byte, error) {
	bad := func() ([]byte, error) { return nil, problem(422, "invalid_directory_observation") }
	if observation.Objects == nil || len(observation.Objects) > directoryObservationV2MaxRows {
		return bad()
	}
	source, err := encodeDirectorySourceV2(observation.Source)
	if err != nil {
		return bad()
	}
	defer clear(source)
	used := len(directoryObservationV2Prefix) + len(directoryObservationV2Source) + len(source) + 1
	pieces := make([]json.RawMessage, 0, len(observation.Objects))
	defer func() { clearDirectoryObservationV2Pieces(pieces) }()
	seen := make(map[string]bool, len(observation.Objects))
	for _, object := range observation.Objects {
		if seen[object.Base.GUID] || !ldapconnection.DirectoryDNInDomain(object.Base.DN, observation.Source.Domain) {
			return bad()
		}
		piece, err := directoryassets.EncodeStoredObjectV2(object)
		if err != nil {
			clear(piece)
			return bad()
		}
		extra := len(piece)
		if len(pieces) > 0 {
			extra++
		}
		if len(piece) > directoryassets.MaxStoredObjectV2Bytes || extra > directoryObservationMaxBytes-used {
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
	raw = append(raw, directoryObservationV2Prefix...)
	for i, piece := range pieces {
		if i > 0 {
			raw = append(raw, ',')
		}
		raw = append(raw, piece...)
	}
	raw = append(raw, directoryObservationV2Source...)
	raw = append(raw, source...)
	raw = append(raw, '}')
	return raw, nil
}

// Keep the existing source semantics, including canonical RootDSE host casing
// and a possible root dot. The old observation codec and bytes stay unchanged.
func encodeDirectorySourceV2(source ldapconnection.DirectorySource) ([]byte, error) {
	bad := func() ([]byte, error) { return nil, problem(422, "invalid_directory_observation") }
	// Bound raw inputs before DNS case folding or DN parsing can copy them.
	// Only the observed RootDSE host permits one additional DNS root dot.
	if len(source.ServerName) > 253 || len(source.Domain) > 253 || len(source.DCHostName) > 254 || len(source.NamingContext) > 4096 {
		return bad()
	}
	host, err := CanonicalDNS(source.DCHostName)
	if err != nil {
		return bad()
	}
	source.DCHostName = host
	if source.Pages < 1 || source.Pages > 100 || source.StartedAt.IsZero() || source.CompletedAt.Before(source.StartedAt) || source.CompletedAt.Sub(source.StartedAt) > 125*time.Second || source.ElapsedMilliseconds < 0 || source.ElapsedMilliseconds > 125000 {
		return bad()
	}
	for _, name := range []string{source.ServerName, source.DCHostName, source.Domain} {
		canonical, err := CanonicalDNS(name)
		if err != nil || canonical != name {
			return bad()
		}
	}
	if !ldapconnection.DirectoryNamingContextMatchesDomain(source.NamingContext, source.Domain) {
		return bad()
	}
	raw, err := json.Marshal(source)
	if err != nil || len(raw) > directoryObservationV2MaxSourceBytes {
		clear(raw)
		return bad()
	}
	return raw, nil
}

// decodeDirectoryObservationV2 accepts only the exact bounded canonical body,
// digest, count and dictionary version. Success transfers independently owned
// objects to the caller, which must call Discard. Failure returns a zero result
// and clears all owned raw values; the supplied raw body is never modified.
func decodeDirectoryObservationV2(raw []byte, digest string, count int) (out ldapconnection.DirectoryV2Observation, returnedErr error) {
	bad := func() error { return problem(409, "directory_observation_unavailable") }
	defer func() {
		if returnedErr != nil {
			out.Discard()
		}
	}()
	if len(raw) == 0 || len(raw) > directoryObservationMaxBytes || count < 0 || count > directoryObservationV2MaxRows || len(digest) != 64 {
		return out, bad()
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return out, bad()
	}
	fields, err := directoryObservationV2Fields(raw)
	defer clearDirectoryObservationV2Pieces(fields)
	if err != nil || !bytes.Equal(fields[0], []byte("2")) || len(fields[2]) > directoryObservationV2MaxSourceBytes {
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
	out.Objects = make([]directoryassets.StoredObjectV2, 0, count)
	for reader.More() {
		if len(out.Objects) >= count {
			return out, bad()
		}
		var piece json.RawMessage
		if err := reader.Decode(&piece); err != nil || len(piece) > directoryassets.MaxStoredObjectV2Bytes {
			clear(piece)
			return out, bad()
		}
		object, err := directoryassets.DecodeStoredObjectV2(piece)
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
	canonical, err := encodeDirectoryObservationV2(out)
	defer clear(canonical)
	if err != nil || !bytes.Equal(canonical, raw) {
		return out, bad()
	}
	return out, nil
}

// Copy only raw JSON here; source and object bounds precede typed or base64
// decoding. Reject duplicate/unknown/missing keys instead of accepting JSON's
// last-key-wins behavior. All returned pieces are owned, never aliases of raw.
func directoryObservationV2Fields(raw []byte) (fields []json.RawMessage, returnedErr error) {
	bad := func() error { return problem(409, "directory_observation_unavailable") }
	reader := json.NewDecoder(bytes.NewReader(raw))
	token, err := reader.Token()
	if err != nil || token != json.Delim('{') {
		return nil, bad()
	}
	fields = make([]json.RawMessage, 3)
	defer func() {
		if returnedErr != nil {
			clearDirectoryObservationV2Pieces(fields)
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

func clearDirectoryObservationV2Pieces(pieces []json.RawMessage) {
	for _, piece := range pieces {
		clear(piece)
	}
	clear(pieces)
}
