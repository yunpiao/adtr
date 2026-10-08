//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

const operationalUpgradeTenant = "historical-upgrade"

// Build the actual historical producers through v13. In particular, do not run
// today's Migrate and then change its marker: that would conceal both missing
// old guards and accidental adoption of existing operational-log task kinds.
var operationalUpgradeVersionThirteen = `
CREATE SCHEMA adtr;
CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0));
INSERT INTO adtr.schema_version VALUES(true,13);
` + auth.Schema + auth.SchemaV3 + auth.ResourceSchema + auth.TaskPermissionSchema + auth.TaskAuthorizationSchema + tasks.Schema +
	auth.AuditPermissionSchema + audit.Schema + auth.SystemPermissionMarks + systemhealth.Schema +
	auth.SchedulePermissionSchema + auth.ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema +
	auth.DomainPermissionSchema + domains.Schema + audit.DomainViewSchema + auth.OperationAccountPermissionSchema + operationaccounts.Schema + auth.ProfileSchema + audit.OperationAccountViewSchema +
	credentialuse.Schema + audit.CredentialUseViewSchema + domains.AccountReferenceSchema + audit.AccountReferenceViewSchema

type operationalUpgradeFixture struct {
	t             *testing.T
	schemaVersion int
	ctx           context.Context
	config        *pgx.ConnConfig
	conn          *pgx.Conn
	actor         tasks.Principal
}

func newOperationalUpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated PostgreSQL required; compile-only checks do not run migration coverage")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("adtr_operational_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		_ = admin.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close(cleanup)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	f := &operationalUpgradeFixture{t: t, schemaVersion: 13, ctx: ctx, config: cfg, conn: conn}
	f.exec(operationalUpgradeVersionThirteen)
	f.assertVersionThirteen()
	f.exec(`INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)
 VALUES('historical-upgrade',20,253402300799,'synthetic-upgrade','Synthetic');
 INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('historical-upgrade','upgrade-group','Synthetic upgrade group');
 INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('historical-upgrade','platform_admin','upgrade-group');
 INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change,mfa_secret)
 VALUES('historical-upgrade','synthetic-upgrade-user','synthetic-hash','platform_admin',false,'synthetic-mfa');`)
	f.actor.TenantID = operationalUpgradeTenant
	if err = conn.QueryRow(ctx, `SELECT id FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID).Scan(&f.actor.ActorID); err != nil {
		t.Fatal(err)
	}
	// Set up every account/grant before admissions, so their legitimate epoch
	// bumps do not invalidate a previously submitted historical task.
	for _, domain := range []string{"first", "second", "reserved", "history"} {
		f.seedAccountBase(domain)
	}
	f.tx(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES($1,'custom','custom.invalid');
 INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,'upgrade-group','custom');
 INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port)
 VALUES($1,'custom','custom.invalid','dc.custom.invalid','starttls','389');
 INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext)
 VALUES($1,'custom',1,'synthetic-domain-key',decode(repeat('71',40),'hex'));`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID)
		return err
	})
	for _, domain := range []string{"first", "second", "reserved", "history"} {
		f.bindAccountSource(domain)
	}
	return f
}

