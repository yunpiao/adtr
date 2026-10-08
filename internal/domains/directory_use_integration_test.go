//go:build integration

package domains_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	dbstore "github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

type directoryUseFixture struct {
	*accountExecFixture
	directoryGrant string
	kind           tasks.Kind
}

// Runtime cases use the production schema15 migration. Only the intentionally
// absent-dependency case uses historical14 plus partial fragments, without
// forging a version. The synthetic executor never resolves or uses credentials.
func fixtureForDirectoryUse(t *testing.T, dependencies bool, execute tasks.Executor) *directoryUseFixture {
	t.Helper()
	version := dbstore.SchemaVersion
	if !dependencies {
		version = 14
	}
	a := fixtureForAccountExecutorAtVersion(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		t.Error("ledger component reached a network adapter")
		return ldapconnection.Result{}, errors.New("unexpected network adapter")
	}, version)
	f := &directoryUseFixture{accountExecFixture: a}
	if !dependencies {
		a.tx(func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, auth.DirectoryPermissionMarks+credentialuse.DirectoryPurposeSchema+domains.DirectoryUseSchema)
			return err
		})
	}
	a.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.GrantUse); err != nil {
			return err
		}
		r, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/grant", credentialuse.Input{
			AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryPurpose,
			ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "directory-use-grant",
		}, []string{f.id}, credentialuse.DirectoryPurpose)
		f.directoryGrant = r.GrantRevision
		return err
	})
	if execute == nil {
		execute = openDirectoryLedger
	}
	f.kind = domains.DirectoryUseKindForTest(f.store, execute)
	f.installKind(t, f.kind)
	var actual int
	if err := f.conn.QueryRow(context.Background(), `SELECT version FROM adtr.schema_version`).Scan(&actual); err != nil || actual != f.schemaVersion {
		t.Fatal("fixture changed its schema version", actual, err)
	}
	return f
}

func (f *directoryUseFixture) installKind(t *testing.T, kind tasks.Kind) {
	t.Helper()
	var err error
	f.engine, err = tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), f.store.AccountKind(), kind), auth.NewTaskAuthorizer(), f.schemaVersion)
	if err != nil {
		t.Fatal(err)
	}
}

func openDirectoryLedger(ctx context.Context, ex tasks.Execution) tasks.Outcome {
	_, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), domains.OpenDirectoryUseForTest(ctx, tx, ex.Task)
	})
	if err != nil {
		return tasks.Outcome{State: tasks.Failed, Code: "opening_denied"}
	}
	return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
}

func (f *directoryUseFixture) submitDirectoryTx(ctx context.Context, tx pgx.Tx, key string) (tasks.Task, error) {
	payload, _ := json.Marshal(map[string]string{
		"credentialSource": "operation_account", "connectionRevision": "2", "connectionCredentialGeneration": "2",
		"accountId": f.account, "accountCredentialRevision": "1", "grantRoleId": "platform_admin",
		"grantRevision": f.directoryGrant, "policyRevision": strings.Repeat("a", 64),
	})
	out, err := f.engine.SubmitTx(ctx, tx, f.p, tasks.SubmitInput{TaskName: domains.DirectoryKindName, DomainID: f.id, PayloadVersion: 1, Payload: payload, IdempotencyKey: key})
	if err == nil {
		err = domains.ReserveDirectoryUseForTest(ctx, tx, out.Task)
	}
	return out.Task, err
}

func (f *directoryUseFixture) submitDirectory(key string) tasks.Task {
	f.t.Helper()
	var out tasks.Task
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = f.submitDirectoryTx(ctx, tx, key)
		return err
	})
	return out
}

func (f *directoryUseFixture) directoryState(id string) (string, int) {
	f.t.Helper()
	var state string
	var count int
	if err := f.conn.QueryRow(context.Background(), `SELECT u.state,(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.consumer_kind='domain.directory_read' AND d.object_id=u.task_id) FROM adtr.domain_directory_task_uses u WHERE u.task_id=$1`, id).Scan(&state, &count); err != nil {
		f.t.Fatal(err)
	}
	return state, count
}

