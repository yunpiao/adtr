//go:build integration

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newDirectoryV4UpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	t.Helper()
	f := newDirectoryV3UpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 19); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 19
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("current binary accepted schema19")
	}
	return f
}
func TestSchema20PreservesNonemptyV1V2V3HistoryAndDoesNotGrant(t *testing.T) {
	f := newDirectoryV4UpgradeFixture(t)
	f.grantDirectory("history")
	grant := f.grantDirectoryV2("first")
	engine1, opened1 := f.controlledLedgerEngine(domains.New(nil).DirectoryKind(), "domain_directory_task_uses")
	task1 := f.submitDirectory(engine1, "history")
	run1, done1 := f.start(engine1, opened1, task1, "v3-history-v1")
	body1 := stageNonemptyDirectoryHistoryV3Test(t, f, task1, 1)
	f.finish(run1, done1, false)
	engine2, opened2 := f.controlledLedgerEngine(domains.New(nil).DirectoryV2Kind(), "domain_directory_task_uses")
	task2 := f.submitDirectoryV2(engine2, "first", grant)
	run2, done2 := f.start(engine2, opened2, task2, "v3-history-v2")
	body2 := stageNonemptyDirectoryHistoryV3Test(t, f, task2, 2)
	f.finish(run2, done2, false)
	grant3 := f.grantDirectoryV3("first")
	engine3, opened3 := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task3 := f.submitDirectoryV3(engine3, "first", grant3)
	run3, done3 := f.start(engine3, opened3, task3, "v4-history-v3")
	body3 := stageNonemptyDirectoryV3HistoryForV4Test(t, f, task3)
	f.finish(run3, done3, false)
	var legacyValidators string
	if err := f.conn.QueryRow(f.ctx, `SELECT jsonb_object_agg(proname,prosrc)::text FROM pg_proc WHERE pronamespace='adtr'::regnamespace AND proname IN ('domain_directory_observation_canonical_v2','domain_directory_observation_base64_v2','domain_directory_observation_canonical_v3','domain_directory_identity_v3')`).Scan(&legacyValidators); err != nil {
		t.Fatal(err)
	}
	before, projection := f.snapshot(), f.auditProjection()
	delete(before, "schema_version")
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal("repeat", err)
	}
	if err := Ready(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	var retainedValidators string
	if err := f.conn.QueryRow(f.ctx, `SELECT jsonb_object_agg(proname,prosrc)::text FROM pg_proc WHERE pronamespace='adtr'::regnamespace AND proname IN ('domain_directory_observation_canonical_v2','domain_directory_observation_base64_v2','domain_directory_observation_canonical_v3','domain_directory_identity_v3')`).Scan(&retainedValidators); err != nil || retainedValidators != legacyValidators {
		t.Fatal("historical validators changed", err)
	}
	after := f.snapshot()
	for table, want := range before {
		if after[table] != want {
			t.Fatalf("schema20 rewrote %s history", table)
		}
	}
	if projection != f.auditProjection() {
		t.Fatal("old audit interpretation changed")
	}
	for id, want := range map[string][]byte{task1.ID: body1, task2.ID: body2, task3.ID: body3} {
		var raw []byte
		var digest string
		if err := f.conn.QueryRow(f.ctx, `SELECT body,sha256 FROM adtr.domain_directory_observations WHERE task_id=$1`, id).Scan(&raw, &digest); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(want)
		if !bytes.Equal(raw, want) || digest != hex.EncodeToString(sum[:]) {
			t.Fatal("old body/digest changed")
		}
	}
	var valid bool
	if err := f.conn.QueryRow(f.ctx, `SELECT version=$1 AND NOT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v4') AND NOT EXISTS(SELECT FROM adtr.domain_directory_observations WHERE dictionary_version=4) AND to_regprocedure('adtr.domain_directory_membership_v4(jsonb,text)') IS NOT NULL FROM adtr.schema_version`, SchemaVersion).Scan(&valid); err != nil || !valid {
		t.Fatal("migration invented authority/data or missed v4 guard", err)
	}
	f.assertOldEngineRejected(engine2, task2)
}
func TestSchema20RequiresOriginalV2ExecutorReturn(t *testing.T) {
	f := newDirectoryV4UpgradeFixture(t)
	grant := f.grantDirectoryV2("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV2Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV2(engine, "first", grant)
	run, done := f.start(engine, opened, task, "v3-opened-v2")
	before := f.snapshot()
	if err := Migrate(f.ctx, f.config); !errors.Is(err, ErrCredentialUseMigrationBlocked) {
		t.Fatal("open use did not block", err)
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("blocked migration changed rows")
	}
	f.finish(run, done, false)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal("real return did not unblock", err)
	}
}
func TestSchema20RejectsForeignHistoricalGuardAndReservedIdentity(t *testing.T) {
	for _, mode := range []string{"guard", "column", "reserved", "canonical_v2", "canonical_v3", "member_base64", "dependency_index"} {
		t.Run(mode, func(t *testing.T) {
			f := newDirectoryV4UpgradeFixture(t)
			want := ErrDirectoryV4SchemaShape
			switch mode {
			case "guard":
				f.exec(`CREATE OR REPLACE FUNCTION adtr.domain_directory_use_current(u adtr.domain_directory_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$;`)
			case "column":
				f.exec(`ALTER TABLE adtr.domain_directory_observations ADD COLUMN foreign_state text`)
			case "canonical_v2":
				f.exec(`CREATE OR REPLACE FUNCTION adtr.domain_directory_observation_canonical_v2(b jsonb) RETURNS text LANGUAGE plpgsql IMMUTABLE STRICT AS $$ BEGIN RETURN '{}'; END; $$;`)
			case "canonical_v3":
				f.exec(`CREATE OR REPLACE FUNCTION adtr.domain_directory_observation_canonical_v3(b jsonb) RETURNS text LANGUAGE plpgsql IMMUTABLE STRICT AS $$ BEGIN RETURN '{}'; END; $$;`)
			case "member_base64":
				f.exec(`CREATE OR REPLACE FUNCTION adtr.domain_directory_observation_base64_v2(b jsonb, cap integer) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN RETURN true; END; $$;`)
			case "dependency_index":
				f.exec(`DROP INDEX adtr.domain_directory_versioned_use_identity`)
			case "reserved":
				f.exec(`CREATE FUNCTION adtr.domain_directory_membership_v4(jsonb,text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$ SELECT true $$;`)
				want = ErrDirectoryV4ReservedCollision
			}
			before, catalog := f.snapshot(), f.catalogSnapshot()
			if err := Migrate(f.ctx, f.config); !errors.Is(err, want) {
				t.Fatal("unexpected schema adopted", err)
			}
			if !reflect.DeepEqual(before, f.snapshot()) || catalog != f.catalogSnapshot() {
				t.Fatal("failed migration changed history/catalog")
			}
		})
	}
}