func (f *operationalUpgradeFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *operationalUpgradeFixture) tx(work func(pgx.Tx) error) {
	f.t.Helper()
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = tasks.CheckSchemaTx(f.ctx, tx, f.schemaVersion); err != nil {
		f.t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))`, f.actor.TenantID); err != nil {
		f.t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `SELECT id FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, f.actor.TenantID, f.actor.ActorID); err != nil {
		f.t.Fatal(err)
	}
	if err = work(tx); err != nil {
		f.t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

// Current runtime helpers use the current schema. Historical
// fixture setup instead uses their v13 transaction protocol and persisted SQL
// authorization guards. No trigger is disabled and no epoch/schema is forged.
// This does not claim to exercise current HTTP proof or LDAP transport paths.
func (f *operationalUpgradeFixture) seedAccountBase(domain string) {
	f.t.Helper()
	f.tx(func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES($1,$2,$2||'.invalid');
 INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES($1,'upgrade-group',$2);
 INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port,credential_mode)
 VALUES($1,$2,$2||'.invalid','dc.'||$2||'.invalid','starttls','389','unconfigured');
 INSERT INTO adtr.operation_accounts(tenant_id,domain_id,account_id,label) VALUES($1,$2,$2||'-account','Synthetic account');
 INSERT INTO adtr.operation_account_credentials(tenant_id,domain_id,account_id,credential_revision,key_id,ciphertext)
 VALUES($1,$2,$2||'-account',1,'synthetic-account-key',decode(repeat('72',40),'hex'));
 INSERT INTO adtr.domain_dependencies(tenant_id,domain_id,module,object_id) VALUES($1,$2,'operation_accounts',$2||'-account');`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID, domain)
		return err
	})
}

func (f *operationalUpgradeFixture) bindAccountSource(domain string) {
	f.t.Helper()
	f.tx(func(tx pgx.Tx) error {
		var err error
		if _, err = tx.Exec(f.ctx, `SELECT set_config('adtr.credential_use_protocol','credential_governance_v1',true),
 set_config('adtr.credential_use_tenant',$1,true),set_config('adtr.credential_use_actor',$2,true),
 set_config('adtr.credential_use_operation','grant',true),set_config('adtr.credential_use_scope','',true)`, f.actor.TenantID, fmt.Sprint(f.actor.ActorID)); err != nil {
			return err
		}
		if _, err = tx.Exec(f.ctx, `SELECT adtr.credential_use_capture_scope()`); err != nil {
			return err
		}
		if _, err = tx.Exec(f.ctx, `INSERT INTO adtr.operation_account_use_grants(tenant_id,domain_id,account_id,purpose,role_id,account_credential_revision,allowed,updated_by)
 VALUES($1,$2,$2||'-account','domain.connection_test','platform_admin',1,true,$3)`, f.actor.TenantID, domain, f.actor.ActorID); err != nil {
			return err
		}
		if _, err = tx.Exec(f.ctx, `SELECT set_config('adtr.domain_source_protocol','account_reference_source_v1',true),
 set_config('adtr.domain_source_tenant',$1,true),set_config('adtr.domain_source_actor',$2,true),set_config('adtr.domain_source_operation','reference',true)`, f.actor.TenantID, fmt.Sprint(f.actor.ActorID)); err != nil {
			return err
		}
		_, err = tx.Exec(f.ctx, `UPDATE adtr.domain_connections SET credential_mode='operation_account',operation_account_id=$2||'-account',operation_account_credential_revision=1,
 connection_revision=2,credential_revision=2,diagnostic_generation=2 WHERE tenant_id=$1 AND domain_id=$2;
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation)
 VALUES($1,$2,$2||'-account','domain.connection_binding',$2,1,2);
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id)
 VALUES($1,$2,$2||'-account','unknown.synthetic','preserve-unknown');`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID, domain)
		return err
	})
}

func (f *operationalUpgradeFixture) submitAccount(engine *tasks.Engine, domain string) tasks.Task {
	f.t.Helper()
	var out tasks.Submission
	f.tx(func(tx pgx.Tx) error {
		var grant, generation string
		if err := tx.QueryRow(f.ctx, `SELECT g.grant_revision::text,(c.diagnostic_generation+1)::text FROM adtr.domain_connections c
 JOIN adtr.operation_account_use_grants g ON g.tenant_id=c.tenant_id AND g.domain_id=c.domain_id AND g.account_id=c.operation_account_id
 WHERE c.tenant_id=$1 AND c.domain_id=$2 AND g.role_id='platform_admin' AND g.purpose='domain.connection_test'`, f.actor.TenantID, domain).Scan(&grant, &generation); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]string{"credentialSource": "operation_account", "connectionRevision": "2", "connectionCredentialGeneration": "2", "accountId": domain + "-account", "accountCredentialRevision": "1", "grantRoleId": "platform_admin", "grantRevision": grant, "policyRevision": strings.Repeat("a", 64), "diagnosticGeneration": generation})
		if err != nil {
			return err
		}
		out, err = engine.SubmitTx(f.ctx, tx, f.actor, tasks.SubmitInput{TaskName: domains.AccountKindName, DomainID: domain, PayloadVersion: 1, Payload: payload, IdempotencyKey: "admit-" + domain})
		if err != nil {
			return err
		}
		_, err = tx.Exec(f.ctx, `INSERT INTO adtr.domain_account_task_uses(tenant_id,domain_id,task_id,account_id,account_credential_revision,connection_revision,connection_credential_generation,diagnostic_generation,policy_revision,grant_role_id,grant_revision,actor_id,purpose,state)
 VALUES($1,$2,$3,$2||'-account',1,2,2,$4,repeat('a',64),'platform_admin',$5,$6,'domain.connection_test','reserved');
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation)
 VALUES($1,$2,$2||'-account','domain.account_connection_test',$3,1,2);
 UPDATE adtr.domain_connections SET latest_test_task_id=$3,diagnostic_generation=$4 WHERE tenant_id=$1 AND domain_id=$2;`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID, domain, out.Task.ID, generation, grant, f.actor.ActorID)
		return err
	})
	return out.Task
}

type operationalUpgradeExecution struct {
	task     tasks.Task
	release  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (e *operationalUpgradeExecution) stop() { e.once.Do(func() { close(e.release) }) }

type operationalUpgradeRun struct {
	worked bool
	err    error
}

func (f *operationalUpgradeFixture) controlledEngine() (*tasks.Engine, <-chan *operationalUpgradeExecution) {
	f.t.Helper()
	return f.controlledLedgerEngine(domains.New(nil).AccountKind(), "domain_account_task_uses")
}

func (f *operationalUpgradeFixture) controlledLedgerEngine(kind tasks.Kind, ledger string) (*tasks.Engine, <-chan *operationalUpgradeExecution) {
	f.t.Helper()
	opened := make(chan *operationalUpgradeExecution, 4)
	// Avoid unrelated heartbeat writes during full rollback snapshots. Only
	// timing is widened; admission, fencing, explicit grants and the production
	// OnQuiesced implementation remain in use. No secrets are resolved here.
	kind.Lease, kind.Heartbeat, kind.Timeout = 3*time.Minute, time.Minute, time.Minute
	kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		_, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			tag, err := tx.Exec(ctx, `UPDATE `+pgx.Identifier{"adtr", ledger}.Sanitize()+` SET state='opened',opened_at=clock_timestamp(),opener_owner=$2,opener_fencing_token=$3,opener_attempt=$4
 WHERE task_id=$1 AND state='reserved'`, ex.Task.ID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt)
			if err == nil && tag.RowsAffected() != 1 {
				err = errors.New("expected exactly one guarded opened use")
			}
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "open_failed"}
		}
		run := &operationalUpgradeExecution{task: ex.Task, release: make(chan struct{}), returned: make(chan struct{})}
		defer close(run.returned)
		opened <- run
		// Deliberately keep the execution alive after cancellation/lease loss,
		// modelling a resource that has not yet completed its cleanup.
		<-run.release
		return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
	}
	registry, err := tasks.NewRegistry(kind, domains.New(nil).Kind())
	if err != nil {
		f.t.Fatal(err)
	}
	engineConfig := f.config.Copy()
	engineConfig.RuntimeParams["application_name"] = "adtr-historical-account-engine"
	engine, err := tasks.New(engineConfig, registry, auth.NewTaskAuthorizer(), f.schemaVersion)
	if err != nil {
		f.t.Fatal(err)
	}
	return engine, opened
}

func (f *operationalUpgradeFixture) start(engine *tasks.Engine, opened <-chan *operationalUpgradeExecution, want tasks.Task, owner string) (*operationalUpgradeExecution, <-chan operationalUpgradeRun) {
	f.t.Helper()
	done := make(chan operationalUpgradeRun, 1)
	go func() {
		worked, err := engine.RunOne(f.ctx, owner)
		done <- operationalUpgradeRun{worked, err}
	}()
	select {
	case run := <-opened:
		f.t.Cleanup(run.stop)
		if run.task.ID != want.ID {
			f.t.Fatal("controlled executor claimed an unexpected task")
		}
		return run, done
	case result := <-done:
		f.t.Fatalf("executor returned without opening its use: worked=%v err=%v", result.worked, result.err)
	case <-time.After(5 * time.Second):
		f.t.Fatal("controlled executor did not open its use")
	}
	return nil, nil
}

func (f *operationalUpgradeFixture) finish(run *operationalUpgradeExecution, done <-chan operationalUpgradeRun, leaseLost bool) {
	f.t.Helper()
	run.stop()
	select {
	case result := <-done:
		if !result.worked || leaseLost && !errors.Is(result.err, tasks.ErrLeaseLost) || !leaseLost && result.err != nil || errors.Is(result.err, tasks.ErrQuiescencePending) {
			f.t.Fatalf("unexpected execution/acknowledgement result: worked=%v err=%v", result.worked, result.err)
		}
	case <-time.After(5 * time.Second):
		f.t.Fatal("executor return/acknowledgement did not finish")
	}
}

func (f *operationalUpgradeFixture) assertUse(id, state string, dependencies int) {
	f.t.Helper()
	var actual string
	var count int
	err := f.conn.QueryRow(f.ctx, `SELECT state,(SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.account_connection_test' AND object_id=u.task_id)
 FROM adtr.domain_account_task_uses u WHERE task_id=$1`, id).Scan(&actual, &count)
	if err != nil || actual != state || count != dependencies {
		f.t.Fatalf("use state/dependency mismatch: state=%s dependencies=%d err=%v", actual, count, err)
	}
}

func (f *operationalUpgradeFixture) assertVersionThirteen() {
	f.t.Helper()
	var valid bool
	err := f.conn.QueryRow(f.ctx, `SELECT version=13 AND to_regclass('adtr.domain_account_task_uses') IS NOT NULL
 AND to_regclass('adtr.operational_log_events') IS NULL AND to_regclass('adtr.operational_log_state') IS NULL
 AND to_regclass('adtr.operational_log_bundle_snapshots') IS NULL AND to_regclass('adtr.operational_log_audit') IS NULL
 FROM adtr.schema_version WHERE singleton`).Scan(&valid)
	if err != nil || !valid {
		f.t.Fatal("fixture/rollback is not the actual historical schema13", err)
	}
}

// Snapshot every old table, including ciphertext, epochs, task events, receipts,
// grants, source bindings and all use states; do not print their contents.
func (f *operationalUpgradeFixture) snapshot() map[string]string {
	f.t.Helper()
	rows, err := f.conn.Query(f.ctx, `SELECT tablename FROM pg_tables WHERE schemaname='adtr' ORDER BY tablename`)
	if err != nil {
		f.t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			f.t.Fatal(err)
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		f.t.Fatal(err)
	}
	out := make(map[string]string, len(tables))
	for _, name := range tables {
		var raw string
		if err := f.conn.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM `+pgx.Identifier{"adtr", name}.Sanitize()+` t`).Scan(&raw); err != nil {
			f.t.Fatal(err)
		}
		out[name] = raw
	}
	return out
}

