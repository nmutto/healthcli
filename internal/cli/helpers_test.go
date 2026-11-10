package cli

import (
	"bytes"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/importer"
	"github.com/nmutto/healthcli/internal/stats"
)

// heatNow is a Wednesday, so the last column has future days.
var heatNow = time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)

const smallExport = "../../testdata/small/export.xml"

// tempDB returns a --db flag pointing at a fresh database file.
func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

// netSequence runs several commands against one database, in order, and
// returns the whole conversation as one golden:
//
//	$ healthcli log weight 72.5 ...
//	<what it printed>
func netSequence(t *testing.T, path string, commands ...[]string) string {
	t.Helper()
	var b strings.Builder
	for _, args := range commands {
		out, errOut, err := run(t, append([]string{"--db", path}, args...)...)
		b.WriteString("$ healthcli " + strings.Join(args, " ") + "\n" + netText(out, errOut, err))
	}
	return b.String()
}

func at(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }

// The clocks the seeds were written for.
var (
	netTrendClock    = at(2026, 11, 9, 9)              // seedDB
	netHeatClock     = heatNow                         // seedHeat, seedSleep
	netWorkoutsClock = workoutsNow                     // seedWorkouts
	netOverviewClock = overviewNow.Add(24 * time.Hour) // seedOverview: the day after the data
)

func seedScenario(f func(*testing.T, string) string, scenario string) func(*testing.T) string {
	return func(t *testing.T) string { return f(t, scenario) }
}

// openTestDB opens an existing test database for a direct call.
func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// termName describes a termInfo in a golden's section heading.
func termName(ti termInfo) string {
	s := fmt.Sprintf("w%d", ti.width)
	if ti.unicode {
		s += " unicode"
	} else {
		s += " ascii"
	}
	if ti.color {
		s += " colour"
	}
	return s
}

// overviewNow is the fixed "now" of every overview test.
var overviewNow = time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC)

