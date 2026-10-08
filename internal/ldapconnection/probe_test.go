package ldapconnection

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureHost = "dc.synthetic.test"

func fixtureCredential() Credential {
	return Credential{Username: "probe@synthetic.test", Password: []byte("synthetic-only-secret")}
}

func fixtureConfig(t *testing.T, mode Mode, roots *x509.CertPool) Config {
	t.Helper()
	if roots == nil {
		_, roots = trustedFixture(t)
	}
	return Config{Mode: mode, ServerName: fixtureHost, Roots: roots,
		DialIP:          netip.MustParseAddr("127.0.0.1"),
		AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		Authorize:       func(context.Context, Stage) error { return nil },
	}
}

func fixtureCertificate(t *testing.T, dns string, notBefore, notAfter time.Time, usage x509.ExtKeyUsage) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic test only"},
		NotBefore: notBefore, NotAfter: notAfter, DNSNames: []string{dns},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}

func trustedFixture(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	return fixtureCertificate(t, fixtureHost, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
}

func pipeTransport(t *testing.T, mode Mode, serve func(net.Conn) error) transport {
	t.Helper()
	var calls atomic.Int32
	return transport{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if calls.Add(1) != 1 {
			t.Error("probe retried a connection")
		}
		port := "636"
		if mode == StartTLS {
			port = "389"
		}
		if network != "tcp" || address != "127.0.0.1:"+port {
			t.Errorf("unexpected fixed transport destination: %s %s", network, address)
		}
		client, server := net.Pipe()
		done := make(chan error, 1)
		go func() {
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(5 * time.Second))
			done <- serve(server)
		}()
		t.Cleanup(func() {
			_ = client.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(6 * time.Second):
				t.Error("fixture goroutine did not stop")
			}
		})
		return client, nil
	}}
}

func fixtureResult(id, tag, code byte, extra ...[]byte) []byte {
	body := []byte{0x0a, 0x01, code, 0x04, 0x00, 0x04, 0x00}
	for _, item := range extra {
		body = append(body, item...)
	}
	return message(id, tag, body)
}

func fixtureAttribute(name string, values ...string) []byte {
	var encoded [][]byte
	for _, value := range values {
		encoded = append(encoded, octets(value))
	}
	return element(0x30, octets(name), element(0x31, encoded...))
}

func fixtureRootDSE() []byte {
	return append(octets(""), element(0x30,
		fixtureAttribute("dnsHostName", fixtureHost),
		fixtureAttribute("defaultNamingContext", "DC=synthetic,DC=test"),
		fixtureAttribute("supportedCapabilities", "1.2.840.113556.1.4.800"),
		fixtureAttribute("supportedLDAPVersion", "2", "3"))...)
}

func readExpected(conn net.Conn, id int, tag byte) ([]byte, error) {
	got, body, err := readMessage(conn, id)
	if err != nil || got != tag {
		return nil, errors.New("unexpected LDAP request")
	}
	return body, nil
}

func establishFixtureTLS(raw net.Conn, mode Mode, certificate tls.Certificate) (*tls.Conn, error) {
	if mode == StartTLS {
		body, err := readExpected(raw, 1, 0x77)
		if err != nil || !bytes.Equal(body, element(0x80, []byte(startTLSOID))) {
			return nil, errors.New("expected StartTLS as the first plaintext operation")
		}
		if _, err := raw.Write(fixtureResult(1, 0x78, 0)); err != nil {
			return nil, err
		}
	}
	secured := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	return secured, secured.Handshake()
}

func expectNoApplicationBytes(conn net.Conn) error {
	var buffer [1]byte
	n, err := conn.Read(buffer[:])
	if n != 0 || err == nil {
		return errors.New("received forbidden subsequent LDAP bytes")
	}
	var timed net.Error
	if errors.As(err, &timed) && timed.Timeout() {
		return errors.New("probe did not close its connection")
	}
	return nil
}

