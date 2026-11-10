package cli

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

func renderSleepFor(t *testing.T, scenario string, ti termInfo, width int, full bool, by string) []string {
	t.Helper()
	conn, err := db.Open(seedSleep(t, scenario))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r, err := stats.BuildSleepReport(context.Background(), conn, heatNow, stats.SleepOptions{Weeks: min(12, heatWeeksFit(width-1)), By: by})
	if err != nil {
		t.Fatal(err)
	}
	return renderSleepSection(r, ti, width, sleepOpts{Full: full})
}

func TestSleepSectionGoldens(t *testing.T) {
	uni := termInfo{width: 80, unicode: true}
	cases := []struct {
		file     string
		scenario string
		ti       termInfo
		width    int
		full     bool
		by       string
	}{
		{"sleep_rich_80.txt", "rich", uni, 80, true, ""},
		{"sleep_rich_compact_80.txt", "rich", uni, 80, false, ""},
		{"sleep_rich_60.txt", "rich", termInfo{width: 60, unicode: true}, 60, true, ""},
		{"sleep_rich_compact_60.txt", "rich", termInfo{width: 60, unicode: true}, 60, false, ""},
		{"sleep_rich_ascii.txt", "rich", termInfo{width: 80}, 80, true, ""},
		{"sleep_rich_ascii_60.txt", "rich", termInfo{width: 60}, 60, true, ""},
		{"sleep_rich_color.txt", "rich", termInfo{width: 80, unicode: true, color: true}, 80, true, ""},
		{"sleep_rich_bedtime.txt", "rich", uni, 80, true, "bedtime"},
		{"sleep_rich_efficiency.txt", "rich", uni, 80, true, "efficiency"},
		{"sleep_unspecified_80.txt", "unspecified", uni, 80, true, ""},
		{"sleep_unspecified_compact_ascii_60.txt", "unspecified", termInfo{width: 60}, 60, false, ""},
		{"sleep_inbed_80.txt", "inbed", uni, 80, true, ""},
		{"sleep_sparse_80.txt", "sparse", uni, 80, true, ""},
		{"sleep_empty.txt", "empty", uni, 80, true, ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			lines := renderSleepFor(t, c.scenario, c.ti, c.width, c.full, c.by)
			got := strings.Join(lines, "\n") + "\n"
			if len(lines) == 0 {
				got = "" // silently nothing
			}
			checkViewGolden(t, c.file, got)
			for _, line := range lines {
				if w := runeLen(stripANSI(line)); w > c.width {
					t.Errorf("line is %d columns, limit %d: %q", w, c.width, line)
				}
			}
			if !c.ti.unicode && strings.ContainsFunc(got, func(r rune) bool { return r > 127 }) {
				t.Error("ASCII output has non-ASCII characters")
			}
			if !c.ti.color && strings.Contains(got, "\x1b") {
				t.Error("plain output has escape codes")
			}
			if strings.Contains(got, "n/a") || strings.Contains(got, "0m  ") && c.scenario == "unspecified" {
				t.Errorf("missing data must not print n/a or zero rows:\n%s", got)
			}
		})
	}
}

