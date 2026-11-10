package cli

import "testing"

func TestNetTodayCommand(t *testing.T) {
	td := func(extra ...string) []string { return append([]string{"today"}, extra...) }
	apple := seedScenario(seedOverview, "import:small")
	full := seedScenario(seedOverview, "full")
	n := overviewNow
	d8 := []string{"--date", "2026-11-08"}
	cases := []netCase{
		{name: "today_apple_default_date", seed: apple, now: n, cols: "80", args: td()},
		{name: "today_apple_w80", seed: apple, now: n, cols: "80", args: td(d8...)},
		{name: "today_apple_w50", seed: apple, now: n, cols: "50", args: td(d8...)},
		{name: "today_apple_w60", seed: apple, now: n, cols: "60", args: td(d8...)},
		{name: "today_apple_w100", seed: apple, now: n, cols: "100", args: td(d8...)},
		{name: "today_apple_colour", seed: apple, now: n, cols: "80", force: true, args: td(d8...)},
		{name: "today_apple_nocolor_flag", seed: apple, now: n, cols: "80", force: true, args: td(append(d8, "--no-color")...)},
		{name: "today_apple_NO_COLOR", seed: apple, now: n, cols: "80", force: true, env: map[string]string{"NO_COLOR": "1"}, args: td(d8...)},
		{name: "today_apple_ascii_flag", seed: apple, now: n, cols: "80", args: td(append(d8, "--ascii")...)},
		{name: "today_apple_compact", seed: apple, now: n, cols: "80", args: td(append(d8, "--compact")...)},
		{name: "today_apple_category_vitals", seed: apple, now: n, cols: "80", args: td(append(d8, "--category", "vitals")...)},
		{name: "today_apple_category_sleep_activity", seed: apple, now: n, cols: "60", args: td(append(d8, "--category", "sleep,activity")...)},
		{name: "today_apple_category_workouts", seed: apple, now: n, cols: "80", args: td(append(d8, "--category", "workouts")...)},
		{name: "today_apple_json", seed: apple, now: n, args: td(append(d8, "--json")...)},
		{name: "today_apple_category_json", seed: apple, now: n, args: td(append(d8, "--json", "--category", "vitals")...)},
		{name: "today_apple_older_day", seed: apple, now: n, cols: "80", args: td("--date", "2026-11-02")},
		{name: "today_apple_older_day_json", seed: apple, now: n, args: td("--date", "2026-11-02", "--json")},
		{name: "today_full_workout_day", seed: full, now: n, cols: "80", args: td("--date", "2026-11-05")},
		{name: "today_full_workout_day_json", seed: full, now: n, args: td("--date", "2026-11-05", "--json")},
		{name: "today_full_fallback", seed: full, now: at(2026, 11, 20, 9), cols: "80", args: td("--date", "2026-11-18")},
		{name: "today_fallback_json", seed: full, now: at(2026, 11, 20, 9), args: td("--date", "2026-11-18", "--json")},
		{name: "today_stale_default_date", seed: seedScenario(seedOverview, "stale"), now: n, cols: "80", args: td()},
		{name: "today_sparse_device", seed: seedSparseDevice, now: n, cols: "60", args: td(d8...)},
		{name: "today_sparse_device_json", seed: seedSparseDevice, now: n, args: td(append(d8, "--json")...)},
		{name: "today_sleep_only", seed: seedScenario(seedOverview, "sleep-only"), now: n, cols: "80", args: td()},
		{name: "today_steps_only_json", seed: seedScenario(seedOverview, "steps-only"), now: n, args: td("--json")},
		{name: "today_seeddb_category_sleep", seed: seedDB, now: netTrendClock, cols: "80", args: td("--date", "2026-11-08", "--category", "sleep")},
		{name: "today_seeddb_category_workouts_json", seed: seedDB, now: netTrendClock, args: td("--date", "2026-11-08", "--category", "workouts", "--json")},
		{name: "today_empty_db", seed: emptyDB, now: n, cols: "80", args: td()},
		{name: "today_empty_db_json", seed: emptyDB, now: n, args: td("--json")},
		{name: "today_err_bad_date", seed: emptyDB, now: n, args: td("--date", "11/08/2026")},
		{name: "today_err_future", seed: emptyDB, now: n, args: td("--date", "2026-11-09")},
		{name: "today_err_category", seed: emptyDB, now: n, args: td("--category", "bogus")},
		{name: "today_err_arg", seed: emptyDB, now: n, args: td("monday")},
		{name: "today_err_no_db", now: n, args: []string{"--db", "/nonexistent-dir/none.db", "today"}},
	}
	runNetCases(t, cases)
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
