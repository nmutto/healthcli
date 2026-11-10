package cli

import (
	"strings"
	"testing"
)

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
