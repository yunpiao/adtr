package domains

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestDirectoryKindKeepsSingleAttemptAndPublicationBoundary(t *testing.T) {
	kind := New(nil).DirectoryKind()
	if kind.Name != "domain.directory_read" || kind.Version != 1 || kind.MaxAttempts != 1 || !kind.SingleAttemptOnly || kind.ReplaySafe || kind.Schedulable || kind.Platform || kind.OwnerScoped || !kind.CancelDiscardsResult || len(kind.RetryCodes) != 0 || kind.OnQuiesced == nil {
		t.Fatal("directory policy widened or lost cancellation/quiescence boundary")
	}
	if kind.Timeout != 125*time.Second || kind.Lease != 30*time.Second || kind.Heartbeat != time.Second {
		t.Fatal("directory task timeout or lease policy changed")
	}
	if _, err := tasks.NewRegistry(kind); err != nil {
		t.Fatal(err)
	}
	if _, err := kind.Validate(validAccountPins().json()); err == nil {
		t.Fatal("connection-test payload accepted")
	}
	if limits := directoryReadLimits(); limits != (directoryassets.Limits{MaxRows: 10000, MaxPages: 100, MaxPageEntries: 1000, MaxBytes: 16 << 20, MaxCookieBytes: 65536}) {
		t.Fatal("version 1 provisional security caps changed", limits)
	}
}

func TestDirectoryExecutorRejectsInvalidIdentityBeforeTransaction(t *testing.T) {
	for name, change := range map[string]func(*tasks.Task){
		"empty":          func(v *tasks.Task) { v.Payload = nil },
		"malformed":      func(v *tasks.Task) { v.Payload = []byte(`{}`) },
		"b2-payload":     func(v *tasks.Task) { v.Payload = validAccountPins().json() },
		"b2-kind":        func(v *tasks.Task) { v.Kind = AccountKindName },
		"version":        func(v *tasks.Task) { v.PayloadVersion = 2 },
		"repeated":       func(v *tasks.Task) { v.MaxAttempts = 2 },
		"replayed-child": func(v *tasks.Task) { v.ParentID = "previous-task" },
		"no-actor":       func(v *tasks.Task) { v.ActorID = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			task := directoryUseTestTask()
			change(&task)
			out := New(nil).executeDirectory(context.Background(), tasks.Execution{Task: task,
				WithTx: func(context.Context, int64, tasks.FencedWork) (int64, error) {
					t.Fatal("invalid identity reached a transaction")
					return 0, nil
				}})
			assertDirectoryOutcome(t, out, tasks.Failed, "invalid_input")
		})
	}
	assertDirectoryOutcome(t, New(nil).executeDirectory(context.Background(), tasks.Execution{}), tasks.Failed, "executor_unavailable")
}

func TestDirectoryExecutorStopsBeforeOpening(t *testing.T) {
	t.Run("disabled-runtime", func(t *testing.T) {
		calls := 0
		out := New(nil).executeDirectory(context.Background(), tasks.Execution{Task: directoryUseTestTask(),
			WithTx: func(ctx context.Context, _ int64, work tasks.FencedWork) (int64, error) {
				calls++
				_, _, _, err := work(ctx, nil)
				return 0, err
			}})
		assertDirectoryOutcome(t, out, tasks.Failed, "directory_read_disabled")
		if calls != 1 {
			t.Fatal("unexpected transaction count", calls)
		}
	})
	for name, failure := range map[string]error{"fence": tasks.ErrLeaseLost, "authorization": tasks.ErrAuthorization, "cancellation": tasks.ErrCancelRequested, "timeout": context.DeadlineExceeded} {
		t.Run(name, func(t *testing.T) {
			out := New(nil).executeDirectory(context.Background(), tasks.Execution{Task: directoryUseTestTask(),
				WithTx: func(context.Context, int64, tasks.FencedWork) (int64, error) { return 0, failure }})
			want := directoryExecutionError(failure)
			assertDirectoryOutcome(t, out, want.State, want.Code)
		})
	}
	t.Run("already-cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out := New(nil).executeDirectory(ctx, tasks.Execution{Task: directoryUseTestTask(),
			WithTx: func(context.Context, int64, tasks.FencedWork) (int64, error) {
				t.Fatal("cancelled execution reached a transaction")
				return 0, nil
			}})
		assertDirectoryOutcome(t, out, tasks.Cancelled, "cancelled")
	})
}