func (f *operationalUpgradeFixture) catalogSnapshot() string {
	f.t.Helper()
	var snapshot string
	err := f.conn.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'relations',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relkind) ORDER BY c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='adtr'),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid)) ORDER BY c.conrelid::regclass::text,c.conname) FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='adtr'),
 'functions',(SELECT jsonb_agg(pg_get_functiondef(p.oid) ORDER BY p.proname,p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='adtr'),
 'triggers',(SELECT jsonb_agg(pg_get_triggerdef(t.oid) ORDER BY c.relname,t.tgname) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='adtr'))::text`).Scan(&snapshot)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

func (f *operationalUpgradeFixture) requireBlocked() {
	f.t.Helper()
	before, catalog := f.snapshot(), f.catalogSnapshot()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	results := make(chan error, 3)
	for range 3 {
		go func() { results <- Migrate(ctx, f.config) }()
	}
	for range 3 {
		if err := <-results; !errors.Is(err, ErrCredentialUseMigrationBlocked) {
			f.t.Fatalf("concurrent migration must promptly refuse opened uses, got %v", err)
		}
	}
	if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
		f.t.Fatal("refused migration changed historical data or schema")
	}
	f.assertVersionThirteen()
}

func TestVersionThirteenOperationalLogsRequireActualExecutorQuiescence(t *testing.T) {
	f := newOperationalUpgradeFixture(t)
	engine, opened := f.controlledEngine()

	history := f.submitAccount(engine, "history")
	historyRun, historyDone := f.start(engine, opened, history, "upgrade-history")
	f.finish(historyRun, historyDone, false)
	f.assertUse(history.ID, "quiesced", 0)
	first := f.submitAccount(engine, "first")
	firstRun, firstDone := f.start(engine, opened, first, "upgrade-first")
	second := f.submitAccount(engine, "second")
	secondRun, secondDone := f.start(engine, opened, second, "upgrade-second")
	reserved := f.submitAccount(engine, "reserved")
	f.tx(func(tx pgx.Tx) error {
		payload, err := json.Marshal(map[string]string{"connectionRevision": "1", "credentialRevision": "1", "policyRevision": strings.Repeat("b", 64), "diagnosticGeneration": "2"})
		if err != nil {
			return err
		}
		custom, err := engine.SubmitTx(f.ctx, tx, f.actor, tasks.SubmitInput{TaskName: domains.KindName, DomainID: "custom", PayloadVersion: 1, Payload: payload, IdempotencyKey: "preserve-custom"})
		if err != nil {
			return err
		}
		_, err = tx.Exec(f.ctx, `UPDATE adtr.domain_connections SET latest_test_task_id=$2,diagnostic_generation=2 WHERE tenant_id=$1 AND domain_id='custom'`, f.actor.TenantID, custom.Task.ID)
		return err
	})

	f.assertUse(first.ID, "opened", 1)
	f.assertUse(second.ID, "opened", 1)
	f.requireBlocked()
	f.finish(firstRun, firstDone, false)
	f.assertUse(first.ID, "quiesced", 0)
	f.assertUse(second.ID, "opened", 1)
	f.assertUse(reserved.ID, "reserved", 1)
	f.requireBlocked()

	// An expired lease is not execution-return evidence. Change only
	// its database-clock deadline, then use the real recovery path to terminalize.
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, second.ID)
	f.requireBlocked()
	if n, err := engine.RecoverExpired(f.ctx); err != nil || n != 1 {
		t.Fatalf("recover expired historical task: count=%d err=%v", n, err)
	}
	var terminal bool
	if err := f.conn.QueryRow(f.ctx, `SELECT state='failed' AND error_code='executor_lost' AND lease_owner='' AND lease_until IS NULL FROM adtr.tasks WHERE task_id=$1`, second.ID).Scan(&terminal); err != nil || !terminal {
		t.Fatal("recovery did not terminalize the expired attempt", err)
	}
	f.assertUse(second.ID, "opened", 1)
	f.requireBlocked()

	// Put migration first in the schema-gate queue, then let the actual executor
	// return. Its original-schema Finish/OnQuiesced transaction queues behind
	// migration. Migration must release the gate by refusing; waiting for drain
	// under its exclusive gate would deadlock this acknowledgement.
	gate, err := pgx.ConnectConfig(f.ctx, f.config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close(context.Background())
	gateTx, err := gate.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gateTx.Rollback(context.Background())
	if err = tasks.CheckSchemaTx(f.ctx, gateTx, 13); err != nil {
		t.Fatal(err)
	}
	migrationCfg := f.config.Copy()
	migrationCfg.RuntimeParams["application_name"] = "adtr-quiescence-migration-race"
	migrationCtx, stopMigration := context.WithTimeout(f.ctx, 5*time.Second)
	defer stopMigration()
	migrationDone := make(chan error, 1)
	go func() { migrationDone <- Migrate(migrationCtx, migrationCfg) }()
	f.awaitMigrationGate(migrationCfg.RuntimeParams["application_name"])
	secondRun.stop()
	select {
	case <-secondRun.returned:
	case <-time.After(time.Second):
		t.Fatal("controlled executor did not return")
	}
	f.awaitHistoricalEngineGate(engine, 1)
	f.assertUse(second.ID, "opened", 1)
	if err = gateTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-migrationDone:
		if !errors.Is(err, ErrCredentialUseMigrationBlocked) {
			t.Fatal("queued migration did not refuse promptly before old-schema acknowledgement", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("migration retained the exclusive schema gate while waiting for executor acknowledgement")
	}
	f.finish(secondRun, secondDone, true)
	f.assertVersionThirteen()
	f.assertUse(second.ID, "quiesced", 0)
	f.assertUse(reserved.ID, "reserved", 1)
	if engine.PendingQuiescence() != 0 {
		t.Fatal("old-schema execution acknowledgement remained pending")
	}
	var preserved bool
	if err = f.conn.QueryRow(f.ctx, `SELECT count(*)=4 FROM adtr.operation_account_dependencies WHERE consumer_kind='unknown.synthetic'`).Scan(&preserved); err != nil || !preserved {
		t.Fatal("acknowledgement removed unrelated blockers", err)
	}
	before := f.snapshot()
	delete(before, "schema_version")

	results := make(chan error, 3)
	for range 3 {
		go func() { results <- Migrate(f.ctx, f.config) }()
	}
	for range 3 {
		if err = <-results; err != nil {
			t.Fatal("concurrent upgrade after genuine quiescence failed", err)
		}
	}
	if err = Migrate(f.ctx, f.config); err != nil {
		t.Fatal("repeated current-schema migration failed", err)
	}
	after := f.snapshot()
	for table, old := range before {
		if after[table] != old {
			t.Fatalf("upgrade rewrote historical table %s", table)
		}
	}
	if err = f.conn.QueryRow(f.ctx, `SELECT version=$1 AND
 (SELECT count(*)=1 FROM adtr.operational_log_state) AND
 NOT EXISTS(SELECT FROM adtr.operational_log_events) AND NOT EXISTS(SELECT FROM adtr.operational_log_reports) AND
 NOT EXISTS(SELECT FROM adtr.operational_log_audit) AND NOT EXISTS(SELECT FROM adtr.operational_log_bundle_snapshots) AND
 NOT EXISTS(SELECT FROM adtr.operational_log_bundle_rows) AND NOT EXISTS(SELECT FROM adtr.operational_log_bundle_chunks) AND
 NOT EXISTS(SELECT FROM adtr.operational_log_bundle_manifests) FROM adtr.schema_version`, SchemaVersion).Scan(&preserved); err != nil || !preserved {
		t.Fatal("migration did not create the current schema with an empty journal and bundle history", err)
	}
	f.assertOldEngineRejected(engine, secondRun.task)
	if err = Ready(f.ctx, f.config); err != nil {
		t.Fatal("current-schema readiness failed", err)
	}
}

func (f *operationalUpgradeFixture) awaitMigrationGate(application string) {
	f.t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := f.conn.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid
 WHERE a.datname=current_database() AND a.application_name=$1 AND l.locktype='advisory' AND l.classid=0::oid
 AND l.objid::bigint=734192801 AND l.objsubid=1 AND l.mode='ExclusiveLock' AND NOT l.granted)`, application).Scan(&waiting)
		if err != nil {
			f.t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			f.t.Fatal("migration never queued on the shared schema gate")
		}
	}
}

