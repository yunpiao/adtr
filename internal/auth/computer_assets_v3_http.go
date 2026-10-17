package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/dbadmission"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

const computerAssetsV3Path = "/api/computer-assets/v3"

// Stored user reads need the intersection of these two permissions, but never
// task ownership, task permissions, producer gates or credential-use authority.
func computerAssetsV3PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return method == http.MethodGet && path == computerAssetsV3Path && grants["domains"].Readable && grants["directory_assets"].Readable
}

func computerAssetsV3Query(q url.Values) (domains.ComputerAssetsV3Filter, error) {
	f := domains.ComputerAssetsV3Filter{PageIdx: 1, PageSize: 10}
	bad := func() (domains.ComputerAssetsV3Filter, error) {
		return domains.ComputerAssetsV3Filter{}, fail(400, "invalid_input")
	}
	unsupported := false
	for key, values := range q {
		if !utf8.ValidString(key) || len(values) != 1 || !utf8.ValidString(values[0]) {
			return bad()
		}
		switch key {
		case "domainId", "expectedRevision", "expectedCredentialRevision", "observationId", "pageIdx", "pageSize", "startTm", "endTm", "creatTm":
			if values[0] == "" {
				return bad()
			}
		case "search":
		case "computerStatus", "domainName", "updateTm", "computerType", "overviewType":
			unsupported = true
		default:
			return bad()
		}
	}
	f.DomainID, f.ExpectedRevision, f.ExpectedCredentialRevision = q.Get("domainId"), q.Get("expectedRevision"), q.Get("expectedCredentialRevision")
	f.ObservationID, f.Search = q.Get("observationId"), q.Get("search")
	f.StartTm, f.EndTm, f.CreatTm = q.Get("startTm"), q.Get("endTm"), q.Get("creatTm")
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
	if domains.ValidateComputerAssetsV3Filter(f) != nil {
		return bad()
	}
	if unsupported {
		return f, fail(422, "unsupported_computer_asset_filter")
	}
	return f, nil
}

// Only the domain store is injected: these endpoints cannot submit tasks,
// decrypt credentials, open LDAP connections, or mutate an observation.
func (s *Service) ComputerAssetsV3Handler(store *domains.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != computerAssetsV3Path {
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
		f, err := computerAssetsV3Query(q)
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
		out, err := s.handleComputerAssetsV3(ctx, r, store, f)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			s.writeComputerAssetsV3Error(w, ctx, err)
			return
		}
		// Encode the whole response before success headers/bytes, never stream a
		// partial result if a hostile text projection exceeds the fixed cap.
		raw, err := json.Marshal(out.value)
		defer clear(raw)
		if err != nil || len(raw) > domains.DirectoryV3PageMaxBytes {
			s.writeComputerAssetsV3Error(w, ctx, fail(422, "directory_limit_exceeded"))
			return
		}
		if err = ctx.Err(); err != nil {
			s.writeComputerAssetsV3Error(w, ctx, err)
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	})
}

func (s *Service) writeComputerAssetsV3Error(w http.ResponseWriter, ctx context.Context, err error) {
	status, code := domainHTTPError(err)
	if status != http.StatusServiceUnavailable {
		action := "computer_assets_v3_list"
		s.recordFailure(ctx, action)
	}
	write(w, status, map[string]string{"error": code})
}

func (s *Service) handleComputerAssetsV3(ctx context.Context, r *http.Request, store *domains.Store, f domains.ComputerAssetsV3Filter) (*credentialUseResult, error) {
	conn, err := dbadmission.ConnectConfig(ctx, s.database.Copy())
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
	if !computerAssetsV3PathAllowed(r.Method, r.URL.Path, grants) {
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
	value, err := store.ComputerAssetsV3ListTx(ctx, tx, actor.tenant, allowed, f)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
