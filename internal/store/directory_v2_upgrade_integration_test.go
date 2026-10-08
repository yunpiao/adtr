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

func newDirectoryV2UpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newDirectoryUpgradeFixture(t)
	if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 15
	f.assertDirectoryInstalled()
	return f
}

func (f *operationalUpgradeFixture) assertDirectoryV2Installed() {
	f.t.Helper()
	var valid bool
	err := f.conn.QueryRow(f.ctx, `SELECT version=16
 AND to_regclass('adtr.domain_directory_versioned_use_identity') IS NOT NULL
 AND to_regclass('adtr.domain_directory_observations_versioned_latest') IS NOT NULL
 AND to_regprocedure('adtr.domain_directory_observation_canonical_v2(jsonb)') IS NOT NULL
 AND to_regprocedure('adtr.domain_directory_observation_base64_v2(jsonb,integer)') IS NOT NULL
 AND (SELECT count(*)=2 FROM information_schema.columns WHERE table_schema='adtr' AND table_name IN ('domain_directory_task_uses','domain_directory_observations') AND column_name='dictionary_version' AND data_type='integer' AND is_nullable='NO' AND column_default='1')
 AND (SELECT count(*)=3 FROM pg_constraint WHERE connamespace='adtr'::regnamespace AND conname IN ('operation_account_use_grants_purpose_check','operation_account_use_mutations_purpose_check','operation_account_use_audit_purpose_check') AND position('domain.directory_read.v2' in pg_get_constraintdef(oid))>0)
 AND (SELECT count(*)=3 FROM pg_trigger WHERE tgrelid='adtr.domain_directory_observations'::regclass AND NOT tgisinternal)
 FROM adtr.schema_version`).Scan(&valid)
	if err != nil || !valid {
		f.t.Fatal("schema16 missing atomic directory contracts", err)
	}
	if err := Ready(f.ctx, f.config); err != nil {
		f.t.Fatal("schema16 readiness", err)
	}
}

