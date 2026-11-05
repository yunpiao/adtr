package attackpaths

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"time"
	"unicode/utf8"
)

// MaxJSONBytes bounds one complete offline request, including whitespace.
// It is independent of the graph's node, edge and label limits.
const MaxJSONBytes = 8 << 20

type jsonRequest struct {
	Snapshot Snapshot `json:"snapshot"`
	Query    Query    `json:"query"`
}

type jsonShape struct {
	kind   byte
	fields map[string]*jsonShape
	elem   *jsonShape
	limit  int
}

func object(fields map[string]*jsonShape) *jsonShape {
	return &jsonShape{kind: '{', fields: fields}
}
func array(elem *jsonShape, limit int) *jsonShape {
	return &jsonShape{kind: '[', elem: elem, limit: limit}
}

var requestShape = func() *jsonShape {
	text := &jsonShape{kind: 's'}
	stamp := &jsonShape{kind: 't'}
	scope := object(map[string]*jsonShape{"tenantId": text, "domainId": text})
	return object(map[string]*jsonShape{
		"snapshot": object(map[string]*jsonShape{
			"version": text, "simulated": {kind: 'b'}, "scope": scope, "observationId": text,
			"nodes": array(object(map[string]*jsonShape{"id": text, "name": text}), MaxNodes),
			"edges": array(object(map[string]*jsonShape{
				"id": text, "from": text, "to": text, "ruleId": text, "occurredAt": stamp,
			}), MaxEdges),
		}),
		"query": object(map[string]*jsonShape{
			"scope": scope, "from": text, "to": text, "ruleIds": array(text, MaxRules),
			"start": stamp, "end": stamp, "maxDepth": {kind: 'i'},
		}),
	})
}()

// EvaluateJSON evaluates one complete, explicitly synthetic offline request.
// Every field is required. Unknown, duplicate or case-aliased keys, null values,
// invalid Unicode and non-canonical UTC timestamps are rejected. Errors return
// a zero Result and never include input values. Scope remains a caller-supplied
// label, not authentication or authorization.
func EvaluateJSON(data []byte) (Result, error) {
	if len(data) > MaxJSONBytes {
		return Result{}, ErrLimit
	}
	if !utf8.Valid(data) || !validJSONEscapes(data) {
		return Result{}, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := checkJSON(d, requestShape); err != nil {
		return Result{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return Result{}, ErrInvalid
	}
	var request jsonRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return Result{}, ErrInvalid
	}
	g, err := Build(request.Snapshot)
	if err != nil {
		return Result{}, err
	}
	return g.Shortest(request.Query)
}

func checkJSON(d *json.Decoder, shape *jsonShape) error {
	token, err := d.Token()
	if err != nil || token == nil {
		return ErrInvalid
	}
	switch shape.kind {
	case '{':
		if token != json.Delim('{') {
			return ErrInvalid
		}
		seen := make(map[string]bool, len(shape.fields))
		for d.More() {
			token, err = d.Token()
			key, ok := token.(string)
			field, known := shape.fields[key]
			if err != nil || !ok || !known || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
			if err := checkJSON(d, field); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') || len(seen) != len(shape.fields) {
			return ErrInvalid
		}
	case '[':
		if token != json.Delim('[') {
			return ErrInvalid
		}
		count := 0
		for d.More() {
			count++
			if count > shape.limit {
				return ErrLimit
			}
			if err := checkJSON(d, shape.elem); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim(']') {
			return ErrInvalid
		}
	case 's', 't':
		s, ok := token.(string)
		if !ok {
			return ErrInvalid
		}
		if shape.kind == 't' {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil || !instant(t) || t.UTC().Format(time.RFC3339Nano) != s {
				return ErrInvalid
			}
		}
	case 'b':
		if _, ok := token.(bool); !ok {
			return ErrInvalid
		}
	case 'i':
		n, ok := token.(json.Number)
		if !ok {
			return ErrInvalid
		}
		if _, err := strconv.ParseInt(string(n), 10, 32); err != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// encoding/json replaces malformed surrogate escapes rather than rejecting them.
// This scanner rejects them before tokenization; JSON syntax is checked separately.
func validJSONEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		n, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
