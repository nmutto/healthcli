package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

var workoutsNow = time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC) // a Monday

// seedWorkouts writes synthetic workouts for one scenario:
//
//	rich    10 weeks of running, cycling, walking and yoga, with kcal and km
//	sparse  two sessions: a yoga (no kcal, no km) and a short run, no kcal
//	old     one session 60 days ago, nothing since
//	none    nothing
func seedWorkouts(t *testing.T, scenario string) *stats.WorkoutsReport {
	t.Helper()
	conn, err := db.Open(seedWorkoutsDB(t, scenario))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r, err := stats.BuildWorkoutsReport(t.Context(), conn, "2026-11-09", 28, 8)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// seedWorkoutsDB is the database behind seedWorkouts (same scenarios).
func seedWorkoutsDB(t *testing.T, scenario string) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	midnight := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	add := func(back int, activity string, mins int, km, kcal any) {
		start := midnight.AddDate(0, 0, -back).Add(18 * time.Hour).Unix()
		if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
			VALUES (?, ?, ?, ?, ?, ?, 0, 'Test Apple Watch')`, activity, start, start+int64(mins*60), mins*60, kcal, km); err != nil {
			t.Fatal(err)
		}
	}
	switch scenario {
	case "rich":
		for back := 1; back < 70; back++ {
			switch back % 5 {
			case 0:
				add(back, "running", 35+(back*7)%40, 6500+float64(back%7)*900, 380+float64(back%9)*40)
			case 2:
				add(back, "cycling", 45+(back*11)%50, 16000+float64(back%5)*2500, 450+float64(back%6)*50)
			case 3:
				if back%2 == 1 {
					add(back, "walking", 30+back%25, 2800+float64(back%4)*500, 150+float64(back%5)*20)
				}
			case 4:
				if back%3 == 0 {
					add(back, "yoga", 40+back%15, nil, nil)
				}
			}
		}
	case "sparse":
		add(7, "yoga", 45, nil, nil)
		add(17, "running", 50, 7000, nil)
	case "old":
		add(60, "running", 40, 6000, 400)
	}
	return path
}

func TestWorkoutsSectionGoldens(t *testing.T) {
	cases := []struct {
		name, scenario string
		ti             termInfo
		opts           workoutsOpts
	}{
		{"workouts_rich_80.txt", "rich", termInfo{width: 80, unicode: true}, workoutsOpts{Full: true, Weeks: 8}},
		{"workouts_rich_60.txt", "rich", termInfo{width: 60, unicode: true}, workoutsOpts{Full: true, Weeks: 8}},
		{"workouts_rich_ascii.txt", "rich", termInfo{width: 80}, workoutsOpts{Full: true, Weeks: 8}},
		{"workouts_rich_color.txt", "rich", termInfo{width: 80, unicode: true, color: true}, workoutsOpts{Full: true, Weeks: 8, ByType: true}},
		{"workouts_rich_bytype.txt", "rich", termInfo{width: 80, unicode: true}, workoutsOpts{Full: true, Weeks: 8, ByType: true}},
		{"workouts_rich_compact.txt", "rich", termInfo{width: 80, unicode: true}, workoutsOpts{}},
		{"workouts_sparse_80.txt", "sparse", termInfo{width: 80, unicode: true}, workoutsOpts{Full: true, Weeks: 8}},
		{"workouts_sparse_compact.txt", "sparse", termInfo{width: 80, unicode: true}, workoutsOpts{}},
		{"workouts_old_80.txt", "old", termInfo{width: 80, unicode: true}, workoutsOpts{Full: true}},
		{"workouts_none_80.txt", "none", termInfo{width: 80, unicode: true}, workoutsOpts{Full: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := seedWorkouts(t, c.scenario)
			lines := renderWorkoutsSection(r, c.ti, c.ti.width, c.opts)
			got := strings.Join(lines, "\n") + "\n"
			checkViewGolden(t, c.name, got)
			for _, l := range lines {
				if w := runeLen(stripANSI(l)); w > c.ti.width {
					t.Errorf("line is %d wide (max %d): %q", w, c.ti.width, l)
				}
				if !c.ti.unicode && strings.ContainsFunc(l, func(r rune) bool { return r > 127 }) {
					t.Errorf("non-ASCII in ASCII output: %q", l)
				}
			}
			if !c.ti.color && strings.Contains(got, "\x1b") {
				t.Error("escape codes without colour")
			}
		})
	}
}

func TestWorkoutsReportNumbers(t *testing.T) {
	r := seedWorkouts(t, "sparse")
	if r.Count != 2 || r.EnergyKcal != nil || r.DistanceKM == nil || *r.DistanceKM != 7 {
		t.Errorf("sparse totals: %+v", r)
	}
	if len(r.Bests) != 0 {
		t.Errorf("bests need 3 sessions of one activity: %+v", r.Bests)
	}
	rich := seedWorkouts(t, "rich")
	if len(rich.Weeks) != 8 || !rich.Weeks[7].Partial || rich.Weeks[7].Days != 1 || rich.Weeks[7].DeltaPct != nil {
		t.Errorf("weeks: %+v", rich.Weeks)
	}
	if rich.Weeks[3].DeltaPct == nil {
		t.Error("a full week with 4 weeks of history has a delta")
	}
	if len(rich.Bests) == 0 {
		t.Error("rich data has bests")
	}
}

func TestWorkoutsCommand(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return workoutsNow }
	defer func() { nowFunc = old }()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, tz_offset, source)
		VALUES ('yoga', ?, ?, 2700, 0, 'Test')`, workoutsNow.Add(-24*time.Hour).Unix(), workoutsNow.Add(-23*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "workouts")
	if err != nil || !strings.Contains(out, "WORKOUTS") || !strings.Contains(out, "yoga") {
		t.Errorf("text: %v\n%s", err, out)
	}
	out, _, err = run(t, "--db", path, "workouts", "--json")
	if err != nil || strings.Contains(out, "null") || strings.Contains(out, "energy_kcal") {
		t.Errorf("json must leave absent values out, not null: %v\n%s", err, out)
	}
	for _, args := range [][]string{{"--days", "0"}, {"--weeks", "99"}, {"--by", "colour"}} {
		if _, _, err := run(t, append([]string{"--db", path, "workouts"}, args...)...); err == nil {
			t.Errorf("%v should fail", args)
		}
	}
	empty := emptyDB(t)
	out, _, err = run(t, "--db", empty, "workouts")
	if err != nil || !strings.Contains(out, "none in the last 28 days") {
		t.Errorf("empty: %v\n%s", err, out)
	}
}

// jsonNullsAndKeys walks decoded JSON and reports every null (except inside
// the arrays named in skip) and every key that is not snake_case.
func jsonNullsAndKeys(v any, path string, skip map[string]bool, bad *[]string) {
	switch x := v.(type) {
	case nil:
		*bad = append(*bad, path+" is null")
	case map[string]any:
		for k, child := range x {
			if k != strings.ToLower(k) || strings.ContainsAny(k, "- ") {
				*bad = append(*bad, path+"."+k+" is not snake_case")
			}
			if skip[k] {
				continue
			}
			jsonNullsAndKeys(child, path+"."+k, skip, bad)
		}
	case []any:
		for i, child := range x {
			jsonNullsAndKeys(child, fmt.Sprintf("%s[%d]", path, i), skip, bad)
		}
	}
}

// --json: snake_case keys only, durations in seconds (not nanoseconds), no
// null anywhere, and a zero-duration session does not exist.
func TestWorkoutsJSONShape(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return workoutsNow }
	defer func() { nowFunc = old }()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	add := func(back int, activity string, secs int, kcal, metres any) {
		start := workoutsNow.AddDate(0, 0, -back).Unix()
		if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
			VALUES (?, ?, ?, ?, ?, ?, 0, 'Test')`, activity, start, start+int64(secs), secs, kcal, metres); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "yoga", 2700, nil, nil)
	add(2, "running", 1800, 300.0, 5000.0)
	add(3, "walking", 0, nil, nil) // zero duration: ignored everywhere
	conn.Close()

	out, _, err := run(t, "--db", path, "workouts", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	var bad []string
	jsonNullsAndKeys(doc, "$", nil, &bad)
	if len(bad) > 0 {
		t.Errorf("json problems: %v\n%s", bad, out)
	}
	if doc["count"].(float64) != 2 {
		t.Errorf("the zero-duration workout must be ignored, count = %v", doc["count"])
	}
	recent := doc["recent"].([]any)[0].(map[string]any)
	if recent["duration_seconds"].(float64) != 2700 {
		t.Errorf("duration must be seconds: %v", recent)
	}
	if strings.Contains(out, "walking") {
		t.Errorf("zero-duration workout leaked into the report:\n%s", out)
	}
}

// The workouts calendar inside the section is green (it used to fall back to
// the orange activity ramp because only the standalone command set the hue).
func TestWorkoutsSectionHeatmapIsGreen(t *testing.T) {
	r := seedWorkouts(t, "rich")
	ti := termInfo{width: 80, unicode: true, color: true}
	got := strings.Join(renderWorkoutsSection(r, ti, ti.width, workoutsOpts{Full: true, Weeks: 8}), "\n")
	for _, code := range heatGreen {
		if strings.Contains(got, code) {
			return
		}
	}
	t.Errorf("no green heatmap colour in the section:\n%s", got)
}

// Recent is every session of the last 7 days, but never further back than the
// --days window: --days 2 shows the last 2 days.
func TestWorkoutsRecentFollowsDays(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return workoutsNow }
	defer func() { nowFunc = old }()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// workoutsNow is Nov 9 20:00; the sessions are on Nov 8 and Nov 5.
	for _, back := range []int{1, 4} {
		start := workoutsNow.AddDate(0, 0, -back).Add(-3 * time.Hour).Unix()
		if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, tz_offset, source)
			VALUES ('yoga', ?, ?, 2700, 0, 'Test')`, start, start+2700); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	for days, want := range map[string]int{"1": 0, "2": 1, "4": 1, "5": 2, "6": 2, "28": 2} {
		out, _, err := run(t, "--db", path, "workouts", "--days", days, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var rep struct {
			Recent []any `json:"recent"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatal(err)
		}
		if len(rep.Recent) != want {
			t.Errorf("--days %s: recent has %d sessions, want %d\n%s", days, len(rep.Recent), want, out)
		}
	}
	out, _, err := run(t, "--db", path, "workouts", "--days", "1")
	if err != nil || !strings.Contains(out, "none in 1 d (last: Nov 08)") || strings.Contains(out, "RECENT") {
		t.Errorf("an empty window shows the none line and no RECENT block: %v\n%s", err, out)
	}
}
