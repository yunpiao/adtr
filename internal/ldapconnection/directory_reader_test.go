package ldapconnection

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

const fixtureDirectoryBase = "dc=synthetic,dc=test"

func fixtureDirectoryConfig(t *testing.T, mode Mode, roots *x509.CertPool) DirectoryConfig {
	t.Helper()
	c := fixtureConfig(t, mode, roots)
	return DirectoryConfig{Mode: c.Mode, ServerName: c.ServerName, Domain: "synthetic.test",
		DialIP: c.DialIP, Roots: c.Roots, AllowedNetworks: c.AllowedNetworks,
		Limits:    directoryassets.Limits{MaxRows: 8, MaxPages: 4, MaxPageEntries: 4, MaxBytes: 65536, MaxCookieBytes: 256},
		Authorize: func(context.Context, DirectoryStage) error { return nil }}
}

// Standard-library encoding keeps the fixtures independent of directoryInteger.
func fixtureDirectoryInteger(value int) []byte {
	encoded, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func fixtureDirectoryMessage(id int, tag byte, body []byte, controls ...[]byte) []byte {
	parts := [][]byte{fixtureDirectoryInteger(id), element(tag, body)}
	parts = append(parts, controls...)
	return element(0x30, parts...)
}

func fixtureDirectoryControl(cookie []byte) []byte {
	return element(0x30, octets("1.2.840.113556.1.4.319"),
		element(4, element(0x30, fixtureDirectoryInteger(0), element(4, cookie))))
}

func fixtureDirectoryDone(id int, cookie []byte) []byte {
	return fixtureDirectoryMessage(id, 0x65, []byte{10, 1, 0, 4, 0, 4, 0}, element(0xa0, fixtureDirectoryControl(cookie)))
}

func fixtureDirectoryEntry(id int, guid byte, class string) []byte {
	classes := []string{"top", class}
	if class == "computer" {
		classes = []string{"top", "person", "organizationalPerson", "user", "computer"}
	}
	return fixtureDirectoryMessage(id, 0x64, append(octets(fmt.Sprintf("CN=object-%d,OU=assets,DC=synthetic,DC=test", guid)),
		element(0x30, fixtureAttribute("objectGUID", string(bytes.Repeat([]byte{guid}, 16))),
			fixtureAttribute("objectClass", classes...), fixtureAttribute("sAMAccountName", fmt.Sprintf("object-%d", guid)),
			fixtureAttribute("userAccountControl", "0"))...))
}

func fixtureDirectorySession(raw net.Conn, mode Mode, certificate tls.Certificate, root []byte) (*tls.Conn, error) {
	secured, err := establishFixtureTLS(raw, mode, certificate)
	if err != nil {
		return nil, err
	}
	if secured.ConnectionState().ServerName != fixtureHost {
		return nil, errors.New("directory TLS identity changed")
	}
	body, err := readExpected(secured, 2, 0x60)
	if err != nil {
		return nil, err
	}
	expected := append([]byte{2, 1, 3}, octets(fixtureCredential().Username)...)
	expected = append(expected, element(0x80, fixtureCredential().Password)...)
	if !bytes.Equal(body, expected) {
		return nil, errors.New("directory bind identity or credential changed")
	}
	if _, err = secured.Write(fixtureResult(2, 0x61, 0)); err != nil {
		return nil, err
	}
	if _, err = readExpected(secured, 3, 0x63); err != nil {
		return nil, err
	}
	if _, err = secured.Write(message(3, 0x64, root)); err != nil {
		return nil, err
	}
	if _, err = secured.Write(fixtureResult(3, 0x65, 0)); err != nil {
		return nil, err
	}
	return secured, nil
}

func fixtureExpectDirectoryRequest(conn net.Conn, id, pageSize int, cookie []byte) error {
	m, err := readDirectoryMessage(conn, id, maxDirectoryMessageBytes)
	if err != nil {
		return fmt.Errorf("read directory request: %w", err)
	}
	defer clear(m.packet)
	// Derived base; subtree; never dereference; size=0; time=3; typesOnly=false.
	expected := append(octets(fixtureDirectoryBase), []byte{10, 1, 2, 10, 1, 0, 2, 1, 0, 2, 1, 3, 1, 1, 0}...)
	expected = append(expected, element(0xa1,
		element(0xa3, octets("objectClass"), octets("user")),
		element(0xa3, octets("objectClass"), octets("group")))...)
	expected = append(expected, element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl"))...)
	if m.tag != 0x63 || !bytes.Equal(m.body, expected) {
		return errors.New("directory search base, scope, filter, limits or attributes changed")
	}
	expectedControl := element(0x30, octets("1.2.840.113556.1.4.319"), []byte{1, 1, 255},
		element(4, element(0x30, fixtureDirectoryInteger(pageSize), element(4, cookie))))
	if !bytes.Equal(m.controls, expectedControl) {
		return errors.New("directory paging request is not exact, critical, and opaque")
	}
	return nil
}

func assertDirectoryPasswordCleared(t *testing.T, credential Credential) {
	t.Helper()
	if !bytes.Equal(credential.Password, make([]byte, len(credential.Password))) {
		t.Error("directory reader retained the caller's consumed password buffer")
	}
}

func assertEmptyDirectoryObservation(t *testing.T, result DirectoryObservation) {
	t.Helper()
	if !reflect.DeepEqual(result, DirectoryObservation{}) {
		t.Error("failed directory read exposed a partial observation or source metadata")
	}
}

func fixtureReadDirectory(t *testing.T, ctx context.Context, cfg DirectoryConfig, network transport) (DirectoryObservation, error) {
	t.Helper()
	credential := fixtureCredential()
	defer assertDirectoryPasswordCleared(t, credential)
	return readDirectory(ctx, cfg, credential, network)
}

func TestDirectoryReaderSecurePagingAndFixedRequests(t *testing.T) {
	for _, mode := range []Mode{LDAPS, StartTLS} {
		t.Run(string(mode), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, mode, roots)
			cfg.Domain = "SYNTHETIC.TEST."
			cfg.Limits.MaxPageEntries = 1000
			var stages []DirectoryStage
			cfg.Authorize = func(ctx context.Context, stage DirectoryStage) error {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > stepTimeout {
					t.Error("directory authority callback is not time bounded")
				}
				stages = append(stages, stage)
				return nil
			}
			cookie := []byte{0, 255, 128, 'a', 0, '\\'}
			network := pipeTransport(t, mode, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, mode, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if err = fixtureExpectDirectoryRequest(secured, 4, 1000, nil); err != nil {
					return err
				}
				for _, packet := range [][]byte{fixtureDirectoryEntry(4, 1, "user"), fixtureDirectoryEntry(4, 2, "group"), fixtureDirectoryDone(4, cookie)} {
					if _, err = secured.Write(packet); err != nil {
						return err
					}
				}
				if err = fixtureExpectDirectoryRequest(secured, 5, 1000, cookie); err != nil {
					return err
				}
				for _, packet := range [][]byte{fixtureDirectoryEntry(5, 3, "computer"), fixtureDirectoryDone(5, nil)} {
					if _, err = secured.Write(packet); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			started := time.Now()
			result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Objects) != 3 || result.Objects[0].Kind != directoryassets.User || result.Objects[1].Kind != directoryassets.Group || result.Objects[2].Kind != directoryassets.Computer {
				t.Fatalf("wrong directory object kinds: %+v", result.Objects)
			}
			if result.Objects[0].GUID != "01010101-0101-0101-0101-010101010101" || result.Objects[0].SAMAccountName == nil || *result.Objects[0].SAMAccountName != "object-1" ||
				result.Objects[0].UserAccountControl == nil || *result.Objects[0].UserAccountControl != 0 {
				t.Error("directory fields or present zero values were lost")
			}
			s := result.Source
			if s.Pages != 2 || s.ServerName != fixtureHost || s.DCHostName != fixtureHost || s.Domain != "synthetic.test" || s.NamingContext != fixtureDirectoryBase ||
				s.StartedAt.Before(started) || s.CompletedAt.Before(s.StartedAt) || s.CompletedAt.After(time.Now()) || s.ElapsedMilliseconds < 0 {
				t.Fatalf("wrong directory source metadata: %+v", s)
			}
			if !reflect.DeepEqual(stages, []DirectoryStage{DirectoryValidate, DirectoryDial, DirectoryTLS, DirectoryBind, DirectoryRootDSE, DirectoryPage, DirectoryPage, DirectoryReturn}) {
				t.Fatalf("wrong directory authorization sequence: %v", stages)
			}
		})
	}
}

func TestDirectoryReaderOpaqueRepeatedAndMaximumCookies(t *testing.T) {
	for _, cookie := range [][]byte{{0, 255, 0, 128}, bytes.Repeat([]byte{255}, 65536)} {
		t.Run(fmt.Sprint(len(cookie)), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			cfg.Limits.MaxCookieBytes, cfg.Limits.MaxBytes = len(cookie), 3*len(cookie)
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				for page := 0; page < 3; page++ {
					request, response := cookie, cookie
					if page == 0 {
						request = nil
					}
					if page == 2 {
						response = nil
					}
					if err = fixtureExpectDirectoryRequest(secured, page+4, cfg.Limits.MaxPageEntries, request); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryDone(page+4, response)); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
			if err != nil || len(result.Objects) != 0 || result.Source.Pages != 3 {
				t.Fatalf("repeated opaque cookie was not completed: pages=%d, err=%v", result.Source.Pages, err)
			}
		})
	}
}

