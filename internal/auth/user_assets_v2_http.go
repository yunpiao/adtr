package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

const userAssetsV2Path = "/api/user-assets/v2"

// Stored user reads need the intersection of these two permissions, but never
// task ownership, task permissions, producer gates or credential-use authority.
func userAssetsV2PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return method == http.MethodGet && (path == userAssetsV2Path || path == userAssetsV2Path+"/detail") && grants["domains"].Readable && grants["directory_assets"].Readable
}

func userAssetsV2Query(detail bool, q url.Values) (domains.UserAssetsV2Filter, domains.UserAssetV2Input, error) {
	f := domains.UserAssetsV2Filter{PageIdx: 1, PageSize: 10}
	var in domains.UserAssetV2Input
	bad := func() (domains.UserAssetsV2Filter, domains.UserAssetV2Input, error) {
		return domains.UserAssetsV2Filter{}, domains.UserAssetV2Input{}, fail(400, "invalid_input")
	}
	unsupported := false
	// Validate structure before deferred filters; no duplicate, invalid UTF-8 or
	// unknown key is hidden by a recognized-but-unimplemented business filter.
	for key, values := range q {
		if !utf8.ValidString(key) || len(values) != 1 || !utf8.ValidString(values[0]) {
			return bad()
		}
		switch key {
		case "domainId", "expectedRevision", "expectedCredentialRevision", "observationId":
			if values[0] == "" {
				return bad()
			}
		case "search", "pageIdx", "pageSize":
			if detail || key != "search" && values[0] == "" {
				return bad()
			}
		case "objectGUID":
			if !detail || values[0] == "" {
				return bad()
			}
		case "userStatus", "domainName", "startTm", "endTm", "updateTm", "creatTm", "userType", "sAMAccountName", "scoreSort", "overviewType":
			if detail {
				return bad()
			}
			unsupported = true
		default:
			return bad()
		}
	}
	if unsupported {
		return f, in, fail(422, "unsupported_user_asset_filter")
	}
	f.DomainID, f.ExpectedRevision, f.ExpectedCredentialRevision = q.Get("domainId"), q.Get("expectedRevision"), q.Get("expectedCredentialRevision")
	f.ObservationID, f.Search = q.Get("observationId"), q.Get("search")
	if detail {
		in = domains.UserAssetV2Input{DomainID: f.DomainID, ExpectedRevision: f.ExpectedRevision, ExpectedCredentialRevision: f.ExpectedCredentialRevision, ObservationID: f.ObservationID, ObjectGUID: q.Get("objectGUID")}
		if domains.ValidateUserAssetV2Input(in) != nil {
			return bad()
		}
		return f, in, nil
	}
	for _, key := range []string{"pageIdx", "pageSize"} {
		if values, present := q[key]; present {
			n, err := strconv.Atoi(values[0])
			if err != nil || strconv.Itoa(n) != values[0] {
				return bad()
			}
			if key == "pageIdx" {
				f.PageIdx = n
			} else {
				f.PageSize = n
			}
		}
	}
	if domains.ValidateUserAssetsV2Filter(f) != nil {
		return bad()
	}
	return f, in, nil
}

// Only the domain store is injected: these endpoints cannot submit tasks,
// decrypt credentials, open LDAP connections, or mutate an observation.
func (s *Service) UserAssetsV2Handler(store *domains.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != userAssetsV2Path && r.URL.Path != userAssetsV2Path+"/detail" {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 32768 {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		detail := r.URL.Path == userAssetsV2Path+"/detail"
		f, in, err := userAssetsV2Query(detail, q)
		if err != nil {
			status, code := domainHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		if s.database == nil || store == nil {
			write(w, 503, map[string]string{"error": "schema_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
		r = r.WithContext(ctx)
		out, err := s.handleUserAssetsV2(ctx, r, store, detail, f, in)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			s.writeUserAssetsV2Error(w, ctx, detail, err)
			return
		}
		// Encode the whole response before success headers/bytes, never stream a
		// partial result if a hostile text projection exceeds the fixed cap.
		raw, err := json.Marshal(out.value)
		defer clear(raw)
		if err != nil || len(raw) > domains.UserAssetsV2MaxBytes {
			s.writeUserAssetsV2Error(w, ctx, detail, fail(422, "directory_limit_exceeded"))
			return
		}
		if err = ctx.Err(); err != nil {
			s.writeUserAssetsV2Error(w, ctx, detail, err)
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	})
}

func (s *Service) writeUserAssetsV2Error(w http.ResponseWriter, ctx context.Context, detail bool, err error) {
	status, code := domainHTTPError(err)
	if status != http.StatusServiceUnavailable {
		action := "user_assets_v2_list"
		if detail {
			action = "user_assets_v2_detail"
		}
		s.recordFailure(ctx, action)
	}
	write(w, status, map[string]string{"error": code})
}

func (s *Service) handleUserAssetsV2(ctx context.Context, r *http.Request, store *domains.Store, detail bool, f domains.UserAssetsV2Filter, in domains.UserAssetV2Input) (*credentialUseResult, error) {
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, directorySchemaError(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, directorySchemaError(err)
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		return nil, directorySchemaError(err)
	}
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !userAssetsV2PathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	if _, _, err = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); err != nil {
		return nil, err
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, err
	}
	if err = setAuditMetadata(ctx, tx, actor); err != nil {
		return nil, err
	}
	var value any
	if detail {
		value, err = store.UserAssetV2DetailTx(ctx, tx, actor.tenant, allowed, in)
	} else {
		value, err = store.UserAssetsV2ListTx(ctx, tx, actor.tenant, allowed, f)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