// seedOverview builds a synthetic database for one scenario:
//
//	full       every state at once: ok, stale (weight), gaps (HRV), sleep, workouts, an import
//	stale      the same data, but nothing newer than 12 days ago
//	sleep-only one night of sleep and nothing else
//	empty      nothing
//	steps-only one device that records only steps
//	import:X   the synthetic export testdata/X, dates moved to be recent
func seedOverview(t *testing.T, scenario string) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if scenario == "empty" {
		return path
	}
	if strings.HasPrefix(scenario, "import:") {
		conn.Close()
		return seedFromFixture(t, path, strings.TrimPrefix(scenario, "import:"))
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	sample := func(metric string, d time.Time, hour int, value float64, source string) {
		ts := d.Add(time.Duration(hour) * time.Hour).Unix()
		exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, '', 0, ?)`, metric, ts, ts+60, value, source)
	}
	stage := func(stage string, startTS, endTS int64) {
		exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
			VALUES (?, ?, ?, 0, 'Test Apple Watch')`, stage, startTS, endTS)
	}

	// shift moves everything back, so "stale" is the same data, just older.
	shift := 0
	if scenario == "stale" {
		shift = 12
	}
	midnight := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	dayAt := func(back int) time.Time { return midnight.AddDate(0, 0, -(back + shift)) }

	if scenario == "steps-long" { // 12 full weeks of steps, a few zero days
		for back := 0; back < 90; back++ {
			steps := 4000 + float64((back*37)%11)*700
			if back%13 == 5 {
				steps = 0
			}
			sample("step_count", dayAt(back), 12, steps, "Test Phone")
		}
		return path
	}
	if scenario == "steps-only" {
		for back := 0; back < 20; back++ {
			sample("step_count", dayAt(back), 12, 5000+float64((back*7)%9)*600, "Test Phone")
		}
		return path
	}

	if scenario != "sleep-only" {
		for back := 0; back < 40; back++ {
			d := dayAt(back)
			i := 40 - back // grows with time so trends are visible
			steps := 6000 + float64((i*7)%9)*450
			energy := 420 + float64((i*5)%8)*40
			if back == 0 {
				steps, energy = 11800, 640 // today: well above usual
			}
			sample("step_count", d, 12, steps, "Test Apple Watch")
			sample("step_count", d, 13, 99999, "Test iPhone") // loses to the Watch
			sample("active_energy", d, 12, energy, "Test Apple Watch")
			sample("heart_rate", d, 9, 66+float64((i*3)%7), "Test Apple Watch")
			if back >= 1 { // resting HR is written a day late
				sample("resting_heart_rate", d, 7, 58+float64((i*2)%5), "Test Apple Watch")
			}
			if back >= 2 && back%4 != 3 { // HRV: starts two days ago, with holes
				sample("hrv_sdnn", d, 3, 52+float64((i*5)%11), "Test Apple Watch")
			}
		}
		sample("resting_heart_rate", dayAt(1), 8, 70, "Test Apple Watch") // yesterday is a high one
		// Weight: a few weigh-ins, the newest one nine days ago.
		for _, back := range []int{9, 12, 16, 21} {
			sample("body_mass", dayAt(back), 8, 72.4+float64(back%3)*0.1, "manual")
		}
	}

	// One night ending today: core, REM, deep and awake, plus iPhone in-bed.
	night := dayAt(1).Unix() // yesterday 00:00 UTC
	stage("core", night+23*3600, night+27*3600+3000)
	stage("rem", night+27*3600+3000, night+28*3600+1800)
	stage("deep", night+28*3600+1800, night+30*3600)
	stage("awake", night+30*3600, night+30*3600+900)
	exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES ('in_bed', ?, ?, 0, 'Test iPhone')`, night+22*3600+1800, night+31*3600)

	if scenario != "sleep-only" {
		for _, w := range []struct {
			back     int
			activity string
			km       float64
			mins     int
			kcal     float64
		}{{3, "running", 5200, 31, 410}, {6, "cycling", 18000, 55, 520}, {9, "running", 8000, 52, 630}} {
			ts := dayAt(w.back).Unix() + 18*3600
			exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
				VALUES (?, ?, ?, ?, ?, ?, 0, 'Test Apple Watch')`, w.activity, ts, ts+int64(w.mins*60), w.mins*60, w.kcal, w.km)
		}
		exec(`INSERT INTO meta (key, value) VALUES ('last_import_summary', ?)`,
			fmt.Sprintf(`{"at": %d, "read": 1275, "inserted": 1230, "duplicates": 40, "skipped": {"unknown_type": 3, "bad_date": 1, "bad_value": 1}}`,
				overviewNow.AddDate(0, 0, -3).Unix()))
	}
	return path
}

// seedFromFixture imports a synthetic export from testdata and moves every
// timestamp forward by whole days so the newest data is on overviewNow's day
// (the fixtures are dated 2024). Same trick as the importer's other tests.
func seedFromFixture(t *testing.T, path, fixture string) string {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := importer.Import(conn, "../../testdata/"+fixture+"/export.xml", importer.Options{}); err != nil {
		t.Fatal(err)
	}
	var newest int64
	if err := conn.QueryRow(`SELECT max(end_ts + tz_offset) FROM samples`).Scan(&newest); err != nil {
		t.Fatal(err)
	}
	const day = 86400
	shift := (overviewNow.Unix()/day - newest/day) * day
	for _, q := range []string{
		`UPDATE samples SET start_ts = start_ts + ?1, end_ts = end_ts + ?1`,
		`UPDATE sleep_stages SET start_ts = start_ts + ?1, end_ts = end_ts + ?1`,
		`UPDATE workouts SET start_ts = start_ts + ?1, end_ts = end_ts + ?1`,
	} {
		if _, err := conn.Exec(q, shift); err != nil {
			t.Fatal(err)
		}
	}
	// The import time is "now" for real; pin it to a day before overviewNow.
	if _, err := conn.Exec(`UPDATE meta SET value = json_set(value, '$.at', ?) WHERE key = 'last_import_summary'`,
		overviewNow.AddDate(0, 0, -1).Unix()); err != nil {
		t.Fatal(err)
	}
	return path
}

