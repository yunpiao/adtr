package domains

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Caller owns the schema, tenant, actor and connection locks. A reservation and
// its typed dependency must commit together with the original task.
func reserveDirectoryUseTx(ctx context.Context, tx pgx.Tx, t tasks.Task, p directoryPinnedPayload) error {
	return reserveDirectoryUseForProfileTx(ctx, tx, directoryTaskV1, t, p)
}

func reserveDirectoryUseForProfileTx(ctx context.Context, tx pgx.Tx, profile directoryTaskProfile, t tasks.Task, p directoryPinnedPayload) error {
	identity, ok := profile.identity()
	if !ok {
		return errDirectoryUseEvidence
	}
	pins, err := directoryUseTaskPayloadForProfile(profile, t)
	if err != nil || pins != p || t.State != tasks.Queued || t.Attempt != 0 {
		return errDirectoryUseEvidence
	}
	// The old wrapper preserves historical INSERTs; migration 16 defaults
	// absent provenance to dictionary 1. V2 always records its explicit pin.
	statement := `INSERT INTO adtr.domain_directory_task_uses(tenant_id,domain_id,task_id,account_id,account_credential_revision,connection_revision,connection_credential_generation,policy_revision,grant_role_id,grant_revision,actor_id,purpose,state)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'reserved')`
	if profile == directoryTaskV2 {
		statement = `INSERT INTO adtr.domain_directory_task_uses(tenant_id,domain_id,task_id,account_id,account_credential_revision,connection_revision,connection_credential_generation,policy_revision,grant_role_id,grant_revision,actor_id,purpose,state,dictionary_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'reserved',2)`
	}
	_, err = tx.Exec(ctx, statement, t.TenantID, t.DomainID, t.ID, p.AccountID, p.AccountCredentialRevision, p.ConnectionRevision, p.ConnectionCredentialGeneration, p.PolicyRevision, p.GrantRoleID, p.GrantRevision, t.ActorID, identity.purpose)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation) VALUES($1,$2,$3,$4,$5,$6,$7)`, t.TenantID, t.DomainID, p.AccountID, identity.kind, t.ID, p.AccountCredentialRevision, p.ConnectionCredentialGeneration)
	return err
}

// Opening is part of the first fenced checkpoint. The caller must not decrypt
// or use the copied snapshot until WithTx has reported a successful commit.
func openDirectoryUseTx(ctx context.Context, tx pgx.Tx, t tasks.Task, p directoryPinnedPayload) error {
	return openDirectoryUseForProfileTx(ctx, tx, directoryTaskV1, t, p)
}

func openDirectoryUseForProfileTx(ctx context.Context, tx pgx.Tx, profile directoryTaskProfile, t tasks.Task, p directoryPinnedPayload) error {
	identity, ok := profile.identity()
	if !ok {
		return errDirectoryUseEvidence
	}
	pins, err := directoryUseTaskPayloadForProfile(profile, t)
	if err != nil || pins != p || t.State != tasks.Running || t.Attempt != 1 || t.LeaseOwner == "" || t.FencingToken <= 0 {
		return errDirectoryUseEvidence
	}
	tag, err := tx.Exec(ctx, `UPDATE adtr.domain_directory_task_uses u SET state='opened',opened_at=clock_timestamp(),opener_owner=$4,opener_fencing_token=$5,opener_attempt=$6
 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state='reserved' AND actor_id=$7 AND account_id=$8 AND account_credential_revision=$9 AND connection_revision=$10 AND connection_credential_generation=$11 AND policy_revision=$12 AND grant_role_id=$13 AND grant_revision=$14 AND purpose=$15 AND (CASE WHEN to_jsonb(u) ? 'dictionary_version' THEN to_jsonb(u)->'dictionary_version' ELSE '1'::jsonb END)::text=$16`, t.TenantID, t.DomainID, t.ID, t.LeaseOwner, t.FencingToken, t.Attempt, t.ActorID, p.AccountID, p.AccountCredentialRevision, p.ConnectionRevision, p.ConnectionCredentialGeneration, p.PolicyRevision, p.GrantRoleID, p.GrantRevision, identity.purpose, strconv.Itoa(identity.dictionaryVersion))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return problem(409, "directory_use_changed")
	}
	return nil
}

var errDirectoryUseEvidence = errors.New("directory use completion evidence does not match")

type directoryUseRow struct {
	profile              directoryTaskProfile
	payload              directoryPinnedPayload
	actor                int64
	state                string
	owner                *string
	fence                *int64
	attempt              *int
	reason               *string
	openedAt, quiescedAt *time.Time
}

// Validate immutable task identity without consulting live authority. Keeping
// cleanup separate from admission is essential after revocation or lease loss.
func directoryUseTaskPayload(t tasks.Task) (directoryPinnedPayload, error) {
	return directoryUseTaskPayloadForProfile(directoryTaskV1, t)
}

func directoryUseTaskPayloadForProfile(profile directoryTaskProfile, t tasks.Task) (directoryPinnedPayload, error) {
	var p directoryPinnedPayload
	identity, ok := profile.identity()
	if !ok || t.Kind != identity.kind || t.PayloadVersion != 1 || t.MaxAttempts != 1 || t.ParentID != "" || t.ID == "" || t.TenantID == "" || t.DomainID == "" || t.ActorID <= 0 || t.PayloadHash == "" {
		return p, errDirectoryUseEvidence
	}
	if _, err := validateDirectoryPayloadForProfile(profile, t.Payload); err != nil {
		return p, errDirectoryUseEvidence
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return p, errDirectoryUseEvidence
	}
	return p, nil
}

func (u directoryUseRow) validLifecycle() bool {
	unopened := u.openedAt == nil && u.owner == nil && u.fence == nil && u.attempt == nil
	opened := u.openedAt != nil && u.owner != nil && *u.owner != "" && u.fence != nil && *u.fence > 0 && u.attempt != nil && *u.attempt == 1
	switch u.state {
	case "reserved":
		return unopened && u.quiescedAt == nil && u.reason == nil
	case "opened":
		return opened && u.quiescedAt == nil && u.reason == nil
	case "quiesced":
		return u.quiescedAt != nil && u.reason != nil && ((*u.reason == "executor_returned" && opened) || (*u.reason == "never_opened_terminal" && unopened))
	default:
		return false
	}
}

// Lock order is schema (engine/caller), tenant, task, connection, account, use.
// No current user, role, grant, source or lease authority is required to stop a
// previously opened use. Immutable task/payload and saved opener are sufficient.
func lockDirectoryUseTx(ctx context.Context, tx pgx.Tx, tenant, id string) (tasks.Task, directoryUseRow, error) {
	return lockDirectoryUseForProfileTx(ctx, tx, directoryTaskV1, tenant, id)
}

func lockDirectoryUseForProfileTx(ctx context.Context, tx pgx.Tx, profile directoryTaskProfile, tenant, id string) (tasks.Task, directoryUseRow, error) {
	var t tasks.Task
	var u directoryUseRow
	identity, ok := profile.identity()
	if !ok {
		return t, u, errDirectoryUseEvidence
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))`, tenant); err != nil {
		return t, u, err
	}
	err := tx.QueryRow(ctx, `SELECT task_id,tenant_id,domain_id,actor_id,kind,payload_version,payload_hash,payload,state,max_attempts,parent_task_id FROM adtr.tasks WHERE tenant_id=$1 AND task_id=$2 FOR UPDATE`, tenant, id).Scan(&t.ID, &t.TenantID, &t.DomainID, &t.ActorID, &t.Kind, &t.PayloadVersion, &t.PayloadHash, &t.Payload, &t.State, &t.MaxAttempts, &t.ParentID)
	if err != nil {
		return t, u, err
	}
	pins, err := directoryUseTaskPayloadForProfile(profile, t)
	if err != nil {
		return t, u, err
	}
	var locked string
	if err = tx.QueryRow(ctx, `SELECT domain_id FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id=$2 FOR UPDATE`, tenant, t.DomainID).Scan(&locked); err != nil {
		return t, u, err
	}
	if err = tx.QueryRow(ctx, `SELECT account_id FROM adtr.operation_accounts WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 FOR UPDATE`, tenant, t.DomainID, pins.AccountID).Scan(&locked); err != nil {
		return t, u, err
	}
	u.payload.CredentialSource = "operation_account"
	var purpose, dictionaryVersion string
	err = tx.QueryRow(ctx, `SELECT account_id,account_credential_revision::text,connection_revision::text,connection_credential_generation::text,policy_revision,grant_role_id,grant_revision::text,actor_id,state,opener_owner,opener_fencing_token,opener_attempt,quiescence_reason,opened_at,quiesced_at,purpose,(CASE WHEN to_jsonb(u) ? 'dictionary_version' THEN to_jsonb(u)->'dictionary_version' ELSE '1'::jsonb END)::text
 FROM adtr.domain_directory_task_uses u WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 FOR UPDATE`, tenant, t.DomainID, id).Scan(&u.payload.AccountID, &u.payload.AccountCredentialRevision, &u.payload.ConnectionRevision, &u.payload.ConnectionCredentialGeneration, &u.payload.PolicyRevision, &u.payload.GrantRoleID, &u.payload.GrantRevision, &u.actor, &u.state, &u.owner, &u.fence, &u.attempt, &u.reason, &u.openedAt, &u.quiescedAt, &purpose, &dictionaryVersion)
	if err != nil {
		return t, u, err
	}
	if purpose != identity.purpose || dictionaryVersion != strconv.Itoa(identity.dictionaryVersion) || u.actor != t.ActorID || u.payload != pins || !u.validLifecycle() {
		return t, u, errDirectoryUseEvidence
	}
	u.profile = profile
	// Cleanup checks immutable dependency identity, without live authority.
	var count int
	var matched bool
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND consumer_kind=$4 AND account_credential_revision=$6 AND connection_credential_generation=$7),false)
 FROM adtr.operation_account_dependencies WHERE consumer_kind=$4 AND object_id=$5`, tenant, t.DomainID, pins.AccountID, identity.kind, id, pins.AccountCredentialRevision, pins.ConnectionCredentialGeneration).Scan(&count, &matched); err != nil {
		return t, u, err
	}
	if (u.state == "quiesced" && count != 0) || (u.state != "quiesced" && (count != 1 || !matched)) {
		return t, u, errDirectoryUseEvidence
	}
	return t, u, nil
}

func (s *Store) acknowledgeDirectoryUse(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
	return s.acknowledgeDirectoryUseForProfile(ctx, tx, directoryTaskV1, q)
}

func (s *Store) acknowledgeDirectoryUseForProfile(ctx context.Context, tx pgx.Tx, profile directoryTaskProfile, q tasks.QuiescedAttempt) error {
	identity, ok := profile.identity()
	if !ok || q.Kind() != identity.kind || q.PayloadVersion() != 1 || q.Owner() == "" || q.Fence() <= 0 || q.Attempt() != 1 || q.ActorID() <= 0 {
		return errDirectoryUseEvidence
	}
	t, u, err := lockDirectoryUseForProfileTx(ctx, tx, profile, q.TenantID(), q.TaskID())
	if err != nil {
		return err
	}
	if t.ID != q.TaskID() || t.TenantID != q.TenantID() || t.DomainID != q.DomainID() || t.ActorID != q.ActorID() || t.Kind != q.Kind() || t.PayloadVersion != q.PayloadVersion() || t.PayloadHash != q.PayloadHash() {
		return errDirectoryUseEvidence
	}
	if u.state == "reserved" {
		return nil
	} // Only terminal reconciliation proves never-opened.
	// Finish may win the race with this callback and let independent terminal
	// maintenance retire a reservation that this executor never opened. That
	// durable no-open evidence is already a successful acknowledgement.
	if u.state == "quiesced" && u.reason != nil && *u.reason == "never_opened_terminal" && t.State.Terminal() && u.openedAt == nil && u.quiescedAt != nil && u.owner == nil && u.fence == nil && u.attempt == nil {
		return nil
	}
	if u.owner == nil || u.fence == nil || u.attempt == nil || *u.owner != q.Owner() || *u.fence != q.Fence() || *u.attempt != q.Attempt() {
		return errDirectoryUseEvidence
	}
	if u.state == "quiesced" {
		if u.reason == nil || *u.reason != "executor_returned" || u.openedAt == nil || u.quiescedAt == nil {
			return errDirectoryUseEvidence
		}
		return nil
	}
	if u.state != "opened" {
		return errDirectoryUseEvidence
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('adtr.directory_use_protocol','directory_use_cleanup_v1',true),set_config('adtr.directory_use_task',$1,true),set_config('adtr.directory_use_owner',$2,true),set_config('adtr.directory_use_fence',$3,true),set_config('adtr.directory_use_attempt',$4,true)`, t.ID, q.Owner(), strconv.FormatInt(q.Fence(), 10), strconv.Itoa(q.Attempt())); err != nil {
		return err
	}
	return quiesceDirectoryUseTx(ctx, tx, t, u, "executor_returned")
}

