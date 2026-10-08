//go:build integration

package credentialuse_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
)

const directoryRole = "directory_role_000000001"

// Ordinary directory-purpose behavior runs against the complete current
// production migration; the dedicated historical-fragment test stays separate.
func newDirectoryFragmentFixture(t *testing.T) *governanceFixture {
	t.Helper()
	f := newGovernanceFixture(t)
	seedDirectoryRole(t, f)
	return f
}

func seedDirectoryRole(t *testing.T, f *governanceFixture) {
	t.Helper()
	f.exec(t, f.conn, `INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,'Directory reader')`, cuTenant, directoryRole)
	f.exec(t, f.conn, `INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT $1,$2,mark,true,mark<>'domains' FROM unnest(ARRAY['domains','directory_assets','tasks']) mark`, cuTenant, directoryRole)
	f.exec(t, f.conn, `INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES($1,$2,'role-group')`, cuTenant, directoryRole)
	var id int64
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,password_updated_at,mfa_secret)
 VALUES($1,'directory-member','synthetic-hash','viewer',$2,false,clock_timestamp(),'synthetic-mfa') RETURNING id`, cuTenant, directoryRole).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.users["directory-member"] = id
}

// The historical fragment itself must still be atomic and preserve existing
// grants/history/version, independently of the integrating migration.
func TestDirectoryPurposeExtensionFragmentPreservesHistoryAndVersion(t *testing.T) {
	f := newGovernanceFixtureAtVersion(t, 14)
	var version int
	if err := f.conn.QueryRow(f.ctx, `SELECT version FROM adtr.schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	tx := f.begin(t)
	f.exec(t, tx, auth.DirectoryPermissionMarks)
	f.exec(t, tx, credentialuse.DirectoryPurposeSchema)
	var unchanged bool
	if err := tx.QueryRow(f.ctx, `SELECT (SELECT version=$1 FROM adtr.schema_version)
 AND (SELECT count(*)=1 AND bool_and(purpose='domain.connection_test' AND allowed AND grant_revision::text=$2) FROM adtr.operation_account_use_grants)
 AND (SELECT count(*)=1 FROM adtr.operation_account_use_mutations)
 AND (SELECT count(*)=1 FROM adtr.operation_account_use_audit)`, version, f.initial.GrantRevision).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("extension changed grants, history, or migration version", err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func directoryInput(role, key string) credentialuse.Input {
	return credentialuse.Input{AccountID: cuAccount, RoleID: role, Purpose: credentialuse.DirectoryPurpose,
		ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: key}
}

func (f *governanceFixture) directoryGrant(t *testing.T, role, key string) credentialuse.Receipt {
	t.Helper()
	tx := f.begin(t)
	f.context(t, tx, "admin", credentialuse.GrantUse)
	r, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", directoryInput(role, key), []string{"domain-one"}, credentialuse.DirectoryPurpose)
	if err != nil {
		t.Fatal("create explicit directory grant", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

func requireCredentialError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var failure *credentialuse.Error
	if !errors.As(err, &failure) || failure.Status != status || failure.Code != code {
		t.Fatalf("wanted %d %s, got %v", status, code, err)
	}
}

func TestDirectoryPurposeExtensionFragmentEligibilityAndIsolation(t *testing.T) {
	f := newDirectoryFragmentFixture(t)
	s := credentialuse.New()
	t.Run("default-deny-including-admin-and-existing-grantee", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		for _, who := range []struct{ name, role string }{{"admin", "platform_admin"}, {"member", cuRole}, {"directory-member", directoryRole}} {
			got, err := s.EffectiveForPurposeTx(f.ctx, tx, f.principal(who.name), who.role, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
			if err != nil || got.ExplicitlyGranted || got.Eligible || got.GrantRevision != "0" || got.Purpose != credentialuse.DirectoryPurpose || got.ConsumerEnabled != credentialuse.DirectoryConsumerEnabled {
				t.Fatal("directory authority was implied", got, err)
			}
		}
		old, err := s.EffectiveTx(f.ctx, tx, f.principal("member"), cuRole, cuAccount, []string{"domain-one"})
		if err != nil || !old.Eligible || old.GrantRevision != f.initial.GrantRevision {
			t.Fatal("legacy eligibility changed", old, err)
		}
		roles, err := s.RolesForPurposeTx(f.ctx, tx, cuTenant, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, r := range roles.Roles {
			if r.RoleID == directoryRole {
				found = true
			}
		}
		if !found || roles.ConsumerEnabled != credentialuse.DirectoryConsumerEnabled {
			t.Fatal("directory-only role not eligible", roles)
		}
		oldRoles, err := s.RolesTx(f.ctx, tx, cuTenant, cuAccount, []string{"domain-one"})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range oldRoles.Roles {
			if r.RoleID == directoryRole {
				t.Fatal("directory-only role eligible for legacy connection test")
			}
		}
		for _, purpose := range []any{"unknown", "", nil} {
			var eligible bool
			if err = tx.QueryRow(f.ctx, `SELECT adtr.credential_use_role_eligible_for_purpose($1,'domain-one','platform_admin',$2)`, cuTenant, purpose).Scan(&eligible); err != nil || eligible {
				t.Fatal("unknown purpose not denied", err)
			}
		}
	})
	r := f.directoryGrant(t, directoryRole, "directory-allow")
	if r.Purpose != credentialuse.DirectoryPurpose || !r.Allowed {
		t.Fatal("wrong purpose receipt", r)
	}
	t.Run("read-lists-and-receipts-stay-purpose-bound", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		got, err := s.EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		if err != nil || !got.Eligible || !got.ExplicitlyGranted {
			t.Fatal(got, err)
		}
		legacy, err := s.EffectiveTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"})
		if err != nil || legacy.Eligible || legacy.ExplicitlyGranted {
			t.Fatal("directory grant broadened connection-test authority", legacy, err)
		}
		for _, purpose := range []string{credentialuse.Purpose, credentialuse.DirectoryPurpose} {
			list, err := s.GrantsForPurposeTx(f.ctx, tx, cuTenant, cuAccount, []string{"domain-one"}, purpose)
			if err != nil || len(list.Grants) != 1 || list.Grants[0].Purpose != purpose {
				t.Fatal("mixed purpose grant list", list, err)
			}
		}
		recovered, err := s.ReceiptForPurposeTx(f.ctx, tx, f.principal("admin"), "directory-allow", []string{"domain-one"}, credentialuse.DirectoryPurpose)
		if err != nil || recovered.GrantRevision != r.GrantRevision || recovered.Purpose != credentialuse.DirectoryPurpose {
			t.Fatal(recovered, err)
		}
		_, err = s.ReceiptTx(f.ctx, tx, f.principal("admin"), "directory-allow", []string{"domain-one"})
		requireCredentialError(t, err, 404, "not_found")
		_, err = s.EffectiveForPurposeTx(f.ctx, tx, f.principal("member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		requireCredentialError(t, err, 404, "not_found")
		_, err = s.EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-two"}, credentialuse.DirectoryPurpose)
		requireCredentialError(t, err, 404, "not_found")
		_, err = s.EffectiveForPurposeTx(f.ctx, tx, f.principal("foreign-admin"), "platform_admin", cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		requireCredentialError(t, err, 404, "not_found")
	})
	t.Run("cleanup-account-list-filters-purpose", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		f.context(t, tx, "admin", credentialuse.RevokeUse)
		f.exec(t, tx, `UPDATE adtr.operation_account_use_grants SET allowed=false,updated_by=$1 WHERE purpose='domain.connection_test'`, f.users["admin"])
		filter := credentialuse.Filter{PageIdx: 1, PageSize: 10}
		old, err := s.AccountsTx(f.ctx, tx, cuTenant, []string{"domain-one"}, filter)
		if err != nil || old.Page.Total != 0 || len(old.List) != 0 {
			t.Fatal("legacy cleanup included directory-only account", old, err)
		}
		directory, err := s.AccountsForPurposeTx(f.ctx, tx, cuTenant, []string{"domain-one"}, filter, credentialuse.DirectoryPurpose)
		if err != nil || directory.Page.Total != 1 || len(directory.List) != 1 || directory.List[0].AccountID != cuAccount || directory.ConsumerEnabled != credentialuse.DirectoryConsumerEnabled {
			t.Fatal("directory cleanup lost purpose-specific account", directory, err)
		}
	})
	t.Run("idempotency-separates-purpose-and-does-not-reconsume-proof", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		replay, err := s.ReplayForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", directoryInput(directoryRole, "directory-allow"), []string{"domain-one"}, credentialuse.DirectoryPurpose)
		if err != nil || replay == nil || !replay.Replayed || replay.GrantRevision != r.GrantRevision {
			t.Fatal(replay, err)
		}
		collision := directoryInput(directoryRole, "initial-allow")
		_, err = s.ReplayForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", collision, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		requireCredentialError(t, err, 409, "idempotency_conflict")
		collision = directoryInput(directoryRole, "directory-allow")
		collision.Purpose = credentialuse.Purpose
		_, err = s.ReplayTx(f.ctx, tx, f.principal("admin"), "/grant", collision, []string{"domain-one"})
		requireCredentialError(t, err, 409, "idempotency_conflict")
	})
}

func TestDirectoryPurposeExtensionFragmentGovernanceAndEpochs(t *testing.T) {
	f := newDirectoryFragmentFixture(t)
	receipt := f.directoryGrant(t, directoryRole, "governed-directory")
	const insert = `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by)
 VALUES($1,'domain-one',$2,'domain.directory_read','platform_admin',1,true,$3)`
	for _, actor := range []string{"old-binary", "directory-member", "foreign-admin", "disabled-admin", "forced-admin", "expired-admin", "no-mfa-admin"} {
		t.Run("grant-requires-scoped-admin-"+actor, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			id := f.users["admin"]
			if actor != "old-binary" {
				f.context(t, tx, actor, credentialuse.GrantUse)
				id = f.users[actor]
			}
			_, err := tx.Exec(f.ctx, insert, cuTenant, cuAccount, id)
			requireGovernanceDenied(t, err)
		})
	}
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{"permissions", `UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, []any{cuTenant, directoryRole}},
		{"membership", `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, []any{cuTenant, directoryRole}},
		{"group-domain", `DELETE FROM adtr.resource_group_members WHERE tenant_id=$1 AND group_id='role-group'`, []any{cuTenant}},
		{"tenant", `UPDATE adtr.resource_tenant_config SET max_ad_count=0 WHERE tenant_id=$1`, []any{cuTenant}},
		{"user-reset", `UPDATE adtr.users SET password_hash='synthetic-change',must_change=true WHERE id=$1`, []any{f.users["directory-member"]}},
		{"new-member", `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id) VALUES($1,'new-directory-member','synthetic','viewer',$2)`, []any{cuTenant, directoryRole}},
	} {
		t.Run("unguarded-"+tc.name, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			_, err := tx.Exec(f.ctx, tc.sql, tc.args...)
			requireGovernanceDenied(t, err)
		})
	}
	t.Run("dormant-grant-still-protects-restoration", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, cuTenant, directoryRole)
		clearGovernanceContext(t, f, tx)
		_, err := tx.Exec(f.ctx, `UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, cuTenant, directoryRole)
		requireGovernanceDenied(t, err)
	})
	for _, mark := range []string{"directory_assets", "domains", "tasks"} {
		t.Run("permission-removal-restoration-epochs-"+mark, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			f.context(t, tx, "admin", credentialuse.ManageRoles)
			var before, after, restored int64
			if err := tx.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["directory-member"]).Scan(&before); err != nil {
				t.Fatal(err)
			}
			f.exec(t, tx, `UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark=$3`, cuTenant, directoryRole, mark)
			got, err := credentialuse.New().EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
			if err != nil || got.Eligible || !got.ExplicitlyGranted || got.GrantRevision != receipt.GrantRevision {
				t.Fatal("permission removal did not make grant dormant", got, err)
			}
			if err = tx.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["directory-member"]).Scan(&after); err != nil || after <= before {
				t.Fatal("permission removal did not invalidate epoch", err)
			}
			f.exec(t, tx, `UPDATE adtr.access_permissions SET readable=true,writeable=(mark<>'domains') WHERE tenant_id=$1 AND role_id=$2 AND mark=$3`, cuTenant, directoryRole, mark)
			got, err = credentialuse.New().EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
			if err != nil || !got.Eligible {
				t.Fatal("authorized restoration did not restore eligibility", got, err)
			}
			if err = tx.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["directory-member"]).Scan(&restored); err != nil || restored <= after {
				t.Fatal("permission restoration did not invalidate epoch", err)
			}
		})
	}
	t.Run("final-admin-scope-stays-protected", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		f.context(t, tx, "admin", credentialuse.RevokeUse)
		f.exec(t, tx, `UPDATE adtr.operation_account_use_grants SET allowed=false,updated_by=$1 WHERE purpose='domain.connection_test'`, f.users["admin"])
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id='platform_admin'`, cuTenant)
		requireGovernanceDenied(t, tx.Commit(f.ctx))
	})
	t.Run("expiry-denies-use-but-preserves-revoke", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		f.context(t, tx, "admin", credentialuse.ManageTenant)
		f.exec(t, tx, `UPDATE adtr.resource_tenant_config SET expire_time=0 WHERE tenant_id=$1`, cuTenant)
		got, err := credentialuse.New().EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		if err != nil || got.Eligible || !got.ExplicitlyGranted {
			t.Fatal(got, err)
		}
		f.context(t, tx, "admin", credentialuse.RevokeUse)
		in := directoryInput(directoryRole, "expired-directory-revoke")
		in.ExpectedGrantRevision = receipt.GrantRevision
		out, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/revoke", in, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		if err != nil || out.Allowed || out.Purpose != credentialuse.DirectoryPurpose {
			t.Fatal(out, err)
		}
	})
}

