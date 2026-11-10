package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

// seedHeat writes step counts for one scenario and returns the database path:
//
//	full    every day for 20 weeks, a repeating pattern of very different days
//	sparse  about one day in three, so gaps are everywhere
//	flat    the same value every day
//	empty   no steps (one weight reading, so the database is not blank)
func seedHeat(t *testing.T, scenario string) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	add := func(metric string, d time.Time, value float64) {
		ts := d.Add(12 * time.Hour).Unix()
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, '', 0, 'Test Apple Watch')`, metric, ts, ts+60, value); err != nil {
			t.Fatal(err)
		}
	}
	midnight := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	if scenario == "empty" {
		add("body_mass", midnight, 72.4)
		return path
	}
	for back := 0; back < 20*7; back++ {
		switch scenario {
		case "sleep": // a night every day but each 6th, 5h to 9h, as "asleep" only
			if back%6 != 5 {
				mins := 300 + (back*53)%241
				end := midnight.AddDate(0, 0, -back).Add(7 * time.Hour).Unix()
				if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
					VALUES ('asleep_unspecified', ?, ?, 0, 'Test Apple Watch')`, end-int64(mins)*60, end); err != nil {
					t.Fatal(err)
				}
			}
			continue
		case "workouts": // from 8 weeks back, a session on about every third day
			if back < 56 && back%3 != 1 {
				continue
			}
			if back < 56 {
				start := midnight.AddDate(0, 0, -back).Add(18 * time.Hour).Unix()
				dur := int64(20+(back*7)%70) * 60
				if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, tz_offset, source)
					VALUES ('running', ?, ?, ?, 0, 'Test Apple Watch')`, start, start+dur, dur); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		d := midnight.AddDate(0, 0, -back)
		switch scenario {
		case "full":
			add("step_count", d, 3000+float64((back*37)%17)*650)
		case "sparse":
			if back%3 == 0 || back == 1 {
				add("step_count", d, 4000+float64((back*13)%11)*800)
			}
		case "flat":
			add("step_count", d, 8000)
		}
	}
	return path
}

func heatLines(t *testing.T, scenario string, weeks int, ti termInfo) string {
	return heatLinesFor(t, scenario, "step_count", weeks, ti)
}

func heatLinesFor(t *testing.T, scenario, name string, weeks int, ti termInfo) string {
	t.Helper()
	conn, err := db.Open(seedHeat(t, scenario))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	h, err := buildNamedHeatmap(context.Background(), conn, name, min(weeks, heatWeeksFit(ti.width)), heatNow)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(renderHeatmap(h, ti), "\n") + "\n"
}

func TestHeatmapGoldens(t *testing.T) {
	cases := []struct {
		name     string
		scenario string
		weeks    int
		ti       termInfo
	}{
		{"heatmap_full_80.txt", "full", 12, termInfo{width: 80, unicode: true}},
		{"heatmap_full_60.txt", "full", 52, termInfo{width: 60, unicode: true}},
		{"heatmap_full_ascii.txt", "full", 12, termInfo{width: 80}},
		{"heatmap_full_color.txt", "full", 12, termInfo{width: 80, unicode: true, color: true}},
		{"heatmap_sparse.txt", "sparse", 12, termInfo{width: 80, unicode: true}},
		{"heatmap_flat.txt", "flat", 12, termInfo{width: 80, unicode: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := heatLines(t, c.scenario, c.weeks, c.ti)
			checkViewGolden(t, c.name, got)
			for _, line := range strings.Split(got, "\n") {
				if w := runeLen(stripANSI(line)); w > c.ti.width {
					t.Errorf("line is %d columns, terminal is %d: %q", w, c.ti.width, line)
				}
			}
			if !c.ti.unicode && strings.ContainsFunc(got, func(r rune) bool { return r > 127 }) {
				t.Error("ASCII output has non-ASCII characters")
			}
			if !c.ti.color && strings.Contains(got, "\x1b") {
				t.Error("plain output has escape codes")
			}
		})
	}
}

func TestHeatmapSleepAndWorkoutsGoldens(t *testing.T) {
	cases := []struct{ file, scenario, name string }{
		{"heatmap_sleep.txt", "sleep", "sleep"},
		{"heatmap_workouts.txt", "workouts", "workouts"},
	}
	for _, c := range cases {
		got := heatLinesFor(t, c.scenario, c.name, 12, termInfo{width: 80, unicode: true})
		checkViewGolden(t, c.file, got)
	}
	// ASCII: rest days are dots, no gap underscores inside the filled span.
	got := heatLinesFor(t, "workouts", "workouts", 12, termInfo{width: 80})
	if !strings.Contains(got, "rest day") || strings.ContainsFunc(got, func(r rune) bool { return r > 127 }) {
		t.Errorf("ascii workouts:\n%s", got)
	}
	checkViewGolden(t, "heatmap_workouts_ascii.txt", got)
}

func TestHeatmapCommandSleepWorkouts(t *testing.T) {
	fixClock(t, heatNow)

	// No data: friendly line, exit 0.
	for name, want := range map[string]string{
		"sleep":    "No sleep data in the last 12 weeks yet.",
		"workouts": "No workouts in the last 12 weeks yet.",
	} {
		out, _, err := run(t, "--db", seedHeat(t, "empty"), "heatmap", name)
		if err != nil || !strings.HasPrefix(out, want) {
			t.Errorf("%s empty: %q, %v", name, out, err)
		}
	}
	out, _, err := run(t, "--db", seedHeat(t, "workouts"), "heatmap", "workouts", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var h stats.Heatmap
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatal(err)
	}
	if h.Metric != "workouts" || len(h.Thresholds) != 4 || h.DaysWithData == 0 {
		t.Errorf("json = %+v", h)
	}
	_, _, err = run(t, "--db", seedHeat(t, "full"), "heatmap", "nope")
	if err == nil || !strings.Contains(err.Error(), "sleep, workouts") {
		t.Errorf("unknown name should also list sleep and workouts: %v", err)
	}
}

func TestHeatmapCommand(t *testing.T) {
	fixClock(t, heatNow)

	// Empty data: one friendly line, no error.
	out, _, err := run(t, "--db", seedHeat(t, "empty"), "heatmap")
	if err != nil || !strings.HasPrefix(out, "No step count data") || strings.Count(out, "\n") != 1 {
		t.Errorf("empty: %q, %v", out, err)
	}
	checkViewGolden(t, "heatmap_empty.txt", out)

	// A pipe gets plain ASCII; the default metric is steps.
	out, _, err = run(t, "--db", seedHeat(t, "full"), "heatmap")
	if err != nil || strings.Contains(out, "\x1b") || strings.ContainsFunc(out, func(r rune) bool { return r > 127 }) {
		t.Errorf("pipe output: %q, %v", out, err)
	}
	if !strings.Contains(out, "steps per day") || !strings.Contains(out, "< today") {
		t.Errorf("missing legend:\n%s", out)
	}

	// Unknown metric: the usual friendly list.
	_, _, err = run(t, "--db", seedHeat(t, "full"), "heatmap", "nope")
	if err == nil || !strings.Contains(err.Error(), "known metrics") {
		t.Errorf("unknown metric error = %v", err)
	}
	if _, _, err = run(t, "--db", seedHeat(t, "full"), "heatmap", "--weeks", "0"); err == nil {
		t.Error("--weeks 0 should fail")
	}

	// JSON is the grid, with all requested weeks.
	out, _, err = run(t, "--db", seedHeat(t, "sparse"), "heatmap", "steps", "--weeks", "3", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var h stats.Heatmap
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Weeks) != 3 || len(h.Thresholds) != 4 || h.Metric != "step_count" {
		t.Errorf("json = %+v", h)
	}
}

// Each kind of heatmap has its own hue, and sleep stages follow the iOS colours.
func TestHeatmapAndStageColours(t *testing.T) {
	cases := []struct {
		hue  string
		want [stats.HeatLevels]string
	}{
		{stats.HueGreen, heatGreen},
		{stats.HueBlue, heatBlue},
		{stats.HueRed, heatRed},
		{stats.HueCyan, heatCyan},
		{stats.HuePurple, heatPurple},
		{stats.HueViolet, heatViolet},
		{stats.HueOrange, heatOrange},
		{"", heatOrange},
	}
	for _, c := range cases {
		if got := heatColorsFor(&stats.Heatmap{Hue: c.hue}); got != c.want {
			t.Errorf("hue %q: got %v, want %v", c.hue, got, c.want)
		}
	}
	st := style{color: true}
	for stage, code := range map[string]string{"awake": "38;5;208", "rem": "38;5;117", "core": "38;5;33", "deep": "38;5;21"} {
		if got := st.stage(stage, "x"); got != "\x1b["+code+"mx\x1b[0m" {
			t.Errorf("stage %s: got %q", stage, got)
		}
	}
	if got := (style{}).stage("awake", "x"); got != "x" {
		t.Errorf("colour off must stay plain, got %q", got)
	}
}

// With colour on every level is the same block (the colour is the signal);
// without colour the five heights come back.
func TestHeatmapShadeUsesOneBlockWhenColoured(t *testing.T) {
	for _, unicode := range []bool{true, false} {
		g := glyphsFor(unicode)
		plain := map[rune]bool{}
		for level := 1; level <= stats.HeatLevels; level++ {
			if got := g.shade(level, true); got != g.block {
				t.Errorf("unicode=%v level %d coloured: got %q, want %q", unicode, level, got, g.block)
			}
			plain[g.shade(level, false)] = true
		}
		if len(plain) != stats.HeatLevels {
			t.Errorf("unicode=%v: uncoloured shades must be %d distinct glyphs, got %d", unicode, stats.HeatLevels, len(plain))
		}
	}
}

// Few days: the legend lists only levels a day can have, and the by-type map
// only advertises UPPERCASE when a day is that long.
func TestHeatmapLegendWithFewDays(t *testing.T) {
	ti := termInfo{width: 80, unicode: true}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	spec := stats.HeatSpec{Title: "workouts", Format: minutesText, ZeroIsRest: true}
	legendOf := func(spec stats.HeatSpec, byDay map[string]float64) string {
		h, err := stats.HeatmapFromDays(spec, now, 2, byDay)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(renderHeatmap(h, ti), "\n")
	}

	// Four days: the 60 minute day is level 5 and the legend says so.
	got := legendOf(spec, map[string]float64{"2026-11-02": 15, "2026-11-04": 30, "2026-11-06": 45, "2026-11-08": 60})
	if !strings.Contains(got, "█ >") {
		t.Errorf("legend should list the top level:\n%s", got)
	}

	// Two days: only levels 1 and 5 exist, so only one ≤ bound is listed.
	got = legendOf(spec, map[string]float64{"2026-11-02": 20, "2026-11-04": 40})
	if n := strings.Count(got, "≤"); n != 1 {
		t.Errorf("two days should list one ≤ bound, got %d:\n%s", n, got)
	}

	// Efficiency bounds that round to the same text are listed once.
	pct := stats.HeatSpec{Title: "sleep efficiency", Format: func(v float64) string { return fmt.Sprintf("%.0f%%", v) }}
	got = legendOf(pct, map[string]float64{
		"2026-11-01": 94.6, "2026-11-02": 94.8, "2026-11-03": 95.0, "2026-11-04": 95.2, "2026-11-05": 95.4, "2026-11-06": 99,
	})
	if n := strings.Count(got, "≤95%"); n > 1 {
		t.Errorf("≤95%% listed %d times:\n%s", n, got)
	}

	// By type: one lone day is level 1 or 5; with two days of 20 and 40
	// minutes the longer one is uppercase and the key says so, a flat
	// single-level week never does.
	r := &stats.WorkoutsReport{
		TopTypeByDay: map[string]string{"2026-11-02": "running", "2026-11-04": "running"},
		ByType:       []stats.TypeTotal{{Activity: "running"}},
	}
	h, _ := stats.HeatmapFromDays(spec, now, 2, map[string]float64{"2026-11-02": 20, "2026-11-04": 40})
	if got := strings.Join(renderTypeHeatmap(h, r, ti), "\n"); !strings.Contains(got, "UPPERCASE") {
		t.Errorf("a long day exists, key should explain UPPERCASE:\n%s", got)
	}
	h, _ = stats.HeatmapFromDays(spec, now, 2, map[string]float64{"2026-11-02": 20})
	if got := strings.Join(renderTypeHeatmap(h, r, ti), "\n"); strings.Contains(got, "UPPERCASE") {
		t.Errorf("no uppercase day, key should not mention it:\n%s", got)
	}
}

// seedHues has 13 weeks of one reading a day for a metric of every hue
// family of the heatmaps (steps are orange, workouts green, sleep blue).
func seedHues(t *testing.T) string {
	path := seedHeat(t, "empty")
	for i, m := range []string{"step_count", "heart_rate", "respiratory_rate", "apple_sleeping_wrist_temperature", "body_mass"} {
		values := make([]float64, 90)
		for d := range values {
			values[d] = 40 + float64((d*(7+i)+i)%23)*(1+float64(i))
		}
		seedDays(t, path, m, "2026-08-06", values...)
	}
	return path
}

func TestNetHeatmapCommand(t *testing.T) {
	hm := func(extra ...string) []string { return append([]string{"heatmap"}, extra...) }
	cases := []netCase{
		{name: "heatmap_steps_w80", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "80", args: hm()},
		{name: "heatmap_steps_w50", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "50", args: hm("--weeks", "52")},
		{name: "heatmap_steps_w100_weeks20", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "100", args: hm("--weeks", "20")},
		{name: "heatmap_steps_ascii_flag", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "80", args: hm("--ascii")},
		{name: "heatmap_steps_colour", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "80", force: true, args: hm()},
		{name: "heatmap_steps_nocolor_flag", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "80", force: true, args: hm("--no-color")},
		{name: "heatmap_steps_NO_COLOR", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "80", force: true, env: map[string]string{"NO_COLOR": "1"}, args: hm()},
		{name: "heatmap_steps_one_week", seed: seedScenario(seedHeat, "full"), now: netHeatClock, cols: "80", args: hm("--weeks", "1")},
		{name: "heatmap_steps_json", seed: seedScenario(seedHeat, "full"), now: netHeatClock, args: hm("--weeks", "3", "--json")},
		{name: "heatmap_sparse_w80", seed: seedScenario(seedHeat, "sparse"), now: netHeatClock, cols: "80", args: hm()},
		{name: "heatmap_sparse_json", seed: seedScenario(seedHeat, "sparse"), now: netHeatClock, args: hm("--weeks", "3", "--json")},
		{name: "heatmap_flat_json", seed: seedScenario(seedHeat, "flat"), now: netHeatClock, args: hm("--weeks", "2", "--json")},
		{name: "heatmap_empty_w80", seed: seedScenario(seedHeat, "empty"), now: netHeatClock, cols: "80", args: hm()},
		{name: "heatmap_empty_json", seed: seedScenario(seedHeat, "empty"), now: netHeatClock, args: hm("--json")},
		{name: "heatmap_sleep_w80", seed: seedScenario(seedHeat, "sleep"), now: netHeatClock, cols: "80", args: hm("sleep")},
		{name: "heatmap_sleep_w50", seed: seedScenario(seedHeat, "sleep"), now: netHeatClock, cols: "50", args: hm("sleep")},
		{name: "heatmap_sleep_colour", seed: seedScenario(seedHeat, "sleep"), now: netHeatClock, cols: "80", force: true, args: hm("sleep")},
		{name: "heatmap_sleep_json", seed: seedScenario(seedHeat, "sleep"), now: netHeatClock, args: hm("sleep", "--weeks", "3", "--json")},
		{name: "heatmap_workouts_w80", seed: seedScenario(seedHeat, "workouts"), now: netHeatClock, cols: "80", args: hm("workouts")},
		{name: "heatmap_workouts_w50", seed: seedScenario(seedHeat, "workouts"), now: netHeatClock, cols: "50", args: hm("workouts")},
		{name: "heatmap_workouts_colour", seed: seedScenario(seedHeat, "workouts"), now: netHeatClock, cols: "80", force: true, args: hm("workouts")},
		{name: "heatmap_workouts_json", seed: seedScenario(seedHeat, "workouts"), now: netHeatClock, args: hm("workouts", "--weeks", "3", "--json")},
		{name: "heatmap_workouts_empty", seed: seedScenario(seedHeat, "empty"), now: netHeatClock, cols: "80", args: hm("workouts")},
		{name: "heatmap_sleep_empty", seed: seedScenario(seedHeat, "empty"), now: netHeatClock, cols: "80", args: hm("sleep")},
		{name: "heatmap_err_unknown_metric", seed: emptyDB, now: netHeatClock, args: hm("bogus")},
		{name: "heatmap_err_weeks0", seed: emptyDB, now: netHeatClock, args: hm("--weeks", "0")},
		{name: "heatmap_err_weeks54", seed: emptyDB, now: netHeatClock, args: hm("--weeks", "54")},
		{name: "heatmap_err_two_metrics", seed: emptyDB, now: netHeatClock, args: hm("steps", "sleep")},
		{name: "heatmap_err_no_db", now: netHeatClock, args: []string{"--db", "/nonexistent-dir/none.db", "heatmap"}},
	}
	// Each hue family, with colour on: the legend and the cells carry the hue.
	for _, m := range []string{"steps", "heart_rate", "respiratory_rate", "apple_sleeping_wrist_temperature", "body_mass"} {
		cases = append(cases,
			netCase{name: "heatmap_hue_" + m + "_colour", seed: seedHues, now: netHeatClock, cols: "80", force: true, args: hm(m, "--weeks", "8")},
			netCase{name: "heatmap_hue_" + m + "_json", seed: seedHues, now: netHeatClock, args: hm(m, "--weeks", "2", "--json")})
	}
	runNetCases(t, cases)
}

// Unicode screens, which the command cannot reach: every hue family in
// colour, plus narrow and ASCII variants.
func TestNetHeatmapRenderUnicode(t *testing.T) {
	hues := seedHues(t)
	heat := seedHeat(t, "full")
	var b strings.Builder
	for _, c := range []struct {
		path, metric string
		weeks        int
		ti           termInfo
	}{
		{heat, "step_count", 12, termInfo{width: 50, unicode: true}},
		{heat, "step_count", 12, termInfo{width: 100, unicode: true}},
		{heat, "step_count", 30, termInfo{width: 100, unicode: true, color: true}},
		{hues, "step_count", 8, termInfo{width: 80, unicode: true, color: true}},
		{hues, "heart_rate", 8, termInfo{width: 80, unicode: true, color: true}},
		{hues, "respiratory_rate", 8, termInfo{width: 80, unicode: true, color: true}},
		{hues, "apple_sleeping_wrist_temperature", 8, termInfo{width: 80, unicode: true, color: true}},
		{hues, "body_mass", 8, termInfo{width: 80, unicode: true, color: true}},
		{seedHeat(t, "sleep"), "sleep", 12, termInfo{width: 80, unicode: true, color: true}},
		{seedHeat(t, "sleep"), "sleep", 12, termInfo{width: 50, unicode: true}},
		{seedHeat(t, "workouts"), "workouts", 12, termInfo{width: 80, unicode: true, color: true}},
		{seedHeat(t, "workouts"), "workouts", 12, termInfo{width: 50}},
	} {
		conn := openTestDB(t, c.path)
		h, err := buildNamedHeatmap(t.Context(), conn, c.metric, min(c.weeks, heatWeeksFit(c.ti.width)), heatNow)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString("## " + c.metric + " " + termName(c.ti) + "\n" + strings.Join(renderHeatmap(h, c.ti), "\n") + "\n\n")
	}
	checkViewGolden(t, "net_heatmap_unicode.txt", b.String())
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

// (g) ASCII: a rest day and the lowest level must be different glyphs.
func TestASCIIRestDayDiffersFromLowestLevel(t *testing.T) {
	g := glyphsFor(false)
	if restGlyph(g) == g.shade(1, false) || restGlyph(g) == g.gap {
		t.Errorf("rest %q, lowest %q, gap %q must all differ", restGlyph(g), g.shade(1, false), g.gap)
	}
}

// A heatmap legend that is too wide wraps onto a second line instead of
// being cut or overflowing.
func TestHeatmapLegendWraps(t *testing.T) {
	h := overviewReport(t, "import:small").Heatmap
	for _, width := range []int{50, 60} {
		lines := renderHeatmap(h, termInfo{width: width, unicode: true})
		for _, l := range lines {
			if runeLen(stripANSI(l)) > width {
				t.Errorf("width %d: %q", width, l)
			}
		}
		if text := strings.Join(lines, "\n"); !strings.Contains(text, "today") || strings.Contains(text, "…") {
			t.Errorf("legend lost or cut:\n%s", text)
		}
	}
}
