// Package learningconfig validates offline synthetic learning-configuration
// values. It performs no learning, authorization, persistence or external I/O.
package learningconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

const (
	MaxWindowSeconds  int64 = 30 * 24 * 60 * 60
	MaxAllowedFields        = 256
	MaxSelectedFields       = 32
)

var ErrInvalid = errors.New("invalid synthetic learning configuration")
var label = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var field = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

// Input is an engineering-only format, not a decoder of legacy machineTime.
// Labels describe synthetic fixtures; they neither identify authorized resources
// nor grant access. AllowedFields is caller-declared vocabulary, not permission.
type Input struct {
	RuleLabel       string
	DataSourceLabel string
	DomainLabel     string
	WindowSeconds   int64
	AllowedFields   []string
	SelectedFields  []string
}

type snapshot struct {
	Format          string   `json:"format"`
	Synthetic       bool     `json:"synthetic"`
	RuleLabel       string   `json:"ruleLabel"`
	DataSourceLabel string   `json:"dataSourceLabel"`
	DomainLabel     string   `json:"domainLabel"`
	WindowSeconds   int64    `json:"windowSeconds"`
	AllowedFields   []string `json:"allowedFields"`
	SelectedFields  []string `json:"selectedFields"`
}

// Configuration is immutable after construction and safe for concurrent reads.
// Its zero value is invalid. Copying a valid value preserves its immutable bytes.
type Configuration struct{ canonical string }

// New validates all input without trimming, coercion, defaulting or deduplication.
// Both vocabularies are unordered sets; their canonical order is lexical ASCII.
func New(in Input) (Configuration, error) {
	for _, item := range []struct{ name, value string }{
		{"ruleLabel", in.RuleLabel}, {"dataSourceLabel", in.DataSourceLabel}, {"domainLabel", in.DomainLabel},
	} {
		if !label.MatchString(item.value) {
			return Configuration{}, fmt.Errorf("%w: %s", ErrInvalid, item.name)
		}
	}
	if in.WindowSeconds < 1 || in.WindowSeconds > MaxWindowSeconds {
		return Configuration{}, fmt.Errorf("%w: windowSeconds", ErrInvalid)
	}
	allowed, err := canonicalFields(in.AllowedFields, MaxAllowedFields, "allowedFields")
	if err != nil {
		return Configuration{}, err
	}
	selected, err := canonicalFields(in.SelectedFields, MaxSelectedFields, "selectedFields")
	if err != nil {
		return Configuration{}, err
	}
	known := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		known[name] = true
	}
	for _, name := range selected {
		if !known[name] {
			return Configuration{}, fmt.Errorf("%w: selectedFields outside vocabulary", ErrInvalid)
		}
	}
	value := snapshot{"synthetic-learning-config-v1", true, in.RuleLabel, in.DataSourceLabel, in.DomainLabel, in.WindowSeconds, allowed, selected}
	encoded, err := json.Marshal(value)
	if err != nil {
		return Configuration{}, fmt.Errorf("%w: encoding", ErrInvalid)
	}
	return Configuration{canonical: string(encoded)}, nil
}

func canonicalFields(values []string, limit int, name string) ([]string, error) {
	if len(values) == 0 || len(values) > limit {
		return nil, fmt.Errorf("%w: %s count", ErrInvalid, name)
	}
	copyValues := append([]string(nil), values...)
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !field.MatchString(value) || seen[value] {
			return nil, fmt.Errorf("%w: %s member", ErrInvalid, name)
		}
		seen[value] = true
	}
	sort.Strings(copyValues)
	return copyValues, nil
}

// CanonicalJSON returns a new byte slice including the explicit synthetic marker,
// format version and complete allowed vocabulary. It is not an execution request.
func (c Configuration) CanonicalJSON() ([]byte, error) {
	if c.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(c.canonical), nil
}

// Digest hashes exactly CanonicalJSON with SHA-256. It proves value equality only,
// not authenticity, authorization, training completion or model quality.
func (c Configuration) Digest() (string, error) {
	if c.canonical == "" {
		return "", ErrInvalid
	}
	sum := sha256.Sum256([]byte(c.canonical))
	return hex.EncodeToString(sum[:]), nil
}
