package domains

import (
	"bytes"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"io"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func ValidID(v string) bool  { return identifier.MatchString(v) && v != "platform" }
func ValidKey(v string) bool { return identifier.MatchString(v) && !strings.HasPrefix(v, "schedule_") }
func Revision(v string) (int64, error) {
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 1 || strconv.FormatInt(n, 10) != v {
		return 0, problem(400, "invalid_input")
	}
	return n, nil
}

func CanonicalDNS(v string) (string, error) {
	for _, b := range []byte(v) {
		if b >= 128 {
			return "", problem(400, "invalid_input")
		}
	}
	v = strings.ToLower(strings.TrimSuffix(v, "."))
	if len(v) > 253 || len(v) == 0 {
		return "", problem(400, "invalid_input")
	}
	labels := strings.Split(v, ".")
	if len(labels) < 2 {
		return "", problem(400, "invalid_input")
	}
	if _, e := netip.ParseAddr(v); e == nil {
		return "", problem(400, "invalid_input")
	}
	for _, l := range labels {
		if len(l) < 1 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", problem(400, "invalid_input")
		}
		for _, b := range []byte(l) {
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
				return "", problem(400, "invalid_input")
			}
		}
	}
	return v, nil
}
func credentialValid(username, password string) bool {
	return domainconfig.ValidCredentialPair(username, password)
}

func ValidateInput(path string, in *Input) error {
	bad := func() error { return problem(400, "invalid_input") }
	if path == "/create" || path == "/create-unconfigured" {
		var e error
		in.Domain, e = CanonicalDNS(in.Domain)
		if e != nil || !ValidKey(in.IdempotencyKey) {
			return bad()
		}
	} else {
		if !ValidID(in.DomainID) {
			return bad()
		}
		if _, e := Revision(in.ExpectedRevision); e != nil {
			return e
		}
	}
	if path == "/create" || path == "/create-unconfigured" || path == "/update" {
		var e error
		in.DCHostName, e = CanonicalDNS(in.DCHostName)
		if e != nil {
			return e
		}
		if in.Port != "389" && in.Port != "636" {
			return bad()
		}
		if in.LDAPAddr != "" {
			ip, e := netip.ParseAddr(in.LDAPAddr)
			if e != nil || ip.Zone() != "" {
				return bad()
			}
			ip = ip.Unmap()
			if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				return bad()
			}
			in.LDAPAddr = ip.String()
		}
		if (in.Username == nil) != (in.Password == nil) || path == "/create" && in.Username == nil {
			return bad()
		}
		if path == "/create-unconfigured" && (in.Username != nil || in.Password != nil || in.DomainID != "" || in.ExpectedRevision != "" || in.ConfirmDomain != "") {
			return bad()
		}
		if in.Username != nil && !credentialValid(*in.Username, *in.Password) {
			return bad()
		}
	}
	if path == "/delete" {
		var e error
		in.ConfirmDomain, e = CanonicalDNS(in.ConfirmDomain)
		if e != nil {
			return e
		}
	}
	if path == "/test" && !ValidKey(in.IdempotencyKey) {
		return bad()
	}
	return nil
}
func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{PageIdx: 1, PageSize: 20, Domains: []string{}, Statuses: []string{}}
	bad := func() (Filter, error) { return f, problem(400, "invalid_input") }
	for k, vs := range q {
		if len(vs) == 0 || k != "filterDomain" && k != "filterStatus" && len(vs) != 1 {
			return bad()
		}
		switch k {
		case "pageIdx", "pageSize", "tmSort":
			n, e := strconv.Atoi(vs[0])
			if e != nil || strconv.Itoa(n) != vs[0] {
				return bad()
			}
			switch k {
			case "pageIdx":
				if n < 1 || n > 1000000 {
					return bad()
				}
				f.PageIdx = n
			case "pageSize":
				if n != -1 && (n < 1 || n > 100) {
					return bad()
				}
				f.PageSize = n
			case "tmSort":
				if n < -1 || n > 1 {
					return bad()
				}
				f.Ascending = n == 1
			}
		case "filterDomain":
			for _, v := range vs {
				d, e := CanonicalDNS(v)
				if e != nil {
					return bad()
				}
				if !slices.Contains(f.Domains, d) {
					f.Domains = append(f.Domains, d)
				}
			}
			if len(f.Domains) > 100 {
				return bad()
			}
		case "filterStatus":
			for _, v := range vs {
				if !slices.Contains([]string{"unverified", "testing", "verified", "error"}, v) {
					return bad()
				}
				if !slices.Contains(f.Statuses, v) {
					f.Statuses = append(f.Statuses, v)
				}
			}
			if len(f.Statuses) > 4 {
				return bad()
			}
		case "filterKeyword":
			if !utf8.ValidString(vs[0]) || utf8.RuneCountInString(vs[0]) > 50 || strings.ContainsRune(vs[0], 0) {
				return bad()
			}
			f.Keyword = vs[0]
		default:
			return bad()
		}
	}
	if f.PageSize == -1 && f.PageIdx != 1 {
		return bad()
	}
	return f, nil
}
func validatePayload(raw json.RawMessage) (json.RawMessage, error) {
	if !exactObject(raw, "connectionRevision", "credentialRevision", "policyRevision", "diagnosticGeneration") {
		return nil, problem(400, "invalid_payload")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var p pinnedPayload
	if d.Decode(&p) != nil || d.Decode(&struct{}{}) != io.EOF {
		return nil, problem(400, "invalid_payload")
	}
	for _, v := range []string{p.ConnectionRevision, p.CredentialRevision, p.DiagnosticGeneration} {
		if _, e := Revision(v); e != nil {
			return nil, problem(400, "invalid_payload")
		}
	}
	if len(p.PolicyRevision) != 64 {
		return nil, problem(400, "invalid_payload")
	}
	for _, b := range []byte(p.PolicyRevision) {
		if !(b >= 'a' && b <= 'f' || b >= '0' && b <= '9') {
			return nil, problem(400, "invalid_payload")
		}
	}
	return p.json(), nil
}

// Payload/credential schemas contain only scalar values, so nested objects are
// rejected by their typed decoder after exact, duplicate-free key validation.
func exactObject(raw []byte, keys ...string) bool {
	if !utf8.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !slices.Contains(keys, key) {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(seen) != len(keys) {
		return false
	}
	_, err = d.Token()
	return err == io.EOF
}
