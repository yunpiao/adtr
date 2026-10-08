package audit

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/tasks"
)

// HistoryFilter is a local, explicit F38 contract. Source status aliases and
// unsupported report producers are not silently mapped to audit exports.
type HistoryFilter struct {
	PageIdx, PageSize, SortTm int
	Status                    []string
	StartTm, EndTm            string
}
type ExportHistory struct {
	Page      Page               `json:"page"`
	List      []ExportHistoryRow `json:"list"`
	Exhausted bool               `json:"exhausted"`
}
type ExportHistoryRow struct {
	TaskUUID       string      `json:"taskUUID"`
	FileName       string      `json:"fileName"`
	ModelType      string      `json:"modelType"`
	FileType       string      `json:"fileType"`
	State          tasks.State `json:"state"`
	Progress       int         `json:"progress"`
	Error          string      `json:"error"`
	CreatedAt      time.Time   `json:"createdAt"`
	UpdatedAt      time.Time   `json:"updatedAt"`
	Attempt        int         `json:"attempt"`
	MaxAttempts    int         `json:"maxAttempts"`
	NextAttemptAt  *time.Time  `json:"nextAttemptAt,omitempty"`
	DownloadReady  bool        `json:"downloadReady"`
	DownloadStatus string      `json:"downloadStatus"`
	RowCount       *int        `json:"rowCount,omitempty"`
	SnapshotAt     *time.Time  `json:"snapshotAt,omitempty"`
	DownloadPath   string      `json:"downloadPath,omitempty"`
}

var historyTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?(Z|[+-][0-9]{2}:[0-9]{2})$`)

func historyTime(raw string) (string, error) {
	if !historyTimePattern.MatchString(raw) {
		return "", problem(400, "invalid_input")
	}
	// time.Parse permits a +24 hour or 60-minute offset in some Go versions;
	// require actual RFC3339 zone ranges before normalization.
	if raw[len(raw)-1] != 'Z' {
		zone := raw[len(raw)-6:]
		hour, _ := strconv.Atoi(zone[1:3])
		minute, _ := strconv.Atoi(zone[4:])
		if hour > 23 || minute > 59 {
			return "", problem(400, "invalid_input")
		}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || t.Year() < 1 || t.Year() > 9999 || t.UTC().Year() < 1 || t.UTC().Year() > 9999 {
		return "", problem(400, "invalid_input")
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}
func historyInteger(raw string, min, max int) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max || strconv.Itoa(n) != raw {
		return 0, problem(400, "invalid_input")
	}
	return n, nil
}
func ParseHistoryFilter(q url.Values) (HistoryFilter, error) {
	f := HistoryFilter{PageIdx: 1, PageSize: 20, SortTm: -1, Status: []string{}}
	bad := func() (HistoryFilter, error) { return HistoryFilter{}, problem(400, "invalid_input") }
	for key, values := range q {
		if len(values) == 0 {
			return bad()
		}
		for _, v := range values {
			if v == "" || !utf8.ValidString(v) {
				return bad()
			}
		}
		if key == "appType" {
			return HistoryFilter{}, problem(400, "unsupported_app_type")
		}
		if key == "modelType" {
			if len(values) > 5 {
				return bad()
			}
			seen := map[string]bool{}
			unsupported := false
			for _, v := range values {
				if !ValidText(v, 32) || strings.TrimSpace(v) != v || seen[v] {
					return bad()
				}
				seen[v] = true
				unsupported = unsupported || v != "Audit"
			}
			if unsupported {
				return HistoryFilter{}, problem(400, "unsupported_model_type")
			}
			continue
		}
		if key == "status" {
			if len(values) > 9 {
				return bad()
			}
			seen := map[string]bool{}
			for _, v := range values {
				if !tasks.State(v).Valid() || seen[v] {
					return bad()
				}
				seen[v] = true
				f.Status = append(f.Status, v)
			}
			continue
		}
		if len(values) != 1 {
			return bad()
		}
		v := values[0]
		var err error
		switch key {
		case "pageIdx":
			f.PageIdx, err = historyInteger(v, 1, 1000000)
		case "pageSize":
			if v == "-1" {
				f.PageSize = -1
			} else {
				f.PageSize, err = historyInteger(v, 1, 100)
			}
		case "sortTm":
			if v == "1" {
				f.SortTm = 1
			} else if v != "-1" {
				return bad()
			}
		case "startTm":
			f.StartTm, err = historyTime(v)
		case "endTm":
			f.EndTm, err = historyTime(v)
		default:
			return bad()
		}
		if err != nil {
			return HistoryFilter{}, err
		}
	}
	if f.PageSize == -1 && f.PageIdx != 1 {
		return bad()
	}
	if f.StartTm != "" && f.EndTm != "" {
		start, _ := time.Parse(time.RFC3339Nano, f.StartTm)
		end, _ := time.Parse(time.RFC3339Nano, f.EndTm)
		if !start.Before(end) {
			return bad()
		}
	}
	return f, nil
}
