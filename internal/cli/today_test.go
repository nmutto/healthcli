package cli

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

// The long label is shown in full when it fits and shortened (never cut) when not.
func TestTodayLabelColumn(t *testing.T) {
	path := seedOverview(t, "import:small")
	for _, width := range []int{80, 100, 200} {
		got := todayPage(t, path, "2026-11-08", termInfo{width: width, unicode: true}, overviewView{})
		if !strings.Contains(got, "Walking + running distance") {
			t.Errorf("width %d: label should be shown in full:\n%s", width, got)
		}
	}
	for _, width := range []int{60} {
		got := todayPage(t, path, "2026-11-08", termInfo{width: width, unicode: true}, overviewView{})
		if strings.Contains(got, "Walking + running") || strings.Contains(got, "Walk + run dis…") {
			t.Errorf("width %d: want the short label, not a cut one:\n%s", width, got)
		}
		if !strings.Contains(got, "Walk + run distance") {
			t.Errorf("width %d: missing short label:\n%s", width, got)
		}
	}
}

func TestTodayFallbackLineIsWrappedNotCut(t *testing.T) {
	got := todayPage(t, seedOverview(t, "stale"), "2026-11-08", termInfo{width: 50}, overviewView{})
	flat := strings.Join(strings.Fields(got), " ") // the note may be wrapped over lines
	if !strings.Contains(flat, "No data for 2026-11-08; showing 2026-10-27, the latest day with data") || strings.Contains(got, "…") {
		t.Errorf("fallback note cut or missing:\n%s", got)
	}
	for _, want := range []string{} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Sleep's usual is the nights BEFORE the shown night, in `today` and `sleep` alike.
func TestTodaySleepUsualExcludesThatNight(t *testing.T) {
	conn, err := db.Open(seedOverview(t, "import:small"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d, err := stats.BuildDay(t.Context(), conn, "2026-11-08")
	if err != nil || d.Sleep == nil {
		t.Fatalf("no sleep: %v", err)
	}
	all, err := stats.BuildSleepReport(t.Context(), conn, time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC), stats.SleepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Sleep.Usual.Nights != all.Usual.Nights {
		t.Errorf("today's usual has %d nights, sleep's has %d", d.Sleep.Usual.Nights, all.Usual.Nights)
	}
}

// --category narrows the footer count and the JSON alike.
func TestTodayCategoryScopesFooterAndJSON(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "import:small")
	full := todayPage(t, path, "2026-11-08", termInfo{width: 80}, overviewView{})
	vitals, _, err := run(t, "--db", path, "today", "--category", "vitals")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(\d+) tracked metrics? ha`)
	if a, b := re.FindStringSubmatch(full), re.FindStringSubmatch(vitals); a == nil || b == nil || a[1] == b[1] {
		t.Errorf("footer count should shrink with --category: %v vs %v", a, b)
	}
	out, _, err := run(t, "--db", path, "today", "--category", "vitals", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Metrics []struct{ Category string }
		Sleep   *struct{}
		Without []string `json:"tracked_without_reading"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Metrics) == 0 || r.Sleep != nil {
		t.Errorf("want vitals only and no sleep: %+v", r)
	}
	for _, m := range r.Metrics {
		if m.Category != "vitals" {
			t.Errorf("category %q in a vitals-only list", m.Category)
		}
	}
	if len(r.Without) != strings.Count(vitals, "") && !strings.Contains(vitals, "tracked metric") {
		t.Errorf("footer and list disagree: %v\n%s", r.Without, vitals)
	}
}

// An overflowed daily sum is a reading (huge-number format), and a 0 m
// distance is not drawn as "0.0 km".
func TestTodayOverflowAndZeroDistance(t *testing.T) {
	fixClock(t, overviewNow)
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC).Unix()
	for _, h := range []int64{0, 1} {
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES ('step_count', ?, ?, 1.7e308, 'count', 0, 'Test Phone')`, ts+h*3600, ts+h*3600+60); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
		VALUES ('yoga', ?, ?, 1800, 120, 0, 0, 'Test Watch')`, ts+7200, ts+9000); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "today", "--date", "2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Steps") || strings.Contains(out, "no reading on this day") {
		t.Errorf("overflowed steps should be a reading:\n%s", out)
	}
	if strings.Contains(out, "0.0 km") {
		t.Errorf("zero distance should not be shown:\n%s", out)
	}
	js, _, err := run(t, "--db", path, "today", "--date", "2026-11-08", "--json")
	if err != nil || !json.Valid([]byte(js)) {
		t.Errorf("JSON must stay valid with an overflowed value: %v\n%s", err, js)
	}
}

