package ldapconnection

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func fixtureDirectoryV2Config(t *testing.T, mode Mode, roots *x509.CertPool) DirectoryV2Config {
	t.Helper()
	c := fixtureDirectoryConfig(t, mode, roots)
	return DirectoryV2Config{Mode: c.Mode, ServerName: c.ServerName, Domain: c.Domain,
		DialIP: c.DialIP, Roots: c.Roots, AllowedNetworks: c.AllowedNetworks,
		Limits: c.Limits, Authorize: func(context.Context, DirectoryStage) error { return nil }}
}

func fixtureExpectDirectoryV2Request(conn net.Conn, id, pageSize int, cookie []byte) error {
	m, err := readDirectoryMessage(conn, id, maxDirectoryMessageBytes)
	if err != nil {
		return err
	}
	defer clear(m.packet)
	expected := append(octets(fixtureDirectoryBase), []byte{10, 1, 2, 10, 1, 0, 2, 1, 0, 2, 1, 3, 1, 1, 0}...)
	expected = append(expected, element(0xa1,
		element(0xa3, octets("objectClass"), octets("user")),
		element(0xa3, octets("objectClass"), octets("group")))...)
	expected = append(expected, element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl"),
		octets("objectSid"), octets("mail"), octets("description"), octets("whenCreated"))...)
	if m.tag != 0x63 || !bytes.Equal(m.body, expected) {
		return errors.New("v2 fixed search changed")
	}
	control := element(0x30, octets("1.2.840.113556.1.4.319"), []byte{1, 1, 255},
		element(4, element(0x30, fixtureDirectoryInteger(pageSize), element(4, cookie))))
	if !bytes.Equal(m.controls, control) {
		return errors.New("v2 exact critical paging control changed")
	}
	return nil
}

func fixtureDirectoryV2Body(guid byte, class string, supplemental ...[]byte) []byte {
	classes := []string{"top", class}
	if class == "computer" {
		classes = []string{"top", "person", "organizationalPerson", "user", "computer"}
	}
	attrs := [][]byte{fixtureAttribute("objectGUID", string(bytes.Repeat([]byte{guid}, 16))),
		fixtureAttribute("objectClass", classes...), fixtureAttribute("sAMAccountName", fmt.Sprintf("object-%d", guid)),
		fixtureAttribute("userAccountControl", "0")}
	attrs = append(attrs, supplemental...)
	return append(octets(fmt.Sprintf("CN=object-%d,OU=assets,DC=synthetic,DC=test", guid)), element(0x30, attrs...)...)
}

func fixtureV2Supplemental() [][]byte {
	return [][]byte{fixtureAttribute("objectSid", string([]byte{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0})),
		fixtureAttribute("mail", " Mail\x00@Synthetic.Test\t"), fixtureAttribute("description", "\x00\r\n\t🧪<script>\\\""),
		fixtureAttribute("whenCreated", "00010101000000.0Z")}
}

func fixtureReadDirectoryV2(t *testing.T, ctx context.Context, cfg DirectoryV2Config, network transport) (DirectoryV2Observation, error) {
	t.Helper()
	credential := fixtureCredential()
	defer assertDirectoryPasswordCleared(t, credential)
	return readDirectoryV2(ctx, cfg, credential, network)
}

func assertEmptyDirectoryV2Observation(t *testing.T, result DirectoryV2Observation) {
	t.Helper()
	if !reflect.DeepEqual(result, DirectoryV2Observation{}) {
		t.Error("failed v2 read exposed a partial observation")
	}
}