func assertError(t *testing.T, err error, code Code, stage Stage) {
	t.Helper()
	var classified *Error
	if !errors.As(err, &classified) || classified.Code != code || classified.Stage != stage {
		t.Fatalf("got %v; want %s:%s", err, code, stage)
	}
	if errors.Unwrap(err) != nil {
		t.Error("raw error was retained")
	}
}

func TestProbeSecureSuccess(t *testing.T) {
	for _, mode := range []Mode{LDAPS, StartTLS} {
		t.Run(string(mode), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			var stages []Stage
			cfg := fixtureConfig(t, mode, roots)
			cfg.Authorize = func(ctx context.Context, stage Stage) error {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > stepTimeout {
					t.Error("authorization callback lacks its bounded deadline")
				}
				stages = append(stages, stage)
				return nil
			}
			network := pipeTransport(t, mode, func(raw net.Conn) error {
				secured, err := establishFixtureTLS(raw, mode, certificate)
				if err != nil {
					return err
				}
				body, err := readExpected(secured, 2, 0x60)
				if err != nil {
					return err
				}
				bindFields := berReader(body)
				version, err := bindFields.expect(0x02)
				if err != nil || !bytes.Equal(version, []byte{3}) {
					return errors.New("bind did not request LDAPv3")
				}
				username, err := bindFields.expect(0x04)
				if err != nil || string(username) != fixtureCredential().Username {
					return errors.New("incorrect bind identity")
				}
				password, err := bindFields.expect(0x80)
				if err != nil || !bytes.Equal(password, fixtureCredential().Password) || len(bindFields) != 0 {
					return errors.New("incorrect bind credential")
				}
				if _, err := secured.Write(fixtureResult(2, 0x61, 0)); err != nil {
					return err
				}
				body, err = readExpected(secured, 3, 0x63)
				if err != nil {
					return err
				}
				// This literal verifies empty base DN, base scope, no alias
				// dereference, size=1, time=3, typesOnly=false, presence filter.
				prefix := []byte{4, 0, 10, 1, 0, 10, 1, 0, 2, 1, 1, 2, 1, 3, 1, 1, 0, 0x87, 11}
				prefix = append(prefix, []byte("objectClass")...)
				if !bytes.HasPrefix(body, prefix) {
					return errors.New("RootDSE search scope, limits or filter changed")
				}
				fields := berReader(body[len(prefix):])
				attrs, err := fields.expect(0x30)
				if err != nil || len(fields) != 0 {
					return errors.New("invalid requested attribute list")
				}
				list := berReader(attrs)
				for _, expected := range []string{"dnsHostName", "defaultNamingContext", "supportedCapabilities", "supportedLDAPVersion"} {
					value, err := list.expect(4)
					if err != nil || string(value) != expected {
						return errors.New("unexpected RootDSE attribute selection")
					}
				}
				if len(list) != 0 {
					return errors.New("extra requested attributes")
				}
				if _, err := secured.Write(message(3, 0x64, fixtureRootDSE())); err != nil {
					return err
				}
				_, err = secured.Write(fixtureResult(3, 0x65, 0))
				return err
			})
			result, err := probe(context.Background(), cfg, fixtureCredential(), network)
			if err != nil {
				t.Fatal(err)
			}
			if result.DCHostName != fixtureHost || result.DefaultNamingContext != "DC=synthetic,DC=test" ||
				!reflect.DeepEqual(result.SupportedCapabilities, []string{"1.2.840.113556.1.4.800"}) || result.ElapsedMilliseconds < 0 {
				t.Fatalf("unexpected RootDSE result: %+v", result)
			}
			if !reflect.DeepEqual(stages, []Stage{StageDial, StageTLS, StageBind, StageSearch}) {
				t.Fatalf("unexpected authorization sequence: %v", stages)
			}
		})
	}
}