func (f *operationalUpgradeFixture) awaitHistoricalEngineGate(engine *tasks.Engine, pending int) {
	f.t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := f.conn.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid
 WHERE a.datname=current_database() AND a.application_name='adtr-historical-account-engine' AND l.locktype='advisory' AND l.classid=0::oid
 AND l.objid::bigint=734192801 AND l.objsubid=1 AND l.mode='ShareLock' AND NOT l.granted)`).Scan(&waiting)
		if err != nil {
			f.t.Fatal(err)
		}
		if waiting && engine.PendingQuiescence() == pending {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			f.t.Fatal("historical engine did not queue behind migration with the expected pending witnesses")
		}
	}
}

func (f *operationalUpgradeFixture) assertOldEngineRejected(engine *tasks.Engine, old tasks.Task) {
	f.t.Helper()
	before := f.snapshot()
	if _, err := engine.Claim(f.ctx, "old-upgrade-worker"); !errors.Is(err, tasks.ErrSchemaIncompatible) {
		f.t.Fatal("historical engine accepted current-schema claim", err)
	}
	if worked, err := engine.RunOne(f.ctx, "old-upgrade-runner"); worked || !errors.Is(err, tasks.ErrSchemaIncompatible) {
		f.t.Fatal("historical engine accepted current-schema execution", err)
	}
	if n, err := engine.RecoverExpired(f.ctx); n != 0 || !errors.Is(err, tasks.ErrSchemaIncompatible) {
		f.t.Fatal("historical engine accepted current-schema recovery", err)
	}
	lease := tasks.Lease{Task: old, Owner: old.LeaseOwner, Token: old.FencingToken}
	if _, err := engine.Start(f.ctx, lease); !errors.Is(err, tasks.ErrSchemaIncompatible) {
		f.t.Fatal("historical engine accepted current-schema Start", err)
	}
	called := false
	if _, err := engine.WithTx(f.ctx, lease, old.ResultVersion, func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		called = true
		return 0, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
	}); called || !errors.Is(err, tasks.ErrSchemaIncompatible) {
		f.t.Fatal("historical engine entered a current-schema checkpoint", err)
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		f.t.Fatal("rejected old runtime changed current-schema data")
	}
}

func TestVersionThirteenOperationalLogsRejectLegacyBundleKindAtomically(t *testing.T) {
	for _, state := range []string{"queued", "succeeded"} {
		t.Run(state, func(t *testing.T) {
			f := newOperationalUpgradeFixture(t)
			f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts)
 SELECT 'legacy-bundle-task',$1,'platform','system.logs_bundle',1,'{"historical":"unrelated"}',repeat('d',64),id,authorization_version::text,'legacy-bundle-key',$2,1 FROM adtr.users WHERE tenant_id=$1`, f.actor.TenantID, state)
			before, catalog := f.snapshot(), f.catalogSnapshot()
			for range 2 {
				ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
				err := Migrate(ctx, f.config)
				cancel()
				if !errors.Is(err, ErrOperationalLogKindCollision) {
					t.Fatal("legacy reserved bundle kind must block adoption independently of account uses", err)
				}
				f.assertVersionThirteen()
				if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
					t.Fatal("reserved bundle kind collision changed historical data/schema")
				}
			}
		})
	}
}

