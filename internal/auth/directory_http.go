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
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

var directoryRoutes = map[string]taskRoute{
	"/observation": {"GET", "", false},
	"/receipt":     {"GET", "", false},
	"/task":        {"GET", "", false},
	"/sync":        {"POST", "domainId expectedRevision expectedCredentialGeneration idempotencyKey actorPassword totpCode", true},
	"/cancel":      {"POST", "taskUUID actorPassword totpCode", true},
}

type directoryRequest struct {
	domains.DirectoryInput
	TaskUUID      string `json:"taskUUID"`
	ActorPassword string `json:"actorPassword"`
	TOTPCode      string `json:"totpCode"`
}

// Proof never enters the task payload or an idempotency fingerprint.
func (directoryRequest) String() string               { return "[directory mutation]" }
func (directoryRequest) GoString() string             { return "[directory mutation]" }
func (directoryRequest) MarshalJSON() ([]byte, error) { return []byte(`"[directory mutation]"`), nil }

// Introspection and execution share the same exact route/permission boundary.
// Reading stored objects conveys no authority to use an operation credential.
func directoryPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return directoryPathAllowedForProfile(method, path, grants, directoryHTTPV1)
}

func directoryPathAllowedForProfile(method, path string, grants map[string]AccessAuth, profile directoryHTTPProfile) bool {
	prefix, _ := directoryHTTPEndpoint(profile)
	if prefix == "" {
		return false
	}
	suffix, prefixed := strings.CutPrefix(path, prefix)
	route, known := directoryRoutes[suffix]
	if !prefixed || !known || method != route.method || !grants["domains"].Readable || !grants["directory_assets"].Readable {
		return false
	}
	if route.write && !grants["directory_assets"].Writeable {
		return false
	}
	if suffix != "/observation" {
		return grants["tasks"].Readable && (!route.write || grants["tasks"].Writeable)
	}
	return true
}

func (s *Service) DirectoryHandler(store *domains.Store, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ServeDirectory(w, r, store, engine)
	})
}

func (s *Service) ServeDirectory(w http.ResponseWriter, r *http.Request, store *domains.Store, engine *tasks.Engine) {
	s.serveDirectoryForProfile(w, r, store, engine, directoryHTTPV1)
}

