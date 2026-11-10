package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
)

// fixClock makes "now" a fixed time for the rest of the test.
func fixClock(t *testing.T, now time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })
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
	out, _, err := run(t, "--db", tempDB(t), "today", "--json")
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
