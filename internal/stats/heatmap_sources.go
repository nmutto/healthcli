package stats

import (
	"context"
	"database/sql"
)

// These producers feed HeatmapFromDays: each returns one number per local
// day, and only days that have data appear in the map.

// SleepAsleepByDay returns minutes asleep per night, keyed by the day the
// night ends on (the same rule as SleepNights). It works for devices that
// only write "asleep_unspecified". Nights with no sleep time are left out.
func SleepAsleepByDay(ctx context.Context, conn *sql.DB, from, to string) (map[string]float64, error) {
	nights, err := SleepNights(ctx, conn, from, to)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, n := range nights {
		if n.Asleep > 0 {
			out[n.Night] = n.Asleep.Minutes()
		}
	}
	return out, nil
}

// WorkoutMinutesByDay returns the minutes of workouts per local day (the day
// each session started on). Rest days are not in the map.
func WorkoutMinutesByDay(ctx context.Context, conn *sql.DB, from, to string) (map[string]float64, error) {
	workouts, err := Workouts(ctx, conn, from, to)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, w := range workouts {
		out[w.Day] += w.Duration.Minutes()
	}
	return out, nil
}
