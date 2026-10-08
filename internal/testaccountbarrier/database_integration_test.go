//go:build integration

package main

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
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	dbstore "github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

// This layer uses actual migrations, admission, fenced task execution and the
// production account-use acknowledgement. The synthetic opening executor is not
// production account-executor, wire or browser execution evidence.
func migratedDatabase(t *testing.T) (*pgx.ConnConfig, *pgx.Conn) {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required; real PostgreSQL is not skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("test database configuration invalid")
	}
	owner, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test PostgreSQL unavailable")
	}
	var random [10]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal("test identity unavailable")
	}
	name := "account_barrier_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = owner.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		owner.Close(ctx)
		t.Fatal("isolated test database creation failed")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := owner.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error("isolated test database cleanup failed")
		}
		owner.Close(ctx)
	})
	cfg = cfg.Copy()
	cfg.Database = name
	if dbstore.Migrate(ctx, cfg) != nil {
		t.Fatal("actual migrations failed")
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("isolated test database unavailable")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn.Close(ctx)
	})
	return cfg, conn
}

func TestDatabaseBarrierActualMigratedSchema(t *testing.T) {
	cfg, _ := migratedDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// ConnString retains the original parsed DSN, not cfg.Database's isolated
	// test database. Pass the modified configuration to both helper connections.
	db, err := connectDatabaseConfig(ctx, cfg)
	if err != nil {
		t.Fatal("helper did not prepare against actual migrated schema")
	}
	defer db.close()
	for _, conn := range []*pgx.Conn{db.lock, db.observer} {
		var name string
		if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil || name != cfg.Database {
			t.Fatal("helper connection did not select the isolated migrated database")
		}
	}
	if db.lock.PgConn().PID() == db.observer.PgConn().PID() {
		t.Fatal("observer shares lock connection")
	}
	if locked, err := db.tryLock(ctx, validArm()); err != nil || locked {
		t.Fatal("empty migrated database produced a match")
	}
	if err = db.release(); err != nil {
		t.Fatal("empty polling transaction did not roll back")
	}
	var timeout string
	if db.lock.QueryRow(ctx, "SHOW idle_in_transaction_session_timeout").Scan(&timeout) != nil || timeout != "1min" {
		t.Fatal("server lock watchdog not configured")
	}
}

func barrierRuntime(t *testing.T) *domainconfig.Runtime {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic key generation failed")
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal("synthetic certificate generation failed")
	}
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	policyPath := filepath.Join(dir, "policy.json")
	if os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600) != nil {
		t.Fatal("synthetic CA write failed")
	}
	if os.WriteFile(policyPath, []byte(`{"version":1,"targets":[{"tenantId":"fixture","domain":"example.test","serverNames":["dc1.example.test"],"cidrs":["10.20.0.0/24"]}]}`), 0600) != nil {
		t.Fatal("synthetic policy write failed")
	}
	values := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))), "ADTR_DOMAIN_PROBE_ENABLED": "true", "ADTR_LDAP_CA_FILE": caPath, "ADTR_LDAP_EGRESS_POLICY_FILE": policyPath}
	runtime, err := domainconfig.Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal("synthetic runtime invalid")
	}
	return runtime
}

