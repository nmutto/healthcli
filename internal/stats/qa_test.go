package stats

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"
)

// Several stage rows of one source covering the same interval (a revised
// estimate written next to the old one) must not be counted twice.
func TestSleepOverlappingRowsAreMerged(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "core", "2024-12-01 23:00 +0100", "2024-12-02 02:00 +0100", watch)
	addSleep(t, conn, "core", "2024-12-02 01:00 +0100", "2024-12-02 03:00 +0100", watch) // overlaps 1h
	addSleep(t, conn, "core", "2024-12-02 01:30 +0100", "2024-12-02 02:30 +0100", watch) // inside both
	addSleep(t, conn, "deep", "2024-12-02 03:00 +0100", "2024-12-02 04:00 +0100", watch)
	addSleep(t, conn, "in_bed", "2024-12-01 22:50 +0100", "2024-12-02 05:00 +0100", iphone)
	addSleep(t, conn, "in_bed", "2024-12-02 04:00 +0100", "2024-12-02 07:00 +0100", iphone)

	nights, err := SleepNights(context.Background(), conn, "2024-12-02", "2024-12-02")
	if err != nil || len(nights) != 1 {
		t.Fatalf("%v %+v", err, nights)
	}
	n := nights[0]
	if n.Core != 4*time.Hour || n.Deep != time.Hour || n.Asleep != 5*time.Hour {
		t.Errorf("core %v deep %v asleep %v, want 4h 1h 5h", n.Core, n.Deep, n.Asleep)
	}
	if n.InBed != 8*time.Hour+10*time.Minute {
		t.Errorf("in bed %v, want 8h10m (22:50..07:00)", n.InBed)
	}
	if n.Segments != 4 {
		t.Errorf("segments %d, want 4 rows read", n.Segments)
	}
}

func TestSleepZeroLengthRowsAddNothing(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "core", "2024-12-02 01:00 +0100", "2024-12-02 03:00 +0100", watch)
	// end before start must never subtract time
	if _, err := conn.Exec(`INSERT INTO sleep_stages (stage,start_ts,end_ts,tz_offset,source)
		VALUES ('core', 1733100000, 1733090000, 3600, ?)`, watch); err != nil {
		t.Fatal(err)
	}
	nights, _ := SleepNights(context.Background(), conn, "2024-11-01", "2024-12-31")
	for _, n := range nights {
		if n.Core < 0 || n.Asleep < 0 {
			t.Errorf("negative sleep %+v", n)
		}
	}
}

func addWorkout(t *testing.T, conn *sql.DB, activity, from, to, source string) {
	t.Helper()
	start, off := at(t, from)
	end, _ := at(t, to)
	_, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
		VALUES (?, ?, ?, ?, NULL, NULL, ?, ?)`, activity, start, end, end-start, off, source)
	if err != nil {
		t.Fatal(err)
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

// Odd real-world offsets and DST: the local day is the clock date.
func TestOddOffsetsBucketByClock(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-02 00:10 +0545", 1, watch)  // Nepal; UTC is still the 1st
	addSample(t, conn, "step_count", "2024-12-02 23:50 -0930", 10, watch) // Marquesas; UTC is the 3rd
	addSample(t, conn, "step_count", "2024-12-03 00:05 +1400", 100, watch)
	addSample(t, conn, "step_count", "2024-12-01 23:55 -1200", 1000, watch)
	got := series(t, conn, "steps", "2024-12-01", "2024-12-03")
	want := map[string]float64{"2024-12-01": 1000, "2024-12-02": 11, "2024-12-03": 100}
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	for _, d := range got {
		if want[d.Day] != d.Value {
			t.Errorf("%s = %v want %v", d.Day, d.Value, want[d.Day])
		}
	}
}

func TestDSTDayHasNoSpecialCase(t *testing.T) {
	conn := newDB(t)
	// US spring forward 2024-03-10: 01:30 EST (-0500) and 03:30 EDT (-0400) are 1h apart.
	addSample(t, conn, "step_count", "2024-03-10 01:30 -0500", 1, watch)
	addSample(t, conn, "step_count", "2024-03-10 03:30 -0400", 2, watch)
	addSample(t, conn, "step_count", "2024-03-10 23:59 -0400", 4, watch)
	addSample(t, conn, "step_count", "2024-03-11 00:00 -0400", 8, watch)
	got := series(t, conn, "steps", "2024-03-10", "2024-03-11")
	if len(got) != 2 || got[0].Value != 7 || got[1].Value != 8 {
		t.Errorf("%+v", got)
	}
}

func TestCompareAlwaysFinite(t *testing.T) {
	cases := []struct {
		v float64
		b Baseline
	}{
		{1, Baseline{Mean: 1e-320, Stddev: 0}},
		{1e300, Baseline{Mean: 1e-300, Stddev: 1e-300}},
		{-1e300, Baseline{Mean: 1e-5, Stddev: 1e-310}},
		{0, Baseline{}},
		{5, Baseline{Mean: 5}},
		{math.MaxFloat64, Baseline{Mean: -math.MaxFloat64, Stddev: 1}},
	}
	for _, c := range cases {
		got := Compare(c.v, c.b)
		for name, x := range map[string]float64{"delta": got.Delta, "pct": got.DeltaPct, "z": got.Z} {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Errorf("Compare(%v, %+v).%s = %v", c.v, c.b, name, x)
			}
		}
		if got.Label != Above && got.Label != Below && got.Label != Typical {
			t.Errorf("label %q", got.Label)
		}
	}
}

func FuzzCompare(f *testing.F) {
	f.Add(1.0, 2.0, 0.5)
	f.Add(1e300, 1e-300, 0.0)
	f.Fuzz(func(t *testing.T, v, mean, sd float64) {
		for _, x := range []float64{v, mean, sd} {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Skip()
			}
		}
		sd = math.Abs(sd)
		got := Compare(v, Baseline{Mean: mean, Stddev: sd})
		for _, x := range []float64{got.Delta, got.DeltaPct, got.Z} {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Fatalf("Compare(%v, mean %v, sd %v) = %+v", v, mean, sd, got)
			}
		}
	})
}

func TestMeanStddevNeverNaN(t *testing.T) {
	for _, vals := range [][]float64{
		{5, 5, 5, 5, 5, 5, 5}, {0, 0, 0, 0, 0, 0, 0}, {1e-300, 2e-300, 1e-300, 2e-300, 1e-300, 2e-300, 1e-300},
		{1e150, -1e150, 1e150, -1e150, 1e150, -1e150, 1e150}, {79.4, 79.4, 79.4, 79.4, 79.4, 79.4, 79.4},
	} {
		m, sd := meanStddev(vals)
		if math.IsNaN(m) || math.IsInf(m, 0) || math.IsNaN(sd) || math.IsInf(sd, 0) || sd < 0 {
			t.Errorf("%v -> %v %v", vals, m, sd)
		}
	}
}

func TestExtremeDatesDoNotBreakQueries(t *testing.T) {
	conn := newDB(t)
	for _, d := range []string{"0001-01-01", "0000-01-01", "9999-12-31", "1969-12-31", "2024-02-29"} {
		if _, err := DailySeries(context.Background(), conn, "steps", d, d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
		if _, err := SleepNights(context.Background(), conn, d, d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
		if _, err := Workouts(context.Background(), conn, d, d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
		if _, err := BaselineFor(context.Background(), conn, "steps", d, 0); err != nil && err.Error() == "" {
			t.Errorf("%s: %v", d, err)
		}
	}
}
