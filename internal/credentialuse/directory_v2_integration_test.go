//go:build integration

package credentialuse_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/yunpiao/adtr/internal/credentialuse"
)

// This explicit component fixture installs only the new fragment on a real
// historical schema15. It does not claim full migration16/runtime integration.
func newDirectoryV2FragmentFixture(t *testing.T) *governanceFixture {
	t.Helper()
	f := newGovernanceFixtureAtVersion(t, 15)
	seedDirectoryRole(t, f)
	tx := f.begin(t)
	f.exec(t, tx, credentialuse.DirectoryV2PurposeSchema)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func directoryV2Input(role, key string) credentialuse.Input {
	in := directoryInput(role, key)
	in.Purpose = credentialuse.DirectoryV2Purpose
	return in
}

func (f *governanceFixture) directoryV2Grant(t *testing.T, role, key string) credentialuse.Receipt {
	t.Helper()
	tx := f.begin(t)
	f.context(t, tx, "admin", credentialuse.GrantUse)
	r, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", directoryV2Input(role, key), []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
	if err != nil {
		t.Fatal("create separate dictionary 2 grant", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *governanceFixture) allowDirectoryRole(t *testing.T, role string) {
	t.Helper()
	tx := f.begin(t)
	f.context(t, tx, "admin", credentialuse.ManageRoles)
	f.exec(t, tx, `INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'directory_assets',true,true)`, cuTenant, role)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryV2PurposeFragmentPreservesHistoryAndRollsBack(t *testing.T) {
	f := newGovernanceFixtureAtVersion(t, 15)
	seedDirectoryRole(t, f)
	f.directoryGrant(t, directoryRole, "historical-v1")
	const snapshot = `SELECT jsonb_build_object(
 'grants',(SELECT jsonb_agg(to_jsonb(g) ORDER BY purpose,role_id) FROM adtr.operation_account_use_grants g),
 'mutations',(SELECT jsonb_agg(to_jsonb(m) ORDER BY actor_id,idempotency_key) FROM adtr.operation_account_use_mutations m),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM adtr.operation_account_use_audit a),
 'epochs',(SELECT jsonb_agg(jsonb_build_array(id,authorization_version) ORDER BY id) FROM adtr.users),
 'version',(SELECT version FROM adtr.schema_version))::text`
	var before string
	if err := f.conn.QueryRow(f.ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		tx := f.begin(t)
		f.exec(t, tx, credentialuse.DirectoryV2PurposeSchema)
		var after string
		if err := tx.QueryRow(f.ctx, snapshot).Scan(&after); err != nil || after != before {
			t.Fatal("purpose fragment changed old grants, incarnations, timestamps, receipts, audit, epochs or schema version", err)
		}
		var eligible bool
		if err := tx.QueryRow(f.ctx, `SELECT adtr.credential_use_role_eligible_for_purpose($1,'domain-one',$2,'domain.directory_read.v2')`, cuTenant, directoryRole).Scan(&eligible); err != nil || !eligible {
			t.Fatal("fragment was not active inside its transaction", err)
		}
		if commit {
			if err := tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
		} else if err := tx.Rollback(f.ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.conn.QueryRow(f.ctx, snapshot).Scan(&after); err != nil || after != before {
			t.Fatal("fragment commit/rollback changed immutable history or version", err)
		}
		if err := f.conn.QueryRow(f.ctx, `SELECT adtr.credential_use_role_eligible_for_purpose($1,'domain-one',$2,'domain.directory_read.v2')`, cuTenant, directoryRole).Scan(&eligible); err != nil || eligible != commit {
			t.Fatal("purpose eligibility did not commit/roll back atomically", err)
		}
		var constraints int
		if err := f.conn.QueryRow(f.ctx, `SELECT count(*) FROM pg_constraint WHERE connamespace='adtr'::regnamespace
 AND conname IN ('operation_account_use_grants_purpose_check','operation_account_use_mutations_purpose_check','operation_account_use_audit_purpose_check')
 AND position('domain.directory_read.v2' in pg_get_constraintdef(oid))>0`).Scan(&constraints); err != nil || (!commit && constraints != 0) || (commit && constraints != 3) {
			t.Fatal("purpose constraints did not commit/roll back together", constraints, err)
		}
	}
	var noV2, oneEpochTrigger bool
	if err := f.conn.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v2'),
 (SELECT count(*)=1 FROM pg_trigger WHERE tgrelid='adtr.access_permissions'::regclass AND tgfoid='adtr.credential_use_directory_permission_epoch()'::regprocedure AND NOT tgisinternal)`).Scan(&noV2, &oneEpochTrigger); err != nil || !noV2 || !oneEpochTrigger {
		t.Fatal("fragment created v2 authority or duplicated directory permission epoch trigger", err)
	}
}

// These are real PostgreSQL fragment cases, not mocked grant/receipt results.
// Integration-tag compilation is not execution or migration16 acceptance.
func TestDirectoryV2PurposeDefaultDenyAndEligibility(t *testing.T) {
	f := newDirectoryV2FragmentFixture(t)
	v1 := f.directoryGrant(t, directoryRole, "v1-only")
	s := credentialuse.New()
	tx := f.begin(t)
	defer tx.Rollback(context.Background())
	for _, who := range []struct{ name, role string }{{"admin", "platform_admin"}, {"member", cuRole}, {"directory-member", directoryRole}} {
		got, err := s.EffectiveForPurposeTx(f.ctx, tx, f.principal(who.name), who.role, cuAccount, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
		if err != nil || got.ExplicitlyGranted || got.Eligible || got.GrantRevision != "0" || got.Purpose != credentialuse.DirectoryV2Purpose || got.ConsumerEnabled != credentialuse.DirectoryV2ConsumerEnabled {
			t.Fatal("old grants or administrator status implied dictionary 2 authority", got, err)
		}
	}
	old, err := s.EffectiveTx(f.ctx, tx, f.principal("member"), cuRole, cuAccount, []string{"domain-one"})
	if err != nil || !old.Eligible || old.GrantRevision != f.initial.GrantRevision {
		t.Fatal("connection-test authority changed", old, err)
	}
	oldDirectory, err := s.EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryPurpose)
	if err != nil || !oldDirectory.Eligible || oldDirectory.GrantRevision != v1.GrantRevision {
		t.Fatal("dictionary 1 authority changed", oldDirectory, err)
	}
	list, err := s.GrantsForPurposeTx(f.ctx, tx, cuTenant, cuAccount, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
	if err != nil || len(list.Grants) != 0 {
		t.Fatal("migration inserted or copied dictionary 2 grants", list, err)
	}
	accounts, err := s.AccountsForPurposeTx(f.ctx, tx, cuTenant, []string{"domain-one"}, credentialuse.Filter{PageIdx: 1, PageSize: 10}, credentialuse.DirectoryV2Purpose)
	if err != nil || accounts.Page.Total != 0 || len(accounts.List) != 0 {
		t.Fatal("dictionary 2 cleanup list crossed purpose", accounts, err)
	}
	for _, purpose := range []string{credentialuse.Purpose, credentialuse.DirectoryPurpose, credentialuse.DirectoryV2Purpose} {
		roles, err := s.RolesForPurposeTx(f.ctx, tx, cuTenant, cuAccount, []string{"domain-one"}, purpose)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, role := range roles.Roles {
			if role.RoleID == directoryRole {
				found = true
			}
		}
		if found != (purpose != credentialuse.Purpose) {
			t.Fatal("directory-only role eligibility crossed purpose", purpose, roles)
		}
	}
	for _, tc := range []struct {
		purpose      any
		role, domain string
		want         bool
	}{
		{credentialuse.Purpose, cuRole, "domain-one", true},
		{credentialuse.Purpose, directoryRole, "domain-one", false},
		{credentialuse.DirectoryPurpose, directoryRole, "domain-one", true},
		{credentialuse.DirectoryV2Purpose, directoryRole, "domain-one", true},
		{credentialuse.DirectoryV2Purpose, cuRole, "domain-one", false},
		{credentialuse.DirectoryV2Purpose, "platform_admin", "domain-one", true},
		{credentialuse.DirectoryV2Purpose, "platform_admin", "domain-two", false},
		{credentialuse.DirectoryV2Purpose, directoryRole, "domain-two", false},
		{credentialuse.DirectoryV2Purpose, "viewer", "domain-one", false},
		{credentialuse.DirectoryV2Purpose, "missing-role", "domain-one", false},
		{nil, "platform_admin", "domain-one", false},
		{"", "platform_admin", "domain-one", false},
		{"domain.directory_read.v1", "platform_admin", "domain-one", false},
		{"domain.directory_read.v02", "platform_admin", "domain-one", false},
		{"domain.directory_read.v2 ", "platform_admin", "domain-one", false},
		{"domain.directory_read.v3", "platform_admin", "domain-one", false},
	} {
		var got bool
		if err = tx.QueryRow(f.ctx, `SELECT adtr.credential_use_role_eligible_for_purpose($1,$2,$3,$4)`, cuTenant, tc.domain, tc.role, tc.purpose).Scan(&got); err != nil || got != tc.want {
			t.Fatal("purpose/resource/role eligibility mismatch", tc, got, err)
		}
	}
}

func TestDirectoryV2PurposeGrantReceiptAndReplayIsolation(t *testing.T) {
	f := newDirectoryV2FragmentFixture(t)
	f.allowDirectoryRole(t, cuRole)
	v1 := f.directoryGrant(t, cuRole, "v1-allow")
	v2 := f.directoryV2Grant(t, cuRole, "v2-allow")
	s := credentialuse.New()
	tx := f.begin(t)
	defer tx.Rollback(context.Background())
	grants := []struct {
		purpose, key string
		receipt      credentialuse.Receipt
	}{
		{credentialuse.Purpose, "initial-allow", f.initial},
		{credentialuse.DirectoryPurpose, "v1-allow", v1},
		{credentialuse.DirectoryV2Purpose, "v2-allow", v2},
	}
	for _, saved := range grants {
		got, err := s.GrantsForPurposeTx(f.ctx, tx, cuTenant, cuAccount, []string{"domain-one"}, saved.purpose)
		if err != nil || len(got.Grants) != 1 || got.Grants[0].Purpose != saved.purpose || got.Grants[0].GrantRevision != saved.receipt.GrantRevision {
			t.Fatal("grant list mixed exact purposes or incarnations", got, err)
		}
		effective, err := s.EffectiveForPurposeTx(f.ctx, tx, f.principal("member"), cuRole, cuAccount, []string{"domain-one"}, saved.purpose)
		if err != nil || !effective.Eligible || effective.GrantRevision != saved.receipt.GrantRevision || effective.Purpose != saved.purpose {
			t.Fatal("exact grant was not used", effective, err)
		}
		for _, selected := range grants {
			in := directoryInput(cuRole, saved.key)
			in.Purpose = selected.purpose
			receipt, err := s.ReceiptForPurposeTx(f.ctx, tx, f.principal("admin"), saved.key, []string{"domain-one"}, selected.purpose)
			if selected.purpose != saved.purpose {
				requireCredentialError(t, err, 404, "not_found")
			} else if err != nil || receipt.GrantRevision != saved.receipt.GrantRevision || receipt.Purpose != selected.purpose {
				t.Fatal("exact receipt was not recovered", receipt, err)
			}
			// A committed retry needs no new proof. Reusing a global actor/key
			// for another exact purpose is always a conflict, never a regrant.
			replay, err := s.ReplayForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", in, []string{"domain-one"}, selected.purpose)
			if selected.purpose != saved.purpose {
				requireCredentialError(t, err, 409, "idempotency_conflict")
				_, err = s.MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", in, []string{"domain-one"}, selected.purpose)
				requireCredentialError(t, err, 409, "idempotency_conflict")
			} else if err != nil || replay == nil || !replay.Replayed || replay.Purpose != saved.purpose || replay.GrantRevision != saved.receipt.GrantRevision {
				t.Fatal("committed receipt replay changed purpose or incarnation", replay, err)
			}
		}
	}
	for _, purpose := range []string{credentialuse.DirectoryPurpose, credentialuse.DirectoryV2Purpose} {
		in := directoryInput(cuRole, "wrong-legacy-route")
		in.Purpose = purpose
		_, err := s.MutateTx(f.ctx, tx, f.principal("admin"), "/grant", in, []string{"domain-one"})
		requireCredentialError(t, err, 422, "unsupported_credential_purpose")
	}
	_, err := s.ReceiptForPurposeTx(f.ctx, tx, f.principal("member"), "v2-allow", []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
	requireCredentialError(t, err, 404, "not_found")
	_, err = s.EffectiveForPurposeTx(f.ctx, tx, f.principal("member"), cuRole, cuAccount, []string{"domain-two"}, credentialuse.DirectoryV2Purpose)
	requireCredentialError(t, err, 404, "not_found")
	var unchanged bool
	if err = tx.QueryRow(f.ctx, `SELECT (SELECT count(*)=3 FROM adtr.operation_account_use_grants)
 AND (SELECT count(*)=3 FROM adtr.operation_account_use_mutations)
 AND (SELECT count(*)=3 FROM adtr.operation_account_use_audit)`).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("receipt replay or collision changed authority/history", err)
	}
}

func TestDirectoryV2PurposeRevokeRegrantAndPairInvalidation(t *testing.T) {
	f := newDirectoryV2FragmentFixture(t)
	f.allowDirectoryRole(t, cuRole)
	v1 := f.directoryGrant(t, cuRole, "v1-pair")
	v2 := f.directoryV2Grant(t, cuRole, "v2-pair")
	s := credentialuse.New()
	t.Run("exact-revoke-and-regrant", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		f.context(t, tx, "admin", credentialuse.RevokeUse)
		in := directoryV2Input(cuRole, "v2-revoke")
		in.ExpectedGrantRevision = v2.GrantRevision
		revoked, err := s.MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/revoke", in, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
		if err != nil || revoked.Allowed {
			t.Fatal(revoked, err)
		}
		for _, old := range []credentialuse.Receipt{f.initial, v1} {
			effective, err := s.EffectiveForPurposeTx(f.ctx, tx, f.principal("member"), cuRole, cuAccount, []string{"domain-one"}, old.Purpose)
			if err != nil || !effective.Eligible || effective.GrantRevision != old.GrantRevision {
				t.Fatal("v2 revoke changed old purpose", effective, err)
			}
		}
		f.context(t, tx, "admin", credentialuse.GrantUse)
		in.IdempotencyKey = "v2-stale-regrant"
		_, err = s.MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", in, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
		requireCredentialError(t, err, 409, "revision_conflict")
		in.IdempotencyKey, in.ExpectedGrantRevision = "v2-regrant", revoked.GrantRevision
		regranted, err := s.MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/grant", in, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
		if err != nil || !regranted.Allowed {
			t.Fatal(regranted, err)
		}
		first, _ := strconv.ParseInt(v2.GrantRevision, 10, 64)
		middle, _ := strconv.ParseInt(revoked.GrantRevision, 10, 64)
		last, _ := strconv.ParseInt(regranted.GrantRevision, 10, 64)
		if !(first < middle && middle < last) {
			t.Fatal("dictionary 2 grant incarnation was reused")
		}
	})
	tx := f.begin(t)
	f.context(t, tx, "member", credentialuse.MutateAccount)
	f.exec(t, tx, `UPDATE adtr.operation_accounts SET revision=revision+1,credential_revision=credential_revision+1 WHERE account_id=$1`, cuAccount)
	f.exec(t, tx, `UPDATE adtr.operation_account_credentials SET credential_revision=credential_revision+1,ciphertext=decode(repeat('cd',32),'hex') WHERE account_id=$1`, cuAccount)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var invalidated bool
	if err := f.conn.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=3 AND bool_and(NOT allowed AND account_credential_revision=1 AND grant_revision>$1::bigint) FROM adtr.operation_account_use_grants WHERE account_id=$2)
 AND (SELECT count(*)=3 AND count(DISTINCT purpose)=3 FROM adtr.operation_account_use_audit WHERE account_id=$2 AND action='credential_use_pair_replaced')`, v2.GrantRevision, cuAccount).Scan(&invalidated); err != nil || !invalidated {
		t.Fatal("pair replacement failed to invalidate/audit every exact purpose", err)
	}
}

func TestDirectoryV2PurposeSQLGuardsRemainExact(t *testing.T) {
	f := newDirectoryV2FragmentFixture(t)
	f.allowDirectoryRole(t, cuRole)
	v1 := f.directoryGrant(t, cuRole, "v1-guard")
	v2 := f.directoryV2Grant(t, cuRole, "v2-guard")
	const insert = `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by)
 VALUES($1,'domain-one',$2,'domain.directory_read.v2','platform_admin',1,true,$3)`
	for _, actor := range []string{"old-binary", "directory-member", "foreign-admin", "disabled-admin", "forced-admin", "expired-admin", "no-mfa-admin"} {
		t.Run("requires-current-scoped-admin-"+actor, func(t *testing.T) {
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
		name, query string
		args        []any
		state       string
	}{
		{"unknown-grant", `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by) VALUES($1,'domain-one',$2,'domain.directory_read.v3','platform_admin',1,true,$3)`, []any{cuTenant, cuAccount, f.users["admin"]}, "42501"},
		{"stale-pair", `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by) VALUES($1,'domain-one',$2,'domain.directory_read.v2','platform_admin',2,true,$3)`, []any{cuTenant, cuAccount, f.users["admin"]}, "42501"},
		{"wrong-actor", insert, []any{cuTenant, cuAccount, f.users["member"]}, "42501"},
		{"missing-admin-domain-scope", `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by) VALUES($1,'domain-two',$2,'domain.directory_read.v2','platform_admin',1,true,$3)`, []any{cuTenant, cuOtherAccount, f.users["admin"]}, "42501"},
		{"v1-incarnation-cannot-authorize-v2-receipt", `INSERT INTO adtr.operation_account_use_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed) VALUES($1,$2,'forged-v2','grant',repeat('a',64),'domain-one',$3,$4,'domain.directory_read.v2',$5,1,true)`, []any{cuTenant, f.users["admin"], cuAccount, cuRole, v1.GrantRevision}, "42501"},
		{"v2-incarnation-cannot-authorize-v1-receipt", `INSERT INTO adtr.operation_account_use_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed) VALUES($1,$2,'forged-v1','grant',repeat('a',64),'domain-one',$3,$4,'domain.directory_read',$5,1,true)`, []any{cuTenant, f.users["admin"], cuAccount, cuRole, v2.GrantRevision}, "42501"},
		{"unknown-mutation", `INSERT INTO adtr.operation_account_use_mutations(tenant_id,actor_id,idempotency_key,operation,fingerprint,domain_id,account_id,role_id,purpose,grant_revision,account_credential_revision,allowed) VALUES($1,$2,'forged-unknown','grant',repeat('a',64),'domain-one',$3,$4,'domain.directory_read.v3',$5,1,true)`, []any{cuTenant, f.users["admin"], cuAccount, cuRole, v2.GrantRevision}, "42501"},
		{"unknown-audit", `INSERT INTO adtr.operation_account_use_audit(tenant_id,actor_id,domain_id,account_id,role_id,purpose,action,old_grant_revision,new_grant_revision,account_credential_revision,allowed,result) VALUES($1,$2,'domain-one',$3,$4,'domain.directory_read.v3','credential_use_grant',0,1,1,true,'saved')`, []any{cuTenant, f.users["admin"], cuAccount, cuRole}, "23514"},
		{"immutable-purpose", `UPDATE adtr.operation_account_use_grants SET purpose='domain.directory_read' WHERE purpose='domain.directory_read.v2'`, nil, "42501"},
		{"immutable-incarnation", `UPDATE adtr.operation_account_use_grants SET grant_revision=grant_revision+1 WHERE purpose='domain.directory_read.v2'`, nil, "42501"},
		{"immutable-receipt", `DELETE FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read.v2'`, nil, "P0001"},
		{"immutable-audit", `DELETE FROM adtr.operation_account_use_audit WHERE purpose='domain.directory_read.v2'`, nil, "P0001"},
		{"durable-grant", `DELETE FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v2'`, nil, "42501"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			f.context(t, tx, "admin", credentialuse.GrantUse)
			_, err := tx.Exec(f.ctx, tc.query, tc.args...)
			requireSQLState(t, err, tc.state)
		})
	}
}

func TestDirectoryV2PurposeDormantGrantManagementAndEpochs(t *testing.T) {
	f := newDirectoryV2FragmentFixture(t)
	r := f.directoryV2Grant(t, directoryRole, "v2-only-governed")
	for _, tc := range []struct {
		name, query string
		args        []any
	}{
		{"permission", `UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, []any{cuTenant, directoryRole}},
		{"membership", `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, []any{cuTenant, directoryRole}},
		{"user-reset", `UPDATE adtr.users SET password_hash='synthetic-change',must_change=true WHERE id=$1`, []any{f.users["directory-member"]}},
		{"new-member", `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id) VALUES($1,'new-v2-member','synthetic','viewer',$2)`, []any{cuTenant, directoryRole}},
	} {
		t.Run("unguarded-"+tc.name, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			_, err := tx.Exec(f.ctx, tc.query, tc.args...)
			requireGovernanceDenied(t, err)
		})
	}
	t.Run("dormant-restoration-needs-fresh-context", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		f.context(t, tx, "admin", credentialuse.ManageRoles)
		f.exec(t, tx, `UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, cuTenant, directoryRole)
		clearGovernanceContext(t, f, tx)
		_, err := tx.Exec(f.ctx, `UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, cuTenant, directoryRole)
		requireGovernanceDenied(t, err)
	})
	for _, mark := range []string{"directory_assets", "domains", "tasks"} {
		t.Run("permission-epochs-"+mark, func(t *testing.T) {
			tx := f.begin(t)
			defer tx.Rollback(context.Background())
			f.context(t, tx, "admin", credentialuse.ManageRoles)
			var prior int64
			if err := tx.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["directory-member"]).Scan(&prior); err != nil {
				t.Fatal(err)
			}
			for _, restore := range []bool{false, true} {
				f.exec(t, tx, `UPDATE adtr.access_permissions SET readable=$4,writeable=$4 AND mark<>'domains' WHERE tenant_id=$1 AND role_id=$2 AND mark=$3`, cuTenant, directoryRole, mark, restore)
				got, err := credentialuse.New().EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
				if err != nil || got.Eligible != restore || !got.ExplicitlyGranted || got.GrantRevision != r.GrantRevision {
					t.Fatal("permission change lost dormant/exact v2 grant semantics", got, err)
				}
				var next int64
				if err = tx.QueryRow(f.ctx, `SELECT authorization_version FROM adtr.users WHERE id=$1`, f.users["directory-member"]).Scan(&next); err != nil || next <= prior {
					t.Fatal("directory permission change failed to invalidate actor", err)
				}
				prior = next
			}
		})
	}
	t.Run("expired-tenant-denies-use-but-allows-exact-revoke", func(t *testing.T) {
		tx := f.begin(t)
		defer tx.Rollback(context.Background())
		f.context(t, tx, "admin", credentialuse.ManageTenant)
		f.exec(t, tx, `UPDATE adtr.resource_tenant_config SET expire_time=0 WHERE tenant_id=$1`, cuTenant)
		got, err := credentialuse.New().EffectiveForPurposeTx(f.ctx, tx, f.principal("directory-member"), directoryRole, cuAccount, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
		if err != nil || got.Eligible || !got.ExplicitlyGranted {
			t.Fatal("tenant expiry did not deny effective v2 use", got, err)
		}
		f.context(t, tx, "admin", credentialuse.RevokeUse)
		in := directoryV2Input(directoryRole, "expired-v2-revoke")
		in.ExpectedGrantRevision = r.GrantRevision
		receipt, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.principal("admin"), "/revoke", in, []string{"domain-one"}, credentialuse.DirectoryV2Purpose)
		if err != nil || receipt.Allowed || receipt.Purpose != credentialuse.DirectoryV2Purpose {
			t.Fatal("expiry blocked exact v2 revocation", receipt, err)
		}
	})
}
