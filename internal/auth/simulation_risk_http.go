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
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/tasks"
)

const simulationRiskPath = "/api/simulation/risk"

var simulationRiskRoutes = map[string]string{"/datasets": "GET", "/query": "POST", "/reset": "POST", "/operation": "GET"}

func simulationRiskPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationRiskPath)
	required, known := simulationRiskRoutes[suffix]
	g := grants["simulation_risk"]
	return ok && known && method == required && g.Readable && (suffix != "/reset" || g.Writeable)
}

type simulationRiskInput struct {
	DomainID         string `json:"domainId"`
	DatasetID        string `json:"datasetId"`
	Subject          string `json:"subject"`
	OperationID      string `json:"operationId"`
	Offset           int    `json:"offset"`
	Limit            int    `json:"limit"`
	Reason           string `json:"reason"`
	ExpectedRevision uint64 `json:"expectedRevision"`
}

func simulationRiskKeys(path string) (string, string) {
	switch path {
	case "/datasets":
		return "domainId offset limit", ""
	case "/operation":
		return "domainId datasetId operationId", ""
	case "/reset":
		return "domainId datasetId subject operationId reason expectedRevision", ""
	case "/query":
		return "domainId datasetId subject", ""
	}
	return "", ""
}

func decodeSimulationRiskInput(w http.ResponseWriter, r *http.Request, path string) (simulationRiskInput, error) {
	var in simulationRiskInput
	bad := func() (simulationRiskInput, error) { return simulationRiskInput{}, fail(400, "invalid_input") }
	required, optional := simulationRiskKeys(path)
	var raw []byte
	if r.Method == "POST" {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || r.URL.RawQuery != "" {
			return bad()
		}
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
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
			switch k {
			case "offset", "limit":
				n, e := strconv.Atoi(v[0])
				if e != nil || strconv.Itoa(n) != v[0] {
					return bad()
				}
				obj[k] = n
			default:
				obj[k] = v[0]
			}
		}
		raw, _ = json.Marshal(obj)
	}
	fields, err := resourceExactObject(raw, required+" "+optional)
	if err != nil {
		return bad()
	}
	for _, v := range fields {
		if string(v) == "null" {
			return bad()
		}
	}
	for _, key := range strings.Fields(required) {
		if _, ok := fields[key]; !ok {
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
	if path != "/datasets" && !messageIdentifier(in.DatasetID) {
		return bad()
	}
	if (path == "/query" || path == "/reset") && (in.Subject == "" || len(in.Subject) > 128 || strings.TrimSpace(in.Subject) != in.Subject || !validAccessText(in.Subject, 128)) {
		return bad()
	}
	if (path == "/operation" || path == "/reset") && !messageIdentifier(in.OperationID) {
		return bad()
	}
	if path == "/datasets" && (in.Offset < 0 || in.Offset > securityevents.MaxEvents || in.Limit < 1 || in.Limit > simulationevents.MaxPage) {
		return bad()
	}
	if path == "/reset" && (in.ExpectedRevision == 0 || in.Reason == "" || strings.TrimSpace(in.Reason) != in.Reason || len(in.Reason) > 128 || !validAccessText(in.Reason, 128)) {
		return bad()
	}
	return in, nil
}

// SimulationRiskHandler is opt-in. It has no fixture upload, producer or AD
// client, and never changes roles or resource grants. Pass false by default.
func (s *Service) SimulationRiskHandler(enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationRiskPath)
		method, known := simulationRiskRoutes[path]
		if !ok || !known {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if !enabled {
			write(w, 404, map[string]string{"error": "simulation_disabled"})
			return
		}
		if method != r.Method {
			w.Header().Set("Allow", method)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if method == "POST" && (s.origin == "" || r.Header.Get("Origin") != s.origin) {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		in, err := decodeSimulationRiskInput(w, r, path)
		if err != nil {
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
		out, err := s.handleSimulationRisk(ctx, r.WithContext(ctx), path, in)
		if err != nil {
			status, code := simulationRiskHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		write(w, 200, out.value)
	})
}
func simulationRiskHTTPError(err error) (int, string) {
	var f failure
	if errors.As(err, &f) {
		return f.status, f.code
	}
	switch {
	case errors.Is(err, simulationrisk.ErrNotFound) || errors.Is(err, simulationevents.ErrNotFound):
		return 404, "not_found"
	case errors.Is(err, simulationrisk.ErrDenied) || errors.Is(err, simulationevents.ErrDenied):
		return 403, "forbidden"
	case errors.Is(err, simulationrisk.ErrConflict) || errors.Is(err, simulationevents.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(err, simulationrisk.ErrInvalid) || errors.Is(err, simulationevents.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(err, simulationrisk.ErrLimit) || errors.Is(err, simulationevents.ErrLimit):
		return 422, "simulation_limit_exceeded"
	default:
		return 503, "simulation_unavailable"
	}
}
func (s *Service) handleSimulationRisk(ctx context.Context, r *http.Request, path string, in simulationRiskInput) (*credentialUseResult, error) {
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
	// Runtime uses the exact application-wide schema gate.
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
	if !simulationRiskPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	scope := simulationrisk.Scope{Tenant: actor.tenant, Domain: in.DomainID, Read: grants["simulation_risk"].Readable, Manage: grants["simulation_risk"].Writeable}
	var value any
	switch path {
	case "/datasets":
		value, err = simulationevents.ListDatasetsTx(ctx, tx, securityevents.Scope{Tenant: scope.Tenant, Domain: scope.Domain, Read: scope.Read}, in.Offset, in.Limit)
	case "/query":
		value, err = simulationrisk.EvaluateTx(ctx, tx, scope, in.DatasetID, in.Subject)
	case "/operation":
		value, err = simulationrisk.OperationTx(ctx, tx, scope, in.DatasetID, strconv.FormatInt(actor.ID, 10), in.OperationID)
	case "/reset":
		value, err = simulationrisk.ResetTx(ctx, tx, scope, simulationrisk.ResetInput{DatasetID: in.DatasetID, Subject: in.Subject, OperationID: in.OperationID, ExpectedRevision: in.ExpectedRevision, Reason: in.Reason, Actor: strconv.FormatInt(actor.ID, 10)}, func(ctx context.Context, tx pgx.Tx, _ simulationrisk.Receipt) error {
			return s.audit(ctx, tx, actor, "simulation_risk_reset", actor.ID)
		})
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}

// simulationRiskProfileReadable is a navigation hint, not authorization. The
// caller already holds the identity tenant/user locks and has rechecked session
// validity (or verified login credentials). Legacy authentication-only schemas return false without
// querying tables or columns that did not exist before migration 25.
func simulationRiskProfileReadable(ctx context.Context, tx pgx.Tx, actor User) (bool, error) {
	if actor.NeedChange {
		return false, nil
	}
	var available bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('adtr.schema_version') IS NOT NULL AND to_regclass('adtr.access_permissions') IS NOT NULL`).Scan(&available); err != nil {
		return false, err
	}
	if !available {
		return false, nil
	}
	var version int
	if err := tx.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton").Scan(&version); err != nil {
		return false, err
	}
	if version != 25 && version != 26 {
		return false, nil
	}
	var role string
	if err := tx.QueryRow(ctx, "SELECT COALESCE(NULLIF(role_id,''),role) FROM adtr.users WHERE tenant_id=$1 AND id=$2", actor.tenant, actor.ID).Scan(&role); err != nil {
		return false, err
	}
	grants, err := loadAccessGrants(ctx, tx, actor.tenant, role)
	if err != nil {
		return false, err
	}
	return grants["simulation_risk"].Readable, nil
}
