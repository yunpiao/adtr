package domains

import (
	"net/url"
	"slices"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// ParseSelectionFilter accepts only the frozen Stage A vocabulary. Unsupported
// legacy filters are explicit errors, so clients cannot mistake ignored filters
// for a supported application, inventory or license decision.
func ParseSelectionFilter(q url.Values) (SelectionFilter, error) {
	f := SelectionFilter{PageIdx: 1, PageSize: 20}
	legacy := false
	for k, values := range q {
		if len(values) != 1 {
			return f, problem(400, "invalid_input")
		}
		switch k {
		case "pageIdx", "pageSize", "keyword", "observationState":
		case "appType", "application", "type", "ModuleType", "status", "statusList", "scanType":
			legacy = true
		default:
			return f, problem(400, "invalid_input")
		}
	}
	for k, values := range q {
		v := values[0]
		switch k {
		case "pageIdx", "pageSize":
			n, e := strconv.Atoi(v)
			if e != nil || strconv.Itoa(n) != v {
				return f, problem(400, "invalid_input")
			}
			if k == "pageIdx" {
				if n < 1 || n > 1000000 {
					return f, problem(400, "invalid_input")
				}
				f.PageIdx = n
			} else {
				if !slices.Contains([]int{10, 20, 30, 40, 50}, n) {
					return f, problem(400, "invalid_input")
				}
				f.PageSize = n
			}
		case "keyword":
			if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 50 {
				return f, problem(400, "invalid_input")
			}
			for _, r := range v {
				if unicode.IsControl(r) {
					return f, problem(400, "invalid_input")
				}
			}
			f.Keyword = v
		case "observationState":
			if !slices.Contains([]string{"unverified", "testing", "verified", "error"}, v) {
				return f, problem(400, "invalid_input")
			}
			f.ObservationState = v
		}
	}
	if legacy {
		return f, problem(422, "unsupported_source_filter")
	}
	return f, nil
}

func ParseSelectionInput(q url.Values) (SelectionInput, error) {
	var in SelectionInput
	if len(q) != 3 || len(q["domainId"]) != 1 || len(q["expectedRevision"]) != 1 || len(q["expectedCredentialRevision"]) != 1 {
		return in, problem(400, "invalid_input")
	}
	in = SelectionInput{q.Get("domainId"), q.Get("expectedRevision"), q.Get("expectedCredentialRevision")}
	if !ValidID(in.DomainID) {
		return in, problem(400, "invalid_input")
	}
	if _, e := Revision(in.ExpectedRevision); e != nil {
		return in, e
	}
	if _, e := Revision(in.ExpectedCredentialRevision); e != nil {
		return in, e
	}
	return in, nil
}
