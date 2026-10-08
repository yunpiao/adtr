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
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

const sourceBaseFields = "domainId expectedRevision expectedConnectionCredentialGeneration idempotencyKey actorPassword totpCode"

var domainSourceRoutes = map[string]taskRoute{
	"":           {"GET", "", false},
	"/mutation":  {"GET", "", false},
	"/reference": {"POST", sourceBaseFields + " accountId expectedAccountRevision expectedAccountCredentialRevision expectedGrantRevision", true},
	"/custom":    {"POST", sourceBaseFields + " username password", true},
	"/detach":    {"POST", sourceBaseFields, true},
}

type domainSourceRequest struct {
	domains.SourceInput
	ActorPassword string `json:"actorPassword"`
	TOTPCode      string `json:"totpCode"`
}

func (domainSourceRequest) String() string   { return "[domain credential source mutation]" }
func (domainSourceRequest) GoString() string { return "[domain credential source mutation]" }
func (domainSourceRequest) MarshalJSON() ([]byte, error) {
	return []byte(`"[domain credential source mutation]"`), nil
}

func domainSourcePathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/domains/credential-source")
	route, known := domainSourceRoutes[suffix]
	a := grants["domains"]
	return ok && known && method == route.method && a.Readable && (!(route.write || suffix == "/mutation") || a.Writeable) && (suffix != "/reference" || grants["operation_accounts"].Readable)
}

func decodeDomainSourceRequest(w http.ResponseWriter, r *http.Request, path string, route taskRoute, in *domainSourceRequest) error {
	bad := func() error { return fail(400, "invalid_input") }
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
	if e != nil {
		return bad()
	}
	defer clear(raw)
	if !utf8.Valid(raw) || r.URL.RawQuery != "" {
		return bad()
	}
	fields, e := resourceExactObject(raw, route.fields)
	if e != nil {
		return bad()
	}
	if resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return bad()
	}
	for _, k := range strings.Fields(route.fields) {
		v, ok := fields[k]
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
	return domains.ValidateSourceInput(strings.TrimPrefix(path, "/"), &in.SourceInput)
}

func (s *Service) serveDomainSource(w http.ResponseWriter, r *http.Request, store *domains.Store) {
	w.Header().Set("Cache-Control", "no-store")
	path, ok := strings.CutPrefix(r.URL.Path, "/api/domains/credential-source")
	route, known := domainSourceRoutes[path]
	if !ok || !known {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(r.URL.RawQuery) > 32768 {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in domainSourceRequest
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
		e = decodeDomainSourceRequest(w, r, path, route, &in)
	} else if path == "/mutation" {
		e = domainSingleQuery(q, "idempotencyKey", domains.ValidKey)
	} else {
		e = domainSingleQuery(q, "domainId", domains.ValidID)
	}
	if e != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if s.database == nil || store == nil {
		write(w, 503, map[string]string{"error": "schema_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	out, e := s.handleDomainSource(ctx, r, store, path, route, in, q)
	if e != nil {
		status, code := domainSourceHTTPError(e)
		if status != 503 {
			s.recordFailure(ctx, "domain_credential"+strings.ReplaceAll(path, "/", "_"))
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
	write(w, 200, out.value)
}

func (s *Service) handleDomainSource(ctx context.Context, r *http.Request, store *domains.Store, path string, route taskRoute, in domainSourceRequest, q url.Values) (*credentialUseResult, error) {
	conn, e := pgx.ConnectConfig(ctx, s.database.Copy())
	if e != nil {
		return nil, fail(503, "schema_unavailable")
	}
	defer conn.Close(ctx)
	tx, e := conn.Begin(ctx)
	if e != nil {
		return nil, fail(503, "schema_unavailable")
	}
	defer tx.Rollback(ctx)
	if e = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); e != nil {
		if errors.Is(e, tasks.ErrSchemaIncompatible) {
			return nil, fail(503, "schema_incompatible")
		}
		return nil, fail(503, "schema_unavailable")
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
	if !domainSourcePathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	// Persisted active scope permits reducing an obsolete binding and recovering
	// safe receipts after entitlement or account-use permission has been revoked.
	allowed, e := s.credentialUseCleanupScope(ctx, tx, actor, role)
	if e != nil {
		return nil, e
	}
	p := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	id := in.DomainID
	if path == "" {
		id = q.Get("domainId")
	}
	if path != "/mutation" && !slices.Contains(allowed, id) {
		return nil, fail(404, "not_found")
	}
	var out any
	switch path {
	case "":
		live := true
		if _, _, e = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); e != nil {
			var de *domains.Error
			if !errors.As(e, &de) || (de.Code != "tenant_expired" && de.Code != "tenant_domain_limit_exceeded" && de.Code != "tenant_not_configured") {
				return nil, e
			}
			live = false
		}
		out, e = store.SourceDetailTx(ctx, tx, p, role, id, live && grants["operation_accounts"].Readable, live && grants["tasks"].Readable && grants["tasks"].Writeable && grants["domains"].Writeable)
	case "/mutation":
		var receipt domains.SourceReceipt
		receipt, e = store.SourceReceiptTx(ctx, tx, p, q.Get("idempotencyKey"), allowed)
		out = map[string]any{"receipt": receipt}
	default:
		op := strings.TrimPrefix(path, "/")
		var replay *domains.SourceReceipt
		replay, e = store.SourceReplayTx(ctx, tx, p, op, in.SourceInput, allowed)
		if e != nil {
			return nil, e
		}
		if replay != nil {
			out = *replay
			break
		}
		if path != "/detach" {
			if _, _, e = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); e != nil {
				return nil, e
			}
		}
		if e = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); e != nil {
			return nil, e
		}
		if e = setAuditMetadata(ctx, tx, actor); e != nil {
			return nil, e
		}
		if e = domains.SetSourceContext(ctx, tx, p, op); e != nil {
			return nil, e
		}
		out, e = store.MutateSourceTx(ctx, tx, p, role, op, in.SourceInput, allowed)
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &credentialUseResult{value: out, actorID: actor.ID}, nil
}

func domainSourceHTTPError(err error) (int, string) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.ConstraintName == "domain_source_authorization" {
		return 403, "credential_source_authorization_required"
	}
	return credentialUseHTTPError(err)
}
