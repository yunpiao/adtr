//go:build integration

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func TestDirectoryV2ProductionStorageGuardLosslessAndCanonical(t *testing.T) {
	f := newDirectoryV2UpgradeFixture(t)
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.schemaVersion = 16
	grant := f.grantDirectoryV2("first")
	engine, opened := f.controlledLedgerEngine(domains.New(nil).DirectoryV2Kind(), "domain_directory_task_uses")
	task := f.submitDirectoryV2(engine, "first", grant)
	run, done := f.start(engine, opened, task, "v2-storage-guard")
	// Use the reviewed actual object codec. The synthetic controlled executor
	// proves SQL authority/fencing, not LDAP transport or production capacity.
	mail, description := "mail\x00\r\n\t🙂", "description\x00<&>\r\n\t🦉"
	sam := "canonical <&> \"quote\" \\slash \u2028\u2029"
	object, err := directoryassets.DecodeV2(directoryassets.Entry{DN: "CN=Supplemental,DC=first,DC=invalid", Attributes: []directoryassets.Attribute{
		{Name: "objectGUID", Values: [][]byte{make([]byte, 16)}},
		{Name: "objectClass", Values: [][]byte{[]byte("user")}},
		{Name: "sAMAccountName", Values: [][]byte{[]byte(sam)}},
		{Name: "objectSid", Values: [][]byte{{1, 1, 0, 0, 0, 0, 0, 5, 32, 0, 0, 0}}},
		{Name: "mail", Values: [][]byte{[]byte(mail)}},
		{Name: "description", Values: [][]byte{[]byte(description)}},
		{Name: "whenCreated", Values: [][]byte{[]byte("00010101000000.0Z")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	envelope := struct {
		DictionaryVersion int                              `json:"dictionaryVersion"`
		Objects           []directoryassets.StoredObjectV2 `json:"objects"`
		Source            ldapconnection.DirectorySource   `json:"source"`
	}{2, []directoryassets.StoredObjectV2{object}, ldapconnection.DirectorySource{ServerName: "dc.first.invalid", DCHostName: "dc.first.invalid", Domain: "first.invalid", NamingContext: "DC=first,DC=invalid", StartedAt: now, CompletedAt: now, Pages: 1}}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, escape := range []string{`\u003c`, `\u0026`, `\u003e`, `\"quote\"`, `\\slash`, `\u2028`, `\u2029`} {
		if !bytes.Contains(body, []byte(escape)) {
			t.Fatal("canonical base string fixture does not exercise required escaping")
		}
	}
	const insert = `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256,dictionary_version)
 SELECT tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,$2,$3,$4,$5 FROM adtr.domain_directory_task_uses WHERE task_id=$1`
	for _, tc := range []struct {
		name       string
		body       []byte
		count, pin int
		badDigest  bool
	}{
		{"wrong-dictionary-column", body, 1, 1, false},
		{"wrong-dictionary-envelope", bytes.Replace(body, []byte(`"dictionaryVersion":2`), []byte(`"dictionaryVersion":1`), 1), 1, 2, false},
		{"wrong-count", body, 0, 2, false},
		{"wrong-digest", body, 1, 2, true},
		{"leading-whitespace", append([]byte(" "), body...), 1, 2, false},
		{"duplicate-envelope-key", bytes.Replace(body, []byte(`"dictionaryVersion":2`), []byte(`"dictionaryVersion":2,"dictionaryVersion":2`), 1), 1, 2, false},
		{"unknown-source-field", bytes.Replace(body, []byte(`"pages":1`), []byte(`"pages":1,"unknown":1`), 1), 1, 2, false},
		{"wrong-source", bytes.Replace(body, []byte(`"domain":"first.invalid"`), []byte(`"domain":"other.invalid"`), 1), 1, 2, false},
		{"fractional-source-pages", bytes.Replace(body, []byte(`"pages":1`), []byte(`"pages":1.0`), 1), 1, 2, false},
		{"wrong-kind", bytes.Replace(body, []byte(`"kind":"user"`), []byte(`"kind":"group"`), 1), 1, 2, false},
		{"public-projection-nul", bytes.Replace(body, []byte(`"mailBytes":"`+base64.StdEncoding.EncodeToString([]byte(mail))+`"`), []byte(`"mailBytes":"mail\u0000"`), 1), 1, 2, false},
		{"noncanonical-base64", bytes.Replace(body, []byte(`"mailBytes":"`+base64.StdEncoding.EncodeToString([]byte(mail))+`"`), []byte(`"mailBytes":"AR=="`), 1), 1, 2, false},
		{"invalid-utf8", bytes.Replace(body, []byte(base64.StdEncoding.EncodeToString([]byte(mail))), []byte(base64.StdEncoding.EncodeToString([]byte{0xed, 0xa0, 0x80})), 1), 1, 2, false},
		{"invalid-sid", bytes.Replace(body, []byte(base64.StdEncoding.EncodeToString(object.Supplemental.ObjectSIDBytes)), []byte("AQ=="), 1), 1, 2, false},
		{"invalid-year-zero", bytes.Replace(body, []byte(base64.StdEncoding.EncodeToString([]byte("00010101000000.0Z"))), []byte(base64.StdEncoding.EncodeToString([]byte("00000101000000.0Z"))), 1), 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.count == 1 && tc.pin == 2 && !tc.badDigest && bytes.Equal(tc.body, body) {
				t.Fatal("invalid-data fixture did not change the canonical body")
			}
			tx, err := f.conn.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			digest := sha256.Sum256(tc.body)
			hash := hex.EncodeToString(digest[:])
			if tc.badDigest {
				hash = strings.Repeat("0", 64)
			}
			if _, err = tx.Exec(f.ctx, insert, task.ID, tc.count, tc.body, hash, tc.pin); err == nil {
				t.Fatal("SQL publication accepted an invalid stored envelope")
			}
		})
	}
	for _, field := range []string{"opener_owner", "opener_fencing_token", "opener_attempt", "actor_id"} {
		t.Run("wrong-"+field, func(t *testing.T) {
			tx, err := f.conn.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			digest := sha256.Sum256(body)
			query := insert
			// Mutate only the SELECT projection, retaining the real admitted use.
			projection := "SELECT tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,"
			replacement := field + "+1"
			if field == "opener_owner" {
				replacement = "opener_owner||'-forged'"
			}
			query = strings.Replace(query, projection, strings.Replace(projection, field, replacement, 1), 1)
			if _, err = tx.Exec(f.ctx, query, task.ID, 1, body, hex.EncodeToString(digest[:]), 2); err == nil {
				t.Fatal("SQL publication accepted different opener evidence")
			}
		})
	}
	digest := sha256.Sum256(body)
	f.tx(func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, insert, task.ID, 1, body, hex.EncodeToString(digest[:]), 2)
		if err == nil {
			_, err = tx.Exec(f.ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result) VALUES($1,$2,'first','domain_directory_v2_result',2,2,2,$3,'success')`, f.actor.TenantID, f.actor.ActorID, task.ID)
		}
		return err
	})
	assertAudit := func(result, state string, available bool) {
		t.Helper()
		var got, gotState string
		var gotAvailable bool
		if err := f.conn.QueryRow(f.ctx, `SELECT event_result,event_args->>'taskState',result_available FROM adtr.audit_source WHERE source='domain' AND event='domain_directory_v2_result' AND event_args->>'taskUUID'=$1`, task.ID).Scan(&got, &gotState, &gotAvailable); err != nil || got != result || gotState != state || gotAvailable != available {
			t.Fatal("v2 audit misrepresented execution evidence", got, gotState, gotAvailable, err)
		}
	}
	assertAudit("NONE", "running", false)
	var raw []byte
	var hash string
	if err := f.conn.QueryRow(f.ctx, `SELECT body,sha256 FROM adtr.domain_directory_observations WHERE task_id=$1 AND dictionary_version=2`, task.ID).Scan(&raw, &hash); err != nil || !bytes.Equal(raw, body) || hash != hex.EncodeToString(digest[:]) {
		t.Fatal("PostgreSQL changed canonical raw bytes or digest", err)
	}
	var stored struct {
		Objects []json.RawMessage `json:"objects"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil || len(stored.Objects) != 1 {
		t.Fatal("stored object envelope", err)
	}
	decoded, err := directoryassets.DecodeStoredObjectV2(stored.Objects[0])
	if err != nil {
		t.Fatal(err)
	}
	public, err := directoryassets.ProjectV2(decoded)
	if err != nil || public.SAMAccountName == nil || *public.SAMAccountName != sam || public.Mail == nil || *public.Mail != mail || len(public.Description) != 1 || public.Description[0] != description || public.WhenCreated == nil || *public.WhenCreated != "0001-01-01T00:00:00Z" || public.ObjectSID == nil || *public.ObjectSID != "S-1-5-32" {
		t.Fatal("lossless PostgreSQL NUL/control/non-BMP/year0001 roundtrip", err)
	}
	if err := Migrate(f.ctx, f.config); err != nil {
		t.Fatal("idempotent schema16 invocation rejected an opened v2 use", err)
	}
	f.finish(run, done, false)
	assertAudit("SUCCESS", "succeeded", true)
	for _, sql := range []string{`UPDATE adtr.domain_directory_observations SET dictionary_version=1`, `DELETE FROM adtr.domain_directory_observations`, `TRUNCATE adtr.domain_directory_observations`, `TRUNCATE adtr.domain_directory_task_uses`} {
		if _, err := f.conn.Exec(f.ctx, sql); err == nil {
			t.Fatal("versioned history lost immutable/no-TRUNCATE protection")
		}
	}
}
