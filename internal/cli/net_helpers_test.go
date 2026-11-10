package cli

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
)

// The "net_" goldens are a safety net for refactors: every command is run
// end to end through the cobra tree with a fixed clock and a fixed
// environment, and its whole output (stdout, error, stderr) is saved. A
// change in any word of any command then shows up as a failing test.
//
// Run through run(), the output is never a terminal, so the text is ASCII.
// Unicode screens are covered by the cases that call the render functions
// with a termInfo.

// netCase is one command run.
type netCase struct {
	name  string                    // golden file: net_<name>.txt (or .json when the name ends in _json)
	seed  func(t *testing.T) string // makes the database
	now   time.Time                 // the fixed clock
	cols  string                    // $COLUMNS ("" = unset: width 80)
	force bool                      // FORCE_COLOR=1
	env   map[string]string         // any other variable
	args  []string                  // arguments after --db <path>
}

// netOutput runs c and returns everything it printed.
func netOutput(t *testing.T, c netCase) string {
	t.Helper()
	for _, k := range []string{"COLUMNS", "FORCE_COLOR", "NO_COLOR", "TERM", "LANG", "LC_ALL", "LC_CTYPE"} {
		t.Setenv(k, "")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLUMNS", c.cols)
	if c.force {
		t.Setenv("FORCE_COLOR", "1")
	}
	for k, v := range c.env {
		t.Setenv(k, v)
	}
	if !c.now.IsZero() {
		fixClock(t, c.now)
	}
	args := c.args
	if c.seed != nil {
		args = append([]string{"--db", c.seed(t)}, args...)
	}
	out, errOut, err := run(t, args...)
	return netText(out, errOut, err)
}

// netText joins what a run printed into one readable golden.
func netText(out, errOut string, err error) string {
	var b strings.Builder
	b.WriteString(out)
	if err != nil {
		fmt.Fprintf(&b, "--- error ---\n%v\n", err)
	}
	if errOut != "" {
		fmt.Fprintf(&b, "--- stderr ---\n%s", errOut)
	}
	return b.String()
}

// netGoldenFile names the golden: *_json cases are .json files.
func netGoldenFile(name string) string {
	if strings.HasSuffix(name, "_json") {
		return "net_" + strings.TrimSuffix(name, "_json") + ".json"
	}
	return "net_" + name + ".txt"
}

// runNetCases runs every case as a subtest and compares it with its golden.
func runNetCases(t *testing.T, cases []netCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := netOutput(t, c)
			checkViewGolden(t, netGoldenFile(c.name), got)
			if !c.force && strings.Contains(got, "\x1b") {
				t.Error("escape codes without colour being asked for")
			}
		})
	}
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