func TestTLSFailureNeverBinds(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name          string
		host          string
		before, after time.Time
		usage         x509.ExtKeyUsage
		untrusted     bool
		code          Code
	}{
		{"hostname", "wrong.synthetic.test", now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageServerAuth, false, CodeTLSHostname},
		{"expired", fixtureHost, now.Add(-2 * time.Hour), now.Add(-time.Hour), x509.ExtKeyUsageServerAuth, false, CodeTLSExpired},
		{"not-yet-valid", fixtureHost, now.Add(time.Hour), now.Add(2 * time.Hour), x509.ExtKeyUsageServerAuth, false, CodeTLSExpired},
		{"untrusted", fixtureHost, now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageServerAuth, true, CodeTLSUntrusted},
		{"wrong-eku", fixtureHost, now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageClientAuth, false, CodeTLSUntrusted},
	} {
		for _, mode := range []Mode{LDAPS, StartTLS} {
			t.Run(tc.name+"/"+string(mode), func(t *testing.T) {
				certificate, roots := fixtureCertificate(t, tc.host, tc.before, tc.after, tc.usage)
				if tc.untrusted {
					_, roots = trustedFixture(t)
				}
				network := pipeTransport(t, mode, func(raw net.Conn) error {
					secured, err := establishFixtureTLS(raw, mode, certificate)
					if err == nil {
						return errors.New("server unexpectedly completed rejected TLS handshake")
					}
					if secured != nil {
						return expectNoApplicationBytes(secured)
					}
					return errors.New("fixture failed before TLS")
				})
				cfg := fixtureConfig(t, mode, roots)
				cfg.Authorize = func(_ context.Context, stage Stage) error {
					if stage != StageDial && stage != StageTLS {
						t.Error("probe reached credentials after rejected TLS")
					}
					return nil
				}
				_, err := probe(context.Background(), cfg, fixtureCredential(), network)
				assertError(t, err, tc.code, StageTLS)
			})
		}
	}
}

func TestStartTLSRefusalNeverBinds(t *testing.T) {
	network := pipeTransport(t, StartTLS, func(raw net.Conn) error {
		if _, err := readExpected(raw, 1, 0x77); err != nil {
			return err
		}
		if _, err := raw.Write(fixtureResult(1, 0x78, 53)); err != nil {
			return err
		}
		return expectNoApplicationBytes(raw)
	})
	_, err := probe(context.Background(), fixtureConfig(t, StartTLS, nil), fixtureCredential(), network)
	assertError(t, err, CodeStartTLSRequired, StageStartTLS)
}

func TestBindFailureNeverSearchesOrRetries(t *testing.T) {
	for _, tc := range []struct {
		name string
		code byte
		want Code
	}{
		{"credentials", 49, CodeCredentialsRejected}, {"access", 50, CodeLDAPAccessDenied},
		{"referral", 10, CodeReferralRejected}, {"timeout", 3, CodeLDAPTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := establishFixtureTLS(raw, LDAPS, certificate)
				if err != nil {
					return err
				}
				if _, err := readExpected(secured, 2, 0x60); err != nil {
					return err
				}
				// Server diagnostics deliberately contain credential-shaped text;
				// the public error must contain only the fixed code and stage.
				body := append([]byte{10, 1, tc.code, 4, 0}, octets("private diagnostic synthetic-only-secret")...)
				if _, err := secured.Write(message(2, 0x61, body)); err != nil {
					return err
				}
				return expectNoApplicationBytes(secured)
			})
			_, err := probe(context.Background(), fixtureConfig(t, LDAPS, roots), fixtureCredential(), network)
			assertError(t, err, tc.want, StageBind)
			if strings.Contains(fmt.Sprintf("%+v", err), "synthetic") {
				t.Error("diagnostic leaked")
			}
		})
	}
}

