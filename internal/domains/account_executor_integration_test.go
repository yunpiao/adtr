//go:build integration

package domains_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	dbstore "github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

type accountExecFixture struct {
	*execFixture
	account, grant string
}

func fixtureForAccountExecutor(t *testing.T, probe func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error)) *accountExecFixture {
	t.Helper()
	return fixtureForAccountExecutorAtVersion(t, probe, dbstore.SchemaVersion)
}

func fixtureForAccountExecutorAtVersion(t *testing.T, probe func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error), version int) *accountExecFixture {
	t.Helper()
	f := fixtureForExecutorAtVersion(t, probe, version)
	a := &accountExecFixture{execFixture: f}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetSelfContext(ctx, tx, f.p, credentialuse.ConfirmMFA); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE adtr.users SET mfa_secret='synthetic-mfa' WHERE tenant_id=$1 AND id=$2`, f.p.TenantID, f.p.ActorID)
		return err
	})
	username, password, label := "account-reader@example.test", "Synthetic registered pair", "Synthetic account"
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		r, err := operationaccounts.New(f.runtime).MutateTx(ctx, tx, f.p, "/create", operationaccounts.Input{DomainID: f.id, ExpectedDomainRevision: "1", Username: &username, Password: &password, Label: &label, IdempotencyKey: "account-create"}, []string{f.id})
		a.account = r.AccountID
		return err
	})
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.GrantUse); err != nil {
			return err
		}
		r, err := credentialuse.New().MutateTx(ctx, tx, f.p, "/grant", credentialuse.Input{AccountID: a.account, RoleID: "platform_admin", Purpose: credentialuse.Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "grant-account"}, []string{f.id})
		a.grant = r.GrantRevision
		return err
	})
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, f.p, "reference"); err != nil {
			return err
		}
		_, err := f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "reference", domains.SourceInput{DomainID: f.id, ExpectedRevision: "1", ExpectedConnectionCredentialGeneration: "1", AccountID: a.account, ExpectedAccountRevision: "1", ExpectedAccountCredentialRevision: "1", ExpectedGrantRevision: a.grant, IdempotencyKey: "bind-account"}, []string{f.id})
		return err
	})
	var err error
	f.engine, err = tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), f.store.AccountKind()), auth.NewTaskAuthorizer(), version)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (a *accountExecFixture) attempt(work func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, a.config)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = a.engine.CheckSchemaTx(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))`, a.p.TenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, a.p.TenantID, a.p.ActorID); err != nil {
		return err
	}
	if err = work(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (a *accountExecFixture) submitAccount(key string) tasks.Task {
	a.t.Helper()
	var out tasks.Submission
	a.tx(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = a.store.SubmitTx(ctx, tx, a.engine, a.p, domains.Input{DomainID: a.id, ExpectedRevision: "2", IdempotencyKey: key})
		return err
	})
	if out.Task.Kind != domains.AccountKindName {
		a.t.Fatal("wrong account kind")
	}
	return out.Task
}
func (a *accountExecFixture) revoke() {
	a.t.Helper()
	a.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, a.p, credentialuse.RevokeUse); err != nil {
			return err
		}
		r, err := credentialuse.New().MutateTx(ctx, tx, a.p, "/revoke", credentialuse.Input{AccountID: a.account, RoleID: "platform_admin", Purpose: credentialuse.Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: a.grant, IdempotencyKey: "revoke-account"}, []string{a.id})
		a.grant = r.GrantRevision
		return err
	})
}
func (a *accountExecFixture) detach() {
	a.t.Helper()
	a.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, a.p, "detach"); err != nil {
			return err
		}
		_, err := a.store.MutateSourceTx(ctx, tx, a.p, "platform_admin", "detach", domains.SourceInput{DomainID: a.id, ExpectedRevision: "2", ExpectedConnectionCredentialGeneration: "2", IdempotencyKey: "detach-account"}, []string{a.id})
		return err
	})
}
func (a *accountExecFixture) replace() error {
	username, password := "new-reader@example.test", "Synthetic replacement pair"
	return a.attempt(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, a.p, credentialuse.MutateAccount); err != nil {
			return err
		}
		_, err := operationaccounts.New(a.runtime).MutateTx(ctx, tx, a.p, "/update", operationaccounts.Input{AccountID: a.account, ExpectedRevision: "1", Username: &username, Password: &password, IdempotencyKey: "replace-account"}, []string{a.id})
		return err
	})
}
func (a *accountExecFixture) useState(id string) (string, int) {
	a.t.Helper()
	var state string
	var count int
	err := a.conn.QueryRow(context.Background(), `SELECT u.state,(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.account_connection_test' AND d.object_id=u.task_id) FROM adtr.domain_account_task_uses u WHERE u.task_id=$1`, id).Scan(&state, &count)
	if err != nil {
		a.t.Fatal(err)
	}
	return state, count
}
func requireAccountBlocked(t *testing.T, err error) {
	t.Helper()
	var e *operationaccounts.Error
	if !errors.As(err, &e) || e.Code != "account_in_use" {
		t.Fatal("replacement not blocked by unresolved use", err)
	}
}