// These tests stub pgx and WithTx to exercise executor ordering and ownership.
// They do not execute PostgreSQL, its locks/triggers/commit, or real LDAP I/O.
func TestDirectoryExecutorStubbedOpeningFailureStopsCredentialUse(t *testing.T) {
	for _, mode := range []string{"grant-denied", "open-conflict", "missing-envelope", "scan-error", "commit-error", "cancel-after-commit", "panic-in-scan", "panic-after-callback"} {
		t.Run(mode, func(t *testing.T) {
			store, task, tx := directoryExecutorStubFixture(t)
			tx.failure = mode
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var outcome tasks.Outcome
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				outcome = store.executeDirectory(ctx, tasks.Execution{Task: task,
					WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
						calls++
						if calls != 1 || version != task.ResultVersion {
							t.Fatal("credential use continued after failed opening")
						}
						progress, cursor, result, err := work(ctx, tx)
						assertDirectoryCheckpoint(t, progress, cursor, result)
						if err != nil {
							return version, err
						}
						switch mode {
						case "commit-error":
							return version, errors.New("synthetic secret-bearing commit error")
						case "cancel-after-commit":
							cancel()
						case "panic-after-callback":
							panic("synthetic transaction panic")
						}
						return version + 1, nil
					}})
			}()
			if strings.HasPrefix(mode, "panic-") {
				if panicked == nil {
					t.Fatal("panic did not reach the engine boundary")
				}
			} else {
				if panicked != nil {
					t.Fatal("unexpected panic", panicked)
				}
				state, code := tasks.Failed, "checkpoint_failed"
				switch mode {
				case "grant-denied":
					code = "authorization_revoked"
				case "open-conflict":
					code = "directory_use_changed"
				case "missing-envelope":
					code = "credential_unavailable"
				case "cancel-after-commit":
					state, code = tasks.Cancelled, "cancelled"
				}
				assertDirectoryOutcome(t, outcome, state, code)
			}
			if mode == "grant-denied" && (tx.openCalls != 0 || tx.envelopeCalls != 0) {
				t.Fatal("authority rejection reached opening or envelope read")
			}
			if mode == "open-conflict" && tx.envelopeCalls != 0 {
				t.Fatal("failed opening reached envelope read")
			}
			if tx.scannedEnvelope != nil && !bytes.Equal(tx.scannedEnvelope, make([]byte, len(tx.scannedEnvelope))) {
				t.Fatal("executor retained copied envelope after error/cancellation/panic")
			}
		})
	}
}

func TestDirectoryExecutorRealReaderRejectsBeforeNetworkWithStubbedFence(t *testing.T) {
	for _, rejection := range []string{"engine-fence", "current-directory-grant"} {
		t.Run(rejection, func(t *testing.T) {
			store, task, tx := directoryExecutorStubFixture(t)
			calls := 0
			out := store.executeDirectory(context.Background(), tasks.Execution{Task: task,
				WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
					calls++
					if version != task.ResultVersion+int64(calls-1) {
						t.Fatal("executor used a stale checkpoint version")
					}
					if calls == 2 && rejection == "engine-fence" {
						// ReadDirectory's first typed authorization callback reaches the
						// fence before DNS/dial. This is a synthetic engine rejection.
						return version, tasks.ErrAuthorization
					}
					if calls > 2 {
						t.Fatal("failed transport attempted publication")
					}
					if calls == 2 {
						// Invoke the real callback/currentDirectory with a now-revoked
						// synthetic grant. Reusing opening authority would miss this.
						tx.failure = "grant-denied"
					}
					progress, cursor, result, err := work(ctx, tx)
					assertDirectoryCheckpoint(t, progress, cursor, result)
					return version + 1, err
				}})
			assertDirectoryOutcome(t, out, tasks.Failed, "authorization_revoked")
			if calls != 2 || tx.envelopeCalls != 1 || tx.openCalls != 1 {
				t.Fatal("real reader did not recheck after opening", calls, tx.envelopeCalls, tx.openCalls)
			}
			if !bytes.Equal(tx.scannedEnvelope, make([]byte, len(tx.scannedEnvelope))) {
				t.Fatal("copied envelope retained after reader rejection")
			}
		})
	}
}

