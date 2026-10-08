//go:build integration

package domains_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
)

func sourceProblem(t *testing.T, err error, code string) {
	t.Helper()
	var problem *domains.Error
	if !errors.As(err, &problem) || problem.Code != code {
		t.Fatalf("wanted %s; got %v", code, err)
	}
}

func TestSourceReceiptReplaySurvivesRevocationAndOmitsPointers(t *testing.T) {
	f := fixtureForAccountExecutor(t, nil)
	originalGrant := f.grant
	f.revoke()
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		in := domains.SourceInput{DomainID: f.id, ExpectedRevision: "1", ExpectedConnectionCredentialGeneration: "1", AccountID: f.account, ExpectedAccountRevision: "1", ExpectedAccountCredentialRevision: "1", ExpectedGrantRevision: originalGrant, IdempotencyKey: "bind-account"}
		r, err := f.store.SourceReplayTx(ctx, tx, f.p, "reference", in, []string{f.id})
		if err != nil {
			return err
		}
		if r == nil || !r.Replayed || r.Revision != "2" || r.ConnectionCredentialGeneration != "2" {
			t.Fatal("committed source replay changed", r)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), f.account) || strings.Contains(string(raw), "grantRevision") || strings.Contains(string(raw), "accountId") {
			t.Fatal("safe receipt disclosed account provenance", string(raw))
		}
		visible, err := f.store.SourceDetailTx(ctx, tx, f.p, "platform_admin", f.id, false, false)
		if err != nil {
			return err
		}
		if visible.Reference != nil || visible.TestEligible || !visible.CredentialConfigured {
			t.Fatal("account-read loss did not redact pointer", visible)
		}
		in.ExpectedGrantRevision = f.grant
		_, err = f.store.SourceReplayTx(ctx, tx, f.p, "reference", in, []string{f.id})
		sourceProblem(t, err, "idempotency_conflict")
		return nil
	})
	// Fresh ungranted selection must hide CAS even when every supplied revision
	// is stale. Replaying a saved result above must not restore the allow.
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, f.p, "reference"); err != nil {
			return err
		}
		_, err := f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "reference", domains.SourceInput{DomainID: f.id, ExpectedRevision: "1", ExpectedConnectionCredentialGeneration: "1", AccountID: f.account, ExpectedAccountRevision: "999", ExpectedAccountCredentialRevision: "999", ExpectedGrantRevision: "999", IdempotencyKey: "new-revoked"}, []string{f.id})
		return err
	})
	sourceProblem(t, err, "not_found")
}

