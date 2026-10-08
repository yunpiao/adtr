package domains

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

type DirectoryInput struct {
	DomainID                     string `json:"domainId"`
	ExpectedRevision             string `json:"expectedRevision"`
	ExpectedCredentialGeneration string `json:"expectedCredentialGeneration"`
	IdempotencyKey               string `json:"idempotencyKey"`
}

func ValidateDirectoryInput(in DirectoryInput) error {
	if !ValidID(in.DomainID) || !ValidKey(in.IdempotencyKey) {
		return problem(422, "invalid_input")
	}
	for _, revision := range []string{in.ExpectedRevision, in.ExpectedCredentialGeneration} {
		if _, err := Revision(revision); err != nil {
			return problem(422, "invalid_input")
		}
	}
	return nil
}

// SubmitDirectoryTx never advances diagnostic_generation or latest_test_task.
// Caller must first check fresh proof, directory-write and current domain scope.
func (s *Store) SubmitDirectoryTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, in DirectoryInput) (tasks.Submission, error) {
	return s.submitDirectoryForProfileTx(ctx, tx, engine, p, in, directoryTaskV1)
}

// SubmitDirectoryV2Tx admits only the separately granted fixed dictionary 2.
func (s *Store) SubmitDirectoryV2Tx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, in DirectoryInput) (tasks.Submission, error) {
	return s.submitDirectoryForProfileTx(ctx, tx, engine, p, in, directoryTaskV2)
}

func (s *Store) submitDirectoryForProfileTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, in DirectoryInput, profile directoryTaskProfile) (tasks.Submission, error) {
	identity, ok := profile.identity()
	if !ok {
		return tasks.Submission{}, tasks.ErrAuthorization
	}
	if err := ValidateDirectoryInput(in); err != nil {
		return tasks.Submission{}, err
	}
	if engine == nil || !s.directoryEnabled(profile) {
		return tasks.Submission{}, problem(503, "directory_read_disabled")
	}
	v, err := getRow(ctx, tx, p.TenantID, in.DomainID, true)
	if err != nil {
		return tasks.Submission{}, err
	}
	if v.Revision != in.ExpectedRevision || v.CredentialRevision != in.ExpectedCredentialGeneration {
		return tasks.Submission{}, problem(409, "revision_conflict")
	}
	if v.CredentialSource != "operation_account" {
		return tasks.Submission{}, problem(409, "directory_account_required")
	}
	ip := netip.Addr{}
	if v.LDAPAddr != "" {
		ip, err = netip.ParseAddr(v.LDAPAddr)
		if err != nil {
			return tasks.Submission{}, problem(503, "invalid_config")
		}
	}
	if _, err = s.runtime.Policy().AuthorizeTarget(p.TenantID, v.Domain, v.DCHostName, ip); err != nil {
		return tasks.Submission{}, problem(403, "destination_denied")
	}
	role, grant, err := directoryGrantForProfileTx(ctx, tx, profile, p, v)
	if err != nil {
		return tasks.Submission{}, err
	}
	pins := directoryPinnedPayload{"operation_account", v.Revision, v.CredentialRevision, v.accountID, strconv.FormatInt(v.accountCredential, 10), role, grant, s.policyRevision()}
	var actor int64
	var version int
	var raw json.RawMessage
	err = tx.QueryRow(ctx, `SELECT actor_id,payload_version,payload FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND kind=$3 AND idempotency_key=$4`, p.TenantID, v.DomainID, identity.kind, in.IdempotencyKey).Scan(&actor, &version, &raw)
	if err == nil {
		canonical, validErr := validateDirectoryPayloadForProfile(profile, raw)
		var previous directoryPinnedPayload
		if actor != p.ActorID || version != 1 || validErr != nil || json.Unmarshal(canonical, &previous) != nil || previous != pins {
			return tasks.Submission{}, problem(409, "idempotency_conflict")
		}
		return engine.SubmitTx(ctx, tx, p, tasks.SubmitInput{TaskName: identity.kind, DomainID: v.DomainID, PayloadVersion: 1, Payload: canonical, IdempotencyKey: in.IdempotencyKey})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tasks.Submission{}, err
	}
	out, err := engine.SubmitTx(ctx, tx, p, tasks.SubmitInput{TaskName: identity.kind, DomainID: v.DomainID, PayloadVersion: 1, Payload: profile.payload(pins), IdempotencyKey: in.IdempotencyKey})
	if err != nil {
		return out, err
	}
	if out.Replayed {
		return tasks.Submission{}, problem(409, "idempotency_conflict")
	}
	if err = reserveDirectoryUseForProfileTx(ctx, tx, profile, out.Task, pins); err != nil {
		return tasks.Submission{}, err
	}
	if err = audit(ctx, tx, p, v.DomainID, profile.auditAction("submit"), v.revision, v.revision, v.credential, out.Task.ID, "queued"); err != nil {
		return tasks.Submission{}, err
	}
	return out, nil
}
