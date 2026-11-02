package simulationdetectionschedules

import "time"

// CalendarAt derives every occurrence from the original UTC anchor, not from
// the preceding clamped month. This prevents January 31 -> February -> March drift.
func CalendarAt(anchor time.Time, cycle int, index int64) (time.Time, error) {
	a := anchor.UTC()
	if cycle < 0 || cycle > 2 || index < 0 || index > 4000000 || a.Year() < 1 || a.Year() > 9999 || a.Nanosecond() != 0 {
		return time.Time{}, ErrInvalid
	}
	var out time.Time
	if cycle == 2 {
		month := int64(a.Year()-1)*12 + int64(a.Month()-1) + index
		if month >= 9999*12 {
			return time.Time{}, ErrLimit
		}
		y, m := int(month/12)+1, time.Month(month%12+1)
		day := a.Day()
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if day > last {
			day = last
		}
		out = time.Date(y, m, day, a.Hour(), a.Minute(), a.Second(), 0, time.UTC)
	} else {
		days := index
		if cycle == 1 {
			days *= 7
		}
		if days > 3652058 {
			return time.Time{}, ErrLimit
		}
		out = a.AddDate(0, 0, int(days))
	}
	if out.Year() > 9999 || out.Year() < 1 {
		return time.Time{}, ErrLimit
	}
	return out, nil
}

// latestIndex is constant-time for centuries of downtime, with at most one
// adjustment for month-day clamping. It returns -1 before the anchor.
func latestIndex(anchor time.Time, cycle int, now time.Time) (int64, error) {
	a := anchor.UTC()
	n := now.UTC()
	if n.Before(a) {
		return -1, nil
	}
	var i int64
	switch cycle {
	case 0:
		i = (n.Unix() - a.Unix()) / 86400
	case 1:
		i = (n.Unix() - a.Unix()) / (7 * 86400)
	case 2:
		i = int64((n.Year()-a.Year())*12 + int(n.Month()-a.Month()))
	default:
		return 0, ErrInvalid
	}
	at, e := CalendarAt(a, cycle, i)
	if e != nil {
		return 0, e
	}
	if at.After(n) {
		i--
	}
	return i, nil
}
