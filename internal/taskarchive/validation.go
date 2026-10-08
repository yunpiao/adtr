package taskarchive

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yunpiao/adtr/internal/tasks"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var utcTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?Z$`)

func problem(status int, code string) error { return &tasks.Error{Status: status, Code: code} }

// ParseBefore accepts UTC RFC3339 at PostgreSQL's exact microsecond precision.
// Rejecting finer values avoids rounding a strict cutoff past a target instant.
func ParseBefore(raw string) (time.Time, error) {
	if !utcTimestamp.MatchString(raw) {
		return time.Time{}, problem(400, "invalid_input")
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || t.Year() < 1 {
		return time.Time{}, problem(400, "invalid_input")
	}
	return t.UTC(), nil
}

func validateTargets(targets []Target, reason, key string) ([]Target, error) {
	if len(targets) < 1 || len(targets) > 100 || !identifier.MatchString(key) || !utf8.ValidString(reason) || strings.TrimSpace(reason) != reason || utf8.RuneCountInString(reason) < 1 || utf8.RuneCountInString(reason) > 500 {
		return nil, problem(400, "invalid_input")
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return nil, problem(400, "invalid_input")
		}
	}
	out := append([]Target(nil), targets...)
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	for i, t := range out {
		if !identifier.MatchString(t.TaskID) || t.VisibilityVersion < 0 || i > 0 && out[i-1].TaskID == t.TaskID {
			return nil, problem(400, "invalid_input")
		}
	}
	return out, nil
}
func ValidateArchive(in ArchiveInput) error {
	if _, err := validateTargets(in.Targets, in.Reason, in.IdempotencyKey); err != nil {
		return err
	}
	_, err := ParseBefore(in.Before)
	return err
}
func ValidateRestore(in RestoreInput) error {
	_, err := validateTargets(in.Targets, in.Reason, in.IdempotencyKey)
	return err
}
func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{PageIdx: 1, PageSize: 20}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return f, problem(400, "invalid_input")
		}
		if key == "before" {
			f.Before = values[0]
			continue
		}
		if key != "pageIdx" && key != "pageSize" {
			return f, problem(400, "invalid_input")
		}
		n, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(n) != values[0] || n < 1 || key == "pageIdx" && n > 1000000 || key == "pageSize" && n > 100 {
			return f, problem(400, "invalid_input")
		}
		if key == "pageIdx" {
			f.PageIdx = n
		} else {
			f.PageSize = n
		}
	}
	_, err := ParseBefore(f.Before)
	return f, err
}
