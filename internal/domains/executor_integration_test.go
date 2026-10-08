//go:build integration

package domains_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	dbstore "github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

type execFixture struct {
	t             *testing.T
	schemaVersion int
	config        *pgx.ConnConfig
	conn          *pgx.Conn
	store         *domains.Store
	runtime       *domainconfig.Runtime
	engine        *tasks.Engine
	p             tasks.Principal
	id            string
}

func runtimeForExecutor(t *testing.T) *domainconfig.Runtime {
	t.Helper()
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &k.PublicKey, k)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	policyPath := filepath.Join(dir, "policy.json")
	if e = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(policyPath, []byte(`{"version":1,"targets":[{"tenantId":"fixture","domain":"example.test","serverNames":["dc1.example.test"],"cidrs":["10.20.0.0/24"]}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	values := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))), "ADTR_DOMAIN_PROBE_ENABLED": "true", "ADTR_LDAP_CA_FILE": caPath, "ADTR_LDAP_EGRESS_POLICY_FILE": policyPath}
	r, e := domainconfig.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func fixtureForExecutor(t *testing.T, probe func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error)) *execFixture {
	t.Helper()
	return fixtureForExecutorAtVersion(t, probe, dbstore.SchemaVersion)
}

// Historical setup is reserved for explicit fragment absence/collision tests.
func fixtureForExecutorAtVersion(t *testing.T, probe func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error), version int) *execFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; real PostgreSQL is not skipped")
	}
	ctx := context.Background()
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal("invalid test DB configuration")
	}
	owner, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	var token [10]byte
	if _, e = rand.Read(token[:]); e != nil {
		t.Fatal(e)
	}
	name := "domain_executor_" + hex.EncodeToString(token[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, e = owner.Exec(ctx, "CREATE DATABASE "+quoted); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if _, e := owner.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
		owner.Close(ctx)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	migrate := dbstore.Migrate
	if version == 14 {
		migrate = dbstore.MigrateDirectoryBaselineForTest
	} else if version != dbstore.SchemaVersion {
		t.Fatal("unsupported fixture schema version")
	}
	if e = migrate(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	conn, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	runtime := runtimeForExecutor(t)
	s := domains.NewProbeForTest(runtime, probe)
	engine, e := tasks.New(cfg, tasks.ProductionRegistry(s.Kind()), auth.NewTaskAuthorizer(), version)
	if e != nil {
		t.Fatal(e)
	}
	f := &execFixture{t: t, schemaVersion: version, config: cfg, conn: conn, store: s, runtime: runtime, engine: engine, p: tasks.Principal{TenantID: "fixture"}}
	if e = conn.QueryRow(ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change,password_updated_at) VALUES('fixture','synthetic','synthetic-not-login','platform_admin',false,clock_timestamp()) RETURNING id`).Scan(&f.p.ActorID); e != nil {
		t.Fatal(e)
	}
	f.exec(`INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('fixture',10,$1,'fixture','fixture')`, time.Now().Add(time.Hour).Unix())
	username, password := "reader@example.test", "Synthetic executor secret"
	input := domains.Input{Domain: "example.test", DCHostName: "dc1.example.test", LDAPAddr: "10.20.0.8", Port: "389", Username: &username, Password: &password, IdempotencyKey: "create"}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		out, e := s.CreateTx(ctx, tx, f.p, input, time.Now())
		f.id = out.DomainID
		return e
	})
	f.exec(`INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('fixture','group','group')`)
	f.exec(`INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('fixture','group',$1)`, f.id)
	f.exec(`INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('fixture','group','platform_admin')`)
	return f
}
func (f *execFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, e := f.conn.Exec(context.Background(), q, args...); e != nil {
		f.t.Fatal(e)
	}
}
func (f *execFixture) tx(work func(context.Context, pgx.Tx) error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, e := pgx.ConnectConfig(ctx, f.config)
	if e != nil {
		f.t.Fatal(e)
	}
	defer conn.Close(ctx)
	tx, e := conn.Begin(ctx)
	if e != nil {
		f.t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if e = f.engine.CheckSchemaTx(ctx, tx); e != nil {
		f.t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))`, f.p.TenantID); e != nil {
		f.t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, f.p.TenantID, f.p.ActorID); e != nil {
		f.t.Fatal(e)
	}
	if e = work(ctx, tx); e != nil {
		f.t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		f.t.Fatal(e)
	}
}
func (f *execFixture) submit(key string) tasks.Task {
	f.t.Helper()
	var out tasks.Submission
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		var e error
		out, e = f.store.SubmitTx(ctx, tx, f.engine, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "1", IdempotencyKey: key})
		return e
	})
	return out.Task
}
func (f *execFixture) connection() domains.Connection {
	f.t.Helper()
	var out domains.Connection
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		var e error
		out, e = f.store.DetailTx(ctx, tx, f.p.TenantID, f.id)
		return e
	})
	return out
}
func fixtureObservation() ldapconnection.Result {
	return ldapconnection.Result{DCHostName: "dc1.example.test", DefaultNamingContext: "DC=example,DC=test", SupportedCapabilities: []string{"1.2.840.113556.1.4.800"}}
}

func TestDiagnosticDatabaseTransactionsEndBeforeProbeAndRejectStalePublication(t *testing.T) {
	for _, change := range []string{"configuration", "generation", "cancel"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var binds atomic.Int32
			f := fixtureForExecutor(t, func(ctx context.Context, cfg ldapconnection.Config, c ldapconnection.Credential) (ldapconnection.Result, error) {
				if e := cfg.Authorize(ctx, ldapconnection.StageDial); e != nil {
					return ldapconnection.Result{}, e
				}
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return ldapconnection.Result{}, ctx.Err()
				}
				if e := cfg.Authorize(ctx, ldapconnection.StageBind); e != nil {
					return ldapconnection.Result{}, e
				}
				binds.Add(1)
				return fixtureObservation(), nil
			})
			task := f.submit("first")
			done := make(chan error, 1)
			go func() { _, e := f.engine.RunOne(context.Background(), "fixture-worker"); done <- e }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("probe never started")
			}
			// These real DB writes have a 3s bound while probe is blocked. A transaction
			// held over the network would fail this gate instead of silently passing.
			switch change {
			case "configuration":
				f.tx(func(ctx context.Context, tx pgx.Tx) error {
					_, e := f.store.UpdateTx(ctx, tx, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "1", DCHostName: "dc1.example.test", LDAPAddr: "10.20.0.9", Port: "389"})
					return e
				})
			case "generation":
				f.submit("second")
			case "cancel":
				f.tx(func(ctx context.Context, tx pgx.Tx) error { _, e := f.engine.CancelTx(ctx, tx, f.p, task.ID); return e })
			}
			close(release)
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("executor did not stop")
			}
			if binds.Load() != 0 {
				t.Fatal("bind after stale phase check")
			}
			var count int
			if e := f.conn.QueryRow(context.Background(), "SELECT count(*) FROM adtr.domain_diagnostics WHERE task_id=$1", task.ID).Scan(&count); e != nil || count != 0 {
				t.Fatal("stale observation committed", e, count)
			}
			var state, code string
			if e := f.conn.QueryRow(context.Background(), "SELECT state,error_code FROM adtr.tasks WHERE task_id=$1", task.ID).Scan(&state, &code); e != nil {
				t.Fatal(e)
			}
			want := "authorization_revoked"
			if change == "generation" {
				want = "connection_revision_changed"
			}
			if change == "cancel" {
				want = "cancelled"
			}
			if code != want || state == "succeeded" {
				t.Fatal(state, code)
			}
		})
	}
}

func TestDiagnosticEvidencePublishesOnlyAfterFencedTerminalSuccess(t *testing.T) {
	var password []byte
	f := fixtureForExecutor(t, func(ctx context.Context, cfg ldapconnection.Config, c ldapconnection.Credential) (ldapconnection.Result, error) {
		password = c.Password
		for _, stage := range []ldapconnection.Stage{ldapconnection.StageDial, ldapconnection.StageBind, ldapconnection.StageSearch} {
			if e := cfg.Authorize(ctx, stage); e != nil {
				return ldapconnection.Result{}, e
			}
		}
		return fixtureObservation(), nil
	})
	task := f.submit("probe")
	lease, e := f.engine.Claim(context.Background(), "fixture-worker")
	if e != nil {
		t.Fatal(e)
	}
	lease, e = f.engine.Start(context.Background(), lease)
	if e != nil {
		t.Fatal(e)
	}
	out := f.store.Kind().Execute(context.Background(), tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, v, work)
	}})
	if out.State != tasks.Succeeded {
		t.Fatal(out)
	}
	for _, b := range password {
		if b != 0 {
			t.Fatal("owned password was not cleared")
		}
	}
	before := f.connection()
	if before.ConnectionState != "testing" || before.LastDiagnostic != nil || before.LatestTaskUUID != task.ID {
		t.Fatal("staged success was published", before)
	}
	if _, e = f.engine.Finish(context.Background(), lease, out); e != nil {
		t.Fatal(e)
	}
	after := f.connection()
	if after.ConnectionState != "verified" || after.LastDiagnostic == nil || after.LastDiagnostic.Code != "ok" || after.LastDiagnostic.Stage != "complete" {
		t.Fatal("terminal evidence not published", after)
	}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		_, e := f.store.UpdateTx(ctx, tx, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "1", DCHostName: "dc1.example.test", LDAPAddr: "10.20.0.9", Port: "389"})
		return e
	})
	after = f.connection()
	if after.LastDiagnostic != nil || after.ConnectionState != "unverified" || after.LatestTaskUUID != "" {
		t.Fatal("edit retained old verification")
	}
}

func TestDiagnosticPolicyRevisionChangePreventsAnyProbe(t *testing.T) {
	var called atomic.Int32
	f := fixtureForExecutor(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		called.Add(1)
		return fixtureObservation(), nil
	})
	task := f.submit("probe")
	// A new startup CA changes the policy digest without changing saved endpoints.
	changed := domains.NewProbeForTest(runtimeForExecutor(t), func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		called.Add(1)
		return fixtureObservation(), nil
	})
	engine, e := tasks.New(f.config, tasks.ProductionRegistry(changed.Kind()), auth.NewTaskAuthorizer(), dbstore.SchemaVersion)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = engine.RunOne(context.Background(), "changed-policy"); e != nil {
		t.Fatal(e)
	}
	if called.Load() != 0 {
		t.Fatal("stale policy probed")
	}
	var code string
	if e = f.conn.QueryRow(context.Background(), "SELECT error_code FROM adtr.tasks WHERE task_id=$1", task.ID).Scan(&code); e != nil || code != "policy_revision_changed" {
		t.Fatal(code, e)
	}
}

func TestCompletedProbePreservesLateCancellationRace(t *testing.T) {
	f := fixtureForExecutor(t, func(ctx context.Context, cfg ldapconnection.Config, c ldapconnection.Credential) (ldapconnection.Result, error) {
		for _, stage := range []ldapconnection.Stage{ldapconnection.StageDial, ldapconnection.StageBind, ldapconnection.StageSearch} {
			if e := cfg.Authorize(ctx, stage); e != nil {
				return ldapconnection.Result{}, e
			}
		}
		return fixtureObservation(), nil
	})
	task := f.submit("probe")
	lease, e := f.engine.Claim(context.Background(), "fixture-worker")
	if e != nil {
		t.Fatal(e)
	}
	lease, e = f.engine.Start(context.Background(), lease)
	if e != nil {
		t.Fatal(e)
	}
	out := f.store.Kind().Execute(context.Background(), tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, v, work)
	}})
	if out.State != tasks.Succeeded {
		t.Fatal(out)
	}
	// The network probe and final fenced evidence commit already completed.
	// A subsequent cancellation cannot claim its bind was undone.
	f.tx(func(ctx context.Context, tx pgx.Tx) error { _, e := f.engine.CancelTx(ctx, tx, f.p, task.ID); return e })
	terminal, e := f.engine.Finish(context.Background(), lease, out)
	if e != nil || terminal.State != tasks.Succeeded {
		t.Fatal("late cancel concealed completed probe", terminal.State, e)
	}
	var events int
	if e = f.conn.QueryRow(context.Background(), "SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND action='completed-with-cancel-race'", task.ID).Scan(&events); e != nil || events != 1 {
		t.Fatal("late completion lost explicit race event", e, events)
	}
	if f.connection().ConnectionState != "verified" {
		t.Fatal("terminal observation unavailable after late cancel")
	}
}

func TestDiagnosticPayloadAndAuditNeverContainCredential(t *testing.T) {
	f := fixtureForExecutor(t, func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error) {
		return ldapconnection.Result{}, &ldapconnection.Error{Code: ldapconnection.CodeCredentialsRejected, Stage: ldapconnection.StageBind}
	})
	task := f.submit("failed")
	if _, e := f.engine.RunOne(context.Background(), "fixture-worker"); e != nil {
		t.Fatal(e)
	}
	var all string
	e := f.conn.QueryRow(context.Background(), `SELECT jsonb_build_object('task',to_jsonb(t),'audits',(SELECT jsonb_agg(to_jsonb(a)) FROM adtr.domain_audit a WHERE a.domain_id=t.domain_id),'diagnostics',(SELECT jsonb_agg(to_jsonb(d)) FROM adtr.domain_diagnostics d WHERE d.task_id=t.task_id))::text FROM adtr.tasks t WHERE task_id=$1`, task.ID).Scan(&all)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"reader@example.test", "Synthetic executor secret", "ciphertext", "synthetic-not-login"} {
		if strings.Contains(all, secret) {
			t.Fatal("secret entered durable task/history")
		}
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(all), &parsed) != nil {
		t.Fatal("invalid history")
	}
}
