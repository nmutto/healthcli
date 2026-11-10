package cli

import (
	"strings"
	"testing"
)

func TestNetWorkoutsCommand(t *testing.T) {
	wo := func(extra ...string) []string { return append([]string{"workouts"}, extra...) }
	rich := seedScenario(seedWorkoutsDB, "rich")
	sparse := seedScenario(seedWorkoutsDB, "sparse")
	n := netWorkoutsClock
	cases := []netCase{
		{name: "workouts_rich_w80", seed: rich, now: n, cols: "80", args: wo()},
		{name: "workouts_rich_w50", seed: rich, now: n, cols: "50", args: wo()},
		{name: "workouts_rich_w60", seed: rich, now: n, cols: "60", args: wo()},
		{name: "workouts_rich_w100", seed: rich, now: n, cols: "100", args: wo()},
		{name: "workouts_rich_colour", seed: rich, now: n, cols: "80", force: true, args: wo()},
		{name: "workouts_rich_nocolor_flag", seed: rich, now: n, cols: "80", force: true, args: wo("--no-color")},
		{name: "workouts_rich_NO_COLOR", seed: rich, now: n, cols: "80", force: true, env: map[string]string{"NO_COLOR": "1"}, args: wo()},
		{name: "workouts_rich_ascii_flag", seed: rich, now: n, cols: "80", args: wo("--ascii")},
		{name: "workouts_rich_by_type", seed: rich, now: n, cols: "80", args: wo("--by", "type")},
		{name: "workouts_rich_by_type_w50", seed: rich, now: n, cols: "50", args: wo("--by", "TYPE")},
		{name: "workouts_rich_by_type_colour", seed: rich, now: n, cols: "80", force: true, args: wo("--by", "type")},
		{name: "workouts_rich_days7", seed: rich, now: n, cols: "80", args: wo("--days", "7")},
		{name: "workouts_rich_days2", seed: rich, now: n, cols: "80", args: wo("--days", "2")},
		{name: "workouts_rich_days1", seed: rich, now: n, cols: "80", args: wo("--days", "1")},
		{name: "workouts_rich_days90_weeks12", seed: rich, now: n, cols: "100", args: wo("--days", "90", "--weeks", "12")},
		{name: "workouts_rich_weeks1", seed: rich, now: n, cols: "80", args: wo("--weeks", "1")},
		{name: "workouts_rich_json", seed: rich, now: n, args: wo("--json")},
		{name: "workouts_rich_days2_json", seed: rich, now: n, args: wo("--json", "--days", "2", "--weeks", "2")},
		{name: "workouts_sparse_w80", seed: sparse, now: n, cols: "80", args: wo()},
		{name: "workouts_sparse_days30", seed: sparse, now: n, cols: "80", args: wo("--days", "30")},
		{name: "workouts_sparse_json", seed: sparse, now: n, args: wo("--json", "--days", "30")},
		{name: "workouts_old_w80", seed: seedScenario(seedWorkoutsDB, "old"), now: n, cols: "80", args: wo()},
		{name: "workouts_old_days90", seed: seedScenario(seedWorkoutsDB, "old"), now: n, cols: "80", args: wo("--days", "90")},
		{name: "workouts_old_json", seed: seedScenario(seedWorkoutsDB, "old"), now: n, args: wo("--json")},
		{name: "workouts_none_w80", seed: seedScenario(seedWorkoutsDB, "none"), now: n, cols: "80", args: wo()},
		{name: "workouts_none_json", seed: seedScenario(seedWorkoutsDB, "none"), now: n, args: wo("--json")},
		{name: "workouts_none_by_type", seed: seedScenario(seedWorkoutsDB, "none"), now: n, cols: "80", args: wo("--by", "type")},
		{name: "workouts_err_days0", seed: emptyDB, now: n, args: wo("--days", "0")},
		{name: "workouts_err_days366", seed: emptyDB, now: n, args: wo("--days", "366")},
		{name: "workouts_err_weeks0", seed: emptyDB, now: n, args: wo("--weeks", "0")},
		{name: "workouts_err_by", seed: emptyDB, now: n, args: wo("--by", "sport")},
		{name: "workouts_err_arg", seed: emptyDB, now: n, args: wo("running")},
		{name: "workouts_err_no_db", now: n, args: []string{"--db", "/nonexistent-dir/none.db", "workouts"}},
	}
	runNetCases(t, cases)
}

// Unicode workouts screens, which the command cannot reach.
func TestNetWorkoutsRenderUnicode(t *testing.T) {
	var b strings.Builder
	for _, c := range []struct {
		scenario string
		ti       termInfo
		opts     workoutsOpts
	}{
		{"rich", termInfo{width: 50, unicode: true}, workoutsOpts{Full: true, Weeks: 8}},
		{"rich", termInfo{width: 100, unicode: true}, workoutsOpts{Full: true, Weeks: 12}},
		{"rich", termInfo{width: 100, unicode: true, color: true}, workoutsOpts{Full: true, Weeks: 12, ByType: true}},
		{"rich", termInfo{width: 50, unicode: true, color: true}, workoutsOpts{Full: true, Weeks: 4, ByType: true}},
		{"rich", termInfo{width: 60}, workoutsOpts{Full: true, Weeks: 8, ByType: true}},
		{"sparse", termInfo{width: 50, unicode: true, color: true}, workoutsOpts{Full: true, Weeks: 8}},
		{"old", termInfo{width: 60, unicode: true, color: true}, workoutsOpts{Full: true, Weeks: 8}},
		{"none", termInfo{width: 50}, workoutsOpts{Full: true, Weeks: 8}},
	} {
		lines := renderWorkoutsSection(seedWorkouts(t, c.scenario), c.ti, c.ti.width, c.opts)
		b.WriteString("## " + c.scenario + " " + termName(c.ti) + "\n" + strings.Join(lines, "\n") + "\n\n")
	}
	checkViewGolden(t, "net_workouts_unicode.txt", b.String())
}
