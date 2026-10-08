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

func reserveAccountUseTx(ctx context.Context, tx pgx.Tx, t tasks.Task, p accountPinnedPayload) error {
	_, err := tx.Exec(ctx, `INSERT INTO adtr.domain_account_task_uses(tenant_id,domain_id,task_id,account_id,account_credential_revision,connection_revision,connection_credential_generation,diagnostic_generation,policy_revision,grant_role_id,grant_revision,actor_id,purpose,state)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'domain.connection_test','reserved')`, t.TenantID, t.DomainID, t.ID, p.AccountID, p.AccountCredentialRevision, p.ConnectionRevision, p.ConnectionCredentialGeneration, p.DiagnosticGeneration, p.PolicyRevision, p.GrantRoleID, p.GrantRevision, t.ActorID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation) VALUES($1,$2,$3,$4,$5,$6,$7)`, t.TenantID, t.DomainID, p.AccountID, AccountKindName, t.ID, p.AccountCredentialRevision, p.ConnectionCredentialGeneration)
	return err
}

// Opening is part of the first fenced checkpoint. The caller must not decrypt
// or use the copied snapshot until WithTx has reported a successful commit.
func openAccountUseTx(ctx context.Context, tx pgx.Tx, t tasks.Task, p accountPinnedPayload) error {
	tag, err := tx.Exec(ctx, `UPDATE adtr.domain_account_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$4,opener_fencing_token=$5,opener_attempt=$6
 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state='reserved' AND actor_id=$7 AND account_id=$8 AND account_credential_revision=$9 AND connection_revision=$10 AND connection_credential_generation=$11 AND diagnostic_generation=$12 AND policy_revision=$13 AND grant_role_id=$14 AND grant_revision=$15 AND purpose='domain.connection_test'`, t.TenantID, t.DomainID, t.ID, t.LeaseOwner, t.FencingToken, t.Attempt, t.ActorID, p.AccountID, p.AccountCredentialRevision, p.ConnectionRevision, p.ConnectionCredentialGeneration, p.DiagnosticGeneration, p.PolicyRevision, p.GrantRoleID, p.GrantRevision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return problem(409, "account_use_changed")
	}
	return nil
}

var errAccountUseEvidence = errors.New("account use completion evidence does not match")

type accountUseRow struct {
	payload              accountPinnedPayload
	actor                int64
	state                string
	owner                *string
	fence                *int64
	attempt              *int
	reason               *string
	openedAt, quiescedAt *time.Time
}

// Lock order is schema (engine/caller), tenant, task, connection, account, use.
// No current user, role, grant, source or lease authority is required to stop a
// previously opened use. Immutable task/payload and saved opener are sufficient.
func lockAccountUseTx(ctx context.Context, tx pgx.Tx, tenant, id string) (tasks.Task, accountUseRow, error) {
	var t tasks.Task
	var u accountUseRow
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))`, tenant); err != nil {
		return t, u, err
	}
	err := tx.QueryRow(ctx, `SELECT task_id,tenant_id,domain_id,actor_id,kind,payload_version,payload_hash,payload,state FROM adtr.tasks WHERE tenant_id=$1 AND task_id=$2 FOR UPDATE`, tenant, id).Scan(&t.ID, &t.TenantID, &t.DomainID, &t.ActorID, &t.Kind, &t.PayloadVersion, &t.PayloadHash, &t.Payload, &t.State)
	if err != nil {
		return t, u, err
	}
	if t.Kind != AccountKindName || t.PayloadVersion != 1 {
		return t, u, errAccountUseEvidence
	}
	if _, err = validateAccountPayload(t.Payload); err != nil {
		return t, u, errAccountUseEvidence
	}
	var pins accountPinnedPayload
	if err = json.Unmarshal(t.Payload, &pins); err != nil {
		return t, u, errAccountUseEvidence
	}
	var locked string
	if err = tx.QueryRow(ctx, `SELECT domain_id FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id=$2 FOR UPDATE`, tenant, t.DomainID).Scan(&locked); err != nil {
		return t, u, err
	}
	if err = tx.QueryRow(ctx, `SELECT account_id FROM adtr.operation_accounts WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 FOR UPDATE`, tenant, t.DomainID, pins.AccountID).Scan(&locked); err != nil {
		return t, u, err
	}
	u.payload.CredentialSource = "operation_account"
	err = tx.QueryRow(ctx, `SELECT account_id,account_credential_revision::text,connection_revision::text,connection_credential_generation::text,diagnostic_generation::text,policy_revision,grant_role_id,grant_revision::text,actor_id,state,opener_owner,opener_fencing_token,opener_attempt,quiescence_reason,opened_at,quiesced_at
 FROM adtr.domain_account_task_uses WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND purpose='domain.connection_test' FOR UPDATE`, tenant, t.DomainID, id).Scan(&u.payload.AccountID, &u.payload.AccountCredentialRevision, &u.payload.ConnectionRevision, &u.payload.ConnectionCredentialGeneration, &u.payload.DiagnosticGeneration, &u.payload.PolicyRevision, &u.payload.GrantRoleID, &u.payload.GrantRevision, &u.actor, &u.state, &u.owner, &u.fence, &u.attempt, &u.reason, &u.openedAt, &u.quiescedAt)
	if err != nil {
		return t, u, err
	}
	if u.actor != t.ActorID || u.payload != pins {
		return t, u, errAccountUseEvidence
	}
	return t, u, nil
}

