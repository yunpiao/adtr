//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Start with the real historical13 fixture and its guarded records, then run
// the actual migration14 sequence. Never migrate15 and forge an older marker.
func newDirectoryUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newOperationalUpgradeFixture(t)
	if err := MigrateDirectoryBaselineForTest(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 14
	f.assertDirectoryBaseline()
	if err := Ready(f.ctx, f.config); err == nil {
		t.Fatal("current runtime accepted the historical directory baseline")
	}
	return f
}

func (f *operationalUpgradeFixture) assertDirectoryBaseline() {
	f.t.Helper()
	var intact bool
	err := f.conn.QueryRow(f.ctx, `SELECT version=14
 AND to_regclass('adtr.operational_log_events') IS NOT NULL
 AND to_regclass('adtr.domain_directory_task_uses') IS NULL
 AND to_regclass('adtr.domain_directory_observations') IS NULL
 AND to_regclass('adtr.domain_directory_use_identity') IS NULL
 AND to_regprocedure('adtr.credential_use_role_eligible_for_purpose(text,text,text,text)') IS NULL
 AND NOT EXISTS(SELECT FROM pg_constraint WHERE conrelid='adtr.access_permissions'::regclass AND conname='access_permissions_mark_check' AND position('directory_assets' in pg_get_constraintdef(oid))>0)
 FROM adtr.schema_version`).Scan(&intact)
	if err != nil || !intact {
		f.t.Fatal("expected actual schema14 without directory extension", err)
	}
}

func (f *operationalUpgradeFixture) assertDirectoryInstalled() {
	f.t.Helper()
	var intact bool
	err := f.conn.QueryRow(f.ctx, `SELECT version=15
 AND to_regclass('adtr.domain_directory_task_uses') IS NOT NULL
 AND to_regclass('adtr.domain_directory_observations') IS NOT NULL
 AND to_regclass('adtr.domain_directory_use_identity') IS NOT NULL
 AND to_regprocedure('adtr.credential_use_role_eligible_for_purpose(text,text,text,text)') IS NOT NULL
 AND EXISTS(SELECT FROM pg_constraint WHERE conrelid='adtr.operation_account_dependencies'::regclass AND conname='operation_account_dependency_pins' AND position('domain.directory_read' in pg_get_constraintdef(oid))>0)
 AND (SELECT count(*)=3 FROM pg_trigger WHERE tgrelid='adtr.domain_directory_observations'::regclass AND NOT tgisinternal)
 AND EXISTS(SELECT FROM pg_trigger WHERE tgname='credential_use_directory_permission_epoch' AND NOT tgisinternal)
 FROM adtr.schema_version`).Scan(&intact)
	if err != nil || !intact {
		f.t.Fatal("directory migration missing atomic schema contracts", err)
	}
}

func (f *operationalUpgradeFixture) auditProjection() string {
	f.t.Helper()
	var raw string
	if err := f.conn.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY to_jsonb(a)::text),'[]'::jsonb)::text FROM adtr.audit_source a`).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	return raw
}

func TestVersionFourteenDirectoryUpgradePreservesRecordsAndGrants(t *testing.T) {
	f := newDirectoryUpgradeFixture(t)
	engine, opened := f.controlledEngine()
	history := f.submitAccount(engine, "history")
	run, done := f.start(engine, opened, history, "directory-upgrade-history")
	f.finish(run, done, false)
	f.assertUse(history.ID, "quiesced", 0)
	reserved := f.submitAccount(engine, "reserved")
	// Existing source projections and unknown metadata must survive unchanged.
	f.exec(`INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result)
 VALUES($1,$2,'history','domain_credential_reference',1,2,2,'saved');
 INSERT INTO adtr.operation_account_audit(tenant_id,actor_id,domain_id,account_id,action,old_revision,new_revision,old_credential_revision,new_credential_revision,result)
 VALUES($1,$2,'history','history-account','operation_account_create',0,1,0,1,'saved');
 INSERT INTO adtr.resource_audit(tenant_id,actor_id,action,target_id) VALUES($1,$2,'historical_unknown','preserve-original');`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID, f.actor.ActorID)
	before, projection := f.snapshot(), f.auditProjection()
	delete(before, "schema_version")
	results := make(chan error, 3)
	for range 3 {
		go func() { results <- MigrateDirectoryV2BaselineForTest(f.ctx, f.config) }()
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal("concurrent directory upgrade", err)
		}
	}
	if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err != nil {
		t.Fatal("repeat migration", err)
	}
	f.assertDirectoryInstalled()
	after := f.snapshot()
	for table, data := range before {
		if after[table] != data {
			t.Fatalf("directory upgrade rewrote historical table %s", table)
		}
	}
	if projection != f.auditProjection() {
		t.Fatal("directory upgrade reinterpreted historical audit projections")
	}
	f.assertUse(reserved.ID, "reserved", 1)
	var denied bool
	if err := f.conn.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read')
 AND NOT EXISTS(SELECT FROM adtr.access_permissions WHERE mark='directory_assets')
 AND NOT EXISTS(SELECT FROM adtr.domain_directory_task_uses)
 AND NOT EXISTS(SELECT FROM adtr.domain_directory_observations)`).Scan(&denied); err != nil || !denied {
		t.Fatal("migration granted or manufactured directory authority", err)
	}
	if err := Ready(f.ctx, f.config); err == nil {
		t.Fatal("schema16 runtime accepted historical schema15")
	}
	f.assertOldEngineRejected(engine, run.task)
	if err := MigrateDirectoryBaselineForTest(f.ctx, f.config); err == nil {
		t.Fatal("historical fixture helper downgraded an installed schema15")
	}
	f.assertDirectoryInstalled()
}

