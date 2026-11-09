package stats

import (
	"context"
	"testing"
	"time"
)

func TestSleepAsleepIsUnionOfStages(t *testing.T) {
	conn := newDB(t)
	// Core 23:00-02:00 and REM 01:00-03:00 overlap by an hour: asleep is 4 h, not 5 h.
	addSleep(t, conn, "core", "2024-12-01 23:00 +0100", "2024-12-02 02:00 +0100", watch)
	addSleep(t, conn, "rem", "2024-12-02 01:00 +0100", "2024-12-02 03:00 +0100", watch)
	// A 12-minute and a 3-minute awake period inside the night; only the first is an awakening.
	addSleep(t, conn, "awake", "2024-12-02 03:00 +0100", "2024-12-02 03:12 +0100", watch)
	addSleep(t, conn, "core", "2024-12-02 03:12 +0100", "2024-12-02 04:00 +0100", watch)
	addSleep(t, conn, "awake", "2024-12-02 04:00 +0100", "2024-12-02 04:03 +0100", watch)
	addSleep(t, conn, "core", "2024-12-02 04:03 +0100", "2024-12-02 05:00 +0100", watch)
	// iPhone in bed started 20 minutes before the first sleep.
	addSleep(t, conn, "in_bed", "2024-12-01 22:40 +0100", "2024-12-02 05:10 +0100", iphone)

	nights, err := SleepNights(context.Background(), conn, "2024-12-01", "2024-12-03")
	if err != nil || len(nights) != 1 {
		t.Fatalf("%v, %v", nights, err)
	}
	n := nights[0]
	// 23:00-03:00 (core+rem union) + 03:12-04:00 + 04:03-05:00 = 4h + 48m + 57m.
	if want := 5*time.Hour + 45*time.Minute; n.Asleep != want {
		t.Errorf("asleep %v, want %v", n.Asleep, want)
	}
	if n.Awakenings != 1 {
		t.Errorf("awakenings %d, want 1 (the 3-minute blip is ignored)", n.Awakenings)
	}
	if n.Latency != 20*time.Minute {
		t.Errorf("latency %v, want 20m", n.Latency)
	}
	if bed, _ := n.BedtimeMin(); ClockText(bed) != "23:00" {
		t.Errorf("bedtime %s", ClockText(bed))
	}
	if wake, _ := n.WakeMin(); ClockText(wake) != "05:00" {
		t.Errorf("wake %s", ClockText(wake))
	}
	if mid, _ := n.MidpointMin(); ClockText(mid) != "02:00" {
		t.Errorf("midpoint %s", ClockText(mid))
	}
	if len(n.Segments) != 6 || n.Segments[0].Stage != "core" {
		t.Errorf("segments %+v", n.Segments)
	}
}

func TestSleepEfficiencyAndLatencyLimits(t *testing.T) {
	n := SleepNight{Asleep: 8 * time.Hour, InBed: 7 * time.Hour}
	if p, clamped, ok := n.Efficiency(); !ok || !clamped || p != 100 {
		t.Errorf("asleep > in bed: %v %v %v", p, clamped, ok)
	}
	n = SleepNight{Asleep: 7 * time.Hour, Awake: time.Hour}
	if p, _, ok := n.Efficiency(); !ok || p != 87.5 {
		t.Errorf("fallback to awake: %v %v", p, ok)
	}
	if _, _, ok := (SleepNight{Asleep: time.Hour}).Efficiency(); ok {
		t.Error("no in bed and no awake: efficiency unknown")
	}
	if _, _, ok := (SleepNight{InBed: time.Hour}).Efficiency(); ok {
		t.Error("in bed only: no efficiency")
	}

	conn := newDB(t)
	// In bed began 3 hours before sleep: a guess too wild to show.
	addSleep(t, conn, "in_bed", "2024-12-01 20:00 +0100", "2024-12-02 06:00 +0100", iphone)
	addSleep(t, conn, "asleep_unspecified", "2024-12-01 23:00 +0100", "2024-12-02 06:00 +0100", iphone)
	nights, _ := SleepNights(context.Background(), conn, "2024-12-01", "2024-12-03")
	if len(nights) != 1 || nights[0].Latency != 0 {
		t.Errorf("latency over 90 min must be hidden: %+v", nights)
	}
}

