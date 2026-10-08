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
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/tasks"
)

type auditRoute struct {
	method string
	write  bool
	fields string
}

var auditRoutes = map[string]auditRoute{
	"": {"GET", false, ""}, "/types": {"GET", false, ""}, "/columns": {"GET", false, ""},
	"/delete": {"POST", true, "id reason actorPassword totpCode"}, "/restore": {"POST", true, "id reason actorPassword totpCode"},
	"/exports":         {"POST", true, "startTm endTm keyword filterEvent createSort logTypeList selectColumn idempotencyKey actorPassword totpCode"},
	"/exports/history": {"GET", false, ""},
	"/exports/detail":  {"GET", false, ""}, "/exports/download": {"GET", false, ""},
}

type auditRequest struct {
	ID             []string `json:"id"`
	Reason         string   `json:"reason"`
	StartTm        string   `json:"startTm"`
	EndTm          string   `json:"endTm"`
	Keyword        string   `json:"keyword"`
	FilterEvent    []string `json:"filterEvent"`
	CreateSort     int      `json:"createSort"`
	LogTypeList    []int    `json:"logTypeList"`
	SelectColumn   []string `json:"selectColumn"`
	IdempotencyKey string   `json:"idempotencyKey"`
	ActorPassword  string   `json:"actorPassword"`
	TOTPCode       string   `json:"totpCode"`
}

