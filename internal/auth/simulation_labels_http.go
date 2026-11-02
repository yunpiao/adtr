package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/schemaversion"
	labels "github.com/yunpiao/adtr/internal/simulationlabels"
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

const simulationLabelsPath = "/api/simulation/labels"

var simulationLabelsRouteOrder = []string{"/datasets", "/observations", "/observation", "/policies", "/policy", "/history", "/evaluations", "/evaluation", "/assets", "/asset", "/comparisons", "/comparison", "/operation", "/create", "/update", "/archive", "/restore", "/evaluate", "/compare"}
var simulationLabelsRoutes = map[string]taskRoute{
	"/datasets":     {"GET", "domainId offset limit", false},
	"/observations": {"GET", "domainId datasetId offset limit", false},
	"/observation":  {"GET", "domainId datasetId observationId", false},
	"/policies":     {"GET", "domainId datasetId offset limit", false},
	"/policy":       {"GET", "domainId datasetId policyId", false},
	"/history":      {"GET", "domainId datasetId policyId offset limit", false},
	"/evaluations":  {"GET", "domainId datasetId offset limit", false},
	"/evaluation":   {"GET", "domainId datasetId evaluationId", false},
	"/assets":       {"GET", "domainId datasetId evaluationId kind name offset limit", false},
	"/asset":        {"GET", "domainId datasetId evaluationId objectId", false},
	"/comparisons":  {"GET", "domainId datasetId offset limit", false},
	"/comparison":   {"GET", "domainId datasetId comparisonId", false},
	"/operation":    {"GET", "domainId datasetId operationId", false},
	"/create":       {"POST", "domainId datasetId policyId expectedRevision spec operationId reason", true},
	"/update":       {"POST", "domainId datasetId policyId expectedRevision spec operationId reason", true},
	"/archive":      {"POST", "domainId datasetId policyId expectedRevision operationId reason", true},
	"/restore":      {"POST", "domainId datasetId policyId expectedRevision operationId reason", true},
	"/evaluate":     {"POST", "domainId datasetId policyId observationId expectedRevision operationId reason", true},
	"/compare":      {"POST", "domainId datasetId beforeId afterId operationId reason", true},
}

func simulationLabelsPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationLabelsPath)
	rt, known := simulationLabelsRoutes[suffix]
	g := grants["simulation_labels"]
	return ok && known && method == rt.method && g.Readable && (!rt.write || g.Writeable)
}

type simulationLabelsInput struct {
	DomainID         string            `json:"domainId"`
	DatasetID        string            `json:"datasetId"`
	PolicyID         string            `json:"policyId"`
	ObservationID    string            `json:"observationId"`
	EvaluationID     string            `json:"evaluationId"`
	ComparisonID     string            `json:"comparisonId"`
	ObjectID         string            `json:"objectId"`
	OperationID      string            `json:"operationId"`
	BeforeID         string            `json:"beforeId"`
	AfterID          string            `json:"afterId"`
	Reason           string            `json:"reason"`
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	Offset           int               `json:"offset"`
	Limit            int               `json:"limit"`
	ExpectedRevision uint64            `json:"expectedRevision"`
	Spec             labels.PolicySpec `json:"spec"`
}

