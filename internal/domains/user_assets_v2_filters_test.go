package domains

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestUserAssetsV2FilterExactSAMBounds(t *testing.T) {
	for _, raw := range []string{"", " ", "A\x00😀é e\u0301 Σς İi .*[]%_", strings.Repeat("a", 256), strings.Repeat("😀", 128), strings.Repeat("界", 256)} {
		f := validUserAssetsV2Filter()
		f.SAMAccountName = raw
		if err := ValidateUserAssetsV2Filter(f); err != nil {
			t.Fatalf("valid exact SAM rejected %q: %v", raw, err)
		}
	}
	for _, raw := range []string{strings.Repeat("a", 257), strings.Repeat("😀", 129), strings.Repeat("界", 342), string([]byte{255})} {
		f := validUserAssetsV2Filter()
		f.SAMAccountName = raw
		requireUserAssetsV2Error(t, ValidateUserAssetsV2Filter(f), 400, "invalid_input")
	}
}

func TestUserAssetsV2StrictNanosecondTimeRange(t *testing.T) {
	for _, tc := range []struct{ start, end string }{
		{"0001-01-01T00:00:00Z", "0001-01-01T00:00:00.000000001Z"},
		{"2026-10-11T22:00:00.123456789+09:00", "2026-10-11T13:00:00.123456790Z"},
		{"9999-12-31T23:59:59.999999998Z", "9999-12-31T23:59:59.999999999Z"},
		{"2024-02-29T00:00:00-00:00", "2024-02-29T23:59:59Z"},
	} {
		f := validUserAssetsV2Filter()
		f.StartTm, f.EndTm = tc.start, tc.end
		if err := ValidateUserAssetsV2Filter(f); err != nil {
			t.Fatal(tc, err)
		}
		a, b, err := userAssetsV2TimeRange(f)
		if err != nil || !a.Before(b) || a.Location() != time.UTC || b.Location() != time.UTC {
			t.Fatal("range lost precision/UTC", a, b, err)
		}
	}
	for _, raw := range []string{"", "0000-12-31T23:59:59Z", "0001-01-01T00:00:00+00:01", "9999-12-31T23:59:59-00:01", "2026-01-01", "2026-01-01t00:00:00z", "2026-01-01T0:00:00Z", "2026-01-01T00:00:00", "2026-01-01T00:00:00,1Z", "2026-01-01T00:00:00.1234567890Z", "2026-02-29T00:00:00Z", "2026-01-01T24:00:00Z", "2026-01-01T00:00:60Z", "2026-01-01T00:00:00+24:00", "2026-01-01T00:00:00+00:60", " 2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z\n", "２０２６-01-01T00:00:00Z"} {
		f := validUserAssetsV2Filter()
		f.StartTm, f.EndTm = raw, "9999-12-31T23:59:59Z"
		requireUserAssetsV2Error(t, ValidateUserAssetsV2Filter(f), 400, "invalid_input")
	}
	for _, tc := range []struct{ start, end string }{{"2026-01-01T00:00:00Z", ""}, {"", "2026-01-01T00:00:00Z"}, {"2026-01-01T00:00:00Z", "2026-01-01T09:00:00+09:00"}, {"2026-01-01T00:00:00.000000002Z", "2026-01-01T00:00:00.000000001Z"}} {
		f := validUserAssetsV2Filter()
		f.StartTm, f.EndTm = tc.start, tc.end
		requireUserAssetsV2Error(t, ValidateUserAssetsV2Filter(f), 400, "invalid_input")
	}
	for _, order := range []string{"1", "-1", "0", "ASC", "desc ", "created"} {
		f := validUserAssetsV2Filter()
		f.CreatTm = order
		requireUserAssetsV2Error(t, ValidateUserAssetsV2Filter(f), 400, "invalid_input")
	}
}