func (f *operationalUpgradeFixture) requireDirectoryUpgradeBlocked() {
	f.t.Helper()
	before, catalog := f.snapshot(), f.catalogSnapshot()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	results := make(chan error, 3)
	for range 3 {
		go func() { results <- MigrateDirectoryV2BaselineForTest(ctx, f.config) }()
	}
	for range 3 {
		if err := <-results; !errors.Is(err, ErrCredentialUseMigrationBlocked) {
			f.t.Fatal("directory migration did not promptly refuse opened B2 use", err)
		}
	}
	f.assertDirectoryBaseline()
	if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
		f.t.Fatal("blocked directory migration modified schema or records")
	}
}

func TestVersionFourteenDirectoryUpgradeRequiresActualB2ExecutorQuiescence(t *testing.T) {
	f := newDirectoryUpgradeFixture(t)
	engine, opened := f.controlledEngine()
	task := f.submitAccount(engine, "first")
	run, done := f.start(engine, opened, task, "directory-upgrade-b2")
	f.requireDirectoryUpgradeBlocked()
	// Age, expiration and a terminal task do not prove executor return.
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 day' WHERE task_id=$1`, task.ID)
	if n, err := engine.RecoverExpired(f.ctx); err != nil || n != 1 {
		t.Fatal("recover historical B2 attempt", n, err)
	}
	f.assertUse(task.ID, "opened", 1)
	f.requireDirectoryUpgradeBlocked()
	f.raceUpgradeAgainstReturn(engine, run, done, func(ctx context.Context, cfg *pgx.ConnConfig) error {
		return MigrateDirectoryV2BaselineForTest(ctx, cfg)
	})
	f.assertDirectoryBaseline()
	f.assertUse(task.ID, "quiesced", 0)
	if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err != nil {
		t.Fatal("upgrade after genuine B2 return", err)
	}
	f.assertDirectoryInstalled()
}

// Put an upgrade first in the gate queue, then return the executor. Refusal must
// roll back promptly so its still-exact-schema acknowledgement can acquire the
// shared gate. This exercises both the B2 and directory cleanup implementations.
func (f *operationalUpgradeFixture) raceUpgradeAgainstReturn(engine *tasks.Engine, run *operationalUpgradeExecution, done <-chan operationalUpgradeRun, upgrade func(context.Context, *pgx.ConnConfig) error) {
	f.t.Helper()
	gate, err := pgx.ConnectConfig(f.ctx, f.config.Copy())
	if err != nil {
		f.t.Fatal(err)
	}
	defer gate.Close(context.Background())
	tx, err := gate.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = tasks.CheckSchemaTx(f.ctx, tx, f.schemaVersion); err != nil {
		f.t.Fatal(err)
	}
	config := f.config.Copy()
	config.RuntimeParams["application_name"] = "adtr-directory-upgrade-race"
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	migration := make(chan error, 1)
	go func() { migration <- upgrade(ctx, config) }()
	f.awaitMigrationGate(config.RuntimeParams["application_name"])
	run.stop()
	select {
	case <-run.returned:
	case <-time.After(time.Second):
		f.t.Fatal("executor did not return")
	}
	f.awaitHistoricalEngineGate(engine, 1)
	if err = tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	select {
	case err = <-migration:
		if !errors.Is(err, ErrCredentialUseMigrationBlocked) {
			f.t.Fatal("upgrade did not refuse before old executor acknowledgement", err)
		}
	case <-time.After(3 * time.Second):
		f.t.Fatal("upgrade retained the exclusive gate needed by executor cleanup")
	}
	f.finish(run, done, true)
	if engine.PendingQuiescence() != 0 {
		f.t.Fatal("exact-schema cleanup remained pending")
	}
}