func (f *operationalUpgradeFixture) stageHistoricalDirectory(task tasks.Task) {
	f.t.Helper()
	now := time.Now().UTC()
	body, err := json.Marshal(ldapconnection.DirectoryObservation{Objects: []directoryassets.Object{}, Source: ldapconnection.DirectorySource{ServerName: "dc." + task.DomainID + ".invalid", DCHostName: "dc." + task.DomainID + ".invalid", Domain: task.DomainID + ".invalid", NamingContext: "DC=" + task.DomainID + ",DC=invalid", StartedAt: now, CompletedAt: now, Pages: 1}})
	if err != nil {
		f.t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	f.tx(func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256)
 SELECT tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,0,$2,$3 FROM adtr.domain_directory_task_uses WHERE task_id=$1;
 INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result)
 SELECT tenant_id,actor_id,domain_id,'domain_directory_result',2,2,2,task_id,'success' FROM adtr.tasks WHERE task_id=$1;`, pgx.QueryExecModeSimpleProtocol, task.ID, body, hex.EncodeToString(digest[:]))
		return err
	})
}

func TestVersionFifteenDirectoryV2UpgradePreservesBytesAndAuthority(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	f.grantDirectory("history")
	f.grantDirectory("reserved")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryKind(), "domain_directory_task_uses")
	history := f.submitDirectory(engine, "history")
	run, done := f.start(engine, opened, history, "v15-directory-history")
	f.stageHistoricalDirectory(history)
	f.finish(run, done, false)
	reserved := f.submitDirectory(engine, "reserved")
	before, projection := f.snapshot(), f.auditProjection()
	delete(before, "schema_version")
	results := make(chan error, 3)
	for range 3 {
		go func() { results <- Migrate(f.ctx, f.config) }()
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal("concurrent production migration16", err)
		}
	}
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal("repeat production migration16", err)
	}
	f.assertDirectoryV2Installed()
	after := f.snapshot()
	for _, table := range []string{"domain_directory_task_uses", "domain_directory_observations"} {
		var original string
		if err := f.conn.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t)-'dictionary_version' ORDER BY (to_jsonb(t)-'dictionary_version')::text),'[]'::jsonb)::text FROM `+pgx.Identifier{"adtr", table}.Sanitize()+` t`).Scan(&original); err != nil {
			t.Fatal(err)
		}
		after[table] = original
	}
	for table, original := range before {
		if after[table] != original {
			t.Fatalf("migration16 changed historical %s bytes, provenance, authority or audit", table)
		}
	}
	if projection != f.auditProjection() {
		t.Fatal("migration16 changed historical audit projection")
	}
	var denied bool
	if err := f.conn.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v2') AND NOT EXISTS(SELECT FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read.v2') AND NOT EXISTS(SELECT FROM adtr.operation_account_use_audit WHERE purpose='domain.directory_read.v2') AND NOT EXISTS(SELECT FROM adtr.domain_directory_observations WHERE dictionary_version<>1) AND NOT EXISTS(SELECT FROM adtr.domain_directory_task_uses WHERE dictionary_version<>1)`).Scan(&denied); err != nil || !denied {
		t.Fatal("migration invented v2 data or authority", err)
	}
	f.assertDirectoryUse(reserved.ID, "reserved", 1)
	f.assertOldEngineRejected(engine, history)
	if err := MigrateDirectoryV2BaselineForTest(f.ctx, f.config); err == nil {
		t.Fatal("historical helper downgraded schema16")
	}
	f.assertDirectoryV2Installed()
	f.exec(`INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result) VALUES($1,$2,'history','domain_directory_v2_result',2,2,2,$3,'success')`, f.actor.TenantID, f.actor.ActorID, history.ID)
	var wrongProfileDenied bool
	if err := f.conn.QueryRow(f.ctx, `SELECT event_result='NONE' AND NOT result_available AND NOT(event_args ? 'taskState') FROM adtr.audit_source WHERE source='domain' AND event='domain_directory_v2_result' AND event_args->>'taskUUID'=$1`, history.ID).Scan(&wrongProfileDenied); err != nil || !wrongProfileDenied {
		t.Fatal("v2 audit borrowed success from a historical v1 task", err)
	}
}

func TestVersionFifteenDirectoryV2UpgradePreservesReservedGovernanceRecords(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	// An earlier component may have durably created real v2 grant incarnations,
	// receipts and audit. Their presence is a collision, never permission to
	// adopt the partial installation or to copy/rewrite those records.
	f.exec(credentialuse.DirectoryV2PurposeSchema)
	f.grantDirectoryV2("first")
	var records bool
	if err := f.conn.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v2') AND EXISTS(SELECT FROM adtr.operation_account_use_mutations WHERE purpose='domain.directory_read.v2') AND EXISTS(SELECT FROM adtr.operation_account_use_audit WHERE purpose='domain.directory_read.v2')`).Scan(&records); err != nil || !records {
		t.Fatal("governed reserved-purpose fixture is incomplete", err)
	}
	f.requireDirectoryV2Blocked(ErrDirectoryV2ReservedCollision)
}

func (f *operationalUpgradeFixture) requireDirectoryV2Blocked(want error) {
	f.t.Helper()
	before, catalog := f.snapshot(), f.catalogSnapshot()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	if err := Migrate(ctx, f.config); !errors.Is(err, want) {
		f.t.Fatal("migration16 did not reject incompatible history promptly", err)
	}
	if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
		f.t.Fatal("refused migration16 changed rows or left partial DDL")
	}
}

func TestVersionFifteenDirectoryV2UpgradeRequiresOriginalB2AndV1Returns(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	f.grantDirectory("second")
	b2, b2Opened := f.controlledEngine()
	v1, v1Opened := f.controlledLedgerEngine(domains.New(nil).DirectoryKind(), "domain_directory_task_uses")
	b2Task := f.submitAccount(b2, "first")
	b2Run, b2Done := f.start(b2, b2Opened, b2Task, "v16-blocked-b2")
	v1Task := f.submitDirectory(v1, "second")
	v1Run, v1Done := f.start(v1, v1Opened, v1Task, "v16-blocked-v1")
	f.requireDirectoryV2Blocked(ErrCredentialUseMigrationBlocked)
	f.finish(b2Run, b2Done, false)
	f.assertUse(b2Task.ID, "quiesced", 0)
	f.requireDirectoryV2Blocked(ErrCredentialUseMigrationBlocked)
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 day' WHERE task_id=$1`, v1Task.ID)
	if n, err := v1.RecoverExpired(f.ctx); err != nil || n != 1 {
		t.Fatal("historical directory lease recovery", n, err)
	}
	f.requireDirectoryV2Blocked(ErrCredentialUseMigrationBlocked)
	f.raceUpgradeAgainstReturn(v1, v1Run, v1Done, Migrate)
	f.assertDirectoryInstalled()
	f.assertDirectoryUse(v1Task.ID, "quiesced", 0)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal("genuine executor return did not unblock migration16", err)
	}
	f.assertDirectoryV2Installed()
}