func TestAuthorizationDenialSendsNoStageBytes(t *testing.T) {
	for _, denied := range []Stage{StageDial, StageBind, StageSearch} {
		t.Run(string(denied), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureConfig(t, LDAPS, roots)
			cfg.Authorize = func(_ context.Context, stage Stage) error {
				if stage == denied {
					return errors.New("private revision diagnostic")
				}
				return nil
			}
			network := transport{dial: func(context.Context, string, string) (net.Conn, error) {
				t.Error("denied dial was attempted")
				return nil, errors.New("unexpected dial")
			}}
			if denied != StageDial {
				network = pipeTransport(t, LDAPS, func(raw net.Conn) error {
					secured, err := establishFixtureTLS(raw, LDAPS, certificate)
					if err != nil {
						return err
					}
					if denied == StageSearch {
						if _, err := readExpected(secured, 2, 0x60); err != nil {
							return err
						}
						if _, err := secured.Write(fixtureResult(2, 0x61, 0)); err != nil {
							return err
						}
					}
					return expectNoApplicationBytes(secured)
				})
			}
			_, err := probe(context.Background(), cfg, fixtureCredential(), network)
			assertError(t, err, CodeAuthorizationRevoked, denied)
		})
	}
}

func TestEgressRejectsEveryUnsafeAnswerBeforeDial(t *testing.T) {
	allowed := netip.MustParseAddr("127.0.0.1")
	for _, tc := range []struct {
		name      string
		addresses []netip.Addr
	}{
		{"mixed-denied", []netip.Addr{allowed, netip.MustParseAddr("192.0.2.1")}},
		{"mapped-denied", []netip.Addr{netip.MustParseAddr("::ffff:192.0.2.1")}},
		{"azure-platform", []netip.Addr{netip.MustParseAddr("168.63.129.16")}},
		{"azure-platform-mapped", []netip.Addr{netip.MustParseAddr("::ffff:168.63.129.16")}},
		{"metadata-v4", []netip.Addr{netip.MustParseAddr("100.100.100.200")}},
		{"metadata-mapped", []netip.Addr{netip.MustParseAddr("::ffff:100.100.100.200")}},
		{"metadata-v6", []netip.Addr{netip.MustParseAddr("fd00:ec2::254")}},
		{"broadcast", []netip.Addr{netip.MustParseAddr("255.255.255.255")}},
		{"unspecified", []netip.Addr{netip.MustParseAddr("0.0.0.0")}},
		{"multicast", []netip.Addr{netip.MustParseAddr("224.0.0.1")}},
		{"link-local", []netip.Addr{netip.MustParseAddr("169.254.2.1")}},
		{"v6-link-local", []netip.Addr{netip.MustParseAddr("fe80::1")}},
		{"zone", []netip.Addr{netip.MustParseAddr("fe80::1%eth0")}},
		{"mapped-zone", []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1%eth0")}},
		{"excess", make([]netip.Addr, 17)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fixtureConfig(t, LDAPS, nil)
			cfg.DialIP = netip.Addr{}
			if tc.name != "mixed-denied" && tc.name != "mapped-denied" {
				cfg.AllowedNetworks = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
			}
			calls := 0
			network := transport{
				lookup: func(ctx context.Context, network, host string) ([]netip.Addr, error) {
					calls++
					if host != fixtureHost || network != "ip" {
						t.Error("unexpected DNS query")
					}
					return tc.addresses, nil
				},
				dial: func(context.Context, string, string) (net.Conn, error) {
					t.Error("unsafe answer was dialed")
					return nil, errors.New("unexpected dial")
				},
			}
			_, err := probe(context.Background(), cfg, fixtureCredential(), network)
			assertError(t, err, CodeEgressDenied, StageDNS)
			if calls != 1 {
				t.Errorf("DNS queried %d times", calls)
			}
		})
	}
}

func TestDialIPIsNormalizedAndCannotChangeTLSIdentity(t *testing.T) {
	certificate, roots := trustedFixture(t)
	cfg := fixtureConfig(t, LDAPS, roots)
	cfg.DialIP = netip.MustParseAddr("::ffff:127.0.0.1")
	network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
		secured, err := establishFixtureTLS(raw, LDAPS, certificate)
		if err != nil {
			return err
		}
		if secured.ConnectionState().ServerName != fixtureHost {
			return errors.New("TLS DNS identity changed to IP")
		}
		if _, err := readExpected(secured, 2, 0x60); err != nil {
			return err
		}
		_, err = secured.Write(fixtureResult(2, 0x61, 49))
		return err
	})
	_, err := probe(context.Background(), cfg, fixtureCredential(), network)
	assertError(t, err, CodeCredentialsRejected, StageBind)
}