func TestDirectoryPurposeExtensionFragmentRevokeAndPairInvalidation(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		name := "replace-pair"
		if deleting {
			name = "delete-account"
		}
		t.Run(name, func(t *testing.T) {
			f := newDirectoryFragmentFixture(t)
			tx := f.begin(t)
			f.context(t, tx, "admin", credentialuse.ManageRoles)
			f.exec(t, tx, `INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'directory_assets',true,true)`, cuTenant, cuRole)
			if err := tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			receipt := f.directoryGrant(t, cuRole, "same-role-directory")
			t.Run("purpose-specific-revoke-does-not-touch-legacy", func(t *testing.T) {
				tx := f.begin(t)
				defer tx.Rollback(context.Background())
				f.context(t, tx, "admin", credentialuse.RevokeUse)
				in := directoryInput(cuRole, "only-directory-revoke")
				in.ExpectedGrantRevision = receipt.GrantRevision
				revoked, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/revoke", in, []string{"domain-one"}, credentialuse.DirectoryPurpose)
				if err != nil || revoked.Allowed || revoked.GrantRevision == receipt.GrantRevision {
					t.Fatal(revoked, err)
				}
				old, err := credentialuse.New().EffectiveTx(f.ctx, tx, f.principal("member"), cuRole, cuAccount, []string{"domain-one"})
				if err != nil || !old.Eligible || old.GrantRevision != f.initial.GrantRevision {
					t.Fatal("directory revoke changed connection-test grant", old, err)
				}
				in.IdempotencyKey = "stale-directory-revoke"
				_, err = credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/revoke", in, []string{"domain-one"}, credentialuse.DirectoryPurpose)
				requireCredentialError(t, err, 409, "revision_conflict")
			})
			var before int64
			if err := f.conn.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["member"]).Scan(&before); err != nil {
				t.Fatal(err)
			}
			tx = f.begin(t)
			f.context(t, tx, "member", credentialuse.MutateAccount)
			event := "credential_use_pair_replaced"
			if deleting {
				event = "credential_use_account_deleted"
				f.exec(t, tx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1,deleted_at=clock_timestamp() WHERE account_id=$1`, cuAccount)
				f.exec(t, tx, `DELETE FROM adtr.operation_account_credentials WHERE account_id=$1`, cuAccount)
				f.exec(t, tx, `DELETE FROM adtr.domain_dependencies WHERE module='operation_accounts' AND object_id=$1`, cuAccount)
			} else {
				f.exec(t, tx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE account_id=$1`, cuAccount)
				f.exec(t, tx, `UPDATE adtr.operation_account_credentials SET credential_revision=credential_revision+1,ciphertext=decode(repeat('cd',32),'hex') WHERE account_id=$1`, cuAccount)
			}
			if err := tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			var revoked, epoch, audited bool
			if err := f.conn.QueryRow(f.ctx, `SELECT
    (SELECT count(*)=2 AND bool_and(NOT allowed AND account_credential_revision=1 AND grant_revision>$1::bigint AND updated_by=$2) FROM adtr.operation_account_use_grants WHERE account_id=$3),
    (SELECT authorization_version>$4 FROM adtr.users WHERE id=$2),
    (SELECT count(*)=2 AND count(DISTINCT purpose)=2 AND bool_and(actor_id=$2) FROM adtr.operation_account_use_audit WHERE account_id=$3 AND action=$5)`, receipt.GrantRevision, f.users["member"], cuAccount, before, event).Scan(&revoked, &epoch, &audited); err != nil || !revoked || !epoch || !audited {
				t.Fatal("pair mutation lost purpose invalidation, epoch or audit", revoked, epoch, audited, err)
			}
		})
	}
}