func TestDirectoryReaderTerminalZeroResults(t *testing.T) {
	certificate, roots := trustedFixture(t)
	cfg := fixtureDirectoryConfig(t, LDAPS, roots)
	network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
		secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
		if err != nil {
			return err
		}
		if err = fixtureExpectDirectoryRequest(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
			return err
		}
		if _, err = secured.Write(fixtureDirectoryDone(4, nil)); err != nil {
			return err
		}
		return expectNoApplicationBytes(secured)
	})
	result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
	if err != nil || len(result.Objects) != 0 || result.Source.Pages != 1 {
		t.Fatalf("terminal empty search failed: %+v, %v", result, err)
	}
}

func TestDirectoryReaderRejectsDuplicateGUIDsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits func(*directoryassets.Limits)
		pages  [][][]byte
	}{
		{"duplicate-same-page", nil, [][][]byte{{fixtureDirectoryEntry(4, 1, "user"), fixtureDirectoryEntry(4, 1, "group"), fixtureDirectoryDone(4, nil)}}},
		{"duplicate-across-pages", nil, [][][]byte{{fixtureDirectoryEntry(4, 1, "user"), fixtureDirectoryDone(4, []byte{1})}, {fixtureDirectoryEntry(5, 1, "computer"), fixtureDirectoryDone(5, nil)}}},
		{"pages", func(l *directoryassets.Limits) { l.MaxPages = 1 }, [][][]byte{{fixtureDirectoryEntry(4, 1, "user"), fixtureDirectoryDone(4, []byte{1})}}},
		{"rows", func(l *directoryassets.Limits) { l.MaxRows = 1 }, [][][]byte{{fixtureDirectoryEntry(4, 1, "user"), fixtureDirectoryDone(4, []byte{1})}, {fixtureDirectoryEntry(5, 2, "group"), fixtureDirectoryDone(5, nil)}}},
		{"object-bytes", func(l *directoryassets.Limits) { l.MaxBytes = 1 }, [][][]byte{{fixtureDirectoryEntry(4, 1, "user"), fixtureDirectoryDone(4, nil)}}},
		{"cookie-bytes", func(l *directoryassets.Limits) { l.MaxBytes = 1 }, [][][]byte{{fixtureDirectoryDone(4, []byte{1, 2})}}},
		{"cumulative-cookie-bytes", func(l *directoryassets.Limits) { l.MaxBytes = 1 }, [][][]byte{{fixtureDirectoryDone(4, []byte{1})}, {fixtureDirectoryDone(5, []byte{1})}}},
		{"cookie-length", func(l *directoryassets.Limits) { l.MaxCookieBytes = 1 }, [][][]byte{{fixtureDirectoryDone(4, []byte{1, 2})}}},
		{"page-entries", func(l *directoryassets.Limits) { l.MaxPageEntries = 1 }, [][][]byte{{fixtureDirectoryEntry(4, 1, "user"), fixtureDirectoryEntry(4, 2, "group")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			if tc.limits != nil {
				tc.limits(&cfg.Limits)
			}
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				for page, packets := range tc.pages {
					var cookie []byte
					if page > 0 {
						cookie = []byte{1}
					}
					if err = fixtureExpectDirectoryRequest(secured, page+4, cfg.Limits.MaxPageEntries, cookie); err != nil {
						return err
					}
					for _, packet := range packets {
						if _, err = secured.Write(packet); err != nil {
							return err
						}
					}
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
			assertError(t, err, CodeInvalidResponse, StageSearch)
			assertEmptyDirectoryObservation(t, result)
		})
	}
}

func TestDirectoryReaderRejectsControlsAndProtocolViolations(t *testing.T) {
	control := fixtureDirectoryControl(nil)
	value := element(4, element(0x30, fixtureDirectoryInteger(0), octets("")))
	done := func(controls ...[]byte) []byte {
		return fixtureDirectoryMessage(4, 0x65, []byte{10, 1, 0, 4, 0, 4, 0}, controls...)
	}
	for _, tc := range []struct {
		name   string
		packet []byte
		code   Code
	}{
		{"missing-control", done(), CodeInvalidResponse},
		{"empty-control-list", done(element(0xa0)), CodeInvalidResponse},
		{"unexpected-control", done(element(0xa0, element(0x30, octets("1.2.3"), value))), CodeInvalidResponse},
		{"duplicate-control", done(element(0xa0, control, control)), CodeInvalidResponse},
		{"duplicate-control-envelope", done(element(0xa0, control), element(0xa0, control)), CodeInvalidResponse},
		{"invalid-criticality", done(element(0xa0, element(0x30, octets(pagedResultsOID), []byte{1, 1, 1}, value))), CodeInvalidResponse},
		{"missing-control-value", done(element(0xa0, element(0x30, octets(pagedResultsOID)))), CodeInvalidResponse},
		{"malformed-control-value", done(element(0xa0, element(0x30, octets(pagedResultsOID), octets("broken")))), CodeInvalidResponse},
		{"negative-size", done(element(0xa0, element(0x30, octets(pagedResultsOID), element(4, element(0x30, []byte{2, 1, 255}, octets("")))))), CodeInvalidResponse},
		{"missing-cookie", done(element(0xa0, element(0x30, octets(pagedResultsOID), element(4, element(0x30, fixtureDirectoryInteger(0)))))), CodeInvalidResponse},
		{"trailing-page-value", done(element(0xa0, element(0x30, octets(pagedResultsOID), element(4, element(0x30, fixtureDirectoryInteger(0), octets(""), octets("extra")))))), CodeInvalidResponse},
		{"wrong-message-id", fixtureDirectoryDone(5, nil), CodeInvalidResponse},
		{"wrong-entry-id", fixtureDirectoryEntry(5, 1, "user"), CodeInvalidResponse},
		{"unsolicited-operation", fixtureDirectoryMessage(4, 0x78, nil), CodeInvalidResponse},
		{"entry-control", fixtureDirectoryMessage(4, 0x64, append(octets("CN=x,"+fixtureDirectoryBase), element(0x30)...), element(0xa0, control)), CodeInvalidResponse},
		{"search-reference", fixtureDirectoryMessage(4, 0x73, octets("ldaps://other.synthetic.test")), CodeReferralRejected},
		{"result-referral", fixtureDirectoryMessage(4, 0x65, []byte{10, 1, 10, 4, 0, 4, 0}, element(0xa0, control)), CodeReferralRejected},
		{"ldap-timeout", fixtureDirectoryMessage(4, 0x65, []byte{10, 1, 3, 4, 0, 4, 0}, element(0xa0, control)), CodeLDAPTimeout},
		{"indefinite-length", []byte{0x30, 0x80}, CodeInvalidResponse},
		{"oversized-message-before-body", []byte{0x30, 0x83, 2, 0, 1}, CodeInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if err = fixtureExpectDirectoryRequest(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
					return err
				}
				if _, err = secured.Write(tc.packet); err != nil {
					return err
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
			assertError(t, err, tc.code, StageSearch)
			assertEmptyDirectoryObservation(t, result)
		})
	}
}

func TestDirectoryReaderRefusesWrongDomainOrNonADBeforePaging(t *testing.T) {
	for _, tc := range []struct{ name, domain, capability string }{
		{"wrong-domain", "DC=other,DC=test", "1.2.840.113556.1.4.800"},
		{"child-domain", "DC=child,DC=synthetic,DC=test", "1.2.840.113556.1.4.800"},
		{"non-ad", "DC=synthetic,DC=test", "1.2.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			cfg.Authorize = func(_ context.Context, stage DirectoryStage) error {
				if stage == DirectoryPage || stage == DirectoryReturn {
					t.Error("directory search reached authority after rejected RootDSE")
				}
				return nil
			}
			root := append(octets(""), element(0x30, fixtureAttribute("dnsHostName", fixtureHost), fixtureAttribute("defaultNamingContext", tc.domain),
				fixtureAttribute("supportedCapabilities", tc.capability), fixtureAttribute("supportedLDAPVersion", "3"))...)
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, root)
				if err != nil {
					return err
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
			assertError(t, err, CodeInvalidResponse, StageSearch)
			assertEmptyDirectoryObservation(t, result)
		})
	}
}

type directoryObservedConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *directoryObservedConn) Close() error { c.closed.Store(true); return c.Conn.Close() }

func fixtureObserveDirectoryClose(network transport) (transport, **directoryObservedConn) {
	var observed *directoryObservedConn
	dial := network.dial
	network.dial = func(ctx context.Context, kind, address string) (net.Conn, error) {
		conn, err := dial(ctx, kind, address)
		if err != nil {
			return nil, err
		}
		observed = &directoryObservedConn{Conn: conn}
		return observed, nil
	}
	return network, &observed
}

func TestDirectoryReaderReauthorizesEveryPageAndAfterClose(t *testing.T) {
	for _, denied := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(denied), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			pages := 0
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				for page := 1; page < denied && page <= 2; page++ {
					var request, response []byte
					if page == 1 {
						response = []byte{1}
					} else {
						request = []byte{1}
					}
					if err = fixtureExpectDirectoryRequest(secured, page+3, cfg.Limits.MaxPageEntries, request); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryEntry(page+3, byte(page), "user")); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryDone(page+3, response)); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			network, observed := fixtureObserveDirectoryClose(network)
			cfg.Authorize = func(_ context.Context, stage DirectoryStage) error {
				if stage == DirectoryPage {
					pages++
					if pages == denied {
						return errors.New("private revoked grant revision")
					}
				}
				if stage == DirectoryReturn {
					if *observed == nil || !(*observed).closed.Load() {
						t.Error("final authorization preceded connection cleanup")
					}
					return errors.New("private publication revocation")
				}
				return nil
			}
			result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
			assertError(t, err, CodeAuthorizationRevoked, StageSearch)
			assertEmptyDirectoryObservation(t, result)
			if pages != min(denied, 2) || *observed == nil || !(*observed).closed.Load() {
				t.Fatal("directory authority or connection cleanup was skipped")
			}
			if strings.Contains(fmt.Sprint(err), "private") {
				t.Error("directory authority error leaked private context")
			}
		})
	}
}

