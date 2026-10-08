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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
)

var domainRoutes = map[string]taskRoute{
	"": {"GET", "", false}, "/detail": {"GET", "", false}, "/creation": {"GET", "", false}, "/test-result": {"GET", "", false},
	"/create-unconfigured": {"POST", "domain dcHostName ldapAddr port idempotencyKey actorPassword totpCode", true},
	"/create":              {"POST", "domain dcHostName ldapAddr port username password idempotencyKey actorPassword totpCode", true},
	"/update":              {"POST", "domainId expectedRevision dcHostName ldapAddr port username password actorPassword totpCode", true},
	"/delete":              {"POST", "domainId expectedRevision confirmDomain actorPassword totpCode", true},
	"/test":                {"POST", "domainId expectedRevision idempotencyKey actorPassword totpCode", true},
}

type domainRequest struct {
	domains.Input
	ActorPassword string `json:"actorPassword"`
	TOTPCode      string `json:"totpCode"`
}

func (domainRequest) String() string               { return "[domain mutation]" }
func (domainRequest) GoString() string             { return "[domain mutation]" }
func (domainRequest) MarshalJSON() ([]byte, error) { return []byte(`"[domain mutation]"`), nil }

func domainPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	if strings.HasPrefix(path, "/api/domains/credential-source") {
		return domainSourcePathAllowed(method, path, grants)
	}
	suffix, ok := strings.CutPrefix(path, "/api/domains")
	route, known := domainRoutes[suffix]
	a := grants["domains"]
	if !ok || !known || method != route.method || !a.Readable || route.write && !a.Writeable {
		return false
	}
	if suffix == "/test" || suffix == "/test-result" {
		t := grants["tasks"]
		return t.Readable && (!route.write || t.Writeable)
	}
	return true
}

