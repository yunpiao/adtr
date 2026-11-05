package learningconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// MaxJSONBytes bounds the complete input, including insignificant whitespace.
// This is an offline format limit, not a training or legacy capacity limit.
const MaxJSONBytes = 64 << 10

// Parse reads exactly one synthetic-learning-config-v1 snapshot. All eight keys
// are required and case-sensitive. Unknown/duplicate keys, null, coerced types,
// fractional/exponent durations, invalid UTF-8 and trailing values are rejected.
// The result always passes New; fixture labels do not grant authorization.
func Parse(data []byte) (Configuration, error) {
	invalid := func() (Configuration, error) {
		return Configuration{}, fmt.Errorf("%w: JSON snapshot", ErrInvalid)
	}
	if len(data) == 0 || len(data) > MaxJSONBytes || !utf8.Valid(data) {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return invalid()
	}
	var in Input
	seen := make(map[string]bool, 8)
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return invalid()
		}
		seen[key] = true
		switch key {
		case "format", "ruleLabel", "dataSourceLabel", "domainLabel":
			token, err = d.Token()
			value, ok := token.(string)
			if err != nil || !ok {
				return invalid()
			}
			switch key {
			case "format":
				if value != "synthetic-learning-config-v1" {
					return invalid()
				}
			case "ruleLabel":
				in.RuleLabel = value
			case "dataSourceLabel":
				in.DataSourceLabel = value
			case "domainLabel":
				in.DomainLabel = value
			}
		case "synthetic":
			token, err = d.Token()
			if err != nil || token != true {
				return invalid()
			}
		case "windowSeconds":
			token, err = d.Token()
			value, ok := token.(json.Number)
			if err != nil || !ok {
				return invalid()
			}
			in.WindowSeconds, err = value.Int64()
			if err != nil {
				return invalid()
			}
		case "allowedFields", "selectedFields":
			limit := MaxAllowedFields
			if key == "selectedFields" {
				limit = MaxSelectedFields
			}
			values, err := parseFields(d, limit)
			if err != nil {
				return invalid()
			}
			if key == "allowedFields" {
				in.AllowedFields = values
			} else {
				in.SelectedFields = values
			}
		default:
			return invalid()
		}
	}
	if len(seen) != 8 {
		return invalid()
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return invalid()
	}
	if _, err := d.Token(); err != io.EOF {
		return invalid()
	}
	// All supported strings are ASCII. Invalid UTF-16 escape sequences replaced
	// by encoding/json with U+FFFD therefore fail the fixed keys/format or New's
	// ASCII validators; no malformed Unicode can be silently repaired and accepted.
	return New(in)
}

func parseFields(d *json.Decoder, limit int) ([]string, error) {
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return nil, ErrInvalid
	}
	values := make([]string, 0)
	for d.More() {
		if len(values) == limit {
			return nil, ErrInvalid
		}
		token, err := d.Token()
		value, ok := token.(string)
		if err != nil || !ok {
			return nil, ErrInvalid
		}
		values = append(values, value)
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') {
		return nil, ErrInvalid
	}
	return values, nil
}
