package domainconfig_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
)

// Each expectation freezes the pre-extraction F01 contract. Checking the F01
// entry point too catches wiring or normalization changes outside this package.
func TestCredentialPairMatchesF01CompatibilityContract(t *testing.T) {
	tests := []struct {
		name, username, password string
		valid                    bool
	}{
		{"downlevel", `OTHER\reader`, "x", true},
		{"UPN alternate suffix", "reader@alternate.test", "x", true},
		{"UPN uppercase trailing dot", "reader@ALTERNATE.TEST.", "x", true},
		{"password edge spaces", `OTHER\reader`, " secret ", true},
		{"one space password", `OTHER\reader`, " ", true},
		{"password controls preserved", `OTHER\reader`, "\t\n\r\x01\x7f", true},
		{"unicode password", "reader@alternate.test", strings.Repeat("密", 50), true},
		{"maximum password bytes", "reader@alternate.test", strings.Repeat("🔒", 50), true},
		{"maximum username runes", `D\` + strings.Repeat("u", 48), "x", true},
		{"unicode username", `部门\读者`, "x", true},
		{"unicode UPN local part", "读者@alternate.test", "x", true},
		{"internal Unicode whitespace F01 compatibility", `D\a` + "\u00a0" + "b", "x", true},
		{"format character F01 compatibility", `D\a` + "\u200b" + "b", "x", true},
		{"empty username", "", "x", false},
		{"unqualified", "reader", "x", false},
		{"missing downlevel domain", `\reader`, "x", false},
		{"missing downlevel user", `DOMAIN\`, "x", false},
		{"duplicate backslash", `DOMAIN\\reader`, "x", false},
		{"leading space", " user@example.test", "x", false},
		{"trailing space", `DOMAIN\reader `, "x", false},
		{"leading Unicode space", "\u00a0" + `DOMAIN\reader`, "x", false},
		{"trailing Unicode space", `DOMAIN\reader` + "\u3000", "x", false},
		{"control username", "D\\re\tader", "x", false},
		{"delete control username", "D\\re\x7fader", "x", false},
		{"username NUL", "D\\a\x00b", "x", false},
		{"invalid UTF8 username", "D\\" + string([]byte{0xff}), "x", false},
		{"oversize username runes", `D\` + strings.Repeat("u", 49), "x", false},
		{"oversize username bytes", `D\` + strings.Repeat("🔒", 50), "x", false},
		{"local UPN suffix", "user@localhost", "x", false},
		{"duplicate at", "a@b@example.test", "x", false},
		{"mixed separators", `a\b@c.test`, "x", false},
		{"downlevel user space", `DOMAIN\a b`, "x", false},
		{"downlevel domain space", `DOM AIN\reader`, "x", false},
		{"downlevel slash", `DOMAIN\a/b`, "x", false},
		{"downlevel colon", `DOM:AIN\reader`, "x", false},
		{"UPN empty user", "@example.test", "x", false},
		{"UPN empty suffix", "reader@", "x", false},
		{"UPN user space", "a b@example.test", "x", false},
		{"UPN user slash", "a/b@example.test", "x", false},
		{"UPN user colon", "a:b@example.test", "x", false},
		{"UPN suffix space", "reader@ example.test", "x", false},
		{"UPN suffix duplicate dot", "reader@example.test..", "x", false},
		{"UPN suffix leading dot", "reader@.example.test", "x", false},
		{"UPN suffix leading hyphen", "reader@-a.example", "x", false},
		{"UPN suffix trailing hyphen", "reader@a-.example", "x", false},
		{"UPN suffix underscore", "reader@a_1.example", "x", false},
		{"UPN suffix Unicode fold", "reader@K.example", "x", false},
		{"UPN suffix Unicode", "reader@é.example", "x", false},
		{"UPN suffix IPv4", "reader@127.0.0.1", "x", false},
		{"empty password", "reader@alternate.test", "", false},
		{"password NUL", "reader@alternate.test", "a\x00b", false},
		{"invalid UTF8 password", "reader@alternate.test", string([]byte{0xff}), false},
		{"oversize password runes", "reader@alternate.test", strings.Repeat("a", 51), false},
		{"oversize password bytes", "reader@alternate.test", strings.Repeat("🔒", 51), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := domainconfig.ValidCredentialPair(tc.username, tc.password); got != tc.valid {
				t.Fatalf("shared validator accepted=%v, want %v", got, tc.valid)
			}
			username, password := tc.username, tc.password
			in := domains.Input{
				DomainID: "synthetic-domain", ExpectedRevision: "1",
				DCHostName: "dc.example.test", Port: "636",
				Username: &username, Password: &password,
			}
			if got := domains.ValidateInput("/update", &in) == nil; got != tc.valid {
				t.Fatalf("F01 validator accepted=%v, want %v", got, tc.valid)
			}
			if username != tc.username || password != tc.password {
				t.Fatal("validation changed credential bytes")
			}
		})
	}
}

func FuzzCredentialPairF01Equivalence(f *testing.F) {
	f.Add(`DOMAIN\reader`, " synthetic secret ")
	f.Add("reader@alternate.test", strings.Repeat("🔒", 50))
	f.Add("reader@K.example", "x")
	f.Add("reader@EXAMPLE.TEST.", "\t\n")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, username, password string) {
		if len(username) > 201 || len(password) > 201 {
			return
		}
		originalUsername, originalPassword := username, password
		in := domains.Input{
			DomainID: "synthetic-domain", ExpectedRevision: "1",
			DCHostName: "dc.example.test", Port: "636",
			Username: &username, Password: &password,
		}
		want := legacyF01CredentialValid(username, password)
		if got := domainconfig.ValidCredentialPair(username, password); got != want {
			t.Fatal("shared validator differs from original F01")
		}
		if got := domains.ValidateInput("/update", &in) == nil; got != want {
			t.Fatal("F01 input validation differs from original F01")
		}
		if username != originalUsername || password != originalPassword {
			t.Fatal("validation changed credential bytes")
		}
	})
}

// This is the F01 credential predicate from before the shared-validator
// extraction. Keep it independent of ValidCredentialPair: the production F01
// wrapper may delegate to that function without making fuzz parity tautological.
// CanonicalDNS remains F01's independent DNS validation implementation.
func legacyF01CredentialValid(username, password string) bool {
	for _, v := range []string{username, password} {
		if !utf8.ValidString(v) || len(v) > 200 || utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 50 || strings.ContainsRune(v, 0) {
			return false
		}
	}
	if strings.TrimSpace(username) != username {
		return false
	}
	for _, r := range username {
		if unicode.IsControl(r) {
			return false
		}
	}
	if strings.Count(username, `\`) == 1 && !strings.Contains(username, "@") {
		a, b, _ := strings.Cut(username, `\`)
		return a != "" && b != "" && !strings.ContainsAny(a+b, " /:")
	}
	if strings.Count(username, "@") == 1 && !strings.Contains(username, `\`) {
		a, b, _ := strings.Cut(username, "@")
		_, e := domains.CanonicalDNS(b)
		return a != "" && !strings.ContainsAny(a, " /:") && e == nil
	}
	return false
}