func TestTodayIndexLineAndWorkoutsSeparator(t *testing.T) {
	got := todayPage(t, seedOverview(t, "full"), "2026-11-05", termInfo{width: 80, unicode: true}, overviewView{})
	if !strings.Contains(got, "VITALS 2 !1 · ACTIVITY 2 !1 · WORKOUTS") {
		t.Errorf("index line missing:\n%s", got)
	}
	if !strings.Contains(got, "1 session  31m  410 kcal  5.2 km") {
		t.Errorf("workouts band should use the overview's separator:\n%s", got)
	}
}

// The command reference example is the golden it claims to be (trailing spaces aside).
func TestReadmeTodayExampleMatchesGolden(t *testing.T) {
	readme, err := os.ReadFile("../../docs/commands.md")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/golden/today_apple_80.txt")
	if err != nil {
		t.Fatal(err)
	}
	const head = "$ healthcli --db demo.db today --date 2026-11-08\n"
	text := string(readme)
	i := strings.Index(text, head)
	if i < 0 {
		t.Fatal("command reference today example not found")
	}
	block := text[i+len(head):]
	block = block[:strings.Index(block, "\n```")]
	norm := func(s string) string {
		lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " ")
		}
		return strings.Join(lines, "\n")
	}
	if norm(block) != norm(string(golden)) {
		t.Error("command reference today example differs from testdata/golden/today_apple_80.txt")
	}
}

// The long label shows in full wherever it fits, in overview and today, and is
// shortened (never cut) where it does not.
func TestLongLabelOverviewAndToday(t *testing.T) {
	for _, width := range []int{60, 80, 100} {
		ti := termInfo{width: width, unicode: true}
		for name, text := range map[string]string{
			"overview": renderOverview(overviewReport(t, "import:small"), ti, testView),
			"today":    todayPage(t, seedOverview(t, "import:small"), "2026-11-08", ti, overviewView{}),
		} {
			full := strings.Contains(text, "Walking + running distance")
			short := strings.Contains(text, "Walk + run distance")
			if strings.Contains(text, "Walking + running d…") || full == short {
				t.Errorf("%s at %d: want the full or the short label, never a cut one", name, width)
			}
			if width >= 80 && !full {
				t.Errorf("%s at %d: full label should fit", name, width)
			}
		}
	}
}

