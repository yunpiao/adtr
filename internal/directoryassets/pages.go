package directoryassets

import (
	"bytes"
	"encoding/json"
	"errors"
)

var (
	ErrLimits     = errors.New("invalid_directory_limits")
	ErrLimit      = errors.New("directory_limit_exceeded")
	ErrPage       = errors.New("invalid_directory_page")
	ErrDuplicate  = errors.New("duplicate_directory_object")
	ErrIncomplete = errors.New("directory_observation_incomplete")
	ErrClosed     = errors.New("directory_observation_closed")
)

// MaxBytes bounds encoded object bytes plus received cookie bytes, not total Go
// heap use. Every cap is explicit; zero never means unlimited.
type Limits struct{ MaxRows, MaxPages, MaxPageEntries, MaxBytes, MaxCookieBytes int }
type Accumulator struct {
	limits      Limits
	rows        []Object
	ids         map[string]bool
	cookie      []byte
	pages, used int
	done        bool
	failed      error
}

func NewAccumulator(l Limits) (*Accumulator, error) {
	if l.MaxRows < 1 || l.MaxRows > 100000 || l.MaxPages < 1 || l.MaxPages > 10000 || l.MaxPageEntries < 1 || l.MaxPageEntries > 1000 || l.MaxBytes < 1 || l.MaxBytes > 64<<20 || l.MaxCookieBytes < 1 || l.MaxCookieBytes > 65536 {
		return nil, ErrLimits
	}
	return &Accumulator{limits: l, ids: make(map[string]bool)}, nil
}
func (a *Accumulator) String() string   { return "directory observation (redacted)" }
func (a *Accumulator) GoString() string { return a.String() }
func (a *Accumulator) fail(err error) error {
	if a.failed == nil {
		a.failed = err
	}
	a.rows = nil
	a.ids = nil
	clear(a.cookie)
	a.cookie = nil
	return a.failed
}

// AddPage must receive the exact opaque cookie used for this request. Cookies
// may legitimately repeat; page/row/byte caps bound no-progress loops. An empty
// response cookie alone terminates a successful paged search.
func (a *Accumulator) AddPage(entries []Entry, requestCookie, responseCookie []byte) error {
	if a.failed != nil {
		return a.failed
	}
	if a.done {
		return a.fail(ErrClosed)
	}
	if len(requestCookie) > a.limits.MaxCookieBytes || len(responseCookie) > a.limits.MaxCookieBytes {
		return a.fail(ErrLimit)
	}
	if !bytes.Equal(requestCookie, a.cookie) {
		return a.fail(ErrPage)
	}
	if a.pages >= a.limits.MaxPages || len(entries) > a.limits.MaxPageEntries || len(entries) > a.limits.MaxRows-len(a.rows) {
		return a.fail(ErrLimit)
	}
	cost := len(responseCookie)
	if cost > a.limits.MaxBytes-a.used {
		return a.fail(ErrLimit)
	}
	page := make([]Object, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		object, err := Decode(entry)
		if err != nil {
			return a.fail(err)
		}
		if a.ids[object.GUID] || seen[object.GUID] {
			return a.fail(ErrDuplicate)
		}
		seen[object.GUID] = true
		encoded, err := json.Marshal(object)
		if err != nil {
			return a.fail(ErrEntry)
		}
		if len(encoded) > a.limits.MaxBytes-a.used-cost {
			return a.fail(ErrLimit)
		}
		cost += len(encoded)
		page = append(page, object)
	}
	for id := range seen {
		a.ids[id] = true
	}
	a.rows = append(a.rows, page...)
	a.pages++
	a.used += cost
	clear(a.cookie)
	a.cookie = append([]byte(nil), responseCookie...)
	a.done = len(responseCookie) == 0
	return nil
}
func (a *Accumulator) Result() ([]Object, error) {
	if a.failed != nil {
		return nil, a.failed
	}
	if !a.done {
		return nil, ErrIncomplete
	}
	out := make([]Object, len(a.rows))
	for i, o := range a.rows {
		out[i] = cloneObject(o)
	}
	return out, nil
}