func (f *directoryUseFixture) revokeDirectory() {
	f.t.Helper()
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.RevokeUse); err != nil {
			return err
		}
		_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryPurpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: f.directoryGrant, IdempotencyKey: "revoke-directory"}, []string{f.id}, credentialuse.DirectoryPurpose)
		return err
	})
}

func TestDirectoryUseFragmentMissingDependencyIntegrationFailsClosed(t *testing.T) {
	f := fixtureForDirectoryUse(t, false, nil)
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.submitDirectoryTx(ctx, tx, "blocked-fragment")
		return err
	})
	var db *pgconn.PgError
	if !errors.As(err, &db) || db.ConstraintName != "operation_account_dependency_pins" {
		t.Fatal("missing typed pins did not refuse admission", err)
	}
	var count int
	if err := f.conn.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM adtr.tasks WHERE kind='domain.directory_read')+(SELECT count(*) FROM adtr.domain_directory_task_uses)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed admission left a partial task/use", count, err)
	}
}

func TestDirectoryUseFragmentIsIndependentOfDiagnosticsAndPinsExactGrant(t *testing.T) {
	f := fixtureForDirectoryUse(t, true, nil)
	task := f.submitDirectory("first-directory")
	var generation int64
	var latest *string
	if err := f.conn.QueryRow(context.Background(), `SELECT diagnostic_generation,latest_test_task_id FROM adtr.domain_connections WHERE domain_id=$1`, f.id).Scan(&generation, &latest); err != nil || generation != 2 || latest != nil {
		t.Fatal("directory admission changed diagnostics", generation, latest, err)
	}
	// A later B2 diagnostic must neither supersede the directory ledger nor be
	// reinterpreted as a directory-use grant or dependency.
	b2 := f.submitAccount("later-diagnostic")
	if state, n := f.useState(b2.ID); state != "reserved" || n != 1 {
		t.Fatal("B2 ledger changed", state, n)
	}
	if _, err := f.engine.RunOne(context.Background(), "directory-opener"); err != nil {
		t.Fatal(err)
	}
	if state, n := f.directoryState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("directory could not finish independently", state, n)
	}
	f.revokeDirectory()
	if err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.submitDirectoryTx(ctx, tx, "revoked-directory")
		return err
	}); err == nil {
		t.Fatal("B2 grant or administrator implied directory authority")
	}
}

func TestDirectoryUseOpenedBlockerRequiresActualReturnAfterRevocationAndLeaseLoss(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f := fixtureForDirectoryUse(t, true, func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		out := openDirectoryLedger(ctx, ex)
		if out.State != tasks.Succeeded {
			return out
		}
		close(started)
		<-release
		return out
	})
	var witness tasks.QuiescedAttempt
	kind := f.kind
	acknowledge := kind.OnQuiesced
	kind.OnQuiesced = func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
		witness = q
		return acknowledge(ctx, tx, q)
	}
	f.installKind(t, kind)
	task := f.submitDirectory("blocked-directory")
	done := make(chan error, 1)
	exited := make(chan struct{})
	runCtx, cancelRun := context.WithCancel(context.Background())
	// Registered after fixture creation, so this releases and joins the worker
	// before the fixture closes its connection or drops its isolated database.
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		cancelRun()
		select {
		case <-exited:
		case <-time.After(8 * time.Second):
			t.Error("directory worker did not stop before fixture cleanup")
		}
	})
	go func() {
		defer close(exited)
		_, err := f.engine.RunOne(runCtx, "original-directory-owner")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("directory ledger did not open")
	}
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID)
	if _, err := f.engine.RecoverExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.revokeDirectory()
	f.detach()
	requireAccountBlocked(t, f.replace())
	if n, err := f.store.ReconcileReservedDirectoryUses(context.Background(), f.config, 16); err != nil || n != 0 {
		t.Fatal("terminal/expired opened use released", n, err)
	}
	if state, n := f.directoryState(task.ID); state != "opened" || n != 1 {
		t.Fatal("opened blocker lost", state, n)
	}
	once.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("actual-return acknowledgement did not finish")
	}
	if state, n := f.directoryState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("original opener did not acknowledge", state, n)
	}
	if err := f.replace(); err != nil {
		t.Fatal("proven return did not release account", err)
	}
	// The exact engine-issued witness remains idempotent even after the source
	// and credential revision have changed. No current grant/lease is needed.
	for range 2 {
		if err := f.attempt(func(ctx context.Context, tx pgx.Tx) error { return acknowledge(ctx, tx, witness) }); err != nil {
			t.Fatal("idempotent acknowledgement depended on current authority", err)
		}
	}
}

