package cli

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

// Like every other command, trend takes --ascii and --no-color and prints
// pure ASCII when the output is a pipe (here: a buffer).
func TestTrendIsPlainASCIIOnAPipe(t *testing.T) {
	path := seedDB(t)
	for _, extra := range [][]string{nil, {"--ascii"}, {"--no-color"}, {"--ascii", "--no-color"}} {
		args := append([]string{"--db", path, "trend", "steps", "--days", "14", "--date", "2026-11-08", "-v"}, extra...)
		out, _, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", extra, err)
		}
		for _, r := range out {
			if r > 127 {
				t.Fatalf("%v: non-ASCII %q in:\n%s", extra, r, out)
			}
		}
	}
}

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

// The trend line uses the palette of the heatmaps: the hue of the metric,
// dim for low bars and bright for high ones. Never one flat cyan.
func TestTrendLineUsesTheMetricPalette(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, math.NaN()}
	st := style{color: true}
	cases := []struct {
		metric string
		hue    string
		first  string // colour of the lowest bar
		last   string // colour of the tallest bar
	}{
		{"step_count", stats.HueOrange, "38;5;94", "38;5;214"},
		{"resting_heart_rate", stats.HueRed, "38;5;52", "38;5;196"},
		{"respiratory_rate", stats.HueCyan, "38;5;23", "38;5;51"},
		{"body_mass", stats.HueViolet, "38;5;53", "38;5;99"},
		{"sleep", stats.HueBlue, "38;5;24", "38;5;117"},
	}
	for _, c := range cases {
		if got := stats.HueFor(c.metric); got != c.hue && !(c.hue == stats.HueOrange && got == "") {
			t.Errorf("%s: hue %q, want %q", c.metric, got, c.hue)
		}
		line := trendLine(values, stats.HueFor(c.metric), unicodeGlyphs, st)
		if !strings.HasPrefix(line, "\x1b["+c.first+"m") {
			t.Errorf("%s: lowest bar should start with colour %s: %q", c.metric, c.first, line)
		}
		if !strings.Contains(line, "\x1b["+c.last+"m") {
			t.Errorf("%s: tallest bar should use colour %s: %q", c.metric, c.last, line)
		}
		if c.hue != stats.HueCyan && strings.Contains(line, "\x1b[36m") {
			t.Errorf("%s: the old flat cyan is back: %q", c.metric, line)
		}
		if !strings.Contains(line, "\x1b[2m·") && !strings.Contains(line, "\x1b[2m"+string(unicodeGlyphs.gap)) {
			t.Errorf("%s: the gap should be dim: %q", c.metric, line)
		}
		// Colour adds nothing but colour.
		if plain := trendLine(values, stats.HueFor(c.metric), unicodeGlyphs, style{}); stripANSI(line) != plain {
			t.Errorf("%s: stripped %q != plain %q", c.metric, stripANSI(line), plain)
		}
	}
}

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

