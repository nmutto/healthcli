package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nmutto/healthcli/internal/metrics"
)

// LatestDataDay finds the newest local day with any data: the newest
// sample of any metric, workout or night of sleep. Like the overview, it
// ignores anything starting more than a day (futureSlack) after now. found
// is false for an empty database. No candidate is later than now's local
// date: an evening nap is dated tomorrow by the 18:00 sleep shift, and
// tomorrow is not a day with data yet.
func LatestDataDay(ctx context.Context, conn *sql.DB, now time.Time) (day string, found bool, err error) {
	maxStart := now.Add(futureSlack).Unix()
	today := now.Format(DayLayout)
	consider := func(d string) {
		// "YYYY-MM-DD" strings sort in date order, so > compares dates.
		if d > day && d <= today {
			day, found = d, true
		}
	}
	for _, name := range metrics.Names() {
		s, err := LatestSampleUpTo(ctx, conn, name, maxStart)
		if errors.Is(err, ErrNoData) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		consider(s.Day)
	}

	// Workouts and sleep have no "latest" helper, so ask directly.
	// Local day = UTC time + tz_offset; a night belongs to the date it ends on,
	// with the nightShift (see SleepNight). Days after today are left out in
	// the query, so an older night can still win.
	var d sql.NullString // NULL when the table is empty
	err = conn.QueryRowContext(ctx,
		`SELECT max(date(start_ts + tz_offset, 'unixepoch')) FROM workouts WHERE start_ts <= ? AND date(start_ts + tz_offset, 'unixepoch') <= ?`,
		maxStart, today).Scan(&d)
	if err != nil {
		return "", false, fmt.Errorf("find latest day: %w", err)
	}
	if d.Valid {
		consider(d.String)
	}

	err = conn.QueryRowContext(ctx,
		`SELECT max(date(end_ts + tz_offset + ?, 'unixepoch')) FROM sleep_stages WHERE start_ts <= ? AND date(end_ts + tz_offset + ?, 'unixepoch') <= ?`,
		nightShift, maxStart, nightShift, today).Scan(&d)
	if err != nil {
		return "", false, fmt.Errorf("find latest day: %w", err)
	}
	if d.Valid {
		consider(d.String)
	}
	return day, found, nil
}

// LatestDayFor finds the newest day that has data for metric ("sleep" is
// the pseudo-metric for nights of sleep), to point the user at a window that
// is not empty. found is false when there is no data at all. It does not
// skip future-dated rows.
func LatestDayFor(ctx context.Context, conn *sql.DB, metric string) (day string, found bool, err error) {
	if metric == "sleep" {
		var d sql.NullString
		err := conn.QueryRowContext(ctx,
			`SELECT max(date(end_ts + tz_offset + ?, 'unixepoch')) FROM sleep_stages WHERE stage != 'in_bed'`,
			nightShift).Scan(&d)
		return d.String, d.Valid, err
	}
	s, err := LatestSample(ctx, conn, metric)
	if errors.Is(err, ErrNoData) {
		return "", false, nil
	}
	return s.Day, err == nil, err
}