func TestVersionFourteenDirectoryUpgradeRejectsReservedKindCollisionsAtomically(t *testing.T) {
	for _, mode := range []string{"queued_task", "terminal_task", "dependency"} {
		t.Run(mode, func(t *testing.T) {
			f := newDirectoryUpgradeFixture(t)
			want := ErrDirectoryTaskKindCollision
			if mode == "dependency" {
				want = ErrDirectoryDependencyKindCollision
				f.exec(`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id)
 VALUES($1,'first','first-account','domain.directory_read','unknown-original-directory')`, f.actor.TenantID)
			} else {
				state := "queued"
				if mode == "terminal_task" {
					state = "succeeded"
				}
				f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts)
 SELECT 'legacy-directory-task',$1,'first','domain.directory_read',1,'{"original":"unrelated"}',repeat('d',64),id,authorization_version::text,'legacy-directory-key',$2,1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID, state)
			}
			before, catalog := f.snapshot(), f.catalogSnapshot()
			results := make(chan error, 3)
			for range 3 {
				go func() { results <- MigrateDirectoryV2BaselineForTest(f.ctx, f.config) }()
			}
			for range 3 {
				if err := <-results; !errors.Is(err, want) {
					t.Fatal("reserved directory identity was adopted", err)
				}
			}
			f.assertDirectoryBaseline()
			if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
				t.Fatal("directory collision left changed rows, constraints, triggers or partial schema")
			}
		})
	}
}

// Retain the historical standalone preflight coverage. Production migration16
// and its rollback/cleanup races are additionally exercised by the v2 upgrade
// suite, without fabricating a version stamp or a cleanup witness.
func futureDirectoryUpgradePreflight(ctx context.Context, cfg *pgx.ConnConfig) error {
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734192801)`); err != nil {
		return err
	}
	var installed int
	if err = tx.QueryRow(ctx, `SELECT version FROM adtr.schema_version`).Scan(&installed); err != nil {
		return err
	}
	return requireCredentialUseQuiescence(ctx, tx, installed, 16)
}

func (f *operationalUpgradeFixture) submitDirectory(engine *tasks.Engine, domain string) tasks.Task {
	f.t.Helper()
	var out tasks.Submission
	f.tx(func(tx pgx.Tx) error {
		var grant string
		if err := tx.QueryRow(f.ctx, `SELECT grant_revision::text FROM adtr.operation_account_use_grants WHERE tenant_id=$1 AND domain_id=$2 AND role_id='platform_admin' AND purpose='domain.directory_read'`, f.actor.TenantID, domain).Scan(&grant); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]string{"credentialSource": "operation_account", "connectionRevision": "2", "connectionCredentialGeneration": "2", "accountId": domain + "-account", "accountCredentialRevision": "1", "grantRoleId": "platform_admin", "grantRevision": grant, "policyRevision": strings.Repeat("a", 64)})
		if err != nil {
			return err
		}
		out, err = engine.SubmitTx(f.ctx, tx, f.actor, tasks.SubmitInput{TaskName: domains.DirectoryKindName, DomainID: domain, PayloadVersion: 1, Payload: payload, IdempotencyKey: "directory-" + domain})
		if err != nil {
			return err
		}
		_, err = tx.Exec(f.ctx, `INSERT INTO adtr.domain_directory_task_uses(tenant_id,domain_id,task_id,account_id,account_credential_revision,connection_revision,connection_credential_generation,policy_revision,grant_role_id,grant_revision,actor_id,purpose,state)
 VALUES($1,$2,$3,$2||'-account',1,2,2,repeat('a',64),'platform_admin',$4,$5,'domain.directory_read','reserved');
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation)
 VALUES($1,$2,$2||'-account','domain.directory_read',$3,1,2);`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID, domain, out.Task.ID, grant, f.actor.ActorID)
		return err
	})
	return out.Task
}

func (f *operationalUpgradeFixture) grantDirectory(domain string) {
	f.t.Helper()
	f.tx(func(tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(f.ctx, tx, f.actor, credentialuse.GrantUse); err != nil {
			return err
		}
		_, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.actor, "/grant", credentialuse.Input{AccountID: domain + "-account", RoleID: "platform_admin", Purpose: credentialuse.DirectoryPurpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "grant-directory-" + domain}, []string{domain}, credentialuse.DirectoryPurpose)
		return err
	})
}

func (f *operationalUpgradeFixture) assertDirectoryUse(id, state string, dependencies int) {
	f.t.Helper()
	var actual string
	var count int
	err := f.conn.QueryRow(f.ctx, `SELECT state,(SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read' AND object_id=u.task_id) FROM adtr.domain_directory_task_uses u WHERE task_id=$1`, id).Scan(&actual, &count)
	if err != nil || actual != state || count != dependencies {
		f.t.Fatal("directory use or dependency changed", actual, count, err)
	}
}