func TestCancelledOperationsCloseAndJoin(t *testing.T) {
	for _, stage := range []Stage{StageTLS, StageStartTLS, StageBind, StageSearch} {
		t.Run(string(stage), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			mode := LDAPS
			if stage == StageStartTLS {
				mode = StartTLS
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			network := pipeTransport(t, mode, func(raw net.Conn) error {
				if stage == StageTLS {
					buffer := make([]byte, 4096)
					if _, err := raw.Read(buffer); err != nil {
						return err
					}
					cancel()
					_, err := io.Copy(io.Discard, raw)
					return err
				}
				if stage == StageStartTLS {
					if _, err := readExpected(raw, 1, 0x77); err != nil {
						return err
					}
					cancel()
					return expectNoApplicationBytes(raw)
				}
				secured, err := establishFixtureTLS(raw, mode, certificate)
				if err != nil {
					return err
				}
				if _, err := readExpected(secured, 2, 0x60); err != nil {
					return err
				}
				if stage == StageSearch {
					if _, err := secured.Write(fixtureResult(2, 0x61, 0)); err != nil {
						return err
					}
					if _, err := readExpected(secured, 3, 0x63); err != nil {
						return err
					}
				}
				cancel()
				return expectNoApplicationBytes(secured)
			})
			started := time.Now()
			_, err := probe(ctx, fixtureConfig(t, mode, roots), fixtureCredential(), network)
			assertError(t, err, CodeCancelled, stage)
			if time.Since(started) > time.Second {
				t.Error("cancellation was not prompt")
			}
		})
	}
}

func TestParentDeadlineBoundsBlockedTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
		_, err := io.Copy(io.Discard, raw)
		return err
	})
	started := time.Now()
	_, err := probe(ctx, fixtureConfig(t, LDAPS, nil), fixtureCredential(), network)
	assertError(t, err, CodeConnectionTimeout, StageTLS)
	if time.Since(started) > time.Second {
		t.Error("parent deadline was not respected")
	}
}

func TestTLSBelow12NeverBinds(t *testing.T) {
	certificate, roots := trustedFixture(t)
	network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
		secured := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{certificate},
			MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11})
		if err := secured.Handshake(); err == nil {
			return errors.New("obsolete TLS version was accepted")
		}
		return expectNoApplicationBytes(secured)
	})
	_, err := probe(context.Background(), fixtureConfig(t, LDAPS, roots), fixtureCredential(), network)
	assertError(t, err, CodeTLSFailed, StageTLS)
}

func TestDNSAndConnectFailuresNeverRetryOrLeak(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dnsError  error
		noAnswers bool
		code      Code
		stage     Stage
	}{
		{"dns-failed", errors.New("private DNS information"), false, CodeDNSFailed, StageDNS},
		{"dns-timeout", context.DeadlineExceeded, false, CodeConnectionTimeout, StageDNS},
		{"no-answers", nil, true, CodeDNSFailed, StageDNS},
		{"connect-failed", nil, false, CodeConnectFailed, StageDial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fixtureConfig(t, LDAPS, nil)
			cfg.DialIP = netip.Addr{}
			lookups, dials := 0, 0
			network := transport{
				lookup: func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
					lookups++
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > dialTimeout {
						t.Error("DNS budget missing")
					}
					if tc.noAnswers || tc.dnsError != nil {
						return nil, tc.dnsError
					}
					return []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1"), netip.MustParseAddr("127.0.0.2")}, nil
				},
				dial: func(ctx context.Context, _, address string) (net.Conn, error) {
					dials++
					if address != "127.0.0.1:636" {
						t.Error("DNS was resolved again or unnormalized address was dialed")
					}
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > dialTimeout {
						t.Error("dial budget missing")
					}
					return nil, errors.New("private transport information")
				},
			}
			_, err := probe(context.Background(), cfg, fixtureCredential(), network)
			assertError(t, err, tc.code, tc.stage)
			if lookups != 1 || dials > 1 {
				t.Errorf("unexpected retries: DNS=%d, dial=%d", lookups, dials)
			}
			if tc.stage == StageDNS && dials != 0 {
				t.Error("dial ran after DNS failure")
			}
		})
	}
}