func TestDirectoryPurposeExtensionFragmentFailClosedSQL(t *testing.T) {
	f := newDirectoryFragmentFixture(t)
	var expiredID int64
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,password_updated_at,mfa_secret) VALUES($1,'directory-expired','synthetic','viewer',$2,false,clock_timestamp()-interval '100 days','synthetic-mfa') RETURNING id`, cuTenant, directoryRole).Scan(&expiredID); err != nil {
		t.Fatal(err)
	}
	f.users["directory-expired"] = expiredID
	r := f.directoryGrant(t, directoryRole, "exact-directory")
	t.Run("expired-password-denies-current-use", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		got, err := credentialuse.New().EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-expired"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
		if err != nil || got.Eligible || !got.ExplicitlyGranted {
			t.Fatal("expired password retained effective use", got, err)
		}
	})
	for _, tc := range []struct {
		name, query string
		args        []any
		state       string
	}{
		{"unknown-grant-purpose", `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by) VALUES($1,'domain-one',$2,'domain.unknown','platform_admin',1,true,$3)`, []any{cuTenant, cuAccount, f.users["admin"]}, "42501"},
		{"stale-account-pair", `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by) VALUES($1,'domain-one',$2,'domain.directory_read','platform_admin',2,true,$3)`, []any{cuTenant, cuAccount, f.users["admin"]}, "42501"},
		{"unknown-audit-purpose", `INSERT INTO adtr.operation_account_use_audit(tenant_id,actor_id,domain_id,account_id,role_id,purpose,action,old_grant_revision,new_grant_revision,account_credential_revision,allowed,result) VALUES($1,$2,'domain-one',$3,$4,'domain.unknown','credential_use_grant',0,1,1,true,'saved')`, []any{cuTenant, f.users["admin"], cuAccount, directoryRole}, "23514"},
		{"receipt-cross-purpose-revision", `INSERT INTO adtr.operation_account_use_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed) VALUES($1,$2,'forged-purpose','grant',repeat('a',64),'domain-one',$3,$4,'domain.connection_test',$5,1,true)`, []any{cuTenant, f.users["admin"], cuAccount, directoryRole, r.GrantRevision}, "42501"},
		{"purpose-is-immutable", `UPDATE adtr.operation_account_use_grants SET purpose='domain.connection_test' WHERE purpose='domain.directory_read'`, nil, "42501"},
		{"directory-history-is-immutable", `DELETE FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read'`, nil, "P0001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			f.context(t, tx, "admin", credentialuse.GrantUse)
			_, err := tx.Exec(f.ctx, tc.query, tc.args...)
			requireSQLState(t, err, tc.state)
		})
	}
	for _, field := range []string{"disabled=true", "must_change=true"} {
		t.Run("current-user-"+field, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			if field != "disabled=true" {
				f.context(t, tx, "admin", credentialuse.ResetPassword)
				f.exec(t, tx, `UPDATE adtr.users SET password_hash='synthetic-new-hash',must_change=true WHERE id=$1`, f.users["directory-member"])
			} else {
				f.exec(t, tx, `UPDATE adtr.users SET disabled=true WHERE id=$1`, f.users["directory-member"])
			}
			got, err := credentialuse.New().EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
			if err != nil || got.Eligible || !got.ExplicitlyGranted {
				t.Fatal("stale user security state retained effective use", got, err)
			}
		})
	}
}