func TestAccountExecutorUsesOnlyRegisteredPairAndLabelEditDoesNotRevoke(t *testing.T) {
	var owned []byte
	var f *accountExecFixture
	f = fixtureForAccountExecutor(t, func(ctx context.Context, cfg ldapconnection.Config, p ldapconnection.Credential) (ldapconnection.Result, error) {
		if p.Username != "account-reader@example.test" || string(p.Password) != "Synthetic registered pair" {
			t.Error("executor did not use exact synthetic registered pair")
		}
		owned = p.Password
		var opened, custom int
		if err := f.conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM adtr.domain_account_task_uses WHERE domain_id=$1 AND state='opened'),(SELECT count(*) FROM adtr.domain_credentials WHERE domain_id=$1)`, f.id).Scan(&opened, &custom); err != nil || opened != 1 || custom != 0 {
			t.Error("snapshot opened without committed ledger or retained custom pair", err)
		}
		// Label-only mutation preserves grant, credential revision and actor epoch.
		label := "Renamed synthetic account"
		if err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
			_, err := operationaccounts.New(f.runtime).MutateTx(ctx, tx, f.p, "/update", operationaccounts.Input{AccountID: f.account, ExpectedRevision: "1", Label: &label, IdempotencyKey: "label-only"}, []string{f.id})
			return err
		}); err != nil {
			t.Error(err)
		}
		for _, stage := range []ldapconnection.Stage{ldapconnection.StageDial, ldapconnection.StageTLS, ldapconnection.StageBind, ldapconnection.StageSearch} {
			if err := cfg.Authorize(ctx, stage); err != nil {
				return ldapconnection.Result{}, err
			}
		}
		return fixtureObservation(), nil
	})
	task := f.submitAccount("use-account")
	if state, n := f.useState(task.ID); state != "reserved" || n != 1 {
		t.Fatal("missing atomic reservation", state, n)
	}
	if _, err := f.engine.RunOne(context.Background(), "account-worker"); err != nil {
		t.Fatal(err)
	}
	for _, b := range owned {
		if b != 0 {
			t.Fatal("owned password not cleared before quiescence")
		}
	}
	if state, n := f.useState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("use not acknowledged after return", state, n)
	}
	if c := f.connection(); c.ConnectionState != "verified" {
		t.Fatal("current account success not verified", c.ConnectionState)
	}
}

func TestAccountRevocationLeavesSafeReplayAndReservedTerminalMaintenance(t *testing.T) {
	var calls atomic.Int32
	f := fixtureForAccountExecutor(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		calls.Add(1)
		return fixtureObservation(), nil
	})
	task := f.submitAccount("reserved")
	f.revoke()
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		out, err := f.store.SubmitTx(ctx, tx, f.engine, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "2", IdempotencyKey: "reserved"})
		if err == nil && (!out.Replayed || out.Task.ID != task.ID || out.Task.AuthorizationVersion != task.AuthorizationVersion) {
			t.Error("replay changed immutable task")
		}
		return err
	})
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.store.SubmitTx(ctx, tx, f.engine, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "2", IdempotencyKey: "forbidden-new"})
		return err
	})
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("admin consumed without explicit grant", err)
	}
	if _, err = f.engine.RunOne(context.Background(), "denied-start"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("transport reached after grant revocation")
	}
	if state, n := f.useState(task.ID); state != "reserved" || n != 1 {
		t.Fatal("terminalization released resource without maintenance")
	}
	n, err := f.store.ReconcileReservedAccountUses(context.Background(), f.config, 16)
	if err != nil || n != 1 {
		t.Fatal("reserved terminal maintenance failed", n, err)
	}
	if state, n := f.useState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("reserved use remains", state, n)
	}
	f.detach()
	if err = f.replace(); err != nil {
		t.Fatal("quiesced history prevented later pair replacement", err)
	}
}

func TestOpenedUseBlocksReplacementUntilActualReturnDespiteExpiryRevokeAndDetach(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var owned []byte
	f := fixtureForAccountExecutor(t, func(_ context.Context, _ ldapconnection.Config, p ldapconnection.Credential) (ldapconnection.Result, error) {
		owned = p.Password
		close(started)
		<-release
		return fixtureObservation(), nil
	})
	task := f.submitAccount("blocked-open")
	done := make(chan error, 1)
	go func() { _, err := f.engine.RunOne(context.Background(), "old-owner"); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not open")
	}
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID)
	if _, err := f.engine.RecoverExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.revoke()
	f.detach()
	requireAccountBlocked(t, f.replace())
	if n, err := f.store.ReconcileReservedAccountUses(context.Background(), f.config, 16); err != nil || n != 0 {
		t.Fatal("maintenance changed opened use", n, err)
	}
	if state, n := f.useState(task.ID); state != "opened" || n != 1 {
		t.Fatal("terminal task lost opened blocker", state, n)
	}
	once.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("return acknowledgement did not finish")
	}
	for _, b := range owned {
		if b != 0 {
			t.Fatal("owned pair survived callback")
		}
	}
	if state, n := f.useState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("saved opener did not acknowledge after fencing change", state, n)
	}
	if err := f.replace(); err != nil {
		t.Fatal("replacement after proven stop failed", err)
	}
}

func TestProcessLossKeepsOpenedAccountUseQuarantined(t *testing.T) {
	f := fixtureForAccountExecutor(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		return fixtureObservation(), nil
	})
	task := f.submitAccount("lost-process")
	lease, err := f.engine.Claim(context.Background(), "lost-owner")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.engine.Start(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	outcome := f.store.AccountKind().Execute(context.Background(), tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, v, work)
	}})
	if outcome.State != tasks.Succeeded {
		t.Fatal(outcome)
	}
	for _, wrong := range []string{"wrong-owner", "wrong-fence", "wrong-attempt"} {
		err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
			owner, fence, attempt := lease.Owner, lease.Token, lease.Task.Attempt
			switch wrong {
			case "wrong-owner":
				owner = "unrelated-owner"
			case "wrong-fence":
				fence++
			case "wrong-attempt":
				attempt++
			}
			if _, err := tx.Exec(ctx, `SELECT set_config('adtr.account_use_protocol','account_use_cleanup_v1',true),set_config('adtr.account_use_task',$1,true),set_config('adtr.account_use_owner',$2,true),set_config('adtr.account_use_fence',$3,true),set_config('adtr.account_use_attempt',$4,true)`, task.ID, owner, strconv.FormatInt(fence, 10), strconv.Itoa(attempt)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE adtr.domain_account_task_uses SET state='quiesced',quiesced_at=clock_timestamp(),quiescence_reason='executor_returned' WHERE task_id=$1`, task.ID)
			return err
		})
		if err == nil {
			t.Fatal("wrong opener identity released account use", wrong)
		}
	}
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.account_connection_test' AND object_id=$1`, task.ID)
		return err
	})
	if err == nil {
		t.Fatal("opened dependency removed without quiescence")
	}
	// Direct Execute intentionally has no engine-issued return witness: this
	// models losing the only in-process evidence after the transport stops.
	if _, err = f.engine.Finish(context.Background(), lease, outcome); err != nil {
		t.Fatal(err)
	}
	f.detach()
	restart, err := tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), f.store.AccountKind()), auth.NewTaskAuthorizer(), dbstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err = restart.RetryQuiescence(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, err := f.store.ReconcileReservedAccountUses(context.Background(), f.config, 16); err != nil || n != 0 {
		t.Fatal("restart reconciled opened use", n, err)
	}
	if state, n := f.useState(task.ID); state != "opened" || n != 1 {
		t.Fatal("process-loss blocker discarded", state, n)
	}
	requireAccountBlocked(t, f.replace())
}