func TestSleepNarrowFallback(t *testing.T) {
	lines := renderSleepFor(t, "rich", termInfo{width: 44, unicode: true}, 44, true, "")
	if len(lines) > 5 {
		t.Errorf("narrow section should be a bar and a few numbers:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "asleep") {
		t.Errorf("no headline numbers: %v", lines)
	}
}

func TestSleepCommand(t *testing.T) {
	fixClock(t, heatNow)

	out, _, err := run(t, "--db", seedSleep(t, "empty"), "sleep")
	if err != nil || !strings.HasPrefix(out, "No sleep data") {
		t.Errorf("empty: %q, %v", out, err)
	}

	out, _, err = run(t, "--db", seedSleep(t, "rich"), "sleep", "--sleep-target", "7h30m", "--nights", "14")
	if err != nil || !strings.Contains(out, "vs 7h 30m target") || strings.Contains(out, "\x1b") {
		t.Errorf("rich: %q, %v", out, err)
	}
	if !strings.Contains(out, "time asleep per day") {
		t.Errorf("rich output has no calendar:\n%s", out)
	}

	// --weeks 0 hides the calendar.
	out, _, err = run(t, "--db", seedSleep(t, "rich"), "sleep", "--weeks", "0")
	if err != nil || strings.Contains(out, "per day") {
		t.Errorf("--weeks 0: %q, %v", out, err)
	}

	out, _, err = run(t, "--db", seedSleep(t, "rich"), "sleep", "--json", "--by", "bedtime")
	if err != nil {
		t.Fatal(err)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"night", "seven_nights", "usual", "regularity", "debt", "heatmap"} {
		if rep[key] == nil {
			t.Errorf("json lacks %q", key)
		}
	}
	if rep["heatmap"].(map[string]any)["metric"] != "sleep_bedtime" {
		t.Errorf("heatmap = %v", rep["heatmap"])
	}

	// In-bed only: no sleep numbers anywhere.
	out, _, err = run(t, "--db", seedSleep(t, "inbed"), "sleep", "--json")
	if err != nil || strings.Contains(out, "efficiency") || strings.Contains(out, "\"debt\"") || strings.Contains(out, "null") {
		t.Errorf("inbed json: %q, %v", out, err)
	}

	for _, args := range [][]string{{"--by", "nope"}, {"--nights", "3"}, {"--sleep-target", "soon"}, {"--sleep-target", "1h"}, {"--weeks", "99"}} {
		if _, _, err := run(t, append([]string{"--db", seedSleep(t, "rich"), "sleep"}, args...)...); err == nil {
			t.Errorf("%v should fail", args)
		}
	}
}

// A 6-minute awakening inside 8 hours at 40 columns (12 minutes a column)
// must still show: awake wins any column it touches.
func TestHypnogramAwakeWins(t *testing.T) {
	const h = int64(3600)
	n := stats.SleepNight{Start: 0, End: 8 * h, Segments: []stats.Seg{
		{Stage: "core", Start: 0, End: 4 * h},
		{Stage: "awake", Start: 4 * h, End: 4*h + 360},
		{Stage: "core", Start: 4*h + 360, End: 8 * h},
	}}
	lines := hypnogram(n, 40, unicodeGlyphs, style{})
	if got := strings.Count(lines[1], "░"); got != 1 {
		t.Errorf("awake row has %d cells, want 1:\n%s", got, strings.Join(lines, "\n"))
	}
	if strings.Count(lines[3], "▒") < 38 {
		t.Errorf("core row lost cells:\n%s", strings.Join(lines, "\n"))
	}
}

// Two awakenings five minutes apart must not run into one block.
func TestHypnogramKeepsAwakeningsApart(t *testing.T) {
	const h, m = int64(3600), int64(60)
	n := stats.SleepNight{Start: 0, End: 8 * h, Segments: []stats.Seg{
		{Stage: "core", Start: 0, End: 4 * h},
		{Stage: "awake", Start: 4 * h, End: 4*h + 20*m},
		{Stage: "core", Start: 4*h + 20*m, End: 4*h + 25*m},
		{Stage: "awake", Start: 4*h + 25*m, End: 4*h + 45*m},
		{Stage: "core", Start: 4*h + 45*m, End: 8 * h},
	}}
	lines := hypnogram(n, 40, unicodeGlyphs, style{})
	runs := 0
	for i, r := range []rune(strings.TrimPrefix(lines[1], sleepIndent+"awake ")) {
		if r == '░' && (i == 0 || []rune(strings.TrimPrefix(lines[1], sleepIndent+"awake "))[i-1] != '░') {
			runs++
		}
	}
	if runs != 2 {
		t.Errorf("awake row has %d blocks, want 2:\n%s", runs, strings.Join(lines, "\n"))
	}
}

// The unspecified bar must be visible in ASCII and must not look like the
// rule under the header (it used to be the same dash, a "duplicate rule").
func TestSleepUnspecifiedBarIsNotARule(t *testing.T) {
	for _, width := range []int{60, 80} {
		lines := renderSleepFor(t, "unspecified", termInfo{width: width}, width, false, "")
		if len(lines) < 3 {
			t.Fatalf("too short: %v", lines)
		}
		rule, bar := strings.TrimSpace(lines[1]), strings.TrimSpace(lines[2])
		if bar == "" || bar == rule || strings.Trim(bar, "-") == "" {
			t.Errorf("width %d: line under the rule must be the bar, got %q (rule %q)", width, bar, rule)
		}
	}
}

// Last night has no stages but earlier nights do: a dim dash, not a blank.
func TestSleepLastNightDashWhenStagesMissing(t *testing.T) {
	path := seedSleep(t, "rich")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	midnight := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC).Unix()
	if _, err := conn.Exec(`DELETE FROM sleep_stages WHERE end_ts >= ?`, midnight); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES ('asleep_unspecified', ?, ?, 0, 'Test iPhone')`, midnight-3600, midnight+6*3600); err != nil {
		t.Fatal(err)
	}
	r, err := stats.BuildSleepReport(context.Background(), conn, heatNow, stats.SleepOptions{Weeks: 12})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(renderSleepSection(r, termInfo{width: 80}, 80, sleepOpts{Full: true}), "\n")
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, " Deep ") && !strings.Contains(line[:20], "-") {
			t.Errorf("Deep row needs a dash in the LAST NIGHT cell: %q", line)
		}
	}
	if !strings.Contains(got, " Deep ") {
		t.Errorf("no Deep row:\n%s", got)
	}
}

// Delta phrases read as English: "same as usual", "worse than usual".
func TestRegularityWording(t *testing.T) {
	g := glyphsFor(true)
	for _, c := range []struct {
		now, usual float64
		want       string
	}{
		{20, 20, "same as usual (±20m)"}, {50, 30, "worse than usual (±30m)"}, {10, 30, "better than usual (±30m)"},
	} {
		r := &stats.SleepReport{Regularity: &stats.SleepRegularity{Nights: 14, BedtimeMin: c.now, MidpointMin: 5, UsualBedtimeMin: c.usual}}
		line := regularityLine(r, g, style{}, 80, 11, 40)[0]
		if !strings.Contains(line, c.want) || strings.Contains(line, "same than") {
			t.Errorf("want %q in %q", c.want, line)
		}
	}
}

// The standalone commands also fit 50 columns, heatmap --weeks 26 included.
func TestStandaloneCommandsFit50(t *testing.T) {
	fixClock(t, heatNow)
	t.Setenv("COLUMNS", "50")
	t.Setenv("FORCE_COLOR", "")
	path := seedSleep(t, "rich")
	for _, args := range [][]string{{"sleep"}, {"sleep", "--weeks", "26"}, {"heatmap", "sleep", "--weeks", "26"},
		{"heatmap", "workouts", "--weeks", "26"}, {"workouts"}, {"overview", "--full"}} {
		out, _, err := run(t, append([]string{"--db", path}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, line := range strings.Split(out, "\n") {
			if n := runeLen(line); n > 50 {
				t.Errorf("%v: %d wide: %q", args, n, line)
			}
		}
	}
}

// Stages that overlap each other add up to more than Asleep (the union);
// the shares must still add up to 100%.
func TestStageSharesUseTheStageTotals(t *testing.T) {
	n := stats.SleepNight{Asleep: 4 * time.Hour, Core: 3 * time.Hour, REM: 2 * time.Hour, Deep: time.Hour}
	if got := stagePct(n.Core, stageSum(n)); got != " 50%" {
		t.Errorf("core share %q, want \" 50%%\"", got)
	}
}

// The help text must name the thresholds the table really uses.
func TestSleepHelpNamesTheAwakeThreshold(t *testing.T) {
	cmd := newSleepCmd(&options{})
	if !strings.Contains(cmd.Long, "10% for awake time") {
		t.Errorf("help does not mention the 10%% threshold for awake and latency")
	}
}

// The regularity row keeps a space before the verdict and never runs past the
// width: at 60 the numbers (27 wide) are wider than their 24-wide column, so
// the "(14 nights)" part is dropped to make room.
func TestRegularityLineSpacingAndWidth(t *testing.T) {
	r := &stats.SleepReport{Regularity: &stats.SleepRegularity{Nights: 14, BedtimeMin: 1, MidpointMin: 9, UsualBedtimeMin: 1}}
	for _, c := range []struct{ width, nameW, cols int }{{50, 11, 24}, {60, 11, 24}, {70, 11, 24}, {80, 13, 40}} {
		for _, g := range []glyphSet{glyphsFor(true), glyphsFor(false)} {
			line := regularityLine(r, g, style{}, c.width, c.nameW, c.cols)[0]
			if n := runeLen(line); n > c.width {
				t.Errorf("width %d: line is %d wide: %q", c.width, n, line)
			}
			if strings.Contains(line, ")"+g.flat) || strings.Contains(line, "mid"+g.flat) {
				t.Errorf("width %d: no space before the verdict: %q", c.width, line)
			}
		}
	}
}

// seedSleepAwakeOnly has one night with only "awake" rows (no sleep).
func seedSleepAwakeOnly(t *testing.T) string {
	path := seedSleep(t, "empty")
	conn := openTestDB(t, path)
	defer conn.Close()
	end := netHeatClock.Add(-9 * 3600).Unix() // 06:00 on 2026-11-04
	if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES ('awake', ?, ?, 0, 'Test Apple Watch')`, end-1800, end); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNetSleepCommand(t *testing.T) {
	sl := func(extra ...string) []string { return append([]string{"sleep"}, extra...) }
	rich := seedScenario(seedSleep, "rich")
	cases := []netCase{
		{name: "sleep_rich_w80", seed: rich, now: netHeatClock, cols: "80", args: sl()},
		{name: "sleep_rich_w50", seed: rich, now: netHeatClock, cols: "50", args: sl()},
		{name: "sleep_rich_w60", seed: rich, now: netHeatClock, cols: "60", args: sl()},
		{name: "sleep_rich_w100", seed: rich, now: netHeatClock, cols: "100", args: sl()},
		{name: "sleep_rich_colour", seed: rich, now: netHeatClock, cols: "80", force: true, args: sl()},
		{name: "sleep_rich_nocolor_flag", seed: rich, now: netHeatClock, cols: "80", force: true, args: sl("--no-color")},
		{name: "sleep_rich_NO_COLOR", seed: rich, now: netHeatClock, cols: "80", force: true, env: map[string]string{"NO_COLOR": "1"}, args: sl()},
		{name: "sleep_rich_ascii_flag", seed: rich, now: netHeatClock, cols: "80", args: sl("--ascii")},
		{name: "sleep_rich_bedtime", seed: rich, now: netHeatClock, cols: "80", args: sl("--by", "bedtime")},
		{name: "sleep_rich_efficiency", seed: rich, now: netHeatClock, cols: "80", args: sl("--by", "efficiency")},
		{name: "sleep_rich_efficiency_colour", seed: rich, now: netHeatClock, cols: "60", force: true, args: sl("--by", "efficiency")},
		{name: "sleep_rich_target_nights", seed: rich, now: netHeatClock, cols: "80", args: sl("--sleep-target", "7h30m", "--nights", "14")},
		{name: "sleep_rich_no_calendar", seed: rich, now: netHeatClock, cols: "80", args: sl("--weeks", "0")},
		{name: "sleep_rich_4_weeks", seed: rich, now: netHeatClock, cols: "50", args: sl("--weeks", "4")},
		{name: "sleep_rich_json", seed: rich, now: netHeatClock, args: sl("--json", "--weeks", "3")},
		{name: "sleep_rich_bedtime_json", seed: rich, now: netHeatClock, args: sl("--json", "--weeks", "3", "--by", "bedtime")},
		{name: "sleep_rich_efficiency_json", seed: rich, now: netHeatClock, args: sl("--json", "--weeks", "3", "--by", "efficiency")},
		{name: "sleep_unspecified_w80", seed: seedScenario(seedSleep, "unspecified"), now: netHeatClock, cols: "80", args: sl()},
		{name: "sleep_unspecified_w50", seed: seedScenario(seedSleep, "unspecified"), now: netHeatClock, cols: "50", args: sl()},
		{name: "sleep_unspecified_json", seed: seedScenario(seedSleep, "unspecified"), now: netHeatClock, args: sl("--json", "--weeks", "2")},
		{name: "sleep_inbed_w80", seed: seedScenario(seedSleep, "inbed"), now: netHeatClock, cols: "80", args: sl()},
		{name: "sleep_inbed_json", seed: seedScenario(seedSleep, "inbed"), now: netHeatClock, args: sl("--json", "--weeks", "2")},
		{name: "sleep_sparse_w80", seed: seedScenario(seedSleep, "sparse"), now: netHeatClock, cols: "80", args: sl()},
		{name: "sleep_sparse_w50_colour", seed: seedScenario(seedSleep, "sparse"), now: netHeatClock, cols: "50", force: true, args: sl()},
		{name: "sleep_sparse_json", seed: seedScenario(seedSleep, "sparse"), now: netHeatClock, args: sl("--json", "--weeks", "2")},
		{name: "sleep_awake_only_w80", seed: seedSleepAwakeOnly, now: netHeatClock, cols: "80", args: sl()},
		{name: "sleep_awake_only_json", seed: seedSleepAwakeOnly, now: netHeatClock, args: sl("--json", "--weeks", "2")},
		{name: "sleep_empty_w80", seed: seedScenario(seedSleep, "empty"), now: netHeatClock, cols: "80", args: sl()},
		{name: "sleep_empty_json", seed: seedScenario(seedSleep, "empty"), now: netHeatClock, args: sl("--json")},
		{name: "sleep_stale_w80", seed: rich, now: at(2026, 12, 20, 15), cols: "80", args: sl()},
		{name: "sleep_err_target_short", seed: emptyDB, now: netHeatClock, args: sl("--sleep-target", "2h")},
		{name: "sleep_err_target_garbage", seed: emptyDB, now: netHeatClock, args: sl("--sleep-target", "lots")},
		{name: "sleep_err_by", seed: rich, now: netHeatClock, args: sl("--by", "colour")},
		{name: "sleep_err_weeks", seed: emptyDB, now: netHeatClock, args: sl("--weeks", "60")},
		{name: "sleep_err_nights", seed: rich, now: netHeatClock, args: sl("--nights", "3")},
		{name: "sleep_err_arg", seed: emptyDB, now: netHeatClock, args: sl("tonight")},
		{name: "sleep_err_no_db", now: netHeatClock, args: []string{"--db", "/nonexistent-dir/none.db", "sleep"}},
	}
	runNetCases(t, cases)
}

