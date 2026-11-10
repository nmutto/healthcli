package cli

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
)

// addWeights writes one weight a day for n days ending 2026-11-08, with
// values that differ from day to day so that the mean tells baselines apart.
func addWeights(t *testing.T, path string, n int) {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i := 0; i < n; i++ {
		ts := day(t, "2026-11-08") - int64(i)*86400 + 8*3600
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES ('body_mass', ?, ?, ?, 'kg', 0, 'manual')`, ts, ts+60, 70+float64(i)); err != nil {
			t.Fatal(err)
		}
	}
}

// trend and overview must report the same baseline for the same data: the
// 28 days BEFORE the compared day, the day itself left out.
func TestTrendBaselineAgreesWithOverview(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	for _, n := range []int{5, 12} {
		path := tempDB(t)
		addWeights(t, path, n)

		var ov struct {
			Metrics []struct {
				Metric string   `json:"metric"`
				Days   int      `json:"baseline_days_with_data"`
				Mean   *float64 `json:"baseline_mean"`
			} `json:"metrics"`
		}
		out, _, err := run(t, "--db", path, "overview", "--json")
		if err != nil || json.Unmarshal([]byte(out), &ov) != nil {
			t.Fatalf("overview: %v\n%s", err, out)
		}
		var tr trendReport
		out, _, err = run(t, "--db", path, "trend", "weight", "--days", "7", "--date", "2026-11-08", "--json")
		if err != nil || json.Unmarshal([]byte(out), &tr) != nil {
			t.Fatalf("trend: %v\n%s", err, out)
		}

		for _, m := range ov.Metrics {
			if m.Metric != "body_mass" {
				continue
			}
			if tr.Baseline.DaysWithData != m.Days || m.Days != n-1 {
				t.Errorf("n=%d: trend has %d baseline days, overview %d, want %d", n, tr.Baseline.DaysWithData, m.Days, n-1)
			}
			if (tr.Baseline.Mean == nil) != (m.Mean == nil) || (m.Mean != nil && math.Abs(*tr.Baseline.Mean-*m.Mean) > 1e-9) {
				t.Errorf("n=%d: trend mean %v, overview mean %v", n, tr.Baseline.Mean, m.Mean)
			}
		}
	}
}
