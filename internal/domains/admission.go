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

func (s *Store) SubmitTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, in Input) (tasks.Submission, error) {
	if e := ValidateInput("/test", &in); e != nil {
		return tasks.Submission{}, e
	}
	v, e := getRow(ctx, tx, p.TenantID, in.DomainID, true)
	if e != nil {
		return tasks.Submission{}, e
	}
	// Resolve the whole family before CAS/source selection. Replays retain the
	// original payload and epoch and never consume another account reservation.
	var actor int64
	var kind string
	var version int
	var raw json.RawMessage
	e = tx.QueryRow(ctx, `SELECT actor_id,kind,payload_version,payload FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND kind IN ($3,$4) AND idempotency_key=$5`, p.TenantID, v.DomainID, KindName, AccountKindName, in.IdempotencyKey).Scan(&actor, &kind, &version, &raw)
	if e == nil {
		valid := actor == p.ActorID && version == 1 && v.Revision == in.ExpectedRevision
		switch kind {
		case KindName:
			var old pinnedPayload
			_, err := validatePayload(raw)
			valid = valid && err == nil && json.Unmarshal(raw, &old) == nil && v.CredentialSource == "custom" && old.ConnectionRevision == v.Revision && old.CredentialRevision == v.CredentialRevision && old.PolicyRevision == s.policyRevision()
		case AccountKindName:
			var old accountPinnedPayload
			_, err := validateAccountPayload(raw)
			valid = valid && err == nil && json.Unmarshal(raw, &old) == nil && v.CredentialSource == "operation_account" && old.ConnectionRevision == v.Revision && old.ConnectionCredentialGeneration == v.CredentialRevision && old.AccountID == v.accountID && old.AccountCredentialRevision == strconv.FormatInt(v.accountCredential, 10) && old.PolicyRevision == s.policyRevision()
		default:
			valid = false
		}
		if !valid {
			return tasks.Submission{}, problem(409, "idempotency_conflict")
		}
		return engine.SubmitTx(ctx, tx, p, tasks.SubmitInput{TaskName: kind, DomainID: v.DomainID, PayloadVersion: 1, Payload: raw, IdempotencyKey: in.IdempotencyKey})
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return tasks.Submission{}, e
	}
	if v.Revision != in.ExpectedRevision {
		return tasks.Submission{}, problem(409, "revision_conflict")
	}
	if v.CredentialSource == "unconfigured" {
		return tasks.Submission{}, problem(409, "credential_unconfigured")
	}
	if v.CredentialSource != "custom" && v.CredentialSource != "operation_account" {
		return tasks.Submission{}, problem(409, "credential_source_changed")
	}
	if !s.runtime.Enabled() {
		return tasks.Submission{}, problem(503, "domain_key_unavailable")
	}
	if !s.runtime.ProbeEnabled() {
		return tasks.Submission{}, problem(503, "domain_probe_disabled")
	}
	ip := netip.Addr{}
	if v.LDAPAddr != "" {
		ip, e = netip.ParseAddr(v.LDAPAddr)
		if e != nil {
			return tasks.Submission{}, problem(503, "invalid_config")
		}
	}
	if _, e = s.runtime.Policy().AuthorizeTarget(p.TenantID, v.Domain, v.DCHostName, ip); e != nil {
		return tasks.Submission{}, problem(403, "destination_denied")
	}
	if v.CredentialSource == "operation_account" {
		return s.submitAccountTx(ctx, tx, engine, p, in, v)
	}
	payload := pinnedPayload{v.Revision, v.CredentialRevision, s.policyRevision(), strconv.FormatInt(v.generation+1, 10)}
	out, e := engine.SubmitTx(ctx, tx, p, tasks.SubmitInput{TaskName: KindName, DomainID: v.DomainID, PayloadVersion: 1, Payload: payload.json(), IdempotencyKey: in.IdempotencyKey})
	if e != nil {
		return out, familyConflict(e)
	}
	if out.Replayed {
		return tasks.Submission{}, problem(409, "idempotency_conflict")
	}
	if e = recordTestSubmission(ctx, tx, p, v, out.Task.ID); e != nil {
		return tasks.Submission{}, e
	}
	return out, nil
}