func TestDirectoryV2ReaderSecurePagingAndOwnedFacts(t *testing.T) {
	for _, mode := range []Mode{LDAPS, StartTLS} {
		t.Run(string(mode), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryV2Config(t, mode, roots)
			cfg.Domain = "SYNTHETIC.TEST."
			var stages []DirectoryStage
			cfg.Authorize = func(ctx context.Context, stage DirectoryStage) error {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > stepTimeout {
					t.Error("v2 authority phase has no fixed deadline")
				}
				stages = append(stages, stage)
				return nil
			}
			cookie := []byte{0, 255, 0, 128, '\\'}
			network := pipeTransport(t, mode, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, mode, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				for page := 0; page < 2; page++ {
					request, response := []byte(nil), cookie
					if page == 1 {
						request, response = cookie, nil
					}
					if err = fixtureExpectDirectoryV2Request(secured, page+4, cfg.Limits.MaxPageEntries, request); err != nil {
						return err
					}
					packets := [][]byte{fixtureDirectoryMessage(page+4, 0x64, fixtureDirectoryV2Body(byte(page+1), []string{"user", "computer"}[page], fixtureV2Supplemental()...))}
					if page == 0 {
						packets = append(packets, fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV2Body(3, "group")))
					}
					packets = append(packets, fixtureDirectoryDone(page+4, response))
					for _, packet := range packets {
						if _, err = secured.Write(packet); err != nil {
							return err
						}
						clear(packet)
					}
				}
				return expectNoApplicationBytes(secured)
			})
			network, observed := fixtureObserveDirectoryClose(network)
			result, err := fixtureReadDirectoryV2(t, context.Background(), cfg, network)
			if err != nil {
				t.Fatal(err)
			}
			defer result.Discard()
			if *observed == nil || !(*observed).closed.Load() {
				t.Fatal("v2 reader returned before I/O close")
			}
			if len(result.Objects) != 3 || result.Source.Pages != 2 || result.Source.Domain != "synthetic.test" || result.Source.DCHostName != fixtureHost || result.Source.NamingContext != fixtureDirectoryBase || result.Source.CompletedAt.Before(result.Source.StartedAt) {
				t.Fatal("v2 observation metadata or count changed")
			}
			if result.Objects[0].Base.Kind != directoryassets.User || result.Objects[1].Base.Kind != directoryassets.Group || result.Objects[2].Base.Kind != directoryassets.Computer {
				t.Fatal("v2 class inheritance changed")
			}
			public, err := directoryassets.ProjectV2(result.Objects[0])
			if err != nil || public.ObjectSID == nil || *public.ObjectSID != "S-1-5-21" || public.Mail == nil || *public.Mail != " Mail\x00@Synthetic.Test\t" || !reflect.DeepEqual(public.Description, []string{"\x00\r\n\t🧪<script>\\\""}) || public.WhenCreated == nil || *public.WhenCreated != "0001-01-01T00:00:00Z" {
				t.Fatal("v2 raw facts were lost or normalized")
			}
			missing := result.Objects[1].Supplemental
			if missing.ObjectSIDBytes != nil || missing.MailBytes != nil || missing.DescriptionBytes != nil || missing.WhenCreatedBytes != nil {
				t.Fatal("omitted facts became present")
			}
			if !reflect.DeepEqual(stages, []DirectoryStage{DirectoryValidate, DirectoryDial, DirectoryTLS, DirectoryBind, DirectoryRootDSE, DirectoryPage, DirectoryPage, DirectoryReturn}) {
				t.Fatalf("v2 authority stages: %v", stages)
			}
			retained := result.Objects[0].Supplemental.MailBytes
			result.Discard()
			result.Discard()
			if !bytes.Equal(retained, make([]byte, len(retained))) {
				t.Fatal("caller discard retained raw facts")
			}
			assertEmptyDirectoryV2Observation(t, result)
		})
	}
}

func TestDirectoryV2ReaderEmptyRepeatedAndMaximumCookies(t *testing.T) {
	for _, cookie := range [][]byte{nil, {0, 255, 0}, bytes.Repeat([]byte{0x80}, 65536)} {
		t.Run(fmt.Sprint(len(cookie)), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryV2Config(t, LDAPS, roots)
			cfg.Limits.MaxCookieBytes, cfg.Limits.MaxBytes = 65536, 200000
			pages := 1
			if len(cookie) > 0 {
				pages = 3
			}
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				for page := 0; page < pages; page++ {
					request, response := cookie, cookie
					if page == 0 {
						request = nil
					}
					if page == pages-1 {
						response = nil
					}
					if err = fixtureExpectDirectoryV2Request(secured, page+4, cfg.Limits.MaxPageEntries, request); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryDone(page+4, response)); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectoryV2(t, context.Background(), cfg, network)
			defer result.Discard()
			if err != nil || result.Objects == nil || len(result.Objects) != 0 || result.Source.Pages != pages {
				t.Fatal("v2 successful empty search or opaque cookies changed", err)
			}
		})
	}
}

