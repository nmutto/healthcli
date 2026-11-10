package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
)

// seedDays writes one reading per day for metric, one value per day starting
// at first (a "YYYY-MM-DD" date), at 12:00 UTC from a synthetic source.
func seedDays(t *testing.T, path, metric, first string, values ...float64) {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start, err := time.Parse("2006-01-02", first)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range values {
		ts := start.AddDate(0, 0, i).Add(12 * time.Hour).Unix()
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, '', 0, 'Test Band')`, metric, ts, ts+60, v); err != nil {
			t.Fatal(err)
		}
	}
}

// A baseline mean of 0 has no percentage: delta_pct is null, the text says
// n/a, and a jump from 0 to 30 is still "better".
func TestTodayZeroBaseline(t *testing.T) {
	fixClock(t, time.Date(2024, 12, 12, 15, 0, 0, 0, time.UTC))
	path := tempDB(t)
	seedDays(t, path, "flights_climbed", "2024-12-01", 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 30)

	out, _, err := run(t, "--db", path, "today", "--date", "2024-12-11", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Metrics []struct {
			Verdict    string `json:"verdict"`
			Comparison struct {
				DeltaPct *float64 `json:"delta_pct"`
			} `json:"comparison"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Metrics) != 1 || rep.Metrics[0].Verdict != "better" {
		t.Fatalf("want one metric with verdict better, got %s", out)
	}
	if rep.Metrics[0].Comparison.DeltaPct != nil {
		t.Errorf("delta_pct = %v, want null", *rep.Metrics[0].Comparison.DeltaPct)
	}

	text, _, err := run(t, "--db", path, "today", "--date", "2024-12-11", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "n/a") {
		t.Errorf("text should say n/a:\n%s", text)
	}
}

// One future-dated sample, workout or sleep row must not pull the fallback
// day into the future; the overview ignores them the same way.
func TestTodayFallbackIgnoresFuture(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC))
	path := tempDB(t)
	seedDays(t, path, "step_count", "2026-10-01", 5000)
	future := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC).Unix()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source) VALUES ('heart_rate', ?1, ?1, 60, '', 0, 'Test Band')`,
		`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, tz_offset, source) VALUES ('Running', ?1, ?1 + 60, 60, 0, 'Test Band')`,
		`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source) VALUES ('core', ?1, ?1 + 3600, 0, 'Test Band')`,
	} {
		if _, err := conn.Exec(q, future); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "today", "--date", "2026-11-08", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Status string `json:"status"`
		Date   string `json:"date"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Status != statusFallback || rep.Date != "2026-10-01" {
		t.Errorf("status %q date %q, want fallback to 2026-10-01", rep.Status, rep.Date)
	}
}