func TestVersionFifteenDirectoryV2UpgradeRejectsCollisionsAndUnexpectedShapes(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		want      error
	}{
		{"queued-kind", `INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'unknown-v2',tenant_id,'first','domain.directory_read.v2',1,'{"unknown":"preserve"}',repeat('a',64),id,authorization_version::text,'unknown-v2','queued',1 FROM adtr.users`, ErrDirectoryV2ReservedCollision},
		{"terminal-kind", `INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) SELECT 'unknown-v2',tenant_id,'first','domain.directory_read.v2',1,'{"unknown":"preserve"}',repeat('a',64),id,authorization_version::text,'unknown-v2','succeeded',1 FROM adtr.users`, ErrDirectoryV2ReservedCollision},
		{"dependency", `INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES('historical-upgrade','first','first-account','domain.directory_read.v2','unknown-v2')`, ErrDirectoryV2ReservedCollision},
		{"use-provenance", `ALTER TABLE adtr.domain_directory_task_uses ADD dictionary_version text`, ErrDirectoryV2ReservedCollision},
		{"observation-provenance", `ALTER TABLE adtr.domain_directory_observations ADD dictionary_version integer`, ErrDirectoryV2ReservedCollision},
		{"dependency-index", `CREATE INDEX domain_directory_versioned_use_identity ON adtr.operation_account_dependencies(object_id)`, ErrDirectoryV2ReservedCollision},
		{"observation-index", `CREATE INDEX domain_directory_observations_versioned_latest ON adtr.domain_directory_observations(task_id)`, ErrDirectoryV2ReservedCollision},
		{"canonical-function-overload", `CREATE FUNCTION adtr.domain_directory_observation_canonical_v2(text) RETURNS text LANGUAGE sql AS $$ SELECT $1 $$`, ErrDirectoryV2ReservedCollision},
		{"base64-function-overload", `CREATE FUNCTION adtr.domain_directory_observation_base64_v2(text) RETURNS text LANGUAGE sql AS $$ SELECT $1 $$`, ErrDirectoryV2ReservedCollision},
		{"foreign-use-pins", `CREATE OR REPLACE FUNCTION adtr.domain_directory_use_pins(u adtr.domain_directory_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$`, ErrDirectoryV2SchemaShape},
		{"foreign-publication-guard", `CREATE OR REPLACE FUNCTION adtr.domain_directory_observation_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$`, ErrDirectoryV2SchemaShape},
		{"foreign-immutable-function", `CREATE OR REPLACE FUNCTION adtr.reject_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$`, ErrDirectoryV2SchemaShape},
		{"foreign-audit-function", `CREATE OR REPLACE FUNCTION adtr.audit_event_deletable(source text,event text) RETURNS boolean LANGUAGE SQL IMMUTABLE AS $$ SELECT true $$`, ErrDirectoryV2SchemaShape},
		{"foreign-audit-view", `ALTER VIEW adtr.audit_source SET (security_barrier=true)`, ErrDirectoryV2SchemaShape},
		{"foreign-grant-event", `CREATE OR REPLACE FUNCTION adtr.credential_use_grant_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$`, ErrDirectoryV2SchemaShape},
		{"foreign-receipt-guard", `CREATE OR REPLACE FUNCTION adtr.credential_use_receipt_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$`, ErrDirectoryV2SchemaShape},
		{"foreign-audit-capture", `CREATE OR REPLACE FUNCTION adtr.capture_audit_metadata() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$`, ErrDirectoryV2SchemaShape},
		{"missing-domain-audit-immutable", `DROP TRIGGER domain_audit_immutable ON adtr.domain_audit`, ErrDirectoryV2SchemaShape},
		{"disabled-domain-audit-immutable", `ALTER TABLE adtr.domain_audit DISABLE TRIGGER domain_audit_immutable`, ErrDirectoryV2SchemaShape},
		{"missing-domain-audit-truncate", `DROP TRIGGER domain_audit_no_truncate ON adtr.domain_audit`, ErrDirectoryV2SchemaShape},
		{"disabled-domain-audit-truncate", `ALTER TABLE adtr.domain_audit DISABLE TRIGGER domain_audit_no_truncate`, ErrDirectoryV2SchemaShape},
		{"missing-domain-audit-capture", `DROP TRIGGER domain_audit_capture ON adtr.domain_audit`, ErrDirectoryV2SchemaShape},
		{"missing-receipt-immutable", `DROP TRIGGER credential_use_receipts_immutable ON adtr.operation_account_use_mutations`, ErrDirectoryV2SchemaShape},
		{"disabled-receipt-immutable", `ALTER TABLE adtr.operation_account_use_mutations DISABLE TRIGGER credential_use_receipts_immutable`, ErrDirectoryV2SchemaShape},
		{"missing-receipt-truncate", `DROP TRIGGER credential_use_receipts_no_truncate ON adtr.operation_account_use_mutations`, ErrDirectoryV2SchemaShape},
		{"disabled-receipt-truncate", `ALTER TABLE adtr.operation_account_use_mutations DISABLE TRIGGER credential_use_receipts_no_truncate`, ErrDirectoryV2SchemaShape},
		{"missing-credential-audit-immutable", `DROP TRIGGER credential_use_audit_immutable ON adtr.operation_account_use_audit`, ErrDirectoryV2SchemaShape},
		{"disabled-credential-audit-immutable", `ALTER TABLE adtr.operation_account_use_audit DISABLE TRIGGER credential_use_audit_immutable`, ErrDirectoryV2SchemaShape},
		{"missing-credential-audit-truncate", `DROP TRIGGER credential_use_audit_no_truncate ON adtr.operation_account_use_audit`, ErrDirectoryV2SchemaShape},
		{"disabled-credential-audit-truncate", `ALTER TABLE adtr.operation_account_use_audit DISABLE TRIGGER credential_use_audit_no_truncate`, ErrDirectoryV2SchemaShape},
		{"missing-grants-truncate", `DROP TRIGGER credential_use_grants_no_truncate ON adtr.operation_account_use_grants`, ErrDirectoryV2SchemaShape},
		{"disabled-grants-truncate", `ALTER TABLE adtr.operation_account_use_grants DISABLE TRIGGER credential_use_grants_no_truncate`, ErrDirectoryV2SchemaShape},
		{"missing-grant-event-trigger", `DROP TRIGGER credential_use_grant_event ON adtr.operation_account_use_grants`, ErrDirectoryV2SchemaShape},
		{"disabled-grant-event-trigger", `ALTER TABLE adtr.operation_account_use_grants DISABLE TRIGGER credential_use_grant_event`, ErrDirectoryV2SchemaShape},
		{"missing-receipt-guard-trigger", `DROP TRIGGER credential_use_receipt_guard ON adtr.operation_account_use_mutations`, ErrDirectoryV2SchemaShape},
		{"disabled-credential-audit-capture", `ALTER TABLE adtr.operation_account_use_audit DISABLE TRIGGER credential_use_audit_capture`, ErrDirectoryV2SchemaShape},
		{"missing-immutable-guard", `DROP TRIGGER domain_directory_observations_immutable ON adtr.domain_directory_observations`, ErrDirectoryV2SchemaShape},
		{"disabled-immutable-guard", `ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`, ErrDirectoryV2SchemaShape},
		{"extra-observation-column", `ALTER TABLE adtr.domain_directory_observations ADD unknown_original text`, ErrDirectoryV2SchemaShape},
		{"duplicate-task-missing-account-fk", `DO $$ DECLARE n text; BEGIN SELECT conname INTO n FROM pg_constraint WHERE conrelid='adtr.domain_directory_task_uses'::regclass AND contype='f' AND confrelid='adtr.operation_accounts'::regclass; EXECUTE format('ALTER TABLE adtr.domain_directory_task_uses DROP CONSTRAINT %I',n);END; $$; ALTER TABLE adtr.domain_directory_task_uses ADD CONSTRAINT unknown_duplicate_task_fk FOREIGN KEY(task_id) REFERENCES adtr.tasks(task_id)`, ErrDirectoryV2SchemaShape},
		{"duplicate-account-missing-task-fk", `DO $$ DECLARE n text; BEGIN SELECT conname INTO n FROM pg_constraint WHERE conrelid='adtr.domain_directory_task_uses'::regclass AND contype='f' AND confrelid='adtr.tasks'::regclass; EXECUTE format('ALTER TABLE adtr.domain_directory_task_uses DROP CONSTRAINT %I',n);END; $$; ALTER TABLE adtr.domain_directory_task_uses ADD CONSTRAINT unknown_duplicate_account_fk FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id)`, ErrDirectoryV2SchemaShape},
		{"weak-ledger-check", `ALTER TABLE adtr.domain_directory_task_uses DROP CONSTRAINT domain_directory_task_uses_purpose_check; ALTER TABLE adtr.domain_directory_task_uses ADD CONSTRAINT domain_directory_task_uses_purpose_check CHECK(purpose<>'')`, ErrDirectoryV2SchemaShape},
		{"missing-dependency-check", `ALTER TABLE adtr.operation_account_dependencies DROP CONSTRAINT operation_account_dependency_pins`, ErrDirectoryV2SchemaShape},
		{"purpose-fragment-only", credentialuse.DirectoryV2PurposeSchema, ErrDirectoryV2SchemaShape},
		{"audit-fragment-only", domains.DirectoryAuditV2Schema, ErrDirectoryV2SchemaShape},
		{"reserved-audit-record", domains.DirectoryAuditV2Schema + `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result) SELECT tenant_id,id,'first','domain_directory_v2_submit',1,1,1,'unknown-original' FROM adtr.users`, ErrDirectoryV2ReservedCollision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDirectoryV2UpgradeFixture(t)
			f.exec(tc.sql)
			for range 2 {
				f.requireDirectoryV2Blocked(tc.want)
			}
			var version int
			if err := f.conn.QueryRow(f.ctx, `SELECT version FROM adtr.schema_version`).Scan(&version); err != nil || version != 15 {
				t.Fatal("refusal advanced historical marker", version, err)
			}
		})
	}
}