func (f *operationalUpgradeFixture) grantDirectoryV3(domain string) string {
	f.t.Helper()
	var revision string
	f.tx(func(tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(f.ctx, tx, f.actor, credentialuse.GrantUse); err != nil {
			return err
		}
		out, err := credentialuse.New().MutateForPurposeTx(f.ctx, tx, f.actor, "/grant", credentialuse.Input{AccountID: domain + "-account", RoleID: "platform_admin", Purpose: credentialuse.DirectoryV3Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "grant-v3-" + domain}, []string{domain}, credentialuse.DirectoryV3Purpose)
		revision = out.GrantRevision
		return err
	})
	return revision
}

func (f *operationalUpgradeFixture) submitDirectoryV3(engine *tasks.Engine, domain, grant string) tasks.Task {
	f.t.Helper()
	var out tasks.Submission
	f.tx(func(tx pgx.Tx) error {
		payload, _ := json.Marshal(map[string]any{"credentialSource": "operation_account", "connectionRevision": "2", "connectionCredentialGeneration": "2", "accountId": domain + "-account", "accountCredentialRevision": "1", "grantRoleId": "platform_admin", "grantRevision": grant, "policyRevision": strings.Repeat("a", 64), "dictionaryVersion": 3})
		var err error
		out, err = engine.SubmitTx(f.ctx, tx, f.actor, tasks.SubmitInput{TaskName: domains.DirectoryV3KindName, DomainID: domain, PayloadVersion: 1, Payload: payload, IdempotencyKey: "v3-" + domain})
		if err == nil {
			_, err = tx.Exec(f.ctx, `INSERT INTO adtr.domain_directory_task_uses(tenant_id,domain_id,task_id,account_id,account_credential_revision,connection_revision,connection_credential_generation,policy_revision,grant_role_id,grant_revision,actor_id,purpose,state,dictionary_version)
 VALUES($1,$2,$3,$2||'-account',1,2,2,repeat('a',64),'platform_admin',$4,$5,'domain.directory_read.v3','reserved',3);
 INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id,account_credential_revision,connection_credential_generation)
 VALUES($1,$2,$2||'-account','domain.directory_read.v3',$3,1,2);`, pgx.QueryExecModeSimpleProtocol, f.actor.TenantID, domain, out.Task.ID, grant, f.actor.ActorID)
		}
		return err
	})
	return out.Task
}

