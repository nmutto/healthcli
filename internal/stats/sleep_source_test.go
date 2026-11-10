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