func TestDirectoryPurposeExtensionFragmentRoleCascadeKeepsHistory(t *testing.T) {
	f := newDirectoryFragmentFixture(t)
	r := f.directoryGrant(t, directoryRole, "deleted-directory-role")
	tx := f.begin(t)
	f.context(t, tx, "admin", credentialuse.ManageRoles)
	f.exec(t, tx, `UPDATE adtr.users SET role_id='' WHERE id=$1`, f.users["directory-member"])
	f.exec(t, tx, `DELETE FROM adtr.access_roles WHERE tenant_id=$1 AND id=$2`, cuTenant, directoryRole)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var kept bool
	if err := f.conn.QueryRow(f.ctx, `SELECT
 NOT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE role_id=$1)
 AND (SELECT count(*)=1 FROM adtr.operation_account_use_mutations WHERE role_id=$1 AND purpose='domain.directory_read')
 AND (SELECT count(*)=1 FROM adtr.operation_account_use_audit WHERE role_id=$1 AND purpose='domain.directory_read' AND action='credential_use_role_deleted' AND actor_id=$2)`, directoryRole, f.users["admin"]).Scan(&kept); err != nil || !kept {
		t.Fatal("directory role cascade lost immutable purpose history", err)
	}
	tx = f.begin(t)
	defer tx.Rollback(context.Background())
	recovered, err := credentialuse.New().ReceiptForPurposeTx(f.ctx, tx, f.principal("admin"), "deleted-directory-role", []string{"domain-one"}, credentialuse.DirectoryPurpose)
	if err != nil || recovered.GrantRevision != r.GrantRevision || recovered.CurrentGrantRevision != "0" || recovered.CurrentAllowed || recovered.Purpose != credentialuse.DirectoryPurpose {
		t.Fatal("deleted directory role receipt no longer recovers", recovered, err)
	}
}

