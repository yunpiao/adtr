package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

const groupMembersV4Path = "/api/group-assets/v4/members"

// Stored group reads need the intersection of these two permissions, but never
// task ownership, task permissions, producer gates or credential-use authority.
func groupMembersV4PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return method == http.MethodGet && path == groupMembersV4Path && grants["domains"].Readable && grants["directory_assets"].Readable
}

func groupMembersV4Query(q url.Values) (domains.GroupMembersV4Input, error) {
	// Reuse only the exact five-key immutable detail input validator. No list
	// filters or business aliases are supported on this endpoint.
	_, in, err := userAssetsV2Query(true, q)
	return domains.GroupMembersV4Input(in), err
}

// Only the domain store is injected: these endpoints cannot submit tasks,
// decrypt credentials, open LDAP connections, or mutate an observation.
func (s *Service) GroupMembersV4Handler(store *domains.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != groupMembersV4Path {
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
		f, err := groupMembersV4Query(q)
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
		out, err := s.handleGroupMembersV4(ctx, r, store, f)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			s.writeGroupMembersV4Error(w, ctx, err)
			return
		}
		// Encode the whole response before success headers/bytes, never stream a
		// partial result if a hostile text projection exceeds the fixed cap.
		raw, err := json.Marshal(out.value)
		defer clear(raw)
		if err != nil || len(raw)+1 > domains.DirectoryV4PageMaxBytes {
			s.writeGroupMembersV4Error(w, ctx, fail(422, "directory_limit_exceeded"))
			return
		}
		if err = ctx.Err(); err != nil {
			s.writeGroupMembersV4Error(w, ctx, err)
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	})
}

func (s *Service) writeGroupMembersV4Error(w http.ResponseWriter, ctx context.Context, err error) {
	status, code := domainHTTPError(err)
	if status != http.StatusServiceUnavailable {
		action := "group_members_v4_read"
		s.recordFailure(ctx, action)
	}
	write(w, status, map[string]string{"error": code})
}

func (s *Service) handleGroupMembersV4(ctx context.Context, r *http.Request, store *domains.Store, f domains.GroupMembersV4Input) (*credentialUseResult, error) {
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
	if !groupMembersV4PathAllowed(r.Method, r.URL.Path, grants) {
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
	value, err := store.GroupMembersV4Tx(ctx, tx, actor.tenant, allowed, f)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &credentialUseResult{value: value, actorID: actor.ID}, nil
}
