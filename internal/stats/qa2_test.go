package stats

import (
	"context"
	"testing"
	"time"
)

// A night that crosses a clock change: bedtime and wake use the offset of
// their own rows (it used to use the first row's offset for both).
func TestSleepClockUsesEachRowsOffset(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "core", "2024-03-30 23:30 +0100", "2024-03-31 01:30 +0100", watch)
	addSleep(t, conn, "core", "2024-03-31 03:00 +0200", "2024-03-31 06:40 +0200", watch)
	nights, err := SleepNights(context.Background(), conn, "2024-03-31", "2024-03-31")
	if err != nil || len(nights) != 1 {
		t.Fatalf("%v %+v", err, nights)
	}
	bed, _ := nights[0].BedtimeMin()
	wake, _ := nights[0].WakeMin()
	if got := ClockText(bed); got != "23:30" {
		t.Errorf("bedtime %s, want 23:30", got)
	}
	if got := ClockText(wake); got != "06:40" {
		t.Errorf("wake %s, want 06:40 (the second row's own offset)", got)
	}
}

// A nap more than 3 h from the main block counts in Asleep and Nap, but not
// in bedtime, wake, midpoint or the hypnogram rows.
func TestSleepNapsStayOutOfTheMainBlock(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "core", "2024-12-02 14:00 +0100", "2024-12-02 14:45 +0100", watch) // nap after waking
	addSleep(t, conn, "core", "2024-12-01 23:00 +0100", "2024-12-02 06:00 +0100", watch)
	// An iPhone nap the Watch did not record is not lost either.
	addSleep(t, conn, "asleep_unspecified", "2024-12-02 09:15 +0100", "2024-12-02 09:45 +0100", iphone)
	nights, err := SleepNights(context.Background(), conn, "2024-12-02", "2024-12-02")
	if err != nil || len(nights) != 1 {
		t.Fatalf("%v %+v", err, nights)
	}
	n := nights[0]
	if n.Nap != 75*time.Minute || n.Asleep != 7*time.Hour+75*time.Minute {
		t.Errorf("asleep %v nap %v, want 8h15m and 1h15m", n.Asleep, n.Nap)
	}
	bed, _ := n.BedtimeMin()
	mid, _ := n.MidpointMin()
	if ClockText(bed) != "23:00" || ClockText(mid) != "02:30" {
		t.Errorf("bedtime %s midpoint %s, want 23:00 and 02:30", ClockText(bed), ClockText(mid))
	}
	if len(n.Segments) != 1 || n.Segments[0].Start != n.Start {
		t.Errorf("the hypnogram has only the main block: %+v", n.Segments)
	}
	if pct, _, _ := n.Efficiency(); pct != 0 { // no in-bed, no awake rows: no efficiency
		t.Errorf("efficiency %v", pct)
	}
}

// The weekly delta needs 3 weeks of training since the first workout; weeks
// before it are not zero weeks.
func TestWeeklyDeltaIgnoresWeeksBeforeFirstWorkout(t *testing.T) {
	day := func(s string) Workout { d, _ := parseDay(s); return Workout{Day: s, Start: d, Duration: time.Hour} }
	// Mondays: 2026-10-05, 12, 19, 26 and the partial week of 2026-11-02.
	all := []Workout{day("2026-10-05"), day("2026-10-12"), day("2026-10-19"), day("2026-10-26")}
	weeks := weeklyVolume(all, "2026-11-04", 8)
	byStart := map[string]WeekVolume{}
	for _, w := range weeks {
		byStart[w.Start] = w
	}
	if byStart["2026-10-19"].DeltaPct != nil {
		t.Errorf("only 2 weeks since the first workout: no delta, got %v", *byStart["2026-10-19"].DeltaPct)
	}
	d := byStart["2026-10-26"].DeltaPct
	if d == nil || *d != 0 {
		t.Errorf("3 earlier weeks of 1 h each, so the delta is 0, got %v", d)
	}
}

// A session with no duration is ignored (it is noise, not a workout).
func TestZeroDurationWorkoutsAreIgnored(t *testing.T) {
	conn := newDB(t)
	addWorkout(t, conn, "running", "2024-12-02 07:00 +0100", "2024-12-02 07:00 +0100", watch)
	addWorkout(t, conn, "yoga", "2024-12-02 08:00 +0100", "2024-12-02 08:30 +0100", watch)
	got, err := Workouts(context.Background(), conn, "2024-12-01", "2024-12-03")
	if err != nil || len(got) != 1 || got[0].Activity != "yoga" {
		t.Errorf("%v %+v", err, got)
	}
}
