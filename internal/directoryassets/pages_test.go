package directoryassets

import (
	"encoding/json"
	"errors"
	"testing"
)

func limits() Limits {
	return Limits{MaxRows: 4, MaxPages: 4, MaxPageEntries: 2, MaxBytes: 16384, MaxCookieBytes: 16}
}
func accumulator(t *testing.T, l Limits) *Accumulator {
	t.Helper()
	a, err := NewAccumulator(l)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestPagedObservationUsesOpaqueCookiesAndDefensiveResults(t *testing.T) {
	a := accumulator(t, limits())
	cookie := []byte{0, 255, 1}
	e := entry(1, "user", "top")
	e.Attributes = append(e.Attributes, Attribute{"samaccountname", [][]byte{[]byte("Synthetic")}})
	if err := a.AddPage([]Entry{e}, nil, cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Result(); !errors.Is(err, ErrIncomplete) {
		t.Fatal("partial result escaped")
	}
	cookie[0] = 9 // Caller cannot alter the retained next-request token.
	if err := a.AddPage([]Entry{entry(2, "computer", "user")}, []byte{0, 255, 1}, []byte{0, 255, 1}); err != nil {
		t.Fatal("opaque token may repeat", err)
	}
	if err := a.AddPage(nil, []byte{0, 255, 1}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := a.Result()
	if err != nil || len(result) != 2 {
		t.Fatal("complete result unavailable", err)
	}
	result[0].Classes[0] = "tampered"
	*result[0].SAMAccountName = "tampered"
	next, _ := a.Result()
	if next[0].Classes[0] == "tampered" || *next[0].SAMAccountName == "tampered" {
		t.Fatal("caller mutated retained observation")
	}
}
func TestEmptyObservationAndTerminalFailure(t *testing.T) {
	a := accumulator(t, limits())
	if err := a.AddPage(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	r, err := a.Result()
	if err != nil || r == nil || len(r) != 0 {
		t.Fatal("empty completed observation lost")
	}
	if err := a.AddPage(nil, nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatal("extra terminal page accepted")
	}
	if _, err := a.Result(); !errors.Is(err, ErrClosed) {
		t.Fatal("failed observation still publishable")
	}
}
func TestPageFailuresDiscardEveryPreviouslyCollectedRow(t *testing.T) {
	cases := []struct {
		name              string
		next              []Entry
		request, response []byte
		want              error
	}{
		{"wrong request", nil, []byte("wrong"), nil, ErrPage},
		{"duplicate identity", []Entry{entry(1, "group")}, []byte("next"), nil, ErrDuplicate},
		{"duplicate in page", []Entry{entry(2, "user"), entry(2, "user")}, []byte("next"), nil, ErrDuplicate},
		{"malformed", []Entry{entry(2, "contact")}, []byte("next"), nil, ErrKind},
		{"cookie cap", nil, []byte("next"), make([]byte, 17), ErrLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := accumulator(t, limits())
			if err := a.AddPage([]Entry{entry(1, "user")}, nil, []byte("next")); err != nil {
				t.Fatal(err)
			}
			if err := a.AddPage(tc.next, tc.request, tc.response); !errors.Is(err, tc.want) {
				t.Fatal("unexpected failure", err)
			}
			if r, err := a.Result(); r != nil || !errors.Is(err, tc.want) {
				t.Fatal("failed page retained publishable rows")
			}
			if a.rows != nil || a.ids != nil || a.cookie != nil {
				t.Fatal("failed observation retained internal data")
			}
		})
	}
}
func TestExplicitResourceCaps(t *testing.T) {
	for _, l := range []Limits{{}, {MaxRows: 100001, MaxPages: 1, MaxPageEntries: 1, MaxBytes: 100, MaxCookieBytes: 1}} {
		if _, err := NewAccumulator(l); !errors.Is(err, ErrLimits) {
			t.Fatal("unbounded policy accepted")
		}
	}
	l := limits()
	l.MaxRows = 1
	a := accumulator(t, l)
	if err := a.AddPage([]Entry{entry(1, "user"), entry(2, "user")}, nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("row cap ignored")
	}
	l = limits()
	l.MaxPageEntries = 1
	a = accumulator(t, l)
	if err := a.AddPage([]Entry{entry(1, "user"), entry(2, "user")}, nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("page entry cap ignored")
	}
	l = limits()
	l.MaxPages = 2
	a = accumulator(t, l)
	if err := a.AddPage(nil, nil, []byte("same")); err != nil {
		t.Fatal(err)
	}
	if err := a.AddPage(nil, []byte("same"), []byte("same")); err != nil {
		t.Fatal(err)
	}
	if err := a.AddPage(nil, []byte("same"), nil); !errors.Is(err, ErrLimit) {
		t.Fatal("empty page loop unbounded")
	}
	object, _ := Decode(entry(1, "user"))
	encoded, _ := json.Marshal(object)
	l = limits()
	l.MaxBytes = len(encoded)
	a = accumulator(t, l)
	if err := a.AddPage([]Entry{entry(1, "user")}, nil, nil); err != nil {
		t.Fatal("exact byte boundary rejected", err)
	}
	l.MaxBytes--
	a = accumulator(t, l)
	if err := a.AddPage([]Entry{entry(1, "user")}, nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("encoded byte cap ignored")
	}
}