func overviewReport(t *testing.T, scenario string) *stats.Overview {
	t.Helper()
	conn, err := db.Open(seedOverview(t, scenario))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	o, err := stats.BuildOverview(t.Context(), conn, overviewNow, stats.DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	return o
}

var testView = overviewView{dbName: "demo.db", now: overviewNow}

var (
	uni80   = termInfo{width: 80, unicode: true}
	ascii80 = termInfo{width: 80}
)

func overviewText(t *testing.T, scenario string, ti termInfo, v overviewView) string {
	t.Helper()
	return renderOverview(overviewReport(t, scenario), ti, withDB(v, "demo.db", overviewNow))
}

// emptyDB returns a --db flag pointing at a database that exists but has no
// rows. `query` never creates a database, so tests that look for "nothing was
// stored" need one up front.
func emptyDB(t *testing.T) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	return path
}

// run executes the root command with the given args and returns what it
// wrote to stdout and stderr, plus the error (if any).
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

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

// seedDays writes one reading per day for metric, one value per day starting
// at first (a "YYYY-MM-DD" date), at 12:00 UTC from a synthetic source.
func seedDays(t *testing.T, path, metric, first string, values ...float64) {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start, err := time.Parse("2006-01-02", first)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range values {
		ts := start.AddDate(0, 0, i).Add(12 * time.Hour).Unix()
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, '', 0, 'Test Band')`, metric, ts, ts+60, v); err != nil {
			t.Fatal(err)
		}
	}
}

// fixClock makes "now" a fixed time for the rest of the test.
func fixClock(t *testing.T, now time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })
}

// todayPage renders the page for a scenario at a fixed clock, as the command does.
func todayPage(t *testing.T, path, date string, ti termInfo, view overviewView) string {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r, err := buildToday(t.Context(), conn, date)
	if err != nil {
		t.Fatal(err)
	}
	view.now, view.oneDay = overviewNow.Add(24*time.Hour), true // the clock is the day after the data
	return renderToday(r, ti, view)
}

// day returns midnight UTC of a "YYYY-MM-DD" day as Unix seconds. All seed
// rows use offset 0 so local day == UTC day and the numbers are easy to read.
func day(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return tm.Unix()
}

// seedDB builds a synthetic database: 30 days (2026-10-10 .. 2026-11-08) of
// steps that cycle through 6000..9000 (one gap on 2026-11-01), 2026-11-08
// is a big day, resting heart rate only on the last 3 days (too few for a
// baseline), one night of sleep and one workout on 2026-11-08.
func seedDB(t *testing.T) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	sample := func(metric, d string, hour int, value float64, unit, source string) {
		ts := day(t, d) + int64(hour)*3600
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, ?, 0, ?)`, metric, ts, ts+60, value, unit, source); err != nil {
			t.Fatal(err)
		}
	}
	start := day(t, "2026-10-10")
	for i := 0; i < 30; i++ {
		d := time.Unix(start, 0).UTC().AddDate(0, 0, i).Format("2006-01-02")
		if d == "2026-11-01" {
			continue
		}
		steps := 6000 + float64(i%7)*500
		if d == "2026-11-08" {
			steps = 12345
		}
		sample("step_count", d, 12, steps, "count", "Test Apple Watch")
		sample("step_count", d, 13, 99999, "count", "Test iPhone") // loses to the Watch
		sample("heart_rate", d, 9, 60+float64(i%5), "bpm", "Test Apple Watch")
	}
	for _, d := range []string{"2026-11-06", "2026-11-07", "2026-11-08"} {
		sample("resting_heart_rate", d, 7, 58, "bpm", "Test Apple Watch")
	}
	sample("body_mass", "2026-11-08", 8, 72.46, "kg", "manual")

	stage := func(stage string, startTS, endTS int64) {
		if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
			VALUES (?, ?, ?, 0, 'Test Apple Watch')`, stage, startTS, endTS); err != nil {
			t.Fatal(err)
		}
	}
	d7 := day(t, "2026-11-07")
	stage("core", d7+23*3600, d7+27*3600+1800) // 4h30
	stage("deep", d7+27*3600+1800, d7+29*3600) // 1h30
	stage("rem", d7+29*3600, d7+30*3600)       // 1h
	stage("awake", d7+30*3600, d7+30*3600+900) // 15m

	ts := day(t, "2026-11-08") + 18*3600
	if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
		VALUES ('running', ?, ?, 2700, 412.4, 6200, 0, 'Test Apple Watch')`, ts, ts+2700); err != nil {
		t.Fatal(err)
	}
	return path
}

var workoutsNow = time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)

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
