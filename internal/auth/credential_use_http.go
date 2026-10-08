package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

const credentialUseMutationFields = "accountId roleId purpose expectedAccountRevision expectedCredentialRevision expectedGrantRevision idempotencyKey actorPassword totpCode"

var credentialUseRoutes = map[string]taskRoute{
	"/accounts":  {"GET", "", false},
	"/grants":    {"GET", "", false},
	"/roles":     {"GET", "", false},
	"/effective": {"GET", "", false},
	"/mutation":  {"GET", "", false},
	"/grant":     {"POST", credentialUseMutationFields, true},
	"/revoke":    {"POST", credentialUseMutationFields, true},
}

type credentialUseRequest struct {
	credentialuse.Input
	ActorPassword string `json:"actorPassword"`
	TOTPCode      string `json:"totpCode"`
}

// Proof is deliberately outside credentialuse.Input: replay fingerprints and
// durable receipts only receive the canonical, nonsecret mutation metadata.
func (credentialUseRequest) String() string   { return "[credential use mutation]" }
func (credentialUseRequest) GoString() string { return "[credential use mutation]" }
func (credentialUseRequest) MarshalJSON() ([]byte, error) {
	return []byte(`"[credential use mutation]"`), nil
}

type credentialUseResult struct {
	value   any
	actorID int64
}

type credentialUseMutationResult struct {
	credentialuse.Receipt
	ConsumerEnabled bool `json:"consumerEnabled"`
}

func credentialUsePathAllowed(method, path, role string, grants map[string]AccessAuth) bool {
	return credentialUsePathAllowedForPurpose(method, path, role, grants, credentialuse.Purpose)
}

func directoryCredentialUsePathAllowed(method, path, role string, grants map[string]AccessAuth) bool {
	return credentialUsePathAllowedForPurpose(method, path, role, grants, credentialuse.DirectoryPurpose)
}

func directoryV2CredentialUsePathAllowed(method, path, role string, grants map[string]AccessAuth) bool {
	return credentialUsePathAllowedForPurpose(method, path, role, grants, credentialuse.DirectoryV2Purpose)
}

// Only a trusted HTTP entry point selects a purpose. Neither request metadata
// nor a grant for one purpose can select another endpoint or enable its consumer.
func credentialUseEndpointForPurpose(purpose string) (prefix string, consumerEnabled bool) {
	switch purpose {
	case credentialuse.Purpose:
		return "/api/credential-use", credentialuse.ConsumerEnabled
	case credentialuse.DirectoryPurpose:
		return "/api/directory-credential-use", credentialuse.DirectoryConsumerEnabled
	case credentialuse.DirectoryV2Purpose:
		return "/api/directory-credential-use/v2", credentialuse.DirectoryV2ConsumerEnabled
	default:
		return "", false
	}
}

func credentialUsePathAllowedForPurpose(method, path, role string, grants map[string]AccessAuth, purpose string) bool {
	prefix, _ := credentialUseEndpointForPurpose(purpose)
	if prefix == "" {
		return false
	}
	suffix, prefixed := strings.CutPrefix(path, prefix)
	route, known := credentialUseRoutes[suffix]
	if !prefixed || !known || method != route.method {
		return false
	}
	if suffix == "/effective" {
		if purpose == credentialuse.DirectoryPurpose || purpose == credentialuse.DirectoryV2Purpose {
			return grants["domains"].Readable && grants["directory_assets"].Readable
		}
		return grants["operation_accounts"].Readable
	}
	return role == "platform_admin"
}

func (s *Service) CredentialUseHandler(store *credentialuse.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ServeCredentialUse(w, r, store)
	})
}

func (s *Service) DirectoryCredentialUseHandler(store *credentialuse.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveCredentialUseForPurpose(w, r, store, credentialuse.DirectoryPurpose)
	})
}

func (s *Service) DirectoryV2CredentialUseHandler(store *credentialuse.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveCredentialUseForPurpose(w, r, store, credentialuse.DirectoryV2Purpose)
	})
}

func (s *Service) ServeCredentialUse(w http.ResponseWriter, r *http.Request, store *credentialuse.Store) {
	s.serveCredentialUseForPurpose(w, r, store, credentialuse.Purpose)
}

