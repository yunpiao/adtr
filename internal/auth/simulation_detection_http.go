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
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationevents"
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

const simulationDetectionPath = "/api/simulation/detection"

var simulationDetectionRouteOrder = []string{"/datasets", "/templates", "/template", "/create", "/update", "/archive", "/run", "/task", "/receipt", "/cancel"}
var simulationDetectionRoutes = map[string]taskRoute{
	"/datasets":  {"GET", "domainId offset limit", false},
	"/templates": {"GET", "domainId", false},
	"/template":  {"GET", "domainId templateId", false},
	"/create":    {"POST", "domainId templateId name operationId rules", true},
	"/update":    {"POST", "domainId templateId name operationId rules expectedVersion", true},
	"/archive":   {"POST", "domainId templateId operationId expectedVersion", true},
	"/run":       {"POST", "domainId templateId datasetId templateVersion operationId", true},
	"/task":      {"GET", "domainId taskUUID", false},
	"/receipt":   {"GET", "domainId operationId", false},
	"/cancel":    {"POST", "domainId taskUUID operationId", true},
}

func simulationDetectionPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationDetectionPath)
	rt, known := simulationDetectionRoutes[suffix]
	g := grants["simulation_detection"]
	return ok && known && rt.method == method && g.Readable && (!rt.write || g.Writeable)
}

type simulationDetectionRule struct {
	ID         string                     `json:"id"`
	Enabled    bool                       `json:"enabled"`
	Combine    string                     `json:"combine"`
	Conditions []detectionrules.Condition `json:"conditions"`
}
type simulationDetectionInput struct {
	DomainID        string                    `json:"domainId"`
	TemplateID      string                    `json:"templateId"`
	Name            string                    `json:"name"`
	OperationID     string                    `json:"operationId"`
	ExpectedVersion int64                     `json:"expectedVersion"`
	TemplateVersion int64                     `json:"templateVersion"`
	DatasetID       string                    `json:"datasetId"`
	TaskUUID        string                    `json:"taskUUID"`
	Rules           []simulationDetectionRule `json:"rules"`
	Offset          int                       `json:"offset"`
	Limit           int                       `json:"limit"`
}