func TestVersionFifteenDirectoryV2UpgradeRollsBackLateDDLFailure(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	// A disposable-database event trigger fails after the purpose, dependency,
	// ledger and provenance work. No historical trigger or record is disabled.
	f.exec(`CREATE FUNCTION adtr.fixture_reject_directory_v2_late() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN
 IF EXISTS(SELECT FROM pg_event_trigger_ddl_commands() WHERE object_identity LIKE 'adtr.domain_directory_observation_base64_v2(%') THEN RAISE EXCEPTION 'synthetic late fragment failure';END IF;END; $$;
 CREATE EVENT TRIGGER fixture_directory_v2_late ON ddl_command_end EXECUTE FUNCTION adtr.fixture_reject_directory_v2_late();`)
	before, catalog := f.snapshot(), f.catalogSnapshot()
	for range 2 {
		if err := Migrate(f.ctx, f.config); err == nil {
			t.Fatal("late fragment failure unexpectedly committed")
		}
		if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
			t.Fatal("late failure left changed provenance, functions, constraints or rows")
		}
	}
	f.assertDirectoryInstalled()
	f.exec(`DROP EVENT TRIGGER fixture_directory_v2_late; DROP FUNCTION adtr.fixture_reject_directory_v2_late()`)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal("retry after removing the test-only DDL failure", err)
	}
	f.assertDirectoryV2Installed()
}

