package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

// A baseline that overflowed is "no usable baseline", not "need 7d (have 28)".
func TestOverflowedBaselineIsNotNeedMoreDays(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 28; i++ { // 28 huge days: their sum overflows
		ts := day(t, "2026-11-08") - int64(i)*86400 + 12*3600
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES ('step_count', ?, ?, 1.7e308, 'count', 0, 'Test Phone')`, ts, ts+60); err != nil {
			t.Fatal(err)
		}
	}
	ts := day(t, "2026-11-08") + 12*3600
	if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES ('step_count', ?, ?, 5000, 'count', 0, 'Test Phone')`, ts, ts+60); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	for _, cmd := range [][]string{{"overview"}, {"today", "--date", "2026-11-08"}} {
		out, _, err := run(t, append([]string{"--db", path}, cmd...)...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "have 28") || !strings.Contains(out, "no usable baseline") {
			t.Errorf("%v:\n%s", cmd, out)
		}
	}
}

// With a flat baseline every z is 0; the headline then names the metric that
// moved the most, not the first one that differs.
func TestHeadlineMoverTieGoesToTheBiggerChange(t *testing.T) {
	above, zero := stats.Above, 0.0
	row := func(label, unit string, value, mean, pct float64) stats.MetricRow {
		return stats.MetricRow{Label: label, Unit: unit, State: stats.StateOK, Verdict: stats.VerdictChanged,
			Comparison: &above, Z: &zero, Value: &value, Mean: &mean, DeltaPct: &pct}
	}
	o := &stats.Overview{Metrics: []stats.MetricRow{
		row("Small mover", "kg", 70.2, 70, 0.3),
		row("Big mover", "kg", 90, 70, 28.6),
		row("Percent mover", "percent", 98, 97, 1), // 1 point
	}}
	got := headline(o, overviewView{}, asciiGlyphs, 100)
	if !strings.Contains(got, "Big mover") {
		t.Errorf("headline = %q, want Big mover", got)
	}
}
