package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
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

// MarshalJSON writes a workout with snake_case keys and the duration in
// seconds (a time.Duration would print as nanoseconds). Energy and distance
// are left out when the workout did not record them.
func (w Workout) MarshalJSON() ([]byte, error) {
	v := struct {
		Day             string   `json:"day"`
		Activity        string   `json:"activity"`
		Start           string   `json:"start"`
		End             string   `json:"end"`
		DurationSeconds float64  `json:"duration_seconds"`
		EnergyKcal      *float64 `json:"energy_kcal,omitempty"`
		DistanceKM      *float64 `json:"distance_km,omitempty"`
		Source          string   `json:"source,omitempty"`
	}{w.Day, w.Activity, w.Start.Format(time.RFC3339), w.End.Format(time.RFC3339),
		w.Duration.Seconds(), w.EnergyKcal, kmPtr(w.DistanceM), w.Source}
	return json.Marshal(v)
}

// Workouts returns the workouts that started on fromDay..toDay (local days,
// both inclusive), oldest first.
//
// A workout is a session, not a running total, so sessions are not summed.
// But the Watch and the iPhone often both record the same session: when two
// workouts of the same activity from DIFFERENT sources overlap in time, only
// the preferred source's one is kept (Watch > iPhone > manual > other, see
// better). Same-source workouts are never merged, so back-to-back sessions
// stay separate. Each result carries its Source.
func Workouts(ctx context.Context, conn *sql.DB, fromDay, toDay string) ([]Workout, error) {
	// Read one extra day on each side so a duplicate that started just
	// across midnight still takes part in the duplicate check.
	// (At the ends of the calendar AddDays fails; then no padding is needed.)
	from, to := fromDay, toDay
	if d, err := AddDays(fromDay, -1); err == nil && len(d) == 10 && d[0] != '-' {
		from = d
	}
	if d, err := AddDays(toDay, 1); err == nil && len(d) == 10 {
		to = d
	}
	all, err := readWorkouts(ctx, conn, from, to)
	if err != nil {
		return nil, err
	}
	var out []Workout
	for _, w := range dropDuplicateWorkouts(all) {
		if w.Day >= fromDay && w.Day <= toDay {
			out = append(out, w)
		}
	}
	return out, nil
}

// dropDuplicateWorkouts walks the workouts best source first and skips any
// that overlaps one already kept for the same activity from another source.
// The input is sorted by start and so is the result.
func dropDuplicateWorkouts(all []Workout) []Workout {
	order := make([]int, len(all))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := all[order[a]], all[order[b]]
		if x.Source == y.Source {
			return false
		}
		return better(x.Source, 1, y.Source, 1)
	})
	keep := make([]bool, len(all))
	var kept []int
	for _, i := range order {
		dup := false
		for _, k := range kept {
			a, b := all[i], all[k]
			if a.Activity == b.Activity && a.Source != b.Source && a.Start.Before(b.End) && b.Start.Before(a.End) {
				dup = true
				break
			}
		}
		if !dup {
			keep[i] = true
			kept = append(kept, i)
		}
	}
	var out []Workout
	for i, w := range all {
		if keep[i] {
			out = append(out, w)
		}
	}
	return out
}

func readWorkouts(ctx context.Context, conn *sql.DB, fromDay, toDay string) ([]Workout, error) {
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
		if seconds <= 0 { // a session with no duration is noise: ignored everywhere
			continue
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
