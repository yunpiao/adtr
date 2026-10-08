package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var kindName = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)
var safeCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type Registry struct{ kinds map[string]Kind }

func NewRegistry(kinds ...Kind) (*Registry, error) {
	r := &Registry{kinds: map[string]Kind{}}
	for _, k := range kinds {
		if !kindName.MatchString(k.Name) || k.Version < 1 || k.MaxAttempts < 1 || k.MaxAttempts > 5 || k.Validate == nil || k.Execute == nil || !(k.ReplaySafe || k.SingleAttemptOnly) || k.Lease < 100*time.Millisecond || k.Lease > 5*time.Minute || k.Heartbeat < 10*time.Millisecond || k.Heartbeat > k.Lease/3 || k.Timeout < 10*time.Millisecond || k.Timeout > time.Hour || k.RetryBase < 10*time.Millisecond || k.RetryCap < k.RetryBase || k.RetryCap > time.Minute {
			return nil, fmt.Errorf("invalid task kind policy: %s", k.Name)
		}
		if k.SingleAttemptOnly && (k.ReplaySafe || k.MaxAttempts != 1 || len(k.RetryCodes) != 0 || k.Schedulable) {
			return nil, fmt.Errorf("invalid single-attempt task policy")
		}
		if k.OnQuiesced != nil && (!k.SingleAttemptOnly || k.Schedulable) {
			return nil, fmt.Errorf("quiescence hook requires unschedulable single-attempt kind")
		}
		if _, exists := r.kinds[k.Name]; exists {
			return nil, fmt.Errorf("duplicate task kind")
		}
		for _, code := range k.RetryCodes {
			if !safeCode.MatchString(code) || code == "authorization_revoked" || code == "invalid_input" || code == "credentials_invalid" || code == "certificate_invalid" {
				return nil, fmt.Errorf("invalid retry policy")
			}
		}
		k.RetryCodes = append([]string(nil), k.RetryCodes...)
		r.kinds[k.Name] = k
	}
	return r, nil
}
func ProductionRegistry(extra ...Kind) *Registry {
	health := Kind{Name: "infrastructure.health", Version: 1, Platform: true, Schedulable: true, MaxAttempts: 5, Lease: 30 * time.Second, Heartbeat: 5 * time.Second, Timeout: 10 * time.Second, RetryBase: 5 * time.Second, RetryCap: 60 * time.Second, RetryCodes: []string{"database_unavailable"}, ReplaySafe: true, Validate: emptyPayload, Execute: func(ctx context.Context, ex Execution) Outcome {
		if _, err := ex.Checkpoint(ctx, 25, json.RawMessage(`{}`), json.RawMessage(`{}`), ex.Task.ResultVersion); err != nil {
			if errors.Is(err, ErrCancelRequested) {
				return Outcome{State: Cancelled, Code: "cancelled"}
			}
			if errors.Is(err, ErrAuthorization) {
				return Outcome{State: Failed, Code: "authorization_revoked"}
			}
			return Outcome{State: Failed, Code: "checkpoint_failed"}
		}
		if ex.Probe == nil || ex.Probe(ctx) != nil {
			return Outcome{State: Failed, Code: "database_unavailable", Retryable: true}
		}
		return Outcome{State: Succeeded, Result: json.RawMessage(`{"database":"ready","queue":"ready"}`)}
	}}
	r, err := NewRegistry(append([]Kind{health}, extra...)...)
	if err != nil {
		panic(err)
	}
	return r
}
func (r *Registry) Kinds() []KindInfo {
	out := []KindInfo{}
	for _, k := range r.kinds {
		scope := "domain"
		if k.Platform {
			scope = "platform"
		}
		out = append(out, KindInfo{k.Name, k.Version, scope, k.MaxAttempts, int(k.Timeout / time.Second)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskName < out[j].TaskName })
	return out
}
func (r *Registry) kind(name string) (Kind, error) {
	k, ok := r.kinds[name]
	if !ok {
		return Kind{}, problem(400, "unknown_task_kind")
	}
	return k, nil
}
func (k Kind) scope(domain string) Scope {
	return Scope{DomainID: domain, TaskName: k.Name, Platform: k.Platform}
}
func (k Kind) retry(code string) bool {
	for _, c := range k.RetryCodes {
		if c == code {
			return true
		}
	}
	return false
}
func (k Kind) delay(attempt int) time.Duration {
	d := k.RetryBase
	for i := 1; i < attempt; i++ {
		if d >= k.RetryCap/2 {
			return k.RetryCap
		}
		d *= 2
	}
	if d > k.RetryCap {
		return k.RetryCap
	}
	return d
}
func emptyPayload(raw json.RawMessage) (json.RawMessage, error) {
	v, err := canonicalObject(raw)
	if err != nil || string(v) != "{}" {
		return nil, problem(400, "invalid_payload")
	}
	return v, nil
}

// Canonicalization rejects ambiguous duplicate keys before hashing. Decoder
// UseNumber preserves integer precision; kind validators must freeze numeric semantics.
func canonicalObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > 65536 || !utf8.Valid(raw) {
		return nil, problem(400, "invalid_payload")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := readJSON(d, 0)
	if err != nil {
		return nil, problem(400, "invalid_payload")
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, problem(400, "invalid_payload")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, problem(400, "invalid_payload")
	}
	out, err := json.Marshal(v)
	if err != nil || len(out) > 65536 {
		return nil, problem(400, "invalid_payload")
	}
	return out, nil
}
func readJSON(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("payload too deep")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			s, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid key")
			}
			if _, exists := m[s]; exists {
				return nil, fmt.Errorf("duplicate key")
			}
			v, err := readJSON(d, depth+1)
			if err != nil {
				return nil, err
			}
			m[s] = v
		}
		if _, err = d.Token(); err != nil {
			return nil, err
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, err := readJSON(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		if _, err = d.Token(); err != nil {
			return nil, err
		}
		return a, nil
	}
	return nil, fmt.Errorf("invalid JSON")
}
