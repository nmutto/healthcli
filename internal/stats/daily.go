package stats

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/nmutto/healthcli/internal/metrics"
)

// DayValue is one metric's value for one local day.
type DayValue struct {
	Day     string  // local day, "YYYY-MM-DD"
	Value   float64 // sum, average or latest sample, per the metric's Daily kind
	Source  string  // the one source the value came from
	Samples int     // how many rows of that source were used
}

// DailySeries returns one value per local day from fromDay to toDay
// (both inclusive, "YYYY-MM-DD") for metric (a name or alias like "steps").
//
// Days without any data are left out, so the result may be shorter than the
// range, or empty. It is sorted by day.
func DailySeries(ctx context.Context, conn *sql.DB, metric, fromDay, toDay string) ([]DayValue, error) {
	m, err := metrics.Lookup(metric)
	if err != nil {
		return nil, err
	}
	lo, hi, err := utcWindow(fromDay, toDay)
	if err != nil {
		return nil, err
	}

	// The local day is computed in SQL with date(unix seconds + offset).
	// The start_ts window lets SQLite use the (metric, start_ts, ...) index;
	// the BETWEEN then trims rows that only fell into the window because of
	// its timezone margin. A sample belongs to the day it STARTS on.
	rows, err := conn.QueryContext(ctx, `
		SELECT date(start_ts + tz_offset, 'unixepoch') AS day, source, value
		FROM samples
		WHERE metric = ?
		  AND start_ts >= ? AND start_ts < ?
		  AND date(start_ts + tz_offset, 'unixepoch') BETWEEN ? AND ?
		ORDER BY start_ts, id`,
		m.Name, lo, hi, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("stats: read %s: %w", m.Name, err)
	}
	defer rows.Close()

	// Collect a running total per (day, source). Rows arrive oldest first,
	// so the last value we see in a bucket is that day's latest sample.
	type bucket struct {
		n      int
		sum    float64
		latest float64
	}
	byDay := map[string]map[string]*bucket{}
	var days []string // every day seen; sorted below
	for rows.Next() {
		var day, source string
		var value float64
		if err := rows.Scan(&day, &source, &value); err != nil {
			return nil, fmt.Errorf("stats: read %s: %w", m.Name, err)
		}
		if byDay[day] == nil {
			byDay[day] = map[string]*bucket{}
			days = append(days, day)
		}
		b := byDay[day][source]
		if b == nil {
			b = &bucket{}
			byDay[day][source] = b
		}
		b.n++
		b.sum += value
		b.latest = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: read %s: %w", m.Name, err)
	}

	// Rows are ordered by UTC start, so after a timezone change two local
	// days could in theory interleave; sorting the day strings fixes that.
	sort.Strings(days)

	out := make([]DayValue, 0, len(days))
	for _, day := range days {
		// Pick the single best source of the day; ignore all others.
		best := ""
		var bb *bucket
		for source, b := range byDay[day] {
			if bb == nil || better(source, b.n, best, bb.n) {
				best, bb = source, b
			}
		}

		var value float64
		switch m.Daily {
		case metrics.Sum:
			value = bb.sum
		case metrics.Avg:
			value = bb.sum / float64(bb.n)
		case metrics.Latest:
			value = bb.latest
		default:
			return nil, fmt.Errorf("stats: metric %s has unknown daily aggregation %q", m.Name, m.Daily)
		}
		out = append(out, DayValue{Day: day, Value: value, Source: best, Samples: bb.n})
	}
	return out, nil
}

// Sample is one stored measurement.
type Sample struct {
	Metric string
	Value  float64
	Unit   string
	Start  time.Time // on the local wall clock it was recorded on
	End    time.Time
	Day    string // local day of Start
	Source string
}

// LatestSample returns the most recent sample of metric (name or alias),
// by start time, or ErrNoData if there is none. If several sources recorded
// a sample at that very same moment, the preferred source wins.
//
// No per-day source rule here: "latest" means the newest reading, from
// whichever device took it.
func LatestSample(ctx context.Context, conn *sql.DB, metric string) (Sample, error) {
	m, err := metrics.Lookup(metric)
	if err != nil {
		return Sample{}, err
	}
	// Fetch every row at the newest start_ts (usually just one) and let Go
	// choose between sources, so the priority rule lives in one place.
	rows, err := conn.QueryContext(ctx, `
		SELECT start_ts, end_ts, value, unit, tz_offset, source
		FROM samples
		WHERE metric = ?
		  AND start_ts = (SELECT MAX(start_ts) FROM samples WHERE metric = ?)`,
		m.Name, m.Name)
	if err != nil {
		return Sample{}, fmt.Errorf("stats: latest %s: %w", m.Name, err)
	}
	defer rows.Close()

	var best Sample
	found := false
	for rows.Next() {
		var start, end int64
		var tz int
		var s Sample
		if err := rows.Scan(&start, &end, &s.Value, &s.Unit, &tz, &s.Source); err != nil {
			return Sample{}, fmt.Errorf("stats: latest %s: %w", m.Name, err)
		}
		s.Metric = m.Name
		s.Start = localTime(start, tz)
		s.End = localTime(end, tz)
		s.Day = s.Start.Format(DayLayout)
		if !found || better(s.Source, 1, best.Source, 1) {
			best, found = s, true
		}
	}
	if err := rows.Err(); err != nil {
		return Sample{}, fmt.Errorf("stats: latest %s: %w", m.Name, err)
	}
	if !found {
		return Sample{}, fmt.Errorf("%w for %s", ErrNoData, m.Name)
	}
	return best, nil
}
