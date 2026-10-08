//go:build realad

// Package adlab exercises the actual restricted ADTR LDAP transport against a
// disposable Microsoft AD DS lab. It is not full product/API acceptance.
package adlab

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

type fixture struct {
	SAM      string               `json:"sam"`
	GUID     string               `json:"guid"`
	DN       string               `json:"dn"`
	Kind     directoryassets.Kind `json:"kind"`
	Disabled bool                 `json:"disabled"`
}
type lab struct {
	LabID    string    `json:"lab_id"`
	IP       string    `json:"ip"`
	CA       string    `json:"ca"`
	Fixtures []fixture `json:"fixtures"`
}

func config(t *testing.T) (lab, ldapconnection.Config) {
	t.Helper()
	if os.Getenv("ADTR_REAL_AD_LAB") != "disposable-adtr-test-only" {
		t.Fatal("explicit disposable lab opt-in is required")
	}
	b, err := os.ReadFile(os.Getenv("ADTR_LAB_MANIFEST"))
	if err != nil {
		t.Fatal("lab manifest unavailable")
	}
	var l lab
	if json.Unmarshal(b, &l) != nil || len(l.Fixtures) != 5 {
		t.Fatal("invalid fixture manifest")
	}
	expected := map[string]directoryassets.Kind{"lab-reader": directoryassets.User, "lab-user": directoryassets.User, "lab-disabled": directoryassets.User, "lab-group": directoryassets.Group, "member01$": directoryassets.Computer}
	seen := make(map[string]bool)
	for _, f := range l.Fixtures {
		key := strings.ToLower(f.SAM)
		kind, ok := expected[key]
		if !ok || seen[key] || f.Kind != kind || f.Disabled != (key == "lab-disabled") || !regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`).MatchString(f.GUID) || !strings.HasSuffix(strings.ToLower(f.DN), ",dc=adtr,dc=test") {
			t.Fatal("exact synthetic fixture set required")
		}
		seen[key] = true
	}
	ip, err := netip.ParseAddr(l.IP)
	if err != nil || ip.String() != "192.168.77.10" {
		t.Fatal("fixed isolated lab IPv4 address required")
	}
	caBytes, err := os.ReadFile(l.CA)
	if err != nil {
		t.Fatal("lab public CA unavailable")
	}
	block, rest := pem.Decode(caBytes)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		t.Fatal("one PEM lab root required")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA || cert.CheckSignatureFrom(cert) != nil || !regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`).MatchString(l.LabID) || cert.Subject.CommonName != "ADTR disposable lab "+l.LabID || cert.NotAfter.Sub(cert.NotBefore) > 48*time.Hour {
		t.Fatal("ephemeral lab trust identity required")
	}
	digest := sha256.Sum256(cert.Raw)
	if hex.EncodeToString(digest[:]) != os.Getenv("ADTR_LAB_CA_SHA256") {
		t.Fatal("host-pinned lab root mismatch")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		t.Fatal("invalid lab public CA")
	}
	if len(os.Getenv("ADTR_LAB_READER_PASSWORD")) < 20 {
		t.Fatal("ephemeral fixture credential unavailable")
	}
	return l, ldapconnection.Config{Mode: ldapconnection.LDAPS, ServerName: "dc01.adtr.test", DialIP: ip, Roots: roots, AllowedNetworks: []netip.Prefix{netip.PrefixFrom(ip, 32)}, Authorize: func(context.Context, ldapconnection.Stage) error { return nil }}
}
func credential() ldapconnection.Credential {
	return ldapconnection.Credential{Username: "lab-reader@adtr.test", Password: []byte(os.Getenv("ADTR_LAB_READER_PASSWORD"))}
}
func probe(ctx context.Context, c ldapconnection.Config, cr ldapconnection.Credential) (ldapconnection.Result, error) {
	defer clear(cr.Password)
	return ldapconnection.Probe(ctx, c, cr)
}
func expectCode(t *testing.T, err error, want ldapconnection.Code) {
	t.Helper()
	var e *ldapconnection.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatal("unexpected sanitized LDAP outcome")
	}
}
func TestRealADTLSBindAndDirectory(t *testing.T) {
	l, c := config(t)
	for _, mode := range []ldapconnection.Mode{ldapconnection.LDAPS, ldapconnection.StartTLS} {
		t.Run(string(mode), func(t *testing.T) {
			c.Mode = mode
			root, err := probe(context.Background(), c, credential())
			if err != nil {
				t.Fatal("authenticated RootDSE failed")
			}
			if !strings.EqualFold(root.DCHostName, "dc01.adtr.test") || !strings.EqualFold(root.DefaultNamingContext, "DC=adtr,DC=test") {
				t.Fatal("unexpected lab directory identity")
			}
			d := ldapconnection.DirectoryConfig{Mode: mode, ServerName: c.ServerName, Domain: "adtr.test", DialIP: c.DialIP, Roots: c.Roots, AllowedNetworks: c.AllowedNetworks, Limits: directoryassets.Limits{MaxRows: 2000, MaxPages: 200, MaxPageEntries: 10, MaxBytes: 8 << 20, MaxCookieBytes: 4096}, Authorize: func(context.Context, ldapconnection.DirectoryStage) error { return nil }}
			obs, err := ldapconnection.ReadDirectory(context.Background(), d, credential())
			if err != nil {
				t.Fatal("real directory read failed")
			}
			if obs.Source.Pages < 2 {
				t.Fatal("real paged directory traversal was not exercised")
			}
			for _, f := range l.Fixtures {
				found := false
				for _, o := range obs.Objects {
					if o.SAMAccountName != nil && *o.SAMAccountName == f.SAM {
						if found || !strings.EqualFold(o.GUID, f.GUID) || !strings.EqualFold(o.DN, f.DN) || o.Kind != f.Kind {
							t.Fatal("directory fixture values differ from AD ground truth")
						}
						if f.Kind == directoryassets.User && (o.UserAccountControl == nil || ((*o.UserAccountControl&2) != 0) != f.Disabled) {
							t.Fatal("account status differs from AD ground truth")
						}
						found = true
					}
				}
				if !found {
					t.Fatal("expected synthetic fixture absent")
				}
			}
		})
	}
}
func TestRealADRejectsBadCredential(t *testing.T) {
	_, c := config(t)
	cr := credential()
	clear(cr.Password)
	cr.Password = []byte("deliberately-wrong-disposable-test-password")
	_, err := probe(context.Background(), c, cr)
	expectCode(t, err, ldapconnection.CodeCredentialsRejected)
}
func TestRealADRejectsDisabledAccount(t *testing.T) {
	_, c := config(t)
	cr := credential()
	cr.Username = "lab-disabled@adtr.test"
	_, err := probe(context.Background(), c, cr)
	expectCode(t, err, ldapconnection.CodeCredentialsRejected)
}
func TestRealADRejectsWrongTLSIdentity(t *testing.T) {
	_, c := config(t)
	c.ServerName = "wrong.adtr.test"
	_, err := probe(context.Background(), c, credential())
	expectCode(t, err, ldapconnection.CodeTLSHostname)
}
func TestRealADRevokedAuthority(t *testing.T) {
	_, c := config(t)
	c.Authorize = func(_ context.Context, s ldapconnection.Stage) error {
		if s == ldapconnection.StageBind {
			return errors.New("revoked")
		}
		return nil
	}
	_, err := probe(context.Background(), c, credential())
	expectCode(t, err, ldapconnection.CodeAuthorizationRevoked)
}
func TestRealADCancelled(t *testing.T) {
	_, c := config(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := probe(ctx, c, credential())
	expectCode(t, err, ldapconnection.CodeCancelled)
	if time.Since(start) > time.Second {
		t.Fatal("cancelled operation did not return promptly")
	}
}

// Cancellation after the real TLS handshake exercises disposal of an opened
// connection; this does not simulate a stalled server or prove all race cases.
func TestRealADCancelledAfterTLS(t *testing.T) {
	_, c := config(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reached := false
	c.Authorize = func(_ context.Context, s ldapconnection.Stage) error {
		if s == ldapconnection.StageBind {
			reached = true
			cancel()
		}
		return nil
	}
	_, err := probe(ctx, c, credential())
	if !reached {
		t.Fatal("real TLS handshake was not reached")
	}
	expectCode(t, err, ldapconnection.CodeCancelled)
}

func TestRealADRejectsUntrustedCA(t *testing.T) {
	_, c := config(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic negative trust setup failed")
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "unrelated synthetic negative-test root"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal("synthetic negative trust setup failed")
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal("synthetic negative trust setup failed")
	}
	c.Roots = x509.NewCertPool()
	c.Roots.AddCert(root)
	_, err = probe(context.Background(), c, credential())
	expectCode(t, err, ldapconnection.CodeTLSUntrusted)
}

// These callbacks exercise transport-stage fencing, not the product's database
// credential-use ledger or user/session authorization implementation.
func TestRealADDirectoryRevokedAfterPage(t *testing.T) {
	_, c := config(t)
	pages := 0
	d := ldapconnection.DirectoryConfig{Mode: c.Mode, ServerName: c.ServerName, Domain: "adtr.test", DialIP: c.DialIP, Roots: c.Roots, AllowedNetworks: c.AllowedNetworks, Limits: directoryassets.Limits{MaxRows: 2000, MaxPages: 200, MaxPageEntries: 10, MaxBytes: 8 << 20, MaxCookieBytes: 4096}, Authorize: func(_ context.Context, s ldapconnection.DirectoryStage) error {
		if s == ldapconnection.DirectoryPage {
			pages++
			if pages == 2 {
				return errors.New("revoked")
			}
		}
		return nil
	}}
	obs, err := ldapconnection.ReadDirectory(context.Background(), d, credential())
	if pages != 2 {
		t.Fatal("real first directory page was not completed")
	}
	expectCode(t, err, ldapconnection.CodeAuthorizationRevoked)
	if len(obs.Objects) != 0 {
		t.Fatal("partial directory observation escaped revoked authority")
	}
}

func TestRealADDirectoryCancelledAfterPage(t *testing.T) {
	_, c := config(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pages := 0
	d := ldapconnection.DirectoryConfig{Mode: c.Mode, ServerName: c.ServerName, Domain: "adtr.test", DialIP: c.DialIP, Roots: c.Roots, AllowedNetworks: c.AllowedNetworks, Limits: directoryassets.Limits{MaxRows: 2000, MaxPages: 200, MaxPageEntries: 10, MaxBytes: 8 << 20, MaxCookieBytes: 4096}, Authorize: func(_ context.Context, s ldapconnection.DirectoryStage) error {
		if s == ldapconnection.DirectoryPage {
			pages++
			if pages == 2 {
				cancel()
			}
		}
		return nil
	}}
	obs, err := ldapconnection.ReadDirectory(ctx, d, credential())
	if pages != 2 {
		t.Fatal("real first directory page was not completed")
	}
	expectCode(t, err, ldapconnection.CodeCancelled)
	if len(obs.Objects) != 0 {
		t.Fatal("partial directory observation escaped cancellation")
	}
}