func TestDirectoryReaderCancellationAndDeadlineCloseIO(t *testing.T) {
	for _, timed := range []bool{false, true} {
		t.Run(fmt.Sprint(timed), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			ctx, cancel := context.WithCancel(context.Background())
			if timed {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			}
			defer cancel()
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if err = fixtureExpectDirectoryRequest(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
					return err
				}
				if _, err = secured.Write(fixtureDirectoryEntry(4, 1, "user")); err != nil {
					return err
				}
				if !timed {
					cancel()
				}
				return expectNoApplicationBytes(secured)
			})
			network, observed := fixtureObserveDirectoryClose(network)
			started := time.Now()
			result, err := fixtureReadDirectory(t, ctx, cfg, network)
			code := CodeCancelled
			if timed {
				code = CodeLDAPTimeout
			}
			assertError(t, err, code, StageSearch)
			assertEmptyDirectoryObservation(t, result)
			if time.Since(started) > time.Second || *observed == nil || !(*observed).closed.Load() {
				t.Error("cancelled directory read did not promptly close its I/O")
			}
		})
	}
}

func TestDirectoryReaderPanicClosesIOAndClearsPassword(t *testing.T) {
	for _, stage := range []DirectoryStage{DirectoryPage, DirectoryReturn} {
		t.Run(string(stage), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if stage == DirectoryReturn {
					if err = fixtureExpectDirectoryRequest(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryDone(4, nil)); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			network, observed := fixtureObserveDirectoryClose(network)
			cfg.Authorize = func(_ context.Context, got DirectoryStage) error {
				if got == stage {
					panic("synthetic authority panic")
				}
				return nil
			}
			credential := fixtureCredential()
			func() {
				defer func() {
					if got := recover(); got != "synthetic authority panic" {
						t.Errorf("unexpected panic behavior: %v", got)
					}
				}()
				_, _ = readDirectory(context.Background(), cfg, credential, network)
			}()
			assertDirectoryPasswordCleared(t, credential)
			if *observed == nil || !(*observed).closed.Load() {
				t.Error("directory panic propagated before closing I/O")
			}
		})
	}
}