func TestVersionFifteenDirectoryV2UpgradeFencesLateOpening(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "B2", true: "directory-v1"}[directory], func(t *testing.T) {
			f := newDirectoryV2UpgradeFixture(t)
			var engine *tasks.Engine
			var task tasks.Task
			ledger := "domain_account_task_uses"
			if directory {
				f.grantDirectory("first")
				engine, _ = f.controlledLedgerEngine(domains.New(nil).DirectoryKind(), "domain_directory_task_uses")
				task = f.submitDirectory(engine, "first")
				ledger = "domain_directory_task_uses"
			} else {
				engine, _ = f.controlledEngine()
				task = f.submitAccount(engine, "first")
			}
			lease, err := engine.Claim(f.ctx, "v15-late-open")
			if err != nil || lease.Task.ID != task.ID {
				t.Fatal("claim", err)
			}
			lease, err = engine.Start(f.ctx, lease)
			if err != nil {
				t.Fatal(err)
			}
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
			if err = tasks.CheckSchemaTx(f.ctx, tx, 15); err != nil {
				t.Fatal(err)
			}
			cfg := f.config.Copy()
			cfg.RuntimeParams["application_name"] = "adtr-v16-late-open"
			migration := make(chan error, 1)
			go func() { migration <- Migrate(f.ctx, cfg) }()
			f.awaitMigrationGate(cfg.RuntimeParams["application_name"])
			called := false
			opening := make(chan error, 1)
			go func() {
				_, err := engine.WithTx(f.ctx, lease, lease.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
					called = true
					_, err := tx.Exec(ctx, `UPDATE `+pgx.Identifier{"adtr", ledger}.Sanitize()+` SET state='opened',opened_at=clock_timestamp(),opener_owner=$2,opener_fencing_token=$3,opener_attempt=$4 WHERE task_id=$1 AND state='reserved'`, task.ID, lease.Owner, lease.Token, lease.Task.Attempt)
					return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
				})
				opening <- err
			}()
			f.awaitHistoricalEngineGate(engine, 0)
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-migration; err != nil {
				t.Fatal("reserved-only upgrade", err)
			}
			if err = <-opening; called || !errors.Is(err, tasks.ErrSchemaIncompatible) {
				t.Fatal("old binary opened credentials after migration16", err)
			}
			f.assertDirectoryV2Installed()
		})
	}
}

