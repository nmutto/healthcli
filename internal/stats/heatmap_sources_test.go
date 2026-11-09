package stats

import (
	"context"
	"testing"
	"time"
)

func TestSleepAsleepByDayUnspecifiedOnly(t *testing.T) {
	conn := newDB(t)
	// One night written as a single unspecified row, one as two stage rows.
	addSleep(t, conn, "asleep_unspecified", "2026-11-02 23:00 +0000", "2026-11-03 07:00 +0000", iphone)
	addSleep(t, conn, "core", "2026-11-03 23:00 +0000", "2026-11-04 03:00 +0000", watch)
	addSleep(t, conn, "deep", "2026-11-04 03:00 +0000", "2026-11-04 05:00 +0000", watch)
	addSleep(t, conn, "awake", "2026-11-04 05:00 +0000", "2026-11-04 05:30 +0000", watch) // not sleep
	got, err := SleepAsleepByDay(context.Background(), conn, "2026-11-01", "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["2026-11-03"] != 480 || got["2026-11-04"] != 360 {
		t.Errorf("minutes by wake day = %v", got)
	}
}

func TestWorkoutMinutesByDaySumsSessions(t *testing.T) {
	conn := newDB(t)
	addWorkout(t, conn, "running", "2026-11-02 07:00 +0000", "2026-11-02 07:30 +0000", watch)
	addWorkout(t, conn, "cycling", "2026-11-02 18:00 +0000", "2026-11-02 19:00 +0000", watch)
	addWorkout(t, conn, "running", "2026-11-04 23:30 +0100", "2026-11-05 00:00 +0100", watch) // local day 4th
	got, err := WorkoutMinutesByDay(context.Background(), conn, "2026-11-01", "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["2026-11-02"] != 90 || got["2026-11-04"] != 30 {
		t.Errorf("minutes by day = %v", got)
	}
}

func TestHeatmapFromDaysRestDays(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) // Sunday; 2 weeks start 2026-10-26
	spec := HeatSpec{Title: "workouts", Unit: "min", ZeroIsRest: true}
	h, err := HeatmapFromDays(spec, now, 2, map[string]float64{
		"2026-10-28": 30, "2026-10-30": 60, "2026-11-02": 45, "2026-11-04": 90, "2026-11-07": 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.DaysWithData != 5 {
		t.Errorf("days with data = %d, want 5 (rest days do not count)", h.DaysWithData)
	}
	if c := h.Weeks[0][0]; c.Value != nil || c.Level != 0 { // before the first workout
		t.Errorf("before the first value stays a gap: %+v", c)
	}
	if c := h.Weeks[0][3]; c.Value == nil || *c.Value != 0 || c.Level != 0 { // Thursday between workouts
		t.Errorf("rest day = %+v, want value 0 and level 0", c)
	}
	if c := h.Weeks[1][2]; c.Level == 0 || c.Value == nil { // 90 min is the top fifth
		t.Errorf("workout day = %+v", c)
	}
	// Without ZeroIsRest the same input has no rest days at all.
	spec.ZeroIsRest = false
	h, _ = HeatmapFromDays(spec, now, 2, map[string]float64{"2026-10-28": 30, "2026-10-30": 60})
	if c := h.Weeks[0][3]; c.Value != nil {
		t.Errorf("a missing day is a gap without ZeroIsRest: %+v", c)
	}
}