func (s *Service) serveCredentialUseForPurpose(w http.ResponseWriter, r *http.Request, store *credentialuse.Store, purpose string) {
	w.Header().Set("Cache-Control", "no-store")
	prefix, _ := credentialUseEndpointForPurpose(purpose)
	path, prefixed := strings.CutPrefix(r.URL.Path, prefix)
	route, known := credentialUseRoutes[path]
	if prefix == "" || !prefixed || !known {
		write(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	if len(r.URL.RawQuery) > 32768 {
		write(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		write(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
		return
	}
	var in credentialUseRequest
	if route.write {
		if r.Header.Get("Origin") != s.origin {
			write(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaErr != nil || media != "application/json" {
			write(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			return
		}
		err = decodeCredentialUseRequestForPurpose(w, r, path, &in, purpose)
	} else {
		err = credentialUseQuery(path, q)
	}
	if err != nil {
		status, code := credentialUseHTTPError(err)
		write(w, status, map[string]string{"error": code})
		return
	}
	if s.database == nil || store == nil {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "schema_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	out, err := s.handleCredentialUseForPurpose(ctx, r, store, path, route, in, q, purpose)
	if err != nil {
		status, code := credentialUseHTTPError(err)
		// Schema failures precede authentication and must not write to an
		// incompatible schema through the failure-audit path either.
		if status != http.StatusServiceUnavailable {
			s.recordFailure(ctx, "credential_use"+strings.ReplaceAll(path, "/", "_"))
		}
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
	write(w, http.StatusOK, out.value)
}

func credentialUseQuery(path string, q url.Values) error {
	switch path {
	case "/accounts":
		_, err := credentialuse.ParseFilter(q)
		return err
	case "/grants", "/roles", "/effective":
		return domainSingleQuery(q, "accountId", operationaccounts.ValidID)
	case "/mutation":
		return domainSingleQuery(q, "idempotencyKey", operationaccounts.ValidKey)
	default:
		return fail(http.StatusBadRequest, "invalid_input")
	}
}

// Cleanup must remain possible when tenant eligibility lapses. Its narrower
// exception preserves live identity, builtin administration and exact active
// F47 membership, while granting and effective-use checks retain all live gates.
func credentialUseCleanupPath(path string) bool {
	return path == "/accounts" || path == "/grants" || path == "/mutation" || path == "/revoke"
}

func (s *Service) credentialUseCleanupScope(ctx context.Context, tx pgx.Tx, actor User, role string) ([]string, error) {
	persisted, err := s.persistedResourceIDs(ctx, tx, actor.tenant, role)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM adtr.resource_domains WHERE tenant_id=$1 AND active AND id=ANY($2::text[]) ORDER BY id`, actor.tenant, persisted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed = append(allowed, id)
	}
	return allowed, rows.Err()
}

func decodeCredentialUseRequest(w http.ResponseWriter, r *http.Request, path string, in *credentialUseRequest) error {
	return decodeCredentialUseRequestForPurpose(w, r, path, in, credentialuse.Purpose)
}

func decodeCredentialUseRequestForPurpose(w http.ResponseWriter, r *http.Request, path string, in *credentialUseRequest, purpose string) error {
	bad := func() error { return fail(http.StatusBadRequest, "invalid_input") }
	if path != "/grant" && path != "/revoke" || r.URL.RawQuery != "" {
		return bad()
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
	defer clear(raw)
	if err != nil {
		return bad()
	}
	if !utf8.Valid(raw) {
		return bad()
	}
	fields, err := resourceExactObject(raw, credentialUseMutationFields)
	if err != nil {
		return bad()
	}
	if err = resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return bad()
	}
	for _, key := range strings.Fields(credentialUseMutationFields) {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return bad()
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(in) != nil || d.Decode(&struct{}{}) != io.EOF || len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return bad()
	}
	for _, c := range in.TOTPCode {
		if c < '0' || c > '9' {
			return bad()
		}
	}
	return credentialuse.ValidateInputForPurpose(path, &in.Input, purpose)
}

func credentialUseHTTPError(err error) (int, string) {
	if credentialuse.IsGovernanceError(err) {
		return http.StatusForbidden, "credential_use_governance_required"
	}
	var c *credentialuse.Error
	var d *domains.Error
	var a *operationaccounts.Error
	switch {
	case errors.As(err, &c):
		return c.Status, c.Code
	case errors.As(err, &d):
		return d.Status, d.Code
	case errors.As(err, &a):
		return a.Status, a.Code
	default:
		return taskHTTPError(err)
	}
}

func (s *Service) handleCredentialUse(ctx context.Context, r *http.Request, store *credentialuse.Store, path string, route taskRoute, in credentialUseRequest, q url.Values) (*credentialUseResult, error) {
	return s.handleCredentialUseForPurpose(ctx, r, store, path, route, in, q, credentialuse.Purpose)
}

func (s *Service) handleCredentialUseForPurpose(ctx context.Context, r *http.Request, store *credentialuse.Store, path string, route taskRoute, in credentialUseRequest, q url.Values, purpose string) (*credentialUseResult, error) {
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, fail(http.StatusServiceUnavailable, "schema_unavailable")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fail(http.StatusServiceUnavailable, "schema_unavailable")
	}
	defer tx.Rollback(ctx)
	// Every credential-use entry point pins the exact live schema before any
	// session, tenant, user, rate-limit, or business-state access.
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		if errors.Is(err, tasks.ErrSchemaIncompatible) {
			return nil, fail(http.StatusServiceUnavailable, "schema_incompatible")
		}
		return nil, fail(http.StatusServiceUnavailable, "schema_unavailable")
	}
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err = s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, err
		}
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !credentialUsePathAllowedForPurpose(r.Method, r.URL.Path, role, grants, purpose) || path != "/effective" && actor.Role != "platform_admin" {
		return nil, fail(http.StatusForbidden, "forbidden")
	}
	var allowed []string
	if credentialUseCleanupPath(path) {
		allowed, err = s.credentialUseCleanupScope(ctx, tx, actor, role)
	} else {
		if _, _, err = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); err != nil {
			return nil, err
		}
		allowed, err = s.effectiveResourceIDs(ctx, tx, actor, role)
	}
	if err != nil {
		return nil, err
	}
	p := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	_, consumerEnabled := credentialUseEndpointForPurpose(purpose)
	var out any
	switch path {
	case "/accounts":
		var filter credentialuse.Filter
		filter, err = credentialuse.ParseFilter(q)
		if err == nil {
			out, err = store.AccountsForPurposeTx(ctx, tx, actor.tenant, allowed, filter, purpose)
		}
	case "/grants":
		out, err = store.GrantsForPurposeTx(ctx, tx, actor.tenant, q.Get("accountId"), allowed, purpose)
	case "/roles":
		out, err = store.RolesForPurposeTx(ctx, tx, actor.tenant, q.Get("accountId"), allowed, purpose)
	case "/effective":
		out, err = store.EffectiveForPurposeTx(ctx, tx, p, role, q.Get("accountId"), allowed, purpose)
	case "/mutation":
		var receipt credentialuse.Receipt
		receipt, err = store.ReceiptForPurposeTx(ctx, tx, p, q.Get("idempotencyKey"), allowed, purpose)
		out = struct {
			Receipt         credentialuse.Receipt `json:"receipt"`
			ConsumerEnabled bool                  `json:"consumerEnabled"`
		}{Receipt: receipt, ConsumerEnabled: consumerEnabled}
	case "/grant", "/revoke":
		// Authentication holds the tenant lock until commit. Recovery still
		// checks the current actor/scope and metadata fingerprint, but happens
		// before proof consumption and obsolete account/grant CAS checks.
		var replay *credentialuse.Receipt
		replay, err = store.ReplayForPurposeTx(ctx, tx, p, path, in.Input, allowed, purpose)
		if err != nil {
			return nil, err
		}
		if replay != nil {
			out = credentialUseMutationResult{Receipt: *replay, ConsumerEnabled: consumerEnabled}
			break
		}
		if err = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); err != nil {
			return nil, err
		}
		operation := credentialuse.GrantUse
		if path == "/revoke" {
			operation = credentialuse.RevokeUse
		}
		if err = credentialuse.SetGovernanceContext(ctx, tx, p, operation); err != nil {
			return nil, err
		}
		if err = setAuditMetadata(ctx, tx, actor); err != nil {
			return nil, err
		}
		var receipt credentialuse.Receipt
		receipt, err = store.MutateForPurposeTx(ctx, tx, p, path, in.Input, allowed, purpose)
		out = credentialUseMutationResult{Receipt: receipt, ConsumerEnabled: consumerEnabled}
	default:
		return nil, fail(http.StatusNotFound, "not_found")
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: out, actorID: actor.ID}, nil
}
