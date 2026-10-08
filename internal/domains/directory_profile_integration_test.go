//go:build integration

package domains_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Component-only installation: the production migrator and registry stay at
// schema15. The isolated fixture installs all three fragments under the existing
// exclusive fence without pretending that migration16 has been integrated.
func installDirectoryProfileFragments(f *directoryUseFixture) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := f.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734192801)`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow(ctx, `SELECT version FROM adtr.schema_version FOR UPDATE`).Scan(&version); err != nil {
		return err
	}
	if version != 15 {
		return errors.New("directory component requires unchanged schema15 fixture")
	}
	if _, err = tx.Exec(ctx, credentialuse.DirectoryV2PurposeSchema+domains.DirectoryDependencyV2Schema+domains.DirectoryUseV2Schema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func grantDirectoryV2(f *directoryUseFixture) string {
	f.t.Helper()
	var revision string
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.GrantUse); err != nil {
			return err
		}
		out, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/grant", credentialuse.Input{
			AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryV2Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "directory-v2-grant",
		}, []string{f.id}, credentialuse.DirectoryV2Purpose)
		revision = out.GrantRevision
		return err
	})
	return revision
}

func openDirectoryV2Ledger(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	_, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), domains.OpenDirectoryV2UseForTest(ctx, tx, ex.Task)
	})
	if err != nil {
		return tasks.Outcome{State: tasks.Failed, Code: "opening_denied"}
	}
	return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
}

func submitDirectoryV2Tx(f *directoryUseFixture, ctx context.Context, tx pgx.Tx, key, grant string) (tasks.Task, error) {
	payload, _ := json.Marshal(map[string]any{"credentialSource": "operation_account", "connectionRevision": "2", "connectionCredentialGeneration": "2", "accountId": f.account, "accountCredentialRevision": "1", "grantRoleId": "platform_admin", "grantRevision": grant, "policyRevision": strings.Repeat("a", 64), "dictionaryVersion": 2})
	out, err := f.engine.SubmitTx(ctx, tx, f.p, tasks.SubmitInput{TaskName: domains.DirectoryV2KindName, DomainID: f.id, PayloadVersion: 1, Payload: payload, IdempotencyKey: key})
	if err == nil {
		err = domains.ReserveDirectoryV2UseForTest(ctx, tx, out.Task)
	}
	return out.Task, err
}

func TestDirectoryProfileFragmentsPreserveLegacyReservationAndHistory(t *testing.T) {
	f := fixtureForDirectoryUse(t, true, nil)
	task := f.submitDirectory("pre-versioned-reservation")
	var original string
	if err := f.conn.QueryRow(context.Background(), `SELECT to_jsonb(t)::text FROM adtr.tasks t WHERE task_id=$1`, task.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := installDirectoryProfileFragments(f); err != nil {
		t.Fatal(err)
	}
	var retained string
	var version, newGrants int
	if err := f.conn.QueryRow(context.Background(), `SELECT to_jsonb(t)::text,u.dictionary_version,(SELECT count(*) FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v2') FROM adtr.tasks t JOIN adtr.domain_directory_task_uses u USING(task_id) WHERE t.task_id=$1`, task.ID).Scan(&retained, &version, &newGrants); err != nil || retained != original || version != 1 || newGrants != 0 {
		t.Fatal("legacy data or grant authority changed", version, newGrants, err)
	}
	if _, err := f.engine.RunOne(context.Background(), "legacy-after-fragment"); err != nil {
		t.Fatal(err)
	}
	if state, n := f.directoryState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("queued legacy task failed after fragment", state, n)
	}
	var schema int
	if err := f.conn.QueryRow(context.Background(), `SELECT version FROM adtr.schema_version`).Scan(&schema); err != nil || schema != 15 {
		t.Fatal("component claimed migration16", schema, err)
	}
}