func TestSourceDetachPreservesUnknownDependenciesAndLegacyUpdateBoundary(t *testing.T) {
	f := fixtureForAccountExecutor(t, nil)
	// A valid pair reaches the legacy source boundary instead of input validation.
	username, password := "synthetic-legacy@example.test", "synthetic-pair"
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.store.UpdateTx(ctx, tx, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "2", DCHostName: "dc1.example.test", LDAPAddr: "10.20.0.8", Port: "389", Username: &username, Password: &password})
		return err
	})
	sourceProblem(t, err, "credential_source_changed")
	// Endpoint-only edits keep the binding generation and account dependency.
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.store.UpdateTx(ctx, tx, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "2", DCHostName: "dc2.example.test", LDAPAddr: "10.20.0.8", Port: "389"})
		return err
	})
	c := f.connection()
	if c.CredentialSource != domains.SourceOperationAccount || c.CredentialRevision != "2" || c.ConnectionCredentialGeneration != "2" || c.Revision != "3" {
		t.Fatal("endpoint edit changed credential identity", c)
	}
	f.exec(`INSERT INTO adtr.operation_account_dependencies(tenant_id,domain_id,account_id,consumer_kind,object_id) VALUES($1,$2,$3,'unrecognized.future_consumer','opaque-object')`, f.p.TenantID, f.id, f.account)
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, f.p, "detach"); err != nil {
			return err
		}
		_, err := f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "detach", domains.SourceInput{DomainID: f.id, ExpectedRevision: "3", ExpectedConnectionCredentialGeneration: "2", IdempotencyKey: "detach-unknown"}, []string{f.id})
		return err
	})
	var unknown, binding int
	if err = f.conn.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE consumer_kind='unrecognized.future_consumer'),count(*) FILTER(WHERE consumer_kind='domain.connection_binding') FROM adtr.operation_account_dependencies WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3`, f.p.TenantID, f.id, f.account).Scan(&unknown, &binding); err != nil || unknown != 1 || binding != 0 {
		t.Fatal("detach removed unknown blocker", unknown, binding, err)
	}
	requireAccountBlocked(t, f.replace())
}

func TestSourceSQLGuardsRejectPairBindingAndReceiptTampering(t *testing.T) {
	f := fixtureForAccountExecutor(t, nil)
	for name, sql := range map[string]string{
		"binding-removal":              `DELETE FROM adtr.operation_account_dependencies WHERE tenant_id=$1 AND domain_id=$2 AND consumer_kind='domain.connection_binding'`,
		"binding-reassignment":         `UPDATE adtr.operation_account_dependencies SET connection_credential_generation=connection_credential_generation+1 WHERE tenant_id=$1 AND domain_id=$2 AND consumer_kind='domain.connection_binding'`,
		"reference-has-custom-pair":    `INSERT INTO adtr.domain_credentials(tenant_id,domain_id,credential_revision,key_id,ciphertext) VALUES($1,$2,2,'synthetic',decode(repeat('ab',32),'hex'))`,
		"source-without-proof-context": `UPDATE adtr.domain_connections SET credential_mode='unconfigured',operation_account_id=NULL,operation_account_credential_revision=NULL,connection_revision=connection_revision+1,credential_revision=credential_revision+1,diagnostic_generation=diagnostic_generation+1,latest_test_task_id=NULL WHERE tenant_id=$1 AND domain_id=$2`,
		"receipt-mutation":             `UPDATE adtr.domain_credential_source_mutations SET credential_source='custom',operation='custom' WHERE tenant_id=$1 AND domain_id=$2`,
	} {
		t.Run(name, func(t *testing.T) {
			err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, sql, f.p.TenantID, f.id)
				return err
			})
			if err == nil {
				t.Fatal("SQL source invariant accepted tampering")
			}
			var pg *pgconn.PgError
			if !errors.As(err, &pg) {
				t.Fatal("unexpected non-SQL rejection", err)
			}
		})
	}
	c := f.connection()
	if c.Revision != "2" || !c.CredentialConfigured || c.CredentialSource != domains.SourceOperationAccount {
		t.Fatal("failed transaction changed reference", c)
	}
}

func TestCustomSameGenerationCiphertextSwapFails(t *testing.T) {
	f := fixtureForExecutor(t, nil)
	tx, err := f.conn.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(context.Background(), `UPDATE adtr.domain_credentials SET ciphertext=decode(repeat('cd',32),'hex') WHERE tenant_id=$1 AND domain_id=$2`, f.p.TenantID, f.id)
	if err == nil {
		t.Fatal("same-generation pair substitution accepted")
	}
}

func TestSourceAuditFailureRollsBackSourceCountersDependencyAndReceipt(t *testing.T) {
	f := fixtureForAccountExecutor(t, nil)
	f.exec(`CREATE FUNCTION adtr.synthetic_source_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='domain_credential_detach' THEN RAISE EXCEPTION 'synthetic audit failure';END IF;RETURN NEW;END;$$;
 CREATE TRIGGER synthetic_source_audit_failure BEFORE INSERT ON adtr.domain_audit FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_source_audit_failure()`)
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, f.p, "detach"); err != nil {
			return err
		}
		_, err := f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "detach", domains.SourceInput{DomainID: f.id, ExpectedRevision: "2", ExpectedConnectionCredentialGeneration: "2", IdempotencyKey: "audit-must-rollback"}, []string{f.id})
		return err
	})
	if err == nil {
		t.Fatal("audit failure did not abort mutation")
	}
	c := f.connection()
	if c.Revision != "2" || c.CredentialRevision != "2" || c.CredentialSource != domains.SourceOperationAccount || !c.CredentialConfigured {
		t.Fatal("audit failure committed source change", c)
	}
	var receipts int
	if err = f.conn.QueryRow(context.Background(), `SELECT count(*) FROM adtr.domain_credential_source_mutations WHERE idempotency_key='audit-must-rollback'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("failed source kept receipt", err)
	}
}