func TestAuthorizationCancellationUsesDeadlineAndStopsBeforeDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := fixtureConfig(t, LDAPS, nil)
	returned := false
	cfg.Authorize = func(ctx context.Context, _ Stage) error {
		cancel()
		<-ctx.Done()
		returned = true
		return errors.New("private database cancellation")
	}
	_, err := probe(ctx, cfg, fixtureCredential(), transport{})
	assertError(t, err, CodeCancelled, StageDial)
	if !returned {
		t.Error("authorization callback outlived Probe")
	}
}

func TestInvalidConfigNeverUsesTransport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config, *Credential)
	}{
		{"plaintext", func(c *Config, _ *Credential) { c.Mode = "ldap" }},
		{"url", func(c *Config, _ *Credential) { c.ServerName = "ldaps://dc.synthetic.test" }},
		{"port", func(c *Config, _ *Credential) { c.ServerName += ":1636" }},
		{"ip-host", func(c *Config, _ *Credential) { c.ServerName = "127.0.0.1" }},
		{"short-host", func(c *Config, _ *Credential) { c.ServerName = "dc" }},
		{"no-authorizer", func(c *Config, _ *Credential) { c.Authorize = nil }},
		{"no-networks", func(c *Config, _ *Credential) { c.AllowedNetworks = nil }},
		{"no-user", func(_ *Config, c *Credential) { c.Username = "" }},
		{"no-password", func(_ *Config, c *Credential) { c.Password = nil }},
		{"huge-password", func(_ *Config, c *Credential) { c.Password = make([]byte, 4097) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, credential := fixtureConfig(t, LDAPS, nil), fixtureCredential()
			tc.mutate(&cfg, &credential)
			_, err := Probe(context.Background(), cfg, credential)
			assertError(t, err, CodeInvalidConfig, StageValidate)
		})
	}
}

func TestCredentialAndErrorsDoNotSerializeSecrets(t *testing.T) {
	credential := fixtureCredential()
	encoded, err := json.Marshal(credential)
	if err != nil || string(encoded) != "{}" {
		t.Fatalf("credential serialized: %s", encoded)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, credential), "synthetic") {
			t.Error("credential formatted")
		}
	}
}

func TestMissingExplicitRootsNeverDialOrBind(t *testing.T) {
	for _, mode := range []Mode{LDAPS, StartTLS} {
		for _, name := range []string{"nil", "empty"} {
			t.Run(string(mode)+"/"+name, func(t *testing.T) {
				cfg := fixtureConfig(t, mode, nil)
				if name == "nil" {
					cfg.Roots = nil
				} else {
					cfg.Roots = x509.NewCertPool()
				}
				cfg.DialIP = netip.Addr{}
				calls := 0
				cfg.Authorize = func(context.Context, Stage) error { calls++; return nil }
				network := transport{
					lookup: func(context.Context, string, string) ([]netip.Addr, error) {
						calls++
						return nil, errors.New("unexpected lookup without explicit trust")
					},
					dial: func(context.Context, string, string) (net.Conn, error) {
						calls++
						return nil, errors.New("unexpected dial without explicit trust")
					},
				}
				_, err := probe(context.Background(), cfg, fixtureCredential(), network)
				assertError(t, err, CodeInvalidConfig, StageValidate)
				if calls != 0 {
					t.Fatal("missing CA pool reached a network or authorization stage")
				}
			})
		}
	}
}