func TestDirectoryV2ReaderFailureDiscardsWholeObservation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		packets [][]byte
		limits  func(*directoryassets.Limits)
		code    Code
	}{
		{"malformed-after-valid", [][]byte{fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV2Body(2, "user", fixtureAttribute("mail", "")))}, nil, CodeInvalidResponse},
		{"unsupported-time", [][]byte{fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV2Body(2, "user", fixtureAttribute("whenCreated", "20261231235960.0Z")))}, nil, CodeUnsupportedProfile},
		{"cross-domain", [][]byte{fixtureDirectoryMessage(4, 0x64, append(octets("CN=x,DC=other,DC=test"), element(0x30)...))}, nil, CodeInvalidResponse},
		{"referral", [][]byte{fixtureDirectoryMessage(4, 0x73, octets("ldaps://other.synthetic.test"))}, nil, CodeReferralRejected},
		{"missing-control", [][]byte{fixtureDirectoryMessage(4, 0x65, []byte{10, 1, 0, 4, 0, 4, 0})}, nil, CodeInvalidResponse},
		{"wrong-control", [][]byte{fixtureDirectoryMessage(4, 0x65, []byte{10, 1, 0, 4, 0, 4, 0}, element(0xa0, element(0x30, octets("1.2.3"), octets("value"))))}, nil, CodeInvalidResponse},
		{"wrong-id", [][]byte{fixtureDirectoryDone(5, nil)}, nil, CodeInvalidResponse},
		{"duplicate-guid", [][]byte{fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV2Body(1, "group")), fixtureDirectoryDone(4, nil)}, nil, CodeInvalidResponse},
		{"byte-budget", [][]byte{fixtureDirectoryDone(4, nil)}, func(l *directoryassets.Limits) { l.MaxBytes = 1 }, CodeDirectoryLimit},
		{"cookie-budget", [][]byte{fixtureDirectoryDone(4, []byte{1, 2})}, func(l *directoryassets.Limits) { l.MaxCookieBytes = 1 }, CodeDirectoryLimit},
		{"page-budget", [][]byte{fixtureDirectoryDone(4, []byte{1})}, func(l *directoryassets.Limits) { l.MaxPages = 1 }, CodeDirectoryLimit},
		{"entry-budget", [][]byte{fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV2Body(2, "user"))}, func(l *directoryassets.Limits) { l.MaxPageEntries = 1 }, CodeDirectoryLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryV2Config(t, LDAPS, roots)
			if tc.limits != nil {
				tc.limits(&cfg.Limits)
			}
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if err = fixtureExpectDirectoryV2Request(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
					return err
				}
				if _, err = secured.Write(fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV2Body(1, "user", fixtureV2Supplemental()...))); err != nil {
					return err
				}
				for _, packet := range tc.packets {
					if _, err = secured.Write(packet); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectoryV2(t, context.Background(), cfg, network)
			assertError(t, err, tc.code, StageSearch)
			assertEmptyDirectoryV2Observation(t, result)
			if strings.Contains(fmt.Sprint(err), "Synthetic") {
				t.Fatal("v2 error exposed attribute values")
			}
		})
	}
}

