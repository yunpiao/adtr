package auth

import (
	"encoding/json"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type AccessAuth struct {
	Readable  bool `json:"readable"`
	Writeable bool `json:"writeable"`
}
type AccessGrant struct {
	Mark string     `json:"mark"`
	Auth AccessAuth `json:"auth"`
}
type AccessPath struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Auth string `json:"auth"`
}
type AccessPermission struct {
	Name      string             `json:"name"`
	Mark      string             `json:"mark"`
	Children  []AccessPermission `json:"children"`
	Paths     []AccessPath       `json:"paths"`
	Checked   bool               `json:"checked"`
	Auth      AccessAuth         `json:"auth"`
	AllowAuth AccessAuth         `json:"allow_auth"`
	Icon      string             `json:"icon"`
}
type accessAssignment struct {
	Username string `json:"username"`
	RoleID   string `json:"roleID"`
}
type accessRequest struct {
	Username      string                     `json:"username"`
	Password      string                     `json:"password"`
	ActorPassword string                     `json:"actorPassword"`
	TOTPCode      string                     `json:"totpCode"`
	RoleID        *string                    `json:"roleID"`
	Role          *string                    `json:"role"`
	Mobile        *string                    `json:"mobile"`
	Email         *string                    `json:"email"`
	Remark        *string                    `json:"remark"`
	Address       *string                    `json:"address"`
	RealName      *string                    `json:"realName"`
	Department    *string                    `json:"department"`
	Post          *string                    `json:"post"`
	Disabled      *bool                      `json:"disabled"`
	RoleName      string                     `json:"roleName"`
	Permissions   []AccessGrant              `json:"permissions"`
	DataSrc       map[string]json.RawMessage `json:"dataSrc"`
	UserRoles     []accessAssignment         `json:"userRoles"`
	Paths         []string                   `json:"paths"`
}
type AccessUser struct {
	ID              int64     `json:"ID"`
	Username        string    `json:"username"`
	PassStrength    string    `json:"passStrength"`
	Role            string    `json:"role"`
	Priv            int       `json:"priv"`
	Mobile          string    `json:"mobile"`
	Email           string    `json:"email"`
	Remark          string    `json:"remark"`
	Created         time.Time `json:"createTm"`
	HasMFA          bool      `json:"hasMfa"`
	Avatar          string    `json:"avatar"`
	PasswordUpdated time.Time `json:"pwdUpdateTm"`
	Address         string    `json:"address"`
	RealName        string    `json:"realName"`
	Department      string    `json:"department"`
	Post            string    `json:"post"`
	RoleID          string    `json:"roleID"`
	RoleName        string    `json:"roleName"`
	Disabled        bool      `json:"disabled"`
}
type AccessRole struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	UserNum     int                 `json:"userNum"`
	Remark      string              `json:"remark"`
	AllowDelete bool                `json:"allowDelete"`
	AllowEdit   bool                `json:"allowEdit"`
	Created     time.Time           `json:"created"`
	DataSrc     map[string][]string `json:"dataSrc"`
}
type accessPage struct {
	Index int `json:"pageIdx"`
	Size  int `json:"pageSize"`
	Total int `json:"total"`
	Pages int `json:"totalPage"`
}
type accessFilter struct {
	page, size, sort                                     int
	search, roleID                                       string
	self                                                 bool
	roles, mfa, strength                                 []string
	startCreated, endCreated, startPassword, endPassword *time.Time
}
type accessRoute struct {
	method, mark string
	write        bool
	fields       string
	also         string
}

// Registry is explicit: no prefix inference, wildcard or client-supplied route.
var accessRoutes = map[string]accessRoute{
	"/users": {"GET", "users", false, "", ""}, "/users/exists": {"GET", "users", false, "", ""},
	"/users/create": {"POST", "users", true, "username password roleID role mobile email remark address realName department post", ""},
	"/users/update": {"POST", "users", true, "username roleID role mobile email remark address realName department post disabled", ""},
	"/users/delete": {"POST", "users", true, "username", ""},
	"/roles":        {"GET", "roles", false, "", ""}, "/roles/detail": {"GET", "roles", false, "", ""}, "/roles/exists": {"GET", "roles", false, "", ""},
	"/roles/save": {"POST", "roles", true, "roleID roleName remark permissions dataSrc", "permissions"}, "/roles/delete": {"POST", "roles", true, "roleID", ""},
	"/assignments": {"POST", "roles", true, "userRoles", ""},
	"/permissions": {"GET", "permissions", false, "", ""}, "/permissions/save": {"POST", "permissions", true, "roleID permissions", ""},
	"/menu": {"GET", "", false, "", ""}, "/check": {"POST", "", false, "paths", ""},
}
var accessMarks = []string{"users", "roles", "permissions", "tasks"}

