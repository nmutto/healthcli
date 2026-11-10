package cli

import (
	"github.com/nmutto/healthcli/internal/db"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

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

// Summary lines drop whole items and say "+N more"; they never end mid-word.
func TestSummaryLinesDropWholeItems(t *testing.T) {
	counts := map[string]int64{stats.ClassWatch: 64, stats.ClassIPhone: 22, stats.ClassManual: 1, stats.ClassOther: 13}
	got := listFit("sources: ", sourceParts(counts), 0, 48)
	if got != "sources: Watch 64%, iPhone 22%, +2 more" {
		t.Errorf("sources: %q", got)
	}
	gaps := []stats.Gap{{Label: "Weight", Missing: 10, Span: 16}, {Label: "Body fat", Missing: 8, Span: 13}, {Label: "BMI", Missing: 8, Span: 13}, {Label: "HRV", Missing: 1, Span: 28}}
	if got := gapsLine(gaps, 48); got != "days missing: Weight 10 of 16, +3 more" {
		t.Errorf("gaps narrow: %q", got)
	}
	if got := gapsLine(gaps, 100); got != "days missing: Weight 10 of 16, Body fat 8 of 13, BMI 8 of 13, +1 more" {
		t.Errorf("gaps wide: %q", got)
	}
}

func TestNotEnoughHistoryHeadlineIsNotCut(t *testing.T) {
	t.Setenv("COLUMNS", "50")
	fixClock(t, time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	// One weigh-in: a fresh value with too little history for a baseline.
	path := tempDB(t)
	if _, _, err := run(t, "--db", path, "log", "weight", "72", "--at", "2026-11-08 08:00"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "--db", path, "overview")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Not enough history") {
		t.Fatalf("no headline in:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Not enough history") && (strings.HasSuffix(line, "...") || len([]rune(line)) > 50) {
			t.Errorf("headline line: %q", line)
		}
	}
}
