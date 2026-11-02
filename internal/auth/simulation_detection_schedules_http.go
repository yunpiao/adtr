package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	schedules "github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/tasks"
)

const simulationDetectionSchedulesPath = "/api/simulation/detection-schedules"

var simulationDetectionSchedulesRouteOrder = []string{"/list", "/detail", "/receipt", "/task", "/create", "/update", "/enable", "/pause", "/archive"}
var simulationDetectionSchedulesRoutes = map[string]taskRoute{
	"/list":    {"GET", "domainId pageIdx pageSize", false},
	"/detail":  {"GET", "domainId id pageIdx pageSize", false},
	"/receipt": {"GET", "domainId operationId", false},
	"/task":    {"GET", "domainId taskId", false},
	"/create":  {"POST", "domainId label cycle templateId templateVersion datasetId operationId", true},
	"/update":  {"POST", "domainId id label cycle templateId templateVersion datasetId operationId expectedControlVersion", true},
	"/enable":  {"POST", "domainId id operationId expectedControlVersion", true},
	"/pause":   {"POST", "domainId id operationId expectedControlVersion", true},
	"/archive": {"POST", "domainId id operationId expectedControlVersion", true},
}

func simulationDetectionSchedulesPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationDetectionSchedulesPath)
	rt, known := simulationDetectionSchedulesRoutes[suffix]
	a, b := grants["simulation_detection"], grants["simulation_detection_schedules"]
	return ok && known && rt.method == method && a.Readable && b.Readable && (!rt.write || a.Writeable && b.Writeable)
}

type simulationDetectionSchedulesInput struct {
	DomainID               string `json:"domainId"`
	ID                     string `json:"id"`
	Label                  string `json:"label"`
	Cycle                  int    `json:"cycle"`
	TemplateID             string `json:"templateId"`
	TemplateVersion        int64  `json:"templateVersion"`
	DatasetID              string `json:"datasetId"`
	OperationID            string `json:"operationId"`
	ExpectedControlVersion int64  `json:"expectedControlVersion"`
	TaskID                 string `json:"taskId"`
	PageIdx                int    `json:"pageIdx"`
	PageSize               int    `json:"pageSize"`
}

