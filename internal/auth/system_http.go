package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/tasks"
)

type systemRoute struct {
	method string
	write  bool
}

var systemRoutes = map[string]systemRoute{
	"/info": {"GET", false}, "/nodes": {"GET", false},
	"/resources/current": {"GET", false}, "/resources/history": {"GET", false},
	"/storage": {"GET", false}, "/storage/settings": {"POST", true},
	"/services": {"GET", false}, "/health": {"GET", false},
}

type systemRequest struct {
	Instance         string `json:"instance"`
	StorageID        string `json:"storageId"`
	SetType          string `json:"setType"`
	Percent          int    `json:"percent"`
	ExpectedRevision int64  `json:"expectedRevision"`
	ActorPassword    string `json:"actorPassword"`
	TOTPCode         string `json:"totpCode"`
}
type systemQuery struct {
	instance, dependencyType string
	page, pageSize           int
	history                  systemhealth.HistoryInput
}

// SystemHandler is the authenticated installation-health API. Every request
// checks current durable identity and the server-owned installation tenant.
func (s *Service) SystemHandler(store *systemhealth.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveSystem(w, r, store) })
}

func systemPathAllowed(method, path, tenant string, grants map[string]AccessAuth) bool {
	suffix, prefixed := strings.CutPrefix(path, "/api/system")
	route, known := systemRoutes[suffix]
	a := grants["system"]
	return tenant == systemhealth.OwnerTenantID && prefixed && known && method == route.method && a.Readable && (!route.write || a.Writeable)
}

func parseSystemQuery(path string, q url.Values) (systemQuery, error) {
	out := systemQuery{instance: systemhealth.LocalInstanceID, dependencyType: "all", page: 1, pageSize: 20}
	allowed := ""
	switch path {
	case "/resources/current":
		allowed = "instance"
	case "/resources/history":
		allowed = "instance startTime endTime graphType storageId"
	case "/storage":
		allowed = "instance page pageSize"
	case "/services":
		allowed = "type"
	}
	for key, values := range q {
		if !slices.Contains(strings.Fields(allowed), key) || len(values) != 1 || values[0] == "" || !utf8.ValidString(values[0]) {
			return out, fail(400, "invalid_input")
		}
	}
	if path == "/resources/current" || path == "/resources/history" || path == "/storage" {
		out.instance = q.Get("instance")
		if !systemIdentifier(out.instance) {
			return out, fail(400, "invalid_input")
		}
	}
	for _, key := range []string{"page", "pageSize"} {
		if v := q.Get(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || strconv.Itoa(n) != v || n < 1 || key == "page" && n > 100000 || key == "pageSize" && (n < 10 || n > 50 || n%10 != 0) {
				return out, fail(400, "invalid_input")
			}
			if key == "page" {
				out.page = n
			} else {
				out.pageSize = n
			}
		}
	}
	if path == "/resources/history" {
		out.history.Instance, out.history.GraphType, out.history.StorageID = out.instance, q.Get("graphType"), q.Get("storageId")
		for _, key := range []string{"startTime", "endTime"} {
			v := q.Get(key)
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 || strconv.FormatInt(n, 10) != v {
				return out, fail(400, "invalid_input")
			}
			if key == "startTime" {
				out.history.StartTime = n
			} else {
				out.history.EndTime = n
			}
		}
		if out.history.EndTime <= out.history.StartTime || out.history.EndTime-out.history.StartTime > int64(systemhealth.MaxHistoryWindow/time.Second) {
			return out, fail(400, "invalid_input")
		}
		switch out.history.GraphType {
		case "cpu_basic", "ram_basic":
			if out.history.StorageID != "" {
				return out, fail(400, "invalid_input")
			}
		case "disk_usage":
			if !systemIdentifier(out.history.StorageID) {
				return out, fail(400, "invalid_input")
			}
		default:
			return out, fail(400, "invalid_input")
		}
	}
	if path == "/services" {
		if v := q.Get("type"); v != "" {
			out.dependencyType = v
		}
		switch out.dependencyType {
		case "all", "service", "port", "engine":
		default:
			return out, fail(400, "invalid_input")
		}
	}
	return out, nil
}

func systemIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i, c := range value {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_') {
			continue
		}
		return false
	}
	return true
}

func decodeSystemRequest(w http.ResponseWriter, r *http.Request) (systemRequest, error) {
	var in systemRequest
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil || !utf8.Valid(raw) {
		return in, fail(400, "invalid_input")
	}
	const required = "instance storageId setType percent expectedRevision actorPassword totpCode"
	const deprecated = "storageDataAlarmValue storageLogValue storageAutoClearValue mount"
	fields, err := resourceExactObject(raw, required+" "+deprecated)
	if err != nil {
		return in, err
	}
	if err = resourceUniqueJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return in, err
	}
	for _, v := range fields {
		if string(v) == "null" {
			return in, fail(400, "invalid_input")
		}
	}
	for _, key := range strings.Fields(deprecated) {
		if _, ok := fields[key]; ok {
			return in, fail(422, "unsupported")
		}
	}
	for _, key := range strings.Fields(required) {
		if _, ok := fields[key]; !ok {
			return in, fail(400, "invalid_input")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
		return in, fail(400, "invalid_input")
	}
	if !systemIdentifier(in.Instance) || !systemIdentifier(in.StorageID) || in.ExpectedRevision < 0 || in.ExpectedRevision == math.MaxInt64 || len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return in, fail(400, "invalid_input")
	}
	for _, c := range in.TOTPCode {
		if c < '0' || c > '9' {
			return in, fail(400, "invalid_input")
		}
	}
	if in.SetType == "log" || in.SetType == "autoClear" {
		return in, fail(422, "unsupported")
	}
	if in.SetType != "alarm" || in.Percent < 85 || in.Percent > 90 {
		return in, fail(400, "invalid_input")
	}
	return in, nil
}