func TestVersionFifteenFutureUpgradeRequiresBothOpenedLedgers(t *testing.T) {
	f := newDirectoryUpgradeFixture(t)
	if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 15
	f.grantDirectory("second")
	f.grantDirectory("reserved")
	b2, b2Opened := f.controlledEngine()
	directory, directoryOpened := f.controlledLedgerEngine(domains.New(nil).DirectoryKind(), "domain_directory_task_uses")
	b2Task := f.submitAccount(b2, "first")
	b2Run, b2Done := f.start(b2, b2Opened, b2Task, "future-b2")
	directoryTask := f.submitDirectory(directory, "second")
	directoryRun, directoryDone := f.start(directory, directoryOpened, directoryTask, "future-directory")
	reserved := f.submitDirectory(directory, "reserved")
	// Re-running the current migration changes no contract, so opened uses
	// must not prevent this idempotent invocation.
	if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err != nil {
		t.Fatal("current-version migration incorrectly required executor drain", err)
	}
	requireBlocked := func() {
		t.Helper()
		before, catalog := f.snapshot(), f.catalogSnapshot()
		ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
		defer cancel()
		if err := futureDirectoryUpgradePreflight(ctx, f.config); !errors.Is(err, ErrCredentialUseMigrationBlocked) {
			t.Fatal("future upgrade accepted an opened ledger", err)
		}
		if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
			t.Fatal("future preflight changed current data or schemas")
		}
	}
	requireBlocked()
	f.finish(b2Run, b2Done, false)
	f.assertUse(b2Task.ID, "quiesced", 0)
	requireBlocked() // Directory alone still blocks after B2 genuinely returns.
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 day' WHERE task_id=$1`, directoryTask.ID)
	if n, err := directory.RecoverExpired(f.ctx); err != nil || n != 1 {
		t.Fatal("recover directory attempt", n, err)
	}
	f.assertDirectoryUse(directoryTask.ID, "opened", 1)
	requireBlocked()
	f.raceUpgradeAgainstReturn(directory, directoryRun, directoryDone, futureDirectoryUpgradePreflight)
	f.assertDirectoryUse(directoryTask.ID, "quiesced", 0)
	f.assertDirectoryUse(reserved.ID, "reserved", 1)
	if err := futureDirectoryUpgradePreflight(f.ctx, f.config); err != nil {
		t.Fatal("quiescent ledgers blocked future preflight", err)
	}
	f.assertDirectoryInstalled()
}

func TestVersionFourteenDirectoryUpgradeRollsBackLateFragmentFailure(t *testing.T) {
	f := newDirectoryUpgradeFixture(t)
	// Force failure after the earlier permission, purpose, dependency and use
	// fragments have executed. An unrelated object is never overwritten/adopted.
	f.exec(`CREATE TABLE adtr.domain_directory_observations(original text NOT NULL);
 INSERT INTO adtr.domain_directory_observations VALUES('unknown-original-object')`)
	before, catalog := f.snapshot(), f.catalogSnapshot()
	for range 2 {
		if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err == nil {
			t.Fatal("directory migration adopted an existing observation object")
		}
		if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
			t.Fatal("late directory failure committed an earlier fragment or changed the collision object")
		}
	}
	var intact bool
	if err := f.conn.QueryRow(f.ctx, `SELECT version=14 AND to_regclass('adtr.domain_directory_task_uses') IS NULL AND to_regprocedure('adtr.credential_use_role_eligible_for_purpose(text,text,text,text)') IS NULL FROM adtr.schema_version`).Scan(&intact); err != nil || !intact {
		t.Fatal("late rollback left a partial directory schema", err)
	}
}

