package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/importer"
	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/nmutto/healthcli/internal/stats"
)

// overviewNow is the fixed "now" of every overview test.
var overviewNow = time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC)

// seedOverview builds a synthetic database for one scenario:
//
//	full       every state at once: ok, stale (weight), gaps (HRV), sleep, workouts, an import
//	stale      the same data, but nothing newer than 12 days ago
//	sleep-only one night of sleep and nothing else
//	empty      nothing
//	steps-only one device that records only steps
//	import:X   the synthetic export testdata/X, dates moved to be recent
func seedOverview(t *testing.T, scenario string) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if scenario == "empty" {
		return path
	}
	if strings.HasPrefix(scenario, "import:") {
		conn.Close()
		return seedFromFixture(t, path, strings.TrimPrefix(scenario, "import:"))
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	sample := func(metric string, d time.Time, hour int, value float64, source string) {
		ts := d.Add(time.Duration(hour) * time.Hour).Unix()
		exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, '', 0, ?)`, metric, ts, ts+60, value, source)
	}
	stage := func(stage string, startTS, endTS int64) {
		exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
			VALUES (?, ?, ?, 0, 'Test Apple Watch')`, stage, startTS, endTS)
	}

	// shift moves everything back, so "stale" is the same data, just older.
	shift := 0
	if scenario == "stale" {
		shift = 12
	}
	midnight := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	dayAt := func(back int) time.Time { return midnight.AddDate(0, 0, -(back + shift)) }

	if scenario == "steps-long" { // 12 full weeks of steps, a few zero days
		for back := 0; back < 90; back++ {
			steps := 4000 + float64((back*37)%11)*700
			if back%13 == 5 {
				steps = 0
			}
			sample("step_count", dayAt(back), 12, steps, "Test Phone")
		}
		return path
	}
	if scenario == "steps-only" {
		for back := 0; back < 20; back++ {
			sample("step_count", dayAt(back), 12, 5000+float64((back*7)%9)*600, "Test Phone")
		}
		return path
	}

	if scenario != "sleep-only" {
		for back := 0; back < 40; back++ {
			d := dayAt(back)
			i := 40 - back // grows with time so trends are visible
			steps := 6000 + float64((i*7)%9)*450
			energy := 420 + float64((i*5)%8)*40
			if back == 0 {
				steps, energy = 11800, 640 // today: well above usual
			}
			sample("step_count", d, 12, steps, "Test Apple Watch")
			sample("step_count", d, 13, 99999, "Test iPhone") // loses to the Watch
			sample("active_energy", d, 12, energy, "Test Apple Watch")
			sample("heart_rate", d, 9, 66+float64((i*3)%7), "Test Apple Watch")
			if back >= 1 { // resting HR is written a day late
				sample("resting_heart_rate", d, 7, 58+float64((i*2)%5), "Test Apple Watch")
			}
			if back >= 2 && back%4 != 3 { // HRV: starts two days ago, with holes
				sample("hrv_sdnn", d, 3, 52+float64((i*5)%11), "Test Apple Watch")
			}
		}
		sample("resting_heart_rate", dayAt(1), 8, 70, "Test Apple Watch") // yesterday is a high one
		// Weight: a few weigh-ins, the newest one nine days ago.
		for _, back := range []int{9, 12, 16, 21} {
			sample("body_mass", dayAt(back), 8, 72.4+float64(back%3)*0.1, "manual")
		}
	}

	// One night ending today: core, REM, deep and awake, plus iPhone in-bed.
	night := dayAt(1).Unix() // yesterday 00:00 UTC
	stage("core", night+23*3600, night+27*3600+3000)
	stage("rem", night+27*3600+3000, night+28*3600+1800)
	stage("deep", night+28*3600+1800, night+30*3600)
	stage("awake", night+30*3600, night+30*3600+900)
	exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES ('in_bed', ?, ?, 0, 'Test iPhone')`, night+22*3600+1800, night+31*3600)

	if scenario != "sleep-only" {
		for _, w := range []struct {
			back     int
			activity string
			km       float64
			mins     int
			kcal     float64
		}{{3, "running", 5200, 31, 410}, {6, "cycling", 18000, 55, 520}, {9, "running", 8000, 52, 630}} {
			ts := dayAt(w.back).Unix() + 18*3600
			exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
				VALUES (?, ?, ?, ?, ?, ?, 0, 'Test Apple Watch')`, w.activity, ts, ts+int64(w.mins*60), w.mins*60, w.kcal, w.km)
		}
		exec(`INSERT INTO meta (key, value) VALUES ('last_import_summary', ?)`,
			fmt.Sprintf(`{"at": %d, "read": 1275, "inserted": 1230, "duplicates": 40, "skipped": {"unknown_type": 3, "bad_date": 1, "bad_value": 1}}`,
				overviewNow.AddDate(0, 0, -3).Unix()))
	}
	return path
}

