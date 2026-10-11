//go:build integration

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newDirectoryV3UpgradeFixture(t *testing.T) *operationalUpgradeFixture {
	f := newDirectoryV2UpgradeFixture(t)
	if err := migrateTo(f.ctx, f.config, 18); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 18
	if Ready(f.ctx, f.config) == nil {
		t.Fatal("current binary accepted schema18")
	}
	return f
}
func stageNonemptyDirectoryHistoryV3Test(t *testing.T, f *operationalUpgradeFixture, task tasks.Task, version int) []byte {
	t.Helper()
	now := time.Now().UTC()
	source := ldapconnection.DirectorySource{ServerName: "dc." + task.DomainID + ".invalid", DCHostName: "dc." + task.DomainID + ".invalid", Domain: task.DomainID + ".invalid", NamingContext: "DC=" + task.DomainID + ",DC=invalid", StartedAt: now, CompletedAt: now, Pages: 1}
	entry := directoryassets.Entry{DN: "CN=Preserved," + source.NamingContext, Attributes: []directoryassets.Attribute{{Name: "objectGUID", Values: [][]byte{make([]byte, 16)}}, {Name: "objectClass", Values: [][]byte{[]byte("user")}}}}
	var body []byte
	var err error
	if version == 1 {
		var object directoryassets.Object
		object, err = directoryassets.Decode(entry)
		if err == nil {
			body, err = json.Marshal(ldapconnection.DirectoryObservation{Objects: []directoryassets.Object{object}, Source: source})
		}
	} else {
		entry.Attributes = append(entry.Attributes, directoryassets.Attribute{Name: "mail", Values: [][]byte{[]byte("old\x00😀@example.test")}})
		var object directoryassets.StoredObjectV2
		object, err = directoryassets.DecodeV2(entry)
		if err == nil {
			body, err = json.Marshal(struct {
				DictionaryVersion int                              `json:"dictionaryVersion"`
				Objects           []directoryassets.StoredObjectV2 `json:"objects"`
				Source            ldapconnection.DirectorySource   `json:"source"`
			}{2, []directoryassets.StoredObjectV2{object}, source})
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	f.tx(func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256,dictionary_version) SELECT tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,1,$2,$3,$4 FROM adtr.domain_directory_task_uses WHERE task_id=$1`, task.ID, body, hex.EncodeToString(sum[:]), version)
		return err
	})
	return body
}
func TestSchema19PreservesNonemptyV1V2HistoryAndDoesNotGrant(t *testing.T) {
	f := newDirectoryV3UpgradeFixture(t)
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
	after := f.snapshot()
	for table, want := range before {
		if after[table] != want {
			t.Fatalf("schema19 rewrote %s history", table)
		}
	}
	if projection != f.auditProjection() {
		t.Fatal("old audit interpretation changed")
	}
	for id, want := range map[string][]byte{task1.ID: body1, task2.ID: body2} {
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
	if err := f.conn.QueryRow(f.ctx, `SELECT version=19 AND NOT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE purpose='domain.directory_read.v3') AND NOT EXISTS(SELECT FROM adtr.domain_directory_observations WHERE dictionary_version=3) AND to_regprocedure('adtr.domain_directory_identity_v3(jsonb)') IS NOT NULL FROM adtr.schema_version`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration invented authority/data or missed v3 guard", err)
	}
	f.assertOldEngineRejected(engine2, task2)
}
func TestSchema19RequiresOriginalV2ExecutorReturn(t *testing.T) {
	f := newDirectoryV3UpgradeFixture(t)
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
func TestSchema19RejectsForeignHistoricalGuardAndReservedIdentity(t *testing.T) {
	for _, mode := range []string{"guard", "column", "reserved"} {
		t.Run(mode, func(t *testing.T) {
			f := newDirectoryV3UpgradeFixture(t)
			want := ErrDirectoryV3SchemaShape
			switch mode {
			case "guard":
				f.exec(`CREATE OR REPLACE FUNCTION adtr.domain_directory_use_current(u adtr.domain_directory_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$;`)
			case "column":
				f.exec(`ALTER TABLE adtr.domain_directory_observations ADD COLUMN foreign_state text`)
			case "reserved":
				f.exec(`CREATE FUNCTION adtr.domain_directory_identity_v3(jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$ SELECT true $$;`)
				want = ErrDirectoryV3ReservedCollision
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

// PostgreSQL validates raw bytes without ever converting SPN NUL into text.
func TestSchema19IdentityGuardMatchesGoBoundaryProfile(t *testing.T) {
	f := newDirectoryV3UpgradeFixture(t)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name     string
		identity any
		valid    bool
	}{
		{"missing", directoryassets.RawIdentityV3{}, true},
		{"zero", directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("0")}, true},
		{"uint32", directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("4294967295")}, true},
		{"nul order case", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{[]byte("HOST/Z"), []byte("host/a\x00\n😀<script>"), []byte("HOST/a")}}, true},
		{"overflow", directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("4294967296")}, false},
		{"signed", directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("-1")}, false},
		{"leading zero", directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("01")}, false},
		{"empty primary", directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte{}}, false},
		{"nul primary", directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("1\x00")}, false},
		{"empty set", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{}}, false},
		{"empty value", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{[]byte{}}}, false},
		{"null value", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{nil}}, false},
		{"duplicate", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{[]byte("HOST/a"), []byte("HOST/a")}}, false},
		{"invalid utf8", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{{0xff}}}, false},
		{"units", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{[]byte(strings.Repeat("😀", 513))}}, false},
		{"unknown key", map[string]any{"primaryGroupIDBytes": nil, "servicePrincipalNameBytes": nil, "extra": nil}, false},
		{"omitted key", map[string]any{"primaryGroupIDBytes": nil}, false},
		{"noncanonical base64", map[string]any{"primaryGroupIDBytes": "MDE=\n", "servicePrincipalNameBytes": nil}, false},
		{"string spn", map[string]any{"primaryGroupIDBytes": nil, "servicePrincipalNameBytes": enc("HOST/a")}, false},
	}
	for _, n := range []int{128, 129} {
		values := make([][]byte, n)
		for i := range values {
			values[i] = []byte(fmt.Sprintf("HOST/%d", i))
		}
		cases = append(cases, struct {
			name     string
			identity any
			valid    bool
		}{fmt.Sprintf("count-%d", n), directoryassets.RawIdentityV3{ServicePrincipalNameBytes: values}, n == 128})
	}
	for _, n := range []int{1024, 1025} {
		cases = append(cases, struct {
			name     string
			identity any
			valid    bool
		}{fmt.Sprintf("ascii-units-%d", n), directoryassets.RawIdentityV3{ServicePrincipalNameBytes: [][]byte{[]byte(strings.Repeat("a", n))}}, n == 1024})
	}
	values := make([][]byte, 32)
	for i := range values {
		values[i] = []byte(fmt.Sprintf("%02d", i) + strings.Repeat("x", 1022))
	}
	cases = append(cases, struct {
		name     string
		identity any
		valid    bool
	}{"aggregate-32768", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: values}, true})
	cases = append(cases, struct {
		name     string
		identity any
		valid    bool
	}{"aggregate-32769", directoryassets.RawIdentityV3{ServicePrincipalNameBytes: append(append([][]byte{}, values...), []byte("y"))}, false})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.identity)
			if err != nil {
				t.Fatal(err)
			}
			var valid bool
			if err = f.conn.QueryRow(f.ctx, `SELECT adtr.domain_directory_identity_v3($1::jsonb)`, string(body)).Scan(&valid); err != nil || valid != tc.valid {
				t.Fatalf("valid=%v want=%v err=%v", valid, tc.valid, err)
			}
		})
	}
}
