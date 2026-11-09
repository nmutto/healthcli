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

// QA regressions for "healthcli today" and the sleep section it shares with
// overview.

// A stat label must always be followed by a space, at every width.
func TestSleepTableKeepsASpaceAfterLabels(t *testing.T) {
	glued := regexp.MustCompile(`(Efficiency|Sleep debt|In bed|Midpoint)[0-9]`)
	for _, width := range []int{50, 55, 60, 80} {
		ti := termInfo{width: width, unicode: true}
		sleep := strings.Join(renderSleepFor(t, "rich", ti, width, true, ""), "\n")
		overview := renderOverview(overviewReport(t, "import:small"), ti, testView)
		for name, text := range map[string]string{"sleep": sleep, "overview": overview, "today": todayPage(t, seedOverview(t, "import:small"), "2026-11-08", ti, overviewView{})} {
			if m := glued.FindString(text); m != "" {
				t.Errorf("%s at %d columns: label glued to its value (%q)", name, width, m)
			}
		}
	}
}

// The "stages not recorded" note is never cut with an ellipsis, with or
// without colour; it shortens to "(no stages)" only when it cannot fit.
func TestSleepUnspecifiedNote(t *testing.T) {
	for _, color := range []bool{false, true} {
		for _, width := range []int{50, 60, 80, 100, 200} {
			ti := termInfo{width: width, unicode: true, color: color}
			text := stripANSI(strings.Join(renderSleepFor(t, "unspecified", ti, width, false, ""), "\n"))
			if strings.Contains(text, "…") {
				t.Errorf("width %d color %v: cut with an ellipsis:\n%s", width, color, text)
			}
			wantFull := width >= 80
			if wantFull && !strings.Contains(text, "(stages not recorded)") {
				t.Errorf("width %d color %v: want the full note:\n%s", width, color, text)
			}
			if !strings.Contains(text, "(stages not recorded)") && !strings.Contains(text, "(no stages)") {
				t.Errorf("width %d color %v: no note:\n%s", width, color, text)
			}
		}
	}
}

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

// Sleep's usual is the nights BEFORE the shown night.
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
	if d.Sleep.Usual.Nights != all.Usual.Nights-1 {
		t.Errorf("usual has %d nights, want %d (one less than %d)", d.Sleep.Usual.Nights, all.Usual.Nights-1, all.Usual.Nights)
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

// The README example is the golden it claims to be (trailing spaces aside).
func TestReadmeTodayExampleMatchesGolden(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
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
		t.Fatal("README today example not found")
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
		t.Error("README today example differs from testdata/golden/today_apple_80.txt")
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