func (s *Service) serveDirectoryForProfile(w http.ResponseWriter, r *http.Request, store *domains.Store, engine *tasks.Engine, profile directoryHTTPProfile) {
	w.Header().Set("Cache-Control", "no-store")
	prefix, _ := directoryHTTPEndpoint(profile)
	path, prefixed := strings.CutPrefix(r.URL.Path, prefix)
	route, known := directoryRoutes[path]
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
	var in directoryRequest
	var filter domains.DirectoryFilter
	if route.write {
		if s.origin == "" || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin {
			write(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if len(r.Header.Values("Content-Type")) != 1 || mediaErr != nil || media != "application/json" {
			write(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			return
		}
		err = decodeDirectoryRequest(w, r, path, &in)
	} else {
		filter, err = directoryQuery(path, q)
	}
	if err != nil {
		write(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
		return
	}
	if s.database == nil || store == nil || engine == nil {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "schema_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	out, err := s.handleDirectoryForProfile(ctx, r, store, engine, path, route, in, filter, q, profile)
	if err != nil {
		s.writeDirectoryErrorForProfile(w, ctx, path, err, profile)
		return
	}
	if profile == directoryHTTPV2 && path == "/observation" {
		// Bound the complete encoded response before writing headers or bytes.
		raw, encodeErr := json.Marshal(out.value)
		defer clear(raw)
		if encodeErr != nil {
			s.writeDirectoryErrorForProfile(w, ctx, path, fail(503, "directory_observation_unavailable"), profile)
			return
		}
		if len(raw) > domains.DirectoryV2PageMaxBytes {
			s.writeDirectoryErrorForProfile(w, ctx, path, fail(422, "directory_limit_exceeded"), profile)
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
	write(w, http.StatusOK, out.value)
}

func directoryQuery(path string, q url.Values) (domains.DirectoryFilter, error) {
	f := domains.DirectoryFilter{PageIdx: 1, PageSize: 50}
	bad := func() (domains.DirectoryFilter, error) { return domains.DirectoryFilter{}, fail(400, "invalid_input") }
	switch path {
	case "/observation":
		for key, values := range q {
			if len(values) != 1 || values[0] == "" {
				return bad()
			}
			v := values[0]
			switch key {
			case "domainId":
				f.DomainID = v
			case "observationId":
				f.ObservationID = v
			case "kind":
				f.Kind = directoryassets.Kind(v)
			case "pageIdx", "pageSize":
				n, err := strconv.Atoi(v)
				if err != nil || strconv.Itoa(n) != v {
					return bad()
				}
				if key == "pageIdx" {
					f.PageIdx = n
				} else {
					f.PageSize = n
				}
			default:
				return bad()
			}
		}
		if domains.ValidateDirectoryFilter(f) != nil {
			return bad()
		}
	case "/receipt":
		if len(q) != 2 || len(q["domainId"]) != 1 || len(q["idempotencyKey"]) != 1 || !domains.ValidID(q.Get("domainId")) || !domains.ValidKey(q.Get("idempotencyKey")) {
			return bad()
		}
		f.DomainID = q.Get("domainId")
	case "/task":
		if domainSingleQuery(q, "taskUUID", domains.ValidID) != nil {
			return bad()
		}
	default:
		return bad()
	}
	return f, nil
}

func decodeDirectoryRequest(w http.ResponseWriter, r *http.Request, path string, in *directoryRequest) error {
	bad := func() error { return fail(http.StatusBadRequest, "invalid_input") }
	route, known := directoryRoutes[path]
	if !known || !route.write || r.URL.RawQuery != "" {
		return bad()
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
	defer clear(raw)
	if err != nil || !utf8.Valid(raw) {
		return bad()
	}
	fields, err := resourceExactObject(raw, route.fields)
	if err != nil || resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return bad()
	}
	for _, key := range strings.Fields(route.fields) {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return bad()
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(in) != nil || d.Decode(&struct{}{}) != io.EOF || len(in.ActorPassword) == 0 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return bad()
	}
	for _, c := range in.TOTPCode {
		if c < '0' || c > '9' {
			return bad()
		}
	}
	if path == "/sync" && domains.ValidateDirectoryInput(in.DirectoryInput) != nil || path == "/cancel" && !domains.ValidID(in.TaskUUID) {
		return bad()
	}
	return nil
}

func (s *Service) writeDirectoryError(w http.ResponseWriter, ctx context.Context, path string, err error) {
	s.writeDirectoryErrorForProfile(w, ctx, path, err, directoryHTTPV1)
}

func (s *Service) writeDirectoryErrorForProfile(w http.ResponseWriter, ctx context.Context, path string, err error, profile directoryHTTPProfile) {
	if profile == directoryHTTPV2 {
		path = "/v2" + path
	}
	status, code := domainHTTPError(err)
	// A failed schema gate must never trigger writes through failure attribution.
	if status != http.StatusServiceUnavailable {
		s.recordFailure(ctx, "directory"+strings.ReplaceAll(path, "/", "_"))
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "900")
	}
	write(w, status, map[string]string{"error": code})
}

func directorySchemaError(err error) error {
	if errors.Is(err, tasks.ErrSchemaIncompatible) {
		return fail(http.StatusServiceUnavailable, "schema_incompatible")
	}
	return fail(http.StatusServiceUnavailable, "schema_unavailable")
}

func (s *Service) handleDirectory(ctx context.Context, r *http.Request, store *domains.Store, engine *tasks.Engine, path string, route taskRoute, in directoryRequest, filter domains.DirectoryFilter, q url.Values) (*credentialUseResult, error) {
	return s.handleDirectoryForProfile(ctx, r, store, engine, path, route, in, filter, q, directoryHTTPV1)
}

func (s *Service) handleDirectoryForProfile(ctx context.Context, r *http.Request, store *domains.Store, engine *tasks.Engine, path string, route taskRoute, in directoryRequest, filter domains.DirectoryFilter, q url.Values, profile directoryHTTPProfile) (*credentialUseResult, error) {
	_, kind := directoryHTTPEndpoint(profile)
	if kind == "" {
		return nil, fail(404, "not_found")
	}
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, directorySchemaError(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, directorySchemaError(err)
	}
	defer tx.Rollback(ctx)
	// Pin the exact schema before identity, rate limits or business-state access.
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		return nil, directorySchemaError(err)
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
	if !directoryPathAllowedForProfile(r.Method, r.URL.Path, grants, profile) {
		return nil, fail(http.StatusForbidden, "forbidden")
	}
	if _, _, err = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); err != nil {
		return nil, err
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, err
	}
	id := in.DomainID
	if !route.write {
		id = filter.DomainID
	}
	if id != "" && !slices.Contains(allowed, id) {
		return nil, fail(http.StatusNotFound, "not_found")
	}
	if route.write {
		if err = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); err != nil {
			return nil, err
		}
	}
	if err = setAuditMetadata(ctx, tx, actor); err != nil {
		return nil, err
	}
	p := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	var value any
	switch path {
	case "/observation":
		if profile == directoryHTTPV2 {
			value, err = store.DirectoryV2ListTx(ctx, tx, actor.tenant, allowed, filter)
		} else {
			value, err = store.DirectoryListTx(ctx, tx, actor.tenant, allowed, filter)
		}
	case "/receipt":
		var task tasks.Task
		if profile == directoryHTTPV2 {
			task, err = store.DirectoryV2ReceiptTx(ctx, tx, engine, p, filter.DomainID, q.Get("idempotencyKey"))
		} else {
			task, err = store.DirectoryReceiptTx(ctx, tx, engine, p, filter.DomainID, q.Get("idempotencyKey"))
		}
		value = map[string]any{"task": publicTask(task)}
	case "/task":
		var detail tasks.Detail
		detail, err = engine.DetailTx(ctx, tx, p, q.Get("taskUUID"))
		if err == nil && (detail.Task.Kind != kind || !slices.Contains(allowed, detail.Task.DomainID)) {
			return nil, fail(http.StatusNotFound, "not_found")
		}
		value = map[string]any{"task": publicTask(detail.Task)}
	case "/sync":
		if profile == directoryHTTPV2 {
			value, err = store.SubmitDirectoryV2Tx(ctx, tx, engine, p, in.DirectoryInput)
		} else {
			value, err = store.SubmitDirectoryTx(ctx, tx, engine, p, in.DirectoryInput)
		}
		if err == nil {
			value = publicTaskValue(value)
		}
	case "/cancel":
		var task tasks.Task
		if profile == directoryHTTPV2 {
			task, err = store.CancelDirectoryV2Tx(ctx, tx, engine, p, in.TaskUUID, allowed)
		} else {
			task, err = store.CancelDirectoryTx(ctx, tx, engine, p, in.TaskUUID, allowed)
		}
		value = map[string]any{"task": publicTask(task)}
	default:
		return nil, fail(http.StatusNotFound, "not_found")
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