func TestCreateUnconfiguredNeedsNoKeyAndSharesCreationKeyNamespace(t *testing.T) {
	f := fixtureForExecutor(t, nil)
	noKey := domains.New(nil)
	input := domains.Input{Domain: "bootstrap.test", DCHostName: "dc.bootstrap.test", Port: "389", IdempotencyKey: "no-key-bootstrap"}
	var created domains.Mutation
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = noKey.CreateUnconfiguredTx(ctx, tx, f.p, input, time.Now())
		return err
	})
	if !created.RequiresResourceAssignment || created.Revision != "1" {
		t.Fatal("bootstrap response changed", created)
	}
	var mode string
	var rev, generation, diagnostic, pairs, pointers int
	err := f.conn.QueryRow(context.Background(), `SELECT credential_mode,connection_revision,credential_revision,diagnostic_generation,(SELECT count(*) FROM adtr.domain_credentials p WHERE p.tenant_id=c.tenant_id AND p.domain_id=c.domain_id),CASE WHEN operation_account_id IS NULL AND operation_account_credential_revision IS NULL THEN 0 ELSE 1 END FROM adtr.domain_connections c WHERE tenant_id=$1 AND domain_id=$2`, f.p.TenantID, created.DomainID).Scan(&mode, &rev, &generation, &diagnostic, &pairs, &pointers)
	if err != nil || mode != "unconfigured" || rev != 1 || generation != 1 || diagnostic != 1 || pairs != 0 || pointers != 0 {
		t.Fatal("bootstrap persisted credential material", err)
	}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		again, err := noKey.CreateUnconfiguredTx(ctx, tx, f.p, input, time.Now())
		if err == nil && (!again.Replayed || again.DomainID != created.DomainID) {
			t.Fatal("bootstrap replay duplicated domain")
		}
		return err
	})
	a := &accountExecFixture{execFixture: f}
	err = a.attempt(func(ctx context.Context, tx pgx.Tx) error {
		cross := input
		cross.Domain = "example.test"
		cross.DCHostName = "dc1.example.test"
		cross.IdempotencyKey = "create"
		_, err := noKey.CreateUnconfiguredTx(ctx, tx, f.p, cross, time.Now())
		return err
	})
	sourceProblem(t, err, "idempotency_conflict")
	// Keep custom creation valid so this probes the shared idempotency namespace.
	username, password := "synthetic-reader@bootstrap.test", "synthetic-password"
	err = a.attempt(func(ctx context.Context, tx pgx.Tx) error {
		cross := input
		cross.Username = &username
		cross.Password = &password
		_, err := f.store.CreateTx(ctx, tx, f.p, cross, time.Now())
		return err
	})
	sourceProblem(t, err, "idempotency_conflict")
}