// Unicode sleep screens at the widths the older goldens do not cover.
func TestNetSleepRenderUnicode(t *testing.T) {
	var b strings.Builder
	for _, c := range []struct {
		scenario string
		ti       termInfo
		by       string
	}{
		{"rich", termInfo{width: 50, unicode: true}, ""},
		{"rich", termInfo{width: 100, unicode: true}, ""},
		{"rich", termInfo{width: 100, unicode: true, color: true}, ""},
		{"rich", termInfo{width: 60, unicode: true, color: true}, "bedtime"},
		{"rich", termInfo{width: 60, unicode: true, color: true}, "efficiency"},
		{"rich", termInfo{width: 60}, "bedtime"},
		{"unspecified", termInfo{width: 50, unicode: true, color: true}, ""},
		{"inbed", termInfo{width: 50, unicode: true}, ""},
		{"inbed", termInfo{width: 80, unicode: true, color: true}, ""},
		{"sparse", termInfo{width: 60, unicode: true, color: true}, ""},
	} {
		lines := renderSleepFor(t, c.scenario, c.ti, c.ti.width, true, c.by)
		b.WriteString("## " + c.scenario + " " + termName(c.ti) + " by=" + c.by + "\n" + strings.Join(lines, "\n") + "\n\n")
	}
	checkViewGolden(t, "net_sleep_unicode.txt", b.String())
}

