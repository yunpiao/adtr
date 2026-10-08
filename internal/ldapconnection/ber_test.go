package ldapconnection

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

func TestReadMessageRejectsMalformedAndOversizedBER(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"indefinite", []byte{0x30, 0x80}},
		{"huge-length", []byte{0x30, 0x84, 0x7f, 0xff, 0xff, 0xff}},
		{"over-limit", []byte{0x30, 0x83, 0x01, 0x00, 0x01}},
		{"nonminimal-length", []byte{0x30, 0x81, 0x7f}},
		{"leading-zero-length", []byte{0x30, 0x82, 0, 0x80}},
		{"wrong-envelope", []byte{0x31, 0}},
		{"truncated", []byte{0x30, 0x03, 0x02, 0x01}},
		{"negative-id", element(0x30, []byte{2, 1, 0xff}, element(0x61, nil))},
		{"unsolicited", message(0, 0x61, nil)},
		{"wrong-id", message(3, 0x61, nil)},
		{"controls", element(0x30, []byte{2, 1, 2}, element(0x61, nil), element(0xa0, nil))},
		{"high-tag", element(0x30, []byte{2, 1, 2}, []byte{0x7f, 0})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := readMessage(bytes.NewReader(tc.data), 2); err == nil {
				t.Fatal("malformed BER accepted")
			}
		})
	}
}

// The reader will fail if a parser attempts to consume the oversized body.
type headerOnlyReader struct {
	header    []byte
	bodyReads int
}

func (r *headerOnlyReader) Read(p []byte) (int, error) {
	if len(r.header) == 0 {
		r.bodyReads++
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.header)
	r.header = r.header[n:]
	return n, nil
}

func TestOversizedHeaderRejectedBeforeReadingBody(t *testing.T) {
	r := &headerOnlyReader{header: []byte{0x30, 0x83, 0x01, 0x00, 0x01}}
	if _, _, err := readMessage(r, 2); !errors.Is(err, errBER) {
		t.Fatalf("got %v", err)
	}
	if r.bodyReads != 0 {
		t.Fatal("oversized body was read")
	}
}

func TestRootDSEBoundsAndRequiredShape(t *testing.T) {
	base := []byte{4, 0}
	goodHost := fixtureAttribute("dnsHostName", fixtureHost)
	goodDN := fixtureAttribute("defaultNamingContext", "DC=synthetic,DC=test")
	goodVersion := fixtureAttribute("supportedLDAPVersion", "3")
	for _, tc := range []struct {
		name  string
		attrs [][]byte
	}{
		{"missing-host", [][]byte{goodDN, goodVersion}},
		{"missing-dn", [][]byte{goodHost, goodVersion}},
		{"missing-version", [][]byte{goodHost, goodDN}},
		{"wrong-version", [][]byte{goodHost, goodDN, fixtureAttribute("supportedLDAPVersion", "2")}},
		{"duplicate-host", [][]byte{goodHost, goodDN, goodVersion, fixtureAttribute("DNSHOSTNAME", fixtureHost)}},
		{"multivalue-host", [][]byte{fixtureAttribute("dnsHostName", fixtureHost, "other.synthetic.test"), goodDN, goodVersion}},
		{"huge-value", [][]byte{goodHost, fixtureAttribute("defaultNamingContext", strings.Repeat("a", maxValueBytes+1)), goodVersion}},
		{"unsafe-value", [][]byte{goodHost, fixtureAttribute("defaultNamingContext", "DC=synthetic\nDC=test"), goodVersion}},
		{"unsupported-attribute", [][]byte{goodHost, goodDN, goodVersion, fixtureAttribute("secret", "value")}},
		{"malformed-oid", [][]byte{goodHost, goodDN, goodVersion, fixtureAttribute("supportedCapabilities", "https://secret.invalid")}},
		{"huge-oid", [][]byte{goodHost, goodDN, goodVersion, fixtureAttribute("supportedCapabilities", "1."+strings.Repeat("1", maxOIDBytes))}},
		{"too-many-capabilities", [][]byte{goodHost, goodDN, goodVersion, fixtureAttribute("supportedCapabilities", repeatedValue("1.2.3", maxCapabilities+1)...)}},
		{"too-many-versions", [][]byte{goodHost, goodDN, fixtureAttribute("supportedLDAPVersion", repeatedValue("3", 9)...)}},
		{"nested-value", [][]byte{goodHost, goodDN, goodVersion, element(0x30, octets("supportedCapabilities"), element(0x31, element(0x31, octets("1.2.3"))))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := append(append([]byte{}, base...), element(0x30, tc.attrs...)...)
			_, err := rootDSE(body)
			assertError(t, err, CodeInvalidResponse, StageSearch)
		})
	}
}

func repeatedValue(value string, count int) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = value
	}
	return values
}

func TestSearchRejectsReferralsAndExtraEntries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		packets [][]byte
		code    Code
	}{
		{"search-reference", [][]byte{message(3, 0x73, octets("ldaps://never-follow.synthetic.test"))}, CodeReferralRejected},
		{"result-referral", [][]byte{fixtureResult(3, 0x65, 10, element(0xa3, octets("ldaps://never-follow.synthetic.test")))}, CodeReferralRejected},
		{"success-with-referral", [][]byte{fixtureResult(3, 0x65, 0, element(0xa3, octets("ldaps://never-follow.synthetic.test")))}, CodeReferralRejected},
		{"no-entry", [][]byte{fixtureResult(3, 0x65, 0)}, CodeInvalidResponse},
		{"two-entries", [][]byte{message(3, 0x64, fixtureRootDSE()), message(3, 0x64, fixtureRootDSE())}, CodeInvalidResponse},
		{"oversized-frame", [][]byte{{0x30, 0x83, 1, 0, 1}}, CodeInvalidResponse},
		{"access-denied", [][]byte{fixtureResult(3, 0x65, 50)}, CodeLDAPAccessDenied},
		{"timeout", [][]byte{fixtureResult(3, 0x65, 3)}, CodeLDAPTimeout},
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
				if _, err := secured.Write(fixtureResult(2, 0x61, 0)); err != nil {
					return err
				}
				if _, err := readExpected(secured, 3, 0x63); err != nil {
					return err
				}
				for _, packet := range tc.packets {
					if _, err := secured.Write(packet); err != nil {
						return err
					}
				}
				return expectNoApplicationBytes(secured)
			})
			_, err := probe(context.Background(), fixtureConfig(t, LDAPS, roots), fixtureCredential(), network)
			assertError(t, err, tc.code, StageSearch)
		})
	}
}

func FuzzBoundedBER(f *testing.F) {
	f.Add(fixtureResult(2, 0x61, 0))
	f.Add([]byte{0x30, 0x80})
	f.Add(message(3, 0x64, fixtureRootDSE()))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxMessageBytes+5 {
			return
		}
		_, body, err := readMessage(bytes.NewReader(data), 2)
		if err == nil {
			_ = resultCode(body, StageBind)
			_, _ = rootDSE(body)
		}
	})
}