func TestDirectoryV2ReaderCancellationRevocationAndPanic(t *testing.T) {
	for _, phase := range []string{"reading", "deadline", "next-page", "return", "page-panic", "return-panic"} {
		t.Run(phase, func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			cfg := fixtureDirectoryV2Config(t, LDAPS, roots)
			ctx, cancel := context.WithCancel(context.Background())
			if phase == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			}
			defer cancel()
			network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
				secured, err := fixtureDirectorySession(raw, LDAPS, certificate, fixtureRootDSE())
				if err != nil {
					return err
				}
				if phase != "page-panic" {
					if err = fixtureExpectDirectoryV2Request(secured, 4, cfg.Limits.MaxPageEntries, nil); err != nil {
						return err
					}
					if _, err = secured.Write(fixtureDirectoryMessage(4, 0x64, fixtureDirectoryV2Body(1, "user", fixtureV2Supplemental()...))); err != nil {
						return err
					}
					if phase == "reading" {
						cancel()
					} else if phase != "deadline" {
						var cookie []byte
						if phase == "next-page" {
							cookie = []byte{1}
						}
						if _, err = secured.Write(fixtureDirectoryDone(4, cookie)); err != nil {
							return err
						}
					}
				}
				return expectNoApplicationBytes(secured)
			})
			network, observed := fixtureObserveDirectoryClose(network)
			pages := 0
			cfg.Authorize = func(_ context.Context, stage DirectoryStage) error {
				if stage == DirectoryPage {
					pages++
				}
				if stage == DirectoryReturn && (*observed == nil || !(*observed).closed.Load()) {
					t.Error("v2 final authority preceded close/join")
				}
				if phase == "next-page" && pages == 2 || phase == "return" && stage == DirectoryReturn {
					return errors.New("private authority denial")
				}
				if phase == "page-panic" && stage == DirectoryPage || phase == "return-panic" && stage == DirectoryReturn {
					panic("synthetic v2 authority panic")
				}
				return nil
			}
			panicked := false
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						if r != "synthetic v2 authority panic" {
							t.Errorf("unexpected panic: %v", r)
						}
					}
				}()
				result, err := fixtureReadDirectoryV2(t, ctx, cfg, network)
				code := CodeAuthorizationRevoked
				if phase == "reading" {
					code = CodeCancelled
				}
				if phase == "deadline" {
					code = CodeLDAPTimeout
				}
				assertError(t, err, code, StageSearch)
				assertEmptyDirectoryV2Observation(t, result)
			}()
			if panicked != strings.HasSuffix(phase, "panic") || *observed == nil || !(*observed).closed.Load() {
				t.Fatal("v2 panic/cancel cleanup failed")
			}
		})
	}
}

func TestDirectoryV2LimitsAndUnknownProfilesFailBeforeAuthority(t *testing.T) {
	for _, mutate := range []func(*DirectoryV2Config){
		func(c *DirectoryV2Config) { c.Limits.MaxRows = 10001 },
		func(c *DirectoryV2Config) { c.Limits.MaxPages = 101 },
		func(c *DirectoryV2Config) { c.Limits.MaxBytes = (16 << 20) + 1 },
		func(c *DirectoryV2Config) { c.Limits.MaxPageEntries = 1001 },
		func(c *DirectoryV2Config) { c.Limits.MaxCookieBytes = 65537 },
		func(c *DirectoryV2Config) { c.Limits.MaxRows = 0 },
		func(c *DirectoryV2Config) { c.Authorize = nil },
		func(c *DirectoryV2Config) { c.Domain = "synthetic.test,DC=other" },
		func(c *DirectoryV2Config) { c.Mode = "ldap" },
	} {
		cfg := fixtureDirectoryV2Config(t, LDAPS, nil)
		cfg.Authorize = func(context.Context, DirectoryStage) error {
			t.Error("invalid v2 config reached authority")
			return nil
		}
		mutate(&cfg)
		result, err := fixtureReadDirectoryV2(t, context.Background(), cfg, transport{})
		assertError(t, err, CodeInvalidConfig, StageValidate)
		assertEmptyDirectoryV2Observation(t, result)
	}
	for _, profile := range []directoryProfile{0, 3, 255} {
		cfg := fixtureDirectoryConfig(t, LDAPS, nil)
		cfg.Authorize = func(context.Context, DirectoryStage) error { t.Error("unknown profile reached authority"); return nil }
		credential := fixtureCredential()
		result, err := readDirectoryProfile(context.Background(), cfg, credential, transport{}, profile)
		assertError(t, err, CodeInvalidConfig, StageValidate)
		assertDirectoryPasswordCleared(t, credential)
		if !reflect.DeepEqual(result, directoryReadResult{}) || profile.searchPacket(fixtureDirectoryBase, 4, 1, nil) != nil {
			t.Fatal("unknown profile produced output")
		}
	}
}

