// Package stats is the read side of the database: daily values, baselines,
// sleep nights, workouts and the latest sample. The CLI's "today" and "trend"
// commands are built on it.
//
// Three rules apply everywhere (see docs/design.md, "Read side"):
//
//   - Days are LOCAL days: the date of start_ts + tz_offset, i.e. the date on
//     the clock where the measurement was taken. Travel just works.
//   - Imports keep every source (iPhone and Watch both count steps). To avoid
//     counting twice, each metric on each day uses only ONE source: the best
//     one that has data that day (Apple Watch > iPhone > manual > other).
//   - Baselines are computed on demand from the raw rows, never stored.
//
// Days are passed around as "YYYY-MM-DD" strings because that is what
// people type and what the CLI prints.
package stats

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// DayLayout is the Go time layout of a day string, e.g. "2024-12-01".
const DayLayout = "2006-01-02"

// ErrNoData means there was nothing to read (e.g. no sample of that metric).
var ErrNoData = errors.New("stats: no data")

// Source classes, from best to worst. A higher number wins.
const (
	ClassOther  = "other"
	ClassManual = "manual"
	ClassIPhone = "iphone"
	ClassWatch  = "watch"
)

// SourceClass sorts a free-text source name (e.g. "Alex’s Apple Watch") into
// one of the classes above by looking for a word in it, ignoring case.
// "manual" is what `healthcli log` writes.
func SourceClass(source string) string {
	s := strings.ToLower(source)
	switch {
	case strings.Contains(s, "watch"):
		return ClassWatch
	case strings.Contains(s, "iphone"):
		return ClassIPhone
	case strings.Contains(s, "manual"):
		return ClassManual
	default:
		return ClassOther
	}
}

// SourcePriority turns a source name into a number: higher is preferred.
func SourcePriority(source string) int {
	switch SourceClass(source) {
	case ClassWatch:
		return 3
	case ClassIPhone:
		return 2
	case ClassManual:
		return 1
	default:
		return 0
	}
}

// better reports whether source a should be used instead of source b.
// Class decides first. Two sources of the same class (say, an old and a new
// iPhone) are never added together, because that could double-count too:
// the one with more rows that day wins, and the name breaks a final tie so
// the answer never depends on the order rows came back in.
func better(a string, aRows int, b string, bRows int) bool {
	pa, pb := SourcePriority(a), SourcePriority(b)
	if pa != pb {
		return pa > pb
	}
	if aRows != bRows {
		return aRows > bRows
	}
	return a < b
}

// parseDay checks a "YYYY-MM-DD" string and returns midnight UTC of it.
func parseDay(day string) (time.Time, error) {
	t, err := time.Parse(DayLayout, day)
	if err != nil {
		return time.Time{}, fmt.Errorf("stats: bad day %q (want YYYY-MM-DD)", day)
	}
	return t, nil
}

// AddDays returns day moved by n days (n may be negative).
func AddDays(day string, n int) (string, error) {
	t, err := parseDay(day)
	if err != nil {
		return "", err
	}
	t = t.AddDate(0, 0, n)
	if y := t.Year(); y < 1 || y > 9999 {
		return "", fmt.Errorf("stats: %d days from %s is out of range", n, day)
	}
	return t.Format(DayLayout), nil
}

// Real-world UTC offsets run from -12:00 to +14:00. A local day can thus
// start up to 14 hours before or 12 hours after the same date in UTC.
const (
	maxOffsetEast = 14 * 60 * 60
	maxOffsetWest = 12 * 60 * 60
)

// utcWindow turns an inclusive range of local days into a UTC timestamp
// window [lo, hi) that surely contains every row of those days, whatever
// timezone a row was recorded in. Queries filter on this window first so
// SQLite can use an index, then keep only rows whose local day is in range.
func utcWindow(fromDay, toDay string) (lo, hi int64, err error) {
	from, err := parseDay(fromDay)
	if err != nil {
		return 0, 0, err
	}
	to, err := parseDay(toDay)
	if err != nil {
		return 0, 0, err
	}
	if to.Before(from) {
		return 0, 0, fmt.Errorf("stats: range %s..%s ends before it starts", fromDay, toDay)
	}
	lo = from.Unix() - maxOffsetEast
	hi = to.AddDate(0, 0, 1).Unix() + maxOffsetWest
	return lo, hi, nil
}

// localTime shows a UTC timestamp on the wall clock it was recorded on.
func localTime(ts int64, tzOffset int) time.Time {
	return time.Unix(ts, 0).In(time.FixedZone("", tzOffset))
}