// trend never prints a line wider than the terminal (50..100 columns).
func TestTrendFitsTheTerminalWidth(t *testing.T) {
	path := seedDB(t)
	for _, width := range []string{"50", "60", "80", "100"} {
		t.Setenv("COLUMNS", width)
		for _, args := range [][]string{
			{"trend", "steps", "--date", "2026-11-08", "-v"},
			{"trend", "weight", "--date", "2026-11-08"},
			{"trend", "sleep", "--date", "2026-11-08"},
			{"trend", "steps", "--date", "2026-12-20"},                 // empty window, hint to a date
			{"trend", "steps", "--days", "90", "--date", "2026-11-08"}, // longer than the terminal
			{"trend", "rr"}, // no data at all: import hint
		} {
			out, _, err := run(t, append([]string{"--db", path}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			limit := atoiOr(width)
			for _, line := range strings.Split(out, "\n") {
				if n := utf8.RuneCountInString(line); n > limit {
					t.Errorf("COLUMNS=%s %v: %d columns: %q", width, args, n, line)
				}
			}
		}
	}
}

func atoiOr(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestNetTrendCommand(t *testing.T) {
	steps := []string{"trend", "steps", "--date", "2026-11-08"}
	with := func(extra ...string) []string { return append(append([]string{}, steps...), extra...) }
	runNetCases(t, []netCase{
		{name: "trend_steps_w80", seed: seedDB, now: netTrendClock, cols: "80", args: with("--days", "14")},
		{name: "trend_steps_w50", seed: seedDB, now: netTrendClock, cols: "50", args: with("--days", "14")},
		{name: "trend_steps_w50_verbose", seed: seedDB, now: netTrendClock, cols: "50", args: with("--days", "10", "-v")},
		{name: "trend_steps_w80_colour", seed: seedDB, now: netTrendClock, cols: "80", force: true, args: with("--days", "14")},
		{name: "trend_steps_w50_colour", seed: seedDB, now: netTrendClock, cols: "50", force: true, args: with("--days", "14")},
		{name: "trend_steps_nocolor_flag", seed: seedDB, now: netTrendClock, cols: "80", force: true, args: with("--days", "14", "--no-color")},
		{name: "trend_steps_NO_COLOR", seed: seedDB, now: netTrendClock, cols: "80", force: true, env: map[string]string{"NO_COLOR": "1"}, args: with("--days", "14")},
		{name: "trend_steps_ascii_flag", seed: seedDB, now: netTrendClock, cols: "80", args: with("--days", "14", "--ascii")},
		{name: "trend_steps_one_day", seed: seedDB, now: netTrendClock, cols: "80", args: with("--days", "1")},
		{name: "trend_steps_long_json", seed: seedDB, now: netTrendClock, args: with("--days", "45", "--json")},
		// The gap is 2026-11-01.
		{name: "trend_steps_gap_day", seed: seedDB, now: netTrendClock, cols: "80", args: []string{"trend", "steps", "--date", "2026-11-03", "--days", "10", "-v"}},
		{name: "trend_steps_gap_day_json", seed: seedDB, now: netTrendClock, args: []string{"trend", "steps", "--date", "2026-11-03", "--days", "10", "--json"}},
		{name: "trend_steps_gap_day_colour", seed: seedDB, now: netTrendClock, cols: "60", force: true, args: []string{"trend", "steps", "--date", "2026-11-03", "--days", "10"}},
		{name: "trend_steps_default_date", seed: seedDB, now: netTrendClock, cols: "80", args: []string{"trend", "steps", "--days", "7"}},
		{name: "trend_steps_no_data_window", seed: seedDB, now: netTrendClock, cols: "80", args: []string{"trend", "steps", "--date", "2026-09-01", "--days", "7"}},
		{name: "trend_steps_no_data_window_json", seed: seedDB, now: netTrendClock, args: []string{"trend", "steps", "--date", "2026-09-01", "--days", "7", "--json"}},
		{name: "trend_steps_empty_db", seed: emptyDB, now: netTrendClock, cols: "80", args: []string{"trend", "steps"}},
		{name: "trend_sleep_w80", seed: seedDB, now: netTrendClock, cols: "80", args: []string{"trend", "sleep", "--date", "2026-11-08", "--days", "10", "-v"}},
		{name: "trend_sleep_w50", seed: seedDB, now: netTrendClock, cols: "50", args: []string{"trend", "sleep", "--date", "2026-11-08", "--days", "10"}},
		{name: "trend_sleep_colour", seed: seedDB, now: netTrendClock, cols: "80", force: true, args: []string{"trend", "sleep", "--date", "2026-11-08", "--days", "10"}},
		{name: "trend_sleep_json", seed: seedDB, now: netTrendClock, args: []string{"trend", "sleep", "--date", "2026-11-08", "--days", "10", "--json"}},
		{name: "trend_sleep_no_night", seed: seedDB, now: netTrendClock, cols: "80", args: []string{"trend", "sleep", "--date", "2026-11-03", "--days", "5"}},
		{name: "trend_rhr_w80", seed: seedDB, now: netTrendClock, cols: "80", args: []string{"trend", "rhr", "--date", "2026-11-08", "--days", "10", "-v"}},
		{name: "trend_rhr_json", seed: seedDB, now: netTrendClock, args: []string{"trend", "rhr", "--date", "2026-11-08", "--days", "10", "--json"}},
		{name: "trend_weight_w80", seed: seedDB, now: netTrendClock, cols: "80", args: []string{"trend", "weight", "--date", "2026-11-08", "--days", "10"}},
		{name: "trend_err_unknown_metric", seed: emptyDB, now: netTrendClock, args: []string{"trend", "bogus"}},
		{name: "trend_err_days0", seed: emptyDB, now: netTrendClock, args: []string{"trend", "steps", "--days", "0"}},
		{name: "trend_err_days_big", seed: emptyDB, now: netTrendClock, args: []string{"trend", "steps", "--days", "4000"}},
		{name: "trend_err_bad_date", seed: emptyDB, now: netTrendClock, args: []string{"trend", "steps", "--date", "yesterday"}},
		{name: "trend_err_no_metric", seed: emptyDB, now: netTrendClock, args: []string{"trend"}},
		{name: "trend_err_no_db", now: netTrendClock, args: []string{"--db", "/nonexistent-dir/none.db", "trend", "steps"}},
	})
}

// The command cannot reach the Unicode screens (output is not a terminal), so
// the sparkline hues are pinned by rendering with an explicit termInfo.
func TestNetTrendRenderUnicode(t *testing.T) {
	path := seedDB(t)
	seedDays(t, path, "respiratory_rate", "2026-10-10", 14, 15, 16, 15, 14, 18, 17, 16, 15, 14, 13, 15, 16, 17, 18, 19, 17, 16, 15, 14, 15, 16, 17, 16, 15, 14, 15, 16, 17)
	var b strings.Builder
	for _, c := range []struct {
		metric string
		ti     termInfo
	}{
		{"steps", termInfo{width: 80, unicode: true}},
		{"steps", termInfo{width: 50, unicode: true}},
		{"steps", termInfo{width: 80, unicode: true, color: true}},
		{"steps", termInfo{width: 50, unicode: true, color: true}},            // orange
		{"rhr", termInfo{width: 80, unicode: true, color: true}},              // red
		{"respiratory_rate", termInfo{width: 80, unicode: true, color: true}}, // cyan
		{"weight", termInfo{width: 80, unicode: true, color: true}},           // violet
		{"sleep", termInfo{width: 80, unicode: true, color: true}},            // blue
		{"sleep", termInfo{width: 50, unicode: true}},
	} {
		metric, unit, err := resolveTrendMetric(c.metric)
		if err != nil {
			t.Fatal(err)
		}
		conn := openTestDB(t, path)
		r, err := buildTrend(t.Context(), conn, metric, unit, "2026-11-08", 21)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		r.verbose = c.metric == "steps" && c.ti.width == 50
		b.WriteString("## " + c.metric + " " + termName(c.ti) + "\n" + renderTrend(r, c.ti) + "\n")
	}
	checkViewGolden(t, "net_trend_unicode.txt", b.String())
}

// An iPhone alone writes only in_bed rows. Those nights have no sleep
// STAGES, so they must not show up as 0 h nights that drag the mean down.
func TestTrendSleepIgnoresInBedOnlyNights(t *testing.T) {
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		end := day(t, "2026-10-20") + int64(i)*86400 + 7*3600
		stage, src := "core", "Test Apple Watch"
		if i%2 == 1 {
			stage, src = "in_bed", "Test iPhone"
		}
		if _, err := conn.Exec(`INSERT INTO sleep_stages (stage,start_ts,end_ts,tz_offset,source) VALUES (?,?,?,0,?)`,
			stage, end-8*3600, end, src); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "trend", "sleep", "--days", "10", "--date", "2026-10-29", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Summary struct {
			DaysWithData int     `json:"days_with_data"`
			Mean         float64 `json:"mean"`
		}
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Summary.DaysWithData != 5 || r.Summary.Mean != 8 {
		t.Errorf("summary %+v, want 5 nights of 8 h", r.Summary)
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