func TestDirectoryV2TLSIdentityFailsBeforeBind(t *testing.T) {
	for _, mode := range []Mode{LDAPS, StartTLS} {
		t.Run(string(mode), func(t *testing.T) {
			certificate, roots := fixtureCertificate(t, "wrong.synthetic.test", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			cfg := fixtureDirectoryV2Config(t, mode, roots)
			cfg.Authorize = func(_ context.Context, stage DirectoryStage) error {
				if stage != DirectoryValidate && stage != DirectoryDial && stage != DirectoryTLS {
					t.Error("v2 invalid TLS reached credentials")
				}
				return nil
			}
			network := pipeTransport(t, mode, func(raw net.Conn) error {
				secured, err := establishFixtureTLS(raw, mode, certificate)
				if err == nil || secured == nil {
					return errors.New("v2 TLS rejection fixture failed")
				}
				return expectNoApplicationBytes(secured)
			})
			result, err := fixtureReadDirectoryV2(t, context.Background(), cfg, network)
			assertError(t, err, CodeTLSHostname, StageTLS)
			assertEmptyDirectoryV2Observation(t, result)
		})
	}
}

func TestDirectoryV2WrongRootScopeNeverPages(t *testing.T) {
	for _, tc := range []struct{ domain, capability string }{
		{"DC=other,DC=test", "1.2.840.113556.1.4.800"},
		{"DC=child,DC=synthetic,DC=test", "1.2.840.113556.1.4.800"},
		{fixtureDirectoryBase, "1.2.3"},
	} {
		certificate, roots := trustedFixture(t)
		cfg := fixtureDirectoryV2Config(t, LDAPS, roots)
		cfg.Authorize = func(_ context.Context, stage DirectoryStage) error {
			if stage == DirectoryPage || stage == DirectoryReturn {
				t.Error("invalid v2 RootDSE reached collection")
			}
			return nil
		}
		root := append(octets(""), element(0x30, fixtureAttribute("dnsHostName", fixtureHost), fixtureAttribute("defaultNamingContext", tc.domain), fixtureAttribute("supportedCapabilities", tc.capability), fixtureAttribute("supportedLDAPVersion", "3"))...)
		network := pipeTransport(t, LDAPS, func(raw net.Conn) error {
			secured, err := fixtureDirectorySession(raw, LDAPS, certificate, root)
			if err != nil {
				return err
			}
			return expectNoApplicationBytes(secured)
		})
		result, err := fixtureReadDirectoryV2(t, context.Background(), cfg, network)
		assertError(t, err, CodeInvalidResponse, StageSearch)
		assertEmptyDirectoryV2Observation(t, result)
	}
}

func TestDirectoryV2EgressRejectsBeforeDial(t *testing.T) {
	for _, dns := range []bool{false, true} {
		cfg := fixtureDirectoryV2Config(t, LDAPS, nil)
		cfg.DialIP = netip.MustParseAddr("192.0.2.1")
		if dns {
			cfg.DialIP = netip.Addr{}
		}
		cfg.Authorize = func(_ context.Context, stage DirectoryStage) error {
			if stage != DirectoryValidate && stage != DirectoryDNS {
				t.Error("v2 unsafe egress reached dial or credentials")
			}
			return nil
		}
		network := transport{lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("192.0.2.1")}, nil
		}, dial: func(context.Context, string, string) (net.Conn, error) {
			t.Error("v2 unsafe address dialed")
			return nil, errors.New("unexpected dial")
		}}
		result, err := fixtureReadDirectoryV2(t, context.Background(), cfg, network)
		stage := StageDial
		if dns {
			stage = StageDNS
		}
		assertError(t, err, CodeEgressDenied, stage)
		assertEmptyDirectoryV2Observation(t, result)
	}
}