func validAccessText(s string, max int) bool {
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
func validRoleName(s string) bool {
	return validAccessText(s, 50) && strings.TrimSpace(s) == s && s != "" && !strings.EqualFold(s, "platform_admin") && !strings.EqualFold(s, "viewer")
}
func validateAccessProfile(in accessRequest) error {
	for _, v := range []*string{in.Address, in.RealName, in.Department, in.Post} {
		if v != nil && !validAccessText(*v, 50) {
			return fail(400, "invalid_input")
		}
	}
	if in.Remark != nil && !validAccessText(*in.Remark, 150) {
		return fail(400, "invalid_input")
	}
	if in.Mobile != nil && *in.Mobile != "" {
		v := *in.Mobile
		if len(v) != 11 || v[0] != '1' || v[1] < '3' || v[1] > '9' {
			return fail(400, "invalid_input")
		}
		for _, c := range v {
			if c < '0' || c > '9' {
				return fail(400, "invalid_input")
			}
		}
	}
	if in.Email != nil && *in.Email != "" {
		v := *in.Email
		a, e := mail.ParseAddress(v)
		if e != nil || a.Address != v || len(v) > 254 || !validAccessText(v, 254) || !strings.Contains(v, ".") {
			return fail(400, "invalid_input")
		}
	}
	return nil
}
func accessString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func accessBuiltin(id string) bool { return id == "platform_admin" || id == "viewer" }
func validRoleID(id string) bool {
	if accessBuiltin(id) {
		return true
	}
	if len(id) != 24 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validateGrants(grants []AccessGrant) (map[string]AccessAuth, error) {
	out := map[string]AccessAuth{}
	for _, g := range grants {
		if !slices.Contains(accessMarks, g.Mark) {
			return nil, fail(400, "unknown_permission")
		}
		if _, ok := out[g.Mark]; ok {
			return nil, fail(400, "duplicate_permission")
		}
		if g.Auth.Writeable && !g.Auth.Readable {
			return nil, fail(400, "invalid_permission")
		}
		out[g.Mark] = g.Auth
	}
	return out, nil
}
func grantAllows(grants map[string]AccessAuth, route accessRoute) bool {
	if route.also != "" && !grants[route.also].Writeable {
		return false
	}
	if route.mark == "" {
		return true
	}
	g := grants[route.mark]
	if route.write {
		return g.Writeable
	}
	return g.Readable
}
func accessCanDelegate(actor, desired map[string]AccessAuth) bool {
	for mark, g := range desired {
		a := actor[mark]
		if g.Readable && !a.Readable || g.Writeable && !a.Writeable {
			return false
		}
	}
	return true
}
func parseAccessFilter(q url.Values, roles bool) (accessFilter, error) {
	f := accessFilter{page: 1, size: 20, sort: -1}
	allowed := map[string]bool{"pageIdx": true, "pageSize": true, "sort": true, "search": true}
	if !roles {
		for _, k := range []string{"isSelf", "roleID", "filterRole", "filterMfaStatus", "filterPassStrength", "filterStartCreateTm", "filterEndCreateTm", "filterStartPassTm", "filterEndPassTm"} {
			allowed[k] = true
		}
	}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 && k != "filterRole" && k != "filterMfaStatus" && k != "filterPassStrength" {
			return f, fail(400, "invalid_input")
		}
	}
	for k, dst := range map[string]*int{"pageIdx": &f.page, "pageSize": &f.size, "sort": &f.sort} {
		if v, ok := q[k]; ok {
			n, e := strconv.Atoi(v[0])
			if e != nil {
				return f, fail(400, "invalid_input")
			}
			*dst = n
		}
	}
	if f.page < 1 || f.page > 1000000 || f.size != -1 && (f.size < 1 || f.size > 100) || f.size == -1 && f.page != 1 || f.sort != 1 && f.sort != -1 && (roles || f.sort != 2 && f.sort != -2) {
		return f, fail(400, "invalid_input")
	}
	f.search = q.Get("search")
	if !validAccessText(f.search, 50) {
		return f, fail(400, "invalid_input")
	}
	if v, ok := q["isSelf"]; ok {
		if v[0] != "true" && v[0] != "false" {
			return f, fail(400, "invalid_input")
		}
		f.self = v[0] == "true"
	}
	f.roleID = q.Get("roleID")
	if f.roleID != "" && !validRoleID(f.roleID) {
		return f, fail(400, "invalid_input")
	}
	f.roles = q["filterRole"]
	f.mfa = q["filterMfaStatus"]
	f.strength = q["filterPassStrength"]
	if len(f.roles) > 100 || len(f.mfa) > 2 || len(f.strength) > 3 {
		return f, fail(400, "invalid_input")
	}
	for _, v := range f.roles {
		if !validRoleID(v) {
			return f, fail(400, "invalid_input")
		}
	}
	for _, v := range f.mfa {
		if v == "disable" {
			return f, fail(400, "unsupported_mfa_status")
		}
		if v != "enable" && v != "stop" {
			return f, fail(400, "invalid_input")
		}
	}
	for _, v := range f.strength {
		if v != "high" && v != "middle" && v != "low" {
			return f, fail(400, "invalid_input")
		}
	}
	for k, dst := range map[string]**time.Time{"filterStartCreateTm": &f.startCreated, "filterEndCreateTm": &f.endCreated, "filterStartPassTm": &f.startPassword, "filterEndPassTm": &f.endPassword} {
		if v, ok := q[k]; ok {
			tm, e := time.Parse(time.RFC3339Nano, v[0])
			if e != nil {
				return f, fail(400, "invalid_input")
			}
			*dst = &tm
		}
	}
	if f.startCreated != nil && f.endCreated != nil && !f.startCreated.Before(*f.endCreated) || f.startPassword != nil && f.endPassword != nil && !f.startPassword.Before(*f.endPassword) {
		return f, fail(400, "invalid_input")
	}
	return f, nil
}
func makeAccessPage(f accessFilter, total int) (accessPage, int, int, error) {
	size := f.size
	if size == -1 {
		if total > 1000 {
			return accessPage{}, 0, 0, fail(422, "result_too_large")
		}
		size = 1000
	}
	pages := (total + size - 1) / size
	return accessPage{f.page, f.size, total, pages}, size, (f.page - 1) * size, nil
}
