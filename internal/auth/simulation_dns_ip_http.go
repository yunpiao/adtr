package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationdnsip"
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

const simulationDNSIPPath = "/api/simulation/dns-ip"

var simulationDNSIPRouteOrder = []string{"/observations", "/records", "/export", "/task", "/receipt", "/cancel", "/download"}
var simulationDNSIPRoutes = map[string]taskRoute{
	"/observations": {"GET", "domainId", false},
	"/records":      {"GET", "domainId observationId keyword family offset limit", false},
	"/export":       {"POST", "domainId observationId observationHash keyword family operationId", true},
	"/task":         {"GET", "domainId taskId", false},
	"/receipt":      {"GET", "domainId operationId", false},
	"/cancel":       {"POST", "domainId taskId operationId", true},
	"/download":     {"GET", "domainId taskId", false},
}

func simulationDNSIPPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, simulationDNSIPPath)
	rt, known := simulationDNSIPRoutes[suffix]
	g := grants["simulation_dns_ip"]
	return ok && known && rt.method == method && g.Readable && (!rt.write || g.Writeable)
}

type simulationDNSIPInput struct {
	DomainID        string `json:"domainId"`
	ObservationID   string `json:"observationId"`
	ObservationHash string `json:"observationHash"`
	Keyword         string `json:"keyword"`
	Family          string `json:"family"`
	OperationID     string `json:"operationId"`
	TaskID          string `json:"taskId"`
	Offset          int    `json:"offset"`
	Limit           int    `json:"limit"`
}

func decodeSimulationDNSIPInput(w http.ResponseWriter, r *http.Request, path string) (simulationDNSIPInput, error) {
	var in simulationDNSIPInput
	bad := func() (simulationDNSIPInput, error) { return in, fail(400, "invalid_input") }
	rt, known := simulationDNSIPRoutes[path]
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
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 4096 {
			return bad()
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return bad()
		}
		obj := map[string]any{}
		for k, v := range q {
			if len(v) != 1 {
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
	if !messageIdentifier(in.DomainID) || in.DomainID == "platform" {
		return bad()
	}
	for _, x := range []struct{ k, v string }{{"observationId", in.ObservationID}, {"operationId", in.OperationID}, {"taskId", in.TaskID}} {
		if _, ok := fields[x.k]; ok && !messageIdentifier(x.v) {
			return bad()
		}
	}
	if path == "/records" || path == "/export" {
		if len(in.Keyword) > 253 || (in.Family != "" && in.Family != "ipv4" && in.Family != "ipv6") {
			return bad()
		}
		for _, c := range in.Keyword {
			if c < 32 || c > 126 {
				return bad()
			}
		}
	}
	if path == "/records" && (in.Offset < 0 || in.Offset > dnsipassets.MaxRecords || in.Limit < 1 || in.Limit > 100) {
		return bad()
	}
	if path == "/export" {
		b, e := hex.DecodeString(in.ObservationHash)
		if e != nil || len(b) != 32 || strings.ToLower(in.ObservationHash) != in.ObservationHash {
			return bad()
		}
	}
	return in, nil
}
func (s *Service) SimulationDNSIPHandler(enabled bool, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		path, ok := strings.CutPrefix(r.URL.Path, simulationDNSIPPath)
		rt, known := simulationDNSIPRoutes[path]
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
		in, err := decodeSimulationDNSIPInput(w, r, path)
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
		out, manifest, data, err := s.handleSimulationDNSIP(ctx, r.WithContext(ctx), path, in, engine)
		if err != nil {
			status, code := simulationDNSIPHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		if path == "/download" {
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": manifest.Filename}))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("X-ADTR-SHA256", manifest.SHA256)
			w.Header().Set("X-ADTR-Row-Count", strconv.Itoa(manifest.RowCount))
			w.WriteHeader(200)
			_, _ = w.Write(data)
			return
		}
		write(w, 200, out.value)
	})
}
func simulationDNSIPHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, simulationdnsip.ErrDenied):
		return 403, "forbidden"
	case errors.Is(err, simulationdnsip.ErrNotFound):
		return 404, "not_found"
	case errors.Is(err, simulationdnsip.ErrConflict):
		return 409, "revision_conflict"
	case errors.Is(err, simulationdnsip.ErrInvalid), errors.Is(err, dnsipassets.ErrInvalid):
		return 400, "invalid_input"
	case errors.Is(err, simulationdnsip.ErrLimit):
		return 422, "simulation_limit_exceeded"
	default:
		return simulationDetectionHTTPError(err)
	}
}
func (s *Service) handleSimulationDNSIP(ctx context.Context, r *http.Request, path string, in simulationDNSIPInput, engine *tasks.Engine) (*credentialUseResult, simulationdnsip.Manifest, []byte, error) {
	var m simulationdnsip.Manifest
	var data []byte
	conn, err := dbadmission.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, m, nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, m, nil, err
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		if errors.Is(err, tasks.ErrSchemaIncompatible) {
			return nil, m, nil, fail(503, "schema_incompatible")
		}
		return nil, m, nil, fail(503, "schema_unavailable")
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, m, nil, err
	}
	if !simulationDNSIPPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, m, nil, fail(403, "forbidden")
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, m, nil, err
	}
	if !slices.Contains(allowed, in.DomainID) {
		return nil, m, nil, fail(404, "not_found")
	}
	g := grants["simulation_dns_ip"]
	scope := simulationdnsip.Scope{Tenant: actor.tenant, Domain: in.DomainID, ActorID: actor.ID, Read: g.Readable, Manage: g.Writeable}
	audit := func(ctx context.Context, tx pgx.Tx, a simulationdnsip.Receipt) error {
		return s.audit(ctx, tx, actor, "simulation_dns_ip_"+a.Action, actor.ID)
	}
	var value any
	switch path {
	case "/observations":
		var v []simulationdnsip.Observation
		v, err = simulationdnsip.ObservationsTx(ctx, tx, scope)
		value = map[string]any{"simulation": true, "observations": v}
	case "/records":
		value, err = simulationdnsip.ListTx(ctx, tx, scope, simulationdnsip.Query{ObservationID: in.ObservationID, Keyword: in.Keyword, Family: in.Family, Offset: in.Offset, Limit: in.Limit})
	case "/export":
		value, err = simulationdnsip.SubmitExportTx(ctx, tx, scope, engine, simulationdnsip.ExportInput{ObservationID: in.ObservationID, ObservationHash: in.ObservationHash, Keyword: in.Keyword, Family: in.Family, OperationID: in.OperationID}, audit)
	case "/receipt":
		value, err = simulationdnsip.ReceiptTx(ctx, tx, scope, engine, in.OperationID)
		if errors.Is(err, simulationdnsip.ErrNotFound) {
			return nil, m, nil, fail(404, "operation_not_found")
		}
	case "/task":
		var v tasks.Detail
		v, err = simulationdnsip.ReadExportTx(ctx, tx, scope, engine, in.TaskID)
		value = map[string]any{"simulation": true, "task": v.Task}
	case "/cancel":
		value, err = simulationdnsip.CancelExportTx(ctx, tx, scope, engine, in.TaskID, in.OperationID, audit)
	case "/download":
		m, data, err = simulationdnsip.DownloadTx(ctx, tx, scope, engine, in.TaskID)
	}
	if err != nil {
		return nil, m, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, m, nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, m, data, nil
}

