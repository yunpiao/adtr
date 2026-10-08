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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/tasks"
)

var scheduleRoutes = map[string]taskRoute{
	"":        {"GET", "", false},
	"/detail": {"GET", "", false},
	"/create": {"POST", "label taskName domainId payloadVersion payload startAt intervalSeconds idempotencyKey actorPassword totpCode", true},
	"/enable": {"POST", "scheduleUUID expectedControlVersion idempotencyKey actorPassword totpCode", true},
	"/pause":  {"POST", "scheduleUUID expectedControlVersion idempotencyKey actorPassword totpCode", true},
}

type scheduleRequest struct {
	Label                  string          `json:"label"`
	TaskName               string          `json:"taskName"`
	DomainID               string          `json:"domainId"`
	PayloadVersion         int             `json:"payloadVersion"`
	Payload                json.RawMessage `json:"payload"`
	StartAt                string          `json:"startAt"`
	IntervalSeconds        int64           `json:"intervalSeconds"`
	IdempotencyKey         string          `json:"idempotencyKey"`
	ScheduleID             string          `json:"scheduleUUID"`
	ExpectedControlVersion int64           `json:"expectedControlVersion"`
	ActorPassword          string          `json:"actorPassword"`
	TOTPCode               string          `json:"totpCode"`
}

func (s *Service) SchedulesHandler(engine *schedules.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveSchedules(w, r, engine) })
}
func schedulePathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/tasks/schedules")
	route, known := scheduleRoutes[suffix]
	t, g := grants["tasks"], grants["schedules"]
	return ok && known && route.method == method && t.Readable && g.Readable && (!route.write || t.Writeable && g.Writeable)
}
func decodeScheduleRequest(w http.ResponseWriter, r *http.Request, route taskRoute) (scheduleRequest, error) {
	var in scheduleRequest
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
	if err != nil || !utf8.Valid(raw) {
		return in, fail(400, "invalid_input")
	}
	fields, err := resourceExactObject(raw, route.fields)
	if err != nil {
		return in, err
	}
	for _, key := range strings.Fields(route.fields) {
		v, ok := fields[key]
		if !ok || string(v) == "null" {
			return in, fail(400, "invalid_input")
		}
	}
	if err = resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return in, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&in); err != nil {
		return in, err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return in, fail(400, "invalid_input")
	}
	if len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return in, fail(400, "invalid_input")
	}
	return in, nil
}
func parseScheduleFilter(q url.Values, detail bool) (schedules.Filter, string, error) {
	f := schedules.Filter{PageIdx: 1, PageSize: 20}
	id := ""
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return f, id, fail(400, "invalid_input")
		}
		v := values[0]
		switch key {
		case "pageIdx", "pageSize":
			n, err := strconv.Atoi(v)
			if err != nil || strconv.Itoa(n) != v || n < 1 || key == "pageIdx" && n > 1000000 || key == "pageSize" && n > 100 {
				return f, id, fail(400, "invalid_input")
			}
			if key == "pageIdx" {
				f.PageIdx = n
			} else {
				f.PageSize = n
			}
		case "state":
			f.State = schedules.State(v)
			if detail || !f.State.Valid() {
				return f, id, fail(400, "invalid_input")
			}
		case "scheduleUUID":
			if !detail || !validResourceID(v) {
				return f, id, fail(400, "invalid_input")
			}
			id = v
		default:
			return f, id, fail(400, "invalid_input")
		}
	}
	if detail && id == "" {
		return f, id, fail(400, "invalid_input")
	}
	return f, id, nil
}
func (s *Service) serveSchedules(w http.ResponseWriter, r *http.Request, engine *schedules.Engine) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/tasks/schedules")
	route, known := scheduleRoutes[path]
	if !prefixed || !known {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in scheduleRequest
	var f schedules.Filter
	var id string
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
		in, err = decodeScheduleRequest(w, r, route)
		if len(q) != 0 {
			err = fail(400, "invalid_input")
		}
	} else {
		f, id, err = parseScheduleFilter(q, path == "/detail")
	}
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if engine == nil {
		write(w, 503, map[string]string{"error": "schedules_unavailable"})
		return
	}
	value, err := s.handleSchedule(ctx, r, engine, path, route, in, f, id)
	if err != nil {
		status, code := taskHTTPError(err)
		if status != 503 {
			s.recordFailure(ctx, "schedule"+path)
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	write(w, 200, value)
}
func (s *Service) handleSchedule(ctx context.Context, r *http.Request, engine *schedules.Engine, path string, route taskRoute, in scheduleRequest, f schedules.Filter, id string) (any, error) {
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
	if !schedulePathAllowed(r.Method, r.URL.Path, grants) {
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
	switch path {
	case "":
		value, err = engine.ListTx(ctx, tx, p, f)
	case "/detail":
		value, err = engine.DetailTx(ctx, tx, p, id, f)
	case "/create":
		value, err = engine.CreateTx(ctx, tx, p, schedules.CreateInput{Label: in.Label, TaskName: in.TaskName, DomainID: in.DomainID, PayloadVersion: in.PayloadVersion, Payload: in.Payload, StartAt: in.StartAt, IntervalSeconds: in.IntervalSeconds, IdempotencyKey: in.IdempotencyKey})
	case "/enable", "/pause":
		value, err = engine.ControlTx(ctx, tx, p, strings.TrimPrefix(path, "/"), schedules.ControlInput{ScheduleID: in.ScheduleID, ExpectedControlVersion: in.ExpectedControlVersion, IdempotencyKey: in.IdempotencyKey})
	default:
		return nil, fail(404, "not_found")
	}
	if err != nil {
		return nil, err
	}
	var auditID string
	switch result := value.(type) {
	case schedules.Creation:
		if !result.Replayed {
			auditID = result.Schedule.ID
		}
	case schedules.Control:
		if !result.Replayed {
			auditID = result.Schedule.ID
		}
	}
	if auditID != "" {
		if err = s.audit(ctx, tx, actor, "schedule."+strings.TrimPrefix(path, "/")+":"+auditID, actor.ID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return value, nil
}
