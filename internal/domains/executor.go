package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

func (s *Store) Kind() tasks.Kind {
	return tasks.Kind{Name: KindName, Version: 1, MaxAttempts: 1, Lease: 30 * time.Second, Heartbeat: time.Second, Timeout: 10 * time.Second, RetryBase: time.Second, RetryCap: time.Second, SingleAttemptOnly: true, ReplaySafe: false, CancelDiscardsResult: false, OwnerScoped: false, Schedulable: false, Validate: validatePayload, Execute: s.execute}
}
func (s *Store) current(ctx context.Context, tx pgx.Tx, t tasks.Task, p pinnedPayload) (row, error) {
	if !s.runtime.ProbeEnabled() {
		return row{}, problem(503, "domain_probe_disabled")
	}
	if s.policyRevision() != p.PolicyRevision {
		return row{}, problem(409, "policy_revision_changed")
	}
	v, e := getRow(ctx, tx, t.TenantID, t.DomainID, false)
	if e != nil {
		var d *Error
		if errors.As(e, &d) && d.Status == 404 {
			return row{}, problem(409, "connection_revision_changed")
		}
		return row{}, e
	}
	if v.CredentialSource != "custom" || v.Revision != p.ConnectionRevision || v.CredentialRevision != p.CredentialRevision || strconv.FormatInt(v.generation, 10) != p.DiagnosticGeneration || v.latest != t.ID {
		return row{}, problem(409, "connection_revision_changed")
	}
	return v, nil
}
func (s *Store) execute(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	if ex.WithTx == nil {
		return failed("executor_unavailable")
	}
	raw, e := validatePayload(ex.Task.Payload)
	if e != nil {
		return failed("invalid_input")
	}
	var pinned pinnedPayload
	_ = json.Unmarshal(raw, &pinned)
	version := ex.Task.ResultVersion
	var config row
	var sealed domainconfig.SealedCredential
	version, e = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		var err error
		config, err = s.current(ctx, tx, ex.Task, pinned)
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT key_id,ciphertext FROM adtr.domain_credentials WHERE tenant_id=$1 AND domain_id=$2 AND credential_revision=$3`, ex.Task.TenantID, ex.Task.DomainID, config.credential).Scan(&sealed.KeyID, &sealed.Ciphertext)
			if errors.Is(err, pgx.ErrNoRows) {
				err = problem(503, "credential_unavailable")
			}
		}
		return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
	})
	if e != nil {
		return executionError(e)
	}
	defer clear(sealed.Ciphertext)
	plaintext, e := s.runtime.Vault().Open(ex.Task.TenantID, ex.Task.DomainID, config.credential, sealed)
	if e != nil {
		code := "credential_unavailable"
		if errors.Is(e, domainconfig.ErrKeyUnavailable) {
			code = "credential_key_unavailable"
		}
		return failed(code)
	}
	var cred struct {
		Version  int    `json:"version"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	valid := exactObject(plaintext, "version", "username", "password")
	e = decoder.Decode(&cred)
	clear(plaintext)
	if !valid || e != nil || cred.Version != 1 || !credentialValid(cred.Username, cred.Password) {
		return failed("credential_unavailable")
	}
	password := []byte(cred.Password)
	cred.Password = ""
	defer clear(password)
	ip := netip.Addr{}
	if config.LDAPAddr != "" {
		ip, e = netip.ParseAddr(config.LDAPAddr)
		if e != nil {
			return failed("invalid_config")
		}
	}
	prefixes, e := s.runtime.Policy().AuthorizeTarget(ex.Task.TenantID, config.Domain, config.DCHostName, ip)
	if e != nil {
		return failed("destination_denied")
	}
	var phaseError error
	progress := 10
	authorize := func(phaseCtx context.Context, _ ldapconnection.Stage) error {
		next, err := ex.WithTx(phaseCtx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := s.current(ctx, tx, ex.Task, pinned)
			return progress + 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			phaseError = err
			return err
		}
		version = next
		progress += 10
		return nil
	}
	started := time.Now()
	result, probeErr := s.probe(ctx, ldapconnection.Config{Mode: ldapconnection.Mode(config.Mode), ServerName: config.DCHostName, DialIP: ip, Roots: s.runtime.Policy().Roots(), AllowedNetworks: prefixes, Authorize: authorize}, ldapconnection.Credential{Username: cred.Username, Password: password})
	// Probe guarantees connection close/join before returning. Clear our owned
	// password now rather than retaining it while acquiring publication locks.
	clear(password)
	cred.Username = ""
	if phaseError != nil {
		return executionError(phaseError)
	}
	code, stage := "success", "identity"
	successful := probeErr == nil
	if probeErr != nil {
		code, stage = probeClassification(probeErr)
	} else if !slices.Contains(result.SupportedCapabilities, "1.2.840.113556.1.4.800") || !matchesNamingContext(result.DefaultNamingContext, config.Domain) {
		code = "directory_mismatch"
		successful = false
	}
	if successful {
		name, err := CanonicalDNS(result.DCHostName)
		if err != nil {
			successful = false
			code = "invalid_response"
		} else {
			result.DCHostName = name
		}
	}
	if !successful {
		result.DCHostName = ""
		result.DefaultNamingContext = ""
	}
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	_, e = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		v, err := s.current(ctx, tx, ex.Task, pinned)
		if err != nil {
			return 0, nil, nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO adtr.domain_diagnostics(tenant_id,domain_id,task_id,connection_revision,credential_revision,policy_revision,diagnostic_generation,stage,code,successful_observation,dc_hostname,naming_context,elapsed_ms) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, v.revision, v.credential, pinned.PolicyRevision, v.generation, stage, code, successful, result.DCHostName, result.DefaultNamingContext, elapsed)
		if err == nil {
			err = audit(ctx, tx, tasks.Principal{TenantID: ex.Task.TenantID, ActorID: ex.Task.ActorID}, v.DomainID, "domain_test_result", v.revision, v.revision, v.credential, ex.Task.ID, code)
		}
		return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), err
	})
	if e != nil {
		return executionError(e)
	}
	if successful {
		return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
	}
	if code == "cancelled" {
		return tasks.Outcome{State: tasks.Cancelled, Code: code}
	}
	return failed(code)
}
func failed(code string) tasks.Outcome {
	return tasks.Outcome{State: tasks.Failed, Code: code, Result: json.RawMessage(`{}`)}
}
func executionError(err error) tasks.Outcome {
	if errors.Is(err, tasks.ErrCancelRequested) || errors.Is(err, context.Canceled) {
		return tasks.Outcome{State: tasks.Cancelled, Code: "cancelled"}
	}
	if errors.Is(err, tasks.ErrAuthorization) {
		return failed("authorization_revoked")
	}
	if errors.Is(err, tasks.ErrLeaseLost) {
		return failed("lease_lost")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failed("connection_timeout")
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Code {
		case "domain_probe_disabled", "policy_revision_changed", "connection_revision_changed", "credential_unavailable":
			return failed(e.Code)
		}
	}
	return failed("checkpoint_failed")
}
func probeClassification(err error) (string, string) {
	var e *ldapconnection.Error
	if !errors.As(err, &e) {
		return "connection_failed", "connect"
	}
	code := string(e.Code)
	switch code {
	case "invalid_config", "dns_failed", "connect_failed", "connection_timeout", "tls_untrusted", "tls_hostname", "tls_expired", "tls_failed", "starttls_required", "credentials_rejected", "ldap_access_denied", "ldap_timeout", "referral_rejected", "invalid_response", "cancelled", "authorization_revoked":
	case "egress_denied":
		code = "destination_denied"
	default:
		code = "connection_failed"
	}
	stage := "connect"
	switch e.Stage {
	case ldapconnection.StageValidate:
		stage = "authorization"
	case ldapconnection.StageDNS:
		stage = "resolve"
	case ldapconnection.StageDial:
		stage = "connect"
	case ldapconnection.StageTLS, ldapconnection.StageStartTLS:
		stage = "tls"
	case ldapconnection.StageBind:
		stage = "bind"
	case ldapconnection.StageSearch:
		stage = "rootdse"
	}
	return code, stage
}