// A baseline mean of 0 has no percentage: delta_pct is null, the text says
// n/a, and a jump from 0 to 30 is still "better".
func TestTodayZeroBaseline(t *testing.T) {
	fixClock(t, time.Date(2024, 12, 12, 15, 0, 0, 0, time.UTC))
	path := tempDB(t)
	seedDays(t, path, "flights_climbed", "2024-12-01", 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 30)

	out, _, err := run(t, "--db", path, "today", "--date", "2024-12-11", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Metrics []struct {
			Verdict    string `json:"verdict"`
			Comparison struct {
				DeltaPct *float64 `json:"delta_pct"`
			} `json:"comparison"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Metrics) != 1 || rep.Metrics[0].Verdict != "better" {
		t.Fatalf("want one metric with verdict better, got %s", out)
	}
	if rep.Metrics[0].Comparison.DeltaPct != nil {
		t.Errorf("delta_pct = %v, want null", *rep.Metrics[0].Comparison.DeltaPct)
	}

	text, _, err := run(t, "--db", path, "today", "--date", "2024-12-11", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "n/a") {
		t.Errorf("text should say n/a:\n%s", text)
	}
}

// One future-dated sample, workout or sleep row must not pull the fallback
// day into the future; the overview ignores them the same way.
func TestTodayFallbackIgnoresFuture(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC))
	path := tempDB(t)
	seedDays(t, path, "step_count", "2026-10-01", 5000)
	future := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC).Unix()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source) VALUES ('heart_rate', ?1, ?1, 60, '', 0, 'Test Band')`,
		`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, tz_offset, source) VALUES ('Running', ?1, ?1 + 60, 60, 0, 'Test Band')`,
		`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source) VALUES ('core', ?1, ?1 + 3600, 0, 'Test Band')`,
	} {
		if _, err := conn.Exec(q, future); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "today", "--date", "2026-11-08", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Status string `json:"status"`
		Date   string `json:"date"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Status != statusFallback || rep.Date != "2026-10-01" {
		t.Errorf("status %q date %q, want fallback to 2026-10-01", rep.Status, rep.Date)
	}
}

// seedSparseDevice is a device that records only steps, heart rate and weight
// (synthetic): 20 days of steps and heart rate, a weigh-in every 4th day.
func seedSparseDevice(t *testing.T) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	midnight := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	add := func(metric string, back, hour int, v float64) {
		ts := midnight.AddDate(0, 0, -back).Add(time.Duration(hour) * time.Hour).Unix()
		if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
			VALUES (?, ?, ?, ?, '', 0, 'Test Band')`, metric, ts, ts+60, v); err != nil {
			t.Fatal(err)
		}
	}
	for back := 0; back < 20; back++ {
		add("step_count", back, 12, 5000+float64((back*7)%9)*600)
		add("heart_rate", back, 9, 68+float64((back*3)%5))
		if back%4 == 0 {
			add("body_mass", back, 8, 71.8+float64(back%3)*0.2)
		}
	}
	return path
}

func TestTodayGoldens(t *testing.T) {
	apple := seedOverview(t, "import:small")
	full := seedOverview(t, "full")
	u80, a80 := termInfo{width: 80, unicode: true}, termInfo{width: 80}
	cases := []struct {
		name, path, date string
		ti               termInfo
		view             overviewView
	}{
		{"today_apple_80.txt", apple, "2026-11-08", u80, overviewView{}},
		{"today_apple_60.txt", apple, "2026-11-08", termInfo{width: 60, unicode: true}, overviewView{}},
		{"today_apple_50.txt", apple, "2026-11-08", termInfo{width: 50, unicode: true}, overviewView{}},
		{"today_apple_100.txt", apple, "2026-11-08", termInfo{width: 100, unicode: true}, overviewView{}},
		{"today_apple_ascii.txt", apple, "2026-11-08", a80, overviewView{}},
		{"today_apple_color.txt", apple, "2026-11-08", termInfo{width: 80, unicode: true, color: true}, overviewView{}},
		{"today_apple_compact.txt", apple, "2026-11-08", u80, overviewView{compact: true}},
		{"today_apple_vitals_sleep.txt", apple, "2026-11-08", u80, overviewView{categories: []string{"vitals", "sleep"}}},
		{"today_workouts_only.txt", full, "2026-11-05", u80, overviewView{categories: []string{"workouts"}}},
		{"today_sparse_device.txt", seedSparseDevice(t), "2026-11-08", u80, overviewView{}},
		{"today_sparse_device_60_ascii.txt", seedSparseDevice(t), "2026-11-06", termInfo{width: 60}, overviewView{}},
		{"today_sleep_only.txt", seedOverview(t, "sleep-only"), "2026-11-08", u80, overviewView{}},
		{"today_workouts_day.txt", full, "2026-11-05", u80, overviewView{}},
		{"today_workouts_day_60.txt", full, "2026-11-05", termInfo{width: 60, unicode: true}, overviewView{}},
		{"today_full_day.txt", full, "2026-11-08", u80, overviewView{}},
		{"today_fallback.txt", seedOverview(t, "stale"), "2026-11-08", u80, overviewView{}},
		{"today_empty.txt", seedOverview(t, "empty"), "2026-11-08", u80, overviewView{}},
		{"today_empty_60_ascii.txt", seedOverview(t, "empty"), "2026-11-08", termInfo{width: 60}, overviewView{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := todayPage(t, c.path, c.date, c.ti, c.view)
			checkViewGolden(t, c.name, got)
			for _, line := range strings.Split(stripANSI(got), "\n") {
				if n := runeLen(line); n > c.ti.width {
					t.Errorf("line is %d wide (> %d): %q", n, c.ti.width, line)
				}
				if !c.ti.unicode && strings.ContainsFunc(line, func(r rune) bool { return r > 127 }) {
					t.Errorf("non-ASCII in ASCII output: %q", line)
				}
			}
			if !c.ti.color && strings.Contains(got, "\x1b") {
				t.Error("escape codes without colour")
			}
		})
	}
}

