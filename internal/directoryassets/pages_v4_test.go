package directoryassets

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAccumulatorV4CallerCanOnlyTightenBudgets(t *testing.T) {
	max := Limits{MaxRows: 10000, MaxPages: 100, MaxPageEntries: 1000, MaxBytes: 16 << 20, MaxCookieBytes: 65536}
	if _, err := NewAccumulatorV4(max); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Limits){
		func(l *Limits) { l.MaxRows++ }, func(l *Limits) { l.MaxPages++ }, func(l *Limits) { l.MaxPageEntries++ }, func(l *Limits) { l.MaxBytes++ }, func(l *Limits) { l.MaxCookieBytes++ },
	} {
		l := max
		change(&l)
		if a, err := NewAccumulatorV4(l); err != ErrLimits || a != nil {
			t.Fatal("caller enlarged v4 budget")
		}
	}
	small := Limits{MaxRows: 1, MaxPages: 1, MaxPageEntries: 1, MaxBytes: 1, MaxCookieBytes: 1}
	if _, err := NewAccumulatorV4(small); err != nil {
		t.Fatal("tighter budget rejected", err)
	}
}

func TestAccumulatorV4ExactCostsAndTerminalBudgets(t *testing.T) {
	for _, value := range []string{strings.Repeat("a", 4096), strings.Repeat("\x00", 4096)} {
		e := memberEntryV4(value)
		o, err := DecodeV4(e)
		if err != nil {
			t.Fatal(err)
		}
		stored, _ := EncodeStoredObjectV4(o)
		p, _ := ProjectV4(o)
		public, _ := json.Marshal(p)
		cost := max(len(stored), len(public))
		ClearStoredObjectsV4([]StoredObjectV4{o})
		for _, budget := range []int{cost + 4, cost + 3} {
			l := limits()
			l.MaxBytes = budget
			a, _ := NewAccumulatorV4(l)
			err := a.AddPage([]Entry{e}, nil, []byte("next"))
			if budget == cost+4 {
				if err != nil || a.used != budget {
					t.Fatal("exact cost", err)
				}
				if err = a.AddPage(nil, []byte("next"), nil); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != ErrLimit {
					t.Fatal("undersized cost", err)
				}
			}
			a.Discard()
		}
	}
	for _, tc := range []struct {
		name              string
		change            func(*Limits)
		entries           []Entry
		request, response []byte
		want              error
	}{
		{"rows", func(l *Limits) { l.MaxRows = 1 }, []Entry{entry(2, "group")}, []byte("next"), nil, ErrLimit},
		{"pages", func(l *Limits) { l.MaxPages = 1 }, nil, []byte("next"), nil, ErrLimit},
		{"pageEntries", func(l *Limits) { l.MaxPageEntries = 1 }, []Entry{entry(2, "group"), entry(3, "group")}, []byte("next"), nil, ErrLimit},
		{"requestCookie", func(l *Limits) {}, nil, []byte("wrong"), nil, ErrPage},
		{"responseCookie", func(l *Limits) { l.MaxCookieBytes = 4 }, nil, []byte("next"), []byte("large"), ErrLimit},
		{"requestCookieLimit", func(l *Limits) { l.MaxCookieBytes = 4 }, nil, []byte("large"), nil, ErrLimit},
		{"duplicate", func(l *Limits) {}, []Entry{entry(1, "group")}, []byte("next"), nil, ErrDuplicate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := limits()
			tc.change(&l)
			a, _ := NewAccumulatorV4(l)
			if err := a.AddPage([]Entry{memberEntryV4("owned")}, nil, []byte("next")); err != nil {
				t.Fatal(err)
			}
			cookie, member := a.cookie, a.rows[0].Membership.MemberBytes[0]
			if err := a.AddPage(tc.entries, tc.request, tc.response); err != tc.want {
				t.Fatal(err)
			}
			if err := a.AddPage(nil, nil, nil); err != tc.want {
				t.Fatal("failure not terminal")
			}
			if got, err := a.Result(); err != tc.want || got != nil {
				t.Fatal("failure escaped")
			}
			if !bytes.Equal(cookie, make([]byte, len(cookie))) || !bytes.Equal(member, make([]byte, len(member))) {
				t.Fatal("failed bytes not cleared")
			}
		})
	}
	a, _ := NewAccumulatorV4(limits())
	if err := a.AddPage(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Result()
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty success")
	}
	if err := a.AddPage(nil, nil, nil); err != ErrClosed {
		t.Fatal("completed restarted")
	}
}