func TestDirectoryReaderInvalidConfigNeverUsesAuthorityOrTransport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DirectoryConfig)
	}{
		{"no-directory-authority", func(c *DirectoryConfig) { c.Authorize = nil }},
		{"invalid-domain", func(c *DirectoryConfig) { c.Domain = "synthetic.test,DC=other" }},
		{"single-label-domain", func(c *DirectoryConfig) { c.Domain = "synthetic" }},
		{"plaintext", func(c *DirectoryConfig) { c.Mode = "ldap" }},
		{"no-roots", func(c *DirectoryConfig) { c.Roots = nil }},
		{"zero-rows", func(c *DirectoryConfig) { c.Limits.MaxRows = 0 }},
		{"zero-pages", func(c *DirectoryConfig) { c.Limits.MaxPages = 0 }},
		{"zero-page-entries", func(c *DirectoryConfig) { c.Limits.MaxPageEntries = 0 }},
		{"excess-page-entries", func(c *DirectoryConfig) { c.Limits.MaxPageEntries = 1001 }},
		{"zero-bytes", func(c *DirectoryConfig) { c.Limits.MaxBytes = 0 }},
		{"zero-cookie", func(c *DirectoryConfig) { c.Limits.MaxCookieBytes = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fixtureDirectoryConfig(t, LDAPS, nil)
			cfg.Authorize = func(context.Context, DirectoryStage) error {
				t.Error("invalid directory configuration reached authority")
				return nil
			}
			tc.mutate(&cfg)
			result, err := fixtureReadDirectory(t, context.Background(), cfg, transport{})
			assertError(t, err, CodeInvalidConfig, StageValidate)
			assertEmptyDirectoryObservation(t, result)
		})
	}
}