// (e) the header says how many nights the usual really has; (f) the header
// midpoint is the table's midpoint; (m) words, not "7 n" or "2x"; (n) the
// hypnogram axis is a clock (00 01 02).
func TestSleepWordsAndNumbersAgree(t *testing.T) {
	got := overviewText(t, "import:small", uni80, overviewView{})
	if !strings.Contains(got, "VS 12-NIGHT USUAL") || strings.Contains(got, "VS 28-NIGHT USUAL") {
		t.Errorf("header must say the real number of nights:\n%s", got)
	}
	for _, bad := range []string{" n)", " n:", "2x", "1x", "0x"} {
		if strings.Contains(got, bad) {
			t.Errorf("abbreviation %q left in:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "7 nights") || !strings.Contains(got, "awakenings") {
		t.Errorf("want 7 nights and awakenings:\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^ +00 +01 +02`).MatchString(got) {
		t.Errorf("hour axis should be 00 01 02:\n%s", got)
	}

	// The unspecified night prints its midpoint in the bar caption too.
	lines := renderSleepFor(t, "unspecified", uni80, 80, true, "")
	text := strings.Join(lines, "\n")
	head := regexp.MustCompile(`midpoint (\d\d:\d\d)`).FindStringSubmatch(text)
	row := regexp.MustCompile(`Midpoint +(\d\d:\d\d)`).FindStringSubmatch(text)
	if head == nil || row == nil || head[1] != row[1] {
		t.Errorf("header midpoint %v differs from the table's %v:\n%s", head, row, text)
	}
}

// (k) The SLEEP subtitle is shortened, never cut with an ellipsis.
func TestSleepHeaderIsNeverCut(t *testing.T) {
	r := &stats.SleepReport{Kind: "stages", AgeDays: 12, Last: stats.SleepNight{
		Night: "2026-10-27", Source: "Alexandra's Very Long Sleep Tracker Pro", InBedSource: "Another Rather Long Source Name"}}
	for _, width := range []int{50, 60, 80, 100} {
		for _, uni := range []bool{true, false} {
			g := glyphsFor(uni)
			h := stripANSI(sleepHeader(r, g, width, style{}))
			if strings.Contains(h, "…") || strings.Contains(h, "...") || runeLen(h) > width {
				t.Errorf("width %d: %q", width, h)
			}
		}
	}
	if h := sleepHeader(r, unicodeGlyphs, 80, style{}); !strings.Contains(h, "Wed 28 Oct") && !strings.Contains(h, "Tue 27 Oct") {
		t.Errorf("the date must survive: %q", h)
	}
}

// (i) 0 awake time against a usual of 0 is "typical", not a blank.
func TestAwakeZeroIsTypical(t *testing.T) {
	zero := 0.0
	var awake sleepStat
	for _, s := range sleepStats {
		if s.name == "Awake" {
			awake = s
		}
	}
	v, ok := compareSleep(awake, &zero, &zero, unicodeGlyphs)
	if !ok || v.verdict != "typical" || !strings.Contains(v.text, "typical") {
		t.Errorf("got %+v %v", v, ok)
	}
}

// QA regressions for "healthcli today" and the sleep section it shares with
// overview.

// A stat label must always be followed by a space, at every width.
func TestSleepTableKeepsASpaceAfterLabels(t *testing.T) {
	glued := regexp.MustCompile(`(Efficiency|Sleep debt|In bed|Midpoint)[0-9]`)
	for _, width := range []int{50, 55, 60, 80} {
		ti := termInfo{width: width, unicode: true}
		sleep := strings.Join(renderSleepFor(t, "rich", ti, width, true, ""), "\n")
		overview := renderOverview(overviewReport(t, "import:small"), ti, testView)
		for name, text := range map[string]string{"sleep": sleep, "overview": overview, "today": todayPage(t, seedOverview(t, "import:small"), "2026-11-08", ti, overviewView{})} {
			if m := glued.FindString(text); m != "" {
				t.Errorf("%s at %d columns: label glued to its value (%q)", name, width, m)
			}
		}
	}
}

// The "stages not recorded" note is never cut with an ellipsis, with or
// without colour; it shortens to "(no stages)" only when it cannot fit.
func TestSleepUnspecifiedNote(t *testing.T) {
	for _, color := range []bool{false, true} {
		for _, width := range []int{50, 60, 80, 100, 200} {
			ti := termInfo{width: width, unicode: true, color: color}
			text := stripANSI(strings.Join(renderSleepFor(t, "unspecified", ti, width, false, ""), "\n"))
			if strings.Contains(text, "…") {
				t.Errorf("width %d color %v: cut with an ellipsis:\n%s", width, color, text)
			}
			wantFull := width >= 80
			if wantFull && !strings.Contains(text, "(stages not recorded)") {
				t.Errorf("width %d color %v: want the full note:\n%s", width, color, text)
			}
			if !strings.Contains(text, "(stages not recorded)") && !strings.Contains(text, "(no stages)") {
				t.Errorf("width %d color %v: no note:\n%s", width, color, text)
			}
		}
	}
}
