package stats

import (
	"context"
	"errors"
	"testing"
)

func TestSourceClass(t *testing.T) {
	cases := map[string]string{
		watch: ClassWatch, "APPLE WATCH": ClassWatch, iphone: ClassIPhone,
		"manual": ClassManual, "MyFitnessPal": ClassOther, "": ClassOther,
	}
	for in, want := range cases {
		if got := SourceClass(in, ""); got != want {
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