func TestDirectoryReaderAdmissionCancellationAndDenial(t *testing.T) {
	for _, name := range []string{"nil-context", "cancelled-context", "denied", "callback-cancelled", "callback-panic"} {
		t.Run(name, func(t *testing.T) {
			cfg := fixtureDirectoryConfig(t, LDAPS, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			code := CodeAuthorizationRevoked
			called := false
			cfg.Authorize = func(callback context.Context, stage DirectoryStage) error {
				called = true
				if stage != DirectoryValidate {
					t.Error("admission failure reached another stage")
				}
				if name == "callback-panic" {
					panic("synthetic admission panic")
				}
				if name == "callback-cancelled" {
					cancel()
					<-callback.Done()
				}
				return errors.New("private admission denial")
			}
			if name == "nil-context" {
				ctx = nil
				code = CodeInvalidConfig
			}
			if name == "cancelled-context" {
				cancel()
				code = CodeCancelled
			}
			if name == "callback-cancelled" {
				code = CodeCancelled
			}
			credential := fixtureCredential()
			defer assertDirectoryPasswordCleared(t, credential)
			if name == "callback-panic" {
				defer func() {
					if got := recover(); got != "synthetic admission panic" {
						t.Errorf("unexpected admission panic: %v", got)
					}
				}()
				_, _ = readDirectory(ctx, cfg, credential, transport{})
				return
			}
			result, err := readDirectory(ctx, cfg, credential, transport{})
			assertError(t, err, code, StageValidate)
			assertEmptyDirectoryObservation(t, result)
			if called != (name == "denied" || name == "callback-cancelled") {
				t.Error("invalid admission callback sequence")
			}
		})
	}
}

func TestDirectoryReaderRootAuthorityDenialSendsNoSearch(t *testing.T) {
	certificate, roots := trustedFixture(t)
	cfg := fixtureDirectoryConfig(t, LDAPS, roots)
	cfg.Authorize = func(_ context.Context, stage DirectoryStage) error {
		if stage == DirectoryRootDSE {
			return errors.New("private root authorization denial")
		}
		if stage == DirectoryPage || stage == DirectoryReturn {
			t.Error("RootDSE denial reached a later authority stage")
		}
		return nil
	}
	network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
		secured, err := establishFixtureTLS(raw, LDAPS, certificate)
		if err != nil {
			return err
		}
		if _, err = readExpected(secured, 2, 0x60); err != nil {
			return err
		}
		if _, err = secured.Write(fixtureResult(2, 0x61, 0)); err != nil {
			return err
		}
		return expectNoApplicationBytes(secured)
	})
	result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
	assertError(t, err, CodeAuthorizationRevoked, StageSearch)
	assertEmptyDirectoryObservation(t, result)
}