func simulationLabelsText(s string, max int, empty bool) bool {
	return (empty || s != "") && len(s) <= max && strings.TrimSpace(s) == s && validAccessText(s, max)
}
func simulationLabelsExact(raw []byte, keys string) (map[string]json.RawMessage, error) {
	fields, e := resourceExactObject(raw, keys)
	if e != nil {
		return nil, e
	}
	for _, k := range strings.Fields(keys) {
		v, ok := fields[k]
		if !ok || string(v) == "null" {
			return nil, fail(400, "invalid_input")
		}
	}
	return fields, nil
}
func decodeSimulationLabelsInput(w http.ResponseWriter, r *http.Request, path string) (simulationLabelsInput, error) {
	var in simulationLabelsInput
	bad := func() (simulationLabelsInput, error) { return in, fail(400, "invalid_input") }
	rt, ok := simulationLabelsRoutes[path]
	if !ok || r.Method != rt.method {
		return bad()
	}
	var raw []byte
	if rt.write {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || r.URL.RawQuery != "" {
			return bad()
		}
		raw, e = io.ReadAll(http.MaxBytesReader(w, r.Body, 196608))
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
	for _, v := range []struct{ k, v string }{{"datasetId", in.DatasetID}, {"policyId", in.PolicyID}, {"observationId", in.ObservationID}, {"evaluationId", in.EvaluationID}, {"comparisonId", in.ComparisonID}, {"operationId", in.OperationID}, {"beforeId", in.BeforeID}, {"afterId", in.AfterID}, {"objectId", in.ObjectID}} {
		if _, ok := fields[v.k]; ok && !simulationLabelsText(v.v, 128, false) {
			return bad()
		}
	}
	if _, ok := fields["limit"]; ok && (in.Offset < 0 || in.Offset > 10000 || in.Limit < 1 || in.Limit > labels.MaxPage) {
		return bad()
	}
	if _, ok := fields["expectedRevision"]; ok {
		if path == "/create" && in.ExpectedRevision != 0 || path != "/create" && (in.ExpectedRevision < 1 || in.ExpectedRevision > labels.MaxRevisions) {
			return bad()
		}
	}
	if rt.write && !simulationLabelsText(in.Reason, 512, false) {
		return bad()
	}
	if path == "/assets" && (!slices.Contains([]string{"", "user", "group", "computer"}, in.Kind) || !simulationLabelsText(in.Name, 256, true)) {
		return bad()
	}
	if rawSpec, ok := fields["spec"]; ok {
		spec, e := simulationLabelsExact(rawSpec, "name sensitiveGroupIds manualLabels")
		if e != nil || !simulationLabelsText(in.Spec.Name, 256, false) || len(in.Spec.SensitiveGroupIDs) > labels.MaxGroups || len(in.Spec.ManualLabels) > labels.MaxManualLabels {
			return bad()
		}
		var manual []json.RawMessage
		if json.Unmarshal(spec["manualLabels"], &manual) != nil || manual == nil || in.Spec.SensitiveGroupIDs == nil {
			return bad()
		}
		seen := map[string]bool{}
		for _, id := range in.Spec.SensitiveGroupIDs {
			if !simulationLabelsText(id, 256, false) || seen[id] {
				return bad()
			}
			seen[id] = true
		}
		seen = map[string]bool{}
		for i, m := range in.Spec.ManualLabels {
			if _, e := simulationLabelsExact(manual[i], "objectId label"); e != nil || !simulationLabelsText(m.ObjectID, 256, false) || !simulationLabelsText(m.Label, 256, false) {
				return bad()
			}
			key := m.ObjectID + "\x00" + m.Label
			if seen[key] {
				return bad()
			}
			seen[key] = true
		}
	}
	return in, nil
}

// SimulationLabelsHandler exposes only bounded synthetic policy operations. Fixture
// ingestion is deliberately absent: observations are trusted server-side inputs.
func (s *Service) SimulationLabelsHandler(enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationLabelsPath)
		rt, known := simulationLabelsRoutes[path]
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
		in, e := decodeSimulationLabelsInput(w, r, path)
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
		out, e := s.handleSimulationLabels(ctx, r.WithContext(ctx), path, in)
		if e != nil {
			status, code := simulationLabelsHTTPError(e)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		write(w, 200, out.value)
	})
}
func simulationLabelsHTTPError(e error) (int, string) {
	switch {
	case errors.Is(e, tasks.ErrSchemaIncompatible):
		return 503, "schema_incompatible"
	case errors.Is(e, labels.ErrDenied):
		return 403, "forbidden"
	case errors.Is(e, labels.ErrNotFound):
		return 404, "not_found"
	case errors.Is(e, labels.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(e, labels.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(e, labels.ErrPageTooLarge):
		return 422, "simulation_page_too_large"
	case errors.Is(e, labels.ErrLimit):
		return 422, "simulation_limit_exceeded"
	case errors.Is(e, labels.ErrIncomplete):
		return 409, "observation_incomplete"
	case errors.Is(e, labels.ErrIncompatible):
		return 409, "observations_incompatible"
	default:
		return simulationDetectionHTTPError(e)
	}
}
func (s *Service) handleSimulationLabels(ctx context.Context, r *http.Request, path string, in simulationLabelsInput) (*credentialUseResult, error) {
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
	actor, role, grants, e := s.authenticateAccess(ctx, tx, r)
	if e != nil {
		return nil, e
	}
	if !simulationLabelsPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	allowed, e := s.effectiveResourceIDs(ctx, tx, actor, role)
	if e != nil {
		return nil, e
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	g := grants["simulation_labels"]
	scope := labels.Scope{Tenant: actor.tenant, Domain: in.DomainID, Read: g.Readable, Manage: g.Writeable}
	actorID := strconv.FormatInt(actor.ID, 10)
	audit := func(ctx context.Context, tx pgx.Tx, receipt labels.Receipt) error {
		return s.audit(ctx, tx, actor, "simulation_labels_"+receipt.Action, actor.ID)
	}
	var value any
	switch path {
	case "/datasets":
		value, e = labels.DatasetsTx(ctx, tx, scope, in.Offset, in.Limit)
	case "/observations":
		value, e = labels.ObservationsTx(ctx, tx, scope, in.DatasetID, in.Offset, in.Limit)
	case "/observation":
		value, e = labels.ObservationTx(ctx, tx, scope, in.DatasetID, in.ObservationID)
	case "/policies":
		value, e = labels.PoliciesTx(ctx, tx, scope, in.DatasetID, in.Offset, in.Limit)
	case "/policy":
		value, e = labels.PolicyTx(ctx, tx, scope, in.DatasetID, in.PolicyID)
	case "/history":
		value, e = labels.PolicyHistoryTx(ctx, tx, scope, in.DatasetID, in.PolicyID, in.Offset, in.Limit)
	case "/evaluations":
		value, e = labels.EvaluationsTx(ctx, tx, scope, in.DatasetID, in.Offset, in.Limit)
	case "/evaluation":
		value, e = labels.EvaluationTx(ctx, tx, scope, in.DatasetID, in.EvaluationID)
	case "/assets":
		value, e = labels.AssetsTx(ctx, tx, scope, in.DatasetID, in.EvaluationID, labels.AssetFilter{Kind: in.Kind, Name: in.Name, Offset: in.Offset, Limit: in.Limit})
	case "/asset":
		value, e = labels.AssetTx(ctx, tx, scope, in.DatasetID, in.EvaluationID, in.ObjectID)
	case "/comparisons":
		value, e = labels.ComparisonsTx(ctx, tx, scope, in.DatasetID, in.Offset, in.Limit)
	case "/comparison":
		value, e = labels.ComparisonTx(ctx, tx, scope, in.DatasetID, in.ComparisonID)
	case "/operation":
		value, e = labels.OperationTx(ctx, tx, scope, in.DatasetID, actorID, in.OperationID)
		if errors.Is(e, labels.ErrNotFound) {
			return nil, fail(404, "operation_not_found")
		}
	case "/create", "/update", "/archive", "/restore":
		value, e = labels.MutatePolicyTx(ctx, tx, scope, labels.PolicyInput{DatasetID: in.DatasetID, PolicyID: in.PolicyID, Action: strings.TrimPrefix(path, "/"), Actor: actorID, OperationID: in.OperationID, Reason: in.Reason, ExpectedRevision: in.ExpectedRevision, Spec: in.Spec}, audit)
	case "/evaluate":
		value, e = labels.EvaluateTx(ctx, tx, scope, labels.EvaluateInput{DatasetID: in.DatasetID, PolicyID: in.PolicyID, ObservationID: in.ObservationID, Actor: actorID, OperationID: in.OperationID, Reason: in.Reason, ExpectedRevision: in.ExpectedRevision}, audit)
	case "/compare":
		value, e = labels.CompareTx(ctx, tx, scope, labels.CompareInput{DatasetID: in.DatasetID, BeforeID: in.BeforeID, AfterID: in.AfterID, Actor: actorID, OperationID: in.OperationID, Reason: in.Reason}, audit)
	default:
		return nil, fail(404, "not_found")
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