func (s *Store) acknowledgeAccountUse(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
	if q.Kind() != AccountKindName || q.PayloadVersion() != 1 || q.Owner() == "" || q.Fence() <= 0 || q.Attempt() != 1 || q.ActorID() <= 0 {
		return errAccountUseEvidence
	}
	t, u, err := lockAccountUseTx(ctx, tx, q.TenantID(), q.TaskID())
	if err != nil {
		return err
	}
	if t.ID != q.TaskID() || t.TenantID != q.TenantID() || t.DomainID != q.DomainID() || t.ActorID != q.ActorID() || t.Kind != q.Kind() || t.PayloadVersion != q.PayloadVersion() || t.PayloadHash != q.PayloadHash() {
		return errAccountUseEvidence
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
		return errAccountUseEvidence
	}
	if u.state == "quiesced" {
		if u.reason == nil || *u.reason != "executor_returned" || u.openedAt == nil || u.quiescedAt == nil {
			return errAccountUseEvidence
		}
		return nil
	}
	if u.state != "opened" {
		return errAccountUseEvidence
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('adtr.account_use_protocol','account_use_cleanup_v1',true),set_config('adtr.account_use_task',$1,true),set_config('adtr.account_use_owner',$2,true),set_config('adtr.account_use_fence',$3,true),set_config('adtr.account_use_attempt',$4,true)`, t.ID, q.Owner(), strconv.FormatInt(q.Fence(), 10), strconv.Itoa(q.Attempt())); err != nil {
		return err
	}
	return quiesceAccountUseTx(ctx, tx, t, u, "executor_returned")
}

func quiesceAccountUseTx(ctx context.Context, tx pgx.Tx, t tasks.Task, u accountUseRow, reason string) error {
	tag, err := tx.Exec(ctx, `UPDATE adtr.domain_account_task_uses SET state='quiesced',quiesced_at=clock_timestamp(),quiescence_reason=$4 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state=$5`, t.TenantID, t.DomainID, t.ID, reason, u.state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAccountUseEvidence
	}
	tag, err = tx.Exec(ctx, `DELETE FROM adtr.operation_account_dependencies WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3 AND consumer_kind=$4 AND object_id=$5 AND account_credential_revision=$6 AND connection_credential_generation=$7`, t.TenantID, t.DomainID, u.payload.AccountID, AccountKindName, t.ID, u.payload.AccountCredentialRevision, u.payload.ConnectionCredentialGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAccountUseEvidence
	}
	return nil
}

// ReconcileReservedAccountUses retires only never-opened reservations of terminal
// tasks. It is independent maintenance, never part of Claim/RecoverExpired or an
// account mutation transaction. Opened uses are intentionally quarantined after
// process loss; only an in-process executor-return witness may acknowledge them.
func (s *Store) ReconcileReservedAccountUses(ctx context.Context, cfg *pgx.ConnConfig, limit int) (int, error) {
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
	rows, err := tx.Query(ctx, `SELECT u.tenant_id,u.task_id FROM adtr.domain_account_task_uses u JOIN adtr.tasks t ON t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id WHERE u.state='reserved' AND t.state IN ('succeeded','failed','partial_failed','dead_letter','cancelled') ORDER BY u.reserved_at,u.task_id LIMIT $1`, limit)
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
		changed, err := reconcileReservedAccountUse(ctx, conn, c.tenant, c.id)
		if err != nil {
			return total, err
		}
		if changed {
			total++
		}
	}
	return total, nil
}

func reconcileReservedAccountUse(ctx context.Context, conn *pgx.Conn, tenant, id string) (bool, error) {
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
	t, u, err := lockAccountUseTx(ctx, tx, tenant, id)
	if err != nil {
		return false, err
	}
	if !t.State.Terminal() || u.state != "reserved" {
		return false, tx.Commit(ctx)
	}
	if u.owner != nil || u.fence != nil || u.attempt != nil {
		return false, errAccountUseEvidence
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('adtr.account_use_protocol','account_use_reserved_cleanup_v1',true),set_config('adtr.account_use_task',$1,true)`, id); err != nil {
		return false, err
	}
	if err = quiesceAccountUseTx(ctx, tx, t, u, "never_opened_terminal"); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