func TestDirectoryUseReservedTerminalReconciliationAndCallbackRace(t *testing.T) {
	f := fixtureForDirectoryUse(t, true, func(context.Context, tasks.Execution) tasks.Outcome {
		return tasks.Outcome{State: tasks.Failed, Code: "policy_revision_changed"}
	})
	task := f.submitDirectory("never-opened")
	original := f.kind.OnQuiesced
	var calls atomic.Int32
	kind := f.kind
	kind.OnQuiesced = func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
		n, err := f.store.ReconcileReservedDirectoryUses(ctx, f.config, 1)
		if err != nil {
			return err
		}
		if n != 1 {
			t.Error("terminal reservation did not reconcile", n)
		}
		calls.Add(1)
		return original(ctx, tx, q)
	}
	f.installKind(t, kind)
	if n, err := f.store.ReconcileReservedDirectoryUses(context.Background(), f.config, 1); err != nil || n != 0 {
		t.Fatal("queued reservation retired", n, err)
	}
	if _, err := f.engine.RunOne(context.Background(), "no-open-owner"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || f.engine.PendingQuiescence() != 0 {
		t.Fatal("durable no-open evidence not acknowledged")
	}
	if state, n := f.directoryState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("reservation unresolved", state, n)
	}
	var reason string
	if err := f.conn.QueryRow(context.Background(), `SELECT quiescence_reason FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND opened_at IS NULL AND opener_owner IS NULL AND opener_fencing_token IS NULL AND opener_attempt IS NULL`, task.ID).Scan(&reason); err != nil || reason != "never_opened_terminal" {
		t.Fatal("incorrect no-open evidence", reason, err)
	}
}

