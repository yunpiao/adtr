package auth

import (
	"context"
	"errors"
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

func domainSelectionPathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return method == http.MethodGet && (path == "/api/domain-selection" || path == "/api/domain-selection/resolve") && grants["domains"].Readable
}

func (s *Service) DomainSelectionHandler(store *domains.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, prefixed := strings.CutPrefix(r.URL.Path, "/api/domain-selection")
		if !prefixed || path != "" && path != "/resolve" {
			write(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			write(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if len(r.URL.RawQuery) > 32768 {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		var f domains.SelectionFilter
		var in domains.SelectionInput
		if err == nil {
			if path == "" {
				f, err = domains.ParseSelectionFilter(q)
			} else {
				in, err = domains.ParseSelectionInput(q)
			}
		} else {
			err = fail(400, "invalid_input")
		}
		if err != nil {
			status, code := domainHTTPError(err)
			write(w, status, map[string]string{"error": code})
			return
		}
		if store == nil || s.database == nil {
			write(w, 503, map[string]string{"error": "domains_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
		r = r.WithContext(ctx)
		out, err := s.handleDomainSelection(ctx, r, store, path, f, in)
		if err != nil {
			status, code := domainHTTPError(err)
			if status != 503 {
				s.recordFailure(ctx, "domain_selection"+strings.ReplaceAll(path, "/", "_"))
			}
			write(w, status, map[string]string{"error": code})
			return
		}
		w.Header().Set("X-ADTR-User-ID", strconv.FormatInt(out.actorID, 10))
		write(w, 200, out.value)
	})
}

type domainSelectionResult struct {
	value   any
	actorID int64
}

func (s *Service) handleDomainSelection(ctx context.Context, r *http.Request, store *domains.Store, path string, f domains.SelectionFilter, in domains.SelectionInput) (*domainSelectionResult, error) {
	conn, err := pgx.ConnectConfig(ctx, s.database.Copy())
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
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
	if !domainSelectionPathAllowed(r.Method, r.URL.Path, grants) {
		return nil, fail(403, "forbidden")
	}
	if _, _, err = domains.EnrollmentEligible(ctx, tx, actor.tenant, s.now()); err != nil {
		return nil, err
	}
	allowed, err := s.effectiveResourceIDs(ctx, tx, actor, role)
	if err != nil {
		return nil, err
	}
	if path == "/resolve" && !slices.Contains(allowed, in.DomainID) {
		return nil, fail(404, "not_found")
	}
	if err = setAuditMetadata(ctx, tx, actor); err != nil {
		return nil, err
	}
	var out any
	if path == "" {
		out, err = store.SelectionListTx(ctx, tx, actor.tenant, allowed, f)
	} else {
		var choice domains.Choice
		choice, err = store.SelectionResolveTx(ctx, tx, actor.tenant, allowed, in)
		out = domains.SelectionResolution{Selection: choice, CheckedAt: s.now().UTC()}
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &domainSelectionResult{value: out, actorID: actor.ID}, nil
}