// seedFromFixture imports a synthetic export from testdata and moves every
// timestamp forward by whole days so the newest data is on overviewNow's day
// (the fixtures are dated 2024). Same trick as the importer's other tests.
func seedFromFixture(t *testing.T, path, fixture string) string {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := importer.Import(conn, "../../testdata/"+fixture+"/export.xml", importer.Options{}); err != nil {
		t.Fatal(err)
	}
	var newest int64
	if err := conn.QueryRow(`SELECT max(end_ts + tz_offset) FROM samples`).Scan(&newest); err != nil {
		t.Fatal(err)
	}
	const day = 86400
	shift := (overviewNow.Unix()/day - newest/day) * day
	for _, q := range []string{
		`UPDATE samples SET start_ts = start_ts + ?1, end_ts = end_ts + ?1`,
		`UPDATE sleep_stages SET start_ts = start_ts + ?1, end_ts = end_ts + ?1`,
		`UPDATE workouts SET start_ts = start_ts + ?1, end_ts = end_ts + ?1`,
	} {
		if _, err := conn.Exec(q, shift); err != nil {
			t.Fatal(err)
		}
	}
	// The import time is "now" for real; pin it to a day before overviewNow.
	if _, err := conn.Exec(`UPDATE meta SET value = json_set(value, '$.at', ?) WHERE key = 'last_import_summary'`,
		overviewNow.AddDate(0, 0, -1).Unix()); err != nil {
		t.Fatal(err)
	}
	return path
}

