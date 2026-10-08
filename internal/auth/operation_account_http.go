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
	"slices"
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

var operationAccountRoutes = map[string]taskRoute{
	"": {"GET", "", false}, "/detail": {"GET", "", false}, "/mutation": {"GET", "", false}, "/domains": {"GET", "", false},
	"/create": {"POST", "domainId expectedDomainRevision username password idempotencyKey label actorPassword totpCode", true},
	"/update": {"POST", "accountId expectedRevision idempotencyKey label username password actorPassword totpCode", true},
	"/delete": {"POST", "accountId expectedRevision confirmAccountId idempotencyKey actorPassword totpCode", true},
}

type operationAccountRequest struct {
	operationaccounts.Input
	ActorPassword string `json:"actorPassword"`
	TOTPCode      string `json:"totpCode"`
}

func operationAccountPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/operation-accounts")
	route, known := operationAccountRoutes[suffix]
	a := grants["operation_accounts"]
	return ok && known && method == route.method && a.Readable && (!route.write || a.Writeable)
}
func (s *Service) OperationAccountsHandler(store *operationaccounts.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.ServeOperationAccounts(w, r, store) })
}
func (s *Service) ServeOperationAccounts(w http.ResponseWriter, r *http.Request, store *operationaccounts.Store) {
	w.Header().Set("Cache-Control", "no-store")
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/operation-accounts")
	route, known := operationAccountRoutes[path]
	if !prefixed || !known {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if len(r.URL.RawQuery) > 32768 {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in operationAccountRequest
	var f operationaccounts.Filter
	if route.write {
		if r.Header.Get("Origin") != s.origin {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		e = decodeOperationAccountRequest(w, r, path, route, &in)
	} else {
		switch path {
		case "":
			f, e = operationaccounts.ParseFilter(q)
		case "/domains":
			for key := range q {
				if key != "pageIdx" && key != "pageSize" && key != "filterKeyword" {
					e = fail(400, "invalid_input")
					break
				}
			}
			if e == nil {
				f, e = operationaccounts.ParseFilter(q)
			}
		case "/detail":
			e = domainSingleQuery(q, "accountId", operationaccounts.ValidID)
		case "/mutation":
			e = domainSingleQuery(q, "idempotencyKey", operationaccounts.ValidKey)
		}
	}
	if e != nil {
		status, code := operationAccountHTTPError(e)
		write(w, status, map[string]string{"error": code})
		return
	}
	if !store.Available() {
		write(w, 503, map[string]string{"error": "domain_key_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	out, e := s.handleOperationAccount(ctx, r, store, path, route, in, f)
	if e != nil {
		status, code := operationAccountHTTPError(e)
		if status != 503 {
			s.recordFailure(ctx, "operation_account"+strings.ReplaceAll(path, "/", "_"))
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	if result, ok := out.(*credentialUseResult); ok {
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(result.actorID, 10))
		out = result.value
	}
	write(w, 200, out)
}
func decodeOperationAccountRequest(w http.ResponseWriter, r *http.Request, path string, route taskRoute, in *operationAccountRequest) error {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
	if e != nil {
		return fail(400, "invalid_input")
	}
	defer clear(raw)
	if !utf8.Valid(raw) || r.URL.RawQuery != "" {
		return fail(400, "invalid_input")
	}
	fields, e := resourceExactObject(raw, route.fields)
	if e != nil {
		return e
	}
	if e = resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))); e != nil {
		return e
	}
	required := route.fields
	switch path {
	case "/create":
		required = "domainId expectedDomainRevision username password idempotencyKey actorPassword totpCode"
	case "/update":
		required = "accountId expectedRevision idempotencyKey actorPassword totpCode"
	}
	for _, key := range strings.Fields(required) {
		if _, ok := fields[key]; !ok {
			return fail(400, "invalid_input")
		}
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fail(400, "invalid_input")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(in) != nil || d.Decode(&struct{}{}) != io.EOF || len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return fail(400, "invalid_input")
	}
	for _, c := range in.TOTPCode {
		if c < '0' || c > '9' {
			return fail(400, "invalid_input")
		}
	}
	return operationaccounts.ValidateInput(path, &in.Input)
}
func operationAccountHTTPError(err error) (int, string) {
	var e *operationaccounts.Error
	if errors.As(err, &e) {
		return e.Status, e.Code
	}
	var d *domains.Error
	if errors.As(err, &d) {
		return d.Status, d.Code
	}
	return taskHTTPError(err)
}
func (s *Service) handleOperationAccount(ctx context.Context, r *http.Request, store *operationaccounts.Store, path string, route taskRoute, in operationAccountRequest, f operationaccounts.Filter) (any, error) {
	conn, e := pgx.ConnectConfig(ctx, s.database.Copy())
	if e != nil {
		return nil, e
	}
	defer conn.Close(ctx)
	tx, e := conn.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); e != nil {
		return nil, e
	}
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if e = s.rate(ctx, "ip:"+ip, 60); e != nil {
			return nil, e
		}
	}
	actor, role, grants, e := s.authenticateAccess(ctx, tx, r)
	if e != nil {
		return nil, e
	}
	if !operationAccountPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	if _, _, e = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); e != nil {
		return nil, e
	}
	allowed, e := s.effectiveResourceIDs(ctx, tx, actor, role)
	if e != nil {
		return nil, e
	}
	p := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	// No builtin administrator or receipt has an exception to current F47 scope.
	if path == "/create" && !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	if path == "/update" || path == "/delete" {
		if _, e = store.ScopeAccountTx(ctx, tx, actor.tenant, in.AccountID, allowed); e != nil {
			return nil, e
		}
	}
	if route.write {
		if e = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); e != nil {
			return nil, e
		}
		if e = setCredentialGovernance(ctx, tx, actor, credentialuse.MutateAccount); e != nil {
			return nil, e
		}
	}
	if e = setAuditMetadata(ctx, tx, actor); e != nil {
		return nil, e
	}
	var out any
	switch path {
	case "":
		out, e = store.ListTx(ctx, tx, actor.tenant, allowed, f)
	case "/domains":
		out, e = store.DomainsTx(ctx, tx, actor.tenant, allowed, f)
	case "/detail":
		var a operationaccounts.Account
		a, e = store.DetailTx(ctx, tx, actor.tenant, r.URL.Query().Get("accountId"), allowed)
		out = map[string]any{"account": a}
	case "/mutation":
		var receipt operationaccounts.Receipt
		receipt, e = store.ReceiptTx(ctx, tx, p, r.URL.Query().Get("idempotencyKey"), allowed)
		out = map[string]any{"receipt": receipt}
	case "/create", "/update", "/delete":
		out, e = store.MutateTx(ctx, tx, p, path, in.Input, allowed)
	default:
		return nil, fail(404, "not_found")
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &credentialUseResult{value: out, actorID: actor.ID}, nil
}
