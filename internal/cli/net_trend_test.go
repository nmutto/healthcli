package cli

import (
	"strings"
	"testing"
)

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
