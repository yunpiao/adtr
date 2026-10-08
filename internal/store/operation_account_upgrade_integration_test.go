//go:build integration

package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExistingVersionNineUpgradePreservesDomainCredentialAndAuthority(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated PostgreSQL required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("adtr_operation_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
	}()
	cfg = cfg.Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	// Only version-9 producer fragments are applied. No F03 table/permission/view.
	prior := `CREATE SCHEMA adtr;CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,9);` +
		auth.Schema + auth.SchemaV3 + auth.ResourceSchema + auth.TaskPermissionSchema + auth.TaskAuthorizationSchema + tasks.Schema +
		auth.AuditPermissionSchema + audit.Schema + auth.SystemPermissionMarks + systemhealth.Schema +
		auth.SchedulePermissionSchema + auth.ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema +
		auth.DomainPermissionSchema + domains.Schema + audit.DomainViewSchema
	if _, err = conn.Exec(ctx, prior); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('old','old-domain','old.invalid',true);INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('old','old-role','Existing role');INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('old','old-role','domains',true,false);INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id) VALUES('old','old-user','synthetic-hash','viewer','old-role');INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port) VALUES('old','old-domain','old.invalid','dc.old.invalid','starttls','389')`); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic-v1", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("v", 32)))}
	runtime, err := domainconfig.Load(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"version":1,"username":"reader@old.invalid","password":"Synthetic old stored credential"}`)
	sealed, err := runtime.Vault().Seal("old", "old-domain", 1, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES('old','old-domain',1,$1,$2)`, sealed.KeyID, sealed.Ciphertext); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = conn.QueryRow(ctx, "SELECT authorization_version FROM adtr.users WHERE username='old-user'").Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	var absent, oldProjection bool
	if err = conn.QueryRow(ctx, "SELECT to_regclass('adtr.operation_accounts') IS NULL,adtr.audit_event_deletable('operation_account','operation_account_create')").Scan(&absent, &oldProjection); err != nil || !absent || !oldProjection {
		t.Fatal("fixture already contains F03", err)
	}
	if Ready(ctx, cfg) == nil {
		t.Fatal("v9 accepted as current")
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := Migrate(ctx, cfg); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if err = Migrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var accounts, credentials, grants int
	if err = conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM adtr.operation_accounts),(SELECT count(*) FROM adtr.operation_account_credentials),(SELECT count(*) FROM adtr.access_permissions WHERE mark='operation_accounts')`).Scan(&accounts, &credentials, &grants); err != nil || accounts != 0 || credentials != 0 || grants != 0 {
		t.Fatal("migration invented accounts/secrets/grants", err)
	}
	var after int64
	if err = conn.QueryRow(ctx, "SELECT authorization_version FROM adtr.users WHERE username='old-user'").Scan(&after); err != nil || after != epoch {
		t.Fatal("migration changed actor authority", err)
	}
	var kept domainconfig.SealedCredential
	if err = conn.QueryRow(ctx, "SELECT key_id,ciphertext FROM adtr.domain_credentials WHERE tenant_id='old' AND domain_id='old-domain' AND credential_revision=1").Scan(&kept.KeyID, &kept.Ciphertext); err != nil || kept.KeyID != sealed.KeyID || !bytes.Equal(kept.Ciphertext, sealed.Ciphertext) {
		t.Fatal("F01 ciphertext changed", err)
	}
	opened, err := runtime.Vault().Open("old", "old-domain", 1, kept)
	defer clear(opened)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatal("F01 credential no longer decrypts", err)
	}
	var protected bool
	if err = conn.QueryRow(ctx, "SELECT NOT adtr.audit_event_deletable('operation_account','operation_account_create')").Scan(&protected); err != nil || !protected {
		t.Fatal("operation controls not protected", err)
	}
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM adtr.audit_source WHERE source='operation_account'").Scan(&count); err != nil || count != 0 {
		t.Fatal("operation audit producer unavailable or invented", err)
	}
}
