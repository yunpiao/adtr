package simulationdetectionschedules

import (
	"errors"
	"testing"
	"time"
)

func TestCalendarMonthClampNeverDrifts(t *testing.T) {
	a := time.Date(2024, 1, 31, 23, 30, 12, 0, time.UTC)
	for _, tc := range []struct {
		i    int64
		want string
	}{{0, "2024-01-31T23:30:12Z"}, {1, "2024-02-29T23:30:12Z"}, {2, "2024-03-31T23:30:12Z"}, {13, "2025-02-28T23:30:12Z"}} {
		got, e := CalendarAt(a, 2, tc.i)
		if e != nil || got.Format(time.RFC3339) != tc.want {
			t.Fatal(tc, got, e)
		}
	}
}
func TestCalendarDailyWeeklyAndLatest(t *testing.T) {
	a := time.Date(2024, 2, 28, 12, 0, 0, 0, time.FixedZone("offset", 8*3600))
	for cycle := 0; cycle < 3; cycle++ {
		for i := int64(0); i < 100; i++ {
			at, e := CalendarAt(a, cycle, i)
			if e != nil {
				t.Fatal(e)
			}
			got, e := latestIndex(a, cycle, at)
			if e != nil || got != i {
				t.Fatal(cycle, i, got, e)
			}
			got, e = latestIndex(a, cycle, at.Add(-time.Second))
			if e != nil || got != i-1 {
				t.Fatal(cycle, i, got, e)
			}
		}
	}
	if got, e := CalendarAt(a, 1, 1); e != nil || got.Sub(a) != 7*24*time.Hour {
		t.Fatal(got, e)
	}
}
func TestCalendarRejectsOverflowAndInvalid(t *testing.T) {
	a := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	for c := 0; c < 3; c++ {
		if _, e := CalendarAt(a, c, 1); !errors.Is(e, ErrLimit) {
			t.Fatal(c, e)
		}
	}
	for _, c := range []int{-1, 3} {
		if _, e := CalendarAt(a, c, 0); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	if _, e := CalendarAt(a, 0, -1); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestDefinitionValidation(t *testing.T) {
	base := Definition{Label: "Synthetic", Cycle: 2, TemplateID: "template", TemplateVersion: 1, DatasetID: "dataset"}
	if e := validateDefinition(base); e != nil {
		t.Fatal(e)
	}
	for _, edit := range []func(*Definition){func(d *Definition) { d.Cycle = 3 }, func(d *Definition) { d.Label = " hidden " }, func(d *Definition) { d.Label = "x\ny" }, func(d *Definition) { d.TemplateVersion = 0 }, func(d *Definition) { d.DatasetID = "../unsafe" }} {
		d := base
		edit(&d)
		if e := validateDefinition(d); !errors.Is(e, ErrInvalid) {
			t.Fatal(d, e)
		}
	}
}
