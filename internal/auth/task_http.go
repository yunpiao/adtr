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
	"github.com/yunpiao/adtr/internal/tasks"
)

type taskRoute struct {
	method, fields string
	write          bool
}

var taskRoutes = map[string]taskRoute{
	"/kinds":   {"GET", "", false},
	"":         {"GET", "", false},
	"/detail":  {"GET", "", false},
	"/submit":  {"POST", "taskName domainId payloadVersion payload idempotencyKey actorPassword totpCode", true},
	"/cancel":  {"POST", "taskUUID actorPassword totpCode", true},
	"/recover": {"POST", "taskUUID idempotencyKey actorPassword totpCode", true},
}

type taskRequest struct {
	TaskName       string          `json:"taskName"`
	DomainID       string          `json:"domainId"`
	PayloadVersion int             `json:"payloadVersion"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotencyKey"`
	TaskUUID       string          `json:"taskUUID"`
	ActorPassword  string          `json:"actorPassword"`
	TOTPCode       string          `json:"totpCode"`
}

// TasksHandler shares the existing identity/proof service; no HTTP assertion can
// set Principal, registered scope policy, authorization epoch, retries or lease.
func (s *Service) TasksHandler(engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveTasks(w, r, engine) })
}

// taskPathAllowed is used by /api/access/check and menu metadata. It deliberately
// accepts exact registry paths only, just as the actual handler does.
func taskPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/tasks")
	rt, known := taskRoutes[suffix]
	a := grants["tasks"]
	return ok && known && method == rt.method && a.Readable && (!rt.write || a.Writeable)
}
func (s *Service) serveTasks(w http.ResponseWriter, r *http.Request, engine *tasks.Engine) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, &auditContext{tenant: "system"})
	r = r.WithContext(ctx)
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/tasks")
	route, known := taskRoutes[path]
	if !prefixed || !known {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in taskRequest
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
		if err = decodeTaskRequest(w, r, route, &in); err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
	}
	// Validate URL input before database/proof work. A malformed request consumes
	// no TOTP and cannot accidentally run an operation with silently ignored input.
	var filter tasks.Filter
	var err error
	switch path {
	case "":
		filter, err = parseTaskFilter(r.URL.Query())
	case "/detail":
		_, err = accessSingleQuery(r, "taskUUID")
	default:
		if len(r.URL.Query()) != 0 {
			err = fail(400, "invalid_input")
		}
	}
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if engine == nil {
		write(w, 503, map[string]string{"error": "tasks_unavailable"})
		return
	}
	value, err := s.handleTask(ctx, r, engine, path, route, in, filter)
	if err != nil {
		status, code := taskHTTPError(err)
		// An unavailable/incompatible schema is rejected before authentication.
		// Do not attempt an audit write against that incompatible schema either.
		if status != http.StatusServiceUnavailable {
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
func taskHTTPError(err error) (int, string) {
	var f failure
	var e *tasks.Error
	switch {
	case errors.As(err, &f):
		return f.status, f.code
	case errors.As(err, &e):
		return e.Status, e.Code
	case errors.Is(err, tasks.ErrAuthorization):
		return 403, "forbidden"
	default:
		return 500, "internal"
	}
}
func decodeTaskRequest(w http.ResponseWriter, r *http.Request, route taskRoute, in *taskRequest) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
	if err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return fail(400, "invalid_input")
	}
	fields, err := resourceExactObject(raw, route.fields)
	if err != nil {
		return err
	}
	for _, key := range strings.Fields(route.fields) {
		v, ok := fields[key]
		if !ok || string(v) == "null" {
			return fail(400, "invalid_input")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(in); err != nil {
		return err
	}
	if err = d.Decode(&struct{}{}); err != io.EOF {
		return fail(400, "invalid_input")
	}
	if len(in.ActorPassword) > 256 || len(in.ActorPassword) == 0 || len(in.TOTPCode) != 6 {
		return fail(400, "invalid_input")
	}
	return resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw)))
}
func parseTaskFilter(q url.Values) (tasks.Filter, error) {
	f := tasks.Filter{PageIdx: 1, PageSize: 20}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return f, fail(400, "invalid_input")
		}
		v := values[0]
		switch key {
		case "pageIdx", "pageSize":
			n, err := strconv.Atoi(v)
			if err != nil || strconv.Itoa(n) != v || n < 1 || key == "pageIdx" && n > 1000000 || key == "pageSize" && n > 100 {
				return f, fail(400, "invalid_input")
			}
			if key == "pageIdx" {
				f.PageIdx = n
			} else {
				f.PageSize = n
			}
		case "domainId":
			if !validResourceID(v) {
				return f, fail(400, "invalid_input")
			}
			f.DomainID = v
		case "taskName":
			if len(v) > 64 || !utf8.ValidString(v) {
				return f, fail(400, "invalid_input")
			}
			f.TaskName = v
		case "state":
			f.State = tasks.State(v)
			if !f.State.Valid() {
				return f, fail(400, "invalid_input")
			}
		default:
			return f, fail(400, "invalid_input")
		}
	}
	return f, nil
}
func (s *Service) handleTask(ctx context.Context, r *http.Request, engine *tasks.Engine, path string, route taskRoute, in taskRequest, filter tasks.Filter) (any, error) {
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
	// Schema compatibility and the migration lock precede every identity/task
	// lock, so a rolling upgrade cannot race a task mutation or reverse order.
	if err = engine.CheckSchemaTx(ctx, tx); err != nil {
		return nil, err
	}
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err := s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, err
		}
	}
	actor, _, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !taskPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	if route.write {
		if err = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); err != nil {
			return nil, err
		}
	}
	principal := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	var value any
	switch path {
	case "/kinds":
		value = map[string]any{"kinds": engine.Kinds()}
	case "":
		value, err = engine.ListTx(ctx, tx, principal, filter)
	case "/detail":
		value, err = engine.DetailTx(ctx, tx, principal, r.URL.Query().Get("taskUUID"))
	case "/submit":
		value, err = engine.SubmitTx(ctx, tx, principal, tasks.SubmitInput{TaskName: in.TaskName, DomainID: in.DomainID, PayloadVersion: in.PayloadVersion, Payload: in.Payload, IdempotencyKey: in.IdempotencyKey})
	case "/cancel":
		var task tasks.Task
		task, err = engine.CancelTx(ctx, tx, principal, in.TaskUUID)
		value = map[string]any{"task": task}
	case "/recover":
		value, err = engine.RecoverTx(ctx, tx, principal, in.TaskUUID, in.IdempotencyKey)
	default:
		return nil, fail(404, "not_found")
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return value, nil
}