// A day shows only the metrics it has; the rest are counted, never dashed.
func TestTodayHidesMetricsWithoutReading(t *testing.T) {
	got := todayPage(t, seedSparseDevice(t), "2026-11-06", termInfo{width: 80}, overviewView{})
	if strings.Contains(got, "Weight") { // weigh-ins are 0, 4, 8.. days back: none on 2026-11-06 (2 back)
		t.Errorf("weight has no reading that day:\n%s", got)
	}
	if !strings.Contains(got, "1 tracked metric has no reading on this day") {
		t.Errorf("missing footer:\n%s", got)
	}
	if strings.Contains(got, "no data") {
		t.Errorf("no dash rows expected:\n%s", got)
	}
}

func TestTodayJSONAdditiveFields(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "import:small")
	out, _, err := run(t, "--db", path, "today", "--date", "2026-11-08", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	// Old contract, then the additions.
	for _, k := range []string{"status", "requested_date", "date", "metrics", "sleep", "workouts", "categories", "tracked_without_reading", "sleep_summary"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing top-level %q", k)
		}
	}
	var metrics []map[string]any
	if err := json.Unmarshal(raw["metrics"], &metrics); err != nil || len(metrics) == 0 {
		t.Fatalf("metrics: %v %v", metrics, err)
	}
	for _, m := range metrics {
		for _, k := range []string{"metric", "label", "unit", "value", "baseline", "comparison", "category", "polarity", "state", "verdict"} {
			if _, ok := m[k]; !ok {
				t.Errorf("%v: missing %q", m["metric"], k)
			}
		}
		if m["value"] == nil {
			t.Errorf("%v: value must be set (metrics without a reading are left out)", m["metric"])
		}
	}
	for _, k := range []string{"categories", "tracked_without_reading"} {
		if string(raw[k]) == "null" {
			t.Errorf("%s is null", k)
		}
	}
}

func TestTodayEmptyJSONHasNoNewNulls(t *testing.T) {
	fixClock(t, overviewNow)
	out, _, err := run(t, "--db", emptyDB(t), "today", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"categories": []`, `"tracked_without_reading": []`, `"metrics": []`, `"workouts": []`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

func TestTodayFlagsAndErrors(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "import:small")
	if _, _, err := run(t, "--db", path, "today", "--category", "bogus"); err == nil || !strings.Contains(err.Error(), "valid categories") {
		t.Errorf("want the category list, got %v", err)
	}
	out, _, err := run(t, "--db", path, "today", "--compact", "--no-color", "--ascii", "--category", "vitals")
	if err != nil || !strings.Contains(out, "VITALS") || strings.Contains(out, "ACTIVITY") {
		t.Errorf("category/compact: %v\n%s", err, out)
	}
}

// today --json carries the same range object as the overview JSON, and only
// for metrics that have a reference range.
func TestTodayJSONMetricRange(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "import:small")
	type metric struct {
		Metric string `json:"metric"`
		Range  *struct {
			Low      float64 `json:"low"`
			High     float64 `json:"high"`
			Source   string  `json:"source"`
			Position string  `json:"position"`
		} `json:"range"`
	}
	rangeByMetric := func(args ...string) map[string]*metric {
		t.Helper()
		out, _, err := run(t, append([]string{"--db", path}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var rep struct {
			Metrics []*metric `json:"metrics"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatal(err)
		}
		got := map[string]*metric{}
		for _, m := range rep.Metrics {
			got[m.Metric] = m
		}
		return got
	}
	day := rangeByMetric("today", "--date", "2026-11-08", "--json")
	over := rangeByMetric("overview", "--json")
	checked := 0
	for name, m := range day {
		o := over[name]
		if o == nil || o.Range == nil {
			if m.Range != nil {
				t.Errorf("%s: range in today but not in overview", name)
			}
			continue
		}
		checked++
		if m.Range == nil || *m.Range != *o.Range {
			t.Errorf("%s: today range %+v, overview range %+v", name, m.Range, o.Range)
		}
	}
	if checked == 0 {
		t.Error("no metric with a range was compared")
	}
}