func TestVersionThirteenOperationalLogsFenceLateAccountUseOpening(t *testing.T) {
	f := newOperationalUpgradeFixture(t)
	engine, opened := f.controlledEngine()
	admission := f.submitAccount(engine, "first")
	lease, err := engine.Claim(f.ctx, "late-old-account-use")
	if err != nil || lease.Task.ID != admission.ID {
		t.Fatal("historical claim failed", err)
	}
	lease, err = engine.Start(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	f.assertUse(admission.ID, "reserved", 1)
	before := f.snapshot()
	delete(before, "schema_version")

	gate, err := pgx.ConnectConfig(f.ctx, f.config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close(context.Background())
	gateTx, err := gate.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gateTx.Rollback(context.Background())
	if err = tasks.CheckSchemaTx(f.ctx, gateTx, 13); err != nil {
		t.Fatal(err)
	}
	migrationConfig := f.config.Copy()
	migrationConfig.RuntimeParams["application_name"] = "adtr-late-opening-migration"
	migrationDone := make(chan error, 1)
	go func() { migrationDone <- Migrate(f.ctx, migrationConfig) }()
	f.awaitMigrationGate(migrationConfig.RuntimeParams["application_name"])

	// This is the old, already-started attempt's first checkpoint. The shared
	// schema gate must run before its module callback can open the reserved use.
	workCalled := false
	checkpointDone := make(chan error, 1)
	go func() {
		_, err := engine.WithTx(f.ctx, lease, lease.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			workCalled = true
			_, err := tx.Exec(ctx, `UPDATE adtr.domain_account_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$2,opener_fencing_token=$3,opener_attempt=$4 WHERE task_id=$1 AND state='reserved'`, admission.ID, lease.Owner, lease.Token, lease.Task.Attempt)
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		checkpointDone <- err
	}()
	f.awaitHistoricalEngineGate(engine, 0)
	if err = gateTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-migrationDone; err != nil {
		t.Fatal("reserved-only migration failed", err)
	}
	if err = <-checkpointDone; workCalled || !errors.Is(err, tasks.ErrSchemaIncompatible) {
		t.Fatal("old first checkpoint could open a use after migration", err)
	}
	select {
	case <-opened:
		t.Fatal("migration race unexpectedly invoked an executor")
	default:
	}
	f.assertUse(admission.ID, "reserved", 1)
	after := f.snapshot()
	for table, old := range before {
		if after[table] != old {
			t.Fatalf("migration/late checkpoint changed historical table %s", table)
		}
	}
}
