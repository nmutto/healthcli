package cli

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

var (
	uni80   = termInfo{width: 80, unicode: true}
	ascii80 = termInfo{width: 80}
)

func overviewText(t *testing.T, scenario string, ti termInfo, v overviewView) string {
	t.Helper()
	return renderOverview(overviewReport(t, scenario), ti, withDB(v, "demo.db", overviewNow))
}

// (e) the header says how many nights the usual really has; (f) the header
// midpoint is the table's midpoint; (m) words, not "7 n" or "2x"; (n) the
// hypnogram axis is a clock (00 01 02).
func TestSleepWordsAndNumbersAgree(t *testing.T) {
	got := overviewText(t, "import:small", uni80, overviewView{})
	if !strings.Contains(got, "VS 13-NIGHT USUAL") || strings.Contains(got, "VS 28-NIGHT USUAL") {
		t.Errorf("header must say the real number of nights:\n%s", got)
	}
	for _, bad := range []string{" n)", " n:", "2x", "1x", "0x"} {
		if strings.Contains(got, bad) {
			t.Errorf("abbreviation %q left in:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "7 nights") || !strings.Contains(got, "awakenings") {
		t.Errorf("want 7 nights and awakenings:\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^ +00 +01 +02`).MatchString(got) {
		t.Errorf("hour axis should be 00 01 02:\n%s", got)
	}

	// The unspecified night prints its midpoint in the bar caption too.
	lines := renderSleepFor(t, "unspecified", uni80, 80, true, "")
	text := strings.Join(lines, "\n")
	head := regexp.MustCompile(`midpoint (\d\d:\d\d)`).FindStringSubmatch(text)
	row := regexp.MustCompile(`Midpoint +(\d\d:\d\d)`).FindStringSubmatch(text)
	if head == nil || row == nil || head[1] != row[1] {
		t.Errorf("header midpoint %v differs from the table's %v:\n%s", head, row, text)
	}
}

// (k) The SLEEP subtitle is shortened, never cut with an ellipsis.
func TestSleepHeaderIsNeverCut(t *testing.T) {
	r := &stats.SleepReport{Kind: "stages", AgeDays: 12, Last: stats.SleepNight{
		Night: "2026-10-27", Source: "Alexandra's Very Long Sleep Tracker Pro", InBedSource: "Another Rather Long Source Name"}}
	for _, width := range []int{50, 60, 80, 100} {
		for _, uni := range []bool{true, false} {
			g := glyphsFor(uni)
			h := stripANSI(sleepHeader(r, g, width, style{}))
			if strings.Contains(h, "…") || strings.Contains(h, "...") || runeLen(h) > width {
				t.Errorf("width %d: %q", width, h)
			}
		}
	}
	if h := sleepHeader(r, unicodeGlyphs, 80, style{}); !strings.Contains(h, "Wed 28 Oct") && !strings.Contains(h, "Tue 27 Oct") {
		t.Errorf("the date must survive: %q", h)
	}
}

// (g) ASCII: a rest day and the lowest level must be different glyphs.
func TestASCIIRestDayDiffersFromLowestLevel(t *testing.T) {
	g := glyphsFor(false)
	if restGlyph(g) == g.shade(1, false) || restGlyph(g) == g.gap {
		t.Errorf("rest %q, lowest %q, gap %q must all differ", restGlyph(g), g.shade(1, false), g.gap)
	}
}

// (i) 0 awake time against a usual of 0 is "typical", not a blank.
func TestAwakeZeroIsTypical(t *testing.T) {
	zero := 0.0
	var awake sleepStat
	for _, s := range sleepStats {
		if s.name == "Awake" {
			awake = s
		}
	}
	v, ok := compareSleep(awake, &zero, &zero, unicodeGlyphs)
	if !ok || v.verdict != "typical" || !strings.Contains(v.text, "typical") {
		t.Errorf("got %+v %v", v, ok)
	}
}

// (l) --category scopes the headline, the legend and the empty headers.
func TestCategoryScopesHeadlineAndLegend(t *testing.T) {
	all := overviewText(t, "import:small", uni80, overviewView{})
	vitals := overviewText(t, "import:small", uni80, overviewView{categories: []string{"vitals"}})
	if !strings.Contains(all, "3 better, 3 worse") || strings.Contains(vitals, "3 better, 3 worse") {
		t.Errorf("headline must count only the shown categories:\n%s", vitals)
	}
	if strings.Contains(vitals, "slept") || strings.Contains(vitals, "Walking asymmetry") {
		t.Errorf("sleep and other categories leaked into --category vitals:\n%s", vitals)
	}
	for _, cat := range []string{"sleep", "workouts"} {
		out := overviewText(t, "import:small", uni80, overviewView{categories: []string{cat}})
		// (The sleep table has a legend of its own; the metric one must be gone.)
		for _, bad := range []string{"METRIC", "better,", "typical vs", "LATEST"} {
			if strings.Contains(out, bad) {
				t.Errorf("--category %s must print no metric header, legend or metric headline (%q):\n%s", cat, bad, out)
			}
		}
	}
	// A category with nothing in it prints no header, legend or headline.
	none := overviewText(t, "steps-only", uni80, overviewView{categories: []string{"body"}})
	for _, bad := range []string{"METRIC", "green better", "typical", "better"} {
		if strings.Contains(none, bad) {
			t.Errorf("empty --category body printed %q:\n%s", bad, none)
		}
	}
}

// (o) one band style for every section; the legend words are the same
// everywhere.
func TestOneBandStyle(t *testing.T) {
	for _, c := range []struct {
		ti   termInfo
		band string
	}{{uni80, "━━ "}, {ascii80, "== "}} {
		got := overviewText(t, "import:small", c.ti, overviewView{full: true})
		for _, title := range []string{"SLEEP", "WORKOUTS", "CALENDAR", "DATA", "VITALS"} {
			if !strings.Contains(got, " "+c.band+title+" ") {
				t.Errorf("%s section should be a band (%q%s):\n%s", title, c.band, title, got)
			}
		}
		if strings.Count(got, "~ higher/lower, no good direction") != 2 {
			t.Errorf("both legends should say '~ higher/lower, no good direction':\n%s", got)
		}
		if strings.Contains(got, "cyan") || strings.Contains(got, "cyan neutral") || strings.Contains(got, "per night") {
			t.Errorf("old wording left in:\n%s", got)
		}
	}
}

// (p) --compact keeps a one-character flag in the margin.
func TestCompactKeepsOneCharacterFlag(t *testing.T) {
	got := overviewText(t, "import:small", uni80, overviewView{compact: true})
	if !strings.Contains(got, "\n! Resting HR") || !strings.Contains(got, "\n+ Walking heart rate") || !strings.Contains(got, "\n~ Respiratory rate") {
		t.Errorf("compact rows need the ! + ~ flag:\n%s", got)
	}
}

// A heatmap legend that is too wide wraps onto a second line instead of
// being cut or overflowing.
func TestHeatmapLegendWraps(t *testing.T) {
	h := overviewReport(t, "import:small").Heatmap
	for _, width := range []int{50, 60} {
		lines := renderHeatmap(h, termInfo{width: width, unicode: true})
		for _, l := range lines {
			if runeLen(stripANSI(l)) > width {
				t.Errorf("width %d: %q", width, l)
			}
		}
		if text := strings.Join(lines, "\n"); !strings.Contains(text, "today") || strings.Contains(text, "…") {
			t.Errorf("legend lost or cut:\n%s", text)
		}
	}
}

var _ = time.Second

// ASCII stage glyphs are distinct from each other and from the rules.
func TestASCIIStageGlyphsAreDistinct(t *testing.T) {
	g := glyphsFor(false)
	used := map[rune]string{g.rule: "rule", g.bandRule: "band rule"}
	for stage, r := range g.stages {
		if other, dup := used[r]; dup {
			t.Errorf("stage %s uses %q, same as %s", stage, r, other)
		}
		used[r] = stage
	}
}
