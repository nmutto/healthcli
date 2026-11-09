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
	t.Helper()
	conn, err := db.Open(seedHeat(t, scenario))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	h, err := stats.BuildHeatmap(context.Background(), conn, "step_count", min(weeks, heatWeeksFit(ti.width)), heatNow)
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