func TestDirectoryV2LedgerRequiresSeparatePurposeAndExactDependency(t *testing.T) {
	f := fixtureForDirectoryUse(t, true, nil)
	if err := installDirectoryProfileFragments(f); err != nil {
		t.Fatal(err)
	}
	f.installKind(t, domains.DirectoryV2UseKindForTest(f.store, openDirectoryV2Ledger))
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := submitDirectoryV2Tx(f, ctx, tx, "legacy-grant-denied", f.directoryGrant)
		return err
	})
	if err == nil {
		t.Fatal("legacy grant authorized v2")
	}
	var count int
	if err := f.conn.QueryRow(context.Background(), `SELECT count(*) FROM adtr.tasks WHERE kind='domain.directory_read.v2'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("denial left a task", count, err)
	}
	grant := grantDirectoryV2(f)
	var task tasks.Task
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		task, err = submitDirectoryV2Tx(f, ctx, tx, "exact-v2", grant)
		return err
	})
	for _, tuple := range []struct {
		purpose string
		version int
	}{
		{credentialuse.DirectoryPurpose, 1}, {credentialuse.DirectoryPurpose, 2}, {credentialuse.DirectoryV2Purpose, 1}, {credentialuse.DirectoryV2Purpose, 3}, {"unknown", 2},
	} {
		var valid bool
		err := f.conn.QueryRow(context.Background(), `SELECT adtr.domain_directory_use_pins(jsonb_populate_record(NULL::adtr.domain_directory_task_uses,to_jsonb(u)||jsonb_build_object('purpose',$2::text,'dictionary_version',$3::integer))) FROM adtr.domain_directory_task_uses u WHERE task_id=$1`, task.ID, tuple.purpose, tuple.version).Scan(&valid)
		if err != nil || valid {
			t.Fatal("mixed purpose/dictionary/task tuple accepted", tuple, err)
		}
	}
	for _, statement := range []string{
		`UPDATE adtr.domain_directory_task_uses SET dictionary_version=1,purpose='domain.directory_read' WHERE task_id=$1`,
		`UPDATE adtr.operation_account_dependencies SET consumer_kind='domain.directory_read' WHERE object_id=$1 AND consumer_kind='domain.directory_read.v2'`,
		`DELETE FROM adtr.operation_account_dependencies WHERE object_id=$1 AND consumer_kind='domain.directory_read.v2'`,
		`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation) SELECT tenant_id,domain_id,account_id,'domain.directory_read',object_id,account_credential_revision,connection_credential_generation FROM adtr.operation_account_dependencies WHERE object_id=$1 AND consumer_kind='domain.directory_read.v2'`,
	} {
		if err := f.attempt(func(ctx context.Context, tx pgx.Tx) error { _, err := tx.Exec(ctx, statement, task.ID); return err }); err == nil {
			t.Fatal("profile/dependency mutation accepted")
		}
	}
	if _, err := f.engine.RunOne(context.Background(), "v2-exact-owner"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := f.conn.QueryRow(context.Background(), `SELECT u.state,(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.object_id=u.task_id AND d.consumer_kind IN ('domain.directory_read','domain.directory_read.v2')) FROM adtr.domain_directory_task_uses u WHERE task_id=$1 AND purpose='domain.directory_read.v2' AND dictionary_version=2`, task.ID).Scan(&state, &count); err != nil || state != "quiesced" || count != 0 {
		t.Fatal("v2 did not release exact dependency", state, count, err)
	}
}

func TestDirectoryProfilesKeepOriginalOpenerCleanupAfterRevocationAndLeaseLoss(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		name := "legacy"
		if v2 {
			name = "v2"
		}
		t.Run(name, func(t *testing.T) {
			f := fixtureForDirectoryUse(t, true, nil)
			if err := installDirectoryProfileFragments(f); err != nil {
				t.Fatal(err)
			}
			grant := f.directoryGrant
			if v2 {
				grant = grantDirectoryV2(f)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			open := openDirectoryLedger
			if v2 {
				open = openDirectoryV2Ledger
			}
			execute := func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
				out := open(ctx, ex)
				if out.State != tasks.Succeeded {
					return out
				}
				close(started)
				<-release
				return out
			}
			kind := domains.DirectoryUseKindForTest(f.store, execute)
			other := domains.DirectoryV2UseKindForTest(f.store, execute)
			if v2 {
				kind, other = other, kind
			}
			original := kind.OnQuiesced
			var witness tasks.QuiescedAttempt
			kind.OnQuiesced = func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
				witness = q
				return original(ctx, tx, q)
			}
			f.installKind(t, kind)
			var task tasks.Task
			f.tx(func(ctx context.Context, tx pgx.Tx) error {
				var err error
				if v2 {
					task, err = submitDirectoryV2Tx(f, ctx, tx, "blocked-versioned", grant)
				} else {
					task, err = f.submitDirectoryTx(ctx, tx, "blocked-versioned")
				}
				return err
			})
			done, exited := make(chan error, 1), make(chan struct{})
			runCtx, cancel := context.WithCancel(context.Background())
			t.Cleanup(func() {
				once.Do(func() { close(release) })
				cancel()
				select {
				case <-exited:
				case <-time.After(8 * time.Second):
					t.Error("worker did not return")
				}
			})
			go func() {
				defer close(exited)
				_, err := f.engine.RunOne(runCtx, "original-versioned-owner")
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("ledger did not open")
			}
			f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID)
			if _, err := f.engine.RecoverExpired(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.tx(func(ctx context.Context, tx pgx.Tx) error {
				if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.RevokeUse); err != nil {
					return err
				}
				purpose := credentialuse.DirectoryPurpose
				if v2 {
					purpose = credentialuse.DirectoryV2Purpose
				}
				_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "revoke-versioned"}, []string{f.id}, purpose)
				return err
			})
			f.detach()
			requireAccountBlocked(t, f.replace())
			if n, err := f.store.ReconcileReservedDirectoryUses(context.Background(), f.config, 100); err != nil || n != 0 {
				t.Fatal("old maintenance released opened use", n, err)
			}
			if n, err := domains.ReconcileReservedDirectoryV2UsesForTest(context.Background(), f.store, f.config, 100); err != nil || n != 0 {
				t.Fatal("v2 maintenance released opened use", n, err)
			}
			once.Do(func() { close(release) })
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, tasks.ErrLeaseLost) {
					t.Fatal(err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("original return was not acknowledged")
			}
			if err := f.replace(); err != nil {
				t.Fatal("original return did not release account", err)
			}
			if err := other.OnQuiesced(context.Background(), nil, witness); err == nil {
				t.Fatal("other profile accepted engine-issued witness")
			}
			for range 2 {
				if err := f.attempt(func(ctx context.Context, tx pgx.Tx) error { return original(ctx, tx, witness) }); err != nil {
					t.Fatal("idempotent cleanup depended on current authority", err)
				}
			}
		})
	}
}

func TestDirectoryProfileFragmentsRejectReservedCollisionsAtomically(t *testing.T) {
	for _, mode := range []string{"task", "dependency", "column", "index"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtureForDirectoryUse(t, true, nil)
			switch mode {
			case "task":
				f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) VALUES('v2-collision',$1,$2,'domain.directory_read.v2',1,'{}','original-collision-hash',$3,'original-epoch','original-collision-key','queued',1)`, f.p.TenantID, f.id, f.p.ActorID)
			case "dependency":
				f.exec(`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES($1,$2,$3,'domain.directory_read.v2','v2-collision')`, f.p.TenantID, f.id, f.account)
			case "column":
				f.exec(`ALTER TABLE adtr.domain_directory_task_uses ADD COLUMN dictionary_version text`)
			case "index":
				f.exec(`CREATE INDEX domain_directory_versioned_use_identity ON adtr.operation_accounts(account_id)`)
			}
			for range 2 {
				err := installDirectoryProfileFragments(f)
				var db *pgconn.PgError
				if !errors.As(err, &db) {
					t.Fatal("collision not rejected by PostgreSQL", err)
				}
				want := "23514"
				if mode == "column" {
					want = "42701"
				}
				if mode == "index" {
					want = "42P07"
				}
				if db.Code != want {
					t.Fatal("unexpected collision failure", mode, err)
				}
				var intact bool
				err = f.conn.QueryRow(context.Background(), `SELECT version=15 AND NOT EXISTS(SELECT FROM pg_constraint WHERE conrelid='adtr.operation_account_use_grants'::regclass AND conname='operation_account_use_grants_purpose_check' AND position('domain.directory_read.v2' in pg_get_constraintdef(oid))>0) AND NOT EXISTS(SELECT FROM pg_constraint WHERE conrelid='adtr.operation_account_dependencies'::regclass AND conname='operation_account_dependency_pins' AND position('domain.directory_read.v2' in pg_get_constraintdef(oid))>0) FROM adtr.schema_version`).Scan(&intact)
				if err != nil || !intact {
					t.Fatal("collision left partial schema", err)
				}
			}
		})
	}
}

