package operationallogs

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"time"
)

var explicitTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.[0-9]{1,6})?(Z|[+-](0[0-9]|1[0-9]|2[0-3]):[0-5][0-9])$`)

func NormalizeSelection(start, end string, modules []string) (Selection, error) {
	out := Selection{StartTm: start, EndTm: end, SystemType: append([]string(nil), modules...)}
	if len(modules) == 0 || len(modules) > 2 {
		return out, problem(400, "invalid_input")
	}
	seen := map[string]bool{}
	for _, m := range modules {
		if (m != string(API) && m != string(Worker)) || seen[m] {
			return out, problem(400, "invalid_input")
		}
		seen[m] = true
	}
	if !explicitTime.MatchString(start) || !explicitTime.MatchString(end) {
		return out, problem(400, "invalid_input")
	}
	a, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return out, problem(400, "invalid_input")
	}
	b, err := time.Parse(time.RFC3339Nano, end)
	if err != nil || a.Year() < 1 || b.Year() < 1 || a.UTC().Year() < 1 || b.UTC().Year() < 1 || a.UTC().Year() > 9999 || b.UTC().Year() > 9999 || !a.Before(b) || b.Sub(a) > MaxWindow {
		return out, problem(400, "invalid_input")
	}
	out.StartTm = a.UTC().Format(time.RFC3339Nano)
	out.EndTm = b.UTC().Format(time.RFC3339Nano)
	sort.Strings(out.SystemType)
	return out, nil
}
func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{PageIdx: 1, PageSize: 20, Selection: Selection{SystemType: []string{string(API), string(Worker)}}}
	for k, v := range q {
		if k == "systemType" {
			if len(v) == 0 {
				return f, problem(400, "invalid_input")
			}
			f.SystemType = append([]string(nil), v...)
			continue
		}
		if len(v) != 1 || v[0] == "" {
			return f, problem(400, "invalid_input")
		}
		switch k {
		case "startTm":
			f.StartTm = v[0]
		case "endTm":
			f.EndTm = v[0]
		case "pageIdx", "pageSize":
			n, err := strconv.Atoi(v[0])
			if err != nil || n == 0 || strconv.Itoa(n) != v[0] {
				return f, problem(400, "invalid_input")
			}
			if k == "pageIdx" {
				f.PageIdx = n
			} else {
				f.PageSize = n
			}
		default:
			return f, problem(400, "invalid_input")
		}
	}
	return normalizeFilter(f)
}
func normalizeFilter(f Filter) (Filter, error) {
	if f.PageIdx == 0 {
		f.PageIdx = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	if f.PageIdx < 1 || f.PageIdx > 1000000 || (f.PageSize != -1 && (f.PageSize < 1 || f.PageSize > 100)) || (f.PageSize == -1 && f.PageIdx != 1) {
		return f, problem(400, "invalid_input")
	}
	if len(f.SystemType) == 0 {
		f.SystemType = []string{string(API), string(Worker)}
	}
	start, end := f.StartTm, f.EndTm
	if start == "" && end == "" {
		start = "2000-01-01T00:00:00Z"
		end = "2000-01-02T00:00:00Z"
	}
	s, err := NormalizeSelection(start, end, f.SystemType)
	if err != nil {
		return f, err
	}
	f.SystemType = s.SystemType
	if f.StartTm != "" || f.EndTm != "" {
		f.StartTm = s.StartTm
		f.EndTm = s.EndTm
	}
	return f, nil
}
