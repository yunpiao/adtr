package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/schemaversion"
	exceptions "github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const simulationDetectionExceptionsPath = "/api/simulation/detection-exceptions"

var simulationDetectionExceptionsRouteOrder = []string{"/snapshot", "/history", "/export", "/receipt", "/evaluation", "/evaluations", "/create", "/edit", "/enable", "/disable", "/archive", "/import", "/preview", "/evaluate"}
var simulationDetectionExceptionsRoutes = map[string]taskRoute{
	"/snapshot":    {"GET", "domainId", false},
	"/history":     {"GET", "domainId revision", false},
	"/export":      {"GET", "domainId", false},
	"/receipt":     {"GET", "domainId operationId", false},
	"/evaluation":  {"GET", "domainId id", false},
	"/evaluations": {"GET", "domainId", false},
	"/create":      {"POST", "domainId operationId expectedRevision declaration", true},
	"/edit":        {"POST", "domainId operationId expectedRevision declaration", true},
	"/enable":      {"POST", "domainId operationId expectedRevision id", true},
	"/disable":     {"POST", "domainId operationId expectedRevision id", true},
	"/archive":     {"POST", "domainId operationId expectedRevision id", true},
	"/import":      {"POST", "domainId operationId expectedRevision entries", true},
	"/preview":     {"POST", "domainId sourceTaskId eventId ruleId id reason notBefore expiresAt", true},
	"/evaluate":    {"POST", "domainId operationId expectedRevision id sourceTaskId", true},
}

func simulationDetectionExceptionsSourceRoute(path string) bool {
	return slices.Contains([]string{"/preview", "/evaluate", "/evaluation", "/evaluations"}, path)
}
func simulationDetectionExceptionsPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationDetectionExceptionsPath)
	rt, known := simulationDetectionExceptionsRoutes[suffix]
	g := grants["simulation_detection_exceptions"]
	if !ok || !known || method != rt.method || !g.Readable || (rt.write && !g.Writeable) {
		return false
	}
	// Receipt authorization is data-dependent: a configuration-only receipt does
	// not acquire task read privileges. Source-bearing receipts recheck below.
	if simulationDetectionExceptionsSourceRoute(suffix) {
		return grants["simulation_detection"].Readable && grants["simulation_events"].Readable
	}
	return true
}

type simulationDetectionExceptionsInput struct {
	DomainID         string                   `json:"domainId"`
	ID               string                   `json:"id"`
	Revision         uint64                   `json:"revision"`
	OperationID      string                   `json:"operationId"`
	ExpectedRevision uint64                   `json:"expectedRevision"`
	Declaration      *exceptions.Declaration  `json:"declaration"`
	Entries          []exceptions.Declaration `json:"entries"`
	SourceTaskID     string                   `json:"sourceTaskId"`
	EventID          string                   `json:"eventId"`
	RuleID           string                   `json:"ruleId"`
	Reason           string                   `json:"reason"`
	NotBefore        time.Time                `json:"notBefore"`
	ExpiresAt        time.Time                `json:"expiresAt"`
}