func TestVersionFourteenDirectoryUpgradeFencesLateB2Opening(t *testing.T) {
	f := newDirectoryUpgradeFixture(t)
	engine, _ := f.controlledEngine()
	task := f.submitAccount(engine, "first")
	lease, err := engine.Claim(f.ctx, "late-b2-directory-upgrade")
	if err != nil || lease.Task.ID != task.ID {
		t.Fatal("historical claim", err)
	}
	lease, err = engine.Start(f.ctx, lease)
	if err != nil {
		t.Fatal("historical start", err)
	}
	f.assertUse(task.ID, "reserved", 1)
	gate, err := pgx.ConnectConfig(f.ctx, f.config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close(context.Background())
	tx, err := gate.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = tasks.CheckSchemaTx(f.ctx, tx, 14); err != nil {
		t.Fatal(err)
	}
	config := f.config.Copy()
	config.RuntimeParams["application_name"] = "adtr-directory-late-open"
	migration := make(chan error, 1)
	go func() { migration <- MigrateDirectoryV2BaselineForTest(f.ctx, config) }()
	f.awaitMigrationGate(config.RuntimeParams["application_name"])
	called := false
	checkpoint := make(chan error, 1)
	go func() {
		_, err := engine.WithTx(f.ctx, lease, lease.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			called = true
			_, err := tx.Exec(ctx, `UPDATE adtr.domain_account_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$2,opener_fencing_token=$3,opener_attempt=$4 WHERE task_id=$1 AND state='reserved'`, task.ID, lease.Owner, lease.Token, lease.Task.Attempt)
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		checkpoint <- err
	}()
	f.awaitHistoricalEngineGate(engine, 0)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-migration; err != nil {
		t.Fatal("reserved-only upgrade", err)
	}
	if err = <-checkpoint; called || !errors.Is(err, tasks.ErrSchemaIncompatible) {
		t.Fatal("old checkpoint opened a use after migration15", err)
	}
	f.assertUse(task.ID, "reserved", 1)
	f.assertDirectoryInstalled()
}

func TestVersionFifteenDirectoryAuditResultWaitsForTaskOutcome(t *testing.T) {
	for _, outcome := range []string{"succeeded", "cancelled", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			f := newDirectoryUpgradeFixture(t)
			if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err != nil {
				t.Fatal(err)
			}
			f.schemaVersion = 15
			f.grantDirectory("first")
			engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryKind(), "domain_directory_task_uses")
			task := f.submitDirectory(engine, "first")
			run, done := f.start(engine, opened, task, "directory-audit-result")
			// A synthetic complete empty observation exercises the installed SQL
			// publication guard and audit projection, without invoking LDAP.
			now := time.Now().UTC()
			body, err := json.Marshal(ldapconnection.DirectoryObservation{
				Objects: []directoryassets.Object{},
				Source:  ldapconnection.DirectorySource{ServerName: "dc.first.invalid", DCHostName: "dc.first.invalid", Domain: "first.invalid", NamingContext: "DC=first,DC=invalid", StartedAt: now, CompletedAt: now, Pages: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(body)
			f.tx(func(tx pgx.Tx) error {
				_, err := tx.Exec(f.ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256)
 SELECT tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,0,$2,$3 FROM adtr.domain_directory_task_uses WHERE task_id=$1`, task.ID, body, hex.EncodeToString(digest[:]))
				if err != nil {
					return err
				}
				_, err = tx.Exec(f.ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result)
 VALUES($1,$2,'first','domain_directory_result',2,2,2,$3,'success')`, f.actor.TenantID, f.actor.ActorID, task.ID)
				return err
			})
			assert := func(result, state string, available bool) {
				t.Helper()
				var actual, actualState string
				var actualAvailable, metadataOnly bool
				err := f.conn.QueryRow(f.ctx, `SELECT event_result,result_available,event_args->>'taskState',
 NOT (event_args ?| ARRAY['accountId','grantRoleId','grantRevision','policyRevision','body','objects'])
 FROM adtr.audit_source WHERE source='domain' AND event='domain_directory_result' AND event_args->>'taskUUID'=$1`, task.ID).Scan(&actual, &actualAvailable, &actualState, &metadataOnly)
				if err != nil || actual != result || actualState != state || actualAvailable != available || !metadataOnly {
					t.Fatal("directory audit misrepresented staged/terminal data", actual, actualAvailable, actualState, err)
				}
			}
			assert("NONE", "running", false)
			switch outcome {
			case "cancelled":
				f.tx(func(tx pgx.Tx) error {
					_, err := engine.CancelTx(f.ctx, tx, f.actor, task.ID)
					return err
				})
			case "failed":
				f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 day' WHERE task_id=$1`, task.ID)
				if n, err := engine.RecoverExpired(f.ctx); err != nil || n != 1 {
					t.Fatal("recover staged audit attempt", n, err)
				}
			}
			f.finish(run, done, outcome == "failed")
			switch outcome {
			case "succeeded":
				assert("SUCCESS", outcome, true)
			case "failed":
				assert("FAIL", outcome, true)
			case "cancelled":
				assert("NONE", outcome, false)
			}
		})
	}
}
