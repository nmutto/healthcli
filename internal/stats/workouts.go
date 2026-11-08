package stats

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Workout is one workout session.
type Workout struct {
	Day      string    // local day it started on
	Activity string    // e.g. "running", "cycling"
	Start    time.Time // on the local wall clock
	End      time.Time
	Duration time.Duration
	// EnergyKcal and DistanceM are nil when the workout did not record them
	// (a yoga session has no distance). nil is different from 0.
	EnergyKcal *float64
	DistanceM  *float64
	Source     string
}

// Workouts returns the workouts that started on fromDay..toDay (local days,
// both inclusive), oldest first.
//
// No source rule here: a workout is a session, not a running total, so every
// stored workout is returned. Each one carries its Source so the caller can
// tell them apart.
func Workouts(ctx context.Context, conn *sql.DB, fromDay, toDay string) ([]Workout, error) {
	lo, hi, err := utcWindow(fromDay, toDay)
	if err != nil {
		return nil, err
	}
	// start_ts uses the workouts_start_ts index (migration 0002).
	rows, err := conn.QueryContext(ctx, `
		SELECT activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source
		FROM workouts
		WHERE start_ts >= ? AND start_ts < ?
		  AND date(start_ts + tz_offset, 'unixepoch') BETWEEN ? AND ?
		ORDER BY start_ts, id`,
		lo, hi, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("stats: read workouts: %w", err)
	}
	defer rows.Close()

	var out []Workout
	for rows.Next() {
		var w Workout
		var start, end int64
		var seconds float64
		var energy, distance sql.NullFloat64 // the columns may be NULL
		var tz int
		if err := rows.Scan(&w.Activity, &start, &end, &seconds, &energy, &distance, &tz, &w.Source); err != nil {
			return nil, fmt.Errorf("stats: read workouts: %w", err)
		}
		w.Start = localTime(start, tz)
		w.End = localTime(end, tz)
		w.Day = w.Start.Format(DayLayout)
		w.Duration = time.Duration(seconds * float64(time.Second))
		if energy.Valid {
			w.EnergyKcal = &energy.Float64
		}
		if distance.Valid {
			w.DistanceM = &distance.Float64
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: read workouts: %w", err)
	}
	return out, nil
}
