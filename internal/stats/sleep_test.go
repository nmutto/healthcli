package stats

import (
	"context"
	"reflect"
	"testing"
	"time"
)

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
		Night: "2024-12-02", Source: watch, SourceClass: ClassWatch, Rows: 6,
		Core: 4*time.Hour + 30*time.Minute, Deep: time.Hour, REM: 90 * time.Minute, Awake: 20 * time.Minute,
		Asleep: 8 * time.Hour, Nap: time.Hour, InBed: 8 * time.Hour, InBedSource: iphone, InBedClass: ClassIPhone,
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
	if n.Rows != 4 {
		t.Errorf("segments %d, want 4 rows read", n.Rows)
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
