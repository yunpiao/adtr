package ldapconnection

import (
	"strings"
	"unicode/utf8"
)

// directoryBaseDN derives the only permitted search base from an ASCII DNS
// domain. A single trailing DNS root dot is accepted and discarded.
func directoryBaseDN(domain string) (string, bool) {
	if !validDNSName(domain) {
		return "", false
	}
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(domain, ".")), ".")
	return "dc=" + strings.Join(labels, ",dc="), true
}

func directoryDNWithin(dn, base string) bool {
	return directoryDNMatchesBase(dn, base, false)
}

func directoryDNEqual(dn, base string) bool {
	return directoryDNMatchesBase(dn, base, true)
}

type directoryAVA struct {
	attribute string
	value     string
}

type directoryRDN []directoryAVA

// directoryDNMatchesBase is deliberately narrower than general LDAP DN
// equality: base must consist only of single-valued DNS domain components.
// Prefix RDNs may be multi-valued, but cannot introduce another DC component.
// This excludes child-domain naming contexts from this domain's object scope.
func directoryDNMatchesBase(dn, base string, exact bool) bool {
	baseRDNs, ok := directoryParseDN(base)
	if !ok || len(baseRDNs) < 2 {
		return false
	}
	labels := make([]string, len(baseRDNs))
	for i, rdn := range baseRDNs {
		if len(rdn) != 1 || rdn[0].attribute != "dc" || !directoryDNSLabel(rdn[0].value) {
			return false
		}
		labels[i] = rdn[0].value
	}
	if !validDNSName(strings.Join(labels, ".")) {
		return false
	}
	rdns, ok := directoryParseDN(dn)
	if !ok || len(rdns) < len(baseRDNs) || (exact && len(rdns) != len(baseRDNs)) {
		return false
	}
	prefixLength := len(rdns) - len(baseRDNs)
	for _, rdn := range rdns[:prefixLength] {
		for _, ava := range rdn {
			if ava.attribute == "dc" {
				return false
			}
		}
	}
	for i, rdn := range rdns[prefixLength:] {
		// Validate ASCII before folding: Unicode lookalikes are not DNS labels.
		if len(rdn) != 1 || rdn[0].attribute != "dc" || !directoryDNSLabel(rdn[0].value) ||
			!strings.EqualFold(rdn[0].value, labels[i]) {
			return false
		}
	}
	return true
}

func directoryDNSLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := range label {
		b := label[i]
		if !directoryASCIIAlpha(b) && (b < '0' || b > '9') && b != '-' {
			return false
		}
	}
	return true
}

// directoryParseDN implements a bounded, fail-closed RFC 4514 string subset.
// It recognizes separators before decoding escapes, including paired literal
// backslashes. It validates every AVA, not just the apparent suffix. BER #hex
// values, raw controls, and non-RFC whitespace/quoted forms are unsupported;
// escaped special characters, hex octets and valid UTF-8 strings are accepted.
// This is a scope check, not an implementation of schema-dependent DN equality.
func directoryParseDN(dn string) ([]directoryRDN, bool) {
	if len(dn) == 0 || len(dn) > maxValueBytes || !utf8.ValidString(dn) {
		return nil, false
	}
	var rdns []directoryRDN
	var rdn directoryRDN
	for pos := 0; pos < len(dn); {
		start := pos
		for pos < len(dn) && dn[pos] != '=' {
			pos++
		}
		if pos == len(dn) {
			return nil, false
		}
		attribute, ok := directoryAttributeType(dn[start:pos])
		if !ok {
			return nil, false
		}
		pos++
		start = pos
		value := make([]byte, 0, 32)
		trailingSpace := false
		for pos < len(dn) && dn[pos] != ',' && dn[pos] != '+' {
			b := dn[pos]
			if b == '\\' {
				pos++
				if pos == len(dn) {
					return nil, false
				}
				b = dn[pos]
				if high, isHex := directoryHexDigit(b); isHex {
					if pos+1 == len(dn) {
						return nil, false
					}
					low, isHex := directoryHexDigit(dn[pos+1])
					if !isHex {
						return nil, false
					}
					b = high<<4 | low
					pos++
				} else if !strings.ContainsRune(" \\\"#+,;<=>", rune(b)) {
					return nil, false
				}
				trailingSpace = false
			} else {
				if b < 0x20 || b == 0x7f || strings.ContainsRune("\";<>", rune(b)) ||
					(pos == start && (b == ' ' || b == '#')) {
					return nil, false
				}
				trailingSpace = b == ' '
			}
			value = append(value, b)
			pos++
		}
		if trailingSpace || !utf8.Valid(value) {
			return nil, false
		}
		rdn = append(rdn, directoryAVA{attribute: attribute, value: string(value)})
		if pos == len(dn) || dn[pos] == ',' {
			rdns = append(rdns, rdn)
			rdn = nil
		}
		if pos < len(dn) {
			pos++
			if pos == len(dn) {
				return nil, false
			}
		}
	}
	return rdns, true
}

func directoryAttributeType(attribute string) (string, bool) {
	if len(attribute) == 0 {
		return "", false
	}
	if directoryASCIIAlpha(attribute[0]) {
		for i := range attribute {
			b := attribute[i]
			if !directoryASCIIAlpha(b) && (b < '0' || b > '9') && b != '-' {
				return "", false
			}
		}
	} else {
		// RFC 4512 numericoid: at least two decimal components, no leading zeros.
		parts := strings.Split(attribute, ".")
		if len(parts) < 2 {
			return "", false
		}
		for _, part := range parts {
			if len(part) == 0 || (len(part) > 1 && part[0] == '0') {
				return "", false
			}
			for i := range part {
				if part[i] < '0' || part[i] > '9' {
					return "", false
				}
			}
		}
	}
	attribute = strings.ToLower(attribute)
	if attribute == "0.9.2342.19200300.100.1.25" || attribute == "domaincomponent" {
		attribute = "dc"
	}
	return attribute, true
}

func directoryASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func directoryHexDigit(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
