package credentialuse

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/tasks"
)

type GovernanceOperation string

const (
	ManageRoles   GovernanceOperation = "manage_roles"
	ManageTenant  GovernanceOperation = "manage_tenant"
	ResetPassword GovernanceOperation = "reset_password"
	MutateAccount GovernanceOperation = "mutate_account"
	GrantUse      GovernanceOperation = "grant"
	RevokeUse     GovernanceOperation = "revoke"
)

type SelfOperation string

const (
	ChangePassword SelfOperation = "self_password"
	BeginMFA       SelfOperation = "self_mfa_begin"
	ConfirmMFA     SelfOperation = "self_mfa_confirm"
	DisableMFA     SelfOperation = "self_mfa_disable"
)

// SetGovernanceContext is called only after fresh proof, with the authenticated
// server principal. The marker conveys provenance, never an administrator bit.
// PostgreSQL independently checks current actor/tenant/domain authority.
func SetGovernanceContext(ctx context.Context, tx pgx.Tx, p tasks.Principal, op GovernanceOperation) error {
	switch op {
	case ManageRoles, ManageTenant, ResetPassword, MutateAccount, GrantUse, RevokeUse:
	default:
		return problem(403, "credential_use_governance_required")
	}
	return setContext(ctx, tx, p, string(op))
}

// SetSelfContext preserves each core-auth route's existing proof semantics. In
// particular, forced/expired users may change their own password.
func SetSelfContext(ctx context.Context, tx pgx.Tx, p tasks.Principal, op SelfOperation) error {
	switch op {
	case ChangePassword, BeginMFA, ConfirmMFA, DisableMFA:
	default:
		return problem(403, "credential_use_governance_required")
	}
	return setContext(ctx, tx, p, string(op))
}
func setContext(ctx context.Context, tx pgx.Tx, p tasks.Principal, op string) error {
	if p.TenantID == "" || p.ActorID <= 0 {
		return problem(403, "credential_use_governance_required")
	}
	_, err := tx.Exec(ctx, `SELECT set_config('adtr.credential_use_protocol','credential_governance_v1',true),set_config('adtr.credential_use_tenant',$1,true),set_config('adtr.credential_use_actor',$2,true),set_config('adtr.credential_use_operation',$3,true),set_config('adtr.credential_use_scope','',true)`, p.TenantID, strconv.FormatInt(p.ActorID, 10), op)
	return err
}
func IsGovernanceError(err error) bool {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "42501" && p.ConstraintName == "credential_use_governance" {
		return true
	}
	var e *Error
	return errors.As(err, &e) && e.Code == "credential_use_governance_required"
}
