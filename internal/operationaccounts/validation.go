package operationaccounts

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/domainconfig"
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
func validLabel(v string) bool {
	if !utf8.ValidString(v) || len(v) > 200 || utf8.RuneCountInString(v) > 50 || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func ValidateInput(path string, in *Input) error {
	bad := func() error { return problem(400, "invalid_input") }
	if !ValidKey(in.IdempotencyKey) {
		return bad()
	}
	switch path {
	case "/create":
		if !ValidID(in.DomainID) {
			return bad()
		}
		if _, e := Revision(in.ExpectedDomainRevision); e != nil {
			return e
		}
		if in.Username == nil || in.Password == nil {
			return bad()
		}
	case "/update", "/delete":
		if !ValidID(in.AccountID) {
			return bad()
		}
		if _, e := Revision(in.ExpectedRevision); e != nil {
			return e
		}
	default:
		return bad()
	}
	if path == "/delete" && in.ConfirmAccountID != in.AccountID {
		return problem(400, "account_confirmation_mismatch")
	}
	if in.Label != nil && !validLabel(*in.Label) {
		return bad()
	}
	if (in.Username == nil) != (in.Password == nil) {
		return bad()
	}
	if in.Username != nil && !domainconfig.ValidCredentialPair(*in.Username, *in.Password) {
		return bad()
	}
	return nil
}
func canonicalDNS(v string) (string, error) {
	for _, b := range []byte(v) {
		if b >= 128 {
			return "", problem(400, "invalid_input")
		}
	}
	v = strings.ToLower(strings.TrimSuffix(v, "."))
	labels := strings.Split(v, ".")
	if len(v) > 253 || len(labels) < 2 {
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
func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{PageIdx: 1, PageSize: 20, Domains: []string{}}
	bad := func() (Filter, error) { return f, problem(400, "invalid_input") }
	// Validate all structural values before returning an unsupported-feature code.
	unsupported := ""
	for k, vs := range q {
		if len(vs) == 0 || (k != "filterDomain" && k != "filterStatus" && len(vs) != 1) {
			return bad()
		}
		switch k {
		case "pageIdx", "pageSize", "isPlaintext":
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
			case "isPlaintext":
				if n < 0 || n > 1 {
					return bad()
				}
				if n == 1 {
					unsupported = "plaintext_unavailable"
				}
			}
		case "filterDomain":
			for _, v := range vs {
				d, e := canonicalDNS(v)
				if e != nil {
					return bad()
				}
				if !slices.Contains(f.Domains, d) {
					f.Domains = append(f.Domains, d)
				}
				if len(f.Domains) > 100 {
					return bad()
				}
			}
		case "filterStatus":
			if len(vs) > 100 {
				return bad()
			}
			for _, v := range vs {
				if v == "" || !utf8.ValidString(v) || len(v) > 200 {
					return bad()
				}
			}
			if unsupported == "" {
				unsupported = "unsupported_status_filter"
			}
		case "filterKeyword":
			if !utf8.ValidString(vs[0]) || len(vs[0]) > 200 || utf8.RuneCountInString(vs[0]) > 50 || strings.ContainsRune(vs[0], 0) {
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
	if unsupported != "" {
		return f, problem(422, unsupported)
	}
	return f, nil
}
