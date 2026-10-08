package domainconfig

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidCredentialPair applies the existing F01 local compatibility contract to
// both domain connection and operation-account credentials. These provisional
// 1–50 codepoint, 200-byte bounds do not describe every AD-supported credential.
// Password bytes are not trimmed, normalized or checked for local complexity.
func ValidCredentialPair(username, password string) bool {
	for _, value := range []string{username, password} {
		if !utf8.ValidString(value) || len(value) > 200 || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 50 || strings.ContainsRune(value, 0) {
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
		domain, user, _ := strings.Cut(username, `\`)
		return domain != "" && user != "" && !strings.ContainsAny(domain+user, " /:")
	}
	if strings.Count(username, "@") == 1 && !strings.Contains(username, `\`) {
		user, suffix, _ := strings.Cut(username, "@")
		_, ok := normalizeDNS(suffix)
		return user != "" && !strings.ContainsAny(user, " /:") && ok
	}
	return false
}