func decodeSimulationDetectionExceptionsInput(w http.ResponseWriter, r *http.Request, path string) (simulationDetectionExceptionsInput, error) {
	var in simulationDetectionExceptionsInput
	bad := func() (simulationDetectionExceptionsInput, error) { return in, fail(400, "invalid_input") }
	rt, ok := simulationDetectionExceptionsRoutes[path]
	if !ok || rt.method != r.Method {
		return bad()
	}
	var raw []byte
	if rt.write {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || r.URL.RawQuery != "" {
			return bad()
		}
		raw, e = io.ReadAll(http.MaxBytesReader(w, r.Body, exceptions.MaxRecordBytes))
		if e != nil || !utf8.Valid(raw) {
			return bad()
		}
	} else {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 4096 {
			return bad()
		}
		q, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil {
			return bad()
		}
		obj := map[string]any{}
		for k, v := range q {
			if len(v) != 1 || !utf8.ValidString(v[0]) {
				return bad()
			}
			if k == "revision" {
				n, e := strconv.ParseUint(v[0], 10, 64)
				if e != nil || strconv.FormatUint(n, 10) != v[0] {
					return bad()
				}
				obj[k] = n
			} else {
				obj[k] = v[0]
			}
		}
		raw, _ = json.Marshal(obj)
	}
	fields, e := simulationLabelsExact(raw, rt.fields)
	if e != nil {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return bad()
	}
	if !messageIdentifier(in.DomainID) || in.DomainID == "platform" {
		return bad()
	}
	for _, v := range []struct{ k, v string }{{"id", in.ID}, {"operationId", in.OperationID}, {"sourceTaskId", in.SourceTaskID}, {"eventId", in.EventID}, {"ruleId", in.RuleID}} {
		if _, ok := fields[v.k]; ok && !messageIdentifier(v.v) {
			return bad()
		}
	}
	if _, ok := fields["expectedRevision"]; ok && (in.ExpectedRevision < 1 || in.ExpectedRevision > exceptions.MaxRevisions) {
		return bad()
	}
	if path == "/history" && (in.Revision < 1 || in.Revision > exceptions.MaxRevisions) {
		return bad()
	}
	if v, ok := fields["declaration"]; ok {
		if in.Declaration == nil || !validSimulationExceptionDeclaration(v, *in.Declaration) {
			return bad()
		}
	}
	if v, ok := fields["entries"]; ok {
		var arr []json.RawMessage
		if json.Unmarshal(v, &arr) != nil || len(arr) < 1 || len(arr) > exceptions.MaxEntries || len(arr) != len(in.Entries) {
			return bad()
		}
		ids := map[string]bool{}
		for i, a := range arr {
			if !validSimulationExceptionDeclaration(a, in.Entries[i]) || ids[in.Entries[i].ID] {
				return bad()
			}
			ids[in.Entries[i].ID] = true
		}
	}
	if path == "/preview" && (!simulationLabelsText(in.Reason, 512, false) || !validSimulationExceptionTimes(in.NotBefore, in.ExpiresAt)) {
		return bad()
	}
	return in, nil
}
func validSimulationExceptionTimes(a, b time.Time) bool {
	return !a.IsZero() && !b.IsZero() && a.Before(b)
}
func validSimulationExceptionDeclaration(raw []byte, d exceptions.Declaration) bool {
	f, e := simulationLabelsExact(raw, "id global sourceTaskId ruleId reason notBefore expiresAt conditions")
	if e != nil || !messageIdentifier(d.ID) || !simulationLabelsText(d.Reason, 512, false) || !validSimulationExceptionTimes(d.NotBefore, d.ExpiresAt) {
		return false
	}
	if d.Global {
		if d.SourceTaskID != "" || d.RuleID != "" {
			return false
		}
	} else if !messageIdentifier(d.SourceTaskID) || !messageIdentifier(d.RuleID) {
		return false
	}
	var arr []json.RawMessage
	if json.Unmarshal(f["conditions"], &arr) != nil || len(arr) < 1 || len(arr) > detectionrules.MaxConditions || len(arr) != len(d.Conditions) {
		return false
	}
	for i, raw := range arr {
		cf, e := simulationLabelsExact(raw, "field operator values")
		if e != nil {
			return false
		}
		c := d.Conditions[i]
		if !slices.Contains([]string{"event.category", "event.outcome", "actor.name", "target.name", "source.ip"}, c.Field) || !slices.Contains([]string{"eq", "in"}, c.Operator) {
			return false
		}
		var values []json.RawMessage
		if json.Unmarshal(cf["values"], &values) != nil || len(values) < 1 || len(values) > detectionrules.MaxValues || (c.Operator == "eq" && len(values) != 1) {
			return false
		}
		for _, v := range values {
			if string(v) == "null" {
				return false
			}
		}
		for _, v := range c.Values {
			if !simulationLabelsText(v, 512, true) {
				return false
			}
		}
	}
	return true
}
func (s *Service) SimulationDetectionExceptionsHandler(enabled, detectionEnabled, eventsEnabled bool, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationDetectionExceptionsPath)
		rt, known := simulationDetectionExceptionsRoutes[path]
		if !ok || !known {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if !enabled || (simulationDetectionExceptionsSourceRoute(path) && (!detectionEnabled || !eventsEnabled)) {
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
		in, e := decodeSimulationDetectionExceptionsInput(w, r, path)
		if e != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		if s.database == nil {
			write(w, 503, map[string]string{"error": "simulation_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
		out, e := s.handleSimulationDetectionExceptions(ctx, r.WithContext(ctx), path, in, detectionEnabled, eventsEnabled, engine)
		if e != nil {
			status, code := simulationDetectionExceptionsHTTPError(e)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		if path == "/export" {
			w.Header().Set("Content-Disposition", `attachment; filename="simulation-detection-exceptions.json"`)
		}
		write(w, 200, out.value)
	})
}
func simulationDetectionExceptionsHTTPError(e error) (int, string) {
	switch {
	case errors.Is(e, tasks.ErrSchemaIncompatible):
		return 503, "schema_incompatible"
	case errors.Is(e, exceptions.ErrDenied):
		return 403, "forbidden"
	case errors.Is(e, exceptions.ErrNotFound):
		return 404, "not_found"
	case errors.Is(e, exceptions.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(e, exceptions.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(e, exceptions.ErrLimit):
		return 422, "simulation_limit_exceeded"
	}
	return simulationDetectionHTTPError(e)
}
func simulationDetectionExceptionsScope(actor User, domain string, g map[string]AccessAuth, detectionEnabled, eventsEnabled bool) exceptions.Scope {
	p := g["simulation_detection_exceptions"]
	return exceptions.Scope{Tenant: actor.tenant, Domain: domain, ActorID: actor.ID, Read: p.Readable, Manage: p.Readable && p.Writeable, DetectionRead: g["simulation_detection"].Readable && detectionEnabled, EventsRead: g["simulation_events"].Readable && eventsEnabled}
}
func (s *Service) handleSimulationDetectionExceptions(ctx context.Context, r *http.Request, path string, in simulationDetectionExceptionsInput, detectionEnabled, eventsEnabled bool, engine *tasks.Engine) (*credentialUseResult, error) {
	conn, e := dbadmission.ConnectConfig(ctx, s.database.Copy())
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
	actor, role, g, e := s.authenticateAccess(ctx, tx, r)
	if e != nil {
		return nil, e
	}
	if !simulationDetectionExceptionsPathAllowed(r.Method, r.URL.Path, g) {
		return nil, fail(403, "forbidden")
	}
	ids, e := s.effectiveResourceIDs(ctx, tx, actor, role)
	if e != nil {
		return nil, e
	}
	if !slices.Contains(ids, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	scope := simulationDetectionExceptionsScope(actor, in.DomainID, g, detectionEnabled, eventsEnabled)
	audit := func(ctx context.Context, tx pgx.Tx, receipt exceptions.Receipt) error {
		return s.audit(ctx, tx, actor, "simulation_detection_exceptions_"+receipt.Action, actor.ID)
	}
	var value any
	switch path {
	case "/snapshot":
		value, e = exceptions.SnapshotTx(ctx, tx, scope)
	case "/history":
		value, e = exceptions.HistoryTx(ctx, tx, scope, in.Revision)
	case "/export":
		value, e = exceptions.ExportTx(ctx, tx, scope)
	case "/receipt":
		value, e = exceptions.ReceiptTx(ctx, tx, scope, engine, in.OperationID)
		if errors.Is(e, exceptions.ErrNotFound) {
			return nil, fail(404, "operation_not_found")
		}
	case "/evaluation":
		value, e = exceptions.EvaluationTx(ctx, tx, scope, engine, in.ID)
	case "/evaluations":
		value, e = exceptions.EvaluationsTx(ctx, tx, scope, engine)
	case "/preview":
		value, e = exceptions.PreviewTx(ctx, tx, scope, engine, exceptions.PreviewInput{SourceTaskID: in.SourceTaskID, EventID: in.EventID, RuleID: in.RuleID, ID: in.ID, Reason: in.Reason, NotBefore: in.NotBefore, ExpiresAt: in.ExpiresAt})
	case "/evaluate":
		value, e = exceptions.EvaluateTx(ctx, tx, scope, engine, exceptions.EvaluationInput{ID: in.ID, OperationID: in.OperationID, SourceTaskID: in.SourceTaskID, ExpectedRevision: in.ExpectedRevision}, audit)
	default:
		value, e = exceptions.MutateTx(ctx, tx, scope, engine, exceptions.Mutation{Action: strings.TrimPrefix(path, "/"), OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, ID: in.ID, Declaration: in.Declaration, Entries: in.Entries}, audit)
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