func quiesceDirectoryUseTx(ctx context.Context, tx pgx.Tx, t tasks.Task, u directoryUseRow, reason string) error {
	identity, ok := u.profile.identity()
	pins, err := directoryUseTaskPayloadForProfile(u.profile, t)
	if !ok || err != nil || pins != u.payload || u.actor != t.ActorID || !u.validLifecycle() {
		return errDirectoryUseEvidence
	}
	tag, err := tx.Exec(ctx, `UPDATE adtr.domain_directory_task_uses SET state='quiesced',quiesced_at=clock_timestamp(),quiescence_reason=$4 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state=$5`, t.TenantID, t.DomainID, t.ID, reason, u.state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errDirectoryUseEvidence
	}
	tag, err = tx.Exec(ctx, `DELETE FROM adtr.operation_account_dependencies WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND consumer_kind=$4 AND object_id=$5 AND account_credential_revision=$6 AND connection_credential_generation=$7`, t.TenantID, t.DomainID, u.payload.AccountID, identity.kind, t.ID, u.payload.AccountCredentialRevision, u.payload.ConnectionCredentialGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errDirectoryUseEvidence
	}
	return nil
}

// ReconcileReservedDirectoryUses retires only never-opened reservations of terminal
// tasks. It is independent maintenance, never part of Claim/RecoverExpired or an
// account mutation transaction. Opened uses are intentionally quarantined after
// process loss; only an in-process executor-return witness may acknowledge them.
func (s *Store) ReconcileReservedDirectoryUses(ctx context.Context, cfg *pgx.ConnConfig, limit int) (int, error) {
	return s.reconcileReservedDirectoryUsesForProfile(ctx, cfg, directoryTaskV1, limit)
}

