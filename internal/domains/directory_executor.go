package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

// DirectoryKind describes the legacy single-attempt directory consumer.
// Its observation is staged by the final checkpoint and becomes visible only
// through a succeeded task. A cancellation racing Finish discards publication.
// ReadDirectory closes and joins its I/O before returning; executor defers clear
// owned credentials before the engine can issue the OnQuiesced return witness.
func (s *Store) DirectoryKind() tasks.Kind {
	return s.directoryKind(directoryTaskV1)
}

// DirectoryV2Kind keeps the fixed v2 purpose, decoder and original-opener
// acknowledgement together. A v1 grant or payload cannot select this consumer.
func (s *Store) DirectoryV2Kind() tasks.Kind {
	return s.directoryKind(directoryTaskV2)
}

func (s *Store) directoryKind(profile directoryTaskProfile) tasks.Kind {
	identity, ok := profile.identity()
	if !ok {
		return tasks.Kind{}
	}
	return tasks.Kind{Name: identity.kind, Version: 1, MaxAttempts: 1,
		Lease: 30 * time.Second, Heartbeat: time.Second, Timeout: 125 * time.Second,
		RetryBase: time.Second, RetryCap: time.Second, SingleAttemptOnly: true,
		ReplaySafe: false, CancelDiscardsResult: true, OwnerScoped: false,
		Schedulable: false,
		Validate: func(raw json.RawMessage) (json.RawMessage, error) {
			return validateDirectoryPayloadForProfile(profile, raw)
		},
		Execute: func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
			return s.executeDirectoryForProfile(ctx, ex, profile)
		},
		OnQuiesced: func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
			return s.acknowledgeDirectoryUseForProfile(ctx, tx, profile, q)
		}}
}

// These explicit provisional security caps are part of kind version 1. They
// are not production-capacity acceptance or caller-selected search controls.
func directoryReadLimits() directoryassets.Limits {
	return directoryassets.Limits{MaxRows: 10000, MaxPages: 100,
		MaxPageEntries: 1000, MaxBytes: 16 << 20, MaxCookieBytes: 65536}
}

func (s *Store) executeDirectory(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	return s.executeDirectoryForProfile(ctx, ex, directoryTaskV1)
}