func TestDatabaseBarrierActualEngineAcknowledgement(t *testing.T) {
	cfg, conn := migratedDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started, finishProbe := make(chan struct{}), make(chan struct{})
	runtime := barrierRuntime(t)
	s := domains.New(runtime)
	kind := s.AccountKind()
	// Only the test's synthetic executor opens a use. The helper executable has
	// no lifecycle writes, and the production OnQuiesced callback is unchanged.
	kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		version, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			tag, err := tx.Exec(ctx, `UPDATE adtr.domain_account_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$2,opener_fencing_token=$3,opener_attempt=$4 WHERE task_id=$1 AND state='reserved'`, ex.Task.ID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt)
			if err == nil && tag.RowsAffected() != 1 {
				err = errors.New("synthetic opening did not match")
			}
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_open_failed"}
		}
		close(started)
		select {
		case <-finishProbe:
		case <-ctx.Done():
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_cancelled"}
		}
		_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := tx.Exec(ctx, `SELECT domain_id FROM adtr.domain_connections WHERE tenant_id=$1 AND domain_id=$2 FOR UPDATE`, ex.Task.TenantID, ex.Task.DomainID)
			return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_checkpoint_failed"}
		}
		return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
	}
	engine, err := tasks.New(cfg, tasks.ProductionRegistry(s.Kind(), kind), auth.NewTaskAuthorizer(), dbstore.SchemaVersion)
	if err != nil {
		t.Fatal("test engine creation failed")
	}
	p := tasks.Principal{TenantID: "fixture"}
	if conn.QueryRow(ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change,password_updated_at) VALUES('fixture','synthetic','synthetic-not-login','platform_admin',false,clock_timestamp()) RETURNING id`).Scan(&p.ActorID) != nil {
		t.Fatal("synthetic actor creation failed")
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, query, args...); err != nil {
			t.Fatal("synthetic setup statement failed")
		}
	}
	exec(`INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('fixture',10,$1,'fixture','fixture')`, time.Now().Add(time.Hour).Unix())
	transact := func(work func(context.Context, pgx.Tx) error) {
		t.Helper()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal("synthetic setup transaction failed")
		}
		defer tx.Rollback(ctx)
		if engine.CheckSchemaTx(ctx, tx) != nil {
			t.Fatal("test schema gate failed")
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))`, p.TenantID); err != nil {
			t.Fatal("test tenant lock failed")
		}
		if _, err = tx.Exec(ctx, `SELECT id FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, p.ActorID); err != nil {
			t.Fatal("test actor lock failed")
		}
		if work(ctx, tx) != nil {
			t.Fatal("genuine domain/account setup failed")
		}
		if tx.Commit(ctx) != nil {
			t.Fatal("genuine setup commit failed")
		}
	}
	var domain, account, grant string
	username, password := "reader@example.test", "Synthetic executor secret"
	transact(func(ctx context.Context, tx pgx.Tx) error {
		out, err := s.CreateTx(ctx, tx, p, domains.Input{Domain: "example.test", DCHostName: "dc1.example.test", LDAPAddr: "10.20.0.8", Port: "389", Username: &username, Password: &password, IdempotencyKey: "create"}, time.Now())
		domain = out.DomainID
		return err
	})
	exec(`INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('fixture','group','group')`)
	exec(`INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('fixture','group',$1)`, domain)
	exec(`INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('fixture','group','platform_admin')`)
	transact(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetSelfContext(ctx, tx, p, credentialuse.ConfirmMFA); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE adtr.users SET mfa_secret='synthetic-mfa' WHERE tenant_id=$1 AND id=$2`, p.TenantID, p.ActorID)
		return err
	})
	transact(func(ctx context.Context, tx pgx.Tx) error {
		out, err := operationaccounts.New(runtime).MutateTx(ctx, tx, p, "/create", operationaccounts.Input{DomainID: domain, ExpectedDomainRevision: "1", Username: &username, Password: &password, IdempotencyKey: "account-create"}, []string{domain})
		account = out.AccountID
		return err
	})
	transact(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, p, credentialuse.GrantUse); err != nil {
			return err
		}
		out, err := credentialuse.New().MutateTx(ctx, tx, p, "/grant", credentialuse.Input{AccountID: account, RoleID: "platform_admin", Purpose: credentialuse.Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "grant"}, []string{domain})
		grant = out.GrantRevision
		return err
	})
	transact(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, p, "reference"); err != nil {
			return err
		}
		_, err := s.MutateSourceTx(ctx, tx, p, "platform_admin", "reference", domains.SourceInput{DomainID: domain, ExpectedRevision: "1", ExpectedConnectionCredentialGeneration: "1", AccountID: account, ExpectedAccountRevision: "1", ExpectedAccountCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "bind"}, []string{domain})
		return err
	})
	arm := armRecord{1, strings.Repeat("a", 32), p.TenantID, strconv.FormatInt(p.ActorID, 10), domain, account, "barrier-task"}
	db, err := connectDatabaseConfig(ctx, cfg)
	if err != nil {
		t.Fatal("barrier preconnection failed")
	}
	defer db.close()
	var submitted tasks.Task
	transact(func(ctx context.Context, tx pgx.Tx) error {
		out, err := s.SubmitTx(ctx, tx, engine, p, domains.Input{DomainID: domain, ExpectedRevision: "2", IdempotencyKey: arm.IdempotencyKey})
		submitted = out.Task
		return err
	})
	// A real reservation must not satisfy the opened-only lock predicate.
	if locked, err := db.tryLock(ctx, arm); err != nil || locked {
		t.Fatal("reservation accepted as opened")
	}
	done := make(chan error, 1)
	go func() { _, err := engine.RunOne(ctx, "synthetic-barrier-worker"); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("genuine executor did not open the use")
	}
	for _, change := range []func(*armRecord){func(a *armRecord) { a.TenantID = "other" }, func(a *armRecord) { a.ActorID = "9223372036854775807" }, func(a *armRecord) { a.DomainID = "other" }, func(a *armRecord) { a.AccountID = "other" }, func(a *armRecord) { a.IdempotencyKey = "other" }} {
		wrong := arm
		change(&wrong)
		if locked, err := db.tryLock(ctx, wrong); err != nil || locked {
			t.Fatal("wrong admission identity acquired a lock")
		}
	}
	if locked, err := db.tryLock(ctx, arm); err != nil || !locked {
		t.Fatal("genuine opened row not locked")
	}
	observed, err := db.observe(ctx, arm)
	if err != nil || observed.TaskID != submitted.ID || observed.UseState != "opened" || !observed.OpenerUnchanged || observed.ExactBindingDependencyCount != 1 || observed.ExactTaskDependencyCount != 1 || observed.TotalDependencyCount != 2 || observed.CustomCredentialCount != 0 {
		t.Fatal("opened snapshot did not match genuine admission")
	}
	// The harness transaction holds no upstream tuple or tenant advisory lock.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal("lock inspection failed")
	}
	var tenantAvailable bool
	if tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('adtr-access:'||$1,0))`, p.TenantID).Scan(&tenantAvailable) != nil || !tenantAvailable {
		t.Fatal("helper blocked tenant lock")
	}
	for _, query := range []string{`SELECT task_id FROM adtr.tasks WHERE task_id=$1 FOR UPDATE NOWAIT`, `SELECT c.domain_id FROM adtr.domain_connections c JOIN adtr.tasks t ON t.tenant_id=c.tenant_id AND t.domain_id=c.domain_id WHERE t.task_id=$1 FOR UPDATE OF c NOWAIT`, `SELECT a.account_id FROM adtr.operation_accounts a JOIN adtr.domain_account_task_uses u ON u.tenant_id=a.tenant_id AND u.domain_id=a.domain_id AND u.account_id=a.account_id WHERE u.task_id=$1 FOR UPDATE OF a NOWAIT`} {
		if _, err = tx.Exec(ctx, query, submitted.ID); err != nil {
			tx.Rollback(ctx)
			t.Fatal("helper blocked an upstream row")
		}
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("lock inspection rollback failed")
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal("use lock inspection failed")
	}
	_, err = tx.Exec(ctx, `SELECT task_id FROM adtr.domain_account_task_uses WHERE task_id=$1 FOR UPDATE NOWAIT`, submitted.ID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
		tx.Rollback(ctx)
		t.Fatal("helper does not hold the genuine use row")
	}
	tx.Rollback(ctx)
	close(finishProbe)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		observed, err = db.observe(ctx, arm)
		if err != nil {
			t.Fatal("independent observer failed during executor completion")
		}
		if observed.TaskState == "succeeded" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if observed.TaskState != "succeeded" || observed.UseState != "opened" || observed.ExactTaskDependencyCount != 1 {
		t.Fatal("synthetic executor did not finish while actual acknowledgement remained held")
	}
	if db.release() != nil {
		t.Fatal("harness rollback failed")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("genuine engine acknowledgement failed after rollback")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("engine acknowledgement did not complete")
	}
	observed, err = db.observe(ctx, arm)
	if err != nil || !observed.isQuiesced() || observed.ExactBindingDependencyCount != 1 || observed.TotalDependencyCount != 1 {
		t.Fatal("genuine executor-return acknowledgement not observed")
	}
}
