package auth

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const userAssetsV3Path = "/api/user-assets/v3"
const userAssetV3Path = userAssetsV3Path + "/detail"

func userAssetV3PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return method == http.MethodGet && (path == userAssetV3Path || path == userAssetsV3Path) && grants["domains"].Readable && grants["directory_assets"].Readable
}
func (s *Service) UserAssetV3Handler(store *domains.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != userAssetV3Path && r.URL.Path != userAssetsV3Path {
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
		detail := r.URL.Path == userAssetV3Path
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
		out, err := s.handleUserAssetV3(ctx, r, store, detail, f, in)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			s.writeUserAssetV3Error(w, ctx, detail, err)
			return
		}
		// Encode the whole response before success headers/bytes, never stream a
		// partial result if a hostile text projection exceeds the fixed cap.
		raw, err := json.Marshal(out.value)
		defer clear(raw)
		if err != nil || len(raw)+1 > domains.DirectoryV3PageMaxBytes {
			s.writeUserAssetV3Error(w, ctx, detail, fail(422, "directory_limit_exceeded"))
			return
		}
		if err = ctx.Err(); err != nil {
			s.writeUserAssetV3Error(w, ctx, detail, err)
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	})
}

func (s *Service) writeUserAssetV3Error(w http.ResponseWriter, ctx context.Context, detail bool, err error) {
	status, code := domainHTTPError(err)
	if status != http.StatusServiceUnavailable {
		action := "user_assets_v3_list"
		if detail {
			action = "user_assets_v3_detail"
		}
		s.recordFailure(ctx, action)
	}
	write(w, status, map[string]string{"error": code})
}

func (s *Service) handleUserAssetV3(ctx context.Context, r *http.Request, store *domains.Store, detail bool, f domains.UserAssetsV2Filter, in domains.UserAssetV2Input) (*credentialUseResult, error) {
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
	if !userAssetV3PathAllowed(r.Method, r.URL.Path, grants) {
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
		value, err = store.UserAssetV3DetailTx(ctx, tx, actor.tenant, allowed, in)
	} else {
		value, err = store.UserAssetsV3ListTx(ctx, tx, actor.tenant, allowed, f)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
