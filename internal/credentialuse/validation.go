package credentialuse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/tasks"
)

func validRole(id string) bool {
	if id == "platform_admin" || id == "viewer" {
		return true
	}
	if len(id) != 24 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// ValidateInput preserves the connection-test-only HTTP contract.
func ValidateInput(path string, in *Input) error {
	return ValidateInputForPurpose(path, in, Purpose)
}

// ValidateInputForPurpose validates an explicitly selected supported purpose.
// A caller must select purpose from its trusted route, never from in.Purpose.
func ValidateInputForPurpose(path string, in *Input, purpose string) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	if in == nil {
		return problem(400, "invalid_input")
	}
	if path != "/grant" && path != "/revoke" || !operationaccounts.ValidID(in.AccountID) || !validRole(in.RoleID) || !operationaccounts.ValidKey(in.IdempotencyKey) {
		return problem(400, "invalid_input")
	}
	if in.Purpose != purpose {
		return problem(422, "unsupported_credential_purpose")
	}
	for _, rev := range []string{in.ExpectedAccountRevision, in.ExpectedCredentialRevision} {
		if _, e := operationaccounts.Revision(rev); e != nil {
			return problem(400, "invalid_input")
		}
	}
	if in.ExpectedGrantRevision != "0" || path != "/grant" {
		if _, e := operationaccounts.Revision(in.ExpectedGrantRevision); e != nil {
			return problem(400, "invalid_input")
		}
	}
	return nil
}

// Fingerprints cover canonical nonsecret intent only. Fresh proof never enters
// this type or digest, allowing a committed retry without consuming another TOTP.
func fingerprint(p tasks.Principal, path string, in Input) string {
	type metadata Input
	raw, _ := json.Marshal(struct {
		Version, Operation, Tenant string
		Actor                      int64
		Input                      metadata
	}{"credential-use-v1", strings.TrimPrefix(path, "/"), p.TenantID, p.ActorID, metadata(in)})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{PageIdx: 1, PageSize: 20}
	for k, vs := range q {
		if len(vs) != 1 {
			return f, problem(400, "invalid_input")
		}
		switch k {
		case "pageIdx", "pageSize":
			n, e := strconv.Atoi(vs[0])
			if e != nil || strconv.Itoa(n) != vs[0] {
				return f, problem(400, "invalid_input")
			}
			if k == "pageIdx" {
				if n < 1 || n > 1000000 {
					return f, problem(400, "invalid_input")
				}
				f.PageIdx = n
			} else {
				if n < 10 || n > 50 || n%10 != 0 {
					return f, problem(400, "invalid_input")
				}
				f.PageSize = n
			}
		case "keyword":
			if !utf8.ValidString(vs[0]) || len(vs[0]) > 200 || utf8.RuneCountInString(vs[0]) > 50 || strings.ContainsFunc(vs[0], unicode.IsControl) {
				return f, problem(400, "invalid_input")
			}
			f.Keyword = vs[0]
		default:
			return f, problem(400, "invalid_input")
		}
	}
	return f, nil
}
