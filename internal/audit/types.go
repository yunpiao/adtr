// Package audit provides tenant- and resource-scoped immutable platform history.
package audit

import (
	"fmt"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const SchemaVersion = schemaversion.Current
const ExportKind = "audit.export"
const MaxExportRows = 100000
const ExportBatchSize = 10000
const MaxArtifactBytes = 128 * 1024 * 1024

// Principal is supplied by authenticated server code, never decoded from HTTP.
type Principal struct {
	TenantID string
	ActorID  int64
	RoleID   string
}
type Filter struct {
	PageIdx     int      `json:"pageIdx,omitempty"`
	PageSize    int      `json:"pageSize,omitempty"`
	StartTm     string   `json:"startTm,omitempty"`
	EndTm       string   `json:"endTm,omitempty"`
	Keyword     string   `json:"keyword,omitempty"`
	FilterEvent []string `json:"filterEvent"`
	CreateSort  int      `json:"createSort,omitempty"`
	LogTypeList []int    `json:"logTypeList"`
	Visibility  string   `json:"visibility,omitempty"`
}
type Row struct {
	ID                string          `json:"ID"`
	LoginUser         *string         `json:"loginUser"`
	SourceIP          *string         `json:"sourceIp"`
	Event             string          `json:"event"`
	EventArgs         string          `json:"eventArgs"`
	EventResult       string          `json:"eventResult"`
	CreateTm          time.Time       `json:"CreateTm"`
	LogType           int             `json:"logType"`
	LogTypeName       string          `json:"logTypeName"`
	UserID            *int64          `json:"userId"`
	Source            string          `json:"source"`
	SourceID          int64           `json:"-"`
	DomainID          *string         `json:"domainId"`
	Availability      map[string]bool `json:"availability"`
	Deleted           bool            `json:"deleted"`
	Deletable         bool            `json:"deletable"`
	VisibilityVersion int64           `json:"visibilityVersion"`
}
type Page struct {
	Index int `json:"pageIdx"`
	Size  int `json:"pageSize"`
	Total int `json:"total"`
	Pages int `json:"totalPage"`
}
type List struct {
	Page      Page  `json:"page"`
	List      []Row `json:"List"`
	Exhausted bool  `json:"exhausted"`
}
type Column struct {
	Prop  string `json:"prop"`
	Label string `json:"label"`
}

var columns = []Column{{"userId", "用户ID"}, {"loginUser", "登录用户"}, {"sourceIp", "登录IP"}, {"logTypeName", "审计类型"}, {"event", "事件"}, {"eventArgs", "事件参数"}, {"eventResult", "事件结果"}, {"CreateTm", "审计时间"}}
var typeNames = []string{"", "威胁事件", "身份欺骗", "规则配置", "主动检测", "应用接入", "威胁阻断", "资产", "报告与审计", "系统设置"}

func Columns() []Column { return append([]Column(nil), columns...) }
func TypeName(n int) string {
	if n > 0 && n < len(typeNames) {
		return typeNames[n]
	}
	return "未知"
}

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func problem(status int, code string) error { return &Error{status, code} }

var stableID = regexp.MustCompile(`^(auth|resource|task|audit|domain|operation_account|credential_use|operational_log)\.([1-9][0-9]{0,18})$`)

func ParseID(id string) (string, int64, error) {
	m := stableID.FindStringSubmatch(id)
	if m == nil {
		return "", 0, problem(400, "invalid_input")
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil || n <= 0 {
		return "", 0, problem(400, "invalid_input")
	}
	return m[1], n, nil
}
func ValidText(s string, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{PageIdx: 1, PageSize: 20, CreateSort: -1, Visibility: "visible", FilterEvent: []string{}, LogTypeList: []int{}}
	for k, v := range q {
		if k == "filterEvent" {
			f.FilterEvent = append([]string(nil), v...)
			continue
		}
		if k == "logTypeList" {
			for _, s := range v {
				n, e := strconv.Atoi(s)
				if e != nil || strconv.Itoa(n) != s {
					return f, problem(400, "invalid_input")
				}
				f.LogTypeList = append(f.LogTypeList, n)
			}
			continue
		}
		if len(v) != 1 || v[0] == "" {
			return f, problem(400, "invalid_input")
		}
		s := v[0]
		switch k {
		case "pageIdx", "pageSize", "createSort":
			n, e := strconv.Atoi(s)
			if e != nil || strconv.Itoa(n) != s || n == 0 {
				return f, problem(400, "invalid_input")
			}
			if k == "pageIdx" {
				f.PageIdx = n
			} else if k == "pageSize" {
				f.PageSize = n
			} else {
				f.CreateSort = n
			}
		case "startTm":
			f.StartTm = s
		case "endTm":
			f.EndTm = s
		case "keyword":
			f.Keyword = s
		case "visibility":
			f.Visibility = s
		default:
			return f, problem(400, "invalid_input")
		}
	}
	return NormalizeFilter(f)
}
func NormalizeFilter(f Filter) (Filter, error) {
	if f.PageIdx == 0 {
		f.PageIdx = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.CreateSort == 0 {
		f.CreateSort = -1
	}
	if f.Visibility == "" {
		f.Visibility = "visible"
	}
	if f.PageIdx < 1 || f.PageIdx > 1000000 || f.PageSize != -1 && (f.PageSize < 1 || f.PageSize > 100) || f.PageSize == -1 && f.PageIdx != 1 || f.CreateSort != 1 && f.CreateSort != -1 || f.Visibility != "visible" && f.Visibility != "hidden" && f.Visibility != "all" || !ValidText(f.Keyword, 50) {
		return f, problem(400, "invalid_input")
	}
	var start, end time.Time
	var err error
	if f.StartTm != "" {
		start, err = time.Parse(time.RFC3339Nano, f.StartTm)
		if err != nil || start.Year() < 1 || start.Year() > 9999 {
			return f, problem(400, "invalid_input")
		}
		f.StartTm = start.UTC().Format(time.RFC3339Nano)
	}
	if f.EndTm != "" {
		end, err = time.Parse(time.RFC3339Nano, f.EndTm)
		if err != nil || end.Year() < 1 || end.Year() > 9999 {
			return f, problem(400, "invalid_input")
		}
		f.EndTm = end.UTC().Format(time.RFC3339Nano)
	}
	if f.StartTm != "" && f.EndTm != "" && !start.Before(end) {
		return f, problem(400, "invalid_input")
	}
	if len(f.FilterEvent) > 100 || len(f.LogTypeList) > 9 {
		return f, problem(400, "invalid_input")
	}
	events := map[string]bool{}
	for _, v := range f.FilterEvent {
		if (v == "" || !ValidText(v, 128)) || events[v] {
			return f, problem(400, "invalid_input")
		}
		events[v] = true
	}
	types := map[int]bool{}
	for _, v := range f.LogTypeList {
		if v < 1 || v > 9 || types[v] {
			return f, problem(400, "invalid_input")
		}
		types[v] = true
	}
	if f.FilterEvent == nil {
		f.FilterEvent = []string{}
	}
	if f.LogTypeList == nil {
		f.LogTypeList = []int{}
	}
	return f, nil
}
func ValidateColumns(selected []string) error {
	if len(selected) < 1 || len(selected) > 8 {
		return problem(400, "invalid_columns")
	}
	seen := map[string]bool{}
	for _, s := range selected {
		found := false
		for _, c := range columns {
			if c.Prop == s {
				found = true
			}
		}
		if !found || seen[s] {
			return problem(400, "invalid_columns")
		}
		seen[s] = true
	}
	return nil
}
func ValidateTargets(ids []string, reason string) error {
	if len(ids) < 1 || len(ids) > 100 || strings.TrimSpace(reason) == "" || !ValidText(reason, 500) {
		return problem(400, "invalid_input")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, _, e := ParseID(id); e != nil || seen[id] {
			return problem(400, "invalid_input")
		}
		seen[id] = true
	}
	return nil
}
func (r Row) Cells(selected []string) []string {
	out := make([]string, len(selected))
	for i, c := range selected {
		switch c {
		case "userId":
			if r.UserID != nil {
				out[i] = strconv.FormatInt(*r.UserID, 10)
			}
		case "loginUser":
			if r.LoginUser != nil {
				out[i] = *r.LoginUser
			}
		case "sourceIp":
			if r.SourceIP != nil {
				out[i] = *r.SourceIP
			}
		case "logTypeName":
			out[i] = r.LogTypeName
		case "event":
			out[i] = r.Event
		case "eventArgs":
			out[i] = r.EventArgs
		case "eventResult":
			out[i] = r.EventResult
		case "CreateTm":
			out[i] = r.CreateTm.UTC().Format(time.RFC3339Nano)
		}
	}
	return out
}
func Headers(selected []string) []string {
	out := make([]string, len(selected))
	for i, s := range selected {
		for _, c := range columns {
			if c.Prop == s {
				out[i] = c.Label
			}
		}
	}
	return out
}
func sourceID(source string, id int64) string { return fmt.Sprintf("%s.%d", source, id) }