func TestDirectoryProfileFragmentsRejectAnOpenedLegacyUse(t *testing.T) {
	f := fixtureForDirectoryUse(t, true, nil)
	f.submitDirectory("opened-before-profile")
	lease, err := f.engine.Claim(context.Background(), "unreturned-legacy-owner")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.engine.Start(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	out := openDirectoryLedger(context.Background(), tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, v, work)
	}})
	if out.State != tasks.Succeeded {
		t.Fatal(out)
	}
	err = installDirectoryProfileFragments(f)
	var db *pgconn.PgError
	if !errors.As(err, &db) || db.ConstraintName != "domain_directory_v2_opened_use" {
		t.Fatal("opened use did not block fragment", err)
	}
	var absent bool
	if err = f.conn.QueryRow(context.Background(), `SELECT NOT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='adtr' AND table_name='domain_directory_task_uses' AND column_name='dictionary_version') AND to_regclass('adtr.domain_directory_versioned_use_identity') IS NULL`).Scan(&absent); err != nil || !absent {
		t.Fatal("opened-use rejection left partial schema", err)
	}
}

func TestDirectoryProfilesReconcileNeverOpenedTerminalUsesWithoutCurrentAuthority(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		name := "legacy"
		if v2 {
			name = "v2"
		}
		t.Run(name, func(t *testing.T) {
			f := fixtureForDirectoryUse(t, true, nil)
			if err := installDirectoryProfileFragments(f); err != nil {
				t.Fatal(err)
			}
			grant, purpose := f.directoryGrant, credentialuse.DirectoryPurpose
			if v2 {
				grant, purpose = grantDirectoryV2(f), credentialuse.DirectoryV2Purpose
			}
			execute := func(context.Context, tasks.Execution) tasks.Outcome {
				return tasks.Outcome{State: tasks.Failed, Code: "policy_revision_changed"}
			}
			kind := domains.DirectoryUseKindForTest(f.store, execute)
			if v2 {
				kind = domains.DirectoryV2UseKindForTest(f.store, execute)
			}
			original := kind.OnQuiesced
			var witness tasks.QuiescedAttempt
			kind.OnQuiesced = func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
				witness = q
				return original(ctx, tx, q)
			}
			f.installKind(t, kind)
			var task tasks.Task
			f.tx(func(ctx context.Context, tx pgx.Tx) error {
				var err error
				if v2 {
					task, err = submitDirectoryV2Tx(f, ctx, tx, "never-opened-versioned", grant)
				} else {
					task, err = f.submitDirectoryTx(ctx, tx, "never-opened-versioned")
				}
				return err
			})
			if _, err := f.engine.RunOne(context.Background(), "never-opened-owner"); err != nil {
				t.Fatal(err)
			}
			f.tx(func(ctx context.Context, tx pgx.Tx) error {
				if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.RevokeUse); err != nil {
					return err
				}
				_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "revoke-never-opened"}, []string{f.id}, purpose)
				return err
			})
			f.detach()
			if v2 {
				if n, err := f.store.ReconcileReservedDirectoryUses(context.Background(), f.config, 100); err != nil || n != 0 {
					t.Fatal("legacy maintenance widened to v2", n, err)
				}
				if n, err := domains.ReconcileReservedDirectoryV2UsesForTest(context.Background(), f.store, f.config, 100); err != nil || n != 1 {
					t.Fatal("v2 reservation did not reconcile", n, err)
				}
			} else {
				if n, err := domains.ReconcileReservedDirectoryV2UsesForTest(context.Background(), f.store, f.config, 100); err != nil || n != 0 {
					t.Fatal("v2 maintenance widened to legacy", n, err)
				}
				if n, err := f.store.ReconcileReservedDirectoryUses(context.Background(), f.config, 100); err != nil || n != 1 {
					t.Fatal("legacy reservation did not reconcile", n, err)
				}
			}
			var valid bool
			if err := f.conn.QueryRow(context.Background(), `SELECT state='quiesced' AND quiescence_reason='never_opened_terminal' AND opened_at IS NULL AND opener_owner IS NULL AND opener_fencing_token IS NULL AND opener_attempt IS NULL AND quiesced_at IS NOT NULL FROM adtr.domain_directory_task_uses WHERE task_id=$1`, task.ID).Scan(&valid); err != nil || !valid {
				t.Fatal("no-open evidence changed", err)
			}
			if err := f.replace(); err != nil {
				t.Fatal("reservation blocker remained", err)
			}
			if err := f.attempt(func(ctx context.Context, tx pgx.Tx) error { return original(ctx, tx, witness) }); err != nil {
				t.Fatal("delayed executor callback rejected durable no-open evidence", err)
			}
		})
	}
}

