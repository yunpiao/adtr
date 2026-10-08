package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Call only after the route's existing proof checks. The database independently
// validates this versioned provenance; it is never a client-controlled role bit.
func setCredentialGovernance(ctx context.Context, tx pgx.Tx, actor User, operation credentialuse.GovernanceOperation) error {
	if err := setAuditMetadata(ctx, tx, actor); err != nil {
		return err
	}
	return credentialuse.SetGovernanceContext(ctx, tx, tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}, operation)
}
func setCredentialSelfContext(ctx context.Context, tx pgx.Tx, actor User, operation credentialuse.SelfOperation) error {
	if err := setAuditMetadata(ctx, tx, actor); err != nil {
		return err
	}
	return credentialuse.SetSelfContext(ctx, tx, tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}, operation)
}
