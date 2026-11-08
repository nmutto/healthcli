package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/health/internal/db"
)

// Goldens for these commands live next to the code (internal/cli/testdata),
// unlike the older ones in the repo-level testdata/. Same -update flag.
func checkViewGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run with -update if intended)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// day returns midnight UTC of a "YYYY-MM-DD" day as Unix seconds. All seed
// rows use offset 0 so local day == UTC day and the numbers are easy to read.
func day(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return tm.Unix()
}

// seedDB builds a synthetic database: 30 days (2026-10-10 .. 2026-11-08) of
// steps that cycle through 6000..9000 (one gap on 2026-11-01), 2026-11-08
// is a big day, resting heart rate only on the last 3 days (too few for a
// baseline), one night of sleep and one workout on 2026-11-08.
func seedDB(t *testing.T) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	sample := func(metric, d string, hour int, value float64, unit, source string) {
		ts := day(t, d) + int64(hour)*3600
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, ?, 0, ?)`, metric, ts, ts+60, value, unit, source); err != nil {
			t.Fatal(err)
		}
	}
	start := day(t, "2026-10-10")
	for i := 0; i < 30; i++ {
		d := time.Unix(start, 0).UTC().AddDate(0, 0, i).Format("2006-01-02")
		if d == "2026-11-01" {
			continue
		}
		steps := 6000 + float64(i%7)*500
		if d == "2026-11-08" {
			steps = 12345
		}
		sample("step_count", d, 12, steps, "count", "Test Apple Watch")
		sample("step_count", d, 13, 99999, "count", "Test iPhone") // loses to the Watch
		sample("heart_rate", d, 9, 60+float64(i%5), "bpm", "Test Apple Watch")
	}
	for _, d := range []string{"2026-11-06", "2026-11-07", "2026-11-08"} {
		sample("resting_heart_rate", d, 7, 58, "bpm", "Test Apple Watch")
	}
	sample("body_mass", "2026-11-08", 8, 72.46, "kg", "manual")

	stage := func(stage string, startTS, endTS int64) {
		if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
			VALUES (?, ?, ?, 0, 'Test Apple Watch')`, stage, startTS, endTS); err != nil {
			t.Fatal(err)
		}
	}
	d7 := day(t, "2026-11-07")
	stage("core", d7+23*3600, d7+27*3600+1800) // 4h30
	stage("deep", d7+27*3600+1800, d7+29*3600) // 1h30
	stage("rem", d7+29*3600, d7+30*3600)       // 1h
	stage("awake", d7+30*3600, d7+30*3600+900) // 15m

	ts := day(t, "2026-11-08") + 18*3600
	if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
		VALUES ('running', ?, ?, 2700, 412.4, 6200, 0, 'Test Apple Watch')`, ts, ts+2700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTodayGoldenText(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "today", "--date", "2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	checkViewGolden(t, "today.txt", out)
}

func TestTodayGoldenJSON(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "today", "--date", "2026-11-08", "--json")
	if err != nil {
		t.Fatal(err)
	}
	checkViewGolden(t, "today.json", out)
}

func TestTodayFallsBackToLatestDay(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "today", "--date", "2026-12-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No data for 2026-12-01; showing latest day with data: 2026-11-08", "2026-11-08 (Sunday)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestTodayEmptyDatabaseHint(t *testing.T) {
	out, _, err := run(t, "--db", tempDB(t), "today")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"health import apple <export.zip>", "health log"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	jsonOut, _, err := run(t, "--db", tempDB(t), "today", "--json")
	if err != nil || !strings.Contains(jsonOut, `"status": "no_data"`) {
		t.Errorf("want no_data status, got %q, %v", jsonOut, err)
	}
}

func TestTodayDefaultsToTodayAndRejectsBadDate(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = old }()

	out, _, err := run(t, "--db", seedDB(t), "today")
	if err != nil || !strings.HasPrefix(out, "2026-11-08 (Sunday)") {
		t.Errorf("default date: %q, %v", out, err)
	}
	if _, _, err := run(t, "--db", tempDB(t), "today", "--date", "11/08/2026"); err == nil {
		t.Error("expected an error for a malformed date")
	}
}

func TestTrendGoldenText(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "trend", "steps", "--days", "14", "--date", "2026-11-08", "--verbose")
	if err != nil {
		t.Fatal(err)
	}
	checkViewGolden(t, "trend_steps.txt", out)
}

func TestTrendGoldenJSON(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "trend", "steps", "--days", "10", "--date", "2026-11-08", "--json")
	if err != nil {
		t.Fatal(err)
	}
	checkViewGolden(t, "trend_steps.json", out)
}

func TestTrendSleep(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "trend", "sleep", "--days", "7", "--date", "2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	// 4h30 + 1h30 + 1h of sleep = 7h 00m (awake time does not count).
	if !strings.Contains(out, "7h 00m") {
		t.Errorf("want 7h 00m of sleep:\n%s", out)
	}
}

func TestTrendUnknownMetricListsChoices(t *testing.T) {
	_, _, err := run(t, "--db", tempDB(t), "trend", "bogus")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"known metrics", "step_count", "steps=step_count", "sleep"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestTrendEmptyWindowPointsToData(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "trend", "weight", "--days", "5", "--date", "2026-12-20")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--date 2026-11-08") {
		t.Errorf("want a hint with the latest day:\n%s", out)
	}
}

func TestTrendRejectsBadDays(t *testing.T) {
	if _, _, err := run(t, "--db", tempDB(t), "trend", "steps", "--days", "0"); err == nil {
		t.Error("expected an error for --days 0")
	}
}

func TestTrendJSONGapIsNull(t *testing.T) {
	out, _, err := run(t, "--db", seedDB(t), "trend", "steps", "--days", "10", "--date", "2026-11-08", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Daily []struct {
			Day   string   `json:"day"`
			Value *float64 `json:"value"`
		} `json:"daily"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	for _, d := range r.Daily {
		if (d.Day == "2026-11-01") != (d.Value == nil) {
			t.Errorf("%s: value %v (only the gap day should be null)", d.Day, d.Value)
		}
	}
}

