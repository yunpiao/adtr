package domains

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// DirectoryReceiptTx recovers only the caller's own immutable intent. It does
// not retry execution or require the deployment read switch to remain enabled.
func (s *Store) DirectoryReceiptTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, domainID, key string) (tasks.Task, error) {
	if !ValidID(domainID) || !ValidKey(key) {
		return tasks.Task{}, problem(422, "invalid_input")
	}
	if engine == nil {
		return tasks.Task{}, problem(503, "directory_unavailable")
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT task_id FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND kind=$4 AND idempotency_key=$5`, p.TenantID, domainID, p.ActorID, DirectoryKindName, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.Task{}, problem(404, "not_found")
	}
	if err != nil {
		return tasks.Task{}, err
	}
	detail, err := engine.DetailTx(ctx, tx, p, id)
	if err != nil {
		return tasks.Task{}, err
	}
	if detail.Task.Kind != DirectoryKindName || detail.Task.ActorID != p.ActorID || detail.Task.DomainID != domainID {
		return tasks.Task{}, problem(404, "not_found")
	}
	return detail.Task, nil
}

// Cancellation records acceptance, never proof that credential use stopped.
// The engine's original return witness still owns opened-use acknowledgement.
func (s *Store) CancelDirectoryTx(ctx context.Context, tx pgx.Tx, engine *tasks.Engine, p tasks.Principal, id string, allowed []string) (tasks.Task, error) {
	if !ValidID(id) {
		return tasks.Task{}, problem(422, "invalid_input")
	}
	if engine == nil {
		return tasks.Task{}, problem(503, "directory_unavailable")
	}
	detail, err := engine.DetailTx(ctx, tx, p, id)
	if err != nil {
		return tasks.Task{}, err
	}
	before := detail.Task
	if before.Kind != DirectoryKindName || !slices.Contains(allowed, before.DomainID) {
		return tasks.Task{}, problem(404, "not_found")
	}
	out, err := engine.CancelTx(ctx, tx, p, id)
	if err != nil {
		return tasks.Task{}, err
	}
	if before.State != tasks.CancelRequested && before.State != tasks.Cancelled {
		var revision, generation int64
		if err = tx.QueryRow(ctx, `SELECT connection_revision,credential_revision FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id=$2 FOR UPDATE`, p.TenantID, before.DomainID).Scan(&revision, &generation); err != nil {
			return tasks.Task{}, err
		}
		if err = audit(ctx, tx, p, before.DomainID, "domain_directory_cancel", revision, revision, generation, id, "cancel_requested"); err != nil {
			return tasks.Task{}, err
		}
	}
	return out, nil
}
