package auth

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type domainAdmissionPins struct {
	ConnectionRevision   string
	CredentialRevision   string
	PolicyRevision       string
	DiagnosticGeneration string
}

// Validate the complete payload, not substrings: a policy digest may legitimately
// contain "dc1". Errors deliberately omit payload values and unknown key names.
func validateDomainAdmissionPayload(raw string, want domainAdmissionPins) error {
	expected := map[string]string{
		"connectionRevision":   want.ConnectionRevision,
		"credentialRevision":   want.CredentialRevision,
		"policyRevision":       want.PolicyRevision,
		"diagnosticGeneration": want.DiagnosticGeneration,
	}
	d := json.NewDecoder(strings.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return errors.New("payload must be a JSON object")
	}
	seen := make(map[string]bool, len(expected))
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return errors.New("invalid payload key")
		}
		key, ok := token.(string)
		value, allowed := expected[key]
		if !ok || !allowed || seen[key] {
			return errors.New("unexpected or duplicate payload key")
		}
		seen[key] = true
		var actual *string
		if err := d.Decode(&actual); err != nil || actual == nil || *actual != value {
			return fmt.Errorf("payload %s must match the expected string pin", key)
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(seen) != len(expected) {
		return errors.New("payload must contain exactly four pins")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing payload data")
	}
	return nil
}

func TestDomainAdmissionPayloadContract(t *testing.T) {
	// This deterministic SHA-256 digest triggers the former raw "dc1" substring check.
	policy := fmt.Sprintf("%x", sha256.Sum256([]byte("synthetic-policy-131")))
	want := domainAdmissionPins{"1", "1", policy, "2"}
	valid := `{"connectionRevision":"1","credentialRevision":"1","policyRevision":"` + policy + `","diagnosticGeneration":"2"}`
	if !strings.Contains(valid, "dc1") {
		t.Fatal("regression fixture no longer exercises the old false positive")
	}
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"digest_contains_dc1", valid, true},
		{"whitespace", " \n" + valid + "\t", true},
		{"malformed", valid[:len(valid)-1], false},
		{"not_object", `[]`, false},
		{"null_object", `null`, false},
		{"missing_pin", strings.Replace(valid, `"connectionRevision":"1",`, "", 1), false},
		{"password_field", strings.TrimSuffix(valid, "}") + `,"password":"synthetic-secret"}`, false},
		{"arbitrary_field", strings.TrimSuffix(valid, "}") + `,"other":"synthetic-secret"}`, false},
		{"duplicate_pin", strings.TrimSuffix(valid, "}") + `,"connectionRevision":"1"}`, false},
		{"wrong_case_key", strings.Replace(valid, "connectionRevision", "ConnectionRevision", 1), false},
		{"number_pin", strings.Replace(valid, `"connectionRevision":"1"`, `"connectionRevision":1`, 1), false},
		{"null_pin", strings.Replace(valid, `"credentialRevision":"1"`, `"credentialRevision":null`, 1), false},
		{"nested_secret", strings.Replace(valid, `"credentialRevision":"1"`, `"credentialRevision":{"password":"synthetic-secret"}`, 1), false},
		{"wrong_connection", strings.Replace(valid, `"connectionRevision":"1"`, `"connectionRevision":"2"`, 1), false},
		{"wrong_credential", strings.Replace(valid, `"credentialRevision":"1"`, `"credentialRevision":"2"`, 1), false},
		{"wrong_policy", strings.Replace(valid, policy, strings.Repeat("b", 64), 1), false},
		{"wrong_generation", strings.Replace(valid, `"diagnosticGeneration":"2"`, `"diagnosticGeneration":"3"`, 1), false},
		{"secret_as_pin", strings.Replace(valid, policy, "synthetic-secret", 1), false},
		{"trailing_object", valid + `{}`, false},
		{"trailing_garbage", valid + `!`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDomainAdmissionPayload(tc.raw, want)
			if (err == nil) != tc.ok {
				t.Fatalf("payload contract accepted=%t, want %t", err == nil, tc.ok)
			}
			if err != nil && (strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), policy)) {
				t.Fatal("validation error disclosed a payload value")
			}
		})
	}
}
