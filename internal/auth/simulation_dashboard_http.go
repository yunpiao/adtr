package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationdashboard"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/tasks"
)

const simulationDashboardPath = "/api/simulation/dashboard"
const simulationDashboardMaxResponse = 4 << 20

type simulationDashboardInput struct{ DomainID, DatasetID, Subject, Window string }

// This route is a composition of existing capabilities, not a new permission.
// All three independent read grants remain necessary, including for admins.
func simulationDashboardPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return method == http.MethodGet && path == simulationDashboardPath+"/summary" &&
		grants["simulation_events"].Readable && grants["simulation_risk"].Readable && grants["simulation_detection"].Readable
}

func decodeSimulationDashboardInput(r *http.Request) (simulationDashboardInput, error) {
	var in simulationDashboardInput
	bad := func() (simulationDashboardInput, error) {
		return simulationDashboardInput{}, fail(400, "invalid_input")
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 2048 {
		return bad()
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return bad()
	}
	for k, values := range q {
		if len(values) != 1 || values[0] == "" {
			return bad()
		}
		switch k {
		case "domainId":
			in.DomainID = values[0]
		case "datasetId":
			in.DatasetID = values[0]
		case "subject":
			in.Subject = values[0]
		case "window":
			in.Window = values[0]
		default:
			return bad()
		}
	}
	if !messageIdentifier(in.DomainID) || !messageIdentifier(in.DatasetID) {
		return bad()
	}
	if in.Window != "1h" && in.Window != "24h" && in.Window != "7d" {
		return bad()
	}
	if _, present := q["subject"]; present && (len(in.Subject) > 128 || strings.TrimSpace(in.Subject) != in.Subject || !validAccessText(in.Subject, 128)) {
		return bad()
	}
	return in, nil
}

// SimulationDashboardHandler is opt-in and cannot expose a disabled constituent
// feature. It only reads synthetic data and never grants access or starts tasks.
func (s *Service) SimulationDashboardHandler(enabled, eventsEnabled, riskEnabled, detectionEnabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != simulationDashboardPath+"/summary" {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if !enabled || !eventsEnabled || !riskEnabled || !detectionEnabled {
			write(w, 404, map[string]string{"error": "simulation_disabled"})
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		in, err := decodeSimulationDashboardInput(r)
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
		out, actorID, err := s.handleSimulationDashboard(ctx, r.WithContext(ctx), in)
		if err != nil {
			status, code := simulationDashboardHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(actorID, 10))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	})
}

func simulationDashboardHTTPError(err error) (int, string) {
	var f failure
	if errors.As(err, &f) {
		return f.status, f.code
	}
	switch {
	case errors.Is(err, simulationdashboard.ErrNotFound):
		return 404, "not_found"
	case errors.Is(err, simulationdashboard.ErrDenied), errors.Is(err, tasks.ErrAuthorization):
		return 403, "forbidden"
	case errors.Is(err, simulationdashboard.ErrLimit):
		return 422, "simulation_limit_exceeded"
	default:
		return 503, "simulation_unavailable"
	}
}

func (s *Service) handleSimulationDashboard(ctx context.Context, r *http.Request, in simulationDashboardInput) ([]byte, int64, error) {
	conn, err := dbadmission.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close(ctx)
	// Preserve the existing READ COMMITTED identity-lock/session-recheck flow.
	// SummaryTx supplies one statement snapshot after these live checks.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		if errors.Is(err, tasks.ErrSchemaIncompatible) {
			return nil, 0, fail(503, "schema_incompatible")
		}
		return nil, 0, fail(503, "schema_unavailable")
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, 0, err
	}
	if !simulationDashboardPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, 0, fail(403, "forbidden")
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, 0, err
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, 0, fail(404, "not_found")
	}
	epoch, err := simulationDetectionTaskAuthorizer(s.now)(ctx, tx, tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}, tasks.Scope{DomainID: in.DomainID, TaskName: simulationdetection.KindName}, tasks.Read)
	if err != nil {
		return nil, 0, err
	}
	if epoch == "" {
		return nil, 0, fail(403, "forbidden")
	}
	summary, err := simulationdashboard.SummaryTx(ctx, tx, simulationdashboard.Scope{Tenant: actor.tenant, Domain: in.DomainID, ActorID: actor.ID, AuthorizationVersion: epoch, Read: true}, simulationdashboard.Input{DatasetID: in.DatasetID, Subject: in.Subject, Window: in.Window})
	if err != nil {
		return nil, 0, err
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil, 0, err
	}
	if len(encoded) > simulationDashboardMaxResponse {
		return nil, 0, simulationdashboard.ErrLimit
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return encoded, actor.ID, nil
}
