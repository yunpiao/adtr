//go:build integration

package domains_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
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

func runtimeForAccountWire(t *testing.T) (*domainconfig.Runtime, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(33), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"dc1.example.test"}, BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ca, policy := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "policy.json")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(policy, []byte(`{"version":1,"targets":[{"tenantId":"fixture","domain":"example.test","serverNames":["dc1.example.test"],"cidrs":["10.20.0.0/24"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"ADTR_DOMAIN_KEY_ID": "synthetic", "ADTR_DOMAIN_KEY": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))), "ADTR_DOMAIN_PROBE_ENABLED": "true", "ADTR_LDAP_CA_FILE": ca, "ADTR_LDAP_EGRESS_POLICY_FILE": policy}
	runtime, err := domainconfig.Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	return runtime, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestAccountTaskEngineSyntheticTLSLDAPWire(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		t.Run(string(mode), func(t *testing.T) {
			// Real local TCP, TLS, bind and RootDSE bytes; the test-only dial mapping
			// preserves the executor's fixed logical IP/port and avoids privileged ports.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { listener.Close() })
			runtime, certificate := runtimeForAccountWire(t)
			serverDone := make(chan error, 1)
			var owned []byte
			var dials atomic.Int32
			probe := func(ctx context.Context, cfg ldapconnection.Config, p ldapconnection.Credential) (ldapconnection.Result, error) {
				owned = p.Password
				return ldapconnection.ProbeWithDialForIntegrationTest(ctx, cfg, p, func(ctx context.Context, network, address string) (net.Conn, error) {
					port := "389"
					if mode == ldapconnection.LDAPS {
						port = "636"
					}
					if network != "tcp" || address != "10.20.0.8:"+port || dials.Add(1) != 1 {
						return nil, errors.New("unexpected synthetic LDAP destination")
					}
					return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
				})
			}
			f := fixtureForAccountExecutor(t, probe)
			f.runtime = runtime
			f.store = domains.NewProbeForTest(runtime, probe)
			expected := "2"
			if mode == ldapconnection.LDAPS {
				f.tx(func(ctx context.Context, tx pgx.Tx) error {
					_, err := f.store.UpdateTx(ctx, tx, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "2", DCHostName: "dc1.example.test", LDAPAddr: "10.20.0.8", Port: "636"})
					return err
				})
				expected = "3"
			}
			kind := f.store.AccountKind()
			original := kind.OnQuiesced
			var acknowledged atomic.Bool
			kind.OnQuiesced = func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
				for _, b := range owned {
					if b != 0 {
						return errors.New("owned pair survived executor return")
					}
				}
				if !acknowledged.Load() {
					select {
					case err := <-serverDone:
						if err != nil {
							return err
						}
					case <-ctx.Done():
						return ctx.Err()
					}
					acknowledged.Store(true)
				}
				return original(ctx, tx, q)
			}
			f.engine, err = tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), kind), auth.NewTaskAuthorizer(), dbstore.SchemaVersion)
			if err != nil {
				t.Fatal(err)
			}
			var task tasks.Task
			f.tx(func(ctx context.Context, tx pgx.Tx) error {
				out, err := f.store.SubmitTx(ctx, tx, f.engine, f.p, domains.Input{DomainID: f.id, ExpectedRevision: expected, IdempotencyKey: "wire-account"})
				task = out.Task
				return err
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			serverStopped := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				_ = listener.Close()
				select {
				case <-serverStopped:
				case <-time.After(2 * time.Second):
					t.Error("synthetic LDAP server did not join")
				}
			})
			go func() {
				defer close(serverStopped)
				conn, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
				defer stopClose()
				_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
				serverDone <- ldapconnection.ServeAccountWireForIntegrationTest(ctx, conn, mode, certificate, "account-reader@example.test", "Synthetic registered pair")
			}()
			if _, err = f.engine.RunOne(ctx, "wire-owner"); err != nil {
				t.Fatal("account wire execution failed", err)
			}
			if dials.Load() != 1 || !acknowledged.Load() {
				t.Fatal("wire or completion acknowledgement did not run")
			}
			if state, n := f.useState(task.ID); state != "quiesced" || n != 0 {
				t.Fatal("wire probe retained unresolved account use")
			}
			if c := f.connection(); c.ConnectionState != "verified" {
				t.Fatal("real TLS LDAP evidence not published", c.ConnectionState)
			}
			var ciphertext int
			if err = f.conn.QueryRow(ctx, `SELECT count(*) FROM adtr.domain_credentials WHERE tenant_id=$1 AND domain_id=$2`, f.p.TenantID, f.id).Scan(&ciphertext); err != nil || ciphertext != 0 {
				t.Fatal("wire reference duplicated domain ciphertext", err)
			}
		})
	}
}