// ReconcileReservedDirectoryV2Uses has an independent bounded candidate set so
// a backlog in one profile cannot starve the other profile's terminal cleanup.
func (s *Store) ReconcileReservedDirectoryV2Uses(ctx context.Context, cfg *pgx.ConnConfig, limit int) (int, error) {
	return s.reconcileReservedDirectoryUsesForProfile(ctx, cfg, directoryTaskV2, limit)
}

func (s *Store) reconcileReservedDirectoryUsesForProfile(ctx context.Context, cfg *pgx.ConnConfig, profile directoryTaskProfile, limit int) (int, error) {
	identity, ok := profile.identity()
	if !ok {
		return 0, errDirectoryUseEvidence
	}
	if cfg == nil || limit < 1 || limit > 100 {
		return 0, problem(400, "invalid_input")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return 0, err
	}
	defer conn.Close(ctx)
	type candidate struct{ tenant, id string }
	candidates := []candidate{}
	// Discovery has the exact gate, but takes no task/resource row locks.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT u.tenant_id,u.task_id FROM adtr.domain_directory_task_uses u JOIN adtr.tasks t ON t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id WHERE u.state='reserved' AND u.purpose=$2 AND t.kind=$2 AND t.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled') ORDER BY u.reserved_at,u.task_id LIMIT $1`, limit, identity.purpose)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.tenant, &c.id); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	total := 0
	for _, c := range candidates {
		changed, err := reconcileReservedDirectoryUseForProfile(ctx, conn, profile, c.tenant, c.id)
		if err != nil {
			return total, err
		}
		if changed {
			total++
		}
	}
	return total, nil
}

func reconcileReservedDirectoryUse(ctx context.Context, conn *pgx.Conn, tenant, id string) (bool, error) {
	return reconcileReservedDirectoryUseForProfile(ctx, conn, directoryTaskV1, tenant, id)
}

func reconcileReservedDirectoryUseForProfile(ctx context.Context, conn *pgx.Conn, profile directoryTaskProfile, tenant, id string) (bool, error) {
	if _, ok := profile.identity(); !ok {
		return false, errDirectoryUseEvidence
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		return false, err
	}
	t, u, err := lockDirectoryUseForProfileTx(ctx, tx, profile, tenant, id)
	if err != nil {
		return false, err
	}
	if !t.State.Terminal() || u.state != "reserved" {
		return false, tx.Commit(ctx)
	}
	if !u.validLifecycle() {
		return false, errDirectoryUseEvidence
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('adtr.directory_use_protocol','directory_use_reserved_cleanup_v1',true),set_config('adtr.directory_use_task',$1,true)`, id); err != nil {
		return false, err
	}
	if err = quiesceDirectoryUseTx(ctx, tx, t, u, "never_opened_terminal"); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
