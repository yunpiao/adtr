//go:build integration

package store

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
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

func TestExistingVersionEightUpgradePreservesScopeWithoutInventingConnections(t *testing.T) {
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
	name := fmt.Sprintf("adtr_domain_upgrade_%d", time.Now().UnixNano())
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
	prior := `CREATE SCHEMA adtr;CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL);INSERT INTO adtr.schema_version VALUES(true,8);` +
		auth.Schema + auth.SchemaV3 + auth.ResourceSchema + auth.TaskPermissionSchema + auth.TaskAuthorizationSchema + tasks.Schema +
		auth.AuditPermissionSchema + strings.Replace(audit.Schema, audit.ViewSchema, auditVersionEightView, 1) + auth.SystemPermissionMarks + systemhealth.Schema +
		auth.SchedulePermissionSchema + auth.ArchivePermissionSchema + schedules.Schema + taskarchive.Schema + systemhealth.SchedulerActivitySchema
	if _, err = conn.Exec(ctx, prior); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('old','old-domain','old.invalid',true);INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('old','old-role','Existing role');INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('old','old-role','users',true,false);INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id) VALUES('old','old-user','synthetic-hash','viewer','old-role')`); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = conn.QueryRow(ctx, "SELECT authorization_version FROM adtr.users WHERE username='old-user'").Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	var absent bool
	if err = conn.QueryRow(ctx, "SELECT to_regclass('adtr.domain_connections') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatal("fixture already contains domain module", err)
	}
	if Ready(ctx, cfg) == nil {
		t.Fatal("v8 accepted as current")
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
	if err = Ready(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var scopes, connections, credentials, grants int
	if err = conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM adtr.resource_domains WHERE tenant_id='old' AND id='old-domain' AND name='old.invalid' AND active),(SELECT count(*) FROM adtr.domain_connections),(SELECT count(*) FROM adtr.domain_credentials),(SELECT count(*) FROM adtr.access_permissions WHERE mark='domains')`).Scan(&scopes, &connections, &credentials, &grants); err != nil || scopes != 1 || connections != 0 || credentials != 0 || grants != 0 {
		t.Fatal("migration changed scope or invented connection/credential/grant", err)
	}
	var after int64
	if err = conn.QueryRow(ctx, "SELECT authorization_version FROM adtr.users WHERE username='old-user'").Scan(&after); err != nil || after != epoch {
		t.Fatal("migration changed actor authority", err)
	}
	var protected bool
	if err = conn.QueryRow(ctx, "SELECT NOT adtr.audit_event_deletable('domain','domain_create')").Scan(&protected); err != nil || !protected {
		t.Fatal("domain controls not protected", err)
	}
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM adtr.audit_source WHERE source='domain'").Scan(&count); err != nil || count != 0 {
		t.Fatal("domain audit projection unavailable or invented", err)
	}
}