func TestDirectoryLegacyOpeningRejectsPresentInvalidProvenance(t *testing.T) {
	// A deliberately partial/foreign schema must not turn explicit NULL or a
	// string "1" into the absent-column schema15 provenance convention.
	for _, value := range []string{"NULL", `'null'::jsonb`, `'"1"'::jsonb`, `'0'::jsonb`, `'2'::jsonb`, `'{}'::jsonb`} {
		t.Run(value, func(t *testing.T) {
			f := fixtureForDirectoryUse(t, true, nil)
			task := f.submitDirectory("invalid-present-provenance")
			f.exec(`ALTER TABLE adtr.domain_directory_task_uses ADD COLUMN dictionary_version jsonb DEFAULT ` + value)
			lease, err := f.engine.Claim(context.Background(), "partial-schema-owner")
			if err != nil {
				t.Fatal(err)
			}
			lease, err = f.engine.Start(context.Background(), lease)
			if err != nil {
				t.Fatal(err)
			}
			out := openDirectoryLedger(context.Background(), tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
				return f.engine.WithTx(ctx, lease, v, work)
			}})
			if out.State != tasks.Failed || out.Code != "opening_denied" {
				t.Fatal("present invalid provenance opened a use", out)
			}
			if state, n := f.directoryState(task.ID); state != "reserved" || n != 1 {
				t.Fatal("invalid provenance changed the reservation", state, n)
			}
		})
	}
}
