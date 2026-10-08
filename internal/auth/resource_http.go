package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/credentialuse"
)

// ServeResources handles local scope management. A /check response is not a bearer
// authorization and must never replace an actual AD operation's in-transaction check.
func (s *Service) ServeResources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, auditContextKey{}, newAuditContext(r))
	r = r.WithContext(ctx)
	path, prefixed := strings.CutPrefix(r.URL.Path, "/api/resources")
	route, ok := resourceRoutes[path]
	if !prefixed || !ok {
		write(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		write(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	var in resourceRequest
	if route.method == "POST" {
		if r.Header.Get("Origin") != s.origin {
			write(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
		if err = decodeResourceRequest(w, r, route, &in); err != nil {
			write(w, 400, map[string]string{"error": "invalid_input"})
			return
		}
	}
	value, err := s.handleResource(ctx, r, path, route, in)
	if err != nil {
		s.recordFailure(ctx, "resource"+path)
		if credentialuse.IsGovernanceError(err) {
			err = fail(403, "credential_use_governance_required")
		}
		var f failure
		var p *pgconn.PgError
		if errors.As(err, &f) {
			if f.status == 429 {
				w.Header().Set("Retry-After", "900")
			}
			write(w, f.status, map[string]string{"error": f.code})
		} else if errors.As(err, &p) && p.Code == "23505" {
			write(w, 409, map[string]string{"error": "name_conflict"})
		} else {
			write(w, 500, map[string]string{"error": "internal"})
		}
		return
	}
	if out, ok := value.(map[string]any); ok && out["sessionRevoked"] == true {
		s.cookie(w, "")
	}
	write(w, 200, value)
}
func decodeResourceRequest(w http.ResponseWriter, r *http.Request, route resourceRoute, in *resourceRequest) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
	if err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return fail(400, "invalid_input")
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fail(400, "invalid_input")
	}
	allowed := map[string]bool{}
	for _, f := range strings.Fields(route.fields) {
		allowed[f] = true
	}
	if route.write {
		allowed["actorPassword"] = true
		allowed["totpCode"] = true
	}
	for k, v := range fields {
		if !allowed[k] || string(v) == "null" {
			return fail(400, "invalid_input")
		}
	}
	if meta, ok := fields["meta"]; ok {
		object, e := resourceExactObject(meta, "name mark datas")
		if e != nil {
			return e
		}
		if datas, ok := object["datas"]; ok {
			if e = resourceExactArray(datas, "appName resources"); e != nil {
				return e
			}
		}
	}
	if checks, ok := fields["resources"]; ok {
		if e := resourceExactArray(checks, "application dataResource"); e != nil {
			return e
		}
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err = d.Decode(in); err != nil {
		return err
	}
	// Reject duplicate keys, including nested fields: encoding/json otherwise
	// silently accepts the final value, making authorization inputs ambiguous.
	return resourceUniqueJSON(json.NewDecoder(strings.NewReader(string(raw))))
}
func resourceUniqueJSON(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return fail(400, "invalid_input")
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return fail(400, "invalid_input")
			}
			seen[key] = true
			if e = resourceUniqueJSON(d); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := resourceUniqueJSON(d); e != nil {
				return e
			}
		}
	default:
		return fail(400, "invalid_input")
	}
	_, err = d.Token()
	return err
}
func (s *Service) handleResource(ctx context.Context, r *http.Request, path string, route resourceRoute, in resourceRequest) (any, error) {
	if route.method == "POST" {
		if len(r.URL.Query()) != 0 {
			return nil, fail(400, "invalid_input")
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if err := s.rate(ctx, "ip:"+ip, 60); err != nil {
			return nil, err
		}
	}
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
	actor, role, grants, err := s.authenticateAccess(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if !resourceRouteAllows(role, grants, route) {
		return nil, fail(403, "forbidden")
	}
	if route.write {
		if err = s.requireAccessProof(ctx, tx, &actor, in.ActorPassword, in.TOTPCode); err != nil {
			return nil, err
		}
		op := credentialuse.ManageRoles
		if path == "/tenant/save" {
			op = credentialuse.ManageTenant
		}
		if err = setCredentialGovernance(ctx, tx, actor, op); err != nil {
			return nil, err
		}
	}
	var value any
	switch path {
	case "/groups":
		value, err = s.listResourceGroups(ctx, tx, actor, r.URL.Query())
	case "/groups/detail":
		if err = resourceQueryOnly(r.URL.Query(), "id"); err == nil {
			var m ResourceMeta
			m, err = s.getResourceGroup(ctx, tx, actor.tenant, r.URL.Query().Get("id"))
			value = map[string]any{"meta": m}
		}
	case "/groups/exists":
		if err = resourceQueryOnly(r.URL.Query(), "name"); err == nil {
			n := r.URL.Query().Get("name")
			if !validResourceName(n) {
				err = fail(400, "invalid_input")
			} else {
				var exists bool
				err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.resource_groups WHERE tenant_id=$1 AND lower(name)=lower($2))", actor.tenant, n).Scan(&exists)
				value = map[string]bool{"isExist": exists}
			}
		}
	case "/groups/create", "/groups/update", "/groups/delete":
		value, err = s.mutateResourceGroup(ctx, tx, actor, role, path, in)
	case "/groups/roles":
		value, err = s.listResourceRoles(ctx, tx, actor, r.URL.Query())
	case "/groups/assign":
		value, err = s.assignResourceRoles(ctx, tx, actor, role, in)
	case "/tenant":
		if len(r.URL.Query()) != 0 {
			err = fail(400, "invalid_input")
		} else {
			var v ResourceTenant
			v, err = s.getResourceTenant(ctx, tx, actor.tenant)
			value = v
		}
	case "/tenant/save":
		value, err = s.saveResourceTenant(ctx, tx, actor, in)
	case "/grants", "/check":
		if len(r.URL.Query()) != 0 {
			err = fail(400, "invalid_input")
			break
		}
		if path == "/check" {
			if err = validateResourceChecks(in); err != nil {
				break
			}
		}
		var ids []string
		ids, err = s.effectiveResourceIDs(ctx, tx, actor, role)
		if err != nil {
			break
		}
		if path == "/grants" {
			list := []map[string]any{}
			if len(ids) > 0 {
				list = append(list, map[string]any{"application": LocalResourceApplication, "resources": ids})
			}
			value = map[string]any{"list": list, "authorizationScopeOnly": true}
		} else {
			granted := map[string]bool{}
			for _, id := range ids {
				granted[id] = true
			}
			results := make([]bool, len(in.Resources))
			for i, check := range in.Resources {
				allow := true
				for _, id := range check.DataResource {
					allow = allow && granted[id]
				}
				results[i] = allow
			}
			value = map[string]any{"results": results, "authorizationScopeOnly": true}
		}
	default:
		err = fail(404, "not_found")
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return value, nil
}

// encoding/json matches struct keys case-insensitively. Validate exact nested
// names before decoding so name/Name aliases cannot bypass duplicate rejection.
func resourceExactObject(raw json.RawMessage, keys string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, fail(400, "invalid_input")
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields(keys) {
		allowed[key] = true
	}
	for key := range object {
		if !allowed[key] {
			return nil, fail(400, "invalid_input")
		}
	}
	return object, nil
}
func resourceExactArray(raw json.RawMessage, keys string) error {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || list == nil {
		return fail(400, "invalid_input")
	}
	for _, item := range list {
		if _, err := resourceExactObject(item, keys); err != nil {
			return err
		}
	}
	return nil
}