// NewSimulationDNSIPTaskAuthorizer handles only the new exact synthetic export kind.
// Existing task authorization is delegated unchanged.
func NewSimulationDNSIPTaskAuthorizer(manualEnabled, schedulesEnabled, enabled bool) tasks.Authorizer {
	legacy := NewSimulationDetectionSchedulesTaskAuthorizer(manualEnabled, schedulesEnabled)
	dns := simulationDNSIPTaskAuthorizer(time.Now)
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, s tasks.Scope, a tasks.Action) (string, error) {
		if s.TaskName != simulationdnsip.KindName {
			return legacy(ctx, tx, p, s, a)
		}
		if !enabled {
			return "", tasks.ErrAuthorization
		}
		return dns(ctx, tx, p, s, a)
	}
}
func simulationDNSIPTaskAuthorizer(now func() time.Time) tasks.Authorizer {
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		deny := func() (string, error) { return "", tasks.ErrAuthorization }
		if tx == nil || p.ActorID <= 0 || !validResourceID(p.TenantID) || !validResourceID(scope.DomainID) || scope.Platform || scope.DomainID == "platform" || scope.TaskName != "simulation.dns_ip.export" || (action != tasks.Read && action != tasks.Write && action != tasks.Execute) {
			return deny()
		}
		if err := lockIdentityTenant(ctx, tx, p.TenantID); err != nil {
			return "", err
		}
		var role string
		var disabled, forced bool
		var updated time.Time
		var revision int64
		err := tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(role_id,''),role),disabled,must_change,password_updated_at,authorization_version FROM adtr.users WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, p.ActorID).Scan(&role, &disabled, &forced, &updated, &revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return deny()
		}
		if err != nil {
			return "", err
		}
		if disabled || forced || !updated.Add(90*24*time.Hour).After(now()) || revision <= 0 {
			return deny()
		}
		grants, err := loadAccessGrants(ctx, tx, p.TenantID, role)
		if err != nil {
			return "", err
		}
		g := grants["simulation_dns_ip"]
		if !g.Readable || action != tasks.Read && !g.Writeable {
			return deny()
		}
		service := Service{now: now}
		allowed, err := service.effectiveResourceIDs(ctx, tx, User{tenant: p.TenantID, ID: p.ActorID}, role)
		if err != nil {
			var f failure
			if errors.As(err, &f) {
				return deny()
			}
			return "", err
		}
		for _, id := range allowed {
			if id == scope.DomainID {
				return strconv.FormatInt(revision, 10), nil
			}
		}
		return deny()
	}
}