func TestDirectoryUseUnwitnessedOpenedUseRefusesCleanupAndDependencyMutation(t *testing.T) {
	f := fixtureForDirectoryUse(t, true, nil)
	task := f.submitDirectory("lost-directory-process")
	lease, err := f.engine.Claim(context.Background(), "lost-directory-owner")
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
	for _, wrong := range []string{"protocol", "owner", "fence", "attempt", "task"} {
		err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
			protocol, owner, fence, attempt, id := "directory_use_cleanup_v1", lease.Owner, lease.Token, lease.Task.Attempt, task.ID
			switch wrong {
			case "protocol":
				protocol = "account_use_cleanup_v1"
			case "owner":
				owner = "wrong-owner"
			case "fence":
				fence++
			case "attempt":
				attempt++
			case "task":
				id = "other-task"
			}
			if _, err := tx.Exec(ctx, `SELECT set_config('adtr.directory_use_protocol',$1,true),set_config('adtr.directory_use_task',$2,true),set_config('adtr.directory_use_owner',$3,true),set_config('adtr.directory_use_fence',$4,true),set_config('adtr.directory_use_attempt',$5,true)`, protocol, id, owner, strconv.FormatInt(fence, 10), strconv.Itoa(attempt)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE adtr.domain_directory_task_uses SET state='quiesced',quiesced_at=clock_timestamp(),quiescence_reason='executor_returned' WHERE task_id=$1`, task.ID)
			return err
		})
		if err == nil {
			t.Fatal("mismatched cleanup evidence accepted", wrong)
		}
	}
	for _, statement := range []string{
		`DELETE FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read' AND object_id=$1`,
		`UPDATE adtr.operation_account_dependencies SET consumer_kind='unknown.other',account_credential_revision=NULL,connection_credential_generation=NULL WHERE consumer_kind='domain.directory_read' AND object_id=$1`,
		`UPDATE adtr.domain_directory_task_uses SET policy_revision=repeat('b',64) WHERE task_id=$1`,
		`DELETE FROM adtr.domain_directory_task_uses WHERE task_id=$1`,
	} {
		if err := f.attempt(func(ctx context.Context, tx pgx.Tx) error { _, err := tx.Exec(ctx, statement, task.ID); return err }); err == nil {
			t.Fatal("unwitnessed use/dependency mutation committed")
		}
	}
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 day' WHERE task_id=$1`, task.ID)
	if _, err := f.engine.RecoverExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.detach()
	if n, err := f.store.ReconcileReservedDirectoryUses(context.Background(), f.config, 100); err != nil || n != 0 {
		t.Fatal("process loss released opened use", n, err)
	}
	requireAccountBlocked(t, f.replace())
}

func TestDirectoryUseFragmentRefusesReservedKindCollisionsAtomically(t *testing.T) {
	for _, mode := range []string{"task", "dependency"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtureForAccountExecutorAtVersion(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
				return fixtureObservation(), nil
			}, 14)
			if mode == "task" {
				f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts)
 VALUES('unknown-directory-task',$1,$2,'domain.directory_read',1,'{}','unknown-original-hash',$3,'opaque-original-epoch','unknown-original-key','queued',1)`, f.p.TenantID, f.id, f.p.ActorID)
			} else {
				f.exec(`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES($1,$2,$3,'domain.directory_read','unknown-directory-object')`, f.p.TenantID, f.id, f.account)
			}
			for range 2 {
				err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
					_, err := tx.Exec(ctx, auth.DirectoryPermissionMarks+credentialuse.DirectoryPurposeSchema+domains.DirectoryDependencySchema+domains.DirectoryUseSchema)
					return err
				})
				var db *pgconn.PgError
				want := "domain_directory_" + mode + "_kind_reserved"
				if !errors.As(err, &db) || db.ConstraintName != want {
					t.Fatal("reserved namespace was adopted", mode, err)
				}
				var intact bool
				if err := f.conn.QueryRow(context.Background(), `SELECT version=$1 AND to_regclass('adtr.domain_directory_task_uses') IS NULL AND to_regclass('adtr.domain_directory_use_identity') IS NULL AND to_regprocedure('adtr.credential_use_role_eligible_for_purpose(text,text,text,text)') IS NULL AND EXISTS(SELECT FROM pg_constraint WHERE conrelid='adtr.operation_account_dependencies'::regclass AND conname='operation_account_dependency_pins' AND position('domain.directory_read' in pg_get_constraintdef(oid))=0) FROM adtr.schema_version`, f.schemaVersion).Scan(&intact); err != nil || !intact {
					t.Fatal("collision left partial schema/version", err)
				}
				var count int
				statement := `SELECT count(*) FROM adtr.tasks WHERE task_id='unknown-directory-task' AND payload='{}'::jsonb AND payload_hash='unknown-original-hash' AND authorization_version='opaque-original-epoch'`
				if mode == "dependency" {
					statement = `SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read' AND object_id='unknown-directory-object' AND account_credential_revision IS NULL AND connection_credential_generation IS NULL`
				}
				if err := f.conn.QueryRow(context.Background(), statement).Scan(&count); err != nil || count != 1 {
					t.Fatal("collision data was rewritten", count, err)
				}
			}
		})
	}
}
