package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

// AccountKind consumes only F03 envelope v2. Its callback is registered only
// because this executor and Probe join resources and clear owned buffers on all
// normal, cancellation and panic unwind paths before returning to the engine.
func (s *Store) AccountKind() tasks.Kind {
	return tasks.Kind{Name: AccountKindName, Version: 1, MaxAttempts: 1, Lease: 30 * time.Second, Heartbeat: time.Second, Timeout: 10 * time.Second, RetryBase: time.Second, RetryCap: time.Second, SingleAttemptOnly: true, ReplaySafe: false, CancelDiscardsResult: false, OwnerScoped: false, Schedulable: false, Validate: validateAccountPayload, Execute: s.executeAccount, OnQuiesced: s.acknowledgeAccountUse}
}

func (s *Store) currentAccount(ctx context.Context, tx pgx.Tx, t tasks.Task, p accountPinnedPayload) (row, error) {
	if !s.runtime.ProbeEnabled() {
		return row{}, problem(503, "domain_probe_disabled")
	}
	if s.policyRevision() != p.PolicyRevision {
		return row{}, problem(409, "policy_revision_changed")
	}
	v, err := getRow(ctx, tx, t.TenantID, t.DomainID, true)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Status == 404 {
			return row{}, problem(409, "connection_revision_changed")
		}
		return row{}, err
	}
	if v.CredentialSource != "operation_account" || v.Revision != p.ConnectionRevision || v.CredentialRevision != p.ConnectionCredentialGeneration || v.accountID != p.AccountID || strconv.FormatInt(v.accountCredential, 10) != p.AccountCredentialRevision || strconv.FormatInt(v.generation, 10) != p.DiagnosticGeneration || v.latest != t.ID {
		return row{}, problem(409, "connection_revision_changed")
	}
	role, grant, err := accountGrantTx(ctx, tx, tasks.Principal{TenantID: t.TenantID, ActorID: t.ActorID}, v)
	if err != nil {
		return row{}, err
	}
	if role != p.GrantRoleID || grant != p.GrantRevision {
		return row{}, tasks.ErrAuthorization
	}
	return v, nil
}

func (s *Store) executeAccount(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	if ex.WithTx == nil {
		return failed("executor_unavailable")
	}
	raw, e := validateAccountPayload(ex.Task.Payload)
	if e != nil {
		return failed("invalid_input")
	}
	var pinned accountPinnedPayload
	_ = json.Unmarshal(raw, &pinned)
	version := ex.Task.ResultVersion
	var config row
	var sealed domainconfig.OperationSealedCredential
	// Install clearing before the transaction: commit failure or panic must
	// not retain an uncommitted sealed snapshot in executor-owned memory.
	defer func() { clear(sealed.Ciphertext) }()
	version, e = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		var err error
		config, err = s.currentAccount(ctx, tx, ex.Task, pinned)
		if err == nil {
			err = openAccountUseTx(ctx, tx, ex.Task, pinned)
		}
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT key_id,ciphertext FROM adtr.operation_account_credentials WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND credential_revision=$4 AND envelope_version=2`, ex.Task.TenantID, ex.Task.DomainID, pinned.AccountID, pinned.AccountCredentialRevision).Scan(&sealed.KeyID, &sealed.Ciphertext)
			if errors.Is(err, pgx.ErrNoRows) {
				err = problem(503, "credential_unavailable")
			}
		}
		return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
	})
	if e != nil {
		return executionError(e)
	}
	plaintext, e := s.runtime.Vault().OpenOperationCredential(ex.Task.TenantID, ex.Task.DomainID, pinned.AccountID, config.accountCredential, sealed)
	if e != nil {
		code := "credential_unavailable"
		if errors.Is(e, domainconfig.ErrKeyUnavailable) {
			code = "credential_key_unavailable"
		}
		return failed(code)
	}
	defer clear(plaintext)
	var cred struct {
		Version  int    `json:"version"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	defer func() { cred.Username = ""; cred.Password = "" }()
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	valid := exactObject(plaintext, "version", "username", "password")
	e = decoder.Decode(&cred)
	clear(plaintext)
	if !valid || e != nil || cred.Version != 2 || !credentialValid(cred.Username, cred.Password) {
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
			_, err := s.currentAccount(ctx, tx, ex.Task, pinned)
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
		v, err := s.currentAccount(ctx, tx, ex.Task, pinned)
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
