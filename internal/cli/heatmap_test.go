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

// heatNow is a Wednesday, so the last column has future days.
var heatNow = time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)

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
	old := nowFunc
	nowFunc = func() time.Time { return heatNow }
	defer func() { nowFunc = old }()

	// No data: friendly line, exit 0.
	for _, name := range []string{"sleep", "workouts"} {
		out, _, err := run(t, "--db", seedHeat(t, "empty"), "heatmap", name)
		if err != nil || !strings.HasPrefix(out, "No "+name+" data") {
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
	old := nowFunc
	nowFunc = func() time.Time { return heatNow }
	defer func() { nowFunc = old }()

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
		metric string
		want   [stats.HeatLevels]string
	}{
		{"workouts", heatGreen},
		{"sleep", heatBlue},
		{"sleep_bedtime", heatBlue},
		{"step_count", heatOrange},
		{"active_energy", heatOrange},
	}
	for _, c := range cases {
		if got := heatColorsFor(&stats.Heatmap{Metric: c.metric}); got != c.want {
			t.Errorf("%s: got %v, want %v", c.metric, got, c.want)
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
