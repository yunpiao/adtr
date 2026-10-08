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
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

var operationalLogRoutes = map[string]taskRoute{
	"": {"GET", "", false}, "/sources": {"GET", "", false},
	"/bundles":         {"POST", "startTm endTm systemType idempotencyKey actorPassword totpCode", true},
	"/bundles/history": {"GET", "", false}, "/bundles/detail": {"GET", "", false}, "/bundles/download": {"GET", "", false},
	"/bundles/cancel": {"POST", "taskUUID actorPassword totpCode", true},
}

type operationalLogRequest struct {
	operationallogs.BundlePayload
	IdempotencyKey string `json:"idempotencyKey"`
	TaskUUID       string `json:"taskUUID"`
	ActorPassword  string `json:"actorPassword"`
	TOTPCode       string `json:"totpCode"`
}

func (operationalLogRequest) String() string   { return "[operational log mutation]" }
func (operationalLogRequest) GoString() string { return "[operational log mutation]" }
func (operationalLogRequest) MarshalJSON() ([]byte, error) {
	return []byte(`"[operational log mutation]"`), nil
}
func operationalLogPathAllowed(method, path, tenant string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, "/api/system/logs")
	route, known := operationalLogRoutes[suffix]
	if !ok || !known || tenant != operationallogs.OwnerTenantID || method != route.method || !grants["system"].Readable || !grants["system_logs"].Readable {
		return false
	}
	return !route.write || (grants["system_logs"].Writeable && grants["tasks"].Readable && grants["tasks"].Writeable)
}
func decodeOperationalLogRequest(w http.ResponseWriter, r *http.Request, path string, route taskRoute, in *operationalLogRequest) error {
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
	if e != nil || resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return bad()
	}
	for _, key := range strings.Fields(route.fields) {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return bad()
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(in) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return bad()
	}
	for _, c := range in.TOTPCode {
		if c < '0' || c > '9' {
			return bad()
		}
	}
	if path == "/bundles/cancel" {
		if !validResourceID(in.TaskUUID) {
			return bad()
		}
		return nil
	}
	if !validResourceID(in.IdempotencyKey) {
		return bad()
	}
	_, e = operationallogs.NormalizeSelection(in.StartTm, in.EndTm, in.SystemType)
	return e
}
func operationalLogHTTPError(err error) (int, string) {
	var e *operationallogs.Error
	if errors.As(err, &e) {
		return e.Status, e.Code
	}
	return taskHTTPError(err)
}
func (s *Service) OperationalLogsHandler(engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveOperationalLogs(w, r, engine) })
}
func (s *Service) serveOperationalLogs(w http.ResponseWriter, r *http.Request, engine *tasks.Engine) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path, prefix := strings.CutPrefix(r.URL.Path, "/api/system/logs")
	route, known := operationalLogRoutes[path]
	if !prefix || !known {
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
	var in operationalLogRequest
	var filter operationallogs.Filter
	var history operationallogs.HistoryFilter
	var taskID string
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
		e = decodeOperationalLogRequest(w, r, path, route, &in)
	} else {
		switch path {
		case "":
			filter, e = operationallogs.ParseFilter(q)
		case "/bundles/history":
			history, e = operationallogs.ParseHistoryFilter(q)
		case "/bundles/detail", "/bundles/download":
			e = domainSingleQuery(q, "taskUUID", validResourceID)
			taskID = q.Get("taskUUID")
		default:
			if len(q) != 0 {
				e = fail(400, "invalid_input")
			}
		}
	}
	if e != nil {
		status, code := operationalLogHTTPError(e)
		write(w, status, map[string]string{"error": code})
		return
	}
	if s.database == nil || engine == nil {
		write(w, 503, map[string]string{"error": "schema_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	out, data, e := s.handleOperationalLogs(ctx, r, engine, path, route, in, filter, history, taskID)
	if e != nil {
		status, code := operationalLogHTTPError(e)
		if status != 503 {
			s.recordFailure(ctx, "system_logs"+strings.ReplaceAll(path, "/", "_"))
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
	if data != nil {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+operationallogs.ArtifactFilename(taskID)+`"`)
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return
	}
	write(w, 200, out.value)
}
func (s *Service) handleOperationalLogs(ctx context.Context, r *http.Request, engine *tasks.Engine, path string, route taskRoute, in operationalLogRequest, filter operationallogs.Filter, history operationallogs.HistoryFilter, taskID string) (*credentialUseResult, []byte, error) {
	conn, e := pgx.ConnectConfig(ctx, s.database.Copy())
	if e != nil {
		return nil, nil, fail(503, "schema_unavailable")
	}
	defer conn.Close(ctx)
	tx, e := conn.Begin(ctx)
	if e != nil {
		return nil, nil, fail(503, "schema_unavailable")
	}
	defer tx.Rollback(ctx)
	if e = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); e != nil {
		return nil, nil, operationalLogSchemaError(e)
	}
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if e = s.rate(ctx, "ip:"+ip, 60); e != nil {
			return nil, nil, e
		}
	}
	actor, _, grants, e := s.authenticateAccess(ctx, tx, r)
	if e != nil {
		return nil, nil, e
	}
	if !operationalLogPathAllowed(r.Method, r.URL.Path, actor.tenant, grants) {
		return nil, nil, fail(403, "forbidden")
	}
	if route.write {
		if e = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); e != nil {
			return nil, nil, e
		}
		if e = setAuditMetadata(ctx, tx, actor); e != nil {
			return nil, nil, e
		}
	}
	p := operationallogs.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	tp := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	var value any
	var data []byte
	switch path {
	case "":
		value, e = operationallogs.ListTx(ctx, tx, p, filter)
	case "/sources":
		value, e = operationallogs.SourcesTx(ctx, tx, p)
	case "/bundles/history":
		value, e = operationallogs.HistoryTx(ctx, tx, p, history)
	case "/bundles/detail":
		value, e = operationallogs.DetailTx(ctx, tx, p, taskID)
	case "/bundles/download":
		data, e = operationallogs.ReadArtifactTx(ctx, tx, p, taskID)
	case "/bundles":
		var raw []byte
		raw, e = json.Marshal(in.BundlePayload)
		if e != nil {
			return nil, nil, e
		}
		var submission tasks.Submission
		submission, e = engine.SubmitTx(ctx, tx, tp, tasks.SubmitInput{TaskName: operationallogs.BundleKindName, DomainID: "platform", PayloadVersion: 1, Payload: raw, IdempotencyKey: in.IdempotencyKey})
		if e == nil && !submission.Replayed {
			e = operationallogs.RecordBundleTx(ctx, tx, p, submission.Task.ID, "bundle_submit")
		}
		value = map[string]any{"taskUUID": submission.Task.ID, "task": publicTask(submission.Task), "replayed": submission.Replayed}
	case "/bundles/cancel":
		var detail tasks.Detail
		detail, e = engine.DetailTx(ctx, tx, tp, in.TaskUUID)
		if e == nil && detail.Task.Kind != operationallogs.BundleKindName {
			return nil, nil, fail(404, "not_found")
		}
		if e == nil {
			var task tasks.Task
			task, e = engine.CancelTx(ctx, tx, tp, in.TaskUUID)
			value = map[string]any{"task": publicTask(task)}
			if e == nil && task.State != detail.Task.State {
				e = operationallogs.RecordBundleTx(ctx, tx, p, task.ID, "bundle_cancel")
			}
		}
	default:
		return nil, nil, fail(404, "not_found")
	}
	if e != nil {
		return nil, nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, nil, e
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, data, nil
}

func operationalLogSchemaError(err error) error {
	if errors.Is(err, tasks.ErrSchemaIncompatible) {
		return fail(503, "schema_incompatible")
	}
	return fail(503, "schema_unavailable")
}
