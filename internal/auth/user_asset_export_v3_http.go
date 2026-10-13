package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

const userAssetExportV3Path = "/api/user-assets/v3/exports"

var userAssetExportV3Routes = map[string]taskRoute{
	"":         {"POST", "domainId expectedRevision expectedCredentialRevision observationId search sAMAccountName startTm endTm creatTm screenColumns idempotencyKey actorPassword totpCode", true},
	"/receipt": {"GET", "", false}, "/detail": {"GET", "", false}, "/download": {"GET", "", false},
	"/cancel": {"POST", "taskID actorPassword totpCode", true},
}

type userAssetExportV3Request struct {
	domains.UserAssetExportV3Payload
	TaskID         string `json:"taskID"`
	IdempotencyKey string `json:"idempotencyKey"`
	ActorPassword  string `json:"actorPassword"`
	TOTPCode       string `json:"totpCode"`
}

func (userAssetExportV3Request) String() string   { return "[user asset export request]" }
func (userAssetExportV3Request) GoString() string { return "[user asset export request]" }
func (userAssetExportV3Request) MarshalJSON() ([]byte, error) {
	return []byte(`"[user asset export request]"`), nil
}

func userAssetExportV3PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	suffix, ok := strings.CutPrefix(path, userAssetExportV3Path)
	route, known := userAssetExportV3Routes[suffix]
	if !ok || !known || method != route.method {
		return false
	}
	for _, mark := range []string{"domains", "directory_assets", "tasks", "user_asset_exports"} {
		if !grants[mark].Readable {
			return false
		}
	}
	return !route.write || grants["tasks"].Writeable && grants["user_asset_exports"].Writeable
}