// ---- rendering without a database ----

func TestSparkline(t *testing.T) {
	nan := func() float64 { var z float64; return z / z }()
	cases := []struct {
		in   []float64
		want string
	}{
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8}, "▁▂▃▄▅▆▇█"},
		{[]float64{1, nan, 8}, "▁·█"},
		{[]float64{5, 5, 5}, "▄▄▄"},
		{[]float64{nan, nan}, "··"},
	}
	for _, c := range cases {
		if got := sparkline(c.in); got != c.want {
			t.Errorf("sparkline(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatting(t *testing.T) {
	if got := formatInt(1234567); got != "1,234,567" {
		t.Errorf("formatInt = %q", got)
	}
	if got := formatInt(-999.6); got != "-1,000" {
		t.Errorf("formatInt negative = %q", got)
	}
	for d, want := range map[time.Duration]string{
		45 * time.Minute:                              "45m",
		7*time.Hour + 12*time.Minute:                  "7h 12m",
		7*time.Hour + 59*time.Second + 45*time.Minute: "7h 46m",
		5*time.Hour + 59*time.Minute + 40*time.Second: "6h 00m", // no "5h 60m"
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
	if got := formatSigned(-0.04, "kg"); got != "0.0" {
		t.Errorf("formatSigned tiny = %q", got)
	}
}

func TestColorOnlyWhenEnabled(t *testing.T) {
	r := &todayReport{Status: statusOK, Date: "2026-11-08", RequestedDate: "2026-11-08"}
	if strings.Contains(renderToday(r, style{}), "\x1b[") {
		t.Error("plain style must not emit escape codes")
	}
	if !strings.Contains(renderToday(r, style{color: true}), "\x1b[") {
		t.Error("color style should emit escape codes")
	}
	var buf strings.Builder
	if colorFor(&buf) {
		t.Error("a non-terminal writer must never get color")
	}
	t.Setenv("NO_COLOR", "1")
	if colorFor(os.Stdout) {
		t.Error("NO_COLOR must disable color")
	}
}

func TestBaselineNotAvailableText(t *testing.T) {
	v := 59.0
	r := &todayReport{Status: statusOK, Date: "2026-11-08", Metrics: []todayMetric{{
		Metric: "resting_heart_rate", Label: "Resting heart rate", Unit: "bpm", Value: &v,
		Baseline: &baselineInfo{DaysWithData: 2, DaysNeeded: 7},
	}}}
	if out := renderToday(r, style{}); !strings.Contains(out, "baseline n/a (need 7 days)") {
		t.Errorf("missing n/a text:\n%s", out)
	}
}
