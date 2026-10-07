package auth

import (
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// LocalResourceApplication is a local API identifier, not a guessed legacy enum.
const LocalResourceApplication = "ad"

type ResourceData struct {
	AppName   string   `json:"appName"`
	Resources []string `json:"resources"`
}
type ResourceMeta struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Datas          []ResourceData `json:"datas"`
	Mark           string         `json:"mark"`
	ApplyRoleCount int            `json:"applyRoleCount"`
	CreateTime     time.Time      `json:"createTime"`
}
type resourceMetaInput struct {
	Name  string         `json:"name"`
	Mark  string         `json:"mark"`
	Datas []ResourceData `json:"datas"`
}
type ResourceTenant struct {
	MaxADCount int    `json:"maxAdCount"`
	ExpireTime int64  `json:"expireTime"`
	UID        string `json:"uid"`
	Name       string `json:"name"`
}
type resourceCheck struct {
	Application  string   `json:"application"`
	DataResource []string `json:"dataResource"`
}
type resourceRequest struct {
	ActorPassword string             `json:"actorPassword"`
	TOTPCode      string             `json:"totpCode"`
	ID            string             `json:"id"`
	Meta          *resourceMetaInput `json:"meta"`
	RoleIDs       []string           `json:"roleIds"`
	ResourceType  *int               `json:"resourceType"`
	Resources     []resourceCheck    `json:"resources"`
	MaxADCount    *int               `json:"maxAdCount"`
	ExpireTime    *int64             `json:"expireTime"`
	UID           *string            `json:"uid"`
	Name          *string            `json:"name"`
}
type resourceRoute struct {
	method, fields            string
	management, write, tenant bool
}

var resourceRoutes = map[string]resourceRoute{
	"/groups":        {method: "GET", management: true},
	"/groups/detail": {method: "GET", management: true},
	"/groups/exists": {method: "GET", management: true},
	"/groups/roles":  {method: "GET", management: true},
	"/groups/create": {method: "POST", management: true, write: true, fields: "meta"},
	"/groups/update": {method: "POST", management: true, write: true, fields: "id meta"},
	"/groups/delete": {method: "POST", management: true, write: true, fields: "id"},
	"/groups/assign": {method: "POST", management: true, write: true, fields: "id roleIds"},
	"/tenant":        {method: "GET", management: true, tenant: true},
	"/tenant/save":   {method: "POST", management: true, write: true, tenant: true, fields: "maxAdCount expireTime uid name"},
	"/grants":        {method: "GET"},
	"/check":         {method: "POST", fields: "resourceType resources"},
}

func resourceText(s string, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func validResourceName(s string) bool {
	if s == "" || len(s) > 256 || !resourceText(s, 256) {
		return false
	}
	for _, c := range s {
		if unicode.IsSpace(c) {
			return false
		}
	}
	return true
}
func validResourceID(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func resourceMembers(meta *resourceMetaInput) ([]string, error) {
	if meta == nil || !validResourceName(meta.Name) || !resourceText(meta.Mark, 500) || meta.Datas == nil || len(meta.Datas) > 1 {
		return nil, fail(400, "invalid_input")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, data := range meta.Datas {
		if data.AppName != LocalResourceApplication {
			return nil, fail(422, "unsupported_application")
		}
		if data.Resources == nil || len(data.Resources) > 1000 {
			return nil, fail(400, "invalid_input")
		}
		for _, id := range data.Resources {
			if !validResourceID(id) || seen[id] {
				return nil, fail(400, "invalid_input")
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func validateResourceChecks(in resourceRequest) error {
	if in.ResourceType == nil || *in.ResourceType != 2 {
		return fail(422, "unsupported_resource_type")
	}
	if len(in.Resources) < 1 || len(in.Resources) > 100 {
		return fail(400, "invalid_input")
	}
	for _, r := range in.Resources {
		if r.Application != LocalResourceApplication {
			return fail(422, "unsupported_application")
		}
		if len(r.DataResource) < 1 || len(r.DataResource) > 1000 {
			return fail(400, "invalid_input")
		}
		seen := map[string]bool{}
		for _, id := range r.DataResource {
			if !validResourceID(id) || seen[id] {
				return fail(400, "invalid_input")
			}
			seen[id] = true
		}
	}
	return nil
}
func resourceTenantInput(in resourceRequest) (ResourceTenant, error) {
	if in.MaxADCount == nil || in.ExpireTime == nil || in.UID == nil || in.Name == nil {
		return ResourceTenant{}, fail(400, "invalid_input")
	}
	v := ResourceTenant{*in.MaxADCount, *in.ExpireTime, *in.UID, *in.Name}
	if v.MaxADCount < 0 || v.MaxADCount > 100000 || v.ExpireTime < 0 || v.ExpireTime > 253402300799 || strings.TrimSpace(v.UID) != v.UID || v.UID == "" || !resourceText(v.UID, 256) || strings.TrimSpace(v.Name) != v.Name || v.Name == "" || !resourceText(v.Name, 32) {
		return ResourceTenant{}, fail(400, "invalid_input")
	}
	return v, nil
}

type resourceFilter struct {
	page, size int
	name       string
	ascending  bool
}

func parseResourceFilter(q url.Values, roleList bool) (resourceFilter, error) {
	f := resourceFilter{page: 1, size: 20}
	for k, v := range q {
		if len(v) != 1 || !(k == "pageIdx" || k == "pageSize" || !roleList && (k == "name" || k == "sort") || roleList && k == "id") {
			return f, fail(400, "invalid_input")
		}
	}
	for k, dst := range map[string]*int{"pageIdx": &f.page, "pageSize": &f.size} {
		if v, ok := q[k]; ok {
			n, e := strconv.Atoi(v[0])
			if e != nil {
				return f, fail(400, "invalid_input")
			}
			*dst = n
		}
	}
	if f.page < 1 || f.page > 1000000 || f.size != -1 && (f.size < 1 || f.size > 100) || f.size == -1 && f.page != 1 {
		return f, fail(400, "invalid_input")
	}
	if !roleList {
		f.name = q.Get("name")
		if !resourceText(f.name, 50) {
			return f, fail(400, "invalid_input")
		}
		if v, ok := q["sort"]; ok {
			if v[0] != "true" && v[0] != "false" {
				return f, fail(400, "invalid_input")
			}
			f.ascending = v[0] == "true"
		}
	}
	return f, nil
}
func resourcePage(f resourceFilter, total int) (accessPage, int, int, error) {
	return makeAccessPage(accessFilter{page: f.page, size: f.size}, total)
}
func resourceQueryOnly(q url.Values, key string) error {
	if len(q) != 1 || len(q[key]) != 1 || q.Get(key) == "" {
		return fail(400, "invalid_input")
	}
	return nil
}

// resourceRouteAllows is shared by real HTTP enforcement and function-path
// introspection. Tenant configuration is a builtin-admin policy, not an inferred
// consequence of a custom role carrying every function permission.
func resourceRouteAllows(role string, grants map[string]AccessAuth, route resourceRoute) bool {
	if !route.management {
		return true
	}
	if route.tenant {
		return role == "platform_admin"
	}
	grant := grants["roles"]
	if route.write {
		return grant.Writeable
	}
	return grant.Readable
}
func resourcePathAllowed(method, path, role string, grants map[string]AccessAuth) bool {
	suffix, prefixed := strings.CutPrefix(path, "/api/resources")
	route, known := resourceRoutes[suffix]
	return prefixed && known && method == route.method && resourceRouteAllows(role, grants, route)
}