func decodeUserAssetExportV3Request(w http.ResponseWriter, r *http.Request, path string) (userAssetExportV3Request, error) {
	var in userAssetExportV3Request
	bad := func() (userAssetExportV3Request, error) { return in, fail(400, "invalid_input") }
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
	defer clear(raw)
	if err != nil || domains.ValidateUserAssetExportV3JSON(raw) != nil {
		return bad()
	}
	allowedFields := userAssetExportV3Routes[path].fields
	unsupportedFields := "userStatus domainName updateTm userType scoreSort overviewType status groupType groupScope assetsType domain"
	if path == "" {
		allowedFields += " " + unsupportedFields
	}
	fields, err := resourceExactObject(raw, allowedFields)
	if err != nil {
		return bad()
	}
	if path == "" {
		for _, key := range strings.Fields(unsupportedFields) {
			if _, present := fields[key]; present {
				return in, fail(422, "unsupported_user_asset_filter")
			}
		}
	}
	required := []string{"taskID", "actorPassword", "totpCode"}
	if path == "" {
		required = []string{"domainId", "expectedRevision", "expectedCredentialRevision", "observationId", "screenColumns", "idempotencyKey", "actorPassword", "totpCode"}
	}
	for _, k := range required {
		if _, ok := fields[k]; !ok {
			return bad()
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
		return bad()
	}
	if len(in.ActorPassword) < 1 || len(in.ActorPassword) > 256 || len(in.TOTPCode) != 6 {
		return bad()
	}
	if path == "/cancel" {
		if !validResourceID(in.TaskID) {
			return bad()
		}
		return in, nil
	}
	if !domains.ValidKey(in.IdempotencyKey) {
		return bad()
	}
	payload, _ := json.Marshal(in.UserAssetExportV3Payload)
	payload, err = domains.ValidateUserAssetExportV3Payload(payload)
	if err != nil {
		return in, err
	}
	if json.Unmarshal(payload, &in.UserAssetExportV3Payload) != nil {
		return bad()
	}
	return in, nil
}

func (s *Service) UserAssetExportV3Handler(store *domains.Store, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		path, prefix := strings.CutPrefix(r.URL.Path, userAssetExportV3Path)
		route, known := userAssetExportV3Routes[path]
		if !prefix || !known {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if r.Method != route.method {
			w.Header().Set("Allow", route.method)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		var in userAssetExportV3Request
		var q url.Values
		var err error
		if len(r.URL.RawQuery) > 32768 {
			err = fail(400, "invalid_input")
		} else {
			q, err = url.ParseQuery(r.URL.RawQuery)
		}
		if err == nil && route.write {
			if s.origin == "" || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin {
				write(w, 403, map[string]string{"error": "forbidden"})
				return
			}
			media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if len(r.Header.Values("Content-Type")) != 1 || e != nil || media != "application/json" || len(q) != 0 {
				err = fail(400, "invalid_input")
			} else {
				in, err = decodeUserAssetExportV3Request(w, r, path)
			}
		} else if err == nil {
			if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
				err = fail(400, "invalid_input")
			} else if path == "/receipt" {
				if len(q) != 2 || len(q["domainId"]) != 1 || len(q["idempotencyKey"]) != 1 || !domains.ValidID(q.Get("domainId")) || !domains.ValidKey(q.Get("idempotencyKey")) {
					err = fail(400, "invalid_input")
				}
			} else if len(q) != 1 || len(q["taskID"]) != 1 || !validResourceID(q.Get("taskID")) {
				err = fail(400, "invalid_input")
			}
		}
		if err != nil {
			status, code := domainHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		if s.database == nil || store == nil || engine == nil {
			write(w, 503, map[string]string{"error": "schema_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
		r = r.WithContext(ctx)
		result, data, artifact, err := s.handleUserAssetExportV3(ctx, r, store, engine, path, route, in, q)
		defer clear(data)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			status, code := domainHTTPError(err)
			if status != 503 {
				s.recordFailure(ctx, "user_asset_export_v3"+path)
			}
			if status == 429 {
				w.Header().Set("Retry-After", "900")
			}
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(result.actorID, 10))
		if data != nil {
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
			w.Header().Set("Content-Disposition", `attachment; filename="`+artifact.Filename+`"`)
			w.Header().Set("Content-Length", strconv.FormatInt(artifact.ByteCount, 10))
			w.Header().Set("X-ADTR-Artifact-SHA256", artifact.SHA256)
			w.Header().Set("X-ADTR-Row-Count", strconv.Itoa(artifact.RowCount))
			w.WriteHeader(200)
			_, _ = w.Write(data)
			return
		}
		raw, err := json.Marshal(result.value)
		if err != nil || len(raw)+1 > 65536 {
			write(w, 500, map[string]string{"error": "artifact_invalid"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(raw)
	})
}

func (s *Service) handleUserAssetExportV3(ctx context.Context, r *http.Request, store *domains.Store, engine *tasks.Engine, path string, route taskRoute, in userAssetExportV3Request, q url.Values) (*credentialUseResult, []byte, domains.UserAssetExportV3Artifact, error) {
	var artifact domains.UserAssetExportV3Artifact
	failOut := func(err error) (*credentialUseResult, []byte, domains.UserAssetExportV3Artifact, error) {
		return nil, nil, artifact, err
	}
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return failOut(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return failOut(err)
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		return failOut(err)
	}
	if route.write {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err = s.rate(ctx, "ip:"+ip, 60); err != nil {
			return failOut(err)
		}
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return failOut(err)
	}
	if !userAssetExportV3PathAllowed(r.Method, r.URL.Path, grants) {
		return failOut(fail(403, "forbidden"))
	}
	if _, _, err = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); err != nil {
		return failOut(err)
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return failOut(err)
	}
	domain := in.DomainID
	if path == "/receipt" {
		domain = q.Get("domainId")
	}
	if domain != "" && !slices.Contains(allowed, domain) {
		return failOut(fail(404, "not_found"))
	}
	if route.write {
		if err = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); err != nil {
			return failOut(err)
		}
	}
	if err = setAuditMetadata(ctx, tx, actor); err != nil {
		return failOut(err)
	}
	p := tasks.Principal{TenantID: actor.tenant, ActorID: actor.ID}
	var value any
	var data []byte
	taskID := q.Get("taskID")
	switch path {
	case "":
		var submission tasks.Submission
		submission, err = store.SubmitUserAssetExportV3Tx(ctx, tx, engine, p, in.UserAssetExportV3Payload, in.IdempotencyKey)
		taskID = submission.Task.ID
		value = map[string]any{"taskID": taskID, "task": publicTask(submission.Task), "request": in.UserAssetExportV3Payload, "replayed": submission.Replayed}
	case "/receipt":
		var task tasks.Task
		var request domains.UserAssetExportV3Payload
		task, request, err = store.UserAssetExportV3ReceiptTx(ctx, tx, engine, p, q.Get("domainId"), q.Get("idempotencyKey"))
		taskID = task.ID
		value = map[string]any{"taskID": taskID, "task": publicTask(task), "request": request}
	case "/detail":
		var d domains.UserAssetExportV3Detail
		d, err = store.UserAssetExportV3DetailTx(ctx, tx, engine, p, taskID)
		d.Task = publicTask(d.Task)
		value = d
	case "/cancel":
		taskID = in.TaskID
		_, _, err = store.UserAssetExportV3TaskTx(ctx, tx, engine, p, taskID)
		if err == nil {
			var task tasks.Task
			task, err = engine.CancelTx(ctx, tx, p, taskID)
			value = map[string]any{"taskID": taskID, "task": publicTask(task)}
		}
	case "/download":
		data, artifact, err = store.ReadUserAssetExportV3Tx(ctx, tx, engine, p, taskID)
	}
	if err != nil {
		clear(data)
		return failOut(err)
	}
	// Recheck time-based authority before transaction commit; never serve data
	// after password/entitlement expiry encountered during a large artifact read.
	if _, err = engine.DetailTx(ctx, tx, p, taskID); err != nil {
		clear(data)
		return failOut(err)
	}
	if err = tx.Commit(ctx); err != nil {
		clear(data)
		return failOut(err)
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, data, artifact, nil
}