func TestDirectoryExecutorInvalidDecryptedEnvelopeNeverReachesReader(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"username":"synthetic@example.test","password":"synthetic-password"}`,
		`{"version":2,"username":"synthetic@example.test","password":""}`,
		`{"version":2,"username":"synthetic@example.test","password":null}`,
		`{"version":2,"username":"synthetic@example.test","password":"synthetic-password","password":"other"}`,
		`{"version":2,"username":"synthetic@example.test","password":"synthetic-password","filter":"(objectClass=*)"}`,
	} {
		t.Run("invalid-envelope", func(t *testing.T) {
			store, task, tx := directoryExecutorStubFixture(t)
			clear(tx.sealed.Ciphertext)
			sealed, err := store.runtime.Vault().SealOperationCredential(task.TenantID, task.DomainID, tx.pins.AccountID, 1, []byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			defer clear(sealed.Ciphertext)
			tx.sealed = sealed
			calls := 0
			out := store.executeDirectory(context.Background(), tasks.Execution{Task: task,
				WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
					calls++
					if calls != 1 {
						t.Fatal("invalid credential reached the reader or publication")
					}
					progress, cursor, result, err := work(ctx, tx)
					assertDirectoryCheckpoint(t, progress, cursor, result)
					return version + 1, err
				}})
			assertDirectoryOutcome(t, out, tasks.Failed, "credential_unavailable")
			if !bytes.Equal(tx.scannedEnvelope, make([]byte, len(tx.scannedEnvelope))) {
				t.Fatal("copied envelope retained after invalid credentials")
			}
		})
	}
}

func TestDirectoryReadProgressStaysBoundedAcrossAllPages(t *testing.T) {
	previous := 40
	for page := 0; page <= directoryReadLimits().MaxPages+10; page++ {
		progress := directoryReadProgress(ldapconnection.DirectoryPage, page)
		if progress < previous || progress > 90 {
			t.Fatal("page progress regressed or exceeded its budget", page, progress)
		}
		previous = progress
	}
	if directoryReadProgress(ldapconnection.DirectoryReturn, 100) != 95 || directoryReadProgress(ldapconnection.DirectoryPage, -1) < 0 {
		t.Fatal("progress reached publication early or became negative")
	}
}

func assertDirectoryOutcome(t *testing.T, out tasks.Outcome, state tasks.State, code string) {
	t.Helper()
	if out.State != state || out.Code != code || out.Retryable || string(out.Result) != "{}" {
		t.Fatal("unexpected directory outcome", out.State, out.Code, out.Retryable, string(out.Result))
	}
}

func assertDirectoryCheckpoint(t *testing.T, progress int, cursor, result json.RawMessage) {
	t.Helper()
	if progress < 0 || progress > 100 || string(cursor) != "{}" || string(result) != "{}" {
		t.Fatal("checkpoint contains objects/cookies or invalid progress")
	}
}

type directoryExecutorStubRow func(...any) error

func (r directoryExecutorStubRow) Scan(dest ...any) error { return r(dest...) }

// Embed the interface to make any unplanned SQL operation fail immediately.
type directoryExecutorStubTx struct {
	pgx.Tx
	t                        *testing.T
	task                     tasks.Task
	pins                     directoryPinnedPayload
	sealed                   domainconfig.OperationSealedCredential
	failure                  string
	openCalls, envelopeCalls int
	scannedEnvelope          []byte
	dictionary               string
}

func (s *directoryExecutorStubTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.t.Helper()
	if len(args) < 2 || args[0] != s.task.TenantID || args[1] != s.task.DomainID {
		s.t.Fatal("query lost tenant/domain scope")
	}
	return directoryExecutorStubRow(func(dest ...any) error {
		var values []any
		switch {
		case strings.Contains(sql, "FROM adtr.domain_connections"):
			values = []any{s.task.DomainID, "example.test", "dc1.example.test", "10.20.0.1", "636", "ldaps", int64(2), int64(2), time.Now(), time.Now(), (*time.Time)(nil), "unrelated-b2-task", int64(987), "operation_account", s.pins.AccountID, int64(1)}
		case strings.Contains(sql, "SELECT g.role_id,g.grant_revision::text"):
			if len(args) != 7 || args[6] != s.task.Kind {
				s.t.Fatal("directory grant query used another purpose")
			}
			if s.failure == "grant-denied" {
				return pgx.ErrNoRows
			}
			values = []any{s.pins.GrantRoleID, s.pins.GrantRevision}
		case strings.Contains(sql, "SELECT key_id,ciphertext"):
			s.envelopeCalls++
			if s.openCalls != 1 || !strings.Contains(sql, "envelope_version=2") || len(args) != 4 || args[2] != s.pins.AccountID || args[3] != s.pins.AccountCredentialRevision {
				s.t.Fatal("envelope read preceded opened use or lost account/version pins")
			}
			if s.failure == "missing-envelope" {
				return pgx.ErrNoRows
			}
			s.scannedEnvelope = append([]byte(nil), s.sealed.Ciphertext...)
			values = []any{s.sealed.KeyID, s.scannedEnvelope}
		default:
			s.t.Fatal("unexpected SQL in stub", sql)
		}
		if len(values) != len(dest) {
			s.t.Fatal("stub column mismatch")
		}
		for i := range values {
			reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(values[i]))
		}
		if strings.Contains(sql, "SELECT key_id,ciphertext") {
			switch s.failure {
			case "scan-error":
				return errors.New("synthetic secret-bearing scan error")
			case "panic-in-scan":
				panic("synthetic scan panic after copying envelope")
			}
		}
		return nil
	})
}

func (s *directoryExecutorStubTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	s.t.Helper()
	if !strings.HasPrefix(sql, "UPDATE adtr.domain_directory_task_uses u SET state='opened'") || len(args) != 16 || args[0] != s.task.TenantID || args[1] != s.task.DomainID || args[2] != s.task.ID || args[14] != s.task.Kind || args[15] != s.dictionary {
		s.t.Fatal("unexpected mutation in opening stub", sql)
	}
	s.openCalls++
	if s.failure == "open-conflict" {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func directoryExecutorStubFixture(t *testing.T) (*Store, tasks.Task, *directoryExecutorStubTx) {
	t.Helper()
	return directoryExecutorStubFixtureForProfile(t, directoryTaskV1)
}

func directoryExecutorStubFixtureForProfile(t *testing.T, profile directoryTaskProfile) (*Store, tasks.Task, *directoryExecutorStubTx) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	caPath, policyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "policy.json")
	if err = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(policyPath, []byte(`{"version":1,"targets":[{"tenantId":"tenant","domain":"example.test","serverNames":["dc1.example.test"],"cidrs":["10.20.0.0/24"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 32)), "ADTR_DIRECTORY_READ_ENABLED": "true", "ADTR_LDAP_CA_FILE": caPath, "ADTR_LDAP_EGRESS_POLICY_FILE": policyPath}
	if profile == directoryTaskV2 {
		env["ADTR_DIRECTORY_READ_V2_ENABLED"] = "true"
	}
	runtime, err := domainconfig.Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	store := New(runtime)
	pins := directoryUseTestPins()
	pins.PolicyRevision = store.policyRevision()
	task := directoryUseTestTask()
	identity, ok := profile.identity()
	if !ok {
		t.Fatal("invalid fixture profile")
	}
	task.Kind, task.Payload = identity.kind, profile.payload(pins)
	task.State, task.Attempt, task.LeaseOwner, task.FencingToken = tasks.Running, 1, "synthetic-worker", 1
	sealed, err := runtime.Vault().SealOperationCredential(task.TenantID, task.DomainID, pins.AccountID, 1, []byte(`{"version":2,"username":"synthetic@example.test","password":"synthetic-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clear(sealed.Ciphertext) })
	return store, task, &directoryExecutorStubTx{t: t, task: task, pins: pins, sealed: sealed, dictionary: strconv.Itoa(identity.dictionaryVersion)}
}
