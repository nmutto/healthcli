package cli

import (
	"strings"
	"testing"
)

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
