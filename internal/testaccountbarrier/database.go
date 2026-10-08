//go:build integration

package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Only u is locked. The task join matches a genuine admission without locking
// any upstream rows needed by the executor's later authorization checkpoints.
const lockSQL = `SELECT u.task_id,u.opener_owner,u.opener_fencing_token,u.opener_attempt,u.opened_at
 FROM adtr.domain_account_task_uses u JOIN adtr.tasks t ON t.task_id=u.task_id
 AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id AND t.actor_id=u.actor_id
 WHERE t.tenant_id=$1 AND t.actor_id=$2 AND t.domain_id=$3 AND u.account_id=$4
 AND t.idempotency_key=$5 AND t.kind='domain.account_connection_test' AND t.payload_version=1
 AND t.max_attempts=1 AND u.purpose='domain.connection_test' AND u.state='opened'
 AND u.opened_at IS NOT NULL AND u.opener_owner IS NOT NULL AND u.opener_owner<>''
 AND u.opener_fencing_token>0 AND u.opener_attempt=1 AND u.quiesced_at IS NULL AND u.quiescence_reason IS NULL
 LIMIT 2 FOR UPDATE OF u NOWAIT`

const snapshotSQL = `SELECT t.task_id,t.tenant_id,t.actor_id::text,t.domain_id,u.account_id,t.kind,t.state,u.state,
 u.opened_at IS NOT NULL,
 (u.opener_owner IS NOT NULL AND u.opener_owner<>'' AND u.opener_fencing_token>0 AND u.opener_attempt=1),
 (u.opener_owner IS NOT DISTINCT FROM $7 AND u.opener_fencing_token IS NOT DISTINCT FROM $8
 AND u.opener_attempt IS NOT DISTINCT FROM $9 AND u.opened_at IS NOT DISTINCT FROM $10),
 u.quiesced_at IS NOT NULL,COALESCE(u.quiescence_reason,''),COALESCE(di.code,''),COALESCE(di.successful_observation,false),
 a.revision::text,a.credential_revision::text,a.deleted_at IS NOT NULL,
 EXISTS(SELECT FROM adtr.operation_account_credentials ac WHERE ac.tenant_id=a.tenant_id AND ac.domain_id=a.domain_id AND ac.account_id=a.account_id),
 c.connection_revision::text,c.credential_revision::text,c.credential_mode,c.operation_account_id IS NOT NULL,
 COALESCE(c.operation_account_id=u.account_id AND c.operation_account_credential_revision=u.account_credential_revision,false),
 (SELECT count(*) FROM adtr.domain_credentials dc WHERE dc.tenant_id=u.tenant_id AND dc.domain_id=u.domain_id),
 (SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.connection_binding'),
 (SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.connection_binding'
 AND d.object_id=u.domain_id AND d.account_credential_revision=u.account_credential_revision AND d.connection_credential_generation=u.connection_credential_generation),
 (SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.account_connection_test'),
 (SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.account_connection_test'
 AND d.object_id=u.task_id AND d.account_credential_revision=u.account_credential_revision AND d.connection_credential_generation=u.connection_credential_generation),
 (SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id)
 FROM adtr.domain_account_task_uses u
 JOIN adtr.tasks t ON t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id AND t.actor_id=u.actor_id
 JOIN adtr.domain_connections c ON c.tenant_id=u.tenant_id AND c.domain_id=u.domain_id
 JOIN adtr.operation_accounts a ON a.tenant_id=u.tenant_id AND a.domain_id=u.domain_id AND a.account_id=u.account_id
 LEFT JOIN adtr.domain_diagnostics di ON di.tenant_id=u.tenant_id AND di.domain_id=u.domain_id AND di.task_id=u.task_id
 WHERE t.tenant_id=$1 AND t.actor_id=$2 AND t.domain_id=$3 AND u.account_id=$4 AND t.idempotency_key=$5
 AND t.task_id=$6 AND t.kind='domain.account_connection_test' AND t.payload_version=1 AND u.purpose='domain.connection_test'`

type openerEvidence struct {
	taskID, owner string
	fence         int64
	attempt       int
	openedAt      time.Time
}

func (openerEvidence) String() string   { return "[private opener evidence]" }
func (openerEvidence) GoString() string { return "[private opener evidence]" }

type database struct {
	lock, observer *pgx.Conn
	tx             pgx.Tx
	evidence       openerEvidence
}