// Observe an actual PostgreSQL lock wait, not merely two scheduled goroutines.
func waitDirectoryFragmentWriter(t *testing.T, f *governanceFixture, holder pgx.Tx, waiter *pgx.Conn, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := holder.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, waiter.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal("observe directory writer lock", err)
		}
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatal("directory writer exited before concurrent admission", err)
		case <-ctx.Done():
			t.Fatal("directory writer never waited on admission lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func startDirectoryFragmentMutation(f *governanceFixture, conn *pgx.Conn, path string, in credentialuse.Input) <-chan error {
	done := make(chan error, 1)
	go func() {
		// Report only after the losing transaction has released every lock.
		done <- func() error {
			tx, err := conn.Begin(f.ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, cuTenant); err != nil {
				return err
			}
			op := credentialuse.GrantUse
			if path == "/revoke" {
				op = credentialuse.RevokeUse
			}
			if err = credentialuse.SetGovernanceContext(f.ctx, tx, f.principal("admin"), op); err != nil {
				return err
			}
			_, err = credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), path, in, []string{"domain-one"}, credentialuse.DirectoryPurpose)
			return err
		}()
	}()
	return done
}

func TestDirectoryPurposeExtensionFragmentConcurrentCASAndRegrant(t *testing.T) {
	f := newDirectoryFragmentFixture(t)
	initial := f.directoryGrant(t, directoryRole, "directory-cas-initial")
	in := directoryInput(directoryRole, "directory-cas-revoke")
	in.ExpectedGrantRevision = initial.GrantRevision
	first := f.begin(t)
	defer first.Rollback(context.Background())
	f.exec(t, first, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, cuTenant)
	f.context(t, first, "admin", credentialuse.RevokeUse)
	revoked, err := credentialuse.New().MutateForPurposeTx(f.ctx, first, f.principal("admin"), "/revoke", in, []string{"domain-one"}, credentialuse.DirectoryPurpose)
	if err != nil {
		t.Fatal(err)
	}
	second := f.secondConnection(t)
	stale := in
	stale.IdempotencyKey = "directory-cas-loser"
	done := startDirectoryFragmentMutation(f, second, "/revoke", stale)
	waitDirectoryFragmentWriter(t, f, first, second, done)
	if err = first.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		requireCredentialError(t, err, 409, "revision_conflict")
	case <-f.ctx.Done():
		t.Fatal("concurrent directory writer failed to finish")
	}
	tx := f.begin(t)
	defer tx.Rollback(context.Background())
	f.context(t, tx, "admin", credentialuse.GrantUse)
	in.ExpectedGrantRevision = revoked.GrantRevision
	in.IdempotencyKey = "directory-cas-regrant"
	regranted, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", in, []string{"domain-one"}, credentialuse.DirectoryPurpose)
	if err != nil {
		t.Fatal(err)
	}
	oldRev, _ := strconv.ParseInt(initial.GrantRevision, 10, 64)
	revokedRev, _ := strconv.ParseInt(revoked.GrantRevision, 10, 64)
	newRev, _ := strconv.ParseInt(regranted.GrantRevision, 10, 64)
	if !(oldRev < revokedRev && revokedRev < newRev) {
		t.Fatal("directory revoke/regrant reused an authorization incarnation")
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var exact bool
	if err = f.conn.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=3 FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read')
 AND (SELECT count(*)=3 FROM adtr.operation_account_use_audit WHERE purpose='domain.directory_read')
 AND NOT EXISTS(SELECT FROM adtr.operation_account_use_mutations WHERE idempotency_key='directory-cas-loser')
 AND (SELECT allowed AND grant_revision::text=$1 FROM adtr.operation_account_use_grants WHERE purpose='domain.connection_test')`, f.initial.GrantRevision).Scan(&exact); err != nil || !exact {
		t.Fatal("directory CAS loser left history or changed legacy grant", err)
	}
}

func TestDirectoryPurposeExtensionFragmentPairReplacementWinsConcurrentGrant(t *testing.T) {
	f := newDirectoryFragmentFixture(t)
	initial := f.directoryGrant(t, directoryRole, "directory-pair-race-initial")
	first := f.begin(t)
	defer first.Rollback(context.Background())
	f.exec(t, first, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, cuTenant)
	f.context(t, first, "member", credentialuse.MutateAccount)
	f.exec(t, first, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE account_id=$1`, cuAccount)
	f.exec(t, first, `UPDATE adtr.operation_account_credentials SET credential_revision=credential_revision+1,ciphertext=decode(repeat('cd',32),'hex') WHERE account_id=$1`, cuAccount)
	second := f.secondConnection(t)
	stale := directoryInput(directoryRole, "directory-stale-pair-grant")
	stale.ExpectedGrantRevision = initial.GrantRevision
	done := startDirectoryFragmentMutation(f, second, "/grant", stale)
	waitDirectoryFragmentWriter(t, f, first, second, done)
	if err := first.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		requireCredentialError(t, err, 409, "revision_conflict")
	case <-f.ctx.Done():
		t.Fatal("grant racing pair replacement failed to finish")
	}
	var exact bool
	if err := f.conn.QueryRow(f.ctx, `SELECT
 (SELECT revision=2 AND credential_revision=2 FROM adtr.operation_accounts WHERE account_id=$1)
 AND (SELECT count(*)=2 AND bool_and(NOT allowed AND account_credential_revision=1) FROM adtr.operation_account_use_grants WHERE account_id=$1)
 AND (SELECT count(*)=2 AND count(DISTINCT purpose)=2 FROM adtr.operation_account_use_audit WHERE account_id=$1 AND action='credential_use_pair_replaced')
 AND NOT EXISTS(SELECT FROM adtr.operation_account_use_mutations WHERE idempotency_key='directory-stale-pair-grant')`, cuAccount).Scan(&exact); err != nil || !exact {
		t.Fatal("concurrent stale grant survived pair invalidation or left history", err)
	}
}

