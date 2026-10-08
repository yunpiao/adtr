package directoryassets

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func accumulatorV2(t *testing.T, l Limits) *AccumulatorV2 {
	t.Helper()
	a, err := NewAccumulatorV2(l)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestV2AccumulatorCookieLifecycleAndDeepResults(t *testing.T) {
	a := accumulatorV2(t, limits())
	cookie := []byte{0, 255, 1}
	e := entryV2(1)
	if err := a.AddPage([]Entry{e}, nil, cookie); err != nil {
		t.Fatal(err)
	}
	if rows, err := a.Result(); rows != nil || !errors.Is(err, ErrIncomplete) {
		t.Fatal("incomplete observation escaped")
	}
	cookie[0] = 4
	e.Attributes[5].Values[0][0] = 99
	if err := a.AddPage([]Entry{entryV2(2)}, []byte{0, 255, 1}, []byte{0, 255, 1}); err != nil {
		t.Fatal("repeated opaque cookie rejected", err)
	}
	if err := a.AddPage(nil, []byte{0, 255, 1}, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Result()
	if err != nil || len(rows) != 2 || rows[0].Supplemental.MailBytes[0] != ' ' {
		t.Fatal("input aliases retained result", err)
	}
	rows[0].Base.Classes[0] = "tampered"
	*rows[0].Base.SAMAccountName = "tampered"
	*rows[0].Base.UserAccountControl = 99
	rows[0].Supplemental.ObjectSIDBytes[0] = 99
	rows[0].Supplemental.MailBytes[0] = 99
	rows[0].Supplemental.DescriptionBytes[0][0] = 99
	rows[0].Supplemental.WhenCreatedBytes[0] = 99
	second, err := a.Result()
	if err != nil || !reflect.DeepEqual(second[0], objectV2(t)) {
		t.Fatal("result aliases retained observation", err)
	}
	retained := a.rows[0].Supplemental.MailBytes
	a.Discard()
	if !bytes.Equal(retained, make([]byte, len(retained))) {
		t.Fatal("discard retained raw bytes")
	}
	if rows, err := a.Result(); rows != nil || !errors.Is(err, ErrClosed) {
		t.Fatal("discarded result still available")
	}
	if !reflect.DeepEqual(second[0], objectV2(t)) {
		t.Fatal("discard mutated caller-owned prior result")
	}
	ClearStoredObjectsV2(second)
	ClearStoredObjectsV2(rows)
	a.Discard()
}

func TestV2AccumulatorFailureClearsPriorBuffersAndIsTerminal(t *testing.T) {
	cases := []struct {
		name              string
		entries           []Entry
		request, response []byte
		want              error
	}{
		{"wrong request", nil, []byte("wrong"), nil, ErrPage},
		{"prior duplicate", []Entry{entryV2(1)}, []byte("next"), nil, ErrDuplicate},
		{"page duplicate", []Entry{entryV2(2), entryV2(2)}, []byte("next"), nil, ErrDuplicate},
		{"bad second entry", []Entry{entryV2(2), entry(3, "contact")}, []byte("next"), nil, ErrKind},
		{"cookie limit", nil, []byte("next"), make([]byte, 17), ErrLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := accumulatorV2(t, limits())
			if err := a.AddPage([]Entry{entryV2(1)}, nil, []byte("next")); err != nil {
				t.Fatal(err)
			}
			o := a.rows[0]
			aliases := [][]byte{a.cookie, o.Supplemental.ObjectSIDBytes, o.Supplemental.MailBytes, o.Supplemental.DescriptionBytes[0], o.Supplemental.WhenCreatedBytes}
			if err := a.AddPage(tc.entries, tc.request, tc.response); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if rows, err := a.Result(); rows != nil || !errors.Is(err, tc.want) {
				t.Fatal("failed observation escaped", err)
			}
			if err := a.AddPage(nil, nil, nil); !errors.Is(err, tc.want) {
				t.Fatal("failed observation restarted")
			}
			for _, alias := range aliases {
				if !bytes.Equal(alias, make([]byte, len(alias))) {
					t.Fatal("failure retained raw bytes/cookie")
				}
			}
			if a.rows != nil || a.cookie != nil || a.ids != nil {
				t.Fatal("failure retained observation references")
			}
		})
	}
}

func TestV2AccumulatorBillsLargerStoredOrPublicEncodingPlusCookies(t *testing.T) {
	for _, controls := range []bool{false, true} {
		t.Run(map[bool]string{false: "stored larger", true: "public larger"}[controls], func(t *testing.T) {
			e := entryV2(1)
			if controls {
				e.Attributes[6].Values[0] = []byte(strings.Repeat("\x00", 1024))
			} else {
				e.Attributes[6].Values[0] = []byte(strings.Repeat("a", 1024))
			}
			o, err := DecodeV2(e)
			if err != nil {
				t.Fatal(err)
			}
			stored := encodedV2(t, o)
			public, err := ProjectV2(o)
			if err != nil {
				t.Fatal(err)
			}
			publicRaw, _ := json.Marshal(public)
			if controls != (len(publicRaw) > len(stored)) {
				t.Fatal("fixture does not select intended larger representation")
			}
			cost := max(len(stored), len(publicRaw))
			l := limits()
			l.MaxBytes = cost + 4
			a := accumulatorV2(t, l)
			if err := a.AddPage([]Entry{e}, nil, []byte("same")); err != nil || a.used != cost+4 {
				t.Fatal("exact encoded plus cookie boundary rejected", err)
			}
			if err := a.AddPage(nil, []byte("same"), nil); err != nil {
				t.Fatal("zero-cost completion rejected", err)
			}
			l.MaxBytes--
			a = accumulatorV2(t, l)
			if err := a.AddPage([]Entry{e}, nil, []byte("same")); !errors.Is(err, ErrLimit) {
				t.Fatal("encoded/cookie cost bypassed", err)
			}
			l.MaxBytes = cost + 7
			a = accumulatorV2(t, l)
			if err := a.AddPage([]Entry{e}, nil, []byte("same")); err != nil {
				t.Fatal(err)
			}
			if err := a.AddPage(nil, []byte("same"), []byte("same")); !errors.Is(err, ErrLimit) {
				t.Fatal("repeated received cookies were not billed")
			}
		})
	}
}

func TestV2AccumulatorEmptyCompletionAndBoundedNoProgress(t *testing.T) {
	a := accumulatorV2(t, limits())
	if err := a.AddPage(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Result()
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty successful observation lost")
	}
	if err := a.AddPage(nil, nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatal("post-completion page accepted")
	}
	l := limits()
	l.MaxPages = 2
	a = accumulatorV2(t, l)
	if a.AddPage(nil, nil, []byte("same")) != nil || a.AddPage(nil, []byte("same"), []byte("same")) != nil {
		t.Fatal("bounded repeated cookies rejected")
	}
	if err := a.AddPage(nil, []byte("same"), nil); !errors.Is(err, ErrLimit) {
		t.Fatal("no-progress page loop unbounded")
	}
	for _, tc := range []struct {
		name   string
		change func(*Limits)
	}{
		{"rows", func(l *Limits) { l.MaxRows = 1 }},
		{"page entries", func(l *Limits) { l.MaxPageEntries = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := limits()
			tc.change(&l)
			a := accumulatorV2(t, l)
			if err := a.AddPage([]Entry{entryV2(1), entryV2(2)}, nil, nil); !errors.Is(err, ErrLimit) {
				t.Fatal("explicit cap ignored")
			}
		})
	}
	for _, invalid := range []Limits{{}, {1, 1, 1, 1, 0}, {100001, 1, 1, 1, 1}, {1, 10001, 1, 1, 1}, {1, 1, 1001, 1, 1}, {1, 1, 1, 64<<20 + 1, 1}, {1, 1, 1, 1, 65537}} {
		if a, err := NewAccumulatorV2(invalid); a != nil || !errors.Is(err, ErrLimits) {
			t.Fatal("invalid local limits accepted")
		}
	}
}
