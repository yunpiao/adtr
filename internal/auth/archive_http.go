package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

type archiveRoute struct {
	method, fields string
	write          bool
}

var archiveRoutes = map[string]archiveRoute{
	"/archive-candidates": {"GET", "", false},
	"/archive":            {"POST", "targets before reason idempotencyKey actorPassword totpCode", true},
	"/restore":            {"POST", "targets reason idempotencyKey actorPassword totpCode", true},
}

type archiveRequest struct {
	Targets        []taskarchive.Target `json:"targets"`
	Before         string               `json:"before"`
	Reason         string               `json:"reason"`
	IdempotencyKey string               `json:"idempotencyKey"`
	ActorPassword  string               `json:"actorPassword"`
	TOTPCode       string               `json:"totpCode"`
}

func (in archiveRequest) archive() taskarchive.ArchiveInput {
	return taskarchive.ArchiveInput{Targets: in.Targets, Before: in.Before, Reason: in.Reason, IdempotencyKey: in.IdempotencyKey}
}
func (in archiveRequest) restore() taskarchive.RestoreInput {
	return taskarchive.RestoreInput{Targets: in.Targets, Reason: in.Reason, IdempotencyKey: in.IdempotencyKey}
}

func (s *Service) ArchiveHandler(engine *taskarchive.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveArchive(w, r, engine) })
}
func archivePathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/tasks")
	route, known := archiveRoutes[suffix]
	t, a := grants["tasks"], grants["task_archive"]
	return ok && known && method == route.method && t.Readable && a.Readable && (!route.write || t.Writeable && a.Writeable)
}
func decodeArchiveRequest(w http.ResponseWriter, r *http.Request, path string, route archiveRoute) (archiveRequest, error) {
	var in archiveRequest
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
	if err != nil || !utf8.Valid(raw) {
		return in, fail(400, "invalid_input")
	}
	fields, err := resourceExactObject(raw, route.fields)
	if err != nil {
		return in, err
	}
	for _, key := range strings.Fields(route.fields) {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			return in, fail(400, "invalid_input")
		}
	}
	if err = resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return in, err
	}
	var targets []json.RawMessage
	if json.Unmarshal(fields["targets"], &targets) != nil || len(targets) < 1 || len(targets) > 100 {
		return in, fail(400, "invalid_input")
	}
	for _, target := range targets {
		values, e := resourceExactObject(target, "taskUUID visibilityVersion")
		if e != nil {
			return in, e
		}
		for _, key := range []string{"taskUUID", "visibilityVersion"} {
			if value, ok := values[key]; !ok || string(value) == "null" {
				return in, fail(400, "invalid_input")
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return in, fail(400, "invalid_input")
	}
	if path == "/archive" {
		err = taskarchive.ValidateArchive(in.archive())
	} else {
		err = taskarchive.ValidateRestore(in.restore())
	}
	return in, err
}
func (s *Service) serveArchive(w http.ResponseWriter, r *http.Request, engine *taskarchive.Engine) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/tasks")
	route, known := archiveRoutes[path]
	if !prefixed || !known {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in archiveRequest
	var filter taskarchive.Filter
	if route.write {
		if r.Header.Get("Origin") != s.origin {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || len(query) != 0 {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		in, err = decodeArchiveRequest(w, r, path, route)
	} else {
		filter, err = taskarchive.ParseFilter(query)
	}
	if err != nil {
		status, code := taskHTTPError(err)
		write(w, status, map[string]string{"error": code})
		return
	}
	if engine == nil {
		write(w, 503, map[string]string{"error": "tasks_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	value, err := s.handleArchive(ctx, r, engine, path, route, in, filter)
	if err != nil {
		status, code := taskHTTPError(err)
		if status != 503 {
			s.recordFailure(ctx, "task"+path)
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	write(w, 200, value)
}
func (s *Service) handleArchive(ctx context.Context, r *http.Request, engine *taskarchive.Engine, path string, route archiveRoute, in archiveRequest, filter taskarchive.Filter) (any, error) {
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = engine.CheckSchemaTx(ctx, tx); err != nil {
		return nil, err
	}
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err = s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, err
		}
	}
	actor, _, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !archivePathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
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
	if path == "/archive-candidates" {
		value, err = engine.CandidatesTx(ctx, tx, p, filter)
	} else {
		var result taskarchive.Result
		if path == "/archive" {
			result, err = engine.ArchiveTx(ctx, tx, p, in.archive())
		} else {
			result, err = engine.RestoreTx(ctx, tx, p, in.restore())
		}
		if err == nil && !result.Replayed {
			err = s.audit(ctx, tx, actor, "task."+result.Receipt.Action+":"+result.Receipt.ID, 0)
		}
		value = result
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return value, nil
}