// Access introspection must agree with enrollment's builtin-admin check.
func domainAccessPathAllowed(method, path, role string, grants map[string]AccessAuth) bool {
	if !domainPathAllowed(method, path, grants) {
		return false
	}
	switch path {
	case "/api/domains/create", "/api/domains/create-unconfigured", "/api/domains/creation":
		return role == "platform_admin"
	}
	return true
}
func (s *Service) DomainsHandler(store *domains.Store, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.ServeDomains(w, r, store, engine) })
}
func (s *Service) ServeDomains(w http.ResponseWriter, r *http.Request, store *domains.Store, engine *tasks.Engine) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/api/domains/credential-source") {
		s.serveDomainSource(w, r, store)
		return
	}
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/domains")
	route, known := domainRoutes[path]
	if !prefixed || !known {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if _, e := url.ParseQuery(r.URL.RawQuery); e != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in domainRequest
	var filter domains.Filter
	var err error
	if route.write {
		if r.Header.Get("Origin") != s.origin {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		err = decodeDomainRequest(w, r, path, route, &in)
	} else {
		switch path {
		case "":
			filter, err = domains.ParseFilter(r.URL.Query())
		case "/detail":
			err = domainSingleQuery(r.URL.Query(), "domainId", domains.ValidID)
		case "/creation":
			err = domainSingleQuery(r.URL.Query(), "idempotencyKey", domains.ValidKey)
		case "/test-result":
			err = domainSingleQuery(r.URL.Query(), "taskUUID", domains.ValidID)
		}
	}
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if store == nil || engine == nil {
		write(w, 503, map[string]string{"error": "domains_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	value, err := s.handleDomain(ctx, r, store, engine, path, route, in, filter)
	if err != nil {
		status, code := domainHTTPError(err)
		if status != 503 {
			s.recordFailure(ctx, "domain"+strings.ReplaceAll(path, "/", "_"))
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	if result, ok := value.(*credentialUseResult); ok {
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(result.actorID, 10))
		value = result.value
	}
	write(w, 200, value)
}
func domainSingleQuery(q url.Values, key string, valid func(string) bool) error {
	if len(q) != 1 || len(q[key]) != 1 || !valid(q.Get(key)) {
		return fail(400, "invalid_input")
	}
	return nil
}
func decodeDomainRequest(w http.ResponseWriter, r *http.Request, path string, route taskRoute, in *domainRequest) error {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
	if e != nil {
		return e
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
	case "/create-unconfigured":
		required = "domain dcHostName port idempotencyKey actorPassword totpCode"
	case "/create":
		required = "domain dcHostName port username password idempotencyKey actorPassword totpCode"
	case "/update":
		required = "domainId expectedRevision dcHostName port actorPassword totpCode"
	}
	for _, k := range strings.Fields(required) {
		if _, ok := fields[k]; !ok {
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
	if d.Decode(in) != nil || d.Decode(&struct{}{}) != io.EOF || len(in.ActorPassword) == 0 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return fail(400, "invalid_input")
	}
	return domains.ValidateInput(path, &in.Input)
}
func domainHTTPError(err error) (int, string) {
	var e *domains.Error
	var pg *pgconn.PgError
	if errors.As(err, &e) {
		return e.Status, e.Code
	}
	if errors.As(err, &pg) && pg.ConstraintName == "domain_source_authorization" {
		return 403, "credential_source_authorization_required"
	}
	if errors.As(err, &pg) && pg.Code == "23505" {
		if pg.ConstraintName == "domain_test_family_idempotency" {
			return 409, "idempotency_conflict"
		}
		return 409, "domain_conflict"
	}
	return taskHTTPError(err)
}
func (s *Service) handleDomain(ctx context.Context, r *http.Request, store *domains.Store, engine *tasks.Engine, path string, route taskRoute, in domainRequest, filter domains.Filter) (any, error) {
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
	if e = engine.CheckSchemaTx(ctx, tx); e != nil {
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
	if !domainPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	p := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	var allowed []string
	if path == "/create" || path == "/create-unconfigured" || path == "/creation" {
		if role != "platform_admin" {
			return nil, fail(403, "forbidden")
		}
	} else {
		if _, _, e = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); e != nil {
			return nil, e
		}
		allowed, e = s.effectiveResourceIDs(ctx, tx, actor, role)
		if e != nil {
			return nil, e
		}
		id := in.DomainID
		if path == "/detail" {
			id = r.URL.Query().Get("domainId")
		}
		if id != "" && !slices.Contains(allowed, id) {
			return nil, fail(404, "not_found")
		}
	}
	if route.write {
		if e = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); e != nil {
			return nil, e
		}
	}
	if e = setAuditMetadata(ctx, tx, actor); e != nil {
		return nil, e
	}
	var value any
	switch path {
	case "":
		value, e = store.ListTx(ctx, tx, actor.tenant, allowed, filter)
	case "/detail":
		var c domains.Connection
		c, e = store.DetailTx(ctx, tx, actor.tenant, r.URL.Query().Get("domainId"))
		value = map[string]any{"connection": c}
	case "/creation":
		var v domains.Receipt
		v, e = store.ReceiptTx(ctx, tx, p, r.URL.Query().Get("idempotencyKey"))
		value = map[string]any{"receipt": v}
	case "/create-unconfigured":
		value, e = store.CreateUnconfiguredTx(ctx, tx, p, in.Input, s.now())
	case "/create":
		value, e = store.CreateTx(ctx, tx, p, in.Input, s.now())
	case "/update":
		var out domains.Mutation
		out, e = store.UpdateTx(ctx, tx, p, in.Input)
		value = map[string]any{"result": out.Result, "domainId": out.DomainID, "revision": out.Revision}
	case "/delete":
		var out domains.Mutation
		out, e = store.DeleteTx(ctx, tx, p, in.Input)
		value = map[string]any{"result": out.Result, "domainId": out.DomainID}
	case "/test":
		value, e = store.SubmitTx(ctx, tx, engine, p, in.Input)
		if e == nil {
			value = publicTaskValue(value)
		}
	case "/test-result":
		var detail tasks.Detail
		detail, e = engine.DetailTx(ctx, tx, p, r.URL.Query().Get("taskUUID"))
		if e == nil && (!domains.IsConnectionTestKind(detail.Task.Kind) || !slices.Contains(allowed, detail.Task.DomainID)) {
			return nil, fail(404, "not_found")
		}
		if e == nil {
			var diagnostic *domains.Diagnostic
			diagnostic, e = store.TestResultTx(ctx, tx, detail.Task)
			value = map[string]any{"task": publicTask(detail.Task), "diagnostic": diagnostic}
		}
	default:
		return nil, fail(404, "not_found")
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
