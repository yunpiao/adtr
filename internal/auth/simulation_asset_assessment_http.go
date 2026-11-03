package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/schemaversion"
	assessment "github.com/yunpiao/adtr/internal/simulationassetassessment"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const simulationAssetAssessmentPath = "/api/simulation/asset-assessment"

var simulationAssetAssessmentRouteOrder = []string{"/observation", "/create", "/history", "/detail", "/rows", "/receipt"}
var simulationAssetAssessmentRoutes = map[string]taskRoute{
	"/observation": {"GET", "domainId observationId", false},
	"/create":      {"POST", "domainId id operationId observationId expectedSourceHash scenarioId", true},
	"/history":     {"GET", "domainId offset limit", false},
	"/detail":      {"GET", "domainId id", false},
	"/rows":        {"GET", "domainId id kind accountStatus activity groupType groupScope offset limit", false},
	"/receipt":     {"GET", "domainId operationId", false},
}
var simulationAssetAssessmentID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var simulationAssetAssessmentHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func simulationAssetAssessmentPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationAssetAssessmentPath)
	rt, known := simulationAssetAssessmentRoutes[suffix]
	g := grants["simulation_asset_assessment"]
	return ok && known && method == rt.method && g.Readable && (!rt.write || g.Writeable)
}

type simulationAssetAssessmentInput struct {
	DomainID           string `json:"domainId"`
	ID                 string `json:"id"`
	OperationID        string `json:"operationId"`
	ObservationID      string `json:"observationId"`
	ExpectedSourceHash string `json:"expectedSourceHash"`
	ScenarioID         string `json:"scenarioId"`
	Kind               string `json:"kind"`
	AccountStatus      string `json:"accountStatus"`
	Activity           string `json:"activity"`
	GroupType          string `json:"groupType"`
	GroupScope         string `json:"groupScope"`
	Offset             int    `json:"offset"`
	Limit              int    `json:"limit"`
}

func decodeSimulationAssetAssessmentInput(w http.ResponseWriter, r *http.Request, path string) (simulationAssetAssessmentInput, error) {
	var in simulationAssetAssessmentInput
	bad := func() (simulationAssetAssessmentInput, error) { return in, fail(400, "invalid_input") }
	rt, ok := simulationAssetAssessmentRoutes[path]
	if !ok || r.Method != rt.method {
		return bad()
	}
	var raw []byte
	if rt.write {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || r.URL.RawQuery != "" {
			return bad()
		}
		raw, e = io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
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
	for _, v := range []struct{ k, v string }{{"id", in.ID}, {"operationId", in.OperationID}, {"observationId", in.ObservationID}, {"scenarioId", in.ScenarioID}} {
		if _, ok := fields[v.k]; ok && !simulationAssetAssessmentID.MatchString(v.v) {
			return bad()
		}
	}
	if _, ok := fields["limit"]; ok && (in.Offset < 0 || in.Offset > assessment.MaxObjects || in.Limit < 1 || in.Limit > assessment.MaxPageSize) {
		return bad()
	}
	if rt.write && !simulationAssetAssessmentHash.MatchString(in.ExpectedSourceHash) {
		return bad()
	}
	if path == "/rows" {
		for _, f := range []struct {
			v       string
			allowed []string
		}{
			{in.Kind, []string{"", "user", "computer", "group"}},
			{in.AccountStatus, []string{"", "enabled", "disabled", "locked", "unknown", "not_applicable"}},
			{in.Activity, []string{"", "recent", "inactive", "never_observed", "unknown", "not_applicable"}},
			{in.GroupType, []string{"", "security", "distribution", "unknown", "not_applicable"}},
			{in.GroupScope, []string{"", "global", "domain_local", "universal", "unknown", "not_applicable"}},
		} {
			if !slices.Contains(f.allowed, f.v) {
				return bad()
			}
		}
	}
	return in, nil
}

func (s *Service) SimulationAssetAssessmentHandler(enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationAssetAssessmentPath)
		rt, known := simulationAssetAssessmentRoutes[path]
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
		in, e := decodeSimulationAssetAssessmentInput(w, r, path)
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
		out, e := s.handleSimulationAssetAssessment(ctx, r.WithContext(ctx), path, in)
		if e != nil {
			status, code := simulationAssetAssessmentHTTPError(e)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		write(w, 200, out.value)
	})
}
func simulationAssetAssessmentHTTPError(e error) (int, string) {
	switch {
	case errors.Is(e, tasks.ErrSchemaIncompatible):
		return 503, "schema_incompatible"
	case errors.Is(e, assessment.ErrDenied):
		return 403, "forbidden"
	case errors.Is(e, assessment.ErrNotFound):
		return 404, "not_found"
	case errors.Is(e, assessment.ErrConflict):
		return 409, "report_conflict"
	case errors.Is(e, assessment.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(e, assessment.ErrLimit):
		return 422, "simulation_limit_exceeded"
	default:
		return simulationDetectionHTTPError(e)
	}
}
func (s *Service) handleSimulationAssetAssessment(ctx context.Context, r *http.Request, path string, in simulationAssetAssessmentInput) (*credentialUseResult, error) {
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
	// authenticateAccess acquires the tenant advisory lock before locking the actor.
	// Permission/resource mutation triggers serialize on that lock and bump this epoch.
	actor, role, grants, e := s.authenticateAccess(ctx, tx, r)
	if e != nil {
		return nil, e
	}
	if !simulationAssetAssessmentPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	allowed, e := s.effectiveResourceIDs(ctx, tx, actor, role)
	if e != nil {
		return nil, e
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	var epoch int64
	if e = tx.QueryRow(ctx, "SELECT authorization_version FROM adtr.users WHERE tenant_id=$1 AND id=$2", actor.tenant, actor.ID).Scan(&epoch); e != nil {
		return nil, e
	}
	if epoch <= 0 {
		return nil, fail(403, "forbidden")
	}
	g := grants["simulation_asset_assessment"]
	scope := assessment.Scope{Tenant: actor.tenant, Domain: in.DomainID, ActorID: actor.ID, Read: g.Readable, Manage: g.Writeable, Epoch: epoch}
	audit := func(ctx context.Context, tx pgx.Tx, receipt assessment.Receipt) error {
		return s.audit(ctx, tx, actor, "simulation_asset_assessment_create", actor.ID)
	}
	var value any
	switch path {
	case "/observation":
		value, e = assessment.ObservationTx(ctx, tx, scope, in.ObservationID)
	case "/create":
		value, e = assessment.CreateTx(ctx, tx, scope, assessment.CreateInput{ID: in.ID, OperationID: in.OperationID, ObservationID: in.ObservationID, ExpectedSourceHash: in.ExpectedSourceHash, ScenarioID: in.ScenarioID}, audit)
	case "/history":
		value, e = assessment.HistoryTx(ctx, tx, scope, in.Offset, in.Limit)
	case "/detail":
		value, e = assessment.ReportTx(ctx, tx, scope, in.ID)
	case "/rows":
		value, e = assessment.RowsTx(ctx, tx, scope, in.ID, assessment.Filter{Kind: in.Kind, AccountStatus: in.AccountStatus, Activity: in.Activity, GroupType: in.GroupType, GroupScope: in.GroupScope, Offset: in.Offset, Limit: in.Limit})
	case "/receipt":
		value, e = assessment.ReceiptTx(ctx, tx, scope, in.OperationID)
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