func decodeSimulationDetectionSchedulesInput(w http.ResponseWriter, r *http.Request, path string) (simulationDetectionSchedulesInput, error) {
	var in simulationDetectionSchedulesInput
	bad := func() (simulationDetectionSchedulesInput, error) { return in, fail(400, "invalid_input") }
	rt, known := simulationDetectionSchedulesRoutes[path]
	if !known {
		return bad()
	}
	var raw []byte
	if rt.write {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || r.URL.RawQuery != "" {
			return bad()
		}
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil || !utf8.Valid(raw) {
			return bad()
		}
	} else {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 2048 {
			return bad()
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return bad()
		}
		obj := map[string]any{}
		for k, v := range q {
			if len(v) != 1 || v[0] == "" {
				return bad()
			}
			if k == "pageIdx" || k == "pageSize" {
				n, e := strconv.Atoi(v[0])
				if e != nil || strconv.Itoa(n) != v[0] {
					return bad()
				}
				obj[k] = n
			} else {
				obj[k] = v[0]
			}
		}
		raw, _ = json.Marshal(obj)
	}
	fields, err := resourceExactObject(raw, rt.fields)
	if err != nil {
		return bad()
	}
	for _, k := range strings.Fields(rt.fields) {
		v, ok := fields[k]
		if !ok || string(v) == "null" {
			return bad()
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return bad()
	}
	if !messageIdentifier(in.DomainID) || in.DomainID == "platform" {
		return bad()
	}
	for _, entry := range []struct{ key, value string }{{"id", in.ID}, {"templateId", in.TemplateID}, {"datasetId", in.DatasetID}, {"operationId", in.OperationID}, {"taskId", in.TaskID}} {
		if _, ok := fields[entry.key]; ok && !messageIdentifier(entry.value) {
			return bad()
		}
	}
	if _, ok := fields["expectedControlVersion"]; ok && in.ExpectedControlVersion < 1 {
		return bad()
	}
	if path == "/list" || path == "/detail" {
		if in.PageIdx < 1 || in.PageIdx > 10000 || in.PageSize < 1 || in.PageSize > 50 {
			return bad()
		}
	}
	if path == "/create" || path == "/update" {
		if in.Label == "" || strings.TrimSpace(in.Label) != in.Label || !validAccessText(in.Label, 80) || in.Cycle < 0 || in.Cycle > 2 || in.TemplateVersion < 1 || in.TemplateVersion > simulationdetection.MaxVersions {
			return bad()
		}
	}
	return in, nil
}

// SimulationDetectionSchedulesHandler is independent and requires both opt-in
// flags. The closed routes reject rate, authority, timing and payload overrides.
func (s *Service) SimulationDetectionSchedulesHandler(manualEnabled, schedulesEnabled bool, queue *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationDetectionSchedulesPath)
		rt, known := simulationDetectionSchedulesRoutes[path]
		if !ok || !known {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if !manualEnabled || !schedulesEnabled {
			write(w, 404, map[string]string{"error": "simulation_disabled"})
			return
		}
		if r.Method != rt.method {
			w.Header().Set("Allow", rt.method)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if rt.write && (s.origin == "" || r.Header.Get("Origin") != s.origin) {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		in, err := decodeSimulationDetectionSchedulesInput(w, r, path)
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		if s.database == nil || queue == nil {
			write(w, 503, map[string]string{"error": "simulation_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
		out, err := s.handleSimulationDetectionSchedules(ctx, r.WithContext(ctx), path, in, queue)
		if err != nil {
			status, code := simulationDetectionSchedulesHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		write(w, 200, out.value)
	})
}
func simulationDetectionSchedulesHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, tasks.ErrSchemaIncompatible):
		return 503, "schema_incompatible"
	case errors.Is(err, schedules.ErrDenied):
		return 403, "forbidden"
	case errors.Is(err, schedules.ErrNotFound):
		return 404, "not_found"
	case errors.Is(err, schedules.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(err, schedules.ErrConfiguration):
		return 409, "configuration_changed"
	case errors.Is(err, schedules.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(err, schedules.ErrLimit):
		return 422, "simulation_limit_exceeded"
	default:
		return simulationDetectionHTTPError(err)
	}
}
func (s *Service) handleSimulationDetectionSchedules(ctx context.Context, r *http.Request, path string, in simulationDetectionSchedulesInput, queue *tasks.Engine) (*credentialUseResult, error) {
	conn, err := dbadmission.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		return nil, err
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !simulationDetectionSchedulesPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	engine, err := schedules.New(s.database, queue, simulationDetectionSchedulesTaskAuthorizer(s.now))
	if err != nil {
		return nil, err
	}
	scope := schedules.Scope{Tenant: actor.tenant, Domain: in.DomainID, ActorID: actor.ID}
	audit := func(ctx context.Context, tx pgx.Tx, a schedules.Audit) error {
		return s.audit(ctx, tx, actor, "simulation_detection_schedules_"+a.Action, actor.ID)
	}
	mutation := schedules.Mutation{ID: in.ID, OperationID: in.OperationID, ExpectedControlVersion: in.ExpectedControlVersion, Definition: schedules.Definition{Label: in.Label, Cycle: in.Cycle, TemplateID: in.TemplateID, TemplateVersion: in.TemplateVersion, DatasetID: in.DatasetID}}
	var value any
	switch path {
	case "/list":
		value, err = engine.ListTx(ctx, tx, scope, schedules.Filter{PageIdx: in.PageIdx, PageSize: in.PageSize})
	case "/detail":
		value, err = engine.DetailTx(ctx, tx, scope, in.ID, in.PageIdx, in.PageSize)
	case "/create":
		value, err = engine.CreateTx(ctx, tx, scope, mutation, audit)
	case "/update":
		value, err = engine.UpdateTx(ctx, tx, scope, mutation, audit)
	case "/enable", "/pause", "/archive":
		value, err = engine.ControlTx(ctx, tx, scope, schedules.ControlInput{ID: in.ID, OperationID: in.OperationID, ExpectedControlVersion: in.ExpectedControlVersion, Action: strings.TrimPrefix(path, "/")}, audit)
	case "/receipt":
		value, err = engine.ReceiptTx(ctx, tx, scope, in.OperationID)
		if errors.Is(err, schedules.ErrNotFound) {
			return nil, fail(404, "operation_not_found")
		}
	case "/task":
		var task tasks.Task
		task, err = engine.ReadRunTx(ctx, tx, scope, in.TaskID)
		value = map[string]any{"simulation": true, "task": task}
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