func stageNonemptyDirectoryV3HistoryForV4Test(t *testing.T, f *operationalUpgradeFixture, task tasks.Task) []byte {
	t.Helper()
	now := time.Now().UTC()
	source := ldapconnection.DirectorySource{ServerName: "dc." + task.DomainID + ".invalid", DCHostName: "dc." + task.DomainID + ".invalid", Domain: task.DomainID + ".invalid", NamingContext: "DC=" + task.DomainID + ",DC=invalid", StartedAt: now, CompletedAt: now, Pages: 1}
	entry := directoryassets.Entry{DN: "CN=PreservedV3," + source.NamingContext, Attributes: []directoryassets.Attribute{{Name: "objectGUID", Values: [][]byte{make([]byte, 16)}}, {Name: "objectClass", Values: [][]byte{[]byte("user")}}, {Name: "primaryGroupID", Values: [][]byte{[]byte("513")}}, {Name: "servicePrincipalName", Values: [][]byte{[]byte("HOST/Z"), []byte("host/a\x00\n😀")}}}}
	object, err := directoryassets.DecodeV3(entry)
	if err != nil {
		t.Fatal(err)
	}
	defer directoryassets.ClearStoredObjectsV3([]directoryassets.StoredObjectV3{object})
	body, err := json.Marshal(struct {
		DictionaryVersion int                              `json:"dictionaryVersion"`
		Objects           []directoryassets.StoredObjectV3 `json:"objects"`
		Source            ldapconnection.DirectorySource   `json:"source"`
	}{3, []directoryassets.StoredObjectV3{object}, source})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	f.tx(func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256,dictionary_version) SELECT tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,1,$2,$3,3 FROM adtr.domain_directory_task_uses WHERE task_id=$1`, task.ID, body, hex.EncodeToString(sum[:]))
		return err
	})
	return body
}

func TestSchema20RequiresOriginalV3ExecutorReturn(t *testing.T) {
	f := newDirectoryV4UpgradeFixture(t)
	grant := f.grantDirectoryV3("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV3Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV3(engine, "first", grant)
	run, done := f.start(engine, opened, task, "v4-opened-v3")
	before := f.snapshot()
	if err := Migrate(f.ctx, f.config); !errors.Is(err, ErrCredentialUseMigrationBlocked) {
		t.Fatal("open use did not block", err)
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("blocked migration changed rows")
	}
	f.finish(run, done, false)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal("real return did not unblock", err)
	}
}
