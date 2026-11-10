package cli

import (
	"strings"
	"testing"

	"github.com/nmutto/healthcli/internal/db"
)

// addDailySamples writes one sample a day for the 12 days before 2026-11-09
// into an existing test database.
func addDailySamples(t *testing.T, path, metric, unit string, value float64) {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i := 0; i < 12; i++ {
		ts := day(t, "2026-10-28") + int64(i)*86400 + 3600*8
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, ?, 0, 'Test Apple Watch')`, metric, ts, ts+60, value, unit); err != nil {
			t.Fatal(err)
		}
	}
}

// trend and heatmap title a metric with its registry label and write its
// unit the way overview does ("15.8/min", "97%"), never the raw names.
func TestTrendAndHeatmapUseRegistryLabelsAndUnits(t *testing.T) {
	path := tempDB(t)
	addDailySamples(t, path, "respiratory_rate", "breaths/min", 15.8)
	addDailySamples(t, path, "vo2_max", "mL/kg/min", 42)
	addDailySamples(t, path, "body_mass_index", "kg/m2", 23.4)
	addDailySamples(t, path, "apple_sleeping_wrist_temperature", "degC", 36.4)
	addDailySamples(t, path, "oxygen_saturation", "percent", 97)
	addDailySamples(t, path, "hrv_sdnn", "ms", 55)

	trends := []struct{ arg, title, value string }{
		{"rr", "Respiratory rate, last", "15.8/min"},
		{"vo2", "VO2 max, last", "42.0 mL/kg/min"},
		{"bmi", "BMI, last", "23.4"},
		{"temp", "Wrist temperature, last", "36.4 C"},
		{"spo2", "Blood oxygen, last", "97%"},
		{"hrv", "Heart rate variability, last", "55 ms"},
	}
	for _, c := range trends {
		out, _, err := run(t, "--db", path, "trend", c.arg, "--days", "10", "--date", "2026-11-08")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{c.title, c.value} {
			if !strings.Contains(out, want) {
				t.Errorf("trend %s: missing %q in:\n%s", c.arg, want, out)
			}
		}
		if strings.Contains(strings.SplitN(out, "\n", 2)[0], "_") {
			t.Errorf("trend %s: raw name in the heading: %s", c.arg, out)
		}
	}

	heats := map[string]string{
		"spo2": "Blood oxygen per day", "hrv": "Heart rate variability per day",
		"temp": "Wrist temperature per day", "rr": "Respiratory rate per day",
	}
	for arg, title := range heats {
		out, _, err := run(t, "--db", path, "heatmap", arg)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, title) {
			t.Errorf("heatmap %s: want %q in:\n%s", arg, title, out)
		}
	}
}
