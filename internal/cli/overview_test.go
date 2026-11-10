package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/metrics"
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
		{"overview_apple_full_80.txt", "import:small", termInfo{width: 80, unicode: true}, overviewView{full: true}},
		{"overview_apple_full_60.txt", "import:small", termInfo{width: 60, unicode: true}, overviewView{full: true}},
		{"overview_apple_full_100.txt", "import:small", termInfo{width: 100, unicode: true}, overviewView{full: true}},
		{"overview_apple_full_ascii_60.txt", "import:small", termInfo{width: 60}, overviewView{full: true}},
		{"overview_apple_compact_60.txt", "import:small", termInfo{width: 60, unicode: true}, overviewView{compact: true}},
		{"overview_apple_compact_100.txt", "import:small", termInfo{width: 100, unicode: true}, overviewView{compact: true}},
		{"overview_apple_heatmaps_80.txt", "import:small", termInfo{width: 80, unicode: true},
			overviewView{categories: []string{"vitals"}, heatmaps: []string{"workouts", "sleep", "step_count"}}},
		{"overview_apple_sleep_only_cat.txt", "import:small", termInfo{width: 80, unicode: true}, overviewView{categories: []string{"sleep"}}},
		{"overview_apple_workouts_only_cat.txt", "import:small", termInfo{width: 80, unicode: true}, overviewView{categories: []string{"workouts"}}},
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
	fixClock(t, overviewNow)

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
	fixClock(t, overviewNow)

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
	out, _, err := run(t, "--db", emptyDB(t), "overview", "--json")
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
		// An in-bed-only night: the section says so instead of "0m asleep".
		SleepSummary: &stats.SleepReport{Kind: "in_bed", Last: stats.SleepNight{InBed: 7*time.Hour + 40*time.Minute}},
		Data:         stats.DataHealth{Samples: 3, SourceCount: map[string]int64{"watch": 3}},
	}
	got := renderOverview(o, termInfo{width: 80}, testView)
	for _, want := range []string{
		"Not enough history to compare yet",
		"need 7d (have 3)",
		"no data",
		"in bed 7h 40m, no sleep stages",
		"sources: Watch 100%",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "DATA == 3 samples") {
		t.Errorf("want plural samples:\n%s", got)
	}
	o.Data.Samples, o.Data.SleepNights = 1, 1
	if got := renderOverview(o, termInfo{width: 80}, testView); !strings.Contains(got, "1 sample, 1 sleep night") {
		t.Errorf("want singular forms:\n%s", got)
	}
	if strings.Contains(got, "none in") || strings.Contains(got, "TYPE ") {
		t.Errorf("no workouts: the section must not appear:\n%s", got)
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
		{metrics.Neutral, stats.Above, "", "higher"}, // a move with no good direction: plain text, no colour
		{metrics.Neutral, stats.Below, "", "lower"},
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
		row := stats.MetricRow{Verdict: v, Comparison: &c.comparison}
		if w := verdictWord(row); w != c.word {
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
	if !strings.Contains(worse, "\x1b[1;31m▲ +9% worse\x1b[0m") || !strings.Contains(worse, "\x1b[1;31m●\x1b[0m") {
		t.Errorf("worse row should be red on delta and marker: %q", worse)
	}
	// The gutter marker is coloured, the label after it is not.
	if !strings.HasPrefix(worse, " \x1b[1;31m!\x1b[0m Resting HR") {
		t.Errorf("red gutter marker, then a plain label: %q", worse)
	}
	if !strings.Contains(got, "\x1b[32m▲ +52% better\x1b[0m") {
		t.Errorf("steps should be green and say better:\n%s", got)
	}
}

func TestOverviewHeatmapFlags(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "full")

	out, _, err := run(t, "--db", path, "overview", "--weeks", "0")
	if err != nil || strings.Contains(out, "CALENDAR") {
		t.Errorf("--weeks 0 should hide the heatmap: %v\n%s", err, out)
	}
	out, _, err = run(t, "--db", path, "overview", "--heatmap", "active_energy", "--weeks", "4")
	if err != nil || !strings.Contains(out, "Active energy per day, 4 weeks") {
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
	fixClock(t, overviewNow)
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

// --heatmap draws exactly the named calendars in the order given; --full adds
// the sleep and workouts versions; a database without them shows neither.
func TestOverviewHeatmapListAndFull(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "import:small")

	out, _, err := run(t, "--db", path, "overview", "--heatmap", "workouts,steps,active_energy", "--weeks", "8")
	if err != nil {
		t.Fatal(err)
	}
	w, s, a := strings.Index(out, "workout minutes per day"), strings.Index(out, "Steps per day"), strings.Index(out, "Active energy per day")
	if w < 0 || s < w || a < s {
		t.Errorf("want workouts, steps, active energy in that order (%d %d %d):\n%s", w, s, a, out)
	}
	if strings.Contains(out, "per night") || strings.Count(out, "Steps per day") != 1 {
		t.Errorf("only the named heatmaps:\n%s", out)
	}

	// Default: one heatmap. --full: also the sleep and workouts ones.
	def, _, _ := run(t, "--db", path, "overview")
	if strings.Count(def, " per day,") != 1 {
		t.Errorf("default shows one heatmap:\n%s", def)
	}
	full, _, _ := run(t, "--db", path, "overview", "--full")
	for _, want := range []string{"workout minutes per day", "Steps per day", "BESTS", "STAT"} {
		if !strings.Contains(full, want) {
			t.Errorf("--full lacks %q", want)
		}
	}
	one, _, _ := run(t, "--db", path, "overview", "--category", "workouts")
	if !strings.Contains(one, "BESTS") || strings.Contains(one, "SLEEP  ") || strings.Contains(one, "DATA") {
		t.Errorf("--category workouts is the full workouts section only:\n%s", one)
	}

	// Steps only: no sleep, no workouts, no empty sections.
	bare, _, _ := run(t, "--db", seedOverview(t, "steps-only"), "overview", "--full")
	if strings.Contains(bare, "SLEEP  ") || strings.Contains(bare, "WORKOUTS ─") || strings.Contains(bare, "WORKOUTS --") {
		t.Errorf("no sleep or workouts section expected:\n%s", bare)
	}
}

func TestOverviewJSONRichSections(t *testing.T) {
	fixClock(t, overviewNow)

	out, _, _ := run(t, "--db", seedOverview(t, "full"), "overview", "--json")
	for _, key := range []string{`"sleep_summary"`, `"workouts_report"`} {
		if !strings.Contains(out, key) {
			t.Errorf("missing %s", key)
		}
	}
	out, _, _ = run(t, "--db", seedOverview(t, "steps-only"), "overview", "--json", "--heatmap", "steps")
	for _, key := range []string{`"sleep_summary"`, `"workouts_report"`} {
		if strings.Contains(out, key) {
			t.Errorf("%s must be omitted when there is no data", key)
		}
	}
	if !strings.Contains(out, `"heatmaps"`) || strings.Contains(out, ": null,\n  \"sleep") {
		t.Errorf("heatmaps map expected:\n%s", out)
	}
}

// Every mode fits the narrowest supported terminal (50 columns) and the
// ones above it: no line wider than the terminal, nothing cut mid-word.
func TestOverviewFitsEveryWidthAndMode(t *testing.T) {
	views := map[string]overviewView{
		"default": {}, "compact": {compact: true}, "full": {full: true},
		"heatmaps": {heatmaps: []string{"workouts", "sleep"}},
		"sleep":    {categories: []string{"sleep"}}, "workouts": {categories: []string{"workouts"}},
	}
	for _, scenario := range []string{"full", "import:small", "sleep-only", "steps-long"} {
		o := overviewReport(t, scenario)
		for name, view := range views {
			for _, width := range []int{50, 60, 80, 100} {
				for _, uni := range []bool{true, false} {
					got := renderOverview(o, termInfo{width: width, unicode: uni}, withDB(view, "demo.db", overviewNow))
					for _, line := range strings.Split(got, "\n") {
						if n := runeLen(line); n > width {
							t.Errorf("%s/%s width %d unicode %v: %d wide: %q", scenario, name, width, uni, n, line)
						}
					}
				}
			}
		}
	}
}

// Absent values are left out of the JSON, never null (the spark arrays are the
// one place where a missing day is a null inside a list).
func TestJSONHasNoNulls(t *testing.T) {
	fixClock(t, overviewNow)
	for _, args := range [][]string{{"overview", "--heatmap", "steps"}, {"heatmap"}, {"heatmap", "workouts"}, {"sleep"}} {
		out, _, err := run(t, append([]string{"--db", seedOverview(t, "full"), "--json"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var doc any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		var bad []string
		jsonNullsAndKeys(doc, "$", map[string]bool{"spark": true}, &bad)
		if len(bad) > 0 {
			t.Errorf("%v: %v", args, bad)
		}
	}
}

// Colour never carries meaning alone: the metric table with its colours
// stripped is the same text as the table without colour. (Heatmaps are left
// out: with colour on, intensity is colour only, by design.)
func TestOverviewColourAddsNoWords(t *testing.T) {
	view := testView
	view.noHeat = true
	for _, scenario := range []string{"full", "stale"} {
		coloured := renderOverview(overviewReport(t, scenario), termInfo{width: 80, unicode: true, color: true}, view)
		plain := renderOverview(overviewReport(t, scenario), termInfo{width: 80, unicode: true}, view)
		if got := stripANSI(coloured); got != plain {
			t.Errorf("%s: stripped colour output differs from the plain output\n--- stripped ---\n%s\n--- plain ---\n%s", scenario, got, plain)
		}
	}
}

// A value outside its reference limit gets a plain dim note under the rows
// of its category; the row itself and its colours are untouched.
func TestRangeNoteUnderTheCategory(t *testing.T) {
	rep := overviewReport(t, "full")
	for i := range rep.Metrics {
		if rep.Metrics[i].Metric == "resting_heart_rate" {
			v := 105.0
			rep.Metrics[i].Value = &v
			rep.Metrics[i].Range = &stats.RangeCheck{High: 100, Source: "test", Position: stats.RangeAbove}
		}
	}
	got := renderOverview(rep, termInfo{width: 80, unicode: true}, testView)
	// The note is wrapped at 80 columns, so look for it with the line breaks removed.
	note := "Resting HR 105 bpm is above the usual adult limit of 100 bpm (general guide, not medical advice)"
	if flat := strings.Join(strings.Fields(got), " "); !strings.Contains(flat, note) {
		t.Fatalf("missing the note %q:\n%s", note, got)
	}
	// The note sits after the last VITALS row and before the next band, on
	// lines of its own that do not start with a margin marker.
	lines := strings.Split(got, "\n")
	noteAt, nextBand := -1, -1
	for i, l := range lines {
		switch {
		case noteAt < 0 && strings.Contains(l, "Resting HR 105 bpm is above"):
			noteAt = i
		case noteAt >= 0 && nextBand < 0 && strings.Contains(l, "ACTIVITY"):
			nextBand = i
		}
	}
	if noteAt < 0 || nextBand < 0 || noteAt > nextBand {
		t.Errorf("note at line %d, next band at %d: the note belongs under the VITALS rows:\n%s", noteAt, nextBand, got)
	}
	if noteAt >= 0 && strings.TrimSpace(lines[noteAt])[0] == '!' {
		t.Errorf("a range note must not use the worse marker: %q", lines[noteAt])
	}

	rep2 := overviewReport(t, "full") // within range: no note
	if strings.Contains(renderOverview(rep2, termInfo{width: 80, unicode: true}, testView), "usual adult") {
		t.Errorf("no note expected when the value is within range")
	}
}

// A value that rounds to its own limit ("95%" below 95%) shows more digits.
func TestRangeNoteShowsDigitsWhenRoundingHidesTheGap(t *testing.T) {
	v := 94.6
	r := stats.MetricRow{Metric: "oxygen_saturation", Label: "Blood oxygen", Unit: "percent", Value: &v,
		Range: &stats.RangeCheck{Low: 95, Position: stats.RangeBelow}}
	want := "Blood oxygen 94.60% is below the usual adult level of 95% (general guide, not medical advice)"
	if got := rangeNote(r, termInfo{unicode: true}); got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	r.Value = new(float64)
	*r.Value = 93
	if got := rangeNote(r, termInfo{unicode: true}); got != "Blood oxygen 93% is below the usual adult level of 95% (general guide, not medical advice)" {
		t.Errorf("plain case: %q", got)
	}
	r.Range.Position = stats.RangeWithin
	if got := rangeNote(r, termInfo{unicode: true}); got != "" {
		t.Errorf("within range gave a note: %q", got)
	}
}

// A metric that is itself a percentage reports its change in points, never a
// relative percent of a small number ("1% to 2%" is not "+100%").
func TestPercentMetricsReportPoints(t *testing.T) {
	g := unicodeGlyphs
	row := func(v, mean float64) stats.MetricRow {
		pct := (v - mean) / mean * 100
		return stats.MetricRow{Unit: "percent", Value: &v, Mean: &mean, DeltaPct: &pct}
	}
	for _, c := range []struct {
		v, mean float64
		want    string
	}{
		{2.6, 1, "▲ +1.6pt"}, {1, 2.6, "▼ -1.6pt"}, {31, 28.6, "▲ +2.4pt"}, {97.2, 97, "▲ +0.2pt"},
		{94, 97, "▼ -3pt"}, {97.02, 97, "– 0pt"},
	} {
		if got := deltaText(row(c.v, c.mean), g); got != c.want {
			t.Errorf("%v vs %v: %q, want %q", c.v, c.mean, got, c.want)
		}
	}
	// Other metrics keep their relative percent.
	v, mean := 110.0, 100.0
	pct := 10.0
	if got := deltaText(stats.MetricRow{Unit: "bpm", Value: &v, Mean: &mean, DeltaPct: &pct}, g); got != "▲ +10%" {
		t.Errorf("bpm: %q", got)
	}
}

// A limit with its own Basis says what it is, and a double support time past
// 40% gets a note that names Apple's range, not "the usual adult limit".
func TestRangeNoteUsesTheBasis(t *testing.T) {
	v := 43.0
	r := stats.MetricRow{Metric: "walking_double_support", Label: "Double support time", Unit: "percent", Value: &v,
		Range: &stats.RangeCheck{High: 40, Basis: "the top of Apple's typical walking range", Position: stats.RangeAbove}}
	want := "Double support time 43.0% is above 40.0%, the top of Apple's typical walking range (general guide, not medical advice)"
	if got := rangeNote(r, termInfo{unicode: true}); got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

// Every workouts figure in the overview uses the same 28-day window: the
// headline count, the table's "active N of 28 d" and workouts_report.
func TestOverviewWorkoutsUseOneWindow(t *testing.T) {
	fixClock(t, overviewNow)

	path := seedOverview(t, "full")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// 29 days back: outside 28 days, inside the old 30-day window.
	start := overviewNow.AddDate(0, 0, -29).Add(-2 * time.Hour).Unix()
	if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, tz_offset, source)
		VALUES ('rowing', ?, ?, 1800, 0, 'Test Apple Watch')`, start, start+1800); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	var o struct {
		Workouts struct {
			Days  int `json:"days"`
			Count int `json:"count"`
		} `json:"workouts"`
		Report struct {
			Days  int `json:"days"`
			Count int `json:"count"`
		} `json:"workouts_report"`
	}
	out, _, err := run(t, "--db", path, "overview", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	if o.Workouts.Days != 28 || o.Report.Days != 28 || o.Workouts.Count != o.Report.Count {
		t.Errorf("workouts %+v and workouts_report %+v must share one 28-day window", o.Workouts, o.Report)
	}
}

// --category narrows the JSON the same way it narrows the text and "today
// --json": only the chosen categories' metrics, and sleep and workouts only
// when asked for.
func TestOverviewJSONHonoursCategory(t *testing.T) {
	fixClock(t, overviewNow)

	path := seedOverview(t, "full")
	decode := func(cat string) map[string]json.RawMessage {
		t.Helper()
		out, _, err := run(t, "--db", path, "overview", "--json", "--category", cat)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	m := decode("vitals")
	var rows []struct{ Category string }
	if err := json.Unmarshal(m["metrics"], &rows); err != nil || len(rows) == 0 {
		t.Fatalf("vitals metrics: %v %s", err, m["metrics"])
	}
	for _, r := range rows {
		if r.Category != "vitals" {
			t.Errorf("--category vitals returned a %s metric", r.Category)
		}
	}
	for _, key := range []string{"sleep", "workouts", "sleep_summary", "workouts_report", "heatmap"} {
		if _, ok := m[key]; ok {
			t.Errorf("--category vitals must leave out %q", key)
		}
	}

	m = decode("sleep,workouts")
	for _, key := range []string{"sleep", "workouts", "sleep_summary", "workouts_report"} {
		if _, ok := m[key]; !ok {
			t.Errorf("--category sleep,workouts must keep %q", key)
		}
	}
	if string(m["metrics"]) != "[]" {
		t.Errorf("--category sleep,workouts has no metric rows, got %s", m["metrics"])
	}
}

// The help names the JSON fields and rules the commands really have.
func TestOverviewAndTodayHelpNameTheNewFields(t *testing.T) {
	for cmd, wants := range map[string][]string{
		"overview": {"range {low, high, source, basis, position", "sleep_nights", "in_range", "only with --heatmap", "sleep_summary.heatmap", "stale rows"},
		"today":    {"range {low, high, source, basis, position", "in_range", "above 100 bpm", "double support (above 40%)", "asymmetry (above 35%)"},
	} {
		out, _, err := run(t, cmd, "--help")
		if err != nil {
			t.Fatal(err)
		}
		flat := strings.Join(strings.Fields(out), " ")
		for _, want := range wants {
			if !strings.Contains(flat, want) {
				t.Errorf("%s --help is missing %q", cmd, want)
			}
		}
	}
}

func TestNetOverviewCommand(t *testing.T) {
	ov := func(extra ...string) []string { return append([]string{"overview"}, extra...) }
	apple := seedScenario(seedOverview, "import:small")
	n := overviewNow
	cases := []netCase{
		{name: "overview_apple_w80", seed: apple, now: n, cols: "80", args: ov()},
		{name: "overview_apple_w50", seed: apple, now: n, cols: "50", args: ov()},
		{name: "overview_apple_w60", seed: apple, now: n, cols: "60", args: ov()},
		{name: "overview_apple_w100", seed: apple, now: n, cols: "100", args: ov()},
		{name: "overview_apple_colour", seed: apple, now: n, cols: "80", force: true, args: ov()},
		{name: "overview_apple_nocolor_flag", seed: apple, now: n, cols: "80", force: true, args: ov("--no-color")},
		{name: "overview_apple_NO_COLOR", seed: apple, now: n, cols: "80", force: true, env: map[string]string{"NO_COLOR": "1"}, args: ov()},
		{name: "overview_apple_ascii_flag", seed: apple, now: n, cols: "80", args: ov("--ascii")},
		{name: "overview_apple_compact", seed: apple, now: n, cols: "80", args: ov("--compact")},
		{name: "overview_apple_full_w50", seed: apple, now: n, cols: "50", args: ov("--full")},
		{name: "overview_apple_full_w100", seed: apple, now: n, cols: "100", args: ov("--full")},
		{name: "overview_apple_no_heatmaps", seed: apple, now: n, cols: "80", args: ov("--weeks", "0")},
		{name: "overview_apple_weeks4", seed: apple, now: n, cols: "60", args: ov("--weeks", "4")},
		{name: "overview_apple_heatmap_sleep", seed: apple, now: n, cols: "80", args: ov("--heatmap", "sleep")},
		{name: "overview_apple_heatmaps_three", seed: apple, now: n, cols: "80", args: ov("--heatmap", "workouts,sleep,step_count", "--category", "vitals")},
		{name: "overview_apple_category_sleep", seed: apple, now: n, cols: "80", args: ov("--category", "sleep")},
		{name: "overview_apple_category_workouts", seed: apple, now: n, cols: "80", args: ov("--category", "workouts")},
		{name: "overview_apple_category_body_mobility", seed: apple, now: n, cols: "80", args: ov("--category", "body,mobility")},
		{name: "overview_apple_json", seed: apple, now: n, args: ov("--json")},
		{name: "overview_apple_category_json", seed: apple, now: n, args: ov("--json", "--category", "sleep,workouts")},
		{name: "overview_sparse_w80", seed: seedScenario(seedOverview, "import:sparse"), now: n, cols: "80", args: ov()},
		{name: "overview_sparse_json", seed: seedScenario(seedOverview, "import:sparse"), now: n, args: ov("--json")},
		{name: "overview_full_scenario_w80", seed: seedScenario(seedOverview, "full"), now: n, cols: "80", args: ov()},
		{name: "overview_stale_w80", seed: seedScenario(seedOverview, "stale"), now: n, cols: "80", args: ov()},
		{name: "overview_stale_json", seed: seedScenario(seedOverview, "stale"), now: n, args: ov("--json")},
		{name: "overview_steps_only_w60", seed: seedScenario(seedOverview, "steps-only"), now: n, cols: "60", args: ov()},
		{name: "overview_sleep_only_json", seed: seedScenario(seedOverview, "sleep-only"), now: n, args: ov("--json")},
		{name: "overview_empty_w80", seed: emptyDB, now: n, cols: "80", args: ov()},
		{name: "overview_empty_json", seed: emptyDB, now: n, args: ov("--json")},
		{name: "overview_err_category", seed: emptyDB, now: n, args: ov("--category", "bogus")},
		{name: "overview_err_weeks", seed: emptyDB, now: n, args: ov("--weeks", "99")},
		{name: "overview_err_heatmap", seed: apple, now: n, args: ov("--heatmap", "bogus")},
		{name: "overview_err_arg", seed: emptyDB, now: n, args: ov("today")},
		{name: "overview_err_no_db", now: n, args: []string{"--db", "/nonexistent-dir/none.db", "overview"}},
	}
	runNetCases(t, cases)
}

// (l) --category scopes the headline, the legend and the empty headers.
func TestCategoryScopesHeadlineAndLegend(t *testing.T) {
	all := overviewText(t, "import:small", uni80, overviewView{})
	vitals := overviewText(t, "import:small", uni80, overviewView{categories: []string{"vitals"}})
	if !strings.Contains(all, "3 better, 1 worse") || strings.Contains(vitals, "3 better, 1 worse") {
		t.Errorf("headline must count only the shown categories:\n%s", vitals)
	}
	if strings.Contains(vitals, "slept") || strings.Contains(vitals, "Walking asymmetry") {
		t.Errorf("sleep and other categories leaked into --category vitals:\n%s", vitals)
	}
	for _, cat := range []string{"sleep", "workouts"} {
		out := overviewText(t, "import:small", uni80, overviewView{categories: []string{cat}})
		// (The sleep table has a legend of its own; the metric one must be gone.)
		for _, bad := range []string{"METRIC", "better,", "typical vs", "LATEST"} {
			if strings.Contains(out, bad) {
				t.Errorf("--category %s must print no metric header, legend or metric headline (%q):\n%s", cat, bad, out)
			}
		}
	}
	// A category with nothing in it prints no header, legend or headline.
	none := overviewText(t, "steps-only", uni80, overviewView{categories: []string{"body"}})
	for _, bad := range []string{"METRIC", "green better", "typical", "better"} {
		if strings.Contains(none, bad) {
			t.Errorf("empty --category body printed %q:\n%s", bad, none)
		}
	}
}

// (o) one band style for every section; the legend words are the same
// everywhere.
func TestOneBandStyle(t *testing.T) {
	for _, c := range []struct {
		ti   termInfo
		band string
	}{{uni80, "━━ "}, {ascii80, "== "}} {
		got := overviewText(t, "import:small", c.ti, overviewView{full: true})
		for _, title := range []string{"SLEEP", "WORKOUTS", "CALENDAR", "DATA", "VITALS"} {
			if !strings.Contains(got, " "+c.band+title+" ") {
				t.Errorf("%s section should be a band (%q%s):\n%s", title, c.band, title, got)
			}
		}
		if strings.Count(got, "~ higher/lower, no good direction") != 2 {
			t.Errorf("both legends should say '~ higher/lower, no good direction':\n%s", got)
		}
		if strings.Contains(got, "cyan") || strings.Contains(got, "cyan neutral") || strings.Contains(got, "per night") {
			t.Errorf("old wording left in:\n%s", got)
		}
	}
}

// (p) --compact keeps a one-character flag in the margin.
func TestCompactKeepsOneCharacterFlag(t *testing.T) {
	got := overviewText(t, "import:small", uni80, overviewView{compact: true})
	if !strings.Contains(got, "\n! Resting HR") || !strings.Contains(got, "\n+ Walking heart rate") || !strings.Contains(got, "\n~ Respiratory rate") {
		t.Errorf("compact rows need the ! + ~ flag:\n%s", got)
	}
}

// Summary lines drop whole items and say "+N more"; they never end mid-word.
func TestSummaryLinesDropWholeItems(t *testing.T) {
	counts := map[string]int64{stats.ClassWatch: 64, stats.ClassIPhone: 22, stats.ClassManual: 1, stats.ClassOther: 13}
	got := listFit("sources: ", sourceParts(counts), 0, 48)
	if got != "sources: Watch 64%, iPhone 22%, +2 more" {
		t.Errorf("sources: %q", got)
	}
	gaps := []stats.Gap{{Label: "Weight", Missing: 10, Span: 16}, {Label: "Body fat", Missing: 8, Span: 13}, {Label: "BMI", Missing: 8, Span: 13}, {Label: "HRV", Missing: 1, Span: 28}}
	if got := gapsLine(gaps, 48); got != "days missing: Weight 10 of 16, +3 more" {
		t.Errorf("gaps narrow: %q", got)
	}
	if got := gapsLine(gaps, 100); got != "days missing: Weight 10 of 16, Body fat 8 of 13, BMI 8 of 13, +1 more" {
		t.Errorf("gaps wide: %q", got)
	}
}

func TestNotEnoughHistoryHeadlineIsNotCut(t *testing.T) {
	t.Setenv("COLUMNS", "50")
	fixClock(t, time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	// One weigh-in: a fresh value with too little history for a baseline.
	path := tempDB(t)
	if _, _, err := run(t, "--db", path, "log", "weight", "72", "--at", "2026-11-08 08:00"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "--db", path, "overview")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Not enough history") {
		t.Fatalf("no headline in:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Not enough history") && (strings.HasSuffix(line, "...") || len([]rune(line)) > 50) {
			t.Errorf("headline line: %q", line)
		}
	}
}