// Parse the restricted DC-only naming context structurally, honoring RFC4514
// escaped characters/hex octets. Multi-valued RDNs and other attributes cannot
// equal the DNS-derived domain DN and are rejected rather than prefix-matched.
func matchesNamingContext(dn, domain string) bool {
	if len(dn) == 0 || len(dn) > 1024 || !utf8.ValidString(dn) {
		return false
	}
	labels := strings.Split(domain, ".")
	parts := []string{}
	start := 0
	escaped := false
	for i := 0; i < len(dn); i++ {
		if escaped {
			escaped = false
			continue
		}
		if dn[i] == '\\' {
			escaped = true
			continue
		}
		if dn[i] == '+' {
			return false
		}
		if dn[i] == ',' {
			parts = append(parts, dn[start:i])
			start = i + 1
		}
	}
	if escaped {
		return false
	}
	parts = append(parts, dn[start:])
	if len(parts) != len(labels) {
		return false
	}
	for i, part := range parts {
		attr, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(attr), "dc") {
			return false
		}
		value = strings.TrimSpace(value)
		var decoded []byte
		for j := 0; j < len(value); j++ {
			if value[j] != '\\' {
				if strings.ContainsRune(`";<>`, rune(value[j])) {
					return false
				}
				decoded = append(decoded, value[j])
				continue
			}
			j++
			if j >= len(value) {
				return false
			}
			if j+1 < len(value) {
				n, e := strconv.ParseUint(value[j:j+2], 16, 8)
				if e == nil {
					decoded = append(decoded, byte(n))
					j++
					continue
				}
			}
			if !strings.ContainsRune(` ,+"\<>;=#`, rune(value[j])) {
				return false
			}
			decoded = append(decoded, value[j])
		}
		for _, b := range decoded {
			if b >= 128 {
				return false
			}
		}
		if !strings.EqualFold(string(decoded), labels[i]) {
			return false
		}
	}
	return true
}