func TestSummarizeRegularityDebtAndWindows(t *testing.T) {
	// 20 nights ending 2024-12-20, asleep 7 h, bedtime 23:00 +- 30 min alternating.
	var nights []SleepNight
	for i := 1; i <= 20; i++ {
		day := time.Date(2024, 12, i, 0, 0, 0, 0, time.UTC)
		start := day.Add(-time.Hour).Unix() // 23:00 the evening before
		if i%2 == 0 {
			start += 30 * 60
		}
		nights = append(nights, SleepNight{
			Night: day.Format(DayLayout), Asleep: 7 * time.Hour, Start: start, End: start + 7*3600,
		})
	}
	r := summarize("2024-12-20", nights, 28, 8*time.Hour)
	if r.Kind != "unspecified" || r.Week.Nights != 7 || r.Fortnight.Nights != 14 || r.Usual.Nights != 20 {
		t.Errorf("windows: %s %d %d %d", r.Kind, r.Week.Nights, r.Fortnight.Nights, r.Usual.Nights)
	}
	if r.Regularity == nil || r.Regularity.Nights != 14 || r.Regularity.BedtimeMin < 15 || r.Regularity.BedtimeMin > 17 {
		t.Errorf("regularity %+v", r.Regularity)
	}
	if r.Regularity.UsualBedtimeMin == 0 {
		t.Error("20 nights is enough for a usual spread")
	}
	if r.Debt == nil || r.Debt.LastSecs != 3600 || r.Debt.Nights != 7 || r.Debt.WindowSecs != 7*3600 {
		t.Errorf("debt %+v", r.Debt)
	}
	if r.Week.DeepSecs != nil {
		t.Error("no stages and no in bed: those averages must be absent")
	}

	// Fewer than 5 nights: no regularity. A surplus is a negative debt.
	short := []SleepNight{{Night: "2024-12-20", Asleep: 9 * time.Hour, Start: 100, End: 100 + 9*3600}}
	r = summarize("2024-12-20", short, 28, 8*time.Hour)
	if r.Regularity != nil || r.Debt.LastSecs != -3600 {
		t.Errorf("short: %+v %+v", r.Regularity, r.Debt)
	}
	if !summarize("2024-12-20", nil, 28, 8*time.Hour).Empty {
		t.Error("no nights: Empty")
	}
}

func TestBuildSleepReportValidation(t *testing.T) {
	conn := newDB(t)
	now := time.Date(2024, 12, 5, 12, 0, 0, 0, time.UTC)
	for _, o := range []SleepOptions{{Nights: 3}, {Nights: 200}, {Target: -time.Hour}, {Weeks: 99}, {By: "nope"}} {
		if _, err := BuildSleepReport(context.Background(), conn, now, o); err == nil {
			t.Errorf("%+v should fail", o)
		}
	}
	r, err := BuildSleepReport(context.Background(), conn, now, SleepOptions{Weeks: 12})
	if err != nil || !r.Empty || r.Heatmap != nil {
		t.Errorf("empty db: %+v %v", r, err)
	}
}

func TestClockMinHandlesMidnight(t *testing.T) {
	// 23:30 comes before 00:30 on the sleep-day clock.
	late := clockMin(time.Date(2024, 12, 2, 0, 30, 0, 0, time.UTC).Unix(), 0)
	early := clockMin(time.Date(2024, 12, 1, 23, 30, 0, 0, time.UTC).Unix(), 0)
	if late-early != 60 || ClockText(late) != "00:30" {
		t.Errorf("late %v early %v", late, early)
	}
}
