package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

// seedSleep writes synthetic nights for one scenario and returns the
// database path. The latest night ends on heatNow's date (a Wednesday).
//
//	rich         40 nights: Watch stages (with awakenings) and iPhone in bed
//	unspecified  6 nights: iPhone "asleep" only, no stages, no in bed
//	inbed        4 nights: in bed only
//	sparse       3 nights of Watch stages
//	empty        no sleep, one weight reading
func seedSleep(t *testing.T, scenario string) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	add := func(stage string, from, to int64, source string) {
		if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
			VALUES (?, ?, ?, 0, ?)`, stage, from, to, source); err != nil {
			t.Fatal(err)
		}
	}
	const watch, phone = "Test Apple Watch", "Test iPhone"
	midnight := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	if scenario == "empty" {
		conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES ('body_mass', ?, ?, 72.4, '', 0, ?)`, midnight.Unix(), midnight.Unix()+60, watch)
		return path
	}
	nights := map[string]int{"rich": 40, "unspecified": 6, "inbed": 4, "sparse": 3}[scenario]
	for k := 0; k < nights; k++ {
		end := midnight.AddDate(0, 0, -k).Add(6*time.Hour + time.Duration(k*7%40)*time.Minute).Unix()
		span := int64(405+k*13%60) * 60 // 6h45 to 7h45
		switch {
		case k < 7: // the last week was shorter, so the table has changes to show
			span -= 50 * 60
		case k < 14:
			span += 30 * 60
		}
		start := end - span
		switch scenario {
		case "unspecified":
			add("asleep_unspecified", start, end, phone)
		case "inbed":
			add("in_bed", start-600, end+300, phone)
		default:
			if scenario == "rich" {
				add("in_bed", start-int64(8+k%9)*60, end+int64(k%5)*60, phone)
			}
			// A repeating pattern of stages (minutes), cut off at wake-up.
			pattern := []struct {
				stage string
				mins  int64
			}{{"core", 70}, {"deep", 45}, {"core", 50}, {"rem", 20}, {"awake", int64(2 + 4*(k%2))},
				{"core", 60}, {"rem", 30}, {"awake", int64(3 + 3*(k%3))}, {"core", 50}, {"rem", 25}, {"core", 90}}
			at := start
			for _, p := range pattern {
				to := min(at+p.mins*60, end)
				if to > at {
					add(p.stage, at, to, watch)
				}
				at = to
			}
		}
	}
	return path
}

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
	old := nowFunc
	nowFunc = func() time.Time { return heatNow }
	defer func() { nowFunc = old }()

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
	old := nowFunc
	nowFunc = func() time.Time { return heatNow }
	defer func() { nowFunc = old }()
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