func connectDatabase(ctx context.Context, dsn string) (*database, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("database_configuration")
	}
	return connectDatabaseConfig(ctx, cfg)
}

func connectDatabaseConfig(ctx context.Context, cfg *pgx.ConnConfig) (*database, error) {
	d := &database{}
	lockConfig := cfg.Copy()
	// Server-side backstop also releases an abandoned transaction if the helper
	// stops scheduling. No SQL runs on this connection after row acquisition.
	lockConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "60000"
	var err error
	d.lock, err = pgx.ConnectConfig(ctx, lockConfig)
	if err != nil {
		return nil, errors.New("database_connect")
	}
	d.observer, err = pgx.ConnectConfig(ctx, cfg.Copy())
	if err == nil {
		_, err = d.lock.Prepare(ctx, "barrier_lock", lockSQL)
	}
	if err == nil {
		_, err = d.observer.Prepare(ctx, "barrier_snapshot", snapshotSQL)
	}
	if err != nil {
		_ = d.close()
		return nil, errors.New("database_connect")
	}
	return d, nil
}

func (d *database) close() error {
	result := d.release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if d.lock != nil {
		if err := d.lock.Close(ctx); err != nil && result == nil {
			result = errors.New("connection_close_failed")
		}
	}
	if d.observer != nil {
		if err := d.observer.Close(ctx); err != nil && result == nil {
			result = errors.New("connection_close_failed")
		}
	}
	return result
}

func (d *database) release() error {
	if d.tx == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := d.tx.Rollback(ctx)
	d.tx = nil
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		// Closing the connection is the fallback release, not a lifecycle write.
		_ = d.lock.Close(ctx)
		return errors.New("rollback_failed")
	}
	return nil
}

func (d *database) tryLock(ctx context.Context, a armRecord) (bool, error) {
	var err error
	if d.tx == nil {
		d.tx, err = d.lock.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return false, errors.New("lock_query_failed")
		}
	}
	actor, _ := strconv.ParseInt(a.ActorID, 10, 64)
	rows, err := d.tx.Query(ctx, "barrier_lock", a.TenantID, actor, a.DomainID, a.AccountID, a.IdempotencyKey)
	if err != nil {
		return d.lockError(err)
	}
	var found []openerEvidence
	for rows.Next() {
		var e openerEvidence
		if err = rows.Scan(&e.taskID, &e.owner, &e.fence, &e.attempt, &e.openedAt); err != nil {
			rows.Close()
			return false, errors.New("lock_query_failed")
		}
		found = append(found, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d.lockError(err)
	}
	if len(found) == 0 {
		return false, nil
	}
	if len(found) != 1 {
		return false, errors.New("lock_match_count")
	}
	d.evidence = found[0]
	return true, nil
}

func (d *database) lockError(err error) (bool, error) {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "55P03" {
		if d.release() != nil {
			return false, errors.New("rollback_failed")
		}
		return false, nil
	}
	return false, errors.New("lock_query_failed")
}

func (d *database) observe(ctx context.Context, a armRecord) (snapshot, error) {
	var s snapshot
	e := d.evidence
	actor, _ := strconv.ParseInt(a.ActorID, 10, 64)
	err := d.observer.QueryRow(ctx, "barrier_snapshot", a.TenantID, actor, a.DomainID, a.AccountID, a.IdempotencyKey, e.taskID, e.owner, e.fence, e.attempt, e.openedAt).Scan(
		&s.TaskID, &s.TenantID, &s.ActorID, &s.DomainID, &s.AccountID, &s.TaskKind, &s.TaskState, &s.UseState,
		&s.OpenedAtPresent, &s.OpenerPresent, &s.OpenerUnchanged, &s.QuiescedAtPresent, &s.QuiescenceReason, &s.DiagnosticCode, &s.DiagnosticSuccessful,
		&s.AccountRevision, &s.AccountCredentialRevision, &s.AccountDeleted, &s.AccountCredentialPresent, &s.ConnectionRevision, &s.ConnectionCredentialGeneration,
		&s.CredentialSource, &s.AccountPointerPresent, &s.AccountPointerMatches, &s.CustomCredentialCount, &s.BindingDependencyCount, &s.ExactBindingDependencyCount,
		&s.TaskDependencyCount, &s.ExactTaskDependencyCount, &s.TotalDependencyCount)
	if err != nil {
		return snapshot{}, errors.New("observer_failed")
	}
	if !s.OpenerUnchanged {
		return snapshot{}, errors.New("opener_changed")
	}
	return s, nil
}