func decodeSimulationDetectionInput(w http.ResponseWriter, r *http.Request, path string) (simulationDetectionInput, error) {
	var in simulationDetectionInput
	bad := func() (simulationDetectionInput, error) { return in, fail(400, "invalid_input") }
	rt, known := simulationDetectionRoutes[path]
	if !known {
		return bad()
	}
	var raw []byte
	if rt.write {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || r.URL.RawQuery != "" {
			return bad()
		}
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
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
			if k == "offset" || k == "limit" {
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
	if !messageIdentifier(in.DomainID) {
		return bad()
	}
	for _, entry := range []struct{ key, value string }{{"templateId", in.TemplateID}, {"datasetId", in.DatasetID}, {"operationId", in.OperationID}, {"taskUUID", in.TaskUUID}} {
		if _, ok := fields[entry.key]; ok && !messageIdentifier(entry.value) {
			return bad()
		}
	}
	if (path == "/update" || path == "/archive") && in.ExpectedVersion <= 0 || path == "/run" && in.TemplateVersion <= 0 {
		return bad()
	}
	if path == "/datasets" && (in.Offset < 0 || in.Offset > securityevents.MaxEvents || in.Limit < 1 || in.Limit > simulationevents.MaxPage) {
		return bad()
	}
	if path == "/create" || path == "/update" {
		if strings.TrimSpace(in.Name) != in.Name || !validAccessText(in.Name, 128) || in.Name == "" || len(in.Rules) == 0 || len(in.Rules) > detectionrules.MaxRules {
			return bad()
		}
		// Validate client rule objects before server-owned metadata is supplied.
		var rules []json.RawMessage
		if json.Unmarshal(fields["rules"], &rules) != nil {
			return bad()
		}
		for _, rule := range rules {
			keys, e := resourceExactObject(rule, "id enabled combine conditions")
			if e != nil || len(keys) != 4 {
				return bad()
			}
			for _, v := range keys {
				if string(v) == "null" {
					return bad()
				}
			}
			var conditions []json.RawMessage
			if json.Unmarshal(keys["conditions"], &conditions) != nil {
				return bad()
			}
			for _, condition := range conditions {
				ck, e := resourceExactObject(condition, "field operator values")
				if e != nil || len(ck) != 3 {
					return bad()
				}
				for _, v := range ck {
					if string(v) == "null" {
						return bad()
					}
				}
			}
		}
		if _, err = detectionrules.Compile(detectionrules.Scope{Tenant: "validation", Domain: in.DomainID}, in.serverRules("validation", 1)); err != nil {
			return bad()
		}
	}
	return in, nil
}
func (in simulationDetectionInput) serverRules(tenant string, version uint64) []detectionrules.Rule {
	rules := make([]detectionrules.Rule, len(in.Rules))
	for i, r := range in.Rules {
		rules[i] = detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: r.ID, Version: version, Scope: detectionrules.Scope{Tenant: tenant, Domain: in.DomainID}, Enabled: r.Enabled, Combine: r.Combine, Conditions: r.Conditions}
	}
	return rules
}

// SimulationDetectionHandler is opt-in and uses only immutable synthetic datasets.
func (s *Service) SimulationDetectionHandler(enabled bool, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationDetectionPath)
		rt, known := simulationDetectionRoutes[path]
		if !ok || !known {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if !enabled {
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
		in, err := decodeSimulationDetectionInput(w, r, path)
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		if s.database == nil || engine == nil {
			write(w, 503, map[string]string{"error": "simulation_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
		out, err := s.handleSimulationDetection(ctx, r.WithContext(ctx), path, in, engine)
		if err != nil {
			status, code := simulationDetectionHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		write(w, 200, out.value)
	})
}
func simulationDetectionHTTPError(err error) (int, string) {
	var f failure
	var te *tasks.Error
	switch {
	case errors.As(err, &f):
		return f.status, f.code
	case errors.As(err, &te):
		return te.Status, te.Code
	case errors.Is(err, tasks.ErrAuthorization), errors.Is(err, simulationdetection.ErrDenied):
		return 403, "forbidden"
	case errors.Is(err, simulationdetection.ErrNotFound):
		return 404, "not_found"
	case errors.Is(err, simulationdetection.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(err, simulationdetection.ErrInvalid), errors.Is(err, detectionrules.ErrInvalid), errors.Is(err, detectionrules.ErrScope):
		return 400, "invalid_input"
	case errors.Is(err, detectionrules.ErrLimit), errors.Is(err, simulationdetection.ErrLimit):
		return 422, "simulation_limit_exceeded"
	default:
		return simulationRiskHTTPError(err)
	}
}
func (s *Service) handleSimulationDetection(ctx context.Context, r *http.Request, path string, in simulationDetectionInput, engine *tasks.Engine) (*credentialUseResult, error) {
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
		if errors.Is(err, tasks.ErrSchemaIncompatible) {
			return nil, fail(503, "schema_incompatible")
		}
		return nil, fail(503, "schema_unavailable")
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !simulationDetectionPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	scope := simulationdetection.Scope{Tenant: actor.tenant, Domain: in.DomainID, ActorID: actor.ID, Read: grants["simulation_detection"].Readable, Manage: grants["simulation_detection"].Writeable}
	audit := func(ctx context.Context, tx pgx.Tx, a simulationdetection.Audit) error {
		return s.audit(ctx, tx, actor, "simulation_detection_"+a.Action, actor.ID)
	}
	var value any
	switch path {
	case "/datasets":
		value, err = simulationevents.ListDatasetsTx(ctx, tx, securityevents.Scope{Tenant: scope.Tenant, Domain: scope.Domain, Read: scope.Read}, in.Offset, in.Limit)
	case "/templates":
		value, err = simulationdetection.ListTx(ctx, tx, scope)
	case "/template":
		value, err = simulationdetection.DetailTx(ctx, tx, scope, in.TemplateID)
	case "/create", "/update", "/archive":
		value, err = simulationdetection.MutateTx(ctx, tx, scope, simulationdetection.Mutation{ID: in.TemplateID, Name: in.Name, OperationID: in.OperationID, ExpectedVersion: in.ExpectedVersion, Rules: in.serverRules(actor.tenant, uint64(in.ExpectedVersion+1)), Archive: path == "/archive"}, audit)
	case "/run":
		value, err = simulationdetection.SubmitRunTx(ctx, tx, scope, engine, simulationdetection.RunInput{TemplateID: in.TemplateID, DatasetID: in.DatasetID, OperationID: in.OperationID, TemplateVersion: in.TemplateVersion}, audit)
	case "/task":
		value, err = simulationdetection.ReadRunTx(ctx, tx, scope, engine, in.TaskUUID)
	case "/receipt":
		value, err = simulationdetection.ReceiptTx(ctx, tx, scope, in.OperationID)
		if errors.Is(err, simulationdetection.ErrNotFound) {
			return nil, fail(404, "operation_not_found")
		}
	case "/cancel":
		value, err = simulationdetection.CancelRunTx(ctx, tx, scope, engine, in.TaskUUID, in.OperationID, audit)
	}
	if err != nil {
		return nil, err
	}
	switch path {
	case "/templates":
		value = map[string]any{"simulation": true, "templates": value}
	case "/template":
		value = map[string]any{"simulation": true, "template": value}
	case "/create", "/update", "/archive":
		value = map[string]any{"operationId": in.OperationID, "kind": strings.TrimPrefix(path, "/"), "template": value}
	case "/run":
		value = map[string]any{"operationId": in.OperationID, "kind": "run", "submission": value}
	case "/cancel":
		value = map[string]any{"operationId": in.OperationID, "kind": "cancel", "task": value}
	case "/task":
		value = map[string]any{"simulation": true, "task": value.(tasks.Detail).Task}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
