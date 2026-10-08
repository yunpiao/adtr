package directoryassets

import (
	"bytes"
	"encoding/json"
)

// AccumulatorV2 owns every raw byte and cookie it retains. It is a sequential
// page consumer, not safe for concurrent mutation. MaxBytes charges the larger
// of stored/public JSON per object plus every received cookie, not Go heap use.
type AccumulatorV2 struct {
	limits      Limits
	rows        []StoredObjectV2
	ids         map[string]bool
	cookie      []byte
	pages, used int
	done        bool
	failed      error
}

func NewAccumulatorV2(l Limits) (*AccumulatorV2, error) {
	if l.MaxRows < 1 || l.MaxRows > 100000 || l.MaxPages < 1 || l.MaxPages > 10000 || l.MaxPageEntries < 1 || l.MaxPageEntries > 1000 || l.MaxBytes < 1 || l.MaxBytes > 64<<20 || l.MaxCookieBytes < 1 || l.MaxCookieBytes > 65536 {
		return nil, ErrLimits
	}
	return &AccumulatorV2{limits: l, ids: make(map[string]bool)}, nil
}

func (*AccumulatorV2) String() string     { return "directory observation v2 (redacted)" }
func (a *AccumulatorV2) GoString() string { return a.String() }

func (a *AccumulatorV2) fail(err error) error {
	if a.failed == nil {
		a.failed = err
	}
	ClearStoredObjectsV2(a.rows)
	a.rows = nil
	a.ids = nil
	clear(a.cookie)
	a.cookie = nil
	return a.failed
}

// Discard clears retained raw bytes/cookies and permanently closes the result.
// Results previously returned by Result are separate caller-owned copies.
func (a *AccumulatorV2) Discard() {
	a.fail(ErrClosed)
}

// AddPage requires the exact opaque request cookie, permits repeated response
// cookies within the explicit caps, and treats only an empty response cookie as
// completion. Any failure permanently discards the entire observation.
func (a *AccumulatorV2) AddPage(entries []Entry, requestCookie, responseCookie []byte) error {
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
	page := make([]StoredObjectV2, 0, len(entries))
	defer func() { ClearStoredObjectsV2(page) }()
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		object, err := DecodeV2(entry)
		if err != nil {
			return a.fail(err)
		}
		// Retain immediately so all subsequent failures clear this object too.
		page = append(page, object)
		if a.ids[object.Base.GUID] || seen[object.Base.GUID] {
			return a.fail(ErrDuplicate)
		}
		seen[object.Base.GUID] = true
		objectCost, err := encodedCostV2(object)
		if err != nil {
			return a.fail(err)
		}
		if objectCost > a.limits.MaxBytes-a.used-cost {
			return a.fail(ErrLimit)
		}
		cost += objectCost
	}
	for id := range seen {
		a.ids[id] = true
	}
	a.rows = append(a.rows, page...)
	page = nil // Ownership transfers only after every entry succeeds.
	a.pages++
	a.used += cost
	clear(a.cookie)
	a.cookie = bytes.Clone(responseCookie)
	a.done = len(responseCookie) == 0
	return nil
}

func encodedCostV2(object StoredObjectV2) (int, error) {
	stored, err := EncodeStoredObjectV2(object)
	if err != nil {
		return 0, err
	}
	defer clear(stored)
	public, err := ProjectV2(object)
	if err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		return 0, ErrEntry
	}
	defer clear(encoded)
	return max(len(stored), len(encoded)), nil
}

func (a *AccumulatorV2) Result() ([]StoredObjectV2, error) {
	if a.failed != nil {
		return nil, a.failed
	}
	if !a.done {
		return nil, ErrIncomplete
	}
	out := make([]StoredObjectV2, len(a.rows))
	for i, object := range a.rows {
		out[i] = cloneStoredObjectV2(object)
	}
	return out, nil
}
