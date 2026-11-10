package stats

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
)

const (
	watch  = "Alex’s Apple Watch"
	iphone = "Alex’s iPhone"
)

// newDB returns an empty, migrated in-memory database.
func newDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// at parses a local time like "2024-12-01 08:00 +0100" into UTC seconds and
// the offset, the way the importer stores them.
func at(t *testing.T, s string) (int64, int) {
	t.Helper()
	tm, err := time.Parse("2006-01-02 15:04 -0700", s)
	if err != nil {
		t.Fatal(err)
	}
	_, off := tm.Zone()
	return tm.Unix(), off
}

func addSample(t *testing.T, conn *sql.DB, metric, when string, value float64, source string) {
	t.Helper()
	ts, off := at(t, when)
	_, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES (?, ?, ?, ?, '', ?, ?)`, metric, ts, ts+60, value, off, source)
	if err != nil {
		t.Fatal(err)
	}
}

func addSleep(t *testing.T, conn *sql.DB, stage, from, to, source string) {
	t.Helper()
	start, off := at(t, from)
	end, _ := at(t, to)
	_, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES (?, ?, ?, ?, ?)`, stage, start, end, off, source)
	if err != nil {
		t.Fatal(err)
	}
}

func series(t *testing.T, conn *sql.DB, metric, from, to string) []DayValue {
	t.Helper()
	got, err := DailySeries(context.Background(), conn, metric, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSourceClass(t *testing.T) {
	cases := map[string]string{
		watch: ClassWatch, "APPLE WATCH": ClassWatch, iphone: ClassIPhone,
		"manual": ClassManual, "MyFitnessPal": ClassOther, "": ClassOther,
	}
	for in, want := range cases {
		if got := SourceClass(in); got != want {
			t.Errorf("SourceClass(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestWatchWinsOverIPhoneSameDay(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-01 09:00 +0100", 1000, watch)
	addSample(t, conn, "step_count", "2024-12-01 10:00 +0100", 500, watch)
	addSample(t, conn, "step_count", "2024-12-01 09:00 +0100", 900, iphone)
	addSample(t, conn, "step_count", "2024-12-01 12:00 +0100", 3000, iphone)

	got := series(t, conn, "steps", "2024-12-01", "2024-12-01")
	if len(got) != 1 {
		t.Fatalf("got %d days, want 1: %+v", len(got), got)
	}
	want := DayValue{Day: "2024-12-01", Value: 1500, Source: watch, Samples: 2}
	if got[0] != want {
		t.Errorf("got %+v, want %+v (only the Watch counts)", got[0], want)
	}
}

func TestIPhoneOnlyDayUsesIPhone(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-01 09:00 +0100", 1000, watch)
	// Watch left on the charger the next day: the iPhone is all there is.
	addSample(t, conn, "step_count", "2024-12-02 09:00 +0100", 700, iphone)
	addSample(t, conn, "step_count", "2024-12-02 18:00 +0100", 300, iphone)

	got := series(t, conn, "step_count", "2024-12-01", "2024-12-02")
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[1].Source != iphone || got[1].Value != 1000 || got[1].Samples != 2 {
		t.Errorf("iPhone-only day = %+v", got[1])
	}
}

func TestManualOnlyBodyMassTakesLatest(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "body_mass", "2024-12-03 07:00 +0100", 79.0, "manual")
	addSample(t, conn, "body_mass", "2024-12-03 21:00 +0100", 79.8, "manual")

	got := series(t, conn, "weight", "2024-12-01", "2024-12-05")
	want := []DayValue{{Day: "2024-12-03", Value: 79.8, Source: "manual", Samples: 2}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestManualLosesToIPhone(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "body_mass", "2024-12-03 07:00 +0100", 79.0, iphone)
	addSample(t, conn, "body_mass", "2024-12-03 21:00 +0100", 85.0, "manual")
	got := series(t, conn, "body_mass", "2024-12-03", "2024-12-03")
	if len(got) != 1 || got[0].Value != 79.0 || got[0].Source != iphone {
		t.Errorf("got %+v", got)
	}
}

func TestAverageMetric(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "heart_rate", "2024-12-01 09:00 +0100", 60, watch)
	addSample(t, conn, "heart_rate", "2024-12-01 10:00 +0100", 80, watch)
	addSample(t, conn, "heart_rate", "2024-12-01 10:00 +0100", 200, iphone)
	got := series(t, conn, "hr", "2024-12-01", "2024-12-01")
	if len(got) != 1 || got[0].Value != 70 {
		t.Errorf("got %+v, want avg 70 from the Watch", got)
	}
}

func TestTimezoneChangeMidRange(t *testing.T) {
	conn := newDB(t)
	// Lives in Rome (+0100), flies to New York (-0500) on the 2nd.
	addSample(t, conn, "step_count", "2024-12-01 23:30 +0100", 100, watch) // 22:30 UTC on the 1st
	addSample(t, conn, "step_count", "2024-12-02 08:00 +0100", 200, watch)
	// 23:00 in New York on the 2nd is 04:00 UTC on the 3rd: still the 2nd locally.
	addSample(t, conn, "step_count", "2024-12-02 23:00 -0500", 50, watch)
	// 00:30 New York on the 3rd.
	addSample(t, conn, "step_count", "2024-12-03 00:30 -0500", 7, watch)

	got := series(t, conn, "steps", "2024-12-01", "2024-12-03")
	want := []DayValue{
		{Day: "2024-12-01", Value: 100, Source: watch, Samples: 1},
		{Day: "2024-12-02", Value: 250, Source: watch, Samples: 2},
		{Day: "2024-12-03", Value: 7, Source: watch, Samples: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day %d: got %+v, want %+v", i, got[i], want[i])
		}
	}

	// The edge days of a range must still be found when the offset is large.
	edge := series(t, conn, "steps", "2024-12-03", "2024-12-03")
	if len(edge) != 1 || edge[0].Value != 7 {
		t.Errorf("single-day range at a far-west offset: %+v", edge)
	}
	first := series(t, conn, "steps", "2024-12-01", "2024-12-01")
	if len(first) != 1 || first[0].Value != 100 {
		t.Errorf("first day: %+v", first)
	}
}

func TestExtremeOffsets(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-05 00:10 +1400", 1, watch) // UTC 10:10 on the 4th
	addSample(t, conn, "step_count", "2024-12-05 23:50 -1200", 2, watch) // UTC 11:50 on the 6th
	got := series(t, conn, "steps", "2024-12-05", "2024-12-05")
	if len(got) != 1 || got[0].Value != 3 {
		t.Errorf("got %+v, want both samples on the 5th", got)
	}
}

func TestEmptyRangesAndBadInput(t *testing.T) {
	conn := newDB(t)
	if got := series(t, conn, "steps", "2024-12-01", "2024-12-31"); len(got) != 0 {
		t.Errorf("empty DB gave %+v", got)
	}
	ctx := context.Background()
	if nights, err := SleepNights(ctx, conn, "2024-12-01", "2024-12-31"); err != nil || len(nights) != 0 {
		t.Errorf("sleep on empty DB: %+v, %v", nights, err)
	}
	if ws, err := Workouts(ctx, conn, "2024-12-01", "2024-12-31"); err != nil || len(ws) != 0 {
		t.Errorf("workouts on empty DB: %+v, %v", ws, err)
	}
	if _, err := LatestSample(ctx, conn, "steps"); !errors.Is(err, ErrNoData) {
		t.Errorf("LatestSample on empty DB: %v, want ErrNoData", err)
	}
	if _, err := DailySeries(ctx, conn, "steps", "2024-12-02", "2024-12-01"); err == nil {
		t.Error("want error for a range that ends before it starts")
	}
	if _, err := DailySeries(ctx, conn, "steps", "12/01/2024", "2024-12-01"); err == nil {
		t.Error("want error for a bad day format")
	}
	if _, err := DailySeries(ctx, conn, "mood", "2024-12-01", "2024-12-01"); err == nil {
		t.Error("want error for an unknown metric")
	}
}

func TestLatestSample(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "body_mass", "2024-12-01 07:00 +0100", 79.0, watch)
	addSample(t, conn, "body_mass", "2024-12-04 07:00 -0500", 78.0, "manual")
	addSample(t, conn, "body_mass", "2024-12-04 07:00 -0500", 78.5, iphone) // same moment: iPhone beats manual

	s, err := LatestSample(context.Background(), conn, "weight")
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != 78.5 || s.Source != iphone || s.Day != "2024-12-04" || s.Start.Hour() != 7 || s.Metric != "body_mass" {
		t.Errorf("got %+v", s)
	}
}

// fillDays writes one Watch step sample per day for n days ending the day
// before 2024-12-29, with the values given (cycled).
func fillDays(t *testing.T, conn *sql.DB, n int, values ...float64) {
	t.Helper()
	day, _ := time.Parse(DayLayout, "2024-12-29")
	for i := 1; i <= n; i++ {
		d := day.AddDate(0, 0, -i).Format(DayLayout)
		addSample(t, conn, "step_count", d+" 12:00 +0100", values[i%len(values)], watch)
	}
}

func TestBaselineNeedsSevenDays(t *testing.T) {
	conn := newDB(t)
	ctx := context.Background()
	fillDays(t, conn, 6, 1000)
	b, err := BaselineFor(ctx, conn, "steps", "2024-12-29", 0)
	if !errors.Is(err, ErrNotEnoughData) || b.N != 6 {
		t.Fatalf("6 days: got %+v, %v; want ErrNotEnoughData with N=6", b, err)
	}

	addSample(t, conn, "step_count", "2024-12-22 12:00 +0100", 1000, watch) // the 7th day
	if _, err := BaselineFor(ctx, conn, "steps", "2024-12-29", 0); err != nil {
		t.Fatalf("7 days: %v", err)
	}
}

func TestBaselineExcludesTargetDayAndUsesWindow(t *testing.T) {
	conn := newDB(t)
	ctx := context.Background()
	fillDays(t, conn, 28, 1000, 3000) // alternating 1000/3000 -> mean 2000
	addSample(t, conn, "step_count", "2024-12-29 12:00 +0100", 99999, watch)
	addSample(t, conn, "step_count", "2024-11-30 12:00 +0100", 99999, watch) // 29 days back: outside

	b, err := BaselineFor(ctx, conn, "step_count", "2024-12-29", 28)
	if err != nil {
		t.Fatal(err)
	}
	if b.N != 28 || b.Mean != 2000 || b.From != "2024-12-01" || b.To != "2024-12-28" {
		t.Errorf("got %+v", b)
	}
	// 14 values 1000 off the mean, 14 values 1000 off: sample stddev = 1000*sqrt(28/27).
	if want := 1000 * math.Sqrt(28.0/27.0); math.Abs(b.Stddev-want) > 1e-9 {
		t.Errorf("stddev %v, want %v", b.Stddev, want)
	}
}

func TestCompare(t *testing.T) {
	b := Baseline{Mean: 2000, Stddev: 500}
	cases := []struct {
		value float64
		label string
		z     float64
		pct   float64
	}{
		{2500, Above, 1, 25}, // exactly at the threshold counts
		{2400, Typical, 0.8, 20},
		{1500, Below, -1, -25},
		{2000, Typical, 0, 0},
	}
	for _, c := range cases {
		got := Compare(c.value, b)
		if got.Label != c.label || math.Abs(got.Z-c.z) > 1e-9 || math.Abs(got.DeltaPct-c.pct) > 1e-9 || got.Delta != c.value-2000 {
			t.Errorf("Compare(%v) = %+v, want label %s z %v pct %v", c.value, got, c.label, c.z, c.pct)
		}
	}

	flat := Baseline{Mean: 79.4, Stddev: 0}
	if got := Compare(79.4, flat); got.Label != Typical || got.Z != 0 {
		t.Errorf("flat same: %+v", got)
	}
	if got := Compare(79.5, flat); got.Label != Above {
		t.Errorf("flat higher: %+v", got)
	}
	if got := Compare(5, Baseline{Mean: 0, Stddev: 0}); got.DeltaPct != 0 || got.Label != Above {
		t.Errorf("zero mean: %+v", got)
	}
}

func TestFlatBaselineHasZeroStddev(t *testing.T) {
	// 79.4 can't be stored exactly in binary; the mean of seven copies must
	// still give a spread of exactly 0 and compare as typical.
	conn := newDB(t)
	day, _ := time.Parse(DayLayout, "2024-12-29")
	for i := 1; i <= 7; i++ {
		addSample(t, conn, "body_mass", day.AddDate(0, 0, -i).Format(DayLayout)+" 07:00 +0100", 79.4, "manual")
	}
	b, err := BaselineFor(context.Background(), conn, "weight", "2024-12-29", 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.Stddev != 0 || Compare(79.4, b).Label != Typical {
		t.Errorf("got %+v, %+v", b, Compare(79.4, b))
	}
}

func TestSleepCrossingMidnight(t *testing.T) {
	conn := newDB(t)
	// Night ending Monday 2024-12-02. The iPhone writes in_bed, the Watch the stages.
	addSleep(t, conn, "in_bed", "2024-12-01 23:00 +0100", "2024-12-02 07:00 +0100", iphone)
	// The Watch's first row ends before midnight: still Monday's night.
	addSleep(t, conn, "core", "2024-12-01 23:10 +0100", "2024-12-01 23:50 +0100", watch)
	addSleep(t, conn, "core", "2024-12-01 23:50 +0100", "2024-12-02 01:10 +0100", watch)
	addSleep(t, conn, "deep", "2024-12-02 01:10 +0100", "2024-12-02 02:10 +0100", watch)
	addSleep(t, conn, "awake", "2024-12-02 02:10 +0100", "2024-12-02 02:30 +0100", watch)
	addSleep(t, conn, "rem", "2024-12-02 02:30 +0100", "2024-12-02 04:00 +0100", watch)
	addSleep(t, conn, "core", "2024-12-02 04:00 +0100", "2024-12-02 06:30 +0100", watch)
	// The iPhone also guessed some sleep: it must not be added to the Watch's.
	addSleep(t, conn, "asleep_unspecified", "2024-12-01 23:30 +0100", "2024-12-02 06:30 +0100", iphone)
	// An afternoon nap ending before 18:00 counts for the night that ended that morning.
	addSleep(t, conn, "asleep_unspecified", "2024-12-01 15:00 +0100", "2024-12-01 17:59 +0100", "Other App")
	// Ending at 18:00 already belongs to the coming night. The Watch wins the
	// stages, but this hour is more than 3 h from its sleep: a nap, kept in the total.
	addSleep(t, conn, "asleep_unspecified", "2024-12-01 17:00 +0100", "2024-12-01 18:00 +0100", "Other App")

	nights, err := SleepNights(context.Background(), conn, "2024-12-01", "2024-12-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(nights) != 2 {
		t.Fatalf("got %d nights: %+v", len(nights), nights)
	}
	if nights[0].Night != "2024-12-01" || nights[0].Source != "Other App" || nights[0].Asleep != 179*time.Minute {
		t.Errorf("night of the 1st: %+v", nights[0])
	}

	n := nights[1]
	want := SleepNight{
		Night: "2024-12-02", Source: watch, Rows: 6,
		Core: 4*time.Hour + 30*time.Minute, Deep: time.Hour, REM: 90 * time.Minute, Awake: 20 * time.Minute,
		Asleep: 8 * time.Hour, Nap: time.Hour, InBed: 8 * time.Hour, InBedSource: iphone,
		hadAwakeRows: true,
	}
	// The timing fields are checked in sleep_summary_test.go.
	want.Start, want.End, want.TZ, want.Segments = n.Start, n.End, n.TZ, n.Segments
	want.StartTZ, want.EndTZ, want.InBedStartTZ, want.InBedEndTZ = n.StartTZ, n.EndTZ, n.InBedStartTZ, n.InBedEndTZ
	want.InBedStart, want.InBedEnd, want.Awakenings, want.Latency = n.InBedStart, n.InBedEnd, n.Awakenings, n.Latency
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
}

func TestSleepTimezoneAndIPhoneOnly(t *testing.T) {
	conn := newDB(t)
	// Sleeping in New York: ends 06:00 local on the 4th = 11:00 UTC.
	addSleep(t, conn, "asleep_unspecified", "2024-12-03 23:00 -0500", "2024-12-04 06:00 -0500", iphone)
	nights, err := SleepNights(context.Background(), conn, "2024-12-04", "2024-12-04")
	if err != nil {
		t.Fatal(err)
	}
	if len(nights) != 1 || nights[0].Asleep != 7*time.Hour || nights[0].Source != iphone || nights[0].InBedSource != "" {
		t.Errorf("got %+v", nights)
	}
}

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