func overviewReport(t *testing.T, scenario string) *stats.Overview {
	t.Helper()
	conn, err := db.Open(seedOverview(t, scenario))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	o, err := stats.BuildOverview(t.Context(), conn, overviewNow, stats.DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	return o
}

var testView = overviewView{dbName: "demo.db", now: overviewNow}

func TestOverviewGoldens(t *testing.T) {
	cases := []struct {
		name, scenario string
		ti             termInfo
		view           overviewView // zero value = the defaults
	}{
		{"overview_full_80.txt", "full", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_full_60.txt", "full", termInfo{width: 60, unicode: true}, overviewView{}},
		{"overview_full_100.txt", "full", termInfo{width: 100, unicode: true}, overviewView{}},
		{"overview_full_ascii.txt", "full", termInfo{width: 80}, overviewView{}},
		{"overview_full_color.txt", "full", termInfo{width: 80, unicode: true, color: true}, overviewView{}},
		{"overview_empty.txt", "empty", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_empty_60_ascii.txt", "empty", termInfo{width: 60}, overviewView{}},
		{"overview_sleep_only.txt", "sleep-only", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_stale.txt", "stale", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_apple_80.txt", "import:small", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_apple_color.txt", "import:small", termInfo{width: 80, unicode: true, color: true}, overviewView{}},
		{"overview_apple_ascii.txt", "import:small", termInfo{width: 80}, overviewView{}},
		{"overview_apple_60.txt", "import:small", termInfo{width: 60, unicode: true}, overviewView{}},
		{"overview_sparse_80.txt", "import:sparse", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_sparse_60_ascii.txt", "import:sparse", termInfo{width: 60}, overviewView{}},
		{"overview_steps_only.txt", "steps-only", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_steps_long_80.txt", "steps-long", termInfo{width: 80, unicode: true}, overviewView{}},
		{"overview_steps_long_60_ascii.txt", "steps-long", termInfo{width: 60}, overviewView{}},
		{"overview_apple_compact.txt", "import:small", termInfo{width: 80, unicode: true}, overviewView{compact: true}},
		{"overview_apple_vitals.txt", "import:small", termInfo{width: 80, unicode: true}, overviewView{categories: []string{"vitals"}}},
		{"overview_apple_sleep_activity.txt", "import:small", termInfo{width: 80, unicode: true}, overviewView{categories: []string{"sleep", "activity"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderOverview(overviewReport(t, c.scenario), c.ti, withDB(c.view, testView.dbName, testView.now))
			checkViewGolden(t, c.name, got)

			// Whatever the scenario: never wider than the terminal, and no
			// non-ASCII characters when unicode is off.
			for _, line := range strings.Split(stripANSI(got), "\n") {
				if n := runeLen(line); n > c.ti.width {
					t.Errorf("line is %d wide (> %d): %q", n, c.ti.width, line)
				}
				if !c.ti.unicode && strings.ContainsFunc(line, func(r rune) bool { return r > 127 }) {
					t.Errorf("non-ASCII in ASCII output: %q", line)
				}
			}
		})
	}
}

func TestOverviewPlainOutputHasNoEscapes(t *testing.T) {
	got := renderOverview(overviewReport(t, "full"), termInfo{width: 80, unicode: true}, testView)
	if strings.Contains(got, "\x1b") {
		t.Error("plain output contains escape codes")
	}
}

func TestOverviewJSONMatchesReport(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return overviewNow }
	defer func() { nowFunc = old }()

	path := seedOverview(t, "full")
	out, _, err := run(t, "--db", path, "overview", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got stats.Overview
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	want := overviewReport(t, "full")
	if len(got.Metrics) != len(want.Metrics) || got.Today != want.Today || got.Sleep != want.Sleep {
		t.Errorf("json differs from the report:\n%s", out)
	}
	states := map[string]string{}
	for _, m := range got.Metrics {
		states[m.Metric] = m.State
	}
	if states["body_mass"] != stats.StateStale || states["step_count"] != stats.StateOK {
		t.Errorf("states = %v", states)
	}
	checkViewGolden(t, "overview_full.json", out)
}

func TestOverviewCommandOnPipeIsASCIIAndPlain(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return overviewNow }
	defer func() { nowFunc = old }()

	out, _, err := run(t, "--db", seedOverview(t, "full"), "overview")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") || strings.ContainsFunc(out, func(r rune) bool { return r > 127 }) {
		t.Errorf("output to a pipe must be plain ASCII:\n%s", out)
	}
	if !strings.Contains(out, "healthcli overview") || !strings.Contains(out, "Steps") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestOverviewEmptyJSON(t *testing.T) {
	out, _, err := run(t, "--db", tempDB(t), "overview", "--json")
	if err != nil || !strings.Contains(out, `"empty": true`) {
		t.Errorf("got %q, %v", out, err)
	}
}

func TestOverviewRejectsArguments(t *testing.T) {
	if _, _, err := run(t, "--db", tempDB(t), "overview", "extra"); err == nil {
		t.Error("expected an error for a stray argument")
	}
}

// Hand-built reports for states and wording that the seeds do not reach.
func TestOverviewHandBuiltStates(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	o := &stats.Overview{
		Today: "2026-11-08",
		Metrics: []stats.MetricRow{
			{Metric: "step_count", Label: "Steps", Unit: "count", State: stats.StateNoBaseline, Value: f(5000), BaselineDays: 3, Spark: make([]*float64, 28)},
			{Metric: "body_mass", Label: "Weight", Unit: "kg", State: stats.StateNoData},
		},
		Sleep:    stats.OverviewSleep{State: stats.StateOK, AsleepSecs: 0, InBedSecs: 7*3600 + 40*60},
		Workouts: stats.WorkoutsSummary{Days: 30},
		Data:     stats.DataHealth{Samples: 3, SourceCount: map[string]int64{"watch": 3}},
	}
	got := renderOverview(o, termInfo{width: 80}, testView)
	for _, want := range []string{
		"Not enough history to compare yet",
		"need 7d (have 3)",
		"no data",
		"in bed 7h 40m, no sleep stages",
		"none in the last 30 days",
		"sources: Watch 100%",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "DATA     3 samples") {
		t.Errorf("want plural samples:\n%s", got)
	}
	o.Data.Samples, o.Data.SleepNights = 1, 1
	if got := renderOverview(o, termInfo{width: 80}, testView); !strings.Contains(got, "1 sample, 1 sleep night") {
		t.Errorf("want singular forms:\n%s", got)
	}
	if strings.Contains(got, "0m asleep") {
		t.Errorf("an in-bed-only night must not say '0m asleep':\n%s", got)
	}
}

// The colour decision: polarity x direction x typical. The verdict word is
// the same in plain output, so colour is never the only signal.
func TestVerdictColourTable(t *testing.T) {
	st := style{color: true}
	cases := []struct {
		polarity   metrics.Polarity
		comparison string
		code, word string // "" = no colour / no word
	}{
		{metrics.HigherBetter, stats.Above, ansiGreen, "better"},
		{metrics.HigherBetter, stats.Below, ansiRed, "worse"},
		{metrics.HigherBetter, stats.Typical, "", ""},
		{metrics.LowerBetter, stats.Below, ansiGreen, "better"},
		{metrics.LowerBetter, stats.Above, ansiRed, "worse"},
		{metrics.LowerBetter, stats.Typical, "", ""},
		{metrics.Neutral, stats.Above, ansiCyan, "changed"},
		{metrics.Neutral, stats.Below, ansiCyan, "changed"},
		{metrics.Neutral, stats.Typical, "", ""},
	}
	for _, c := range cases {
		v := stats.Verdict(c.polarity, c.comparison)
		got := verdictPaint(st, v)("x")
		want := "x"
		if c.code != "" {
			want = "\x1b[" + c.code + "mx\x1b[0m"
		}
		if got != want {
			t.Errorf("%s/%s: painted %q, want %q", c.polarity, c.comparison, got, want)
		}
		if w := verdictWord(v); w != c.word {
			t.Errorf("%s/%s: word %q, want %q", c.polarity, c.comparison, w, c.word)
		}
	}
}

// Colour paints the arrow+delta+word and the band marker, not the row.
func TestOverviewColourIsLocal(t *testing.T) {
	got := renderOverview(overviewReport(t, "full"), termInfo{width: 80, unicode: true, color: true}, testView)
	var worse string
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "Resting HR") {
			worse = l
		}
	}
	if !strings.Contains(worse, "\x1b[31m▲ +9% worse\x1b[0m") || !strings.Contains(worse, "\x1b[31m●\x1b[0m") {
		t.Errorf("worse row should be red on delta and marker: %q", worse)
	}
	// The gutter marker is coloured, the label after it is not.
	if !strings.HasPrefix(worse, " \x1b[31m!\x1b[0m Resting HR") {
		t.Errorf("red gutter marker, then a plain label: %q", worse)
	}
	if !strings.Contains(got, "\x1b[32m▲ +52% better\x1b[0m") {
		t.Errorf("steps should be green and say better:\n%s", got)
	}
}

func TestOverviewHeatmapFlags(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return overviewNow }
	defer func() { nowFunc = old }()
	path := seedOverview(t, "full")

	out, _, err := run(t, "--db", path, "overview", "--weeks", "0")
	if err != nil || strings.Contains(out, "CALENDAR") {
		t.Errorf("--weeks 0 should hide the heatmap: %v\n%s", err, out)
	}
	out, _, err = run(t, "--db", path, "overview", "--heatmap", "active_energy", "--weeks", "4")
	if err != nil || !strings.Contains(out, "active energy per day, 4 weeks") {
		t.Errorf("--heatmap override: %v\n%s", err, out)
	}
	if _, _, err = run(t, "--db", path, "overview", "--heatmap", "nonsense"); err == nil {
		t.Error("an unknown heatmap metric should fail")
	}
	if _, _, err = run(t, "--db", path, "overview", "--weeks", "99"); err == nil {
		t.Error("--weeks 99 should fail")
	}

	var rep stats.Overview
	out, _, _ = run(t, "--db", path, "overview", "--json")
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Heatmap == nil || len(rep.Heatmap.Weeks) != 12 || len(rep.NotTracked) == 0 {
		t.Errorf("json should carry heatmap and not_tracked: %+v", rep.Heatmap)
	}
	if v := rep.Metrics[1]; v.Metric != "resting_heart_rate" || v.Verdict != stats.VerdictWorse || v.Polarity != "lower_better" {
		t.Errorf("json verdict: %+v", v)
	}
}

// A device that tracks little must render cleanly: no empty rows.
func TestOverviewStepsOnlyHasNoEmptyRows(t *testing.T) {
	got := renderOverview(overviewReport(t, "steps-only"), termInfo{width: 80}, testView)
	for _, bad := range []string{"stale", "HRV", "== VITALS", "== BODY", "n/a"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "not tracked in your data: 29 metrics") {
		t.Errorf("missing not-tracked footer:\n%s", got)
	}
}

// Display units and per-metric decimals (stored units are untouched).
func TestWithUnit(t *testing.T) {
	cases := []struct {
		v            float64
		metric, unit string
		unicode      bool
		want         string
	}{
		{36.44, "apple_sleeping_wrist_temperature", "degC", true, "36.4 °C"},
		{36.44, "apple_sleeping_wrist_temperature", "degC", false, "36.4 C"},
		{97, "oxygen_saturation", "percent", true, "97%"},
		{16.24, "respiratory_rate", "breaths/min", true, "16.2/min"},
		{24.06, "body_mass_index", "kg/m2", true, "24.1"},
		{79.94, "body_mass", "kg", true, "79.9 kg"},
		{9412, "step_count", "count", true, "9,412 steps"},
		{42, "vo2_max", "mL/kg/min", true, "42.0 mL/kg/min"},
	}
	for _, c := range cases {
		if got := withUnit(c.v, c.metric, c.unit, c.unicode); got != c.want {
			t.Errorf("withUnit(%v, %s) = %q, want %q", c.v, c.metric, got, c.want)
		}
	}
}

// Whatever the data, no row may be wider than the terminal: long labels,
// freak numbers and a 1500-day-old reading are all cut or capped.
func TestOverviewRowsNeverOverflow(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	mk := func(metric, label string, state string, age int, v float64) stats.MetricRow {
		return stats.MetricRow{Metric: metric, Label: label, Category: "vitals", Unit: "count", State: state,
			AgeDays: age, Value: f(v), Spark: make([]*float64, 28), Mean: f(1), Stddev: f(1),
			DeltaPct: f(1e300), Z: f(1e300), Comparison: new(string), Verdict: stats.VerdictChanged}
	}
	*(mk("x", "", "", 0, 0).Comparison) = ""
	rows := []stats.MetricRow{
		mk("step_count", "Walking + running distance", stats.StateStale, 1500, 12345),
		mk("heart_rate", "A very long label indeed, far beyond any column", stats.StateOK, 0, 1.5e300),
		mk("body_mass", "Weight", stats.StateStale, 400000, 80),
	}
	rows[1].Comparison = new(string)
	*rows[1].Comparison = stats.Above
	o := &stats.Overview{Today: "2026-11-08", Metrics: rows, NotTracked: []string{},
		Sleep: stats.OverviewSleep{State: stats.StateNoData}, Workouts: stats.WorkoutsSummary{Days: 30},
		Data: stats.DataHealth{Samples: 3, SourceCount: map[string]int64{"watch": 3}}}
	for _, width := range []int{60, 70, 80, 90, 100} {
		for _, uni := range []bool{true, false} {
			got := renderOverview(o, termInfo{width: width, unicode: uni}, testView)
			for _, line := range strings.Split(got, "\n") {
				if n := runeLen(line); n > width {
					t.Errorf("width %d unicode %v: line is %d wide: %q", width, uni, n, line)
				}
			}
		}
	}
	if got := renderOverview(o, termInfo{width: 80}, testView); strings.Contains(got, "Inf") || strings.Contains(got, "NaN") {
		t.Errorf("non-finite text leaked:\n%s", got)
	}
}

// Month names above the grid: a later month must not vanish when it sits one
// column after the first (partial) week's label.
func TestHeatMonthLineKeepsSeptember(t *testing.T) {
	var weeks [][]stats.HeatDay
	start, _ := time.Parse(stats.DayLayout, "2026-08-25") // a Monday
	for w := 0; w < 6; w++ {
		var week []stats.HeatDay
		for d := 0; d < 7; d++ {
			week = append(week, stats.HeatDay{Day: start.AddDate(0, 0, w*7+d).Format(stats.DayLayout)})
		}
		weeks = append(weeks, week)
	}
	line := heatMonthLine(weeks)
	if !strings.Contains(line, "Sep") || !strings.Contains(line, "Oct") {
		t.Errorf("month line %q must show Sep and Oct", line)
	}
}

// All-zero data: the lowest shade and a one-sentence legend, no "<=0 <=0 <=0".
func TestHeatmapFlatLegend(t *testing.T) {
	h := &stats.Heatmap{Metric: "step_count", Unit: "count", Today: "2026-11-08", Thresholds: []float64{0, 0, 0, 0}, Flat: true, DaysWithData: 3}
	got := strings.Join(heatLegend(h, termInfo{width: 80}, style{}), "\n")
	if !strings.Contains(got, "every day: 0") || strings.Contains(got, "<=0") {
		t.Errorf("flat legend:\n%s", got)
	}
}

func TestParseCategories(t *testing.T) {
	got, err := parseCategories([]string{"Vitals", " SLEEP ", "vitals"})
	if err != nil || strings.Join(got, ",") != "vitals,sleep" {
		t.Errorf("got %v, %v", got, err)
	}
	_, err = parseCategories([]string{"heart"})
	if err == nil || !strings.Contains(err.Error(), "sleep, workouts") {
		t.Errorf("the error should list valid names: %v", err)
	}
}

func TestOverviewCategoryAndCompactFlags(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return overviewNow }
	defer func() { nowFunc = old }()
	path := seedOverview(t, "full")

	out, _, err := run(t, "--db", path, "overview", "--category", "vitals", "--heatmap", "sleep,workouts,steps")
	if err != nil || !strings.Contains(out, "VITALS") || strings.Contains(out, "ACTIVITY 1") || strings.Contains(out, "DATA") {
		t.Errorf("--category vitals: %v\n%s", err, out)
	}
	if _, _, err = run(t, "--db", path, "overview", "--category", "nope"); err == nil {
		t.Error("an unknown category should fail")
	}
	if _, _, err = run(t, "--db", path, "overview", "--heatmap", "nope"); err == nil {
		t.Error("an unknown heatmap should fail")
	}
	out, _, err = run(t, "--db", path, "overview", "--compact", "--full")
	if err != nil || strings.Contains(out, "\n\n== ACTIVITY") {
		t.Errorf("--compact has no blank line before a band: %v\n%s", err, out)
	}
}

func TestCategorySummaries(t *testing.T) {
	o := overviewReport(t, "full")
	if len(o.Categories) != 3 || o.Categories[0].Name != "vitals" || o.Categories[0].Worse != 1 {
		t.Errorf("categories: %+v", o.Categories)
	}
	for _, c := range o.Categories {
		if sum := c.Better + c.Worse + c.Changed + c.Typical + c.Stale + c.NoBaseline + c.NoData; sum != c.Total {
			t.Errorf("%s: parts add up to %d, total %d", c.Name, sum, c.Total)
		}
	}
}

// At 50 columns the table keeps the gutter, name, latest value and verdict.
func TestOverviewTableFits50(t *testing.T) {
	o := overviewReport(t, "full")
	ti := termInfo{width: 50, unicode: true}
	got := metricTable(o, ti, glyphsFor(true), style{}, overviewView{}) + tableFooter(o, style{}, ti, glyphsFor(true))
	for _, line := range strings.Split(got, "\n") {
		if runeLen(line) > 50 {
			t.Errorf("line is %d wide: %q", runeLen(line), line)
		}
	}
	if !strings.Contains(got, "! Resting HR") || !strings.Contains(got, "worse") {
		t.Errorf("gutter, name and verdict word must survive:\n%s", got)
	}
}