func TestTodayGoldenText(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	out, _, err := run(t, "--db", seedDB(t), "today", "--date", "2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	checkViewGolden(t, "today.txt", out)
}

func TestTodayGoldenJSON(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	out, _, err := run(t, "--db", seedDB(t), "today", "--date", "2026-11-08", "--json")
	if err != nil {
		t.Fatal(err)
	}
	checkViewGolden(t, "today.json", out)
}

func TestTodayFallsBackToLatestDay(t *testing.T) {
	fixClock(t, time.Date(2026, 12, 5, 9, 0, 0, 0, time.UTC))
	out, _, err := run(t, "--db", seedDB(t), "today", "--date", "2026-12-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No data for 2026-12-01; showing 2026-11-08, the latest day with data", "Sunday 2026-11-08"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestTodayEmptyDatabaseHint(t *testing.T) {
	out, _, err := run(t, "--db", emptyDB(t), "today")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"healthcli import apple <export.zip>", "healthcli log"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	jsonOut, _, err := run(t, "--db", emptyDB(t), "today", "--json")
	if err != nil || !strings.Contains(jsonOut, `"status": "no_data"`) {
		t.Errorf("want no_data status, got %q, %v", jsonOut, err)
	}
}

func TestTodayDefaultsToTodayAndRejectsBadDate(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC))

	out, _, err := run(t, "--db", seedDB(t), "today")
	if err != nil || !strings.HasPrefix(out, " Sunday 2026-11-08\n") {
		t.Errorf("default date: %q, %v", out, err)
	}
	if _, _, err := run(t, "--db", tempDB(t), "today", "--date", "11/08/2026"); err == nil {
		t.Error("expected an error for a malformed date")
	}
	if _, _, err := run(t, "--db", tempDB(t), "today", "--date", "2026-11-09"); err == nil || !strings.Contains(err.Error(), "in the future") {
		t.Errorf("expected a friendly future-date error, got %v", err)
	}
}

// An evening nap that ends after 18:00 belongs to tomorrow's sleep night; the
// fallback must not offer that future day (sleep and overview say no data).
func TestTodayFallbackNeverShowsAFutureDay(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 10, 18, 10, 0, 0, time.UTC))
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 10, 16, 30, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 11, 10, 18, 5, 0, 0, time.UTC).Unix()
	if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES ('asleep_unspecified', ?, ?, 0, 'Test Apple Watch')`, start, end); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "today")
	if err != nil || strings.Contains(out, "showing 2026-11-11") {
		t.Errorf("fallback offered a future day: %v\n%s", err, out)
	}
	out, _, err = run(t, "--db", path, "today", "--json")
	if err != nil || !strings.Contains(out, `"status": "no_data"`) {
		t.Errorf("want no_data: %v\n%s", err, out)
	}
}

// The same evening nap must not hide a real night before it.
func TestTodayFallbackKeepsOlderNightBesideFutureNap(t *testing.T) {
	fixClock(t, time.Date(2026, 11, 10, 18, 10, 0, 0, time.UTC))
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, span := range [][2]time.Time{
		{time.Date(2026, 11, 9, 23, 0, 0, 0, time.UTC), time.Date(2026, 11, 10, 6, 30, 0, 0, time.UTC)},   // last night
		{time.Date(2026, 11, 10, 16, 30, 0, 0, time.UTC), time.Date(2026, 11, 10, 18, 5, 0, 0, time.UTC)}, // evening nap
	} {
		if _, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
			VALUES ('asleep_unspecified', ?, ?, 0, 'Test Apple Watch')`, span[0].Unix(), span[1].Unix()); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "today", "--json")
	if err != nil || strings.Contains(out, "2026-11-11") {
		t.Errorf("a future day leaked: %v\n%s", err, out)
	}
}

