package timeconfig

import (
	"testing"
	"time"
)

func makeTZifFixture(t *testing.T, offsets []int32, transitions []int64, indices []byte) *time.Location {
	t.Helper()
	// TZif2: one minimal 32-bit block, then complete 64-bit transition data.
	b := []byte{}
	add32 := func(v uint32) { b = append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) }
	header := func(nt, nz uint32) {
		b = append(b, []byte("TZif2")...)
		b = append(b, make([]byte, 15)...)
		for _, v := range []uint32{0, 0, 0, nt, nz, 2} {
			add32(v)
		}
	}
	header(0, 1)
	add32(0)
	b = append(b, 0, 0, 'X', 0)
	header(uint32(len(transitions)), uint32(len(offsets)))
	for _, v := range transitions {
		u := uint64(v)
		for shift := 56; shift >= 0; shift -= 8 {
			b = append(b, byte(u>>shift))
		}
	}
	b = append(b, indices...)
	for _, off := range offsets {
		add32(uint32(off))
		b = append(b, 0, 0)
	}
	b = append(b, 'X', 0, '\n', '\n')
	loc, err := time.LoadLocationFromTZData("Synthetic", b)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
func TestExtremeTZifAndZeroInstantBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		offs []int32
		at   []int64
		ix   []byte
		wall string
		code string
	}{
		{"full int32 fold", []int32{2147483647, -2147483648}, []int64{1704067200}, []byte{1}, "2024-01-01 00:00:00", "ambiguous_time"},
		{"full int32 gap", []int32{-2147483648, 2147483647}, []int64{1704067200}, []byte{1}, "2024-01-01 00:00:00", "nonexistent_time"},
		{"transition at Go zero instant", []int32{0, -3600}, []int64{-62135596800}, []byte{1}, "0001-01-01 00:00:00", ""},
		{"fold requires traversal across Go zero instant", []int32{0, 3600, 1800}, []int64{-62135596800, -62135593200}, []byte{1, 2}, "0001-01-01 01:45:00", "ambiguous_time"},
		{"one second alternate offset fold", []int32{0, -1}, []int64{1704067200, 1704067201}, []byte{1, 0}, "2023-12-31 23:59:59", "ambiguous_time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := makeTZifFixture(t, tc.offs, tc.at, tc.ix)
			w, err := time.Parse(dateLayout, tc.wall)
			if err != nil {
				t.Fatal(err)
			}
			_, got := resolveWall(w, l)
			if got != tc.code {
				t.Fatalf("got %s want %s", got, tc.code)
			}
		})
	}
}