func TestDirectoryReaderCancellationInsidePageAndFinalAuthority(t *testing.T) {
	for _, stop := range []DirectoryStage{DirectoryPage, DirectoryReturn} {
		t.Run(string(stop), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg.Authorize = func(callback context.Context, stage DirectoryStage) error {
				if stage == stop {
					cancel()
					<-callback.Done()
				}
				return nil
			}
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if stop == DirectoryReturn {
					if err = fixtureExpectDirectoryRequest(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryEntry(4, 1, "user")); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryDone(4, nil)); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectory(t, ctx, cfg, network)
			assertError(t, err, CodeCancelled, StageSearch)
			assertEmptyDirectoryObservation(t, result)
		})
	}
}

func TestDirectoryReaderRejectsInvalidEntriesWithoutPartialResults(t *testing.T) {
	guid := fixtureAttribute("objectGUID", string(bytes.Repeat([]byte{2}, 16)))
	classes := fixtureAttribute("objectClass", "top", "user")
	body := func(dn string, attrs ...[]byte) []byte { return append(octets(dn), element(0x30, attrs...)...) }
	dn := "CN=second," + fixtureDirectoryBase
	for _, tc := range []struct {
		name      string
		body      []byte
		needsDone bool
	}{
		{"different-domain", body("CN=x,DC=other,DC=test", guid, classes), false},
		{"child-domain", body("CN=x,DC=child,"+fixtureDirectoryBase, guid, classes), false},
		{"malformed-dn", body("CN=x,,"+fixtureDirectoryBase, guid, classes), false},
		{"escaped-comma-domain-spoof", body("CN=x\\,DC=synthetic,DC=test", guid, classes), false},
		{"child-domain-alias", body("CN=x,domainComponent=child,"+fixtureDirectoryBase, guid, classes), false},
		{"oversized-guid", body(dn, fixtureAttribute("objectGUID", strings.Repeat("x", 17)), classes), false},
		{"short-guid", body(dn, fixtureAttribute("objectGUID", strings.Repeat("x", 15)), classes), true},
		{"unknown-attribute", body(dn, guid, classes, fixtureAttribute("mail", "x@synthetic.test")), false},
		{"ranged-attribute", body(dn, guid, classes, fixtureAttribute("sAMAccountName;range=0-*", "x")), false},
		{"duplicate-attribute", body(dn, guid, classes, fixtureAttribute("OBJECTGUID", string(bytes.Repeat([]byte{3}, 16)))), true},
		{"duplicate-class", body(dn, guid, fixtureAttribute("objectClass", "user", "USER")), true},
		{"too-many-values", body(dn, guid, classes, fixtureAttribute("sAMAccountName", "a", "b")), false},
		{"oversized-value", body(dn, guid, classes, fixtureAttribute("sAMAccountName", strings.Repeat("x", 1025))), false},
		{"uac-overflow", body(dn, guid, classes, fixtureAttribute("userAccountControl", "4294967296")), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryConfig(t, LDAPS, roots)
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if err = fixtureExpectDirectoryRequest(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
					return err
				}
				if _, err = secured.Write(fixtureDirectoryEntry(4, 1, "user")); err != nil {
					return err
				}
				if _, err = secured.Write(fixtureDirectoryMessage(4, 0x64, tc.body)); err != nil {
					return err
				}
				if tc.needsDone {
					if _, err = secured.Write(fixtureDirectoryDone(4, nil)); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectory(t, context.Background(), cfg, network)
			assertError(t, err, CodeInvalidResponse, StageSearch)
			assertEmptyDirectoryObservation(t, result)
		})
	}
}
