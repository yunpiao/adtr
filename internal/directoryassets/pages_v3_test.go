package directoryassets

import "testing"

func TestAccumulatorV3CallerCanOnlyTightenBudgets(t *testing.T) {
	max := Limits{MaxRows: 10000, MaxPages: 100, MaxPageEntries: 1000, MaxBytes: 16 << 20, MaxCookieBytes: 65536}
	if _, err := NewAccumulatorV3(max); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Limits){
		func(l *Limits) { l.MaxRows++ }, func(l *Limits) { l.MaxPages++ }, func(l *Limits) { l.MaxPageEntries++ }, func(l *Limits) { l.MaxBytes++ }, func(l *Limits) { l.MaxCookieBytes++ },
	} {
		l := max
		change(&l)
		if a, err := NewAccumulatorV3(l); err != ErrLimits || a != nil {
			t.Fatal("caller enlarged v3 budget")
		}
	}
	small := Limits{MaxRows: 1, MaxPages: 1, MaxPageEntries: 1, MaxBytes: 1, MaxCookieBytes: 1}
	if _, err := NewAccumulatorV3(small); err != nil {
		t.Fatal("tighter budget rejected", err)
	}
}