func TestExplicitCustomSourceUsesFreshPairAndRecoversWithoutKey(t *testing.T) {
	f := fixtureForAccountExecutor(t, nil)
	var accountCiphertext []byte
	if err := f.conn.QueryRow(context.Background(), `SELECT ciphertext FROM adtr.operation_account_credentials WHERE tenant_id=$1 AND domain_id=$2 AND account_id=$3`, f.p.TenantID, f.id, f.account).Scan(&accountCiphertext); err != nil {
		t.Fatal(err)
	}
	defer clear(accountCiphertext)
	f.revoke()
	username, password := "fresh-custom@example.test", "Synthetic new custom pair"
	in := domains.SourceInput{DomainID: f.id, ExpectedRevision: "2", ExpectedConnectionCredentialGeneration: "2", IdempotencyKey: "explicit-custom", Username: &username, Password: &password}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, f.p, "custom"); err != nil {
			return err
		}
		out, err := f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "custom", in, []string{f.id})
		if err == nil && (out.CredentialSource != domains.SourceCustom || out.ConnectionCredentialGeneration != "3" || out.Revision != "3") {
			t.Fatal("explicit custom generation mismatch", out)
		}
		return err
	})
	var key string
	var sealed, accountAfter []byte
	var bindings int
	err := f.conn.QueryRow(context.Background(), `SELECT p.key_id,p.ciphertext,(SELECT ciphertext FROM adtr.operation_account_credentials a WHERE a.tenant_id=p.tenant_id AND a.domain_id=p.domain_id AND a.account_id=$3),(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.tenant_id=p.tenant_id AND d.domain_id=p.domain_id AND d.consumer_kind='domain.connection_binding') FROM adtr.domain_credentials p WHERE p.tenant_id=$1 AND p.domain_id=$2 AND p.credential_revision=3`, f.p.TenantID, f.id, f.account).Scan(&key, &sealed, &accountAfter, &bindings)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(sealed)
	defer clear(accountAfter)
	if bindings != 0 || !bytes.Equal(accountCiphertext, accountAfter) {
		t.Fatal("custom transition mutated registered pair or retained binding")
	}
	plain, err := f.runtime.Vault().Open(f.p.TenantID, f.id, 3, domainconfig.SealedCredential{KeyID: key, Ciphertext: sealed})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var pair struct {
		Version  int    `json:"version"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.Unmarshal(plain, &pair) != nil || pair.Version != 1 || pair.Username != username || pair.Password != password {
		t.Fatal("custom transition did not seal the fresh supplied pair")
	}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		noKey := domains.New(nil)
		receipt, err := noKey.SourceReceiptTx(ctx, tx, f.p, "explicit-custom", []string{f.id})
		if err != nil {
			return err
		}
		if !receipt.Replayed || receipt.Revision != "3" || receipt.CredentialSource != domains.SourceCustom {
			t.Fatal("no-key receipt recovery changed result", receipt)
		}
		_, err = noKey.SourceReplayTx(ctx, tx, f.p, "custom", in, []string{f.id})
		sourceProblem(t, err, "domain_key_unavailable")
		return nil
	})
}

func TestDetachRemainsAvailableDuringTenantLapse(t *testing.T) {
	f := fixtureForAccountExecutor(t, nil)
	f.revoke()
	f.exec(`UPDATE adtr.resource_tenant_config SET expire_time=1 WHERE tenant_id=$1`, f.p.TenantID)
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		detail, err := f.store.SourceDetailTx(ctx, tx, f.p, "platform_admin", f.id, false, false)
		if err != nil {
			return err
		}
		if detail.Reference != nil || detail.TestEligible || !detail.CredentialConfigured {
			t.Fatal("safe lapse detail unavailable", detail)
		}
		if err = domains.SetSourceContext(ctx, tx, f.p, "detach"); err != nil {
			return err
		}
		_, err = f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "detach", domains.SourceInput{DomainID: f.id, ExpectedRevision: "2", ExpectedConnectionCredentialGeneration: "2", IdempotencyKey: "expired-detach"}, []string{f.id})
		return err
	})
	c := f.connection()
	if c.CredentialSource != domains.SourceUnconfigured || c.CredentialConfigured {
		t.Fatal("lapsed tenant detach retained credential source", c)
	}
	username, password := "new-custom@example.test", "Synthetic pair"
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		if err := domains.SetSourceContext(ctx, tx, f.p, "custom"); err != nil {
			return err
		}
		_, err := f.store.MutateSourceTx(ctx, tx, f.p, "platform_admin", "custom", domains.SourceInput{DomainID: f.id, ExpectedRevision: "3", ExpectedConnectionCredentialGeneration: "3", IdempotencyKey: "expired-custom", Username: &username, Password: &password}, []string{f.id})
		return err
	})
	sourceProblem(t, err, "tenant_expired")
}
