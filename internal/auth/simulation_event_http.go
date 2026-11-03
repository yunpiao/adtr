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
	"github.com/yunpiao/adtr/internal/tasks"
)

const simulationEventPath = "/api/simulation/events"

var simulationEventRoutes = map[string]string{"/datasets": "GET", "/query": "POST", "/detail": "GET", "/transition": "POST", "/operation": "GET"}

func simulationEventPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationEventPath)
	required, known := simulationEventRoutes[suffix]
	g := grants["simulation_events"]
	return ok && known && method == required && g.Readable && (suffix != "/transition" || g.Writeable)
}

type simulationEventInput struct {
	DomainID         string    `json:"domainId"`
	DatasetID        string    `json:"datasetId"`
	CaseID           string    `json:"caseId"`
	OperationID      string    `json:"operationId"`
	Offset           int       `json:"offset"`
	Limit            int       `json:"limit"`
	HistoryOffset    int       `json:"historyOffset"`
	HistoryLimit     int       `json:"historyLimit"`
	Kind             string    `json:"kind"`
	Actor            string    `json:"actor"`
	Device           string    `json:"device"`
	Session          string    `json:"session"`
	SourceIP         string    `json:"sourceIP"`
	From             time.Time `json:"from"`
	Until            time.Time `json:"until"`
	Action           string    `json:"action"`
	Reason           string    `json:"reason"`
	ExpectedRevision uint64    `json:"expectedRevision"`
}

func simulationEventKeys(path string) (string, string) {
	switch path {
	case "/datasets":
		return "domainId offset limit", ""
	case "/detail":
		return "domainId datasetId caseId historyOffset historyLimit", ""
	case "/operation":
		return "domainId datasetId operationId", ""
	case "/transition":
		return "domainId datasetId caseId operationId action reason expectedRevision", ""
	case "/query":
		return "domainId datasetId offset limit", "kind actor device session sourceIP from until"
	}
	return "", ""
}

func decodeSimulationEventInput(w http.ResponseWriter, r *http.Request, path string) (simulationEventInput, error) {
	var in simulationEventInput
	bad := func() (simulationEventInput, error) { return simulationEventInput{}, fail(400, "invalid_input") }
	required, optional := simulationEventKeys(path)
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
			case "offset", "limit", "historyOffset", "historyLimit":
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
	if (path == "/detail" || path == "/transition") && !messageIdentifier(in.CaseID) {
		return bad()
	}
	if (path == "/operation" || path == "/transition") && !messageIdentifier(in.OperationID) {
		return bad()
	}
	if (path == "/datasets" || path == "/query") && (in.Offset < 0 || in.Offset > securityevents.MaxEvents || in.Limit < 1 || in.Limit > simulationevents.MaxPage) {
		return bad()
	}
	if path == "/detail" && (in.HistoryOffset < 0 || in.HistoryOffset > simulationevents.MaxHistory || in.HistoryLimit < 1 || in.HistoryLimit > simulationevents.MaxPage) {
		return bad()
	}
	if path == "/transition" && (in.ExpectedRevision == 0 || !slices.Contains([]string{"mark_suspicious", "mark_benign", "unmark", "close"}, in.Action) || in.Reason == "" || strings.TrimSpace(in.Reason) != in.Reason || len(in.Reason) > 128 || !validAccessText(in.Reason, 128)) {
		return bad()
	}
	return in, nil
}

// SimulationEventsHandler is opt-in. It has no fixture upload, producer or AD
// client, and never changes roles or resource grants. Pass false by default.
func (s *Service) SimulationEventsHandler(enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, ok := strings.CutPrefix(r.URL.Path, simulationEventPath)
		method, known := simulationEventRoutes[path]
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
		in, err := decodeSimulationEventInput(w, r, path)
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
		out, err := s.handleSimulationEvent(ctx, r.WithContext(ctx), path, in)
		if err != nil {
			status, code := simulationEventHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		write(w, 200, out.value)
	})
}
func simulationEventHTTPError(err error) (int, string) {
	var f failure
	if errors.As(err, &f) {
		return f.status, f.code
	}
	switch {
	case errors.Is(err, simulationevents.ErrNotFound):
		return 404, "not_found"
	case errors.Is(err, simulationevents.ErrDenied):
		return 403, "forbidden"
	case errors.Is(err, simulationevents.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(err, simulationevents.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(err, simulationevents.ErrLimit):
		return 422, "simulation_limit_exceeded"
	default:
		return 503, "simulation_unavailable"
	}
}
func (s *Service) handleSimulationEvent(ctx context.Context, r *http.Request, path string, in simulationEventInput) (*credentialUseResult, error) {
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
	// Hold the application-wide schema gate before acquiring identity locks.
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
	if !simulationEventPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	scope := securityevents.Scope{Tenant: actor.tenant, Domain: in.DomainID, Read: grants["simulation_events"].Readable, Manage: grants["simulation_events"].Writeable}
	var value any
	switch path {
	case "/datasets":
		value, err = simulationevents.ListDatasetsTx(ctx, tx, scope, in.Offset, in.Limit)
	case "/query":
		value, err = simulationevents.QueryTx(ctx, tx, scope, in.DatasetID, securityevents.Filter{Kind: in.Kind, Actor: in.Actor, Device: in.Device, Session: in.Session, SourceIP: in.SourceIP, From: in.From, Until: in.Until, Offset: in.Offset, Limit: in.Limit})
	case "/detail":
		value, err = simulationevents.DetailTx(ctx, tx, scope, in.DatasetID, in.CaseID, in.HistoryOffset, in.HistoryLimit)
	case "/operation":
		value, err = simulationevents.OperationTx(ctx, tx, scope, in.DatasetID, strconv.FormatInt(actor.ID, 10), in.OperationID)
	case "/transition":
		value, err = simulationevents.TransitionTx(ctx, tx, scope, simulationevents.TransitionInput{DatasetID: in.DatasetID, CaseID: in.CaseID, OperationID: in.OperationID, Change: securityevents.Change{Action: in.Action, Reason: in.Reason, ExpectedRevision: in.ExpectedRevision, Actor: strconv.FormatInt(actor.ID, 10), At: s.now().UTC()}}, func(ctx context.Context, tx pgx.Tx, _ simulationevents.Receipt) error {
			return s.audit(ctx, tx, actor, "simulation_event_transition", actor.ID)
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

// simulationEventProfileReadable is a navigation hint, not authorization. The
// caller already holds the identity tenant/user locks and has rechecked session
// validity (or verified login credentials). Legacy authentication-only schemas return false without
// querying tables or columns that did not exist before migration 24.
func simulationEventProfileReadable(ctx context.Context, tx pgx.Tx, actor User) (bool, error) {
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
	if !simulationEventProfileSchemaAvailable(version) {
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
	return grants["simulation_events"].Readable, nil
}

// The navigation hint is safe on the original event schema and the additive
// risk/detection schemas only. Unknown future versions must not advertise capabilities.
func simulationEventProfileSchemaAvailable(version int) bool {
	return version == 24 || version == 25 || version == 26 || version == 27 || version == 28 || version == 29 || version == 30 || version == 31 || version == 32
}
