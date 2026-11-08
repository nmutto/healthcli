package stats

import (
	"context"
	"testing"

	"github.com/nmutto/healthcli/internal/importer"
)

// TestSmallFixture runs the read side over the real importer and the
// synthetic two-week export in testdata/small (Watch + iPhone, a trip from
// +0100 to -0500 and back).
func TestSmallFixture(t *testing.T) {
	conn := newDB(t)
	if _, err := importer.Import(conn, "../../testdata/small/export.xml", importer.Options{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	steps := series(t, conn, "steps", "2024-12-01", "2024-12-14")
	if len(steps) != 14 {
		t.Fatalf("got %d step days, want 14", len(steps))
	}
	for _, dv := range steps {
		if dv.Source != watch {
			t.Errorf("%s: steps from %q, want the Watch", dv.Day, dv.Source)
		}
		// Cross-check against a plain "Watch rows only" sum per local day.
		var want float64
		err := conn.QueryRow(`SELECT SUM(value) FROM samples
			WHERE metric = 'step_count' AND source = ? AND date(start_ts + tz_offset, 'unixepoch') = ?`,
			watch, dv.Day).Scan(&want)
		if err != nil {
			t.Fatal(err)
		}
		if dv.Value != want {
			t.Errorf("%s: steps %v, want Watch-only %v", dv.Day, dv.Value, want)
		}
	}
	if steps[0].Value != 16525 {
		t.Errorf("2024-12-01 steps = %v, want 16525 (Watch only, iPhone's 14866 ignored)", steps[0].Value)
	}

	// body_mass is iPhone-only and Latest; the 7th was weighed in New York.
	mass := series(t, conn, "weight", "2024-12-01", "2024-12-14")
	if len(mass) != 5 || mass[2].Day != "2024-12-07" || mass[2].Value != 78.9 || mass[2].Source != iphone {
		t.Errorf("body_mass = %+v", mass)
	}

	b, err := BaselineFor(ctx, conn, "steps", "2024-12-14", 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.N != 13 || b.Mean <= 0 || b.Stddev <= 0 {
		t.Errorf("baseline = %+v", b)
	}
	if _, err := BaselineFor(ctx, conn, "weight", "2024-12-14", 0); err == nil {
		t.Error("5 weigh-ins should not be enough for a baseline")
	}

	nights, err := SleepNights(ctx, conn, "2024-12-01", "2024-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(nights) != 14 || nights[0].Night != "2024-12-02" {
		t.Fatalf("got %d nights starting %+v", len(nights), nights[0])
	}
	for _, n := range nights {
		if n.Asleep <= 0 || n.Asleep >= n.InBed || n.InBedSource != iphone {
			t.Errorf("night %+v", n)
		}
	}

	ws, err := Workouts(ctx, conn, "2024-12-07", "2024-12-07")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Activity != "running" || ws[0].Start.Hour() != 18 {
		t.Errorf("workouts on the 7th = %+v", ws)
	}

	hr, err := LatestSample(ctx, conn, "hr")
	if err != nil || hr.Source != watch {
		t.Errorf("latest hr = %+v, %v", hr, err)
	}
}