func TestConnectionTestFamilyReplayCannotCrossActorOrSource(t *testing.T) {
	f := fixtureForAccountExecutor(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		return fixtureObservation(), nil
	})
	original := f.submitAccount("family-intent")
	var other int64
	// This role already has a live use grant. Adding a member must follow the
	// same governed role-management boundary as the production access handler.
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.ManageRoles); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change,password_updated_at,mfa_secret) VALUES($1,'another-admin','synthetic-hash','platform_admin',false,clock_timestamp(),'synthetic-mfa') RETURNING id`, f.p.TenantID).Scan(&other)
	})
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.store.SubmitTx(ctx, tx, f.engine, tasks.Principal{TenantID: f.p.TenantID, ActorID: other}, domains.Input{DomainID: f.id, ExpectedRevision: "2", IdempotencyKey: "family-intent"})
		return err
	})
	requireDomainConflict(t, err, "idempotency_conflict")
	f.detach()
	username, password := "fresh-custom@example.test", "Synthetic fresh custom pair"
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, f.p, "custom"); err != nil {
			return err
		}
		_, err := f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "custom", domains.SourceInput{DomainID: f.id, ExpectedRevision: "3", ExpectedConnectionCredentialGeneration: "3", Username: &username, Password: &password, IdempotencyKey: "new-custom"}, []string{f.id})
		return err
	})
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.store.SubmitTx(ctx, tx, f.engine, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "4", IdempotencyKey: "family-intent"})
		return err
	})
	requireDomainConflict(t, err, "idempotency_conflict")
	var count int
	if err = f.conn.QueryRow(context.Background(), `SELECT count(*) FROM adtr.tasks WHERE tenant_id=$1 AND domain_id=$2 AND idempotency_key='family-intent'`, f.p.TenantID, f.id).Scan(&count); err != nil || count != 1 {
		t.Fatal("family key created another task", count, err)
	}
	if state, n := f.useState(original.ID); state != "reserved" || n != 1 {
		t.Fatal("source switch discarded unresolved reservation")
	}
}

func requireDomainConflict(t *testing.T, err error, code string) {
	t.Helper()
	var e *domains.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatal("wrong domain conflict", err)
	}
}

func TestCancelledReservationRetiresOnlyInIndependentMaintenance(t *testing.T) {
	f := fixtureForAccountExecutor(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		t.Fatal("cancelled task reached transport")
		return ldapconnection.Result{}, nil
	})
	task := f.submitAccount("cancel-before-claim")
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.engine.CancelTx(ctx, tx, f.p, task.ID)
		return err
	})
	if state, n := f.useState(task.ID); state != "reserved" || n != 1 {
		t.Fatal("CancelTx removed account reservation")
	}
	if n, err := f.store.ReconcileReservedAccountUses(context.Background(), f.config, 1); err != nil || n != 1 {
		t.Fatal("cancelled reservation not reconciled", n, err)
	}
	var reason string
	if err := f.conn.QueryRow(context.Background(), `SELECT quiescence_reason FROM adtr.domain_account_task_uses WHERE task_id=$1 AND opened_at IS NULL AND opener_owner IS NULL AND opener_fencing_token IS NULL AND opener_attempt IS NULL`, task.ID).Scan(&reason); err != nil || reason != "never_opened_terminal" {
		t.Fatal("wrong terminal reservation evidence", reason, err)
	}
}

func TestReservedReconciliationCanWinFinishBeforeQuiescenceCallback(t *testing.T) {
	f := fixtureForAccountExecutor(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		t.Fatal("never-opened executor reached adapter")
		return ldapconnection.Result{}, nil
	})
	task := f.submitAccount("finish-reconcile-race")
	kind := f.store.AccountKind()
	kind.Execute = func(context.Context, tasks.Execution) tasks.Outcome {
		return tasks.Outcome{State: tasks.Failed, Code: "policy_revision_changed"}
	}
	original := kind.OnQuiesced
	var called atomic.Int32
	kind.OnQuiesced = func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
		// Engine has already committed Finish. Separate maintenance is allowed to
		// win this window before this callback acquires its tenant/task locks.
		n, err := f.store.ReconcileReservedAccountUses(ctx, f.config, 1)
		if err != nil {
			return err
		}
		if n != 1 {
			t.Error("terminal reservation did not reconcile before callback", n)
		}
		called.Add(1)
		return original(ctx, tx, q)
	}
	engine, err := tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), kind), auth.NewTaskAuthorizer(), dbstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunOne(context.Background(), "reconcile-first"); err != nil {
		t.Fatal("callback rejected durable never-opened acknowledgement", err)
	}
	if called.Load() != 1 || engine.PendingQuiescence() != 0 {
		t.Fatal("never-opened acknowledgement retained forever")
	}
	if state, n := f.useState(task.ID); state != "quiesced" || n != 0 {
		t.Fatal("race left unresolved reservation", state, n)
	}
}

func TestAccountGrantRevocationAtEveryExecutorBarrier(t *testing.T) {
	barriers := []string{"snapshot", "dns", "dial", "tls", "bind", "search", "publication"}
	stages := []ldapconnection.Stage{ldapconnection.StageDNS, ldapconnection.StageDial, ldapconnection.StageTLS, ldapconnection.StageBind, ldapconnection.StageSearch}
	for _, barrier := range barriers {
		t.Run(barrier, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			wait := func(ctx context.Context) error {
				close(started)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			var passed atomic.Int32
			var called atomic.Bool
			f := fixtureForAccountExecutor(t, func(ctx context.Context, cfg ldapconnection.Config, p ldapconnection.Credential) (ldapconnection.Result, error) {
				called.Store(true)
				for _, stage := range stages {
					if string(stage) == barrier {
						if err := wait(ctx); err != nil {
							return ldapconnection.Result{}, err
						}
					}
					if err := cfg.Authorize(ctx, stage); err != nil {
						return ldapconnection.Result{}, err
					}
					passed.Add(1)
				}
				if barrier == "publication" {
					if err := wait(ctx); err != nil {
						return ldapconnection.Result{}, err
					}
				}
				return fixtureObservation(), nil
			})
			if barrier == "snapshot" {
				kind := f.store.AccountKind()
				execute := kind.Execute
				kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
					if err := wait(ctx); err != nil {
						return tasks.Outcome{State: tasks.Failed, Code: "cancelled"}
					}
					return execute(ctx, ex)
				}
				var err error
				f.engine, err = tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), kind), auth.NewTaskAuthorizer(), dbstore.SchemaVersion)
				if err != nil {
					t.Fatal(err)
				}
			}
			task := f.submitAccount("revoke-barrier")
			done := make(chan error, 1)
			go func() { _, err := f.engine.RunOne(context.Background(), "barrier-owner"); done <- err }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("executor did not reach barrier")
			}
			f.revoke()
			once.Do(func() { close(release) })
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("revoked executor did not return")
			}
			wanted := 0
			switch barrier {
			case "snapshot":
				if called.Load() {
					t.Fatal("revoked snapshot reached adapter")
				}
			case "publication":
				wanted = len(stages)
			default:
				for i, stage := range stages {
					if string(stage) == barrier {
						wanted = i
					}
				}
			}
			if int(passed.Load()) != wanted {
				t.Fatal("executor passed an unauthorized later phase", passed.Load(), wanted)
			}
			var diagnostics int
			if err := f.conn.QueryRow(context.Background(), `SELECT count(*) FROM adtr.domain_diagnostics WHERE task_id=$1`, task.ID).Scan(&diagnostics); err != nil || diagnostics != 0 {
				t.Fatal("revoked observation published", diagnostics, err)
			}
			if barrier == "snapshot" {
				if state, n := f.useState(task.ID); state != "reserved" || n != 1 {
					t.Fatal("unopened barrier changed reservation")
				}
				if n, err := f.store.ReconcileReservedAccountUses(context.Background(), f.config, 1); err != nil || n != 1 {
					t.Fatal("snapshot reservation did not retire", n, err)
				}
			} else if state, n := f.useState(task.ID); state != "quiesced" || n != 0 {
				t.Fatal("returned revoked executor retained blocker", state, n)
			}
		})
	}
}
