package timeconfig

import "time"

// resolveWall finds every instant that round-trips to the requested wall time.
// It does not assume a transition changes the clock by exactly one hour.
func resolveWall(wall time.Time, location *time.Location) (time.Time, string) {
	seenOffsets := make(map[int]bool)
	matches := make(map[int64]time.Time)
	consider := func(offset int) {
		if seenOffsets[offset] {
			return
		}
		seenOffsets[offset] = true
		// Use seconds, not Duration or UnixNano: both overflow in the supported
		// year range, and FixedZone permits offsets larger than TZif's int32.
		seconds := wall.Unix()
		o := int64(offset)
		const maxInt64 = int64(1<<63 - 1)
		const minInt64 = -maxInt64 - 1
		if (o > 0 && seconds < minInt64+o) || (o < 0 && seconds > maxInt64+o) {
			return
		}
		candidate := time.Unix(seconds-o, 0).In(location)
		y, m, d := candidate.Date()
		h, minute, s := candidate.Clock()
		wy, wm, wd := wall.Date()
		wh, wminute, ws := wall.Clock()
		if y == wy && m == wm && d == wd && h == wh && minute == wminute && s == ws {
			matches[candidate.Unix()] = candidate
		}
	}
	// FixedZone locations have one offset of arbitrary int size. time.Date's
	// candidate gives us that offset, including values outside the TZif range.
	y, m, d := wall.Date()
	h, minute, s := wall.Clock()
	seed := time.Date(y, m, d, h, minute, s, 0, location)
	_, offset := seed.Zone()
	consider(offset)
	// TZif represents offsets as signed 32-bit seconds. All possible UTC
	// candidates therefore lie in this closed interval. Traverse transition
	// bounds and test distinct offsets, including historical sub-hour changes.
	start := time.Unix(wall.Unix()-(1<<31-1), 0)
	end := time.Unix(wall.Unix()+(1<<31), 0)
	for cursor := start; !cursor.After(end); {
		local := cursor.In(location)
		_, offset := local.Zone()
		consider(offset)
		if len(matches) > 1 {
			return time.Time{}, "ambiguous_time"
		}
		_, next := local.ZoneBounds()
		if next == (time.Time{}) || next.After(end) {
			break
		}
		if !next.After(cursor) {
			// Probe past exact-boundary arithmetic before applying the
			// POSIX-footer workaround below.
			probe := cursor.Add(time.Second).In(location)
			_, probeOffset := probe.Zone()
			consider(probeOffset)
			if len(matches) > 1 {
				return time.Time{}, "ambiguous_time"
			}
			_, probeEnd := probe.ZoneBounds()
			if probeEnd == (time.Time{}) {
				break
			}
			if probeEnd.After(probe) {
				cursor = probe
				continue
			}
			// Go 1.27.1's POSIX-extension lookup ends the final yearly
			// interval at yearStart+365 days even in leap years. In that
			// branch both transitions have already occurred. Advance to
			// the next UTC year, never loop on the stale Dec 31 bound.
			next = time.Date(cursor.UTC().Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
		}
		cursor = next
	}
	for _, instant := range matches {
		return instant, ""
	}
	return time.Time{}, "nonexistent_time"
}