func TestDirectoryPurposeExtensionFragmentLockInversionsRollBack(t *testing.T) {
	f := newDirectoryFragmentFixture(t)
	initial := f.directoryGrant(t, directoryRole, "directory-inversion-initial")
	for _, action := range []string{"directory-revoke", "pair-replacement", "directory-permission"} {
		t.Run(action, func(t *testing.T) {
			other := f.secondConnection(t)
			rowTx, err := other.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rowTx.Rollback(context.Background())
			f.exec(t, rowTx, `SELECT id FROM adtr.users WHERE id=$1 FOR UPDATE`, f.users["directory-member"])
			tenantTx := f.begin(t)
			defer tenantTx.Rollback(context.Background())
			f.exec(t, tenantTx, `SELECT adtr.task_auth_lock($1)`, cuTenant)
			f.exec(t, tenantTx, `SET LOCAL statement_timeout='1500ms'`)
			var query string
			var args []any
			switch action {
			case "directory-revoke":
				f.context(t, tenantTx, "admin", credentialuse.RevokeUse)
				query = `UPDATE adtr.operation_account_use_grants SET allowed=false,updated_by=$1 WHERE purpose='domain.directory_read'`
				args = []any{f.users["admin"]}
			case "pair-replacement":
				f.context(t, tenantTx, "member", credentialuse.MutateAccount)
				query = `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE account_id=$1`
				args = []any{cuAccount}
			case "directory-permission":
				f.context(t, tenantTx, "admin", credentialuse.ManageRoles)
				query = `UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`
				args = []any{cuTenant, directoryRole}
			}
			start := time.Now()
			_, err = tenantTx.Exec(f.ctx, query, args...)
			requireSQLState(t, err, "55P03")
			if time.Since(start) >= 1500*time.Millisecond {
				t.Fatal("directory epoch lock waited instead of failing NOWAIT")
			}
			if err = tenantTx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			var unchanged bool
			if err = f.conn.QueryRow(f.ctx, `SELECT
    (SELECT revision=1 AND credential_revision=1 FROM adtr.operation_accounts WHERE account_id=$1)
    AND (SELECT allowed AND grant_revision::text=$2 FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read')
    AND (SELECT readable AND writeable FROM adtr.access_permissions WHERE tenant_id=$3 AND role_id=$4 AND mark='directory_assets')
    AND (SELECT count(*)=1 FROM adtr.operation_account_use_audit WHERE purpose='domain.directory_read')
    AND (SELECT count(*)=1 FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read')`, cuAccount, initial.GrantRevision, cuTenant, directoryRole).Scan(&unchanged); err != nil || !unchanged {
				t.Fatal("failed directory lock inversion left partial state/history", err)
			}
		})
	}
	t.Run("governance-row-to-tenant", func(t *testing.T) {
		other := f.secondConnection(t)
		rowTx, err := other.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rowTx.Rollback(context.Background())
		f.exec(t, rowTx, `SELECT id FROM adtr.users WHERE id=$1 FOR UPDATE`, f.users["directory-member"])
		f.context(t, rowTx, "admin", credentialuse.ManageRoles)
		tenantTx := f.begin(t)
		defer tenantTx.Rollback(context.Background())
		f.exec(t, tenantTx, `SELECT adtr.task_auth_lock($1)`, cuTenant)
		f.exec(t, rowTx, `SET LOCAL statement_timeout='1500ms'`)
		start := time.Now()
		_, err = rowTx.Exec(f.ctx, `UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, cuTenant, directoryRole)
		requireSQLState(t, err, "40001")
		if time.Since(start) >= 1500*time.Millisecond {
			t.Fatal("directory governance row-to-tenant inversion waited")
		}
	})
}