func (f *operationalUpgradeFixture) grantDirectoryV2(domain string) string {
	f.t.Helper()
	var revision string
	f.tx(func(tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(f.ctx, tx, f.actor, credentialuse.GrantUse); err != nil {
			return err
		}
		out, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.actor, "/v2/grant", credentialuse.Input{AccountID: domain + "-account", RoleID: "platform_admin", Purpose: credentialuse.DirectoryV2Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "grant-v2-" + domain}, []string{domain}, credentialuse.DirectoryV2Purpose)
		revision = out.GrantRevision
		return err
	})
	return revision
}

func (f *operationalUpgradeFixture) submitDirectoryV2(engine *tasks.Engine, domain, grant string) tasks.Task {
	f.t.Helper()
	var out tasks.Submission
	f.tx(func(tx pgx.Tx) error {
		payload, _ := json.Marshal(map[string]any{"credentialSource": "operation_account", "connectionRevision": "2", "connectionCredentialGeneration": "2", "accountId": domain + "-account", "accountCredentialRevision": "1", "grantRoleId": "platform_admin", "grantRevision": grant, "policyRevision": strings.Repeat("a", 64), "dictionaryVersion": 2})
		var err error
		out, err = engine.SubmitTx(f.ctx, tx, f.actor, tasks.SubmitInput{TaskName: domains.DirectoryV2KindName, DomainID: domain, PayloadVersion: 1, Payload: payload, IdempotencyKey: "v2-" + domain})
		if err == nil {
			_, err = tx.Exec(f.ctx, `INSERT INTO adtr.domain_directory_task_uses(tenant_id,domain_id,task_id,account_id,account_credential_revision,connection_revision,connection_credential_generation,policy_revision,grant_role_id,grant_revision,actor_id,purpose,state,dictionary_version)
 VALUES($1,$2,$3,$2||'-account',1,2,2,repeat('a',64),'platform_admin',$4,$5,'domain.directory_read.v2','reserved',2);
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation)
 VALUES($1,$2,$2||'-account','domain.directory_read.v2',$3,1,2);`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID, domain, out.Task.ID, grant, f.actor.ActorID)
		}
		return err
	})
	return out.Task
}

func TestVersionFifteenPartialV2OpenedUseStillRequiresOriginalReturn(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	// Historical component-only install: never mislabel this as a legitimate
	// schema16 runtime or falsify the schema marker. Even this partial install
	// must retain the original opener before the migrator inspects its shape.
	f.exec(credentialuse.DirectoryV2PurposeSchema + domains.DirectoryDependencyV2Schema + domains.DirectoryUseV2Schema)
	grant := f.grantDirectoryV2("first")
	kind := domains.New(nil).DirectoryV2Kind()
	engine, opened := f.controlledLedgerEngine(kind, "domain_directory_task_uses")
	task := f.submitDirectoryV2(engine, "first", grant)
	run, done := f.start(engine, opened, task, "historical-component-v2")
	f.requireDirectoryV2Blocked(ErrCredentialUseMigrationBlocked)
	f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 day' WHERE task_id=$1`, task.ID)
	if n, err := engine.RecoverExpired(f.ctx); err != nil || n != 1 {
		t.Fatal("recover historical component v2 attempt", n, err)
	}
	f.raceUpgradeAgainstReturn(engine, run, done, Migrate)
	var returned bool
	if err := f.conn.QueryRow(f.ctx, `SELECT state='quiesced' AND quiescence_reason='executor_returned' AND NOT EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE object_id=$1 AND consumer_kind='domain.directory_read.v2') FROM adtr.domain_directory_task_uses WHERE task_id=$1`, task.ID).Scan(&returned); err != nil || !returned {
		t.Fatal("partial v2 install lost original-opener acknowledgement", err)
	}
	f.requireDirectoryV2Blocked(ErrDirectoryV2ReservedCollision)
}