func (s *Store) executeDirectoryForProfile(ctx context.Context, ex tasks.Execution, profile directoryTaskProfile) tasks.Outcome {
	if ex.WithTx == nil {
		return failed("executor_unavailable")
	}
	pinned, err := directoryUseTaskPayloadForProfile(profile, ex.Task)
	if err != nil {
		return failed("invalid_input")
	}
	if err = ctx.Err(); err != nil {
		return directoryExecutionError(err)
	}
	version := ex.Task.ResultVersion
	var config row
	var sealed domainconfig.OperationSealedCredential
	// Register clearing before any snapshot acquisition, including transaction
	// callback panic and unknown commit outcomes. Never decrypt on commit error.
	defer func() { clear(sealed.Ciphertext) }()
	version, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		var err error
		config, err = s.currentDirectoryForProfile(ctx, tx, profile, ex.Task, pinned)
		if err == nil {
			err = openDirectoryUseForProfileTx(ctx, tx, profile, ex.Task, pinned)
		}
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT key_id,ciphertext FROM adtr.operation_account_credentials WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND credential_revision=$4 AND envelope_version=2`, ex.Task.TenantID, ex.Task.DomainID, pinned.AccountID, pinned.AccountCredentialRevision).Scan(&sealed.KeyID, &sealed.Ciphertext)
			if errors.Is(err, pgx.ErrNoRows) {
				err = problem(503, "credential_unavailable")
			}
		}
		return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
	})
	if err != nil {
		return directoryExecutionError(err)
	}
	if err = ctx.Err(); err != nil {
		return directoryExecutionError(err)
	}
	plaintext, err := s.runtime.Vault().OpenOperationCredential(ex.Task.TenantID, ex.Task.DomainID, pinned.AccountID, config.accountCredential, sealed)
	defer clear(plaintext)
	clear(sealed.Ciphertext)
	if err != nil {
		if errors.Is(err, domainconfig.ErrKeyUnavailable) {
			return failed("credential_key_unavailable")
		}
		return failed("credential_unavailable")
	}
	var credential struct {
		Version  int    `json:"version"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	defer func() { credential.Username = ""; credential.Password = "" }()
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	valid := exactObject(plaintext, "version", "username", "password")
	err = decoder.Decode(&credential)
	clear(plaintext)
	if !valid || err != nil || credential.Version != 2 || !credentialValid(credential.Username, credential.Password) {
		return failed("credential_unavailable")
	}
	password := []byte(credential.Password)
	defer clear(password)
	credential.Password = ""
	ip := netip.Addr{}
	if config.LDAPAddr != "" {
		ip, err = netip.ParseAddr(config.LDAPAddr)
		if err != nil {
			return failed("invalid_config")
		}
	}
	prefixes, err := s.runtime.Policy().AuthorizeTarget(ex.Task.TenantID, config.Domain, config.DCHostName, ip)
	if err != nil {
		return failed("destination_denied")
	}
	var phaseError error
	progress, pages := 10, 0
	// This separate typed callback checks the directory purpose inside the real
	// engine fence at every transport stage, every page and final return.
	var authorize ldapconnection.DirectoryReadAuthorization = func(phaseCtx context.Context, stage ldapconnection.DirectoryStage) error {
		if stage == ldapconnection.DirectoryPage {
			pages++
		}
		nextProgress := max(progress, directoryReadProgress(stage, pages))
		next, err := ex.WithTx(phaseCtx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := s.currentDirectoryForProfile(ctx, tx, profile, ex.Task, pinned)
			return nextProgress, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			phaseError = err
			return err
		}
		version, progress = next, nextProgress
		return nil
	}
	var observation ldapconnection.DirectoryObservation
	var observationV2 ldapconnection.DirectoryV2Observation
	defer observationV2.Discard()
	var readErr error
	switch profile {
	case directoryTaskV1:
		observation, readErr = ldapconnection.ReadDirectory(ctx, ldapconnection.DirectoryConfig{
			Mode: ldapconnection.Mode(config.Mode), ServerName: config.DCHostName,
			Domain: config.Domain, DialIP: ip, Roots: s.runtime.Policy().Roots(),
			AllowedNetworks: prefixes, Limits: directoryReadLimits(), Authorize: authorize,
		}, ldapconnection.Credential{Username: credential.Username, Password: password})
	case directoryTaskV2:
		observationV2, readErr = ldapconnection.ReadDirectoryV2(ctx, ldapconnection.DirectoryV2Config{
			Mode: ldapconnection.Mode(config.Mode), ServerName: config.DCHostName,
			Domain: config.Domain, DialIP: ip, Roots: s.runtime.Policy().Roots(),
			AllowedNetworks: prefixes, Limits: directoryReadLimits(), Authorize: ldapconnection.DirectoryV2ReadAuthorization(authorize),
		}, ldapconnection.Credential{Username: credential.Username, Password: password})
	default:
		return failed("invalid_input")
	}
	clear(password)
	credential.Username = ""
	if phaseError != nil {
		return directoryExecutionError(phaseError)
	}
	if readErr != nil {
		code, _ := probeClassification(readErr)
		if profile == directoryTaskV2 {
			var failure *ldapconnection.Error
			if errors.As(readErr, &failure) && (failure.Code == ldapconnection.CodeDirectoryLimit || failure.Code == ldapconnection.CodeUnsupportedProfile) {
				code = string(failure.Code)
			}
		}
		if code == "cancelled" {
			return directoryExecutionError(context.Canceled)
		}
		return failed(code)
	}
	// AD may return DNS names with mixed case. Persist the same canonical DNS
	// identity used by configured targets, rejecting malformed RootDSE names.
	source := &observation.Source
	if profile == directoryTaskV2 {
		source = &observationV2.Source
	}
	source.DCHostName, err = CanonicalDNS(source.DCHostName)
	if err != nil {
		return failed("invalid_response")
	}
	_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		_, err := s.currentDirectoryForProfile(ctx, tx, profile, ex.Task, pinned)
		if err == nil {
			switch profile {
			case directoryTaskV1:
				err = publishDirectoryObservationTx(ctx, tx, ex.Task, pinned, observation)
			case directoryTaskV2:
				err = publishDirectoryObservationV2Tx(ctx, tx, ex.Task, pinned, observationV2)
			default:
				err = errDirectoryUseEvidence
			}
		}
		return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), err
	})
	if err != nil {
		return directoryExecutionError(err)
	}
	return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
}

// Page callbacks report bounded progress rather than adding a fixed percentage
// per page. Publication alone reaches 100, regardless of the number of pages.
func directoryReadProgress(stage ldapconnection.DirectoryStage, pages int) int {
	switch stage {
	case ldapconnection.DirectoryValidate:
		return 15
	case ldapconnection.DirectoryDNS:
		return 20
	case ldapconnection.DirectoryDial:
		return 25
	case ldapconnection.DirectoryTLS:
		return 30
	case ldapconnection.DirectoryBind:
		return 35
	case ldapconnection.DirectoryRootDSE:
		return 40
	case ldapconnection.DirectoryPage:
		return 40 + min(max(pages, 0), directoryReadLimits().MaxPages)*50/directoryReadLimits().MaxPages
	case ldapconnection.DirectoryReturn:
		return 95
	default:
		return 10
	}
}

func directoryExecutionError(err error) tasks.Outcome {
	var failure *Error
	if errors.As(err, &failure) && (failure.Code == "directory_read_disabled" || failure.Code == "directory_use_changed") {
		return failed(failure.Code)
	}
	outcome := executionError(err)
	outcome.Result = json.RawMessage(`{}`)
	return outcome
}
