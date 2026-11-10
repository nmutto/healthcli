package stats

import (
	"context"
	"testing"
	"time"
)

func TestWorkouts(t *testing.T) {
	conn := newDB(t)
	start, off := at(t, "2024-12-02 23:30 -0500") // UTC: the 3rd
	_, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
		VALUES ('yoga', ?, ?, 1800, NULL, NULL, ?, ?), ('running', ?, ?, 3600, 600, 10000, ?, ?)`,
		start, start+1800, off, watch, start+86400, start+90000, off, watch)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := Workouts(context.Background(), conn, "2024-12-02", "2024-12-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("got %+v", ws)
	}
	w := ws[0]
	if w.Activity != "yoga" || w.Day != "2024-12-02" || w.Duration != 30*time.Minute || w.EnergyKcal != nil || w.DistanceM != nil || w.Start.Hour() != 23 {
		t.Errorf("got %+v", w)
	}
	all, _ := Workouts(context.Background(), conn, "2024-12-01", "2024-12-31")
	if len(all) != 2 || all[1].EnergyKcal == nil || *all[1].EnergyKcal != 600 || *all[1].DistanceM != 10000 {
		t.Errorf("got %+v", all)
	}
}

func TestWorkoutRecordedByTwoSourcesAppearsOnce(t *testing.T) {
	conn := newDB(t)
	addWorkout(t, conn, "running", "2024-12-02 07:00 +0100", "2024-12-02 07:30 +0100", watch)
	addWorkout(t, conn, "running", "2024-12-02 06:58 +0100", "2024-12-02 07:33 +0100", iphone)
	// different activity at the same time is a different session
	addWorkout(t, conn, "yoga", "2024-12-02 07:05 +0100", "2024-12-02 07:25 +0100", iphone)
	// same activity, same source, back to back: two sessions
	addWorkout(t, conn, "cycling", "2024-12-02 17:00 +0100", "2024-12-02 17:30 +0100", iphone)
	addWorkout(t, conn, "cycling", "2024-12-02 17:30 +0100", "2024-12-02 18:00 +0100", iphone)
	// same activity, same time of day, but later and not overlapping
	addWorkout(t, conn, "running", "2024-12-02 19:00 +0100", "2024-12-02 19:30 +0100", iphone)

	ws, err := Workouts(context.Background(), conn, "2024-12-02", "2024-12-02")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range ws {
		got = append(got, w.Activity+"@"+w.Start.Format("15:04")+"/"+SourceClass(w.Source))
	}
	want := []string{"running@07:00/watch", "yoga@07:05/iphone", "cycling@17:00/iphone", "cycling@17:30/iphone", "running@19:00/iphone"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v want %v", got, want)
			break
		}
	}
}

func TestWorkoutDuplicateAcrossRangeEdge(t *testing.T) {
	conn := newDB(t)
	// Watch starts 00:01 on the 3rd, iPhone 23:58 on the 2nd: one session,
	// kept from the Watch, so asking for the 2nd alone must not show the iPhone copy.
	addWorkout(t, conn, "walking", "2024-12-03 00:01 +0100", "2024-12-03 00:40 +0100", watch)
	addWorkout(t, conn, "walking", "2024-12-02 23:58 +0100", "2024-12-03 00:42 +0100", iphone)
	ws, _ := Workouts(context.Background(), conn, "2024-12-02", "2024-12-02")
	if len(ws) != 0 {
		t.Errorf("2nd: %+v", ws)
	}
	ws, _ = Workouts(context.Background(), conn, "2024-12-03", "2024-12-03")
	if len(ws) != 1 || ws[0].Source != watch {
		t.Errorf("3rd: %+v", ws)
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