func (s *Service) serveSystem(w http.ResponseWriter, r *http.Request, store *systemhealth.Store) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/system")
	route, known := systemRoutes[path]
	if !prefixed || !known {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in systemRequest
	var query systemQuery
	if route.write {
		if r.Header.Get("Origin") != s.origin {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || len(q) != 0 {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		in, err = decodeSystemRequest(w, r)
	} else {
		// Reject request bodies, including chunked bodies with unknown length.
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if e != nil || len(body) != 0 {
			err = fail(400, "invalid_input")
		} else {
			query, err = parseSystemQuery(path, q)
		}
	}
	if err == nil && store == nil {
		err = fail(503, "health_unavailable")
	}
	if err != nil {
		status, code := systemHTTPError(err)
		write(w, status, map[string]string{"error": code})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	value, err := s.handleSystem(ctx, r, store, path, route, in, query)
	if err != nil {
		status, code := systemHTTPError(err)
		if status != 503 {
			s.recordFailure(ctx, "system"+path)
		}
		if status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		write(w, status, map[string]string{"error": code})
		return
	}
	write(w, 200, value)
}

func systemHTTPError(err error) (int, string) {
	var e *systemhealth.Error
	var f failure
	var task *tasks.Error
	var pg *pgconn.PgError
	var network net.Error
	switch {
	case errors.As(err, &f):
		return f.status, f.code
	case errors.As(err, &e):
		return e.Status, e.Code
	case errors.As(err, &task):
		return task.Status, task.Code
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.As(err, &network):
		return 503, "database_unavailable"
	case errors.As(err, &pg) && (strings.HasPrefix(pg.Code, "08") || pg.Code == "57P01" || pg.Code == "57P02" || pg.Code == "57P03"):
		return 503, "database_unavailable"
	case errors.As(err, &pg) && (pg.Code == "42P01" || pg.Code == "42703" || pg.Code == "3F000"):
		return 503, "schema_incompatible"
	default:
		return 500, "internal"
	}
}

func (s *Service) handleSystem(ctx context.Context, r *http.Request, store *systemhealth.Store, path string, route systemRoute, in systemRequest, query systemQuery) (any, error) {
	// Establish real protocol/schema health before identity locks. Missing DB
	// means no authenticated answer; there is deliberately no cached identity.
	gateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(gateCtx, s.database.Copy())
	if err != nil {
		return nil, fail(503, "database_unavailable")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(gateCtx)
	if err != nil {
		return nil, fail(503, "database_unavailable")
	}
	defer tx.Rollback(ctx)
	if err = store.CheckSchemaTx(gateCtx, tx); err != nil {
		return nil, err
	}
	cancel()
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err = s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, err
		}
	}
	actor, _, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if actor.tenant != store.OwnerTenantID() || !systemPathAllowed(r.Method, r.URL.Path, actor.tenant, grants) {
		return nil, fail(403, "forbidden")
	}
	if route.write {
		if err = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); err != nil {
			return nil, err
		}
		if err = setAuditMetadata(ctx, tx, actor); err != nil {
			return nil, err
		}
	}
	var value any
	switch path {
	case "/info", "/nodes", "/resources/current", "/services", "/health":
		instance := query.instance
		if instance == "" {
			instance = systemhealth.LocalInstanceID
		}
		var current systemhealth.Current
		current, err = store.CurrentTx(ctx, tx, actor.tenant, instance)
		if err != nil {
			return nil, err
		}
		switch path {
		case "/info":
			value = systemInfoValue(current, current.CheckedAt)
		case "/nodes":
			value = systemNodesValue(current)
		case "/resources/current":
			value = current
		default:
			var worker systemhealth.WorkerActivity
			worker, err = store.WorkerActivityTx(ctx, tx, actor.tenant)
			value = systemHealthValue(current, worker, query.dependencyType, current.CheckedAt)
		}
	case "/resources/history":
		value, err = store.HistoryTx(ctx, tx, actor.tenant, query.history)
	case "/storage":
		var storage systemhealth.StorageView
		storage, err = store.StorageTx(ctx, tx, actor.tenant, query.instance, query.page, query.pageSize)
		value = struct {
			systemhealth.StorageView
			Page     int `json:"page"`
			PageSize int `json:"pageSize"`
		}{storage, query.page, query.pageSize}
	case "/storage/settings":
		var setting systemhealth.AlarmSetting
		setting, err = store.SetAlarmTx(ctx, tx, actor.tenant, actor.ID, systemhealth.AlarmInput{Instance: in.Instance, MountID: in.StorageID, Percent: in.Percent, ExpectedRevision: in.ExpectedRevision})
		if err == nil {
			err = s.audit(ctx, tx, actor, "system_storage_alarm", actor.ID)
		}
		value = struct {
			Result  int                       `json:"result"`
			Setting systemhealth.AlarmSetting `json:"setting"`
		}{1, setting}
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return value, nil
}