// A number is never cut: at width 50 the value column loses its unit first.
func TestTodayNeverCutsDigitsOfABigNumber(t *testing.T) {
	t.Setenv("COLUMNS", "50")
	fixClock(t, time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC))
	path := seedDB(t)
	// `log` now refuses an absurd value, so put it in the way an odd export would.
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := day(t, "2026-11-09") + 12*3600
	if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES ('step_count', ?, ?, 99999999999, 'count', 0, 'manual')`, ts, ts+60); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	out, _, err := run(t, "--db", path, "today", "--date", "2026-11-09")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "99,999,999,999") {
		t.Errorf("want the whole number in:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 50 {
			t.Errorf("%d columns: %q", len([]rune(line)), line)
		}
	}
}

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

func TestDateAndDaysValidation(t *testing.T) {
	db := emptyDB(t)
	bad := [][]string{
		{"today", "--date", "2026-13-01"},
		{"today", "--date", "2026-02-30"},
		{"today", "--date", "2026-1-5"},
		{"today", "--date", "2026-11-08T10:00:00Z"},
		{"today", "--date", " 2026-11-08"},
		{"trend", "steps", "--date", "tomorrow"},
		{"trend", "steps", "--days", "0"},
		{"trend", "steps", "--days", "-5"},
		{"trend", "steps", "--days", "3651"},
		{"trend", "steps", "--days", "99999999999999999999"},
		{"trend", "steps", "--days", "abc"},
		{"trend", "steps", "--date", "9999-12-31"}, // used to loop forever
		{"today", "--date", "0001-01-01"},
		{"today", "--date", "1899-12-31"},
		{"today", "--date", "2999-12-31", "--json"}, // the future: nothing to show
	}
	for _, args := range bad {
		if _, _, err := run(t, append([]string{"--db", db}, args...)...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	good := [][]string{
		{"today", "--date", "2024-02-29"},
		{"trend", "steps", "--days", "1"},
		{"trend", "steps", "--days", "3650"},
		{"trend", "sleep", "--days", "3650", "--json"},
		{"trend", "steps", "--date", "1900-01-01", "--days", "3"},
		{"trend", "steps", "--date", "2999-12-31", "--days", "3650"},
	}
	for _, args := range good {
		if _, _, err := run(t, append([]string{"--db", db}, args...)...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestJSONIsStableAndNoANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	path := seedDB(t)
	for _, args := range [][]string{
		{"today", "--date", "2026-11-08", "--json"},
		{"trend", "steps", "--date", "2026-11-08", "--json"},
		{"trend", "sleep", "--date", "2026-11-08", "--json"},
		{"today", "--date", "2026-11-08"},
		{"trend", "hr", "--date", "2026-11-08", "-v"},
	} {
		a, _, err := run(t, append([]string{"--db", path}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		b, _, _ := run(t, append([]string{"--db", path}, args...)...)
		if a != b {
			t.Errorf("%v: two runs differ", args)
		}
		if strings.Contains(a, "\x1b") {
			t.Errorf("%v: escape codes in non-terminal output", args)
		}
		if strings.Contains(strings.Join(args, " "), "--json") && !json.Valid([]byte(a)) {
			t.Errorf("%v: invalid JSON", args)
		}
	}
}
