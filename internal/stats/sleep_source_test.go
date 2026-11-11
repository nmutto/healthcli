package stats

import (
	"context"
	"testing"
	"time"
)

// A source with only an awake row (or a short nap) must not beat the source
// that has the whole night, and the night must not turn into a nap.
func TestSleepSourceNeedsAsleepTime(t *testing.T) {
	cases := map[string]struct{ stage, from, to string }{
		"watch awake row only": {"awake", "2024-12-02 03:00 +0100", "2024-12-02 03:05 +0100"},
		"watch short nap":      {"core", "2024-12-02 13:00 +0100", "2024-12-02 13:40 +0100"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			conn := newDB(t)
			addSleep(t, conn, "asleep_unspecified", "2024-12-01 23:00 +0100", "2024-12-02 07:00 +0100", iphone)
			addSleep(t, conn, c.stage, c.from, c.to, watch)
			nights, err := SleepNights(context.Background(), conn, "2024-12-02", "2024-12-02")
			if err != nil || len(nights) != 1 {
				t.Fatalf("%v %+v", err, nights)
			}
			n := nights[0]
			if n.Source != iphone {
				t.Errorf("source %q, want the iPhone night", n.Source)
			}
			bed, _ := n.BedtimeMin()
			wake, _ := n.WakeMin()
			if ClockText(bed) != "23:00" || ClockText(wake) != "07:00" {
				t.Errorf("bedtime %s wake %s, want 23:00 and 07:00", ClockText(bed), ClockText(wake))
			}
			// The Watch nap is a separate block, so it adds to Asleep and Nap;
			// an awake row adds nothing.
			wantNap := time.Duration(0)
			if c.stage == "core" {
				wantNap = 40 * time.Minute
			}
			if n.Nap != wantNap || n.Asleep != 8*time.Hour+wantNap {
				t.Errorf("asleep %v nap %v, want %v and %v", n.Asleep, n.Nap, 8*time.Hour+wantNap, wantNap)
			}
		})
	}
}

// Awake counts only inside the main block, like the awakenings do. Awake
// before falling asleep is latency and awake after waking is just being up.
func TestSleepAwakeIsInsideTheMainBlockOnly(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "awake", "2024-12-01 22:30 +0100", "2024-12-01 23:00 +0100", watch) // before sleep
	addSleep(t, conn, "core", "2024-12-01 23:00 +0100", "2024-12-02 02:00 +0100", watch)
	addSleep(t, conn, "awake", "2024-12-02 02:00 +0100", "2024-12-02 02:10 +0100", watch) // awakening
	addSleep(t, conn, "core", "2024-12-02 02:10 +0100", "2024-12-02 07:00 +0100", watch)
	addSleep(t, conn, "awake", "2024-12-02 07:00 +0100", "2024-12-02 07:45 +0100", watch) // up
	nights, err := SleepNights(context.Background(), conn, "2024-12-02", "2024-12-02")
	if err != nil || len(nights) != 1 {
		t.Fatalf("%v %+v", err, nights)
	}
	n := nights[0]
	if n.Awake != 10*time.Minute || n.Awakenings != 1 {
		t.Errorf("awake %v in %d awakenings, want 10m in 1", n.Awake, n.Awakenings)
	}
	// Efficiency without in_bed rows: 7h 50m asleep, 10m awake inside the night.
	if pct, _, ok := n.Efficiency(); !ok || pct < 97.9 || pct > 97.95 {
		t.Errorf("efficiency %.2f ok=%v, want 97.92", pct, ok)
	}
}

// An awake row that overlaps asleep rows of the same source wins: the
// overlap is not asleep time.
func TestSleepAwakeOverlapIsNotAsleep(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "core", "2024-12-01 23:00 +0100", "2024-12-02 07:00 +0100", watch)
	addSleep(t, conn, "awake", "2024-12-02 02:00 +0100", "2024-12-02 03:00 +0100", watch)
	nights, err := SleepNights(context.Background(), conn, "2024-12-02", "2024-12-02")
	if err != nil || len(nights) != 1 {
		t.Fatalf("%v %+v", err, nights)
	}
	n := nights[0]
	if n.Asleep != 7*time.Hour || n.Core != 7*time.Hour {
		t.Errorf("asleep %v core %v, want 7h", n.Asleep, n.Core)
	}
	if n.Awake != time.Hour || n.Awakenings != 1 {
		t.Errorf("awake %v in %d awakenings, want 1h in 1", n.Awake, n.Awakenings)
	}
	if pct, _, ok := n.Efficiency(); !ok || pct != 87.5 {
		t.Errorf("efficiency %.1f ok=%v, want 87.5 (7h of 8h)", pct, ok)
	}
}

// A night with nothing but an awake row has no usable sleep: it is not a
// night, and certainly not "in bed".
func TestSleepAwakeOnlyNightIsNoNight(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "awake", "2024-12-02 03:00 +0100", "2024-12-02 03:05 +0100", watch)
	r, err := BuildSleepReport(context.Background(), conn, time.Date(2024, 12, 2, 12, 0, 0, 0, time.UTC), SleepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Empty || r.Kind == "in_bed" {
		t.Errorf("empty %v kind %q, want an empty report", r.Empty, r.Kind)
	}
}

// Two sources with exactly the same asleep time in main blocks more than 3 h
// apart: the pick must not depend on Go's random map order.
func TestSleepSourceTieIsDeterministic(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "core", "2024-12-02 09:00 +0100", "2024-12-02 13:00 +0100", "Zed")
	addSleep(t, conn, "core", "2024-12-02 01:00 +0100", "2024-12-02 05:00 +0100", "Amy")
	for i := range 50 {
		nights, err := SleepNights(context.Background(), conn, "2024-12-02", "2024-12-02")
		if err != nil || len(nights) != 1 {
			t.Fatalf("%v %+v", err, nights)
		}
		if got := nights[0].Source; got != "Amy" {
			t.Fatalf("run %d: source %q, want Amy (first by name on a tie)", i, got)
		}
	}
}