func (in auditRequest) payload() audit.ExportPayload {
	if in.FilterEvent == nil {
		in.FilterEvent = []string{}
	}
	if in.LogTypeList == nil {
		in.LogTypeList = []int{}
	}
	if in.CreateSort == 0 {
		in.CreateSort = -1
	}
	return audit.ExportPayload{StartTm: in.StartTm, EndTm: in.EndTm, Keyword: in.Keyword, FilterEvent: in.FilterEvent, CreateSort: in.CreateSort, LogTypeList: in.LogTypeList, SelectColumn: in.SelectColumn}
}
func (s *Service) AuditHandler(engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveAudit(w, r, engine) })
}
func auditPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/audit")
	route, known := auditRoutes[suffix]
	if !ok || !known || method != route.method || !grants["audit"].Readable {
		return false
	}
	if strings.HasPrefix(suffix, "/exports") {
		a := grants["audit_exports"]
		if !a.Readable || route.write && !a.Writeable {
			return false
		}
		if route.write {
			t := grants["tasks"]
			return t.Readable && t.Writeable
		}
		return true
	}
	return !route.write || grants["audit"].Writeable
}
func decodeAuditRequest(w http.ResponseWriter, r *http.Request, path string, route auditRoute) (auditRequest, error) {
	var in auditRequest
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
	if err != nil || !utf8.Valid(raw) {
		return in, fail(400, "invalid_input")
	}
	fields, err := resourceExactObject(raw, route.fields)
	if err != nil {
		return in, err
	}
	for _, v := range fields {
		if string(v) == "null" {
			return in, fail(400, "invalid_input")
		}
	}
	if err = resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return in, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&in); err != nil {
		return in, fail(400, "invalid_input")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return in, fail(400, "invalid_input")
	}
	if len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return in, fail(400, "invalid_input")
	}
	if path == "/exports" {
		for _, k := range []string{"selectColumn", "idempotencyKey"} {
			if _, ok := fields[k]; !ok {
				return in, fail(400, "invalid_input")
			}
		}
		if _, ok := fields["createSort"]; ok && in.CreateSort != 1 && in.CreateSort != -1 {
			return in, fail(400, "invalid_input")
		}
		if !validResourceID(in.IdempotencyKey) {
			return in, fail(400, "invalid_input")
		}
		raw, _ = json.Marshal(in.payload())
		if _, err = audit.ValidateExportPayload(raw); err != nil {
			return in, err
		}
	} else if err = audit.ValidateTargets(in.ID, in.Reason); err != nil {
		return in, err
	}
	return in, nil
}
func auditHTTPError(err error) (int, string) {
	var ae *audit.Error
	if errors.As(err, &ae) {
		return ae.Status, ae.Code
	}
	return taskHTTPError(err)
}
func (s *Service) serveAudit(w http.ResponseWriter, r *http.Request, engine *tasks.Engine) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/audit")
	route, known := auditRoutes[path]
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
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in auditRequest
	var f audit.Filter
	var historyFilter audit.HistoryFilter
	var taskID string
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
		in, err = decodeAuditRequest(w, r, path, route)
	} else {
		switch path {
		case "":
			f, err = audit.ParseFilter(query)
		case "/exports/history":
			historyFilter, err = audit.ParseHistoryFilter(query)
		case "/exports/detail", "/exports/download":
			taskID, err = accessSingleQuery(r, "taskUUID")
			if err == nil && !validResourceID(taskID) {
				err = fail(400, "invalid_input")
			}
		default:
			if len(query) != 0 {
				err = fail(400, "invalid_input")
			}
		}
	}
	if err != nil {
		status, code := auditHTTPError(err)
		write(w, status, map[string]string{"error": code})
		return
	}
	if engine == nil {
		write(w, 503, map[string]string{"error": "tasks_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	value, data, err := s.handleAudit(ctx, r, engine, path, route, in, f, taskID, historyFilter)
	if err != nil {
		status, code := auditHTTPError(err)
		if status != 503 {
			s.recordFailure(ctx, "audit"+path)
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	if bound, ok := value.(*credentialUseResult); ok {
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(bound.actorID, 10))
		value = bound.value
	}
	if data != nil {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="`+audit.ArtifactFilename(taskID)+`"`)
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return
	}
	write(w, 200, value)
}
func (s *Service) handleAudit(ctx context.Context, r *http.Request, engine *tasks.Engine, path string, route auditRoute, in auditRequest, f audit.Filter, taskID string, historyFilter audit.HistoryFilter) (any, []byte, error) {
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, audit.SchemaVersion); err != nil {
		return nil, nil, err
	}
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err = s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, nil, err
		}
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, nil, err
	}
	if !auditPathAllowed(r.Method, r.URL.Path, grants) || path == "" && f.Visibility != "visible" && !grants["audit"].Writeable {
		return nil, nil, fail(403, "forbidden")
	}
	if route.write {
		if err = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); err != nil {
			return nil, nil, err
		}
	}
	if err = setAuditMetadata(ctx, tx, actor); err != nil {
		return nil, nil, err
	}
	p := audit.Principal{TenantID: actor.tenant, ActorID: actor.ID, RoleID: role}
	var value any
	var data []byte
	switch path {
	case "":
		value, err = audit.ListTx(ctx, tx, p, f)
	case "/types":
		value, err = audit.TypesTx(ctx, tx, p)
	case "/columns":
		cols := audit.Columns()
		defaults := make([]string, len(cols))
		for i, c := range cols {
			defaults[i] = c.Prop
		}
		value = map[string]any{"columns": cols, "defaultColumns": defaults, "maxColumns": 8}
	case "/delete", "/restore":
		value, err = audit.ChangeVisibilityTx(ctx, tx, p, in.ID, in.Reason, path == "/delete")
	case "/exports":
		payload, _ := json.Marshal(in.payload())
		var submission tasks.Submission
		submission, err = engine.SubmitTx(ctx, tx, tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}, tasks.SubmitInput{TaskName: audit.ExportKind, DomainID: "platform", PayloadVersion: 1, Payload: payload, IdempotencyKey: in.IdempotencyKey})
		if err == nil && !submission.Replayed {
			err = audit.RecordExportTx(ctx, tx, p, submission.Task.ID)
		}
		value = map[string]any{"taskUUID": submission.Task.ID, "task": publicTask(submission.Task), "replayed": submission.Replayed}
	case "/exports/history":
		value, err = audit.HistoryTx(ctx, tx, p, historyFilter)
	case "/exports/detail":
		value, err = audit.DetailTx(ctx, tx, p, taskID)
	case "/exports/download":
		data, err = audit.ReadArtifactTx(ctx, tx, p, taskID)
	default:
		return nil, nil, fail(404, "not_found")
	}
	if err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, data, nil
}