func TestUserAssetsV2ExactSAMIsRawByteEqualityAndAndsSearch(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	base := o.Objects[0]
	o.Objects = nil
	names := []string{"Alpha", "alpha", "Alpha.*", "é", "e\u0301", "Σ", "ς", "İ", "i", "😀", " Alpha "}
	for i, name := range names {
		v := base
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i+1)
		v.Base.SAMAccountName = &name
		o.Objects = append(o.Objects, v)
	}
	null := base
	null.Base.GUID = "ffffffff-4455-6677-8899-aabbccddeeff"
	null.Base.SAMAccountName = nil
	o.Objects = append(o.Objects, null)
	for i, name := range names {
		f := validUserAssetsV2Filter()
		f.SAMAccountName = name
		got, err := userAssetsV2Page(context.Background(), UserAssetsV2Selection{}, "snapshot", o, f)
		if err != nil || got.Page.Total != 1 || len(got.List) != 1 || got.List[0].GUID != fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i+1) {
			t.Fatal("exact SAM folded, normalized or treated as expression", name, got.Page, err)
		}
		f.Search = "no-match"
		got, err = userAssetsV2Page(context.Background(), UserAssetsV2Selection{}, "snapshot", o, f)
		if err != nil || got.Page.Total != 0 {
			t.Fatal("SAM/search must intersect", err)
		}
	}
	f := validUserAssetsV2Filter()
	f.SAMAccountName = "ALPHA"
	got, err := userAssetsV2Page(context.Background(), UserAssetsV2Selection{}, "snapshot", o, f)
	if err != nil || got.Page.Total != 0 {
		t.Fatal("unobserved case matched", err)
	}
	f.SAMAccountName = ""
	got, err = userAssetsV2Page(context.Background(), UserAssetsV2Selection{}, "snapshot", o, f)
	if err != nil || got.Page.Total != len(names)+1 {
		t.Fatal("empty SAM changed no-filter behavior", err)
	}
}

func TestUserAssetsV2CreatedRangeStablePagesNullLastAndYearOne(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	base := o.Objects[0]
	o.Objects = nil
	// Descending source insertion; three equal-time groups cross page boundaries.
	for i := 35; i >= 1; i-- {
		v := base
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		v.Supplemental = directoryassets.RawSupplementalV2{WhenCreatedBytes: []byte("20261011130000.0Z")}
		if i <= 2 {
			v.Supplemental.WhenCreatedBytes = nil
		}
		if i >= 23 {
			v.Supplemental.WhenCreatedBytes = []byte("00010101000000.0Z")
		}
		o.Objects = append(o.Objects, v)
	}
	ids := func(nums ...int) []string {
		out := []string{}
		for _, i := range nums {
			out = append(out, fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i))
		}
		return out
	}
	for _, order := range []string{"", "asc", "desc"} {
		expected := []string{}
		if order == "" {
			for i := 1; i <= 35; i++ {
				expected = append(expected, ids(i)...)
			}
		} else {
			if order == "asc" {
				for i := 23; i <= 35; i++ {
					expected = append(expected, ids(i)...)
				}
				for i := 3; i <= 22; i++ {
					expected = append(expected, ids(i)...)
				}
			} else {
				for i := 3; i <= 22; i++ {
					expected = append(expected, ids(i)...)
				}
				for i := 23; i <= 35; i++ {
					expected = append(expected, ids(i)...)
				}
			}
			expected = append(expected, ids(1, 2)...)
		}
		actual := []string{}
		for page := 1; page <= 4; page++ {
			f := validUserAssetsV2Filter()
			f.CreatTm = order
			f.PageIdx = page
			f.ObservationID = "snapshot"
			got, err := userAssetsV2Page(context.Background(), UserAssetsV2Selection{}, "snapshot", o, f)
			if err != nil || got.Page.Total != 35 || got.Page.Pages != 4 {
				t.Fatal(got.Page, err)
			}
			for _, r := range got.List {
				actual = append(actual, r.GUID)
			}
		}
		if !reflect.DeepEqual(expected, actual) {
			t.Fatal("unstable/null/year1 order", order, actual, expected)
		}
	}
	for _, tc := range []struct {
		start, end string
		total      int
	}{
		{"2026-10-11T13:00:00Z", "2026-10-11T13:00:00.000000001Z", 20},
		{"2026-10-11T12:59:59.999999999Z", "2026-10-11T13:00:00Z", 0},
		{"2026-10-11T13:00:00.000000001Z", "2026-10-11T13:00:01Z", 0},
		{"2026-10-11T22:00:00+09:00", "2026-10-11T13:00:01Z", 20},
		{"0001-01-01T00:00:00Z", "0001-01-01T00:00:00.000000001Z", 13},
	} {
		f := validUserAssetsV2Filter()
		f.StartTm, f.EndTm = tc.start, tc.end
		f.CreatTm = "desc"
		got, err := userAssetsV2Page(context.Background(), UserAssetsV2Selection{}, "snapshot", o, f)
		if err != nil || got.Page.Total != tc.total || len(got.List) != min(10, tc.total) {
			t.Fatal("half-open range/count/page mismatch", tc, got.Page, err)
		}
		for _, r := range got.List {
			if r.WhenCreated == nil {
				t.Fatal("range matched null")
			}
		}
	}
}
